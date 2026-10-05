//! Generated-client boundary for the `SpacetimeDB` persistence contract.
//!
//! Generated SDK types stay behind this adapter so the ECS remains independent
//! from the `SpacetimeDB` client library.

use crate::CURRENT_PROTOCOL_VERSION;
use crate::{
    Build, Deployment, Job, ReducerReceipt, SpacetimeDbError, SpacetimeDbPersistence, TenantId,
};

use crate::OperationStatus;

/// Complete project payload sent across the generated `SpacetimeDB` seam.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ProjectWrite {
    /// Human-readable project name.
    pub name: String,
    /// Stable tenant-local project slug.
    pub slug: String,
    /// Human-readable project description.
    pub description: String,
    /// Repository provider identifier.
    pub repo_provider: String,
    /// Repository URL.
    pub repository: String,
    /// Repository branch.
    pub branch: String,
    /// Creation timestamp.
    pub created_at: u64,
    /// Last update timestamp.
    pub updated_at: u64,
}

/// Complete build payload sent across the generated `SpacetimeDB` seam.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct BuildWrite {
    /// Source bundle, repository, or upload identity.
    pub source: String,
    /// Project owning the build, when present.
    pub project_id: Option<u64>,
    /// Branch, tag, or upload reference.
    pub source_ref: String,
}

impl BuildWrite {
    /// Projects the authoritative ECS build component into durable fields.
    #[must_use]
    pub fn from_component(build: &Build) -> Self {
        let source_ref = build.source_ref.clone().unwrap_or_default();
        Self {
            source: source_ref.clone(),
            project_id: build.project.map(|project| project.0),
            source_ref,
        }
    }
}

/// Complete deployment payload sent across the generated `SpacetimeDB` seam.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct DeploymentWrite {
    /// Project owning the deployment, when present.
    pub project_id: Option<u64>,
    /// Monotonic project deployment revision.
    pub revision: u64,
    /// Deployment target category.
    pub target_type: String,
    /// Deployment target reference.
    pub target_ref: String,
    /// Preferred runner identity.
    pub preferred_runner: String,
    /// Bounded encoded environment entries.
    pub environment: Vec<u8>,
    /// Stable runtime identity.
    pub runtime_id: String,
    /// Runtime execution mode.
    pub runtime_mode: String,
    /// Runtime HTTP endpoint.
    pub runtime_endpoint: String,
    /// Runtime-specific status.
    pub runtime_status: String,
}

impl DeploymentWrite {
    /// Projects the authoritative ECS deployment component into durable fields.
    ///
    /// # Errors
    ///
    /// Returns a serialization error if the bounded environment cannot be
    /// encoded for the generated reducer payload.
    pub fn try_from_component(deployment: &Deployment) -> Result<Self, serde_json::Error> {
        Ok(Self {
            project_id: deployment.project.map(|project| project.0),
            revision: deployment.revision,
            target_type: deployment.target_type.clone(),
            target_ref: deployment.target_ref.clone(),
            preferred_runner: deployment.preferred_runner.clone(),
            environment: serde_json::to_vec(&deployment.environment)?,
            runtime_id: deployment.runtime_id.clone(),
            runtime_mode: deployment.runtime_mode.clone(),
            runtime_endpoint: deployment.runtime_endpoint.clone(),
            runtime_status: deployment.runtime_status.clone(),
        })
    }
}

/// Runtime projection sent across the generated `SpacetimeDB` seam.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct DeploymentRuntimeWrite {
    /// Stable runtime identity.
    pub runtime_id: String,
    /// Runtime execution mode.
    pub runtime_mode: String,
    /// Runtime HTTP endpoint.
    pub runtime_endpoint: String,
    /// Runtime-specific status.
    pub runtime_status: String,
}

/// Runner registration payload sent across the generated `SpacetimeDB` seam.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RunnerWrite {
    /// Advertised capability names.
    pub capabilities: Vec<String>,
    /// Heartbeat timestamp.
    pub heartbeat_at: u64,
    /// Heartbeat lease duration.
    pub lease_seconds: u64,
}

