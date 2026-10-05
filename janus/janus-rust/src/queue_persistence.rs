//! Bounded file persistence for queue jobs and worker leases.

use std::fs::{create_dir_all, read, OpenOptions};
use std::io::{self, Write};
use std::path::Path;

use crate::{EntityId, TenantId};
use crate::{
    Generation, Job, JobQueue, JobState, QueueError, RecoveryQueueError, RecoveryQueuePort,
};

/// Maximum encoded queue snapshot size.
pub const MAX_QUEUE_SNAPSHOT_BYTES: usize = 2 * 1024 * 1024;

const QUEUE_MAGIC: &[u8] = b"JNS-QUE2";
const LEGACY_QUEUE_MAGIC: &[u8] = b"JNS-QUEUE";
const BUILD_RECOVERY_TAG: u64 = 0x4000_0000_0000_0000;
const DEPLOYMENT_RECOVERY_TAG: u64 = 0x8000_0000_0000_0000;
const RECOVERY_ENTITY_MASK: u64 = 0x3fff_ffff_ffff_ffff;
const RECOVERY_MAX_ATTEMPTS: u16 = 3;

/// Failure while saving or loading queue state.
#[derive(Debug)]
pub enum QueueStoreError {
    /// The underlying file operation failed.
    Io(io::Error),
    /// The encoded file is malformed or has trailing bytes.
    Corrupt,
    /// The encoded state exceeded bounds.
    Capacity,
    /// The queue invariants rejected decoded state.
    Invalid(QueueError),
}

/// Versioned file-backed queue snapshot store.
pub struct FileQueueStore {
    path: String,
}

impl FileQueueStore {
    /// Creates a queue store at the supplied path.
    pub fn new(path: impl Into<String>) -> Self {
        Self { path: path.into() }
    }

    /// Saves one bounded queue snapshot and syncs it to disk.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn save<const MAX_JOBS: usize>(
        &self,
        queue: &JobQueue<MAX_JOBS>,
    ) -> Result<(), QueueStoreError> {
        let mut encoder = Encoder::new(MAX_QUEUE_SNAPSHOT_BYTES);
        encoder.bytes(QUEUE_MAGIC);
        let jobs = queue.snapshot();
        encoder.u32(u32::try_from(jobs.len()).unwrap_or(u32::MAX));
        for job in jobs {
            encode_job(&mut encoder, &job, true);
        }
        if encoder.overflowed {
            return Err(QueueStoreError::Capacity);
        }
        let path = Path::new(&self.path);
        if let Some(parent) = path.parent() {
            if !parent.as_os_str().is_empty() {
                create_dir_all(parent).map_err(QueueStoreError::Io)?;
            }
        }
        let mut file = OpenOptions::new()
            .create(true)
            .truncate(true)
            .write(true)
            .open(path)
            .map_err(QueueStoreError::Io)?;
        file.write_all(&encoder.data).map_err(QueueStoreError::Io)?;
        file.sync_all().map_err(QueueStoreError::Io)
    }

    /// Loads queue state into a bounded queue, if the file exists.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn load<const MAX_JOBS: usize>(
        &self,
        queue: &mut JobQueue<MAX_JOBS>,
    ) -> Result<bool, QueueStoreError> {
        let data = match read(&self.path) {
            Ok(data) => data,
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(false),
            Err(error) => return Err(QueueStoreError::Io(error)),
        };
        if data.len() > MAX_QUEUE_SNAPSHOT_BYTES {
            return Err(QueueStoreError::Capacity);
        }
        let mut decoder = Decoder::new(&data);
        let has_metadata = if decoder.take(QUEUE_MAGIC.len()) == Some(QUEUE_MAGIC.to_vec()) {
            true
        } else {
            decoder = Decoder::new(&data);
            if decoder.take(LEGACY_QUEUE_MAGIC.len()) != Some(LEGACY_QUEUE_MAGIC.to_vec()) {
                return Err(QueueStoreError::Corrupt);
            }
            false
        };
        let count = usize::try_from(decoder.u32().ok_or(QueueStoreError::Corrupt)?)
            .map_err(|_| QueueStoreError::Corrupt)?;
        if count > MAX_JOBS {
            return Err(QueueStoreError::Capacity);
        }
        let mut jobs = Vec::with_capacity(count.min(MAX_QUEUE_SNAPSHOT_BYTES));
        for _ in 0..count {
            jobs.push(decode_job(&mut decoder, has_metadata).ok_or(QueueStoreError::Corrupt)?);
        }
        if !decoder.finished() {
            return Err(QueueStoreError::Corrupt);
        }
        queue.restore(jobs).map_err(QueueStoreError::Invalid)?;
        Ok(true)
    }
}

