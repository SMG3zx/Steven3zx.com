//! Transactional application seams between ECS services and `SpacetimeDB`.
//!
//! The local ECS is used as the deterministic execution model. A durable
//! reducer is called for each mutation, and a failed reducer call rolls the
//! local world back to its pre-mutation snapshot. This keeps the boundary
//! explicit until generated `SpacetimeDB` bindings are available.

use crate::{
    BuildService, BuildServiceError, DeploymentMetadata, DeploymentRuntimeWrite, EntityId,
    GeneratedSpacetimeDb, Principal, Project, ProjectService, ProjectServiceError, ProjectWrite,
    SnapshotError, SpacetimeDbDomainReducerClient, SpacetimeDbError, TenantId,
};
use crate::{BuildWrite, DeploymentWrite};

/// Object-safe reducer port used by HTTP/application adapters.
pub trait ProjectReducerPort: Send {
    /// Commits a project creation projection.
    ///
    /// # Errors
    ///
    /// Returns the reducer failure when the durable mutation is rejected.
    fn create_project(
        &mut self,
        id: u64,
        tenant: TenantId,
        project: ProjectWrite,
    ) -> Result<(), SpacetimeDbError>;

    /// Commits a project repository update projection.
    ///
    /// # Errors
    ///
    /// Returns the reducer failure when the durable mutation is rejected.
    fn update_project(
        &mut self,
        id: u64,
        tenant: TenantId,
        project: ProjectWrite,
        status: &str,
    ) -> Result<(), SpacetimeDbError>;

    /// Commits a project deletion.
    ///
    /// # Errors
    ///
    /// Returns the reducer failure when the durable mutation is rejected.
    fn delete_project(&mut self, id: u64, tenant: TenantId) -> Result<(), SpacetimeDbError>;
}

impl<C> ProjectReducerPort for GeneratedSpacetimeDb<C>
where
    C: SpacetimeDbDomainReducerClient + Send,
{
    fn create_project(
        &mut self,
        id: u64,
        tenant: TenantId,
        project: ProjectWrite,
    ) -> Result<(), SpacetimeDbError> {
        self.create_project(id, tenant, project)
    }

    fn update_project(
        &mut self,
        id: u64,
        tenant: TenantId,
        project: ProjectWrite,
        status: &str,
    ) -> Result<(), SpacetimeDbError> {
        self.update_project(id, tenant, project, status)
    }

    fn delete_project(&mut self, id: u64, tenant: TenantId) -> Result<(), SpacetimeDbError> {
        self.delete_project(id, tenant)
    }
}

/// Failure while coordinating a local ECS mutation with a durable reducer.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DurableProjectError {
    /// The local ECS rejected the mutation.
    Local(ProjectServiceError),
    /// The durable reducer rejected the mutation.
    Persistence(SpacetimeDbError),
    /// The compensating local rollback failed.
    Rollback(SnapshotError),
}

/// Project service with an explicit `SpacetimeDB` reducer commit boundary.
pub struct DurableProjectService<C, const MAX_ENTITIES: usize, const MAX_COMMANDS: usize> {
    local: ProjectService<MAX_ENTITIES, MAX_COMMANDS>,
    durable: GeneratedSpacetimeDb<C>,
}

impl<C, const MAX_ENTITIES: usize, const MAX_COMMANDS: usize>
    DurableProjectService<C, MAX_ENTITIES, MAX_COMMANDS>