/// Operation identity and lifecycle payload sent across the generated seam.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct OperationWrite {
    /// Stable operation identifier.
    pub id: String,
    /// Operation kind.
    pub kind: String,
    /// Request correlation identity.
    pub correlation_id: String,
    /// Owning tenant identifier as stored by the operation projection.
    pub tenant_id: String,
    /// Monotonic lifecycle status.
    pub status: OperationStatus,
    /// Creation timestamp.
    pub created_at: u64,
    /// Last transition timestamp.
    pub updated_at: u64,
    /// Successful result, if present.
    pub result: Option<String>,
    /// Failure payload, if present.
    pub failure: Option<String>,
}

/// Reducer calls required from generated `SpacetimeDB` bindings.
pub trait SpacetimeDbReducerClient {
    /// Calls the durable event append reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn reducer_append_event(
        &mut self,
        tenant: TenantId,
        command_id: u64,
        kind: &str,
        payload: &[u8],
    ) -> Result<ReducerReceipt, SpacetimeDbError>;

    /// Calls the durable job enqueue reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn reducer_enqueue_job(&mut self, job: Job) -> Result<ReducerReceipt, SpacetimeDbError>;

    /// Calls the durable job claim reducer.
    fn reducer_claim_job(&mut self, worker: u64, now: u64, lease_seconds: u64) -> Option<Job>;

    /// Calls the durable job completion reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn reducer_complete_job(&mut self, id: u64, now: u64) -> Result<(), SpacetimeDbError>;

    /// Calls the durable job failure/retry reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the worker lease is invalid.
    fn reducer_fail_job(&mut self, id: u64, worker: u64, now: u64) -> Result<(), SpacetimeDbError>;

    /// Calls the durable authentication snapshot reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn reducer_save_auth_snapshot(
        &mut self,
        version: u16,
        snapshot: &[u8],
    ) -> Result<(), SpacetimeDbError>;

    /// Reads the newest durable authentication snapshot.
    fn query_auth_snapshot(&self) -> Option<(u16, Vec<u8>)>;
}

/// Typed project, build, and deployment reducer calls exposed by the
/// generated `SpacetimeDB` client.
pub trait SpacetimeDbDomainReducerClient {
    /// Admits a command through the durable idempotency reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the command conflicts with an existing identity
    /// or violates a bounded field contract.
    fn reducer_admit_command(
        &mut self,
        id: u64,
        tenant: TenantId,
        kind: &str,
        correlation_id: &str,
        payload: &[u8],
        created_at: u64,
    ) -> Result<(), SpacetimeDbError>;

    /// Applies a durable command result transition.
    ///
    /// # Errors
    ///
    /// Returns an error when the transition is stale, terminal, or not owned
    /// by the supplied tenant.
    fn reducer_transition_command(
        &mut self,
        id: u64,
        tenant: TenantId,
        status: &str,
        payload: &[u8],
        updated_at: u64,
    ) -> Result<(), SpacetimeDbError>;

    /// Inserts a pending operation projection.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation identity or bounded fields are invalid.
    fn reducer_create_operation(
        &mut self,
        id: u64,
        tenant: TenantId,
        kind: &str,
        correlation_id: &str,
        created_at: u64,
    ) -> Result<(), SpacetimeDbError>;

    /// Applies a tenant-fenced monotonic operation transition.
    ///
    /// # Errors
    ///
    /// Returns an error when the transition is stale or the payload is invalid.
    fn reducer_transition_operation(
        &mut self,
        id: u64,
        tenant: TenantId,
        status: OperationStatus,
        payload: &[u8],
        updated_at: u64,
    ) -> Result<(), SpacetimeDbError>;

