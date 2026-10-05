//! SpacetimeDB-shaped persistence boundary.
//!
//! The domain kernel does not depend on a database SDK. This module defines
//! the reducer contract that a generated `SpacetimeDB` client/module will
//! implement, plus a bounded in-memory implementation for local replay and
//! contract tests. File stores remain separate local recovery adapters.

use crate::{Job, JobQueue, QueueError, TenantId, CURRENT_PROTOCOL_VERSION};

/// Maximum event kind length accepted by the persistence boundary.
pub const MAX_SPACETIME_EVENT_KIND_BYTES: usize = 128;

/// Maximum serialized payload accepted by one persistence reducer call.
pub const MAX_SPACETIME_PAYLOAD_BYTES: usize = 64 * 1024;

/// A durable event row written by the `append_event` reducer.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct SpacetimeEventRow {
    /// Tenant owning the event.
    pub tenant: TenantId,
    /// Idempotency identity for the originating command.
    pub command_id: u64,
    /// Stable event kind.
    pub kind: String,
    /// Bounded deterministic event payload.
    pub payload: Vec<u8>,
    /// Monotonic module-assigned sequence.
    pub sequence: u64,
}

/// Result returned by a reducer-shaped mutation.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ReducerReceipt {
    /// Protocol version accepted by the module.
    pub protocol_version: u16,
    /// Durable sequence or job identity affected by the reducer.
    pub identity: u64,
    /// Whether this call reused an existing idempotent record.
    pub idempotent_replay: bool,
}

/// Persistence boundary failure.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum SpacetimeDbError {
    /// A reducer supplied an unsupported protocol version.
    UnsupportedProtocol,
    /// A bounded row or collection is full.
    Capacity,
    /// A required field is empty or exceeds its limit.
    InvalidInput,
    /// The requested identity was not found.
    NotFound,
    /// The operation conflicts with an existing idempotency record.
    IdempotencyConflict,
    /// The queue reducer rejected a lease or lifecycle transition.
    Queue(QueueError),
    /// The remote database connection is unavailable.
    Unavailable,
}

impl From<QueueError> for SpacetimeDbError {
    fn from(error: QueueError) -> Self {
        Self::Queue(error)
    }
}

/// The reducer operations required by the Rust control plane.
pub trait SpacetimeDbPersistence {
    /// Append one event using command identity as an idempotency key.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn append_event(
        &mut self,
        tenant: TenantId,
        command_id: u64,
        kind: &str,
        payload: &[u8],
    ) -> Result<ReducerReceipt, SpacetimeDbError>;

    /// Enqueue one durable job, treating an identical job identity as replay.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn enqueue_job(&mut self, job: Job) -> Result<ReducerReceipt, SpacetimeDbError>;

    /// Claim the oldest available job with a bounded lease.
    fn claim_job(&mut self, worker: u64, now: u64, lease_seconds: u64) -> Option<Job>;

    /// Complete a job through the durable queue reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn complete_job(&mut self, id: u64, now: u64) -> Result<(), SpacetimeDbError>;

    /// Store the latest bounded authentication snapshot.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn save_auth_snapshot(&mut self, version: u16, snapshot: &[u8])
        -> Result<(), SpacetimeDbError>;

    /// Load the latest authentication snapshot, if one exists.
    fn load_auth_snapshot(&self) -> Option<(u16, Vec<u8>)>;
}

/// Bounded local reducer implementation used before the generated
/// `SpacetimeDB` client is connected.
///
/// Its operations intentionally mirror the table/reducer contract rather than
/// exposing storage internals to the ECS.
pub struct LocalSpacetimeDb<const MAX_EVENTS: usize, const MAX_JOBS: usize> {
    events: Vec<SpacetimeEventRow>,
    jobs: JobQueue<MAX_JOBS>,
    auth_snapshot: Option<(u16, Vec<u8>)>,
}

impl<const MAX_EVENTS: usize, const MAX_JOBS: usize> LocalSpacetimeDb<MAX_EVENTS, MAX_JOBS> {
    /// Creates an empty local reducer store.
    #[must_use]
    pub fn new() -> Self {
        Self {
            events: Vec::with_capacity(MAX_EVENTS),
            jobs: JobQueue::new(),
            auth_snapshot: None,
        }
    }

    /// Returns events in the same order a module subscription would observe.
    #[must_use]
    pub fn events(&self) -> &[SpacetimeEventRow] {
        &self.events
    }
}

impl<const MAX_EVENTS: usize, const MAX_JOBS: usize> Default
    for LocalSpacetimeDb<MAX_EVENTS, MAX_JOBS>
{
    fn default() -> Self {
        Self::new()
    }
}

