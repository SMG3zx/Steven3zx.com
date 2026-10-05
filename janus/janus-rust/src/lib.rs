//! Janus's bounded control-plane core.
//!
//! Actors own worlds, systems apply commands, and effects leave the world only
//! through explicit events. The deterministic world remains independent of
//! databases and processes; Tokio is used only by the optional actor adapter.

#![deny(missing_docs)]
#![deny(rustdoc::broken_intra_doc_links)]
#![forbid(unsafe_code)]

use std::collections::VecDeque;

mod actor;
mod api;
mod assertions;
mod auth;
mod auth_persistence;
mod build_service;
mod config;
mod durable_service;
pub mod durable_worker;
mod email;
mod http;
// SpacetimeDB owns the generated SDK source and regenerates it from the
// module. Handwritten Janus code remains under the strict lint profile above.
#[allow(clippy::all, clippy::cargo, clippy::nursery, clippy::pedantic)]
#[doc(hidden)]
pub mod module_bindings;
mod object_store;
mod operation;
mod operation_service;
mod persistence;
mod project;
mod project_service;
mod queue;
mod queue_persistence;
mod runner;
mod runtime;
mod runtime_isolation;
mod security;
mod spacetime;
mod spacetime_client;
mod spacetime_runtime;
mod telemetry;
mod tracing_adapter;
mod verification_io;

pub use actor::*;
pub use api::*;
pub use assertions::*;
pub use auth::*;
pub use auth_persistence::*;
pub use build_service::*;
pub use config::*;
pub use durable_service::*;
pub use email::*;
pub use http::*;
pub use object_store::*;
pub use operation::*;
pub use operation_service::*;
pub use persistence::*;
pub use project::*;
pub use project_service::*;
pub use queue::*;
pub use queue_persistence::*;
pub use runner::*;
pub use runtime::*;
pub use runtime_isolation::*;
pub use security::*;
pub use spacetime::*;
pub use spacetime_client::*;
pub use spacetime_runtime::*;
pub use telemetry::*;
pub use tracing_adapter::*;
pub use verification_io::*;

/// Stable identifier for an ECS entity.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub struct EntityId(pub u64);

/// Tenant boundary carried by every control-plane entity and command.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub struct TenantId(pub u32);

/// Monotonic command identity used for idempotency.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub struct CommandId(pub u64);

/// Worker generation used to reject stale completions.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub struct Generation(pub u64);

/// Deterministic simulation time.
#[derive(Clone, Copy, Debug, Default, Eq, Hash, PartialEq, Ord, PartialOrd)]
pub struct Tick(pub u64);

/// Current command/event envelope schema.
pub const CURRENT_PROTOCOL_VERSION: u16 = 5;

/// Maximum retained in-memory diagnostic samples for one deterministic run.
pub const MAX_TRACE_SAMPLES: usize = 4096;

/// Maximum retained simulation ticks before the caller must drain or restart.
pub const MAX_SIMULATION_STEPS: usize = 4096;

/// Maximum environment entries retained on one deployment.
pub const MAX_DEPLOYMENT_ENV_ENTRIES: usize = 32;

/// Validates the Go-compatible deployment environment key grammar.
#[must_use]
pub fn valid_deployment_env_key(key: &str) -> bool {
    let mut bytes = key.bytes();
    let Some(first) = bytes.next() else {
        return false;
    };
    (first == b'_' || first.is_ascii_alphabetic())
        && bytes.all(|value| value == b'_' || value.is_ascii_alphanumeric())
}

/// Version attached to commands, events, and snapshots crossing an actor or
/// persistence boundary.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ProtocolVersion(pub u16);

/// Versioned command envelope used by adapters and replay files.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CommandEnvelope {
    /// Schema version used to decode `command`.
    pub version: ProtocolVersion,
    /// Command identity and payload.
    pub command: Command,
}

/// Error returned when an adapter presents an unsupported schema.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum EnvelopeError {
    /// The version is not supported by this binary.
    UnsupportedVersion(ProtocolVersion),
}

impl CommandEnvelope {
    /// Validates the envelope version before admission to an actor mailbox.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub const fn validate(&self) -> Result<(), EnvelopeError> {
        if self.version.0 == CURRENT_PROTOCOL_VERSION {
            Ok(())
        } else {
            Err(EnvelopeError::UnsupportedVersion(self.version))
        }
    }
}

/// Failure while admitting a versioned, authenticated command envelope.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum EnvelopeAdmissionError {
    /// The command schema is unsupported.
    Envelope(EnvelopeError),
    /// The principal is not authorized for the command.
    Authorization(AuthorizationError),
    /// The actor mailbox is full.
    Capacity,
}

/// External resource category tracked for ownership and cleanup.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ResourceKind {
    /// Operating-system process.
    Process,
    /// Listening or connected socket.
    Socket,
    /// Temporary or durable artifact.
    Artifact,
    /// Lease or lock.
    Lease,
}

/// Ownership record for an external resource.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ResourceClaim {
    /// Resource identity.
    pub resource: EntityId,
    /// Owning tenant.
    pub tenant: TenantId,
    /// Actor/entity responsible for cleanup.
    pub owner: EntityId,
    /// Generation that created the claim.
    pub generation: Generation,
    /// Resource category.
    pub kind: ResourceKind,
}

/// Resource registry errors.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ClaimError {
    /// No claim slots remain.
    Capacity,
    /// Another owner already holds the resource.
    Conflict,
    /// A stale owner attempted to release or replace a claim.
    StaleGeneration,
    /// A different tenant attempted to release the claim.
    TenantBoundary,
    /// The resource does not have a claim.
    NotFound,
}

/// Bounded ownership registry used by process/runtime actors.
pub struct ResourceRegistry<const MAX_RESOURCES: usize> {
    claims: Vec<Option<ResourceClaim>>,
}

impl<const MAX_RESOURCES: usize> ResourceRegistry<MAX_RESOURCES> {
    /// Creates an empty registry with fixed capacity.
    #[must_use]
    pub fn new() -> Self {
        Self {
            claims: vec![None; MAX_RESOURCES],
        }
    }

    /// Claims a resource exactly once.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn claim(&mut self, claim: ResourceClaim) -> Result<(), ClaimError> {
        if self
            .claims
            .iter()
            .flatten()
            .any(|value| value.resource == claim.resource)
        {
            return Err(ClaimError::Conflict);
        }
        let slot = self
            .claims
            .iter_mut()
            .find(|value| value.is_none())
            .ok_or(ClaimError::Capacity)?;
        *slot = Some(claim);
        Ok(())
    }

    /// Releases a resource only when the owner and generation match.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn release(
        &mut self,
        resource: EntityId,
        tenant: TenantId,
        owner: EntityId,
        generation: Generation,
    ) -> Result<(), ClaimError> {
        let index = self
            .claims
            .iter()
            .position(|value| {
                value
                    .as_ref()
                    .is_some_and(|claim| claim.resource == resource)
            })
            .ok_or(ClaimError::NotFound)?;
        let claim = self.claims[index].ok_or(ClaimError::NotFound)?;
        if claim.tenant != tenant {
            return Err(ClaimError::TenantBoundary);
        }
        if claim.owner != owner || claim.generation != generation {
            return Err(ClaimError::StaleGeneration);
        }
        self.claims[index] = None;
        Ok(())
    }

    /// Reads a claim without transferring ownership.
    #[must_use]
    pub fn get(&self, resource: EntityId) -> Option<ResourceClaim> {
        self.claims
            .iter()
            .flatten()
            .find(|value| value.resource == resource)
            .copied()
    }
}

impl<const MAX_RESOURCES: usize> Default for ResourceRegistry<MAX_RESOURCES> {
    fn default() -> Self {
        Self::new()
    }
}

/// Current lifecycle state of a build entity.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum BuildState {
    /// The build has been accepted but not started.
    Pending,
    /// The build has an active execution effect.
    Running,
    /// The build completed successfully.
    Succeeded,
    /// The build failed.
    Failed,
    /// The build was cancelled.
    Cancelled,
}

impl BuildState {
    const fn terminal(self) -> bool {
        matches!(self, Self::Succeeded | Self::Failed | Self::Cancelled)
    }
}

/// Lifecycle state of a deployed runtime.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DeploymentState {
    /// Deployment has been accepted but has no runtime yet.
    Pending,
    /// A runtime start effect is outstanding.
    Starting,
    /// The runtime reported readiness.
    Running,
    /// The runtime exited or failed readiness.
    Failed,
    /// The deployment was intentionally stopped.
    Stopped,
}

impl DeploymentState {
    const fn terminal(self) -> bool {
        matches!(self, Self::Failed | Self::Stopped)
    }
}

/// Deployment components stored by the control-plane actor.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Deployment {
    /// Owning tenant.
    pub tenant: TenantId,
    /// Build artifact used by this deployment.
    pub build: EntityId,
    /// Project owning the deployed build, when the build came from a project workflow.
    pub project: Option<EntityId>,
    /// Monotonic project deployment revision used for temporary domains.
    pub revision: u64,
    /// Deployment target category, such as preview or production.
    pub target_type: String,
    /// Deployment target reference, such as a branch or environment name.
    pub target_ref: String,
    /// Runner explicitly preferred for this deployment.
    pub preferred_runner: String,
    /// Bounded deployment environment entries.
    pub environment: Vec<(String, String)>,
    /// Stable runtime identity assigned by the runner.
    pub runtime_id: String,
    /// Runtime execution mode, when a runtime has started.
    pub runtime_mode: String,
    /// Runtime HTTP endpoint, when one is available.
    pub runtime_endpoint: String,
    /// Runtime-specific status mirrored from the execution adapter.
    pub runtime_status: String,
    /// Current runtime lifecycle state.
    pub state: DeploymentState,
    /// Runtime generation used to reject stale readiness and exit messages.
    pub generation: Generation,
    /// Generation-fenced process resource owned by this deployment.
    pub resource_claim: Option<ResourceClaim>,
}

/// Metadata admitted with a deployment creation request.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct DeploymentMetadata {
    /// Deployment target category.
    pub target_type: String,
    /// Deployment target reference.
    pub target_ref: String,
    /// Preferred runner identity.
    pub preferred_runner: String,
    /// Bounded environment entries.
    pub environment: Vec<(String, String)>,
}

/// Admission mode for operational control.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub enum ControlMode {
    /// Accept normal commands.
    #[default]
    Running,
    /// Reject new work while allowing in-flight work to finish.
    Paused,
    /// Drain in-flight work and reject new work.
    Draining,
}

/// Build components stored together by the control-plane world.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Build {
    /// Owning tenant.
    pub tenant: TenantId,
    /// Current lifecycle state.
    pub state: BuildState,
    /// Active or most recently assigned worker generation.
    pub generation: Generation,
    /// Project owning the build, when submitted from a project workflow.
    pub project: Option<EntityId>,
    /// Source branch, tag, or upload reference used for the build.
    pub source_ref: Option<String>,
}