    /// Inserts a pending tenant-owned project.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn reducer_create_project(
        &mut self,
        id: u64,
        tenant: TenantId,
        project: ProjectWrite,
    ) -> Result<(), SpacetimeDbError>;

    /// Applies a retry-safe tenant-owned project update.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn reducer_update_project(
        &mut self,
        id: u64,
        tenant: TenantId,
        project: ProjectWrite,
        status: &str,
    ) -> Result<(), SpacetimeDbError>;

    /// Deletes a tenant-owned project, treating an absent row as success.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn reducer_delete_project(&mut self, id: u64, tenant: TenantId)
        -> Result<(), SpacetimeDbError>;

    /// Inserts a pending build.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn reducer_create_build(
        &mut self,
        id: u64,
        tenant: TenantId,
        build: BuildWrite,
    ) -> Result<(), SpacetimeDbError>;

    /// Applies a generation-fenced build transition.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn reducer_transition_build(
        &mut self,
        id: u64,
        tenant: TenantId,
        generation: u64,
        status: &str,
    ) -> Result<(), SpacetimeDbError>;

    /// Requeues a running build after worker loss.
    ///
    /// # Errors
    ///
    /// Returns an error when the durable row is missing, stale, or not running.
    fn reducer_recover_build(
        &mut self,
        id: u64,
        tenant: TenantId,
        generation: u64,
    ) -> Result<(), SpacetimeDbError>;

    /// Inserts a deployment for a succeeded build.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn reducer_create_deployment(
        &mut self,
        id: u64,
        tenant: TenantId,
        build_id: u64,
        deployment: DeploymentWrite,
    ) -> Result<(), SpacetimeDbError>;

    /// Applies a generation-fenced deployment transition.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn reducer_transition_deployment(
        &mut self,
        id: u64,
        tenant: TenantId,
        generation: u64,
        status: &str,
    ) -> Result<(), SpacetimeDbError>;

    /// Requeues a starting deployment after runtime loss.
    ///
    /// # Errors
    ///
    /// Returns an error when the durable row is missing, stale, or not starting.
    fn reducer_recover_deployment(
        &mut self,
        id: u64,
        tenant: TenantId,
        generation: u64,
    ) -> Result<(), SpacetimeDbError>;

    /// Applies a generation-fenced deployment status and runtime projection.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn reducer_update_deployment_runtime(
        &mut self,
        id: u64,
        tenant: TenantId,
        generation: u64,
        status: &str,
        runtime: DeploymentRuntimeWrite,
    ) -> Result<(), SpacetimeDbError>;

    /// Registers or refreshes a tenant runner.
    ///
    /// # Errors
    ///
    /// Returns an error when the registration violates the bounded contract.
    fn reducer_register_runner(
        &mut self,
        id: u64,
        tenant: TenantId,
        runner: RunnerWrite,
    ) -> Result<(), SpacetimeDbError>;

    /// Claims a tenant runner by capability.
    ///
    /// # Errors
    ///
    /// Returns an error when no eligible runner is available.
    fn reducer_claim_runner(
        &mut self,
        tenant: TenantId,
        capability: &str,
        now: u64,
    ) -> Result<(), SpacetimeDbError>;

    /// Releases a busy tenant runner.
    ///
    /// # Errors
    ///
    /// Returns an error when the runner is not owned or busy.
    fn reducer_release_runner(&mut self, id: u64, tenant: TenantId)
        -> Result<(), SpacetimeDbError>;

    /// Expires runners whose heartbeat leases have elapsed.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer cannot apply the expiry pass.
    fn reducer_expire_runners(&mut self, now: u64) -> Result<(), SpacetimeDbError>;
}

/// Exposes a generated reducer client through the domain persistence port.
pub struct GeneratedSpacetimeDb<C> {
    client: C,
}

impl<C> GeneratedSpacetimeDb<C> {
    /// Wraps one generated binding client.
    pub const fn new(client: C) -> Self {
        Self { client }
    }

    /// Returns mutable access for SDK-specific subscriptions and lifecycle work.
    pub const fn client_mut(&mut self) -> &mut C {
        &mut self.client
    }
}

impl<C: SpacetimeDbReducerClient> SpacetimeDbPersistence for GeneratedSpacetimeDb<C> {
    fn append_event(
        &mut self,
        tenant: TenantId,
        command_id: u64,
        kind: &str,
        payload: &[u8],
    ) -> Result<ReducerReceipt, SpacetimeDbError> {
        self.client
            .reducer_append_event(tenant, command_id, kind, payload)
    }

    fn enqueue_job(&mut self, job: Job) -> Result<ReducerReceipt, SpacetimeDbError> {
        self.client.reducer_enqueue_job(job)
    }

    fn claim_job(&mut self, worker: u64, now: u64, lease_seconds: u64) -> Option<Job> {
        self.client.reducer_claim_job(worker, now, lease_seconds)
    }