where
    C: SpacetimeDbDomainReducerClient,
{
    /// Creates an empty service pair.
    #[must_use]
    pub fn new(client: C) -> Self {
        Self {
            local: ProjectService::new(),
            durable: GeneratedSpacetimeDb::new(client),
        }
    }

    /// Creates a project and commits its durable repository projection.
    ///
    /// # Errors
    ///
    /// Returns an error when either boundary rejects the mutation or the
    /// local compensating rollback cannot restore the previous snapshot.
    pub fn create(
        &mut self,
        principal: &Principal,
        project: Project,
    ) -> Result<EntityId, DurableProjectError> {
        let snapshot = self.local.snapshot();
        let id = self
            .local
            .create(principal, project)
            .map_err(DurableProjectError::Local)?;
        let projected = self
            .local
            .project(principal, id)
            .map_err(DurableProjectError::Local)?;
        if let Err(error) = self.durable.create_project(
            id.0,
            projected.tenant,
            ProjectWrite {
                name: projected.name.clone(),
                slug: projected.slug.clone(),
                description: projected.description.clone(),
                repo_provider: projected.repo_provider.clone(),
                repository: projected.repo_url.clone(),
                branch: projected.repo_branch.clone(),
                created_at: projected.created_at,
                updated_at: projected.updated_at,
            },
        ) {
            return self.rollback(snapshot, DurableProjectError::Persistence(error));
        }
        Ok(id)
    }

    /// Updates a project and commits its durable repository projection.
    ///
    /// # Errors
    ///
    /// Returns an error when either boundary rejects the mutation or the
    /// local compensating rollback cannot restore the previous snapshot.
    pub fn update_repository(
        &mut self,
        principal: &Principal,
        project: EntityId,
        repo_provider: String,
        repo_url: String,
        repo_branch: String,
    ) -> Result<(), DurableProjectError> {
        let snapshot = self.local.snapshot();
        self.local
            .update_repository(principal, project, repo_provider, repo_url, repo_branch)
            .map_err(DurableProjectError::Local)?;
        let projected = self
            .local
            .project(principal, project)
            .map_err(DurableProjectError::Local)?;
        if let Err(error) = self.durable.update_project(
            project.0,
            projected.tenant,
            ProjectWrite {
                name: projected.name.clone(),
                slug: projected.slug.clone(),
                description: projected.description.clone(),
                repo_provider: projected.repo_provider.clone(),
                repository: projected.repo_url.clone(),
                branch: projected.repo_branch.clone(),
                created_at: projected.created_at,
                updated_at: projected.updated_at,
            },
            &projected.status,
        ) {
            return self.rollback(snapshot, DurableProjectError::Persistence(error));
        }
        Ok(())
    }

    /// Deletes a project and commits the durable delete reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when either boundary rejects the mutation or the
    /// local compensating rollback cannot restore the previous snapshot.
    pub fn delete(
        &mut self,
        principal: &Principal,
        project: EntityId,
    ) -> Result<(), DurableProjectError> {
        let snapshot = self.local.snapshot();
        self.local
            .delete(principal, project)
            .map_err(DurableProjectError::Local)?;
        if let Err(error) = self.durable.delete_project(project.0, principal.tenant) {
            return self.rollback(snapshot, DurableProjectError::Persistence(error));
        }
        Ok(())
    }

    /// Returns the local ECS projection for read-side adapters.
    #[must_use]
    pub const fn local(&self) -> &ProjectService<MAX_ENTITIES, MAX_COMMANDS> {
        &self.local
    }

    /// Returns mutable access to the generated client boundary.
    pub const fn durable_mut(&mut self) -> &mut GeneratedSpacetimeDb<C> {
        &mut self.durable
    }

    fn rollback<T>(
        &mut self,
        snapshot: crate::WorldSnapshot,
        error: DurableProjectError,
    ) -> Result<T, DurableProjectError> {
        self.local.restore(snapshot).map_or_else(
            |rollback| Err(DurableProjectError::Rollback(rollback)),
            |()| Err(error),
        )
    }
}

/// Failure while coordinating a local build/deployment mutation with a
/// durable reducer.
#[derive(Debug)]
pub enum DurableBuildError {
    /// The local ECS rejected the mutation.
    Local(BuildServiceError),
    /// The durable reducer rejected the mutation.
    Persistence(SpacetimeDbError),
    /// The compensating local rollback failed.
    Rollback(SnapshotError),
    /// ECS deployment metadata could not be encoded for the reducer payload.
    Serialization(serde_json::Error),
    /// Recovered work could not be handed to the worker queue.
    Queue(RecoveryQueueError),
}

/// Failure returned by a worker queue during recovery handoff.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RecoveryQueueError {
    /// The queue has no capacity for the recovered item.
    Capacity,
    /// The queue adapter is temporarily unavailable.
    Unavailable,
}