/// Commands accepted by the control-plane actor.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum Command {
    /// Creates a pending build.
    SubmitBuild {
        /// Idempotency identity.
        command_id: CommandId,
        /// Entity to create.
        build: EntityId,
        /// Tenant boundary.
        tenant: TenantId,
    },
    /// Creates a pending build associated with a project and source reference.
    SubmitProjectBuild {
        /// Idempotency identity.
        command_id: CommandId,
        /// Entity to create.
        build: EntityId,
        /// Tenant boundary.
        tenant: TenantId,
        /// Owning project.
        project: EntityId,
        /// Source branch, tag, or upload reference.
        source_ref: String,
    },
    /// Creates a validated tenant-owned project.
    CreateProject {
        /// Idempotency identity.
        command_id: CommandId,
        /// Project component to insert.
        project: Box<Project>,
    },
    /// Updates a project's repository configuration.
    UpdateProjectRepository {
        /// Idempotency identity.
        command_id: CommandId,
        /// Project entity.
        project: EntityId,
        /// Tenant boundary.
        tenant: TenantId,
        /// Repository provider.
        repo_provider: String,
        /// Repository URL.
        repo_url: String,
        /// Source branch or reference.
        repo_branch: String,
    },
    /// Deletes a tenant-owned project.
    DeleteProject {
        /// Idempotency identity.
        command_id: CommandId,
        /// Project entity.
        project: EntityId,
        /// Tenant boundary.
        tenant: TenantId,
    },
    /// Cancels a non-terminal build.
    CancelBuild {
        /// Idempotency identity.
        command_id: CommandId,
        /// Build entity.
        build: EntityId,
        /// Tenant boundary.
        tenant: TenantId,
    },
    /// Reports process completion from a specific generation.
    ProcessFinished {
        /// Idempotency identity.
        command_id: CommandId,
        /// Build entity.
        build: EntityId,
        /// Tenant boundary.
        tenant: TenantId,
        /// Worker generation that produced the result.
        generation: Generation,
        /// Zero is success; non-zero is failure.
        exit_code: u32,
    },
    /// Creates a deployment from a succeeded build.
    CreateDeployment {
        /// Idempotency identity.
        command_id: CommandId,
        /// Entity to create.
        deployment: EntityId,
        /// Tenant boundary.
        tenant: TenantId,
        /// Succeeded build entity.
        build: EntityId,
        /// Deployment target category.
        target_type: String,
        /// Deployment target reference.
        target_ref: String,
        /// Preferred runner identity.
        preferred_runner: String,
        /// Bounded environment entries.
        environment: Vec<(String, String)>,
    },
    /// Reports readiness for a specific runtime generation.
    RuntimeReady {
        /// Idempotency identity.
        command_id: CommandId,
        /// Deployment entity.
        deployment: EntityId,
        /// Tenant boundary.
        tenant: TenantId,
        /// Runtime generation.
        generation: Generation,
        /// Runtime identity assigned by the runner.
        runtime_id: String,
        /// Runtime execution mode.
        runtime_mode: String,
        /// Runtime HTTP endpoint, when available.
        runtime_endpoint: String,
    },
    /// Reports runtime exit for a specific generation.
    RuntimeExited {
        /// Idempotency identity.
        command_id: CommandId,
        /// Deployment entity.
        deployment: EntityId,
        /// Tenant boundary.
        tenant: TenantId,
        /// Runtime generation.
        generation: Generation,
    },
    /// Stops a deployment.
    StopDeployment {
        /// Idempotency identity.
        command_id: CommandId,
        /// Deployment entity.
        deployment: EntityId,
        /// Tenant boundary.
        tenant: TenantId,
    },
    /// Pauses admission of new work.
    Pause {
        /// Idempotency identity.
        command_id: CommandId,
    },
    /// Resumes admission of new work.
    Resume {
        /// Idempotency identity.
        command_id: CommandId,
    },
    /// Begins draining the actor.
    Drain {
        /// Idempotency identity.
        command_id: CommandId,
    },
}

impl Command {
    pub(crate) fn authorization(&self) -> (Option<TenantId>, Permission) {
        match self {
            Self::SubmitBuild { tenant, .. } | Self::SubmitProjectBuild { tenant, .. } => {
                (Some(*tenant), Permission::SubmitBuilds)
            }
            Self::CreateProject { project, .. } => {
                (Some(project.tenant), Permission::ManageProjects)
            }
            Self::UpdateProjectRepository { tenant, .. } | Self::DeleteProject { tenant, .. } => {
                (Some(*tenant), Permission::ManageProjects)
            }
            Self::CancelBuild { tenant, .. } => (Some(*tenant), Permission::CancelBuilds),
            Self::CreateDeployment { tenant, .. } | Self::StopDeployment { tenant, .. } => {
                (Some(*tenant), Permission::ManageDeployments)
            }
            Self::ProcessFinished { tenant, .. }
            | Self::RuntimeReady { tenant, .. }
            | Self::RuntimeExited { tenant, .. } => {
                (Some(*tenant), Permission::OperateControlPlane)
            }
            Self::Pause { .. } | Self::Resume { .. } | Self::Drain { .. } => {
                (None, Permission::OperateControlPlane)
            }
        }
    }
}

/// Failure while admitting an authenticated command to an actor mailbox.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AdmissionError {
    /// The principal is not authorized for the command's tenant or operation.
    Authorization(AuthorizationError),
    /// The actor mailbox is at capacity.
    Capacity,
}

/// Durable or externally observable lifecycle event.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum Event {
    /// A project was accepted.
    ProjectCreated {
        /// Project entity.
        project: EntityId,
        /// Owning tenant.
        tenant: TenantId,
    },
    /// A project's repository configuration changed.
    ProjectRepositoryUpdated {
        /// Project entity.
        project: EntityId,
    },
    /// A project was deleted.
    ProjectDeleted {
        /// Project entity.
        project: EntityId,
    },
    /// A build was accepted.
    BuildAccepted {
        /// Build entity.
        build: EntityId,
        /// Owning tenant.
        tenant: TenantId,
    },
    /// A build execution effect was requested.
    BuildStarted {
        /// Build entity.
        build: EntityId,
        /// Assigned worker generation.
        generation: Generation,
    },
    /// A build completed successfully.
    BuildSucceeded {
        /// Build entity.
        build: EntityId,
        /// Completing worker generation.
        generation: Generation,
    },
    /// A build completed unsuccessfully.
    BuildFailed {
        /// Build entity.
        build: EntityId,
        /// Completing worker generation.
        generation: Generation,
        /// Process exit code.
        exit_code: u32,
    },
    /// A build was cancelled.
    BuildCancelled {
        /// Build entity.
        build: EntityId,
    },
    /// A deployment was accepted.
    DeploymentAccepted {
        /// Deployment entity.
        deployment: EntityId,
        /// Owning tenant.
        tenant: TenantId,
        /// Source build entity.
        build: EntityId,
    },
    /// A runtime start effect was requested.
    DeploymentStarting {
        /// Deployment entity.
        deployment: EntityId,
        /// Assigned runtime generation.
        generation: Generation,
    },
    /// A runtime reported readiness.
    DeploymentRunning {
        /// Deployment entity.
        deployment: EntityId,
        /// Ready runtime generation.
        generation: Generation,
    },
    /// A runtime exited unexpectedly.
    DeploymentFailed {
        /// Deployment entity.
        deployment: EntityId,
        /// Failed runtime generation.
        generation: Generation,
    },
    /// A deployment was intentionally stopped.
    DeploymentStopped {
        /// Deployment entity.
        deployment: EntityId,
    },
    /// The actor paused admission.
    ControlPaused,
    /// The actor resumed admission.
    ControlResumed,
    /// The actor began draining.
    ControlDraining,
    /// A command was rejected without mutating state.
    CommandRejected {
        /// Rejected command identity.
        command_id: CommandId,
        /// Stable rejection reason.
        reason: RejectReason,
    },
}

/// Stable rejection reason suitable for API and simulation assertions.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RejectReason {
    /// The entity does not exist.
    NotFound,
    /// The tenant does not own the entity.
    TenantBoundary,
    /// The requested transition is invalid.
    InvalidTransition,
    /// The supplied generation is stale.
    StaleGeneration,
    /// The world reached its configured entity capacity.
    Capacity,
    /// The referenced build is not succeeded.
    BuildNotSucceeded,
    /// New work is paused.
    Paused,
    /// New work is draining.
    Draining,
}

/// External effect emitted by a system.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum Effect {
    /// Ask an execution actor to run one build generation.
    RunBuild {
        /// Build entity.
        build: EntityId,
        /// Owning tenant.
        tenant: TenantId,
        /// Assigned worker generation.
        generation: Generation,
    },
    /// Ask a runtime actor to start a deployment generation.
    StartRuntime {
        /// Deployment entity.
        deployment: EntityId,
        /// Owning tenant.
        tenant: TenantId,
        /// Source build entity.
        build: EntityId,
        /// Assigned runtime generation.
        generation: Generation,
    },
    /// Ask a runtime actor to stop a deployment.
    StopRuntime {
        /// Deployment entity.
        deployment: EntityId,
        /// Owning tenant.
        tenant: TenantId,
        /// Runtime generation to stop.
        generation: Generation,
    },
}

/// A single structured diagnostic sample. Integrations can translate this to
/// `tokio-rs/tracing` spans without putting tracing in the deterministic core.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct TraceRecord {
    /// Logical operation name.
    pub operation: &'static str,
    /// Simulation or production tick.
    pub tick: Tick,
    /// Optional entity associated with the operation.
    pub entity: Option<EntityId>,
}

/// Deterministic work measurement for one actor tick.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct PerformanceSample {
    /// Tick at which the sample was produced.
    pub tick: Tick,
    /// Number of commands removed from the mailbox.
    pub commands_processed: u32,
    /// Number of events currently buffered after the tick.
    pub buffered_events: u32,
    /// Number of effects currently buffered after the tick.
    pub buffered_effects: u32,
}

/// Serializable control-plane snapshot for crash recovery.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct WorldSnapshot {
    /// Snapshot protocol version.
    pub version: ProtocolVersion,
    /// Last processed tick.
    pub tick: Tick,
    /// Build component storage.
    pub builds: Vec<Option<Build>>,
    /// Deployment component storage.
    pub deployments: Vec<Option<Deployment>>,
    /// Operation component storage.
    pub operations: Vec<Option<Operation>>,
    /// Project component storage.
    pub projects: Vec<Option<Project>>,
    /// Idempotency identities already applied.
    pub seen_commands: Vec<CommandId>,
    /// Admission mode at snapshot time.
    pub control_mode: ControlMode,
}

/// Counts durable ECS entities made eligible for post-crash reconciliation.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct RecoveryReport {
    /// Builds that were running when the snapshot was taken and are queued again.
    pub builds_requeued: u32,
    /// Deployments that were starting when the snapshot was taken and are queued again.
    pub deployments_requeued: u32,
}

/// Snapshot restore error.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum SnapshotError {
    /// Snapshot schema is not supported.
    UnsupportedVersion(ProtocolVersion),
    /// Snapshot storage exceeds this world's configured capacity.
    Capacity,
    /// Snapshot contents violate the world invariants.
    InvariantViolation(InvariantViolation),
}

/// Failure while inserting or advancing an operation component.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum OperationStoreError {
    /// The entity slot is outside this world's bounded operation storage.
    Capacity,
    /// The entity already has an operation component.
    AlreadyExists,
    /// The entity has no operation component.
    NotFound,
    /// The proposed status would regress the operation lifecycle.
    StatusRegression,
}

/// World invariant violation detected after a state transition.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum InvariantViolation {
    /// A deployment references a missing build.
    DeploymentBuildMissing,
    /// A deployment and its build belong to different tenants.
    DeploymentTenantMismatch,
    /// A deployment references a build that has not succeeded.
    DeploymentBuildNotSucceeded,
    /// A tenant's active builds exceed its quota.
    BuildQuotaExceeded,
    /// A tenant's deployments exceed its quota.
    DeploymentQuotaExceeded,
    /// A tenant's active runtimes exceed its quota.
    RuntimeQuotaExceeded,
    /// A transient queue exceeded its fixed capacity.
    CapacityExceeded,
    /// A deployment's process claim does not match its lifecycle owner.
    ResourceOwnershipMismatch,
    /// Two deployment components claim the same external resource.
    DuplicateResourceClaim,
}