    fn complete_job(&mut self, id: u64, now: u64) -> Result<(), SpacetimeDbError> {
        self.client.reducer_complete_job(id, now)
    }

    fn save_auth_snapshot(
        &mut self,
        version: u16,
        snapshot: &[u8],
    ) -> Result<(), SpacetimeDbError> {
        self.client.reducer_save_auth_snapshot(version, snapshot)
    }

    fn load_auth_snapshot(&self) -> Option<(u16, Vec<u8>)> {
        self.client.query_auth_snapshot()
    }
}

impl<C: SpacetimeDbDomainReducerClient> GeneratedSpacetimeDb<C> {
    /// Calls the generated command-admission reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the command identity conflicts or its bounded
    /// fields are invalid.
    pub fn admit_command(
        &mut self,
        id: u64,
        tenant: TenantId,
        kind: &str,
        correlation_id: &str,
        payload: &[u8],
        created_at: u64,
    ) -> Result<(), SpacetimeDbError> {
        self.client
            .reducer_admit_command(id, tenant, kind, correlation_id, payload, created_at)
    }

    /// Calls the generated command-transition reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the transition is stale or invalid.
    pub fn transition_command(
        &mut self,
        id: u64,
        tenant: TenantId,
        status: &str,
        payload: &[u8],
        updated_at: u64,
    ) -> Result<(), SpacetimeDbError> {
        self.client
            .reducer_transition_command(id, tenant, status, payload, updated_at)
    }

    /// Calls the generated operation-create reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation identity or bounded fields are invalid.
    pub fn create_operation(
        &mut self,
        id: u64,
        tenant: TenantId,
        kind: &str,
        correlation_id: &str,
        created_at: u64,
    ) -> Result<(), SpacetimeDbError> {
        self.client
            .reducer_create_operation(id, tenant, kind, correlation_id, created_at)
    }

    /// Calls the generated monotonic operation-transition reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the transition is stale or the payload is invalid.
    pub fn transition_operation(
        &mut self,
        id: u64,
        tenant: TenantId,
        status: OperationStatus,
        payload: &[u8],
        updated_at: u64,
    ) -> Result<(), SpacetimeDbError> {
        self.client
            .reducer_transition_operation(id, tenant, status, payload, updated_at)
    }