/// Queue boundary for generation-fenced recovery work.
pub trait RecoveryQueuePort {
    /// Enqueues a recovered build generation.
    ///
    /// # Errors
    ///
    /// Returns the queue failure when the item cannot be admitted.
    fn enqueue_build(
        &mut self,
        build: EntityId,
        tenant: TenantId,
        generation: crate::Generation,
    ) -> Result<(), RecoveryQueueError>;

    /// Enqueues a recovered deployment generation.
    ///
    /// # Errors
    ///
    /// Returns the queue failure when the item cannot be admitted.
    fn enqueue_deployment(
        &mut self,
        deployment: EntityId,
        tenant: TenantId,
        generation: crate::Generation,
    ) -> Result<(), RecoveryQueueError>;
}

/// Build/deployment service with an explicit durable reducer commit boundary.
pub struct DurableBuildService<C, const MAX_ENTITIES: usize, const MAX_COMMANDS: usize> {
    local: BuildService<MAX_ENTITIES, MAX_COMMANDS>,
    durable: GeneratedSpacetimeDb<C>,
}

/// Startup and periodic-reconciliation boundary for durable worker recovery.
pub struct RecoveryReconciler<C, Q, const MAX_ENTITIES: usize, const MAX_COMMANDS: usize> {
    service: DurableBuildService<C, MAX_ENTITIES, MAX_COMMANDS>,
    queue: Q,
}

impl<C, Q, const MAX_ENTITIES: usize, const MAX_COMMANDS: usize>
    RecoveryReconciler<C, Q, MAX_ENTITIES, MAX_COMMANDS>
where
    C: SpacetimeDbDomainReducerClient,
    Q: RecoveryQueuePort,
{
    /// Creates a recovery boundary with its durable reducer client and queue.
    #[must_use]
    pub const fn new(
        service: DurableBuildService<C, MAX_ENTITIES, MAX_COMMANDS>,
        queue: Q,
    ) -> Self {
        Self { service, queue }
    }

    /// Runs the first recovery pass before workers accept new work.
    ///
    /// # Errors
    ///
    /// Returns a durable, validation, rollback, or queue handoff failure.
    pub fn startup(
        &mut self,
        snapshot: (crate::WorldSnapshot, u64, u64),
    ) -> Result<crate::RecoveryReport, DurableBuildError> {
        self.reconcile(snapshot)
    }

    /// Runs one bounded periodic reconciliation pass.
    ///
    /// # Errors
    ///
    /// Returns a durable, validation, rollback, or queue handoff failure.
    pub fn reconcile(
        &mut self,
        snapshot: (crate::WorldSnapshot, u64, u64),
    ) -> Result<crate::RecoveryReport, DurableBuildError> {
        self.service
            .restore_recover_and_enqueue(snapshot, &mut self.queue)
    }

    /// Borrows the durable service for worker lifecycle operations.
    #[must_use]
    pub const fn service(&self) -> &DurableBuildService<C, MAX_ENTITIES, MAX_COMMANDS> {
        &self.service
    }

    /// Borrows the recovery queue for worker dispatch.
    #[must_use]
    pub const fn queue(&self) -> &Q {
        &self.queue
    }

    /// Mutably borrows the recovery queue for worker dispatch.
    pub const fn queue_mut(&mut self) -> &mut Q {
        &mut self.queue
    }
}

impl<C, const MAX_ENTITIES: usize, const MAX_COMMANDS: usize>
    DurableBuildService<C, MAX_ENTITIES, MAX_COMMANDS>