struct Encoder {
    data: Vec<u8>,
    limit: usize,
    overflowed: bool,
}

/// Bounded, durable queue adapter used by crash recovery reconciliation.
pub struct FileRecoveryQueue<const MAX_JOBS: usize> {
    queue: JobQueue<MAX_JOBS>,
    store: FileQueueStore,
}

impl<const MAX_JOBS: usize> FileRecoveryQueue<MAX_JOBS> {
    /// Opens a file-backed recovery queue and restores its prior snapshot.
    ///
    /// # Errors
    ///
    /// Returns a persistence error when the snapshot cannot be loaded or
    /// violates the bounded queue contract.
    pub fn open(store: FileQueueStore) -> Result<Self, QueueStoreError> {
        let mut queue = JobQueue::new();
        store.load(&mut queue)?;
        Ok(Self { queue, store })
    }

    /// Returns the current deterministic queue snapshot.
    #[must_use]
    pub fn snapshot(&self) -> Vec<Job> {
        self.queue.snapshot()
    }

    fn enqueue_recovery(
        &mut self,
        id: EntityId,
        kind: &'static str,
        job_id: u64,
        tenant: TenantId,
        generation: Generation,
    ) -> Result<(), RecoveryQueueError> {
        if id.0 > RECOVERY_ENTITY_MASK {
            return Err(RecoveryQueueError::Unavailable);
        }
        if let Some(existing) = self.queue.get(job_id) {
            if existing.kind == kind
                && existing.tenant == tenant
                && existing.generation == generation
            {
                return Ok(());
            }
            return Err(RecoveryQueueError::Unavailable);
        }
        let before = self.queue.snapshot();
        self.queue
            .enqueue_with_metadata(job_id, kind, RECOVERY_MAX_ATTEMPTS, tenant, generation)
            .map_err(map_queue_error)?;
        if self.store.save(&self.queue).is_err() {
            if self.queue.restore(before).is_err() {
                return Err(RecoveryQueueError::Unavailable);
            }
            return Err(RecoveryQueueError::Unavailable);
        }
        Ok(())
    }
}

impl<const MAX_JOBS: usize> RecoveryQueuePort for FileRecoveryQueue<MAX_JOBS> {
    fn enqueue_build(
        &mut self,
        build: EntityId,
        tenant: TenantId,
        generation: Generation,
    ) -> Result<(), RecoveryQueueError> {
        self.enqueue_recovery(
            build,
            "build.recover",
            BUILD_RECOVERY_TAG | build.0,
            tenant,
            generation,
        )
    }

    fn enqueue_deployment(
        &mut self,
        deployment: EntityId,
        tenant: TenantId,
        generation: Generation,
    ) -> Result<(), RecoveryQueueError> {
        self.enqueue_recovery(
            deployment,
            "deployment.recover",
            DEPLOYMENT_RECOVERY_TAG | deployment.0,
            tenant,
            generation,
        )
    }
}

const fn map_queue_error(error: QueueError) -> RecoveryQueueError {
    match error {
        QueueError::Capacity => RecoveryQueueError::Capacity,
        QueueError::InvalidKind
        | QueueError::NotFound
        | QueueError::InvalidState
        | QueueError::LeaseMismatch => RecoveryQueueError::Unavailable,
    }
}

impl Encoder {
    fn new(limit: usize) -> Self {
        Self {
            data: Vec::from([]),
            limit,
            overflowed: false,
        }
    }

    fn bytes(&mut self, value: &[u8]) {
        let Some(end) = self.data.len().checked_add(value.len()) else {
            self.overflowed = true;
            return;
        };
        if end > self.limit {
            self.overflowed = true;
            return;
        }
        self.data.extend_from_slice(value);
    }

    fn u8(&mut self, value: u8) {
        self.data.push(value);
    }

    fn u16(&mut self, value: u16) {
        self.data.extend_from_slice(&value.to_le_bytes());
    }

    fn u32(&mut self, value: u32) {
        self.data.extend_from_slice(&value.to_le_bytes());
    }

    fn u64(&mut self, value: u64) {
        self.data.extend_from_slice(&value.to_le_bytes());
    }

    fn string(&mut self, value: &str) {
        self.u32(u32::try_from(value.len()).unwrap_or(u32::MAX));
        self.bytes(value.as_bytes());
    }
}

struct Decoder<'a> {
    data: &'a [u8],
    offset: usize,
}

impl<'a> Decoder<'a> {
    const fn new(data: &'a [u8]) -> Self {
        Self { data, offset: 0 }
    }

