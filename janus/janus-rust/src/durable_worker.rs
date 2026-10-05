//! Reducer-owned worker execution for durable `SpacetimeDB` jobs.

use crate::module_bindings::Job;
use crate::spacetime::SpacetimeDbError;
use crate::spacetime_client::DeploymentRuntimeWrite;
use crate::spacetime_runtime::SpacetimeRuntime;
use crate::TenantId;

/// Port used by the worker to claim and settle durable jobs.
pub trait DurableJobPort {
    /// Claims the oldest available job for a worker lease.
    ///
    /// # Errors
    ///
    /// Returns the reducer or connection error.
    fn claim_job(
        &self,
        worker_id: u64,
        now: u64,
        lease_seconds: u64,
    ) -> Result<Option<Job>, SpacetimeDbError>;

    /// Completes a job while its lease is still valid.
    ///
    /// # Errors
    ///
    /// Returns the reducer or connection error.
    fn complete_job(&self, id: u64, worker_id: u64, now: u64) -> Result<(), SpacetimeDbError>;

    /// Fails a job and lets the reducer retry or dead-letter it.
    ///
    /// # Errors
    ///
    /// Returns the reducer or connection error.
    fn fail_job(&self, id: u64, worker_id: u64, now: u64) -> Result<(), SpacetimeDbError>;
}

/// Bounded provider boundary for one claimed job.
pub trait DurableJobHandler {
    /// Executes the external work represented by a claimed durable job.
    ///
    /// The handler must be bounded by its caller's timeout and must not mutate
    /// durable lifecycle state directly; settlement belongs to reducers.
    ///
    /// # Errors
    ///
    /// Returns a bounded provider failure that should be settled through the
    /// reducer-owned retry policy.
    fn execute(&mut self, job: &Job) -> Result<(), String>;
}

/// Deterministic local build handler for the control-plane verification path.
///
/// This handler advances a durable build through its generation-fenced
/// lifecycle and records the owning operation. A production provider can
/// replace it while retaining the same reducer-owned worker contract.
pub struct DeterministicBuildJobHandler<'a> {
    /// Durable `SpacetimeDB` runtime used for lifecycle transitions.
    pub runtime: &'a SpacetimeRuntime,
    /// Timestamp written to the operation transition.
    pub now: u64,
}

impl DurableJobHandler for DeterministicBuildJobHandler<'_> {
    fn execute(&mut self, job: &Job) -> Result<(), String> {
        let tenant = TenantId(u32::try_from(job.tenant_id).map_err(|_| "tenant overflow")?);
        if job.kind == "deployment.create" {
            return self.execute_deployment(job, tenant);
        }
        if job.kind != "build.enqueue" {
            return Err("unsupported durable job kind".to_owned());
        }
        let builds = self
            .runtime
            .build_components()
            .map_err(|_| "build projection unavailable".to_owned())?;
        let build = builds
            .into_iter()
            .find(|(id, build)| id.0 == job.target_id && build.tenant == tenant)
            .map(|(_, build)| build)
            .ok_or_else(|| "durable build target not found".to_owned())?;
        if build.state == crate::BuildState::Pending {
            self.runtime
                .transition_build(job.target_id, tenant, job.generation, "running")
                .map_err(|_| "build start rejected".to_owned())?;
        }
        self.runtime
            .transition_build(job.target_id, tenant, job.generation, "succeeded")
            .map_err(|_| "build completion rejected".to_owned())?;
        self.runtime
            .transition_operation(job.id, tenant, "succeeded", b"completed", self.now)
            .map_err(|_| "operation completion rejected".to_owned())
    }
}