    /// Calls the generated project-create reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn create_project(
        &mut self,
        id: u64,
        tenant: TenantId,
        project: ProjectWrite,
    ) -> Result<(), SpacetimeDbError> {
        self.client.reducer_create_project(id, tenant, project)
    }

    /// Calls the generated project-update reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn update_project(
        &mut self,
        id: u64,
        tenant: TenantId,
        project: ProjectWrite,
        status: &str,
    ) -> Result<(), SpacetimeDbError> {
        self.client
            .reducer_update_project(id, tenant, project, status)
    }

    /// Calls the generated project-delete reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn delete_project(&mut self, id: u64, tenant: TenantId) -> Result<(), SpacetimeDbError> {
        self.client.reducer_delete_project(id, tenant)
    }

    /// Calls the generated build-create reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn create_build(
        &mut self,
        id: u64,
        tenant: TenantId,
        build: BuildWrite,
    ) -> Result<(), SpacetimeDbError> {
        self.client.reducer_create_build(id, tenant, build)
    }

    /// Calls the generated generation-fenced build transition reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn transition_build(
        &mut self,
        id: u64,
        tenant: TenantId,
        generation: u64,
        status: &str,
    ) -> Result<(), SpacetimeDbError> {
        self.client
            .reducer_transition_build(id, tenant, generation, status)
    }

    /// Calls the generated worker-loss build recovery reducer.
    ///
    /// # Errors
    ///
    /// Returns the durable reducer failure.
    pub fn recover_build(
        &mut self,
        id: u64,
        tenant: TenantId,
        generation: u64,
    ) -> Result<(), SpacetimeDbError> {
        self.client.reducer_recover_build(id, tenant, generation)
    }

    /// Calls the generated deployment-create reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn create_deployment(
        &mut self,
        id: u64,
        tenant: TenantId,
        build_id: u64,
        deployment: DeploymentWrite,
    ) -> Result<(), SpacetimeDbError> {
        self.client
            .reducer_create_deployment(id, tenant, build_id, deployment)
    }

    /// Calls the generated generation-fenced deployment transition reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn transition_deployment(
        &mut self,
        id: u64,
        tenant: TenantId,
        generation: u64,
        status: &str,
    ) -> Result<(), SpacetimeDbError> {
        self.client
            .reducer_transition_deployment(id, tenant, generation, status)
    }

    /// Calls the generated runtime-loss deployment recovery reducer.
    ///
    /// # Errors
    ///
    /// Returns the durable reducer failure.
    pub fn recover_deployment(
        &mut self,
        id: u64,
        tenant: TenantId,
        generation: u64,
    ) -> Result<(), SpacetimeDbError> {
        self.client
            .reducer_recover_deployment(id, tenant, generation)
    }

    /// Calls the generated deployment runtime projection reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn update_deployment_runtime(
        &mut self,
        id: u64,
        tenant: TenantId,
        generation: u64,
        status: &str,
        runtime: DeploymentRuntimeWrite,
    ) -> Result<(), SpacetimeDbError> {
        self.client
            .reducer_update_deployment_runtime(id, tenant, generation, status, runtime)
    }

    /// Calls the generated runner registration reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the registration violates the bounded contract.
    pub fn register_runner(
        &mut self,
        id: u64,
        tenant: TenantId,
        runner: RunnerWrite,
    ) -> Result<(), SpacetimeDbError> {
        self.client.reducer_register_runner(id, tenant, runner)
    }

    /// Calls the generated runner claim reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when no eligible runner is available.
    pub fn claim_runner(
        &mut self,
        tenant: TenantId,
        capability: &str,
        now: u64,
    ) -> Result<(), SpacetimeDbError> {
        self.client.reducer_claim_runner(tenant, capability, now)
    }

    /// Calls the generated runner release reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the runner is not owned or busy.
    pub fn release_runner(&mut self, id: u64, tenant: TenantId) -> Result<(), SpacetimeDbError> {
        self.client.reducer_release_runner(id, tenant)
    }

    /// Calls the generated runner expiry reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer cannot apply the expiry pass.
    pub fn expire_runners(&mut self, now: u64) -> Result<(), SpacetimeDbError> {
        self.client.reducer_expire_runners(now)
    }
}

impl<C: SpacetimeDbReducerClient> GeneratedSpacetimeDb<C> {
    /// Calls the durable job failure/retry reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the worker lease is invalid.
    pub fn fail_job(&mut self, id: u64, worker: u64, now: u64) -> Result<(), SpacetimeDbError> {
        self.client.reducer_fail_job(id, worker, now)
    }
}

/// Small fake reducer client used by the local adapter contract tests.
#[derive(Default)]
pub struct ContractFakeClient {
    events: Vec<(u64, String, Vec<u8>)>,
    jobs: Vec<Job>,
    auth_snapshot: Option<(u16, Vec<u8>)>,
    domain_calls: Vec<String>,
}

impl SpacetimeDbReducerClient for ContractFakeClient {
    fn reducer_append_event(
        &mut self,
        _tenant: TenantId,
        command_id: u64,
        kind: &str,
        payload: &[u8],
    ) -> Result<ReducerReceipt, SpacetimeDbError> {
        if let Some((existing_id, existing_kind, existing_payload)) =
            self.events.iter().find(|(id, _, _)| *id == command_id)
        {
            if existing_kind == kind && existing_payload == payload {
                return Ok(ReducerReceipt {
                    protocol_version: CURRENT_PROTOCOL_VERSION,
                    identity: *existing_id,
                    idempotent_replay: true,
                });
            }
            return Err(SpacetimeDbError::IdempotencyConflict);
        }
        let identity = u64::try_from(self.events.len()).unwrap_or(u64::MAX);
        self.events
            .push((command_id, kind.to_owned(), payload.to_owned()));
        Ok(ReducerReceipt {
            protocol_version: CURRENT_PROTOCOL_VERSION,
            identity,
            idempotent_replay: false,
        })
    }

