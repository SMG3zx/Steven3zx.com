//! Tenant-aware build application use cases over the actor-owned ECS world.

use crate::{
    valid_deployment_env_key, Build, BuildState, Command, CommandId, Deployment,
    DeploymentMetadata, DeploymentState, EntityId, Generation, Permission, Principal, TraceRecord,
    TraceSink, World,
};

/// Failure returned by a build application operation.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum BuildServiceError {
    /// The caller lacks the build permission or tenant ownership.
    Authorization,
    /// The bounded world or entity range is full.
    Capacity,
    /// The command was admitted but the world rejected its transition.
    Rejected,
    /// The requested build or deployment does not exist for the tenant.
    NotFound,
}

struct NoopTrace;

impl TraceSink for NoopTrace {
    fn record(&mut self, _sample: TraceRecord) {}
}

/// Bounded build application service.
pub struct BuildService<const MAX_ENTITIES: usize, const MAX_COMMANDS: usize> {
    world: World<MAX_ENTITIES, MAX_COMMANDS>,
    next_command: u64,
    next_entity: u64,
}

impl<const MAX_ENTITIES: usize, const MAX_COMMANDS: usize>
    BuildService<MAX_ENTITIES, MAX_COMMANDS>
{
    /// Creates an empty build service.
    #[must_use]
    pub fn new() -> Self {
        Self {
            world: World::new(),
            next_command: 1,
            next_entity: 1,
        }
    }

    /// Hydrates build and deployment projections from the authoritative
    /// `SpacetimeDB` subscription before workers are started.
    ///
    /// # Errors
    ///
    /// Returns a snapshot error when a durable row exceeds ECS capacity or
    /// violates lifecycle invariants.
    pub fn hydrate_durable(
        &mut self,
        builds: impl IntoIterator<Item = (EntityId, Build)>,
        deployments: impl IntoIterator<Item = (EntityId, Deployment)>,
    ) -> Result<(), crate::SnapshotError> {
        let mut build_slots = vec![None; MAX_ENTITIES];
        let mut deployment_slots = vec![None; MAX_ENTITIES];
        let mut next_entity = 1_u64;
        for (id, build) in builds {
            let slot = build_slots
                .get_mut(usize::try_from(id.0).map_err(|_| crate::SnapshotError::Capacity)?)
                .ok_or(crate::SnapshotError::Capacity)?;
            if slot.is_some() || build.tenant.0 == 0 {
                return Err(crate::SnapshotError::Capacity);
            }
            *slot = Some(build);
            next_entity = next_entity.max(id.0.saturating_add(1));
        }
        for (id, deployment) in deployments {
            let slot = deployment_slots
                .get_mut(usize::try_from(id.0).map_err(|_| crate::SnapshotError::Capacity)?)
                .ok_or(crate::SnapshotError::Capacity)?;
            if slot.is_some() || deployment.tenant.0 == 0 {
                return Err(crate::SnapshotError::Capacity);
            }
            *slot = Some(deployment);
            next_entity = next_entity.max(id.0.saturating_add(1));
        }
        self.world.restore(crate::WorldSnapshot {
            version: crate::ProtocolVersion(crate::CURRENT_PROTOCOL_VERSION),
            tick: crate::Tick(0),
            builds: build_slots,
            deployments: deployment_slots,
            operations: vec![None; MAX_ENTITIES],
            projects: vec![None; MAX_ENTITIES],
            seen_commands: Vec::with_capacity(MAX_COMMANDS),
            control_mode: crate::ControlMode::Running,
        })?;
        self.next_command = 1;
        self.next_entity = next_entity;
        Ok(())
    }

    /// Captures local ECS state and allocator counters for a durable commit.
    #[must_use]
    pub fn snapshot(&self) -> (crate::WorldSnapshot, u64, u64) {
        (self.world.snapshot(), self.next_command, self.next_entity)
    }

    /// Restores local ECS state and allocator counters after a rejected durable commit.
    ///
    /// # Errors
    ///
    /// Returns the snapshot validation error when the state cannot be restored.
    pub fn restore_snapshot(
        &mut self,
        snapshot: (crate::WorldSnapshot, u64, u64),
    ) -> Result<(), crate::SnapshotError> {
        self.world.restore(snapshot.0)?;
        self.next_command = snapshot.1;
        self.next_entity = snapshot.2;
        Ok(())
    }

    /// Restores incomplete work to the pending state used by the next worker tick.
    #[must_use]
    pub fn recover_incomplete_work(&mut self) -> crate::RecoveryReport {
        self.world.recover_incomplete_work()
    }

    /// Lists only builds belonging to the authenticated tenant.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn list(&self, principal: &Principal) -> Result<Vec<(EntityId, Build)>, BuildServiceError> {
        principal
            .authorize(principal.tenant, Permission::ReadBuilds)
            .map_err(|_| BuildServiceError::Authorization)?;
        Ok((0..MAX_ENTITIES)
            .filter_map(|index| {
                let id = EntityId(u64::try_from(index).unwrap_or(u64::MAX));
                self.world
                    .build(id)
                    .filter(|build| build.tenant == principal.tenant)
                    .cloned()
                    .map(|build| (id, build))
            })
            .collect())
    }

    /// Lists tenant-owned builds, optionally restricted to one project.
    ///
    /// # Errors
    ///
    /// Returns an error when the caller lacks build-read permission.
    pub fn list_for_project(
        &self,
        principal: &Principal,
        project: Option<EntityId>,
    ) -> Result<Vec<(EntityId, Build)>, BuildServiceError> {
        let builds = self.list(principal)?;
        Ok(builds
            .into_iter()
            .filter(|(_, build)| project.is_none_or(|value| build.project == Some(value)))
            .collect())
    }

    /// Reads one tenant-owned build through the same authorization boundary as listing.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn build(
        &self,
        principal: &Principal,
        build: EntityId,
    ) -> Result<Build, BuildServiceError> {
        principal
            .authorize(principal.tenant, Permission::ReadBuilds)
            .map_err(|_| BuildServiceError::Authorization)?;
        self.world
            .build(build)
            .filter(|value| value.tenant == principal.tenant)
            .cloned()
            .ok_or(BuildServiceError::NotFound)
    }

    pub(crate) fn projected_build(&self, build: EntityId) -> Option<&Build> {
        self.world.build(build)
    }

    /// Submits a tenant-owned build through the authorized ECS boundary.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn submit(&mut self, principal: &Principal) -> Result<EntityId, BuildServiceError> {
        principal
            .authorize(principal.tenant, Permission::SubmitBuilds)
            .map_err(|_| BuildServiceError::Authorization)?;
        let build = self.allocate_entity()?;
        let command_id = self.command_id();
        self.world
            .enqueue_authorized(
                principal,
                Command::SubmitBuild {
                    command_id,
                    build,
                    tenant: principal.tenant,
                },
            )
            .map_err(|error| match error {
                crate::AdmissionError::Authorization(_) => BuildServiceError::Authorization,
                crate::AdmissionError::Capacity => BuildServiceError::Capacity,
            })?;
        let mut trace = NoopTrace;
        self.world.tick(&mut trace);
        if self.world.build(build).is_some() {
            Ok(build)
        } else {
            Err(BuildServiceError::Rejected)
        }
    }

    /// Submits a build associated with a tenant-owned project and source reference.
    ///
    /// # Errors
    ///
    /// Returns an error when authorization, boundedness, or source validation
    /// rejects the request.
    pub fn submit_for_project(
        &mut self,
        principal: &Principal,
        project: EntityId,
        source_ref: &str,
    ) -> Result<EntityId, BuildServiceError> {
        principal
            .authorize(principal.tenant, Permission::SubmitBuilds)
            .map_err(|_| BuildServiceError::Authorization)?;
        let source_ref = source_ref.trim();
        if source_ref.is_empty() || source_ref.len() > 256 {
            return Err(BuildServiceError::Rejected);
        }
        let build = self.allocate_entity()?;
        let command_id = self.command_id();
        self.world
            .enqueue_authorized(
                principal,
                Command::SubmitProjectBuild {
                    command_id,
                    build,
                    tenant: principal.tenant,
                    project,
                    source_ref: source_ref.to_owned(),
                },
            )
            .map_err(|error| match error {
                crate::AdmissionError::Authorization(_) => BuildServiceError::Authorization,
                crate::AdmissionError::Capacity => BuildServiceError::Capacity,
            })?;
        let mut trace = NoopTrace;
        self.world.tick(&mut trace);
        if self.world.build(build).is_some() {
            Ok(build)
        } else {
            Err(BuildServiceError::Rejected)
        }
    }

    /// Advances one bounded worker tick and applies lifecycle effects.
    pub fn tick(&mut self) {
        let mut trace = NoopTrace;
        self.world.tick(&mut trace);
    }

    /// Applies a worker completion callback to a tenant-owned build.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn complete_build(
        &mut self,
        principal: &Principal,
        build: EntityId,
        generation: Generation,
        exit_code: u32,
    ) -> Result<(), BuildServiceError> {
        let current = self
            .world
            .build(build)
            .filter(|value| value.tenant == principal.tenant)
            .ok_or(BuildServiceError::Authorization)?;
        if current.state.terminal() || current.generation != generation {
            return Err(BuildServiceError::Rejected);
        }
        let command_id = self.command_id();
        self.world
            .enqueue(Command::ProcessFinished {
                command_id,
                build,
                tenant: principal.tenant,
                generation,
                exit_code,
            })
            .map_err(|_| BuildServiceError::Capacity)?;
        self.tick();
        Ok(())
    }

    /// Creates a deployment only after its tenant-owned build succeeds.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn create_deployment(
        &mut self,
        principal: &Principal,
        build: EntityId,
    ) -> Result<EntityId, BuildServiceError> {
        self.create_deployment_with_metadata(principal, None, build, DeploymentMetadata::default())
    }

    /// Creates a deployment with its project, target, runner, and environment metadata.
    ///
    /// # Errors
    ///
    /// Returns an error when authorization, project ownership, boundedness,
    /// or deployment lifecycle admission rejects the request.
    pub fn create_deployment_with_metadata(
        &mut self,
        principal: &Principal,
        project: Option<EntityId>,
        build: EntityId,
        metadata: DeploymentMetadata,
    ) -> Result<EntityId, BuildServiceError> {
        principal
            .authorize(principal.tenant, Permission::ManageDeployments)
            .map_err(|_| BuildServiceError::Authorization)?;
        if project.is_some_and(|project| {
            self.world.build(build).and_then(|value| value.project) != Some(project)
        }) {
            return Err(BuildServiceError::Rejected);
        }
        if self.world.build(build).map(|value| value.state) != Some(BuildState::Succeeded) {
            return Err(BuildServiceError::Rejected);
        }
        if (!metadata.target_type.is_empty()
            && !matches!(metadata.target_type.as_str(), "preview" | "production"))
            || metadata.target_type.len() > 32
            || metadata.target_ref.len() > 256
            || metadata.preferred_runner.len() > 256
            || metadata.environment.len() > crate::MAX_DEPLOYMENT_ENV_ENTRIES
            || metadata.environment.iter().any(|(key, value)| {
                !valid_deployment_env_key(key) || key.len() > 128 || value.len() > 4096
            })
        {
            return Err(BuildServiceError::Rejected);
        }
        let deployment = self.allocate_entity()?;
        let command_id = self.command_id();
        self.world
            .enqueue_authorized(
                principal,
                Command::CreateDeployment {
                    command_id,
                    deployment,
                    tenant: principal.tenant,
                    build,
                    target_type: metadata.target_type.trim().to_owned(),
                    target_ref: metadata.target_ref.trim().to_owned(),
                    preferred_runner: metadata.preferred_runner.trim().to_owned(),
                    environment: metadata.environment,
                },
            )
            .map_err(|error| match error {
                crate::AdmissionError::Authorization(_) => BuildServiceError::Authorization,
                crate::AdmissionError::Capacity => BuildServiceError::Capacity,
            })?;
        self.tick();
        if self.world.deployment(deployment).is_some() {
            Ok(deployment)
        } else {
            Err(BuildServiceError::Rejected)
        }
    }

    /// Creates a deployment while enforcing the optional project/build boundary.
    ///
    /// # Errors
    ///
    /// Returns an error when the project does not own the build or the normal
    /// deployment admission checks reject the request.
    pub fn create_deployment_for_project(
        &mut self,
        principal: &Principal,
        project: Option<EntityId>,
        build: EntityId,
    ) -> Result<EntityId, BuildServiceError> {
        self.create_deployment_with_metadata(
            principal,
            project,
            build,
            DeploymentMetadata::default(),
        )
    }

    /// Applies a generation-fenced runtime readiness callback.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn runtime_ready(
        &mut self,
        principal: &Principal,
        deployment: EntityId,
        generation: Generation,
    ) -> Result<(), BuildServiceError> {
        self.runtime_ready_with_metadata(principal, deployment, generation, "", "", "")
    }

    /// Applies runtime readiness together with runner-provided metadata.
    ///
    /// # Errors
    ///
    /// Returns an error when the deployment is not owned by the principal,
    /// the generation is stale, or metadata exceeds the bounded field size.
    pub fn runtime_ready_with_metadata(
        &mut self,
        principal: &Principal,
        deployment: EntityId,
        generation: Generation,
        runtime_id: &str,
        runtime_mode: &str,
        runtime_endpoint: &str,
    ) -> Result<(), BuildServiceError> {
        let current = self
            .world
            .deployment(deployment)
            .filter(|value| value.tenant == principal.tenant)
            .ok_or(BuildServiceError::Authorization)?;
        if current.state != DeploymentState::Starting || current.generation != generation {
            return Err(BuildServiceError::Rejected);
        }
        if [runtime_id, runtime_mode, runtime_endpoint]
            .iter()
            .any(|value| value.len() > 256)
        {
            return Err(BuildServiceError::Rejected);
        }
        let command_id = self.command_id();
        self.world
            .enqueue(Command::RuntimeReady {
                command_id,
                deployment,
                tenant: principal.tenant,
                generation,
                runtime_id: runtime_id.trim().to_owned(),
                runtime_mode: runtime_mode.trim().to_owned(),
                runtime_endpoint: runtime_endpoint.trim().to_owned(),
            })
            .map_err(|_| BuildServiceError::Capacity)?;
        self.tick();
        Ok(())
    }

    /// Applies a generation-fenced runtime exit callback.
    ///
    /// # Errors
    ///
    /// Returns an error when the deployment is not tenant-owned, the
    /// generation is stale, or the lifecycle state cannot accept an exit.
    pub fn runtime_exited(
        &mut self,
        principal: &Principal,
        deployment: EntityId,
        generation: Generation,
    ) -> Result<(), BuildServiceError> {
        let current = self
            .world
            .deployment(deployment)
            .filter(|value| value.tenant == principal.tenant)
            .ok_or(BuildServiceError::Authorization)?;
        if !matches!(
            current.state,
            DeploymentState::Starting | DeploymentState::Running
        ) || current.generation != generation
        {
            return Err(BuildServiceError::Rejected);
        }
        let command_id = self.command_id();
        self.world
            .enqueue(Command::RuntimeExited {
                command_id,
                deployment,
                tenant: principal.tenant,
                generation,
            })
            .map_err(|_| BuildServiceError::Capacity)?;
        self.tick();
        Ok(())
    }

    /// Lists only deployments belonging to the authenticated tenant.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn list_deployments(
        &self,
        principal: &Principal,
    ) -> Result<Vec<(EntityId, Deployment)>, BuildServiceError> {
        self.list_deployments_for_project(principal, None)
    }

    /// Lists tenant-owned deployments, optionally restricted to one project.
    ///
    /// # Errors
    ///
    /// Returns an error when the caller lacks deployment permission.
    pub fn list_deployments_for_project(
        &self,
        principal: &Principal,
        project: Option<EntityId>,
    ) -> Result<Vec<(EntityId, Deployment)>, BuildServiceError> {
        principal
            .authorize(principal.tenant, Permission::ManageDeployments)
            .map_err(|_| BuildServiceError::Authorization)?;
        Ok((0..MAX_ENTITIES)
            .filter_map(|index| {
                let id = EntityId(u64::try_from(index).unwrap_or(u64::MAX));
                self.world
                    .deployment(id)
                    .filter(|deployment| {
                        deployment.tenant == principal.tenant
                            && project.is_none_or(|value| deployment.project == Some(value))
                    })
                    .cloned()
                    .map(|deployment| (id, deployment))
            })
            .collect())
    }

    /// Lists tenant-owned deployments with their associated project identity.
    ///
    /// # Errors
    ///
    /// Returns an error when the caller lacks deployment permission.
    pub fn list_deployment_projects(
        &self,
        principal: &Principal,
    ) -> Result<Vec<(EntityId, Deployment, Option<EntityId>)>, BuildServiceError> {
        principal
            .authorize(principal.tenant, Permission::ManageDeployments)
            .map_err(|_| BuildServiceError::Authorization)?;
        Ok((0..MAX_ENTITIES)
            .filter_map(|index| {
                let id = EntityId(u64::try_from(index).unwrap_or(u64::MAX));
                let deployment = self.world.deployment(id)?;
                if deployment.tenant != principal.tenant {
                    return None;
                }
                Some((id, deployment.clone(), deployment.project))
            })
            .collect())
    }

    /// Reads one tenant-owned deployment.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn deployment(
        &self,
        principal: &Principal,
        deployment: EntityId,
    ) -> Result<Deployment, BuildServiceError> {
        principal
            .authorize(principal.tenant, Permission::ManageDeployments)
            .map_err(|_| BuildServiceError::Authorization)?;
        self.world
            .deployment(deployment)
            .filter(|value| value.tenant == principal.tenant)
            .cloned()
            .ok_or(BuildServiceError::NotFound)
    }

    pub(crate) fn projected_deployment(&self, deployment: EntityId) -> Option<&Deployment> {
        self.world.deployment(deployment)
    }

    /// Stops a tenant-owned deployment through the ECS command boundary.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn stop_deployment(
        &mut self,
        principal: &Principal,
        deployment: EntityId,
    ) -> Result<(), BuildServiceError> {
        self.deployment(principal, deployment)?;
        let command_id = self.command_id();
        self.world
            .enqueue_authorized(
                principal,
                Command::StopDeployment {
                    command_id,
                    deployment,
                    tenant: principal.tenant,
                },
            )
            .map_err(|error| match error {
                crate::AdmissionError::Authorization(_) => BuildServiceError::Authorization,
                crate::AdmissionError::Capacity => BuildServiceError::Capacity,
            })?;
        self.tick();
        Ok(())
    }

    const fn command_id(&mut self) -> CommandId {
        let id = CommandId(self.next_command);
        self.next_command = self.next_command.saturating_add(1);
        id
    }

    fn allocate_entity(&mut self) -> Result<EntityId, BuildServiceError> {
        let max_entities = u64::try_from(MAX_ENTITIES).unwrap_or(u64::MAX);
        while self.next_entity < max_entities {
            let candidate = EntityId(self.next_entity);
            self.next_entity = self.next_entity.saturating_add(1);
            if self.world.build(candidate).is_none() {
                return Ok(candidate);
            }
        }
        Err(BuildServiceError::Capacity)
    }
}