impl DeterministicBuildJobHandler<'_> {
    fn execute_deployment(&self, job: &Job, tenant: TenantId) -> Result<(), String> {
        let deployments = self
            .runtime
            .deployment_components()
            .map_err(|_| "deployment projection unavailable".to_owned())?;
        let deployment = deployments
            .into_iter()
            .find(|(id, deployment)| id.0 == job.target_id && deployment.tenant == tenant)
            .map(|(_, deployment)| deployment)
            .ok_or_else(|| "durable deployment target not found".to_owned())?;
        if deployment.state == crate::DeploymentState::Pending {
            self.runtime
                .transition_deployment(job.target_id, tenant, job.generation, "starting")
                .map_err(|_| "deployment start rejected".to_owned())?;
        }
        self.runtime
            .update_deployment_runtime(
                job.target_id,
                tenant,
                job.generation,
                "running",
                DeploymentRuntimeWrite {
                    runtime_id: format!("local-runtime-{}", job.target_id),
                    runtime_mode: "local".to_owned(),
                    runtime_endpoint: format!("http://127.0.0.1/deployments/{}", job.target_id),
                    runtime_status: "running".to_owned(),
                },
            )
            .map_err(|_| "deployment runtime start rejected".to_owned())?;
        self.runtime
            .transition_operation(job.id, tenant, "succeeded", b"running", self.now)
            .map_err(|_| "operation completion rejected".to_owned())
    }
}

/// Result of one worker poll.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum WorkerPoll {
    /// No pending job was available.
    Idle,
    /// A job completed through the reducer boundary.
    Completed {
        /// Completed durable job identity.
        id: u64,
    },
    /// A job failed and was returned to the reducer-owned retry policy.
    Failed {
        /// Failed durable job identity.
        id: u64,
    },
}

/// Runs one reducer-owned worker poll.
///
/// # Errors
///
/// Returns a persistence error when claiming or settling the lease fails.
pub fn poll_once<P, H>(
    port: &P,
    handler: &mut H,
    worker_id: u64,
    now: u64,
    lease_seconds: u64,
) -> Result<WorkerPoll, SpacetimeDbError>
where
    P: DurableJobPort,
    H: DurableJobHandler,
{
    let Some(job) = port.claim_job(worker_id, now, lease_seconds)? else {
        return Ok(WorkerPoll::Idle);
    };
    let id = job.id;
    if handler.execute(&job).is_ok() {
        port.complete_job(id, worker_id, now)?;
        Ok(WorkerPoll::Completed { id })
    } else {
        port.fail_job(id, worker_id, now)?;
        Ok(WorkerPoll::Failed { id })
    }
}

#[cfg(test)]
mod tests {
    use super::{poll_once, DurableJobHandler, DurableJobPort, WorkerPoll};
    use crate::module_bindings::Job;
    use crate::spacetime::SpacetimeDbError;

    #[derive(Default)]
    struct FakePort {
        job: Option<Job>,
    }

    impl DurableJobPort for FakePort {
        fn claim_job(
            &self,
            _worker_id: u64,
            _now: u64,
            _lease_seconds: u64,
        ) -> Result<Option<Job>, SpacetimeDbError> {
            Ok(self.job.clone())
        }

        fn complete_job(
            &self,
            _id: u64,
            _worker_id: u64,
            _now: u64,
        ) -> Result<(), SpacetimeDbError> {
            Ok(())
        }

        fn fail_job(&self, _id: u64, _worker_id: u64, _now: u64) -> Result<(), SpacetimeDbError> {
            Ok(())
        }
    }

    struct Handler {
        succeed: bool,
    }

    impl DurableJobHandler for Handler {
        fn execute(&mut self, _job: &Job) -> Result<(), String> {
            self.succeed
                .then_some(())
                .ok_or_else(|| "provider rejected job".to_owned())
        }
    }

    fn job() -> Job {
        Job {
            id: 7,
            kind: "build.enqueue".to_owned(),
            target_id: 11,
            tenant_id: 11,
            generation: 1,
            state: "claimed".to_owned(),
            attempts: 1,
            max_attempts: 3,
            lease_until: 100,
            worker_id: 9,
        }
    }

    #[test]
    fn successful_handler_settles_claimed_job() {
        let port = FakePort { job: Some(job()) };
        let mut handler = Handler { succeed: true };
        assert_eq!(
            poll_once(&port, &mut handler, 9, 10, 30),
            Ok(WorkerPoll::Completed { id: 7 })
        );
    }

    #[test]
    fn rejected_handler_uses_reducer_failure_path() {
        let port = FakePort { job: Some(job()) };
        let mut handler = Handler { succeed: false };
        assert_eq!(
            poll_once(&port, &mut handler, 9, 10, 30),
            Ok(WorkerPoll::Failed { id: 7 })
        );
    }
}
