//! Bounded queue semantics for claims, leases, retries, and dead letters.

/// Maximum job kind length accepted by the local queue contract.
pub const MAX_JOB_KIND_BYTES: usize = 128;

/// Lifecycle state of one queued job.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum JobState {
    /// Waiting for a worker claim.
    Pending,
    /// Claimed until the lease timestamp.
    Claimed,
    /// Completed successfully.
    Completed,
    /// No further retries are allowed.
    DeadLettered,
}

/// Work item stored by the bounded queue.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Job {
    /// Idempotent job identity.
    pub id: u64,
    /// Dispatch kind.
    pub kind: String,
    /// Current state.
    pub state: JobState,
    /// Number of claims attempted.
    pub attempts: u16,
    /// Maximum allowed claims.
    pub max_attempts: u16,
    /// Worker lease expiry in Unix seconds, if claimed.
    pub lease_until: Option<u64>,
    /// Tenant boundary for the queued operation.
    pub tenant: crate::TenantId,
    /// Generation fence carried into worker recovery.
    pub generation: crate::Generation,
}

/// Queue operation failure.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum QueueError {
    /// The job kind is empty or too long.
    InvalidKind,
    /// The queue has no free slot.
    Capacity,
    /// The requested job does not exist.
    NotFound,
    /// The job is not in a state that accepts this operation.
    InvalidState,
    /// The worker lease does not own the job.
    LeaseMismatch,
}

/// Bounded, idempotent queue with explicit worker leases.
pub struct JobQueue<const MAX_JOBS: usize> {
    jobs: Vec<Option<Job>>,
}

impl<const MAX_JOBS: usize> JobQueue<MAX_JOBS> {
    /// Creates an empty queue.
    #[must_use]
    pub fn new() -> Self {
        Self {
            jobs: vec![None; MAX_JOBS],
        }
    }