    fn reducer_enqueue_job(&mut self, job: Job) -> Result<ReducerReceipt, SpacetimeDbError> {
        if self.jobs.iter().any(|existing| existing.id == job.id) {
            return Ok(ReducerReceipt {
                protocol_version: CURRENT_PROTOCOL_VERSION,
                identity: job.id,
                idempotent_replay: true,
            });
        }
        self.jobs.push(job.clone());
        Ok(ReducerReceipt {
            protocol_version: CURRENT_PROTOCOL_VERSION,
            identity: job.id,
            idempotent_replay: false,
        })
    }

    fn reducer_claim_job(&mut self, _worker: u64, _now: u64, _lease_seconds: u64) -> Option<Job> {
        self.jobs.first().cloned()
    }

    fn reducer_complete_job(&mut self, id: u64, _now: u64) -> Result<(), SpacetimeDbError> {
        let index = self
            .jobs
            .iter()
            .position(|job| job.id == id)
            .ok_or(SpacetimeDbError::NotFound)?;
        self.jobs.remove(index);
        Ok(())
    }

    fn reducer_fail_job(
        &mut self,
        id: u64,
        _worker: u64,
        _now: u64,
    ) -> Result<(), SpacetimeDbError> {
        if self.jobs.iter().any(|job| job.id == id) {
            Ok(())
        } else {
            Err(SpacetimeDbError::NotFound)
        }
    }

    fn reducer_save_auth_snapshot(
        &mut self,
        version: u16,
        snapshot: &[u8],
    ) -> Result<(), SpacetimeDbError> {
        self.auth_snapshot = Some((version, snapshot.to_owned()));
        Ok(())
    }

    fn query_auth_snapshot(&self) -> Option<(u16, Vec<u8>)> {
        self.auth_snapshot.clone()
    }
}

impl SpacetimeDbDomainReducerClient for ContractFakeClient {
    fn reducer_admit_command(
        &mut self,
        id: u64,
        _tenant: TenantId,
        _kind: &str,
        _correlation_id: &str,
        _payload: &[u8],
        _created_at: u64,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push(format!("command.admit:{id}"));
        Ok(())
    }

    fn reducer_transition_command(
        &mut self,
        id: u64,
        _tenant: TenantId,
        _status: &str,
        _payload: &[u8],
        _updated_at: u64,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push(format!("command.transition:{id}"));
        Ok(())
    }

    fn reducer_create_operation(
        &mut self,
        id: u64,
        _tenant: TenantId,
        _kind: &str,
        _correlation_id: &str,
        _created_at: u64,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push(format!("operation.create:{id}"));
        Ok(())
    }

    fn reducer_transition_operation(
        &mut self,
        id: u64,
        _tenant: TenantId,
        _status: OperationStatus,
        _payload: &[u8],
        _updated_at: u64,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push(format!("operation.transition:{id}"));
        Ok(())
    }

    fn reducer_create_project(
        &mut self,
        id: u64,
        _tenant: TenantId,
        _project: ProjectWrite,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push(format!("project.create:{id}"));
        Ok(())
    }

    fn reducer_update_project(
        &mut self,
        id: u64,
        _tenant: TenantId,
        _project: ProjectWrite,
        _status: &str,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push(format!("project.update:{id}"));
        Ok(())
    }

    fn reducer_delete_project(
        &mut self,
        id: u64,
        _tenant: TenantId,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push(format!("project.delete:{id}"));
        Ok(())
    }

    fn reducer_create_build(
        &mut self,
        id: u64,
        _tenant: TenantId,
        _build: BuildWrite,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push(format!("build.create:{id}"));
        Ok(())
    }

    fn reducer_transition_build(
        &mut self,
        id: u64,
        _tenant: TenantId,
        _generation: u64,
        _status: &str,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push(format!("build.transition:{id}"));
        Ok(())
    }

    fn reducer_recover_build(
        &mut self,
        id: u64,
        _tenant: TenantId,
        _generation: u64,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push(format!("build.recover:{id}"));
        Ok(())
    }

    fn reducer_create_deployment(
        &mut self,
        id: u64,
        _tenant: TenantId,
        _build_id: u64,
        _deployment: DeploymentWrite,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push(format!("deployment.create:{id}"));
        Ok(())
    }