    fn take(&mut self, count: usize) -> Option<Vec<u8>> {
        let end = self.offset.checked_add(count)?;
        let value = self.data.get(self.offset..end)?.to_vec();
        self.offset = end;
        Some(value)
    }

    fn u8(&mut self) -> Option<u8> {
        self.take(1)?.first().copied()
    }

    fn u16(&mut self) -> Option<u16> {
        Some(u16::from_le_bytes(self.take(2)?.try_into().ok()?))
    }

    fn u32(&mut self) -> Option<u32> {
        Some(u32::from_le_bytes(self.take(4)?.try_into().ok()?))
    }

    fn u64(&mut self) -> Option<u64> {
        Some(u64::from_le_bytes(self.take(8)?.try_into().ok()?))
    }

    fn string(&mut self) -> Option<String> {
        let length = usize::try_from(self.u32()?).ok()?;
        String::from_utf8(self.take(length)?).ok()
    }

    const fn finished(&self) -> bool {
        self.offset == self.data.len()
    }
}

fn encode_job(encoder: &mut Encoder, job: &Job, include_metadata: bool) {
    encoder.u64(job.id);
    encoder.string(&job.kind);
    encoder.u8(job_state_tag(job.state));
    encoder.u16(job.attempts);
    encoder.u16(job.max_attempts);
    match job.lease_until {
        Some(value) => {
            encoder.u8(1);
            encoder.u64(value);
        }
        None => encoder.u8(0),
    }
    if include_metadata {
        encoder.u32(job.tenant.0);
        encoder.u64(job.generation.0);
    }
}

fn decode_job(decoder: &mut Decoder<'_>, has_metadata: bool) -> Option<Job> {
    Some(Job {
        id: decoder.u64()?,
        kind: decoder.string()?,
        state: decode_job_state(decoder.u8()?)?,
        attempts: decoder.u16()?,
        max_attempts: decoder.u16()?,
        lease_until: match decoder.u8()? {
            0 => None,
            1 => Some(decoder.u64()?),
            _ => return None,
        },
        tenant: if has_metadata {
            TenantId(decoder.u32()?)
        } else {
            TenantId(0)
        },
        generation: if has_metadata {
            Generation(decoder.u64()?)
        } else {
            Generation(0)
        },
    })
}

const fn job_state_tag(state: JobState) -> u8 {
    match state {
        JobState::Pending => 0,
        JobState::Claimed => 1,
        JobState::Completed => 2,
        JobState::DeadLettered => 3,
    }
}

const fn decode_job_state(value: u8) -> Option<JobState> {
    Some(match value {
        0 => JobState::Pending,
        1 => JobState::Claimed,
        2 => JobState::Completed,
        3 => JobState::DeadLettered,
        _ => return None,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn file_queue_store_round_trips_claims_and_retry_state() {
        let path = std::env::temp_dir().join(format!("janus-queue-{}.bin", std::process::id()));
        let _ = std::fs::remove_file(&path);
        let mut source = JobQueue::<2>::new();
        source.enqueue(1, "build", 3).unwrap();
        source.claim(7, 10, 30);
        let store = FileQueueStore::new(path.to_string_lossy().into_owned());
        store.save(&source).unwrap();
        let mut restored = JobQueue::<2>::new();
        assert!(store.load(&mut restored).unwrap());
        assert_eq!(restored.snapshot(), source.snapshot());
        let _ = std::fs::remove_file(path);
    }

    #[test]
    fn file_recovery_queue_persists_generation_fenced_work_idempotently() {
        let path =
            std::env::temp_dir().join(format!("janus-recovery-queue-{}.bin", std::process::id()));
        let _ = std::fs::remove_file(&path);
        let store = FileQueueStore::new(path.to_string_lossy().into_owned());
        let mut queue = FileRecoveryQueue::<2>::open(store).unwrap();
        queue
            .enqueue_build(EntityId(4), TenantId(7), Generation(9))
            .unwrap();
        queue
            .enqueue_build(EntityId(4), TenantId(7), Generation(9))
            .unwrap();
        assert_eq!(queue.snapshot().len(), 1);
        let job = queue.snapshot().pop().unwrap();
        assert_eq!(job.kind, "build.recover");
        assert_eq!(job.tenant, TenantId(7));
        assert_eq!(job.generation, Generation(9));

        let mut reopened =
            FileRecoveryQueue::<2>::open(FileQueueStore::new(path.to_string_lossy().into_owned()))
                .unwrap();
        assert_eq!(reopened.snapshot(), queue.snapshot());
        assert_eq!(
            reopened.enqueue_build(EntityId(4), TenantId(7), Generation(10)),
            Err(RecoveryQueueError::Unavailable)
        );
        let _ = std::fs::remove_file(path);
    }
}