/// Deterministic faults applied by the simulator to the next external effect.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct FaultPlan {
    /// Fail the next build process with a non-zero exit code.
    pub fail_next_process: bool,
    /// Drop the next external effect without producing a completion.
    pub drop_next_effect: bool,
    /// Deliver the next completion twice.
    pub duplicate_next_effect: bool,
}

/// Replay record for one deterministic simulator step.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct ReplayRecord {
    /// Simulation tick.
    pub tick: Tick,
    /// External commands submitted before this tick.
    pub external_commands: Vec<Command>,
    /// Fault plan active for this tick.
    pub faults: FaultPlan,
    /// Number of effects emitted before fault injection.
    pub effects_emitted: u32,
    /// Number of effects dropped.
    pub effects_dropped: u32,
    /// Number of effects duplicated.
    pub effects_duplicated: u32,
    /// Number of lifecycle events appended to the journal.
    pub events_persisted: u32,
}

/// Sink for structured diagnostics and performance samples.
pub trait TraceSink {
    /// Records one diagnostic sample.
    fn record(&mut self, sample: TraceRecord);
}

/// A bounded ECS world owned by one actor.
pub struct World<const MAX_BUILDS: usize, const MAX_COMMANDS: usize> {
    builds: Vec<Option<Build>>,
    deployments: Vec<Option<Deployment>>,
    operations: Vec<Option<Operation>>,
    projects: Vec<Option<Project>>,
    commands: VecDeque<Command>,
    seen_commands: Vec<CommandId>,
    events: Vec<Event>,
    effects: Vec<Effect>,
    tick: Tick,
    performance: PerformanceSample,
    control_mode: ControlMode,
    quota: TenantQuota,
}

impl<const MAX_BUILDS: usize, const MAX_COMMANDS: usize> World<MAX_BUILDS, MAX_COMMANDS> {
    /// Creates an empty world with fixed logical capacities.
    #[must_use]
    pub fn new() -> Self {
        let max_u32 = usize::try_from(u32::MAX).unwrap_or(usize::MAX);
        Self::with_quota(TenantQuota {
            max_builds: u32::try_from(MAX_BUILDS.min(max_u32)).unwrap_or(u32::MAX),
            max_deployments: u32::try_from(MAX_BUILDS.min(max_u32)).unwrap_or(u32::MAX),
            max_running_processes: u32::try_from(MAX_BUILDS.min(max_u32)).unwrap_or(u32::MAX),
        })
    }

    /// Creates a world with explicit per-tenant quotas.
    #[must_use]
    pub fn with_quota(quota: TenantQuota) -> Self {
        Self {
            builds: vec![None; MAX_BUILDS],
            deployments: vec![None; MAX_BUILDS],
            operations: vec![None; MAX_BUILDS],
            projects: vec![None; MAX_BUILDS],
            commands: VecDeque::with_capacity(MAX_COMMANDS),
            seen_commands: Vec::with_capacity(MAX_COMMANDS),
            events: Vec::with_capacity(MAX_COMMANDS.saturating_mul(2)),
            effects: Vec::with_capacity(MAX_COMMANDS),
            tick: Tick::default(),
            performance: PerformanceSample::default(),
            control_mode: ControlMode::Running,
            quota,
        }
    }

    /// Queues a command for the owning actor.
    pub(crate) fn enqueue(&mut self, command: Command) -> Result<(), Command> {
        if self.commands.len() >= MAX_COMMANDS {
            return Err(command);
        }
        self.commands.push_back(command);
        Ok(())
    }