impl<const MAX_ENTITIES: usize, const MAX_COMMANDS: usize> Default
    for BuildService<MAX_ENTITIES, MAX_COMMANDS>
{
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{Permission, SubjectId, TenantId};

    fn principal(tenant: u32) -> Principal {
        Principal::new(SubjectId(1), TenantId(tenant))
            .with_permission(Permission::SubmitBuilds)
            .with_permission(Permission::ReadBuilds)
    }

    fn assert_project_deployment_metadata(deployment: &Deployment) {
        assert_eq!(deployment.project, Some(EntityId(3)));
        assert_eq!(deployment.revision, 1);
        assert_eq!(deployment.target_type, "preview");
        assert_eq!(deployment.target_ref, "release");
        assert_eq!(deployment.preferred_runner, "runner-1");
        assert_eq!(
            deployment.environment,
            [("MODE".to_owned(), "preview".to_owned())]
        );
    }

    #[test]
    fn submit_and_list_preserve_tenant_boundary() {
        let mut service = BuildService::<4, 8>::new();
        let owner = principal(7);
        let other = principal(8);
        let build = service.submit(&owner).unwrap();
        assert_eq!(build, EntityId(1));
        assert_eq!(service.list(&owner).unwrap().len(), 1);
        assert!(service.list(&other).unwrap().is_empty());
    }

    #[test]
    fn project_submission_preserves_source_metadata_and_filtering() {
        let mut service = BuildService::<4, 8>::new();
        let owner = principal(7).with_permission(Permission::ManageDeployments);
        let build = service
            .submit_for_project(&owner, EntityId(3), "feature/import")
            .unwrap();
        let listed = service.list_for_project(&owner, Some(EntityId(3))).unwrap();
        assert_eq!(listed.len(), 1);
        assert_eq!(listed[0].0, build);
        assert_eq!(listed[0].1.project, Some(EntityId(3)));
        assert_eq!(listed[0].1.source_ref.as_deref(), Some("feature/import"));
        assert!(service
            .list_for_project(&owner, Some(EntityId(4)))
            .unwrap()
            .is_empty());
        let generation = service.list(&owner).unwrap()[0].1.generation;
        service
            .complete_build(&owner, build, generation, 0)
            .unwrap();
        let deployment = service
            .create_deployment_with_metadata(
                &owner,
                Some(EntityId(3)),
                build,
                DeploymentMetadata {
                    target_type: "preview".to_owned(),
                    target_ref: "release".to_owned(),
                    preferred_runner: "runner-1".to_owned(),
                    environment: vec![("MODE".to_owned(), "preview".to_owned())],
                },
            )
            .unwrap();
        let deployment = service
            .deployment(&owner, deployment)
            .expect("deployment is visible to its owner");
        assert_project_deployment_metadata(&deployment);
        assert!(valid_deployment_env_key("JANUS_MODE"));
        assert!(!valid_deployment_env_key("9_INVALID"));
        assert_eq!(
            service.create_deployment_with_metadata(
                &owner,
                Some(EntityId(3)),
                build,
                DeploymentMetadata {
                    environment: vec![("9_INVALID".to_owned(), "x".to_owned())],
                    ..DeploymentMetadata::default()
                },
            ),
            Err(BuildServiceError::Rejected)
        );
        assert_eq!(
            service
                .list_deployments_for_project(&owner, Some(EntityId(3)))
                .unwrap()
                .len(),
            1
        );
        assert!(service
            .list_deployments_for_project(&owner, Some(EntityId(4)))
            .unwrap()
            .is_empty());
        assert_eq!(
            service.create_deployment_for_project(&owner, Some(EntityId(4)), build),
            Err(BuildServiceError::Rejected)
        );
    }

    #[test]
    fn read_permission_is_required_for_listing() {
        let service = BuildService::<4, 8>::new();
        let principal = Principal::new(SubjectId(1), TenantId(7));
        assert_eq!(
            service.list(&principal),
            Err(BuildServiceError::Authorization)
        );
    }

    #[test]
    fn build_to_deployment_lifecycle_is_generation_fenced() {
        let principal = Principal::new(SubjectId(1), TenantId(7))
            .with_permission(Permission::SubmitBuilds)
            .with_permission(Permission::ReadBuilds)
            .with_permission(Permission::ManageDeployments);
        let mut service = BuildService::<8, 16>::new();
        let build = service.submit(&principal).unwrap();
        let generation = service.list(&principal).unwrap()[0].1.generation;
        service
            .complete_build(&principal, build, generation, 0)
            .unwrap();
        let deployment = service.create_deployment(&principal, build).unwrap();
        let starting = service.list_deployments(&principal).unwrap();
        assert_eq!(starting[0].0, deployment);
        assert_eq!(starting[0].1.state, DeploymentState::Starting);
        service
            .runtime_ready_with_metadata(
                &principal,
                deployment,
                starting[0].1.generation,
                "runtime-7",
                "wasm-http",
                "http://runtime.local",
            )
            .unwrap();
        let running = &service.list_deployments(&principal).unwrap()[0].1;
        assert_eq!(running.state, DeploymentState::Running);
        assert_eq!(running.runtime_id, "runtime-7");
        assert_eq!(running.runtime_mode, "wasm-http");
        assert_eq!(running.runtime_endpoint, "http://runtime.local");
        assert_eq!(running.runtime_status, "running");
    }
}