where
    C: SpacetimeDbDomainReducerClient,
{
    /// Creates an empty service pair.
    #[must_use]
    pub fn new(client: C) -> Self {
        Self {
            local: BuildService::new(),
            durable: GeneratedSpacetimeDb::new(client),
        }
    }

    /// Restores local ECS state and returns work that must be re-enqueued.
    ///
    /// Durable rows remain the source of truth; the returned report is the
    /// explicit handoff to a worker/queue adapter that schedules the next
    /// generation-fenced effects.
    ///
    /// # Errors
    ///
    /// Returns the snapshot validation error when local state cannot be restored.
    pub fn restore_snapshot_and_recover(
        &mut self,
        snapshot: (crate::WorldSnapshot, u64, u64),
    ) -> Result<crate::RecoveryReport, DurableBuildError> {
        let original = snapshot.clone();
        self.local
            .restore_snapshot(snapshot)
            .map_err(DurableBuildError::Rollback)?;
        let report = self.local.recover_incomplete_work();
        for (index, build) in original.0.builds.iter().enumerate() {
            if let Some(build) = build
                .as_ref()
                .filter(|value| value.state == crate::BuildState::Running)
            {
                let id = EntityId(u64::try_from(index).unwrap_or(u64::MAX));
                if let Err(error) =
                    self.durable
                        .recover_build(id.0, build.tenant, build.generation.0)
                {
                    return self.rollback(original, DurableBuildError::Persistence(error));
                }
            }
        }
        for (index, deployment) in original.0.deployments.iter().enumerate() {
            if let Some(deployment) = deployment
                .as_ref()
                .filter(|value| value.state == crate::DeploymentState::Starting)
            {
                let id = EntityId(u64::try_from(index).unwrap_or(u64::MAX));
                if let Err(error) = self.durable.recover_deployment(
                    id.0,
                    deployment.tenant,
                    deployment.generation.0,
                ) {
                    return self.rollback(original, DurableBuildError::Persistence(error));
                }
            }
        }
        Ok(report)
    }

    /// Restores durable state, then hands every recovered generation to a queue.
    ///
    /// Reducer recovery is completed before queue admission. If queue delivery
    /// fails, the durable rows remain pending and can be retried by the next
    /// reconciliation pass without claiming false execution success.
    ///
    /// # Errors
    ///
    /// Returns a durable recovery or queue handoff failure.
    pub fn restore_recover_and_enqueue<Q: RecoveryQueuePort>(
        &mut self,
        snapshot: (crate::WorldSnapshot, u64, u64),
        queue: &mut Q,
    ) -> Result<crate::RecoveryReport, DurableBuildError> {
        let original = snapshot.clone();
        let report = self.restore_snapshot_and_recover(snapshot)?;
        for (index, build) in original.0.builds.iter().enumerate() {
            if let Some(build) = build
                .as_ref()
                .filter(|value| value.state == crate::BuildState::Running)
            {
                queue
                    .enqueue_build(
                        EntityId(u64::try_from(index).unwrap_or(u64::MAX)),
                        build.tenant,
                        build.generation,
                    )
                    .map_err(DurableBuildError::Queue)?;
            }
        }
        for (index, deployment) in original.0.deployments.iter().enumerate() {
            if let Some(deployment) = deployment
                .as_ref()
                .filter(|value| value.state == crate::DeploymentState::Starting)
            {
                queue
                    .enqueue_deployment(
                        EntityId(u64::try_from(index).unwrap_or(u64::MAX)),
                        deployment.tenant,
                        deployment.generation,
                    )
                    .map_err(DurableBuildError::Queue)?;
            }
        }
        Ok(report)
    }

    /// Submits a project build and commits its durable projection.
    ///
    /// # Errors
    ///
    /// Returns an error when local admission, projection encoding, or the
    /// durable reducer rejects the mutation.
    pub fn submit_for_project(
        &mut self,
        principal: &Principal,
        project: EntityId,
        source_ref: &str,
    ) -> Result<EntityId, DurableBuildError> {
        let snapshot = self.local.snapshot();
        let id = self
            .local
            .submit_for_project(principal, project, source_ref)
            .map_err(DurableBuildError::Local)?;
        let build = self
            .local
            .projected_build(id)
            .ok_or(DurableBuildError::Local(BuildServiceError::Rejected))?;
        if let Err(error) =
            self.durable
                .create_build(id.0, build.tenant, BuildWrite::from_component(build))
        {
            return self.rollback(snapshot, DurableBuildError::Persistence(error));
        }
        Ok(id)
    }

    /// Completes a build and commits its generation-fenced durable transition.
    ///
    /// # Errors
    ///
    /// Returns an error when local admission or the durable transition rejects
    /// the mutation.
    pub fn complete_build(
        &mut self,
        principal: &Principal,
        build: EntityId,
        generation: crate::Generation,
        exit_code: u32,
    ) -> Result<(), DurableBuildError> {
        let snapshot = self.local.snapshot();
        self.local
            .complete_build(principal, build, generation, exit_code)
            .map_err(DurableBuildError::Local)?;
        let projected = self
            .local
            .projected_build(build)
            .ok_or(DurableBuildError::Local(BuildServiceError::Rejected))?;
        let status = match projected.state {
            crate::BuildState::Succeeded => "succeeded",
            crate::BuildState::Failed => "failed",
            _ => return Err(DurableBuildError::Local(BuildServiceError::Rejected)),
        };
        if let Err(error) =
            self.durable
                .transition_build(build.0, projected.tenant, generation.0, status)
        {
            return self.rollback(snapshot, DurableBuildError::Persistence(error));
        }
        Ok(())
    }

    /// Creates a deployment and commits its complete durable projection.
    ///
    /// # Errors
    ///
    /// Returns an error when local admission, metadata encoding, or the
    /// durable reducer rejects the mutation.
    pub fn create_deployment_with_metadata(
        &mut self,
        principal: &Principal,
        project: Option<EntityId>,
        build: EntityId,
        metadata: DeploymentMetadata,
    ) -> Result<EntityId, DurableBuildError> {
        let snapshot = self.local.snapshot();
        let id = self
            .local
            .create_deployment_with_metadata(principal, project, build, metadata)
            .map_err(DurableBuildError::Local)?;
        let projected = self
            .local
            .projected_deployment(id)
            .ok_or(DurableBuildError::Local(BuildServiceError::Rejected))?;
        let payload = DeploymentWrite::try_from_component(projected)
            .map_err(DurableBuildError::Serialization)?;
        if let Err(error) = self
            .durable
            .create_deployment(id.0, projected.tenant, build.0, payload)
        {
            return self.rollback(snapshot, DurableBuildError::Persistence(error));
        }
        Ok(id)
    }

    /// Applies runtime readiness and commits its durable projection.
    ///
    /// # Errors
    ///
    /// Returns an error when local admission or the generation-fenced durable
    /// runtime reducer rejects the mutation.
    pub fn runtime_ready_with_metadata(
        &mut self,
        principal: &Principal,
        deployment: EntityId,
        generation: crate::Generation,
        runtime_id: &str,
        runtime_mode: &str,
        runtime_endpoint: &str,
    ) -> Result<(), DurableBuildError> {
        let snapshot = self.local.snapshot();
        self.local
            .runtime_ready_with_metadata(
                principal,
                deployment,
                generation,
                runtime_id,
                runtime_mode,
                runtime_endpoint,
            )
            .map_err(DurableBuildError::Local)?;
        let projected = self
            .local
            .projected_deployment(deployment)
            .ok_or(DurableBuildError::Local(BuildServiceError::Rejected))?;
        let runtime = DeploymentRuntimeWrite {
            runtime_id: projected.runtime_id.clone(),
            runtime_mode: projected.runtime_mode.clone(),
            runtime_endpoint: projected.runtime_endpoint.clone(),
            runtime_status: projected.runtime_status.clone(),
        };
        if let Err(error) = self.durable.update_deployment_runtime(
            deployment.0,
            projected.tenant,
            generation.0,
            "running",
            runtime,
        ) {
            return self.rollback(snapshot, DurableBuildError::Persistence(error));
        }
        Ok(())
    }

    /// Stops a deployment and commits its generation-fenced durable projection.
    ///
    /// # Errors
    ///
    /// Returns an error when local admission or the durable reducer rejects
    /// the shutdown transition.
    pub fn stop_deployment(
        &mut self,
        principal: &Principal,
        deployment: EntityId,
    ) -> Result<(), DurableBuildError> {
        let snapshot = self.local.snapshot();
        let generation = self
            .local
            .projected_deployment(deployment)
            .filter(|value| value.tenant == principal.tenant)
            .map(|value| value.generation)
            .ok_or(DurableBuildError::Local(BuildServiceError::NotFound))?;
        self.local
            .stop_deployment(principal, deployment)
            .map_err(DurableBuildError::Local)?;
        let projected = self
            .local
            .projected_deployment(deployment)
            .ok_or(DurableBuildError::Local(BuildServiceError::Rejected))?;
        let runtime = DeploymentRuntimeWrite {
            runtime_id: projected.runtime_id.clone(),
            runtime_mode: projected.runtime_mode.clone(),
            runtime_endpoint: projected.runtime_endpoint.clone(),
            runtime_status: projected.runtime_status.clone(),
        };
        if let Err(error) = self.durable.update_deployment_runtime(
            deployment.0,
            projected.tenant,
            generation.0,
            "stopped",
            runtime,
        ) {
            return self.rollback(snapshot, DurableBuildError::Persistence(error));
        }
        Ok(())
    }

    /// Applies an unexpected runtime exit and commits its failed projection.
    ///
    /// # Errors
    ///
    /// Returns an error when local admission or the generation-fenced durable
    /// runtime reducer rejects the exit transition.
    pub fn runtime_exited(
        &mut self,
        principal: &Principal,
        deployment: EntityId,
        generation: crate::Generation,
    ) -> Result<(), DurableBuildError> {
        let snapshot = self.local.snapshot();
        self.local
            .runtime_exited(principal, deployment, generation)
            .map_err(DurableBuildError::Local)?;
        let projected = self
            .local
            .projected_deployment(deployment)
            .ok_or(DurableBuildError::Local(BuildServiceError::Rejected))?;
        let runtime = DeploymentRuntimeWrite {
            runtime_id: projected.runtime_id.clone(),
            runtime_mode: projected.runtime_mode.clone(),
            runtime_endpoint: projected.runtime_endpoint.clone(),
            runtime_status: projected.runtime_status.clone(),
        };
        if let Err(error) = self.durable.update_deployment_runtime(
            deployment.0,
            projected.tenant,
            generation.0,
            "failed",
            runtime,
        ) {
            return self.rollback(snapshot, DurableBuildError::Persistence(error));
        }
        Ok(())
    }

    /// Returns the local ECS build/deployment service for read-side adapters.
    #[must_use]
    pub const fn local(&self) -> &BuildService<MAX_ENTITIES, MAX_COMMANDS> {
        &self.local
    }

    /// Returns mutable access to the generated client boundary.
    pub const fn durable_mut(&mut self) -> &mut GeneratedSpacetimeDb<C> {
        &mut self.durable
    }

    fn rollback<T>(
        &mut self,
        snapshot: (crate::WorldSnapshot, u64, u64),
        error: DurableBuildError,
    ) -> Result<T, DurableBuildError> {
        match self.local.restore_snapshot(snapshot) {
            Err(rollback) => Err(DurableBuildError::Rollback(rollback)),
            Ok(()) => Err(error),
        }
    }
}