    /// Authorizes and queues a command at the external command boundary.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn enqueue_authorized(
        &mut self,
        principal: &Principal,
        command: Command,
    ) -> Result<(), AdmissionError> {
        let (tenant, permission) = command.authorization();
        if let Some(tenant) = tenant {
            principal
                .authorize(tenant, permission)
                .map_err(AdmissionError::Authorization)?;
        } else if principal.authorize(principal.tenant, permission).is_err() {
            return Err(AdmissionError::Authorization(
                AuthorizationError::PermissionDenied,
            ));
        }
        self.enqueue(command).map_err(|_| AdmissionError::Capacity)
    }

    /// Validates, authorizes, and queues one versioned command envelope.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn enqueue_envelope_authorized(
        &mut self,
        principal: &Principal,
        envelope: CommandEnvelope,
    ) -> Result<(), EnvelopeAdmissionError> {
        envelope
            .validate()
            .map_err(EnvelopeAdmissionError::Envelope)?;
        self.enqueue_authorized(principal, envelope.command)
            .map_err(|error| match error {
                AdmissionError::Authorization(error) => {
                    EnvelopeAdmissionError::Authorization(error)
                }
                AdmissionError::Capacity => EnvelopeAdmissionError::Capacity,
            })
    }

    /// Advances one bounded actor tick and applies queued commands.
    pub fn tick<T: TraceSink>(&mut self, trace: &mut T) {
        self.tick = Tick(self.tick.0.saturating_add(1));
        let count = self.commands.len();
        for _ in 0..count {
            if let Some(command) = self.commands.pop_front() {
                self.apply(command, trace);
            }
        }
        self.performance = PerformanceSample {
            tick: self.tick,
            commands_processed: u32::try_from(count).unwrap_or(u32::MAX),
            buffered_events: u32::try_from(self.events.len()).unwrap_or(u32::MAX),
            buffered_effects: u32::try_from(self.effects.len()).unwrap_or(u32::MAX),
        };
        trace.record(TraceRecord {
            operation: "ecs.tick",
            tick: self.tick,
            entity: None,
        });
    }

    /// Returns the deterministic work sample for the most recent tick.
    #[must_use]
    pub const fn performance(&self) -> PerformanceSample {
        self.performance
    }

    /// Drains lifecycle events produced by the last ticks.
    pub fn drain_events(&mut self) -> Vec<Event> {
        std::mem::take(&mut self.events)
    }

    /// Drains external effects produced by the last ticks.
    pub fn drain_effects(&mut self) -> Vec<Effect> {
        std::mem::take(&mut self.effects)
    }

    /// Reads a build owned by this actor.
    pub fn build(&self, entity: EntityId) -> Option<&Build> {
        self.builds
            .get(usize::try_from(entity.0).unwrap_or(usize::MAX))
            .and_then(Option::as_ref)
    }

    /// Reads a deployment owned by this actor.
    pub fn deployment(&self, entity: EntityId) -> Option<&Deployment> {
        self.deployments
            .get(usize::try_from(entity.0).unwrap_or(usize::MAX))
            .and_then(Option::as_ref)
    }

    /// Reads an operation component owned by this actor.
    pub fn operation(&self, entity: EntityId) -> Option<&Operation> {
        self.operations
            .get(usize::try_from(entity.0).unwrap_or(usize::MAX))
            .and_then(Option::as_ref)
    }

    /// Inserts one bounded operation component at an entity slot.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn insert_operation(
        &mut self,
        entity: EntityId,
        operation: Operation,
    ) -> Result<(), OperationStoreError> {
        let slot = self
            .operations
            .get_mut(usize::try_from(entity.0).unwrap_or(usize::MAX))
            .ok_or(OperationStoreError::Capacity)?;
        if slot.is_some() {
            return Err(OperationStoreError::AlreadyExists);
        }
        *slot = Some(operation);
        Ok(())
    }

    /// Replaces an operation only when its lifecycle rank does not regress.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn replace_operation(
        &mut self,
        entity: EntityId,
        operation: Operation,
    ) -> Result<(), OperationStoreError> {
        let slot = self
            .operations
            .get_mut(usize::try_from(entity.0).unwrap_or(usize::MAX))
            .ok_or(OperationStoreError::Capacity)?;
        let current = slot.as_ref().ok_or(OperationStoreError::NotFound)?;
        if operation.status.rank() < current.status.rank() {
            return Err(OperationStoreError::StatusRegression);
        }
        *slot = Some(operation);
        Ok(())
    }

    /// Reads a project component owned by this actor.
    pub fn project(&self, entity: EntityId) -> Option<&Project> {
        self.projects
            .get(usize::try_from(entity.0).unwrap_or(usize::MAX))
            .and_then(Option::as_ref)
    }

    /// Inserts one validated, tenant-owned project component.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn insert_project(&mut self, project: Project) -> Result<(), ProjectError> {
        project.validate()?;
        let slot = self
            .projects
            .get_mut(usize::try_from(project.id.0).unwrap_or(usize::MAX))
            .ok_or(ProjectError::Capacity)?;
        if slot.is_some() {
            return Err(ProjectError::AlreadyExists);
        }
        *slot = Some(project);
        Ok(())
    }

    /// Reads the actor's operational admission mode.
    #[must_use]
    pub const fn control_mode(&self) -> ControlMode {
        self.control_mode
    }

    /// Captures durable ECS state without pending commands or effects.
    #[must_use]
    pub fn snapshot(&self) -> WorldSnapshot {
        WorldSnapshot {
            version: ProtocolVersion(CURRENT_PROTOCOL_VERSION),
            tick: self.tick,
            builds: self.builds.clone(),
            deployments: self.deployments.clone(),
            operations: self.operations.clone(),
            projects: self.projects.clone(),
            seen_commands: self.seen_commands.clone(),
            control_mode: self.control_mode,
        }
    }

    /// Restores durable ECS state and clears transient queues.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn restore(&mut self, snapshot: WorldSnapshot) -> Result<(), SnapshotError> {
        if snapshot.version.0 != CURRENT_PROTOCOL_VERSION {
            return Err(SnapshotError::UnsupportedVersion(snapshot.version));
        }
        if snapshot.builds.len() > MAX_BUILDS
            || snapshot.deployments.len() > MAX_BUILDS
            || snapshot.operations.len() > MAX_BUILDS
            || snapshot.projects.len() > MAX_BUILDS
            || snapshot.seen_commands.len() > MAX_COMMANDS
        {
            return Err(SnapshotError::Capacity);
        }
        let mut candidate = Self::with_quota(self.quota);
        candidate.builds.clone_from(&snapshot.builds);
        candidate.deployments.clone_from(&snapshot.deployments);
        candidate.operations.clone_from(&snapshot.operations);
        candidate.projects.clone_from(&snapshot.projects);
        candidate.seen_commands.clone_from(&snapshot.seen_commands);
        candidate.control_mode = snapshot.control_mode;
        candidate.tick = snapshot.tick;
        candidate
            .validate_invariants()
            .map_err(SnapshotError::InvariantViolation)?;
        self.builds = snapshot.builds;
        self.deployments = snapshot.deployments;
        self.operations = snapshot.operations;
        self.projects = snapshot.projects;
        self.seen_commands = snapshot.seen_commands;
        self.commands.clear();
        self.events.clear();
        self.effects.clear();
        self.tick = snapshot.tick;
        self.control_mode = snapshot.control_mode;
        self.performance = PerformanceSample {
            tick: self.tick,
            ..PerformanceSample::default()
        };
        Ok(())
    }

    /// Restores a snapshot and requeues work that could have been lost with an
    /// external worker or runtime process.
    ///
    /// A running build is returned to `Pending` so the next actor tick emits a
    /// fresh generation-fenced build effect. A starting deployment is treated
    /// the same way and releases its stale process claim before it is started
    /// again. Already-running deployments are left untouched because their
    /// runtime identity is durable and may still be serving traffic.
    ///
    /// # Errors
    ///
    /// Returns an error when the snapshot cannot be restored without violating
    /// the world's version, capacity, or invariant contract.
    pub fn restore_and_recover(
        &mut self,
        snapshot: WorldSnapshot,
    ) -> Result<RecoveryReport, SnapshotError> {
        self.restore(snapshot)?;
        Ok(self.recover_incomplete_work())
    }

    /// Requeues incomplete work after a worker or runtime failure.
    ///
    /// This method is intentionally separate from `tick`: recovery is an
    /// adapter boundary and must be explicit in startup and reconciliation
    /// paths. The next tick performs normal admission, effect emission, and
    /// generation fencing.
    pub fn recover_incomplete_work(&mut self) -> RecoveryReport {
        let mut report = RecoveryReport::default();
        for build in self.builds.iter_mut().flatten() {
            if build.state == BuildState::Running {
                build.state = BuildState::Pending;
                report.builds_requeued = report.builds_requeued.saturating_add(1);
            }
        }
        for deployment in self.deployments.iter_mut().flatten() {
            if deployment.state == DeploymentState::Starting {
                deployment.state = DeploymentState::Pending;
                deployment.resource_claim = None;
                report.deployments_requeued = report.deployments_requeued.saturating_add(1);
            }
        }
        report
    }

    /// Audits ownership, lifecycle, quota, and bounded-queue invariants.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn validate_invariants(&self) -> Result<(), InvariantViolation> {
        if self.commands.len() > MAX_COMMANDS
            || self.seen_commands.len() > MAX_COMMANDS
            || self.events.len() > MAX_COMMANDS.saturating_mul(2)
            || self.effects.len() > MAX_COMMANDS
        {
            return Err(InvariantViolation::CapacityExceeded);
        }
        self.validate_deployment_invariants()?;
        self.validate_resource_claims()?;
        self.validate_quotas()
    }

    fn validate_deployment_invariants(&self) -> Result<(), InvariantViolation> {
        for (index, deployment) in self
            .deployments
            .iter()
            .enumerate()
            .filter_map(|(index, deployment)| deployment.as_ref().map(|value| (index, value)))
        {
            let build = self
                .build(deployment.build)
                .ok_or(InvariantViolation::DeploymentBuildMissing)?;
            if build.tenant != deployment.tenant {
                return Err(InvariantViolation::DeploymentTenantMismatch);
            }
            if build.state != BuildState::Succeeded {
                return Err(InvariantViolation::DeploymentBuildNotSucceeded);
            }
            let claim_required = matches!(
                deployment.state,
                DeploymentState::Starting | DeploymentState::Running
            );
            match (claim_required, deployment.resource_claim) {
                (true, Some(claim))
                    if claim.resource == EntityId(u64::try_from(index).unwrap_or(u64::MAX))
                        && claim.owner == EntityId(u64::try_from(index).unwrap_or(u64::MAX))
                        && claim.tenant == deployment.tenant
                        && claim.generation == deployment.generation
                        && claim.kind == ResourceKind::Process => {}
                (false, None) => {}
                _ => return Err(InvariantViolation::ResourceOwnershipMismatch),
            }
        }
        Ok(())
    }

    fn validate_resource_claims(&self) -> Result<(), InvariantViolation> {
        for (index, deployment) in self
            .deployments
            .iter()
            .enumerate()
            .filter_map(|(index, deployment)| deployment.as_ref().map(|value| (index, value)))
        {
            if let Some(claim) = deployment.resource_claim {
                let duplicate = self
                    .deployments
                    .iter()
                    .enumerate()
                    .filter_map(|(other_index, other)| {
                        other.as_ref().map(|value| (other_index, value))
                    })
                    .any(|(other_index, other)| {
                        other_index != index
                            && other
                                .resource_claim
                                .is_some_and(|other_claim| other_claim.resource == claim.resource)
                    });
                if duplicate {
                    return Err(InvariantViolation::DuplicateResourceClaim);
                }
            }
        }
        Ok(())
    }

    fn validate_quotas(&self) -> Result<(), InvariantViolation> {
        for tenant in self.builds.iter().flatten().map(|value| value.tenant) {
            let build_count = self
                .builds
                .iter()
                .flatten()
                .filter(|value| value.tenant == tenant)
                .count();
            if build_count > usize::try_from(self.quota.max_builds).unwrap_or(usize::MAX) {
                return Err(InvariantViolation::BuildQuotaExceeded);
            }
            let deployment_count = self
                .deployments
                .iter()
                .flatten()
                .filter(|value| value.tenant == tenant)
                .count();
            if deployment_count > usize::try_from(self.quota.max_deployments).unwrap_or(usize::MAX)
            {
                return Err(InvariantViolation::DeploymentQuotaExceeded);
            }
            let runtime_count = self
                .deployments
                .iter()
                .flatten()
                .filter(|value| {
                    value.tenant == tenant
                        && (value.state == DeploymentState::Starting
                            || value.state == DeploymentState::Running)
                })
                .count();
            if runtime_count
                > usize::try_from(self.quota.max_running_processes).unwrap_or(usize::MAX)
            {
                return Err(InvariantViolation::RuntimeQuotaExceeded);
            }
        }
        Ok(())
    }

    fn apply<T: TraceSink>(&mut self, command: Command, trace: &mut T) {
        let command_id = match command {
            Command::CreateProject { command_id, .. }
            | Command::UpdateProjectRepository { command_id, .. }
            | Command::DeleteProject { command_id, .. }
            | Command::SubmitBuild { command_id, .. }
            | Command::SubmitProjectBuild { command_id, .. }
            | Command::CancelBuild { command_id, .. }
            | Command::ProcessFinished { command_id, .. }
            | Command::CreateDeployment { command_id, .. }
            | Command::RuntimeReady { command_id, .. }
            | Command::RuntimeExited { command_id, .. }
            | Command::StopDeployment { command_id, .. }
            | Command::Pause { command_id }
            | Command::Resume { command_id }
            | Command::Drain { command_id } => command_id,
        };
        if self.seen_commands.contains(&command_id) {
            return;
        }
        if self.seen_commands.len() >= MAX_COMMANDS {
            self.reject(command_id, RejectReason::Capacity);
            return;
        }
        self.seen_commands.push(command_id);

        match &command {
            Command::CreateProject { .. } => self.apply_createproject(command, trace),
            Command::UpdateProjectRepository { .. } => {
                self.apply_updateprojectrepository(command, trace);
            }
            Command::DeleteProject { .. } => self.apply_deleteproject(&command, trace),
            Command::SubmitBuild { .. } | Command::SubmitProjectBuild { .. } => {
                self.apply_submitbuild(&command, trace);
            }
            Command::CancelBuild { .. } => self.apply_cancelbuild(&command, trace),
            Command::ProcessFinished { .. } => self.apply_processfinished(&command, trace),
            Command::CreateDeployment { .. } => self.apply_createdeployment(&command, trace),
            Command::RuntimeReady { .. } => self.apply_runtimeready(&command, trace),
            Command::RuntimeExited { .. } => self.apply_runtimeexited(&command, trace),
            Command::StopDeployment { .. } => self.apply_stopdeployment(&command, trace),
            Command::Pause { .. } => self.apply_pause(&command, trace),
            Command::Resume { .. } => self.apply_resume(&command, trace),
            Command::Drain { .. } => self.apply_drain(&command, trace),
        }

        self.start_pending(trace);
        self.start_pending_deployments(trace);
        assert!(
            self.validate_invariants().is_ok(),
            "Janus control-plane invariant violated"
        );
    }

    fn apply_createproject<T: TraceSink>(&mut self, command: Command, _trace: &T) {
        match command {
            Command::CreateProject {
                command_id,
                project,
            } => {
                let project = *project;
                let slot = usize::try_from(project.id.0).unwrap_or(usize::MAX);
                if self.control_mode != ControlMode::Running {
                    self.reject(command_id, self.control_rejection());
                } else if slot >= MAX_BUILDS || self.projects[slot].is_some() {
                    self.reject(command_id, RejectReason::Capacity);
                } else if project.validate().is_err() {
                    self.reject(command_id, RejectReason::InvalidTransition);
                } else {
                    let tenant = project.tenant;
                    self.projects[slot] = Some(project);
                    self.emit_event(Event::ProjectCreated {
                        project: EntityId(u64::try_from(slot).unwrap_or(u64::MAX)),
                        tenant,
                    });
                }
            }
            _ => unreachable!("command variant was pre-dispatched"), // tigerstyle: invariant-checked
        }
    }

    fn apply_updateprojectrepository<T: TraceSink>(&mut self, command: Command, _trace: &T) {
        match command {
            Command::UpdateProjectRepository {
                command_id,
                project,
                tenant,
                repo_provider,
                repo_url,
                repo_branch,
            } => {
                let current = self
                    .projects
                    .get(usize::try_from(project.0).unwrap_or(usize::MAX))
                    .and_then(Option::as_ref)
                    .cloned();
                match current {
                    None => self.reject(command_id, RejectReason::NotFound),
                    Some(current) if current.tenant != tenant => {
                        self.reject(command_id, RejectReason::TenantBoundary);
                    }
                    Some(mut updated) => {
                        updated.repo_provider = repo_provider;
                        updated.repo_url = repo_url;
                        updated.repo_branch = repo_branch;
                        updated.updated_at = self.tick.0;
                        if updated.validate().is_err() {
                            self.reject(command_id, RejectReason::InvalidTransition);
                        } else {
                            self.projects[usize::try_from(project.0).unwrap_or(usize::MAX)] =
                                Some(updated);
                            self.emit_event(Event::ProjectRepositoryUpdated { project });
                        }
                    }
                }
            }
            _ => unreachable!("command variant was pre-dispatched"), // tigerstyle: invariant-checked
        }
    }

    fn apply_deleteproject<T: TraceSink>(&mut self, command: &Command, _trace: &T) {
        match command {
            Command::DeleteProject {
                command_id,
                project,
                tenant,
            } => {
                let command_id = *command_id;
                let project = *project;
                let tenant = *tenant;
                let current = self
                    .projects
                    .get(usize::try_from(project.0).unwrap_or(usize::MAX))
                    .and_then(Option::as_ref)
                    .map(|value| value.tenant);
                match current {
                    None => self.reject(command_id, RejectReason::NotFound),
                    Some(owner) if owner != tenant => {
                        self.reject(command_id, RejectReason::TenantBoundary);
                    }
                    Some(_) => {
                        self.projects[usize::try_from(project.0).unwrap_or(usize::MAX)] = None;
                        self.emit_event(Event::ProjectDeleted { project });
                    }
                }
            }
            _ => unreachable!("command variant was pre-dispatched"), // tigerstyle: invariant-checked
        }
    }

    fn apply_submitbuild<T: TraceSink>(&mut self, command: &Command, trace: &mut T) {
        match command {
            Command::SubmitBuild {
                command_id,
                build,
                tenant,
            } => {
                self.apply_build_submission(*command_id, *build, *tenant, None, None, trace);
            }
            Command::SubmitProjectBuild {
                command_id,
                build,
                tenant,
                project,
                source_ref,
            } => {
                self.apply_build_submission(
                    *command_id,
                    *build,
                    *tenant,
                    Some(*project),
                    Some(source_ref),
                    trace,
                );
            }
            _ => unreachable!("command variant was pre-dispatched"), // tigerstyle: invariant-checked
        }
    }

    fn apply_build_submission<T: TraceSink>(
        &mut self,
        command_id: CommandId,
        build: EntityId,
        tenant: TenantId,
        project: Option<EntityId>,
        source_ref: Option<&String>,
        trace: &mut T,
    ) {
        let slot = usize::try_from(build.0).unwrap_or(usize::MAX);
        if self.control_mode != ControlMode::Running {
            let reason = self.control_rejection();
            self.reject(command_id, reason);
        } else if slot >= MAX_BUILDS
            || self
                .builds
                .iter()
                .flatten()
                .filter(|value| value.tenant == tenant)
                .count()
                >= usize::try_from(self.quota.max_builds).unwrap_or(usize::MAX)
        {
            self.reject(command_id, RejectReason::Capacity);
        } else if self.builds[slot].is_some() {
            self.reject(command_id, RejectReason::InvalidTransition);
        } else {
            self.builds[slot] = Some(Build {
                tenant,
                state: BuildState::Pending,
                generation: Generation(0),
                project,
                source_ref: source_ref.cloned(),
            });
            self.emit_event(Event::BuildAccepted { build, tenant });
            trace.record(TraceRecord {
                operation: "build.accepted",
                tick: self.tick,
                entity: Some(build),
            });
        }
    }

    fn apply_cancelbuild<T: TraceSink>(&mut self, command: &Command, trace: &mut T) {
        match command {
            Command::CancelBuild {
                command_id,
                build,
                tenant,
            } => {
                let command_id = *command_id;
                let build = *build;
                let tenant = *tenant;
                let current = self
                    .builds
                    .get(usize::try_from(build.0).unwrap_or(usize::MAX))
                    .and_then(Option::as_ref)
                    .map(|value| (value.tenant, value.state));
                match current {
                    None => self.reject(command_id, RejectReason::NotFound),
                    Some((owner, _)) if owner != tenant => {
                        self.reject(command_id, RejectReason::TenantBoundary);
                    }
                    Some((_, state)) if state.terminal() => {
                        self.reject(command_id, RejectReason::InvalidTransition);
                    }
                    Some(_) => {
                        if let Some(value) = self
                            .builds
                            .get_mut(usize::try_from(build.0).unwrap_or(usize::MAX))
                            .and_then(Option::as_mut)
                        {
                            value.state = BuildState::Cancelled;
                        }
                        self.emit_event(Event::BuildCancelled { build });
                        trace.record(TraceRecord {
                            operation: "build.cancelled",
                            tick: self.tick,
                            entity: Some(build),
                        });
                    }
                }
            }
            _ => unreachable!("command variant was pre-dispatched"), // tigerstyle: invariant-checked
        }
    }

    fn apply_processfinished<T: TraceSink>(&mut self, command: &Command, _trace: &T) {
        match command {
            Command::ProcessFinished {
                command_id,
                build,
                tenant,
                generation,
                exit_code,
            } => {
                let command_id = *command_id;
                let build = *build;
                let tenant = *tenant;
                let generation = *generation;
                let exit_code = *exit_code;
                let current = self
                    .builds
                    .get(usize::try_from(build.0).unwrap_or(usize::MAX))
                    .and_then(Option::as_ref)
                    .map(|value| (value.tenant, value.generation, value.state));
                match current {
                    None => self.reject(command_id, RejectReason::NotFound),
                    Some((owner, _, _)) if owner != tenant => {
                        self.reject(command_id, RejectReason::TenantBoundary);
                    }
                    Some((_, current_generation, _)) if current_generation != generation => {
                        self.reject(command_id, RejectReason::StaleGeneration);
                    }
                    Some((_, _, state)) if state != BuildState::Running => {
                        self.reject(command_id, RejectReason::InvalidTransition);
                    }
                    Some(_) if exit_code == 0 => {
                        if let Some(value) = self
                            .builds
                            .get_mut(usize::try_from(build.0).unwrap_or(usize::MAX))
                            .and_then(Option::as_mut)
                        {
                            value.state = BuildState::Succeeded;
                        }
                        self.emit_event(Event::BuildSucceeded { build, generation });
                    }
                    Some(_) => {
                        if let Some(value) = self
                            .builds
                            .get_mut(usize::try_from(build.0).unwrap_or(usize::MAX))
                            .and_then(Option::as_mut)
                        {
                            value.state = BuildState::Failed;
                        }
                        self.emit_event(Event::BuildFailed {
                            build,
                            generation,
                            exit_code,
                        });
                    }
                }
            }
            _ => unreachable!("command variant was pre-dispatched"), // tigerstyle: invariant-checked
        }
    }

    fn apply_createdeployment<T: TraceSink>(&mut self, command: &Command, trace: &mut T) {
        match command {
            Command::CreateDeployment {
                command_id,
                deployment,
                tenant,
                build,
                target_type,
                target_ref,
                preferred_runner,
                environment,
            } => {
                let command_id = *command_id;
                let deployment = *deployment;
                let tenant = *tenant;
                let build = *build;
                let slot = usize::try_from(deployment.0).unwrap_or(usize::MAX);
                let build_ready = self.build(build).is_some_and(|value| {
                    value.tenant == tenant && value.state == BuildState::Succeeded
                });
                if self.control_mode != ControlMode::Running {
                    let reason = self.control_rejection();
                    self.reject(command_id, reason);
                } else if !build_ready {
                    let tenant_matches =
                        self.build(build).is_none_or(|value| value.tenant == tenant);
                    self.reject(
                        command_id,
                        if tenant_matches {
                            RejectReason::BuildNotSucceeded
                        } else {
                            RejectReason::TenantBoundary
                        },
                    );
                } else if self.deployment_capacity_exceeded(tenant, slot) {
                    self.reject(command_id, RejectReason::Capacity);
                } else if self.deployments[slot].is_some() {
                    self.reject(command_id, RejectReason::InvalidTransition);
                } else {
                    let metadata = DeploymentMetadata {
                        target_type: target_type.trim().to_owned(),
                        target_ref: target_ref.trim().to_owned(),
                        preferred_runner: preferred_runner.trim().to_owned(),
                        environment: environment.clone(),
                    };
                    self.insert_deployment(slot, tenant, build, &metadata);
                    self.emit_event(Event::DeploymentAccepted {
                        deployment,
                        tenant,
                        build,
                    });
                    trace.record(TraceRecord {
                        operation: "deployment.accepted",
                        tick: self.tick,
                        entity: Some(deployment),
                    });
                }
            }
            _ => unreachable!("command variant was pre-dispatched"), // tigerstyle: invariant-checked
        }
    }

    fn insert_deployment(
        &mut self,
        slot: usize,
        tenant: TenantId,
        build: EntityId,
        metadata: &DeploymentMetadata,
    ) {
        let project = self.build(build).and_then(|value| value.project);
        self.deployments[slot] = Some(Deployment {
            tenant,
            build,
            project,
            revision: self.next_project_revision(project),
            target_type: metadata.target_type.clone(),
            target_ref: metadata.target_ref.clone(),
            preferred_runner: metadata.preferred_runner.clone(),
            environment: metadata.environment.clone(),
            runtime_id: String::default(),
            runtime_mode: String::default(),
            runtime_endpoint: String::default(),
            runtime_status: String::default(),
            state: DeploymentState::Pending,
            generation: Generation(0),
            resource_claim: None,
        });
    }

    fn deployment_capacity_exceeded(&self, tenant: TenantId, slot: usize) -> bool {
        let deployment_count = self
            .deployments
            .iter()
            .flatten()
            .filter(|value| value.tenant == tenant)
            .count();
        let running_count = self
            .deployments
            .iter()
            .flatten()
            .filter(|value| {
                value.tenant == tenant
                    && matches!(
                        value.state,
                        DeploymentState::Starting | DeploymentState::Running
                    )
            })
            .count();
        slot >= MAX_BUILDS
            || deployment_count >= usize::try_from(self.quota.max_deployments).unwrap_or(usize::MAX)
            || running_count
                >= usize::try_from(self.quota.max_running_processes).unwrap_or(usize::MAX)
    }

    fn next_project_revision(&self, project: Option<EntityId>) -> u64 {
        let Some(project) = project else {
            return 1;
        };
        self.deployments
            .iter()
            .flatten()
            .filter(|deployment| deployment.project == Some(project))
            .map(|deployment| deployment.revision)
            .max()
            .unwrap_or(0)
            .saturating_add(1)
    }

    fn apply_runtimeready<T: TraceSink>(&mut self, command: &Command, _trace: &T) {
        match command {
            Command::RuntimeReady {
                command_id,
                deployment,
                tenant,
                generation,
                runtime_id,
                runtime_mode,
                runtime_endpoint,
            } => {
                let command_id = *command_id;
                let deployment = *deployment;
                let tenant = *tenant;
                let generation = *generation;
                let current = self
                    .deployments
                    .get(usize::try_from(deployment.0).unwrap_or(usize::MAX))
                    .and_then(Option::as_ref)
                    .map(|value| (value.tenant, value.generation, value.state));
                match current {
                    None => self.reject(command_id, RejectReason::NotFound),
                    Some((owner, _, _)) if owner != tenant => {
                        self.reject(command_id, RejectReason::TenantBoundary);
                    }
                    Some((_, current_generation, _)) if current_generation != generation => {
                        self.reject(command_id, RejectReason::StaleGeneration);
                    }
                    Some((_, _, state)) if state != DeploymentState::Starting => {
                        self.reject(command_id, RejectReason::InvalidTransition);
                    }
                    Some(_) => {
                        if let Some(value) = self
                            .deployments
                            .get_mut(usize::try_from(deployment.0).unwrap_or(usize::MAX))
                            .and_then(Option::as_mut)
                        {
                            value.state = DeploymentState::Running;
                            runtime_id.trim().clone_into(&mut value.runtime_id);
                            runtime_mode.trim().clone_into(&mut value.runtime_mode);
                            runtime_endpoint
                                .trim()
                                .clone_into(&mut value.runtime_endpoint);
                            "running".clone_into(&mut value.runtime_status);
                        }
                        self.emit_event(Event::DeploymentRunning {
                            deployment,
                            generation,
                        });
                    }
                }
            }
            _ => unreachable!("command variant was pre-dispatched"), // tigerstyle: invariant-checked
        }
    }

    fn apply_runtimeexited<T: TraceSink>(&mut self, command: &Command, _trace: &T) {
        match command {
            Command::RuntimeExited {
                command_id,
                deployment,
                tenant,
                generation,
            } => {
                let command_id = *command_id;
                let deployment = *deployment;
                let tenant = *tenant;
                let generation = *generation;
                let current = self
                    .deployments
                    .get(usize::try_from(deployment.0).unwrap_or(usize::MAX))
                    .and_then(Option::as_ref)
                    .map(|value| (value.tenant, value.generation, value.state));
                match current {
                    None => self.reject(command_id, RejectReason::NotFound),
                    Some((owner, _, _)) if owner != tenant => {
                        self.reject(command_id, RejectReason::TenantBoundary);
                    }
                    Some((_, current_generation, _)) if current_generation != generation => {
                        self.reject(command_id, RejectReason::StaleGeneration);
                    }
                    Some((_, _, state))
                        if state != DeploymentState::Starting
                            && state != DeploymentState::Running =>
                    {
                        self.reject(command_id, RejectReason::InvalidTransition);
                    }
                    Some(_) => {
                        if let Some(value) = self
                            .deployments
                            .get_mut(usize::try_from(deployment.0).unwrap_or(usize::MAX))
                            .and_then(Option::as_mut)
                        {
                            value.state = DeploymentState::Failed;
                            "failed".clone_into(&mut value.runtime_status);
                            value.resource_claim = None;
                        }
                        self.emit_event(Event::DeploymentFailed {
                            deployment,
                            generation,
                        });
                    }
                }
            }
            _ => unreachable!("command variant was pre-dispatched"), // tigerstyle: invariant-checked
        }
    }

    fn apply_stopdeployment<T: TraceSink>(&mut self, command: &Command, _trace: &T) {
        match command {
            Command::StopDeployment {
                command_id,
                deployment,
                tenant,
            } => {
                let command_id = *command_id;
                let deployment = *deployment;
                let tenant = *tenant;
                let current = self
                    .deployments
                    .get(usize::try_from(deployment.0).unwrap_or(usize::MAX))
                    .and_then(Option::as_ref)
                    .map(|value| (value.tenant, value.generation, value.state));
                match current {
                    None => self.reject(command_id, RejectReason::NotFound),
                    Some((owner, _, _)) if owner != tenant => {
                        self.reject(command_id, RejectReason::TenantBoundary);
                    }
                    Some((_, _, state)) if state.terminal() => {
                        self.reject(command_id, RejectReason::InvalidTransition);
                    }
                    Some((_, generation, state)) => {
                        if let Some(value) = self
                            .deployments
                            .get_mut(usize::try_from(deployment.0).unwrap_or(usize::MAX))
                            .and_then(Option::as_mut)
                        {
                            value.state = DeploymentState::Stopped;
                            "stopped".clone_into(&mut value.runtime_status);
                            value.resource_claim = None;
                        }
                        if state == DeploymentState::Starting || state == DeploymentState::Running {
                            self.emit_effect(Effect::StopRuntime {
                                deployment,
                                tenant,
                                generation,
                            });
                        }
                        self.emit_event(Event::DeploymentStopped { deployment });
                    }
                }
            }
            _ => unreachable!("command variant was pre-dispatched"), // tigerstyle: invariant-checked
        }
    }

    fn apply_pause<T: TraceSink>(&mut self, command: &Command, _trace: &T) {
        match command {
            Command::Pause { .. } => {
                if self.control_mode == ControlMode::Running {
                    self.control_mode = ControlMode::Paused;
                    self.emit_event(Event::ControlPaused);
                }
            }
            _ => unreachable!("command variant was pre-dispatched"), // tigerstyle: invariant-checked
        }
    }

    fn apply_resume<T: TraceSink>(&mut self, command: &Command, _trace: &T) {
        match command {
            Command::Resume { .. } => {
                if self.control_mode == ControlMode::Paused {
                    self.control_mode = ControlMode::Running;
                    self.emit_event(Event::ControlResumed);
                }
            }
            _ => unreachable!("command variant was pre-dispatched"), // tigerstyle: invariant-checked
        }
    }

    fn apply_drain<T: TraceSink>(&mut self, command: &Command, _trace: &T) {
        match command {
            Command::Drain { .. } => {
                if self.control_mode != ControlMode::Draining {
                    self.control_mode = ControlMode::Draining;
                    self.emit_event(Event::ControlDraining);
                }
            }
            _ => unreachable!("command variant was pre-dispatched"), // tigerstyle: invariant-checked
        }
    }

    const fn control_rejection(&self) -> RejectReason {
        match self.control_mode {
            ControlMode::Paused => RejectReason::Paused,
            ControlMode::Draining => RejectReason::Draining,
            ControlMode::Running => RejectReason::InvalidTransition,
        }
    }

    fn start_pending<T: TraceSink>(&mut self, trace: &mut T) {
        let mut started = Vec::with_capacity(MAX_BUILDS);
        for (index, slot) in self.builds.iter_mut().enumerate() {
            if let Some(build) = slot.as_mut() {
                if build.state == BuildState::Pending {
                    build.state = BuildState::Running;
                    build.generation = Generation(build.generation.0.saturating_add(1));
                    started.push((
                        EntityId(u64::try_from(index).unwrap_or(u64::MAX)),
                        build.generation,
                    ));
                }
            }
        }
        for (entity, generation) in started {
            let tenant = self.builds[usize::try_from(entity.0).unwrap_or(usize::MAX)]
                .as_ref()
                .map_or(TenantId(0), |build| build.tenant);
            self.emit_event(Event::BuildStarted {
                build: entity,
                generation,
            });
            self.emit_effect(Effect::RunBuild {
                build: entity,
                tenant,
                generation,
            });
            trace.record(TraceRecord {
                operation: "build.started",
                tick: self.tick,
                entity: Some(entity),
            });
        }
    }

    fn emit_event(&mut self, event: Event) {
        assert!(
            self.events.len() < MAX_COMMANDS.saturating_mul(2),
            "Janus event buffer capacity exceeded"
        );
        self.events.push(event);
    }

    fn emit_effect(&mut self, effect: Effect) {
        assert!(
            self.effects.len() < MAX_COMMANDS,
            "Janus effect buffer capacity exceeded"
        );
        self.effects.push(effect);
    }

    fn reject(&mut self, command_id: CommandId, reason: RejectReason) {
        self.emit_event(Event::CommandRejected { command_id, reason });
    }

    fn start_pending_deployments<T: TraceSink>(&mut self, trace: &mut T) {
        let mut starting = Vec::with_capacity(MAX_BUILDS);
        for (index, slot) in self.deployments.iter_mut().enumerate() {
            if let Some(deployment) = slot.as_mut() {
                if deployment.state == DeploymentState::Pending {
                    deployment.state = DeploymentState::Starting;
                    deployment.generation = Generation(deployment.generation.0.saturating_add(1));
                    deployment.resource_claim = Some(ResourceClaim {
                        resource: EntityId(u64::try_from(index).unwrap_or(u64::MAX)),
                        tenant: deployment.tenant,
                        owner: EntityId(u64::try_from(index).unwrap_or(u64::MAX)),
                        generation: deployment.generation,
                        kind: ResourceKind::Process,
                    });
                    starting.push((
                        EntityId(u64::try_from(index).unwrap_or(u64::MAX)),
                        deployment.tenant,
                        deployment.build,
                        deployment.generation,
                    ));
                }
            }
        }
        for (deployment, tenant, build, generation) in starting {
            self.emit_event(Event::DeploymentStarting {
                deployment,
                generation,
            });
            self.emit_effect(Effect::StartRuntime {
                deployment,
                tenant,
                build,
                generation,
            });
            trace.record(TraceRecord {
                operation: "deployment.starting",
                tick: self.tick,
                entity: Some(deployment),
            });
        }
    }
}