    fn reducer_transition_deployment(
        &mut self,
        id: u64,
        _tenant: TenantId,
        _generation: u64,
        _status: &str,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls
            .push(format!("deployment.transition:{id}"));
        Ok(())
    }

    fn reducer_recover_deployment(
        &mut self,
        id: u64,
        _tenant: TenantId,
        _generation: u64,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push(format!("deployment.recover:{id}"));
        Ok(())
    }

    fn reducer_update_deployment_runtime(
        &mut self,
        id: u64,
        _tenant: TenantId,
        _generation: u64,
        _status: &str,
        _runtime: DeploymentRuntimeWrite,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push(format!("deployment.runtime:{id}"));
        Ok(())
    }

    fn reducer_register_runner(
        &mut self,
        id: u64,
        _tenant: TenantId,
        _runner: RunnerWrite,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push(format!("runner.register:{id}"));
        Ok(())
    }

    fn reducer_claim_runner(
        &mut self,
        _tenant: TenantId,
        _capability: &str,
        _now: u64,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push("runner.claim".to_owned());
        Ok(())
    }

    fn reducer_release_runner(
        &mut self,
        id: u64,
        _tenant: TenantId,
    ) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push(format!("runner.release:{id}"));
        Ok(())
    }

    fn reducer_expire_runners(&mut self, _now: u64) -> Result<(), SpacetimeDbError> {
        self.domain_calls.push("runner.expire".to_owned());
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{
        BuildState, DeploymentState, EntityId, Generation, JobState, SpacetimeDbPersistence,
    };

    #[test]
    fn generated_client_adapter_preserves_reducer_receipts_and_snapshots() {
        let mut persistence = GeneratedSpacetimeDb::new(ContractFakeClient::default());
        let first = persistence
            .append_event(TenantId(4), 7, "project.created", b"payload")
            .unwrap();
        let replay = persistence
            .append_event(TenantId(4), 7, "project.created", b"payload")
            .unwrap();
        assert!(!first.idempotent_replay);
        assert!(replay.idempotent_replay);
        persistence.save_auth_snapshot(1, b"auth").unwrap();
        assert_eq!(
            persistence.load_auth_snapshot(),
            Some((1, b"auth".to_vec()))
        );
    }

    #[test]
    fn generated_client_adapter_delegates_queue_reducers() {
        let mut persistence = GeneratedSpacetimeDb::new(ContractFakeClient::default());
        persistence
            .enqueue_job(Job {
                id: 9,
                kind: "build".to_owned(),
                state: JobState::Pending,
                attempts: 0,
                max_attempts: 2,
                lease_until: None,
                tenant: TenantId(7),
                generation: Generation(1),
            })
            .unwrap();
        assert_eq!(persistence.claim_job(3, 10, 20).unwrap().id, 9);
        assert_eq!(persistence.complete_job(9, 10), Ok(()));
        assert!(persistence.claim_job(3, 10, 20).is_none());
    }

    fn project_write(branch: &str, updated_at: u64) -> ProjectWrite {
        ProjectWrite {
            name: "Janus".to_owned(),
            slug: "janus".to_owned(),
            description: "control plane".to_owned(),
            repo_provider: "github".to_owned(),
            repository: "https://example.test/repo".to_owned(),
            branch: branch.to_owned(),
            created_at: 1,
            updated_at,
        }
    }

    fn deployment_write() -> DeploymentWrite {
        DeploymentWrite {
            project_id: Some(1),
            revision: 1,
            target_type: "preview".to_owned(),
            target_ref: "main".to_owned(),
            preferred_runner: "runner-1".to_owned(),
            environment: br#"[{"key":"MODE","value":"test"}]"#.to_vec(),
            runtime_id: String::default(),
            runtime_mode: String::default(),
            runtime_endpoint: String::default(),
            runtime_status: String::default(),
        }
    }

    #[test]
    fn component_projection_preserves_build_and_deployment_metadata() {
        let build = Build {
            tenant: TenantId(7),
            state: BuildState::Succeeded,
            generation: Generation(3),
            project: Some(EntityId(11)),
            source_ref: Some("release-1".to_owned()),
        };
        let build_write = BuildWrite::from_component(&build);
        assert_eq!(build_write.project_id, Some(11));
        assert_eq!(build_write.source, "release-1");
        assert_eq!(build_write.source_ref, "release-1");

        let deployment = Deployment {
            tenant: TenantId(7),
            build: EntityId(12),
            project: Some(EntityId(11)),
            revision: 4,
            target_type: "preview".to_owned(),
            target_ref: "release-1".to_owned(),
            preferred_runner: "runner-1".to_owned(),
            environment: vec![("MODE".to_owned(), "test".to_owned())],
            runtime_id: "runtime-1".to_owned(),
            runtime_mode: "process".to_owned(),
            runtime_endpoint: "http://runtime.local".to_owned(),
            runtime_status: "running".to_owned(),
            state: DeploymentState::Running,
            generation: Generation(4),
            resource_claim: None,
        };
        let deployment_write = DeploymentWrite::try_from_component(&deployment).unwrap();
        assert_eq!(deployment_write.project_id, Some(11));
        assert_eq!(deployment_write.revision, 4);
        assert_eq!(deployment_write.environment, br#"[["MODE","test"]]"#);
        assert_eq!(deployment_write.runtime_endpoint, "http://runtime.local");
    }

    #[test]
    fn generated_client_adapter_delegates_domain_reducers() {
        let mut persistence = GeneratedSpacetimeDb::new(ContractFakeClient::default());
        persistence
            .create_project(1, TenantId(2), project_write("main", 1))
            .unwrap();
        persistence
            .update_project(1, TenantId(2), project_write("release", 2), "ready")
            .unwrap();
        persistence.delete_project(1, TenantId(2)).unwrap();
        persistence
            .create_build(
                2,
                TenantId(2),
                BuildWrite {
                    source: "source-bundle".to_owned(),
                    project_id: Some(1),
                    source_ref: "main".to_owned(),
                },
            )
            .unwrap();
        persistence
            .transition_build(2, TenantId(2), 0, "succeeded")
            .unwrap();
        persistence.recover_build(2, TenantId(2), 0).unwrap();
        persistence
            .create_deployment(3, TenantId(2), 2, deployment_write())
            .unwrap();
        persistence
            .transition_deployment(3, TenantId(2), 0, "running")
            .unwrap();
        persistence.recover_deployment(3, TenantId(2), 0).unwrap();
        persistence
            .update_deployment_runtime(
                3,
                TenantId(2),
                0,
                "running",
                DeploymentRuntimeWrite {
                    runtime_id: "runtime-1".to_owned(),
                    runtime_mode: "process".to_owned(),
                    runtime_endpoint: "http://runtime.local".to_owned(),
                    runtime_status: "running".to_owned(),
                },
            )
            .unwrap();
        assert_eq!(
            persistence.client_mut().domain_calls,
            vec![
                "project.create:1",
                "project.update:1",
                "project.delete:1",
                "build.create:2",
                "build.transition:2",
                "build.recover:2",
                "deployment.create:3",
                "deployment.transition:3",
                "deployment.recover:3",
                "deployment.runtime:3",
            ]
        );
    }

    #[test]
    fn generated_client_adapter_delegates_operation_and_runner_reducers() {
        let mut persistence = GeneratedSpacetimeDb::new(ContractFakeClient::default());
        persistence
            .create_operation(7, TenantId(2), "build.enqueue", "request-7", 10)
            .unwrap();
        persistence
            .transition_operation(7, TenantId(2), OperationStatus::Succeeded, b"accepted", 11)
            .unwrap();
        persistence
            .register_runner(
                9,
                TenantId(2),
                RunnerWrite {
                    capabilities: vec!["build".to_owned()],
                    heartbeat_at: 10,
                    lease_seconds: 30,
                },
            )
            .unwrap();
        persistence.claim_runner(TenantId(2), "build", 11).unwrap();
        persistence.release_runner(9, TenantId(2)).unwrap();
        persistence.expire_runners(100).unwrap();
        assert_eq!(
            persistence.client_mut().domain_calls,
            vec![
                "operation.create:7",
                "operation.transition:7",
                "runner.register:9",
                "runner.claim",
                "runner.release:9",
                "runner.expire",
            ]
        );
    }
}