/// Converts a tenant identifier without leaking storage-specific primitives.
#[must_use]
pub const fn durable_tenant(tenant: TenantId) -> u32 {
    tenant.0
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{
        spacetime_client::{
            BuildWrite, ContractFakeClient, DeploymentRuntimeWrite, DeploymentWrite, RunnerWrite,
        },
        Permission, SpacetimeDbError, SubjectId,
    };

    struct RejectingClient;

    impl SpacetimeDbDomainReducerClient for RejectingClient {
        fn reducer_admit_command(
            &mut self,
            _id: u64,
            _tenant: TenantId,
            _kind: &str,
            _correlation_id: &str,
            _payload: &[u8],
            _created_at: u64,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_transition_command(
            &mut self,
            _id: u64,
            _tenant: TenantId,
            _status: &str,
            _payload: &[u8],
            _updated_at: u64,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_create_operation(
            &mut self,
            _id: u64,
            _tenant: TenantId,
            _kind: &str,
            _correlation_id: &str,
            _created_at: u64,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_transition_operation(
            &mut self,
            _id: u64,
            _tenant: TenantId,
            _status: crate::OperationStatus,
            _payload: &[u8],
            _updated_at: u64,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_create_project(
            &mut self,
            _id: u64,
            _tenant: TenantId,
            _project: ProjectWrite,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_update_project(
            &mut self,
            _id: u64,
            _tenant: TenantId,
            _project: ProjectWrite,
            _status: &str,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_delete_project(
            &mut self,
            _id: u64,
            _tenant: TenantId,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_create_build(
            &mut self,
            _id: u64,
            _tenant: TenantId,
            _build: BuildWrite,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_transition_build(
            &mut self,
            _id: u64,
            _tenant: TenantId,
            _generation: u64,
            _status: &str,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_recover_build(
            &mut self,
            _id: u64,
            _tenant: TenantId,
            _generation: u64,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_create_deployment(
            &mut self,
            _id: u64,
            _tenant: TenantId,
            _build_id: u64,
            _deployment: DeploymentWrite,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_transition_deployment(
            &mut self,
            _id: u64,
            _tenant: TenantId,
            _generation: u64,
            _status: &str,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_recover_deployment(
            &mut self,
            _id: u64,
            _tenant: TenantId,
            _generation: u64,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_update_deployment_runtime(
            &mut self,
            _id: u64,
            _tenant: TenantId,
            _generation: u64,
            _status: &str,
            _runtime: DeploymentRuntimeWrite,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_register_runner(
            &mut self,
            _id: u64,
            _tenant: TenantId,
            _runner: RunnerWrite,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_claim_runner(
            &mut self,
            _tenant: TenantId,
            _capability: &str,
            _now: u64,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_release_runner(
            &mut self,
            _id: u64,
            _tenant: TenantId,
        ) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }

        fn reducer_expire_runners(&mut self, _now: u64) -> Result<(), SpacetimeDbError> {
            Err(SpacetimeDbError::InvalidInput)
        }
    }

    fn principal() -> Principal {
        Principal::new(SubjectId(1), TenantId(7)).with_permission(Permission::ManageProjects)
    }

    fn project() -> Project {
        Project {
            id: EntityId(0),
            tenant: TenantId(0),
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
        }
    }

    #[test]
    fn durable_project_mutations_commit_through_reducer_boundary() {
        let mut service = DurableProjectService::<_, 8, 16>::new(ContractFakeClient::default());
        let owner = principal();
        let id = service.create(&owner, project()).expect("project commits");
        service
            .update_repository(
                &owner,
                id,
                "github".to_owned(),
                "https://github.com/example/janus".to_owned(),
                "release".to_owned(),
            )
            .expect("project update commits");
        service.delete(&owner, id).expect("project delete commits");
    }

    #[test]
    fn rejected_reducer_rolls_back_local_project_state() {
        let mut service = DurableProjectService::<_, 8, 16>::new(RejectingClient);
        let owner = principal();
        assert_eq!(
            service.create(&owner, project()),
            Err(DurableProjectError::Persistence(
                SpacetimeDbError::InvalidInput
            ))
        );
        assert!(service.local().list(&owner).unwrap().is_empty());
    }

    #[test]
    fn durable_build_deployment_mutations_commit_component_projections() {
        let owner = Principal::new(SubjectId(1), TenantId(7))
            .with_permission(Permission::SubmitBuilds)
            .with_permission(Permission::ManageDeployments);
        let mut service = DurableBuildService::<_, 8, 16>::new(ContractFakeClient::default());
        let build = service
            .submit_for_project(&owner, EntityId(11), "release-1")
            .expect("build commits");
        service
            .complete_build(&owner, build, crate::Generation(1), 0)
            .expect("build transition commits");
        let deployment = service
            .create_deployment_with_metadata(
                &owner,
                Some(EntityId(11)),
                build,
                DeploymentMetadata {
                    target_type: "preview".to_owned(),
                    target_ref: "release-1".to_owned(),
                    preferred_runner: "runner-1".to_owned(),
                    environment: vec![("MODE".to_owned(), "test".to_owned())],
                },
            )
            .expect("deployment commits");
        service
            .runtime_ready_with_metadata(
                &owner,
                deployment,
                crate::Generation(1),
                "runtime-1",
                "process",
                "http://runtime.local",
            )
            .expect("runtime projection commits");
        service
            .stop_deployment(&owner, deployment)
            .expect("stop projection commits");
        let failed_deployment = service
            .create_deployment_with_metadata(
                &owner,
                Some(EntityId(11)),
                build,
                DeploymentMetadata::default(),
            )
            .expect("second deployment commits");
        service
            .runtime_exited(&owner, failed_deployment, crate::Generation(1))
            .expect("failed runtime projection commits");
    }

    #[test]
    fn durable_build_service_exposes_snapshot_recovery_work() {
        let owner = Principal::new(SubjectId(1), TenantId(7))
            .with_permission(Permission::SubmitBuilds)
            .with_permission(Permission::ReadBuilds);
        let mut service = DurableBuildService::<_, 8, 16>::new(ContractFakeClient::default());
        let build = service
            .submit_for_project(&owner, EntityId(11), "release-1")
            .expect("build commits");
        let snapshot = service.local().snapshot();
        let report = service
            .restore_snapshot_and_recover(snapshot)
            .expect("snapshot recovery commits locally");
        assert_eq!(report.builds_requeued, 1);
        assert_eq!(report.deployments_requeued, 0);
        assert_eq!(
            service.local().build(&owner, build).unwrap().state,
            crate::BuildState::Pending
        );
    }

    #[derive(Default)]
    struct RecordingRecoveryQueue {
        builds: Vec<(EntityId, TenantId, crate::Generation)>,
        deployments: Vec<(EntityId, TenantId, crate::Generation)>,
    }

    impl RecoveryQueuePort for RecordingRecoveryQueue {
        fn enqueue_build(
            &mut self,
            build: EntityId,
            tenant: TenantId,
            generation: crate::Generation,
        ) -> Result<(), RecoveryQueueError> {
            self.builds.push((build, tenant, generation));
            Ok(())
        }

        fn enqueue_deployment(
            &mut self,
            deployment: EntityId,
            tenant: TenantId,
            generation: crate::Generation,
        ) -> Result<(), RecoveryQueueError> {
            self.deployments.push((deployment, tenant, generation));
            Ok(())
        }
    }

    #[test]
    fn durable_recovery_hands_generation_fenced_work_to_queue() {
        let owner = Principal::new(SubjectId(1), TenantId(7))
            .with_permission(Permission::SubmitBuilds)
            .with_permission(Permission::ReadBuilds);
        let mut service = DurableBuildService::<_, 8, 16>::new(ContractFakeClient::default());
        let build = service
            .submit_for_project(&owner, EntityId(11), "release-1")
            .expect("build commits");
        let snapshot = service.local().snapshot();
        let mut queue = RecordingRecoveryQueue::default();
        let report = service
            .restore_recover_and_enqueue(snapshot, &mut queue)
            .expect("recovery queue handoff succeeds");
        assert_eq!(report.builds_requeued, 1);
        assert_eq!(
            queue.builds,
            vec![(build, TenantId(7), crate::Generation(1))]
        );
        assert!(queue.deployments.is_empty());
    }

    #[test]
    fn recovery_reconciler_exposes_startup_and_periodic_boundaries() {
        let owner = Principal::new(SubjectId(1), TenantId(7))
            .with_permission(Permission::SubmitBuilds)
            .with_permission(Permission::ReadBuilds);
        let mut service = DurableBuildService::<_, 8, 16>::new(ContractFakeClient::default());
        service
            .submit_for_project(&owner, EntityId(11), "release-1")
            .expect("build commits");
        let snapshot = service.local().snapshot();
        let mut reconciler = RecoveryReconciler::new(service, RecordingRecoveryQueue::default());
        let startup = reconciler
            .startup(snapshot.clone())
            .expect("startup recovery");
        assert_eq!(startup.builds_requeued, 1);
        let periodic = reconciler.reconcile(snapshot).expect("periodic recovery");
        assert_eq!(periodic.builds_requeued, 1);
        assert_eq!(reconciler.queue().builds.len(), 2);
    }
}