impl<const MAX_BUILDS: usize, const MAX_COMMANDS: usize> Default
    for World<MAX_BUILDS, MAX_COMMANDS>
{
    fn default() -> Self {
        Self::new()
    }
}

/// In-memory trace sink used by tests and deterministic replay.
#[derive(Default)]
pub struct MemoryTrace {
    samples: Vec<TraceRecord>,
}

impl MemoryTrace {
    /// Returns captured samples without exposing mutable buffer ownership.
    #[must_use]
    pub fn samples(&self) -> &[TraceRecord] {
        &self.samples
    }
}

impl TraceSink for MemoryTrace {
    fn record(&mut self, sample: TraceRecord) {
        assert!(
            self.samples.len() < MAX_TRACE_SAMPLES,
            "Janus trace sample capacity exceeded"
        );
        self.samples.push(sample);
    }
}

/// Deterministic build simulation with a controllable process result.
pub struct Simulator<const MAX_BUILDS: usize, const MAX_COMMANDS: usize> {
    /// Actor-owned control-plane world.
    pub world: World<MAX_BUILDS, MAX_COMMANDS>,
    /// Captured diagnostics.
    pub trace: MemoryTrace,
    /// Per-tick deterministic work samples.
    performance: Vec<PerformanceSample>,
    /// Versioned event journal used by simulation persistence.
    journal: EventJournal<MAX_COMMANDS>,
    /// Replay records sufficient to diagnose scheduler/effect fault behavior.
    replay: Vec<ReplayRecord>,
    faults: FaultPlan,
    pending_external: Vec<Command>,
}