impl<const MAX_EVENTS: usize, const MAX_JOBS: usize> SpacetimeDbPersistence
    for LocalSpacetimeDb<MAX_EVENTS, MAX_JOBS>
{
    fn append_event(
        &mut self,
        tenant: TenantId,
        command_id: u64,
        kind: &str,
        payload: &[u8],
    ) -> Result<ReducerReceipt, SpacetimeDbError> {
        if kind.trim().is_empty()
            || kind.len() > MAX_SPACETIME_EVENT_KIND_BYTES
            || payload.len() > MAX_SPACETIME_PAYLOAD_BYTES
        {
            return Err(SpacetimeDbError::InvalidInput);
        }
        if let Some(existing) = self
            .events
            .iter()
            .find(|event| event.command_id == command_id)
        {
            if existing.tenant == tenant && existing.kind == kind && existing.payload == payload {
                return Ok(ReducerReceipt {
                    protocol_version: CURRENT_PROTOCOL_VERSION,
                    identity: existing.sequence,
                    idempotent_replay: true,
                });
            }
            return Err(SpacetimeDbError::IdempotencyConflict);
        }
        if self.events.len() >= MAX_EVENTS {
            return Err(SpacetimeDbError::Capacity);
        }
        let sequence = u64::try_from(self.events.len()).unwrap_or(u64::MAX);
        self.events.push(SpacetimeEventRow {
            tenant,
            command_id,
            kind: kind.trim().to_owned(),
            payload: payload.to_owned(),
            sequence,
        });
        Ok(ReducerReceipt {
            protocol_version: CURRENT_PROTOCOL_VERSION,
            identity: sequence,
            idempotent_replay: false,
        })
    }

    fn enqueue_job(&mut self, job: Job) -> Result<ReducerReceipt, SpacetimeDbError> {
        let existing = self.jobs.get(job.id).cloned();
        if let Some(existing) = existing {
            if existing == job {
                return Ok(ReducerReceipt {
                    protocol_version: CURRENT_PROTOCOL_VERSION,
                    identity: job.id,
                    idempotent_replay: true,
                });
            }
            return Err(SpacetimeDbError::IdempotencyConflict);
        }
        self.jobs.enqueue(job.id, job.kind, job.max_attempts)?;
        Ok(ReducerReceipt {
            protocol_version: CURRENT_PROTOCOL_VERSION,
            identity: job.id,
            idempotent_replay: false,
        })
    }

    fn claim_job(&mut self, worker: u64, now: u64, lease_seconds: u64) -> Option<Job> {
        self.jobs.claim(worker, now, lease_seconds)
    }

    fn complete_job(&mut self, id: u64, now: u64) -> Result<(), SpacetimeDbError> {
        self.jobs.complete(id, now).map_err(Into::into)
    }

    fn save_auth_snapshot(
        &mut self,
        version: u16,
        snapshot: &[u8],
    ) -> Result<(), SpacetimeDbError> {
        if version == 0 || snapshot.len() > MAX_SPACETIME_PAYLOAD_BYTES {
            return Err(SpacetimeDbError::InvalidInput);
        }
        self.auth_snapshot = Some((version, snapshot.to_owned()));
        Ok(())
    }

    fn load_auth_snapshot(&self) -> Option<(u16, Vec<u8>)> {
        self.auth_snapshot.clone()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn job(id: u64) -> Job {
        Job {
            id,
            kind: "build".to_owned(),
            state: crate::JobState::Pending,
            attempts: 0,
            max_attempts: 2,
            lease_until: None,
            tenant: TenantId(7),
            generation: crate::Generation(1),
        }
    }

    #[test]
    fn event_reducer_is_idempotent_and_conflict_safe() {
        let mut db = LocalSpacetimeDb::<2, 1>::new();
        let first = db
            .append_event(TenantId(7), 42, "build.created", b"x")
            .unwrap();
        let replay = db
            .append_event(TenantId(7), 42, "build.created", b"x")
            .unwrap();
        assert!(!first.idempotent_replay);
        assert!(replay.idempotent_replay);
        assert_eq!(db.events().len(), 1);
        assert_eq!(
            db.append_event(TenantId(7), 42, "build.failed", b"x"),
            Err(SpacetimeDbError::IdempotencyConflict)
        );
    }

    #[test]
    fn queue_reducers_preserve_lease_semantics() {
        let mut db = LocalSpacetimeDb::<1, 2>::new();
        db.enqueue_job(job(9)).unwrap();
        let claimed = db.claim_job(3, 100, 10).unwrap();
        assert_eq!(claimed.lease_until, Some(110));
        assert_eq!(db.complete_job(9, 109), Ok(()));
        assert!(db.claim_job(3, 109, 10).is_none());
    }

    #[test]
    fn auth_snapshot_is_bounded_and_versioned() {
        let mut db = LocalSpacetimeDb::<1, 1>::new();
        assert_eq!(
            db.save_auth_snapshot(0, b"x"),
            Err(SpacetimeDbError::InvalidInput)
        );
        db.save_auth_snapshot(1, b"snapshot").unwrap();
        assert_eq!(db.load_auth_snapshot(), Some((1, b"snapshot".to_vec())));
    }
}