    /// Enqueues one job, treating an existing identity as idempotent success.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn enqueue(
        &mut self,
        id: u64,
        kind: impl Into<String>,
        max_attempts: u16,
    ) -> Result<(), QueueError> {
        self.enqueue_with_metadata(
            id,
            kind,
            max_attempts,
            crate::TenantId(0),
            crate::Generation(0),
        )
    }

    /// Enqueues one job with the tenant and generation fence required by recovery.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn enqueue_with_metadata(
        &mut self,
        id: u64,
        kind: impl Into<String>,
        max_attempts: u16,
        tenant: crate::TenantId,
        generation: crate::Generation,
    ) -> Result<(), QueueError> {
        let kind = kind.into().trim().to_owned();
        if kind.is_empty() || kind.len() > MAX_JOB_KIND_BYTES || max_attempts == 0 {
            return Err(QueueError::InvalidKind);
        }
        if self.jobs.iter().flatten().any(|job| job.id == id) {
            return Ok(());
        }
        let slot = self
            .jobs
            .iter_mut()
            .find(|entry| entry.is_none())
            .ok_or(QueueError::Capacity)?;
        *slot = Some(Job {
            id,
            kind,
            state: JobState::Pending,
            attempts: 0,
            max_attempts,
            lease_until: None,
            tenant,
            generation,
        });
        Ok(())
    }

    /// Claims the oldest pending job or reclaims an expired lease.
    pub fn claim(&mut self, _worker: u64, now: u64, lease_seconds: u64) -> Option<Job> {
        let index = self.jobs.iter().position(|entry| {
            entry.as_ref().is_some_and(|job| {
                job.state == JobState::Pending
                    || (job.state == JobState::Claimed
                        && job.lease_until.is_some_and(|until| until <= now))
            })
        })?;
        let job = self.jobs[index].as_mut()?;
        if job.attempts >= job.max_attempts {
            job.state = JobState::DeadLettered;
            job.lease_until = None;
            return None;
        }
        job.state = JobState::Claimed;
        job.attempts = job.attempts.saturating_add(1);
        job.lease_until = Some(now.saturating_add(lease_seconds));
        Some(job.clone())
    }

    /// Marks a claimed job complete when its lease has not expired.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn complete(&mut self, id: u64, now: u64) -> Result<(), QueueError> {
        let job = self.find_mut(id)?;
        if job.state != JobState::Claimed || job.lease_until.is_some_and(|until| until <= now) {
            return Err(QueueError::LeaseMismatch);
        }
        job.state = JobState::Completed;
        job.lease_until = None;
        Ok(())
    }

    /// Fails a claimed job, returning it to pending or dead-lettering it.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn fail(&mut self, id: u64, now: u64) -> Result<JobState, QueueError> {
        let job = self.find_mut(id)?;
        if job.state != JobState::Claimed || job.lease_until.is_some_and(|until| until <= now) {
            return Err(QueueError::LeaseMismatch);
        }
        job.lease_until = None;
        job.state = if job.attempts >= job.max_attempts {
            JobState::DeadLettered
        } else {
            JobState::Pending
        };
        Ok(job.state)
    }

    /// Returns a stored job by identity.
    #[must_use]
    pub fn get(&self, id: u64) -> Option<&Job> {
        self.jobs.iter().flatten().find(|job| job.id == id)
    }

    /// Captures jobs in deterministic slot order for durable recovery.
    #[must_use]
    pub fn snapshot(&self) -> Vec<Job> {
        self.jobs.iter().flatten().cloned().collect()
    }

    /// Restores jobs after validating lease, retry, identity, and capacity invariants.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn restore(&mut self, jobs: Vec<Job>) -> Result<(), QueueError> {
        if jobs.len() > MAX_JOBS {
            return Err(QueueError::Capacity);
        }
        let mut candidate = Self::new();
        for job in jobs {
            if job.kind.trim().is_empty()
                || job.kind.len() > MAX_JOB_KIND_BYTES
                || job.max_attempts == 0
                || job.attempts > job.max_attempts
                || candidate
                    .jobs
                    .iter()
                    .flatten()
                    .any(|existing| existing.id == job.id)
                || (job.state == JobState::Claimed) != job.lease_until.is_some()
                || (job.state != JobState::Claimed && job.lease_until.is_some())
            {
                return Err(QueueError::InvalidState);
            }
            let slot = candidate
                .jobs
                .iter_mut()
                .find(|entry| entry.is_none())
                .ok_or(QueueError::Capacity)?;
            *slot = Some(job);
        }
        *self = candidate;
        Ok(())
    }

    fn find_mut(&mut self, id: u64) -> Result<&mut Job, QueueError> {
        self.jobs
            .iter_mut()
            .flatten()
            .find(|job| job.id == id)
            .ok_or(QueueError::NotFound)
    }
}

impl<const MAX_JOBS: usize> Default for JobQueue<MAX_JOBS> {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn queue_claims_retries_and_dead_letters_deterministically() {
        let mut queue = JobQueue::<2>::new();
        queue.enqueue(1, "build", 2).unwrap();
        queue.enqueue(1, "build", 2).unwrap();
        let claimed = queue.claim(7, 10, 5).unwrap();
        assert_eq!(claimed.attempts, 1);
        assert_eq!(queue.complete(1, 14), Ok(()));
        assert_eq!(queue.get(1).map(|job| job.state), Some(JobState::Completed));

        queue.enqueue(2, "deploy", 2).unwrap();
        queue.claim(8, 20, 1);
        assert_eq!(queue.fail(2, 20), Ok(JobState::Pending));
        queue.claim(8, 21, 1);
        assert_eq!(queue.fail(2, 21), Ok(JobState::DeadLettered));
    }

    #[test]
    fn expired_leases_cannot_complete_and_queue_capacity_is_bounded() {
        let mut queue = JobQueue::<1>::new();
        queue.enqueue(1, "build", 1).unwrap();
        assert_eq!(queue.enqueue(2, "build", 1), Err(QueueError::Capacity));
        queue.claim(1, 10, 2);
        assert_eq!(queue.complete(1, 12), Err(QueueError::LeaseMismatch));
        assert_eq!(queue.claim(1, 12, 2), None);
        assert_eq!(
            queue.get(1).map(|job| job.state),
            Some(JobState::DeadLettered)
        );
    }
}