impl<const MAX_BUILDS: usize, const MAX_COMMANDS: usize> Simulator<MAX_BUILDS, MAX_COMMANDS> {
    /// Creates an empty deterministic simulator.
    #[must_use]
    pub fn new() -> Self {
        Self {
            world: World::new(),
            trace: MemoryTrace::default(),
            performance: Vec::with_capacity(MAX_SIMULATION_STEPS),
            journal: EventJournal::new(),
            replay: Vec::with_capacity(MAX_SIMULATION_STEPS),
            faults: FaultPlan::default(),
            pending_external: Vec::with_capacity(MAX_COMMANDS),
        }
    }

    /// Causes the next simulated process to fail.
    pub const fn fail_next_process(&mut self) {
        self.faults.fail_next_process = true;
    }

    /// Submits an external command and includes it in the replay transcript.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn submit(&mut self, command: Command) -> Result<(), Command> {
        self.world.enqueue(command.clone())?;
        self.pending_external.push(command);
        Ok(())
    }

    /// Sets the complete fault plan for the next simulation step.
    pub const fn set_fault_plan(&mut self, faults: FaultPlan) {
        self.faults = faults;
    }

    /// Drops the next external effect to exercise recovery behavior.
    pub const fn drop_next_effect(&mut self) {
        self.faults.drop_next_effect = true;
    }

    /// Duplicates the next completion to exercise idempotency behavior.
    pub const fn duplicate_next_effect(&mut self) {
        self.faults.duplicate_next_effect = true;
    }

    /// Returns retained performance samples.
    #[must_use]
    pub fn performance_samples(&self) -> &[PerformanceSample] {
        &self.performance
    }

    /// Returns the versioned event journal.
    #[must_use]
    pub const fn journal(&self) -> &EventJournal<MAX_COMMANDS> {
        &self.journal
    }

    /// Returns the deterministic replay transcript.
    #[must_use]
    pub fn replay(&self) -> &[ReplayRecord] {
        &self.replay
    }

    /// Runs one actor tick and completes all newly emitted process effects.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn step(&mut self) -> Result<(), SimulationError> {
        if self.replay.len() >= MAX_SIMULATION_STEPS {
            return Err(SimulationError::StepCapacity {
                tick: self.world.performance().tick,
            });
        }
        let external_commands = std::mem::take(&mut self.pending_external);
        let faults = self.faults;
        self.world.tick(&mut self.trace);
        self.performance.push(self.world.performance());
        let buffered_events =
            usize::try_from(self.world.performance().buffered_events).unwrap_or(usize::MAX);
        if self.journal.events().len().saturating_add(buffered_events) > MAX_COMMANDS {
            return Err(SimulationError::JournalCapacity {
                tick: self.world.performance().tick,
            });
        }
        let events = self.world.drain_events();
        let persisted_events = u32::try_from(events.len()).unwrap_or(u32::MAX);
        for event in events {
            self.journal
                .append(event)
                .map_err(|_| SimulationError::JournalCapacity {
                    tick: self.world.performance().tick,
                })?;
        }
        let effects = self.world.drain_effects();
        let mut replay = ReplayRecord {
            tick: self.world.performance().tick,
            external_commands,
            faults,
            effects_emitted: u32::try_from(effects.len()).unwrap_or(u32::MAX),
            events_persisted: persisted_events,
            ..ReplayRecord::default()
        };
        replay = self.process_effects(effects, replay);
        self.replay.push(replay);
        Ok(())
    }

    fn process_effects(&mut self, effects: Vec<Effect>, mut replay: ReplayRecord) -> ReplayRecord {
        for effect in effects {
            if self.faults.drop_next_effect {
                self.faults.drop_next_effect = false;
                replay.effects_dropped = replay.effects_dropped.saturating_add(1);
                continue;
            }
            let duplicate = self.faults.duplicate_next_effect;
            self.faults.duplicate_next_effect = false;
            match effect {
                Effect::RunBuild {
                    build,
                    tenant,
                    generation,
                } => self.process_build_effect(build, tenant, generation, duplicate, &mut replay),
                Effect::StartRuntime {
                    deployment,
                    tenant,
                    generation,
                    ..
                } => self.process_runtime_effect(
                    deployment,
                    tenant,
                    generation,
                    duplicate,
                    &mut replay,
                ),
                Effect::StopRuntime { .. } => {}
            }
        }
        replay
    }

    fn process_build_effect(
        &mut self,
        build: EntityId,
        tenant: TenantId,
        generation: Generation,
        duplicate: bool,
        replay: &mut ReplayRecord,
    ) {
        let exit_code = if self.faults.fail_next_process {
            self.faults.fail_next_process = false;
            1
        } else {
            0
        };
        let command = Command::ProcessFinished {
            command_id: CommandId(10_000 + build.0),
            build,
            tenant,
            generation,
            exit_code,
        };
        if self.world.enqueue(command).is_err() {
            replay.effects_dropped = replay.effects_dropped.saturating_add(1);
        }
        if duplicate {
            replay.effects_duplicated = replay.effects_duplicated.saturating_add(1);
            if self
                .world
                .enqueue(Command::ProcessFinished {
                    command_id: CommandId(10_000 + build.0),
                    build,
                    tenant,
                    generation,
                    exit_code,
                })
                .is_err()
            {
                replay.effects_dropped = replay.effects_dropped.saturating_add(1);
            }
        }
    }

    fn process_runtime_effect(
        &mut self,
        deployment: EntityId,
        tenant: TenantId,
        generation: Generation,
        duplicate: bool,
        replay: &mut ReplayRecord,
    ) {
        let command = || Command::RuntimeReady {
            command_id: CommandId(20_000 + deployment.0),
            deployment,
            tenant,
            generation,
            runtime_id: String::default(),
            runtime_mode: String::default(),
            runtime_endpoint: String::default(),
        };
        if self.world.enqueue(command()).is_err() {
            replay.effects_dropped = replay.effects_dropped.saturating_add(1);
        }
        if duplicate {
            replay.effects_duplicated = replay.effects_duplicated.saturating_add(1);
            if self.world.enqueue(command()).is_err() {
                replay.effects_dropped = replay.effects_dropped.saturating_add(1);
            }
        }
    }

    /// Replays a transcript and rejects the first divergent step.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    ///
    /// # Panics
    ///
    /// Panics only if a successful simulation step fails to append its replay
    /// record, which would violate the simulator's internal invariant.
    pub fn from_replay(records: &[ReplayRecord]) -> Result<Self, ReplayError> {
        let mut simulator = Self::new();
        for expected in records {
            simulator.set_fault_plan(expected.faults);
            for command in expected.external_commands.iter().cloned() {
                simulator
                    .submit(command)
                    .map_err(|_| ReplayError::Capacity {
                        tick: expected.tick,
                    })?;
            }
            simulator.step().map_err(|_| ReplayError::Capacity {
                tick: expected.tick,
            })?;
            let Some(actual) = simulator.replay.last() else {
                return Err(ReplayError::Diverged {
                    tick: expected.tick,
                });
            };
            if actual != expected {
                return Err(ReplayError::Diverged {
                    tick: expected.tick,
                });
            }
        }
        Ok(simulator)
    }
}

/// Failure while advancing a deterministic simulation.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum SimulationError {
    /// The simulator reached its retained transcript capacity.
    StepCapacity {
        /// Tick that could not be retained.
        tick: Tick,
    },
    /// The bounded event journal cannot retain the next tick's events.
    JournalCapacity {
        /// Tick whose events could not be persisted.
        tick: Tick,
    },
}

/// Failure while replaying a deterministic simulation transcript.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ReplayError {
    /// A replay command exceeded the configured mailbox capacity.
    Capacity {
        /// Tick at which the command could not be admitted.
        tick: Tick,
    },
    /// Regenerated state or effects diverged from the transcript.
    Diverged {
        /// First divergent tick.
        tick: Tick,
    },
}

impl<const MAX_BUILDS: usize, const MAX_COMMANDS: usize> Default
    for Simulator<MAX_BUILDS, MAX_COMMANDS>
{
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    type TestWorld = World<8, 16>;

    #[test]
    fn build_lifecycle_is_deterministic() {
        let mut simulator = Simulator::<8, 16>::new();
        simulator
            .submit(Command::SubmitBuild {
                command_id: CommandId(1),
                build: EntityId(2),
                tenant: TenantId(1),
            })
            .unwrap();
        simulator.step().unwrap();
        assert_eq!(
            simulator.world.build(EntityId(2)).map(|value| value.state),
            Some(BuildState::Running)
        );
        simulator.step().unwrap();
        assert_eq!(
            simulator.world.build(EntityId(2)).map(|value| value.state),
            Some(BuildState::Succeeded)
        );
    }

    #[test]
    fn stale_generation_cannot_complete_a_build() {
        let mut world = TestWorld::new();
        let mut trace = MemoryTrace::default();
        world
            .enqueue(Command::SubmitBuild {
                command_id: CommandId(1),
                build: EntityId(1),
                tenant: TenantId(1),
            })
            .unwrap();
        world.tick(&mut trace);
        world
            .enqueue(Command::ProcessFinished {
                command_id: CommandId(2),
                build: EntityId(1),
                tenant: TenantId(1),
                generation: Generation(99),
                exit_code: 0,
            })
            .unwrap();
        world.tick(&mut trace);
        assert_eq!(
            world.build(EntityId(1)).map(|value| value.state),
            Some(BuildState::Running)
        );
        assert!(world.drain_events().contains(&Event::CommandRejected {
            command_id: CommandId(2),
            reason: RejectReason::StaleGeneration
        }));
    }

    #[test]
    fn duplicate_commands_are_idempotent() {
        let mut world = TestWorld::new();
        let mut trace = MemoryTrace::default();
        let command = Command::SubmitBuild {
            command_id: CommandId(7),
            build: EntityId(1),
            tenant: TenantId(1),
        };
        world.enqueue(command.clone()).unwrap();
        world.enqueue(command).unwrap();
        world.tick(&mut trace);
        assert_eq!(
            world
                .drain_events()
                .iter()
                .filter(|event| matches!(event, Event::BuildAccepted { .. }))
                .count(),
            1
        );
    }

    #[test]
    fn tenant_boundary_is_enforced() {
        let mut world = TestWorld::new();
        let mut trace = MemoryTrace::default();
        world
            .enqueue(Command::SubmitBuild {
                command_id: CommandId(1),
                build: EntityId(1),
                tenant: TenantId(1),
            })
            .unwrap();
        world.tick(&mut trace);
        world
            .enqueue(Command::CancelBuild {
                command_id: CommandId(2),
                build: EntityId(1),
                tenant: TenantId(2),
            })
            .unwrap();
        world.tick(&mut trace);
        assert_eq!(
            world.build(EntityId(1)).map(|value| value.state),
            Some(BuildState::Running)
        );
        assert!(world.drain_events().contains(&Event::CommandRejected {
            command_id: CommandId(2),
            reason: RejectReason::TenantBoundary
        }));
    }

    #[test]
    fn authorized_admission_enforces_permission_and_tenant_before_queueing() {
        let mut world = TestWorld::new();
        let principal =
            Principal::new(SubjectId(9), TenantId(1)).with_permission(Permission::SubmitBuilds);
        assert!(world
            .enqueue_authorized(
                &principal,
                Command::SubmitBuild {
                    command_id: CommandId(1),
                    build: EntityId(1),
                    tenant: TenantId(1),
                },
            )
            .is_ok());
        assert_eq!(
            world.enqueue_authorized(
                &principal,
                Command::SubmitBuild {
                    command_id: CommandId(2),
                    build: EntityId(2),
                    tenant: TenantId(2),
                },
            ),
            Err(AdmissionError::Authorization(
                AuthorizationError::TenantBoundary
            ))
        );
        assert_eq!(
            world.enqueue_authorized(
                &principal,
                Command::CancelBuild {
                    command_id: CommandId(3),
                    build: EntityId(1),
                    tenant: TenantId(1),
                },
            ),
            Err(AdmissionError::Authorization(
                AuthorizationError::PermissionDenied
            ))
        );
    }

    #[test]
    fn deployment_requires_succeeded_build_and_reaches_running() {
        let mut simulator = Simulator::<8, 16>::new();
        simulator
            .submit(Command::SubmitBuild {
                command_id: CommandId(1),
                build: EntityId(1),
                tenant: TenantId(7),
            })
            .unwrap();
        simulator.step().unwrap();
        simulator.step().unwrap();
        simulator
            .submit(Command::CreateDeployment {
                command_id: CommandId(2),
                deployment: EntityId(2),
                tenant: TenantId(7),
                build: EntityId(1),
                target_type: String::default(),
                target_ref: String::default(),
                preferred_runner: String::default(),
                environment: Vec::from([]),
            })
            .unwrap();
        simulator.step().unwrap();
        assert_eq!(
            simulator
                .world
                .deployment(EntityId(2))
                .map(|value| value.state),
            Some(DeploymentState::Starting)
        );
        assert_eq!(
            simulator
                .world
                .deployment(EntityId(2))
                .and_then(|value| value.resource_claim)
                .map(|claim| (claim.owner, claim.generation, claim.kind)),
            Some((EntityId(2), Generation(1), ResourceKind::Process))
        );
        simulator.step().unwrap();
        assert_eq!(
            simulator
                .world
                .deployment(EntityId(2))
                .map(|value| value.state),
            Some(DeploymentState::Running)
        );
    }

    #[test]
    fn pause_rejects_new_builds_and_resume_reopens_admission() {
        let mut world = TestWorld::new();
        let mut trace = MemoryTrace::default();
        world
            .enqueue(Command::Pause {
                command_id: CommandId(1),
            })
            .unwrap();
        world
            .enqueue(Command::SubmitBuild {
                command_id: CommandId(2),
                build: EntityId(1),
                tenant: TenantId(1),
            })
            .unwrap();
        world.tick(&mut trace);
        assert_eq!(world.control_mode(), ControlMode::Paused);
        assert!(world.drain_events().contains(&Event::CommandRejected {
            command_id: CommandId(2),
            reason: RejectReason::Paused
        }));
        world
            .enqueue(Command::Resume {
                command_id: CommandId(3),
            })
            .unwrap();
        world
            .enqueue(Command::SubmitBuild {
                command_id: CommandId(4),
                build: EntityId(1),
                tenant: TenantId(1),
            })
            .unwrap();
        world.tick(&mut trace);
        assert_eq!(world.control_mode(), ControlMode::Running);
        assert_eq!(
            world.build(EntityId(1)).map(|value| value.state),
            Some(BuildState::Running)
        );
    }

    #[test]
    fn stale_resource_owner_cannot_release_claim() {
        let mut registry = ResourceRegistry::<2>::new();
        registry
            .claim(ResourceClaim {
                resource: EntityId(9),
                tenant: TenantId(1),
                owner: EntityId(3),
                generation: Generation(4),
                kind: ResourceKind::Process,
            })
            .unwrap();
        assert_eq!(
            registry.release(EntityId(9), TenantId(1), EntityId(3), Generation(5)),
            Err(ClaimError::StaleGeneration)
        );
        assert_eq!(
            registry.release(EntityId(9), TenantId(2), EntityId(3), Generation(4)),
            Err(ClaimError::TenantBoundary)
        );
        assert!(registry.get(EntityId(9)).is_some());
        registry
            .release(EntityId(9), TenantId(1), EntityId(3), Generation(4))
            .unwrap();
        assert!(registry.get(EntityId(9)).is_none());
    }

    #[test]
    fn simulator_replays_dropped_and_duplicate_effects() {
        let mut duplicate = Simulator::<8, 16>::new();
        duplicate
            .submit(Command::SubmitBuild {
                command_id: CommandId(1),
                build: EntityId(1),
                tenant: TenantId(1),
            })
            .unwrap();
        duplicate.duplicate_next_effect();
        duplicate.step().unwrap();
        assert_eq!(duplicate.replay()[0].effects_duplicated, 1);
        assert_eq!(duplicate.replay()[0].events_persisted, 2);
        assert_eq!(duplicate.journal().events().len(), 2);
        duplicate.step().unwrap();
        assert_eq!(
            duplicate.world.build(EntityId(1)).map(|value| value.state),
            Some(BuildState::Succeeded)
        );

        let mut dropped = Simulator::<8, 16>::new();
        dropped
            .submit(Command::SubmitBuild {
                command_id: CommandId(1),
                build: EntityId(1),
                tenant: TenantId(1),
            })
            .unwrap();
        dropped.drop_next_effect();
        dropped.step().unwrap();
        assert_eq!(dropped.replay()[0].effects_dropped, 1);
        assert_eq!(
            dropped.world.build(EntityId(1)).map(|value| value.state),
            Some(BuildState::Running)
        );
    }

    #[test]
    fn replay_regenerates_the_same_transcript() {
        let mut original = Simulator::<8, 16>::new();
        original
            .submit(Command::SubmitBuild {
                command_id: CommandId(1),
                build: EntityId(1),
                tenant: TenantId(1),
            })
            .unwrap();
        original.duplicate_next_effect();
        original.step().unwrap();
        original.step().unwrap();
        let replayed = Simulator::<8, 16>::from_replay(original.replay()).unwrap();
        assert_eq!(replayed.replay(), original.replay());
        assert_eq!(
            replayed.world.build(EntityId(1)).map(|value| value.state),
            Some(BuildState::Succeeded)
        );
    }

    #[test]
    fn command_envelope_rejects_unknown_versions() {
        let envelope = CommandEnvelope {
            version: ProtocolVersion(CURRENT_PROTOCOL_VERSION + 1),
            command: Command::Pause {
                command_id: CommandId(1),
            },
        };
        assert_eq!(
            envelope.validate(),
            Err(EnvelopeError::UnsupportedVersion(ProtocolVersion(
                CURRENT_PROTOCOL_VERSION + 1
            )))
        );
    }

    #[test]
    fn versioned_authenticated_admission_checks_schema_and_principal() {
        let mut world = TestWorld::new();
        let principal =
            Principal::new(SubjectId(4), TenantId(2)).with_permission(Permission::SubmitBuilds);
        let envelope = CommandEnvelope {
            version: ProtocolVersion(CURRENT_PROTOCOL_VERSION),
            command: Command::SubmitBuild {
                command_id: CommandId(10),
                build: EntityId(1),
                tenant: TenantId(2),
            },
        };
        assert!(world
            .enqueue_envelope_authorized(&principal, envelope)
            .is_ok());
        let unsupported = CommandEnvelope {
            version: ProtocolVersion(CURRENT_PROTOCOL_VERSION + 1),
            command: Command::SubmitBuild {
                command_id: CommandId(11),
                build: EntityId(2),
                tenant: TenantId(2),
            },
        };
        assert_eq!(
            world.enqueue_envelope_authorized(&principal, unsupported),
            Err(EnvelopeAdmissionError::Envelope(
                EnvelopeError::UnsupportedVersion(ProtocolVersion(CURRENT_PROTOCOL_VERSION + 1))
            ))
        );
    }

    #[test]
    fn snapshot_preserves_state_and_idempotency() {
        let mut source = TestWorld::new();
        let mut trace = MemoryTrace::default();
        source
            .enqueue(Command::SubmitBuild {
                command_id: CommandId(1),
                build: EntityId(1),
                tenant: TenantId(1),
            })
            .unwrap();
        source.tick(&mut trace);
        let snapshot = source.snapshot();
        let mut restored = TestWorld::new();
        restored.restore(snapshot).unwrap();
        restored
            .enqueue(Command::SubmitBuild {
                command_id: CommandId(1),
                build: EntityId(1),
                tenant: TenantId(1),
            })
            .unwrap();
        restored.tick(&mut trace);
        assert_eq!(
            restored.build(EntityId(1)).map(|value| value.state),
            Some(BuildState::Running)
        );
        assert!(restored.drain_events().is_empty());
    }

    #[test]
    fn snapshot_recovery_requeues_incomplete_builds_and_deployments() {
        let mut simulator = Simulator::<8, 16>::new();
        simulator
            .submit(Command::SubmitBuild {
                command_id: CommandId(1),
                build: EntityId(1),
                tenant: TenantId(7),
            })
            .unwrap();
        simulator.step().unwrap();
        simulator.step().unwrap();
        simulator
            .submit(Command::CreateDeployment {
                command_id: CommandId(2),
                deployment: EntityId(2),
                tenant: TenantId(7),
                build: EntityId(1),
                target_type: String::default(),
                target_ref: String::default(),
                preferred_runner: String::default(),
                environment: Vec::from([]),
            })
            .unwrap();
        simulator.step().unwrap();
        simulator
            .submit(Command::SubmitBuild {
                command_id: CommandId(3),
                build: EntityId(3),
                tenant: TenantId(7),
            })
            .unwrap();
        simulator.step().unwrap();

        let mut snapshot = simulator.world.snapshot();
        // Model the durable state observed by reconciliation after a crash:
        // the runtime start was committed, but its completion was not.
        if let Some(deployment) = snapshot.deployments[2].as_mut() {
            deployment.state = DeploymentState::Starting;
        }
        snapshot.builds[3] = Some(Build {
            tenant: TenantId(7),
            state: BuildState::Running,
            generation: Generation(1),
            project: None,
            source_ref: None,
        });

        let mut recovered = TestWorld::new();
        let report = recovered.restore_and_recover(snapshot).unwrap();
        assert_eq!(
            report,
            RecoveryReport {
                builds_requeued: 1,
                deployments_requeued: 1,
            }
        );
        assert_eq!(
            recovered.build(EntityId(3)).map(|value| value.state),
            Some(BuildState::Pending)
        );
        assert_eq!(
            recovered
                .deployment(EntityId(2))
                .map(|value| (value.state, value.resource_claim)),
            Some((DeploymentState::Pending, None))
        );
        recovered.validate_invariants().unwrap();
    }

    #[test]
    fn operation_components_are_bounded_snapshotable_and_monotonic() {
        let mut world = TestWorld::new();
        let operation = Operation::new("op-1", "build.enqueue", "req-1", "tenant-1", 1).unwrap();
        world
            .insert_operation(EntityId(1), operation.clone())
            .unwrap();
        assert_eq!(world.operation(EntityId(1)), Some(&operation));
        let processing = operation.clone().mark_processing(2).unwrap();
        world
            .replace_operation(EntityId(1), processing.clone())
            .unwrap();
        let succeeded = processing.complete_success("accepted", 3).unwrap();
        world
            .replace_operation(EntityId(1), succeeded.clone())
            .unwrap();
        assert_eq!(world.operation(EntityId(1)), Some(&succeeded));
        assert_eq!(
            world.replace_operation(EntityId(1), operation),
            Err(OperationStoreError::StatusRegression)
        );
        let snapshot = world.snapshot();
        let mut restored = TestWorld::new();
        restored.restore(snapshot).unwrap();
        assert_eq!(restored.operation(EntityId(1)), Some(&succeeded));
    }

    #[test]
    fn project_components_are_validated_tenant_owned_and_snapshotable() {
        let mut world = TestWorld::new();
        let project = Project {
            id: EntityId(2),
            tenant: TenantId(7),
            name: "Janus".to_owned(),
            slug: "janus".to_owned(),
            description: "control plane".to_owned(),
            status: "active".to_owned(),
            repo_provider: "github".to_owned(),
            repo_url: "https://github.com/example/janus".to_owned(),
            repo_branch: "main".to_owned(),
            repo_check: None,
            created_at: 1,
            updated_at: 1,
        };
        world.insert_project(project.clone()).unwrap();
        assert_eq!(world.project(EntityId(2)), Some(&project));
        assert_eq!(
            world.insert_project(project),
            Err(ProjectError::AlreadyExists)
        );
        let snapshot = world.snapshot();
        let mut restored = TestWorld::new();
        restored.restore(snapshot).unwrap();
        assert_eq!(
            restored.project(EntityId(2)).map(|value| value.tenant),
            Some(TenantId(7))
        );
    }

    #[test]
    fn project_commands_enforce_tenant_and_emit_lifecycle_events() {
        let mut world = TestWorld::new();
        let principal =
            Principal::new(SubjectId(1), TenantId(7)).with_permission(Permission::ManageProjects);
        let project = Project {
            id: EntityId(2),
            tenant: TenantId(7),
            name: "Janus".to_owned(),
            slug: "janus".to_owned(),
            description: String::default(),
            status: "active".to_owned(),
            repo_provider: "github".to_owned(),
            repo_url: "https://github.com/example/janus".to_owned(),
            repo_branch: "main".to_owned(),
            repo_check: None,
            created_at: 1,
            updated_at: 1,
        };
        world
            .enqueue_authorized(
                &principal,
                Command::CreateProject {
                    command_id: CommandId(30),
                    project: Box::new(project),
                },
            )
            .unwrap();
        let mut trace = MemoryTrace::default();
        world.tick(&mut trace);
        assert!(world.drain_events().contains(&Event::ProjectCreated {
            project: EntityId(2),
            tenant: TenantId(7),
        }));
        world
            .enqueue_authorized(
                &principal,
                Command::UpdateProjectRepository {
                    command_id: CommandId(31),
                    project: EntityId(2),
                    tenant: TenantId(7),
                    repo_provider: "github".to_owned(),
                    repo_url: "https://github.com/example/janus2".to_owned(),
                    repo_branch: "develop".to_owned(),
                },
            )
            .unwrap();
        world.tick(&mut trace);
        assert_eq!(
            world
                .project(EntityId(2))
                .map(|value| value.repo_branch.as_str()),
            Some("develop")
        );
    }

    #[test]
    fn tenant_quota_rejects_second_build() {
        let mut world = TestWorld::with_quota(TenantQuota {
            max_builds: 1,
            max_deployments: 1,
            max_running_processes: 1,
        });
        let mut trace = MemoryTrace::default();
        world
            .enqueue(Command::SubmitBuild {
                command_id: CommandId(1),
                build: EntityId(1),
                tenant: TenantId(3),
            })
            .unwrap();
        world
            .enqueue(Command::SubmitBuild {
                command_id: CommandId(2),
                build: EntityId(2),
                tenant: TenantId(3),
            })
            .unwrap();
        world.tick(&mut trace);
        assert_eq!(
            world.build(EntityId(1)).map(|value| value.state),
            Some(BuildState::Running)
        );
        assert!(world.drain_events().contains(&Event::CommandRejected {
            command_id: CommandId(2),
            reason: RejectReason::Capacity
        }));
    }

    #[test]
    fn one_tick_can_emit_two_events_per_admitted_build_without_reallocation() {
        let mut world = World::<8, 8>::new();
        let mut trace = MemoryTrace::default();
        for index in 0..8 {
            world
                .enqueue(Command::SubmitBuild {
                    command_id: CommandId(u64::try_from(index).unwrap_or(u64::MAX) + 1),
                    build: EntityId(u64::try_from(index).unwrap_or(u64::MAX)),
                    tenant: TenantId(1),
                })
                .unwrap();
        }
        world.tick(&mut trace);
        assert_eq!(world.drain_events().len(), 16);
        assert_eq!(world.performance().buffered_events, 16);
        assert!(world.validate_invariants().is_ok());
    }
}
