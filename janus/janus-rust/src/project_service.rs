//! Authenticated project use cases backed by the actor-owned ECS world.

use crate::{
    Command, CommandId, ControlMode, EntityId, Principal, Project, ProjectError, ProtocolVersion,
    SnapshotError, Tick, TraceRecord, TraceSink, World, WorldSnapshot,
};

/// Failure returned by a project application operation.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ProjectServiceError {
    /// The caller lacks the project permission or tenant ownership.
    Authorization,
    /// The bounded command mailbox is full.
    Capacity,
    /// The project payload failed validation.
    InvalidProject(ProjectError),
    /// The requested project does not exist for the caller's tenant.
    NotFound,
    /// The command was admitted but the world rejected its transition.
    Rejected,
}

struct NoopTrace;

impl TraceSink for NoopTrace {
    fn record(&mut self, _sample: TraceRecord) {}
}

/// Stateful, bounded project application service.
pub struct ProjectService<const MAX_ENTITIES: usize, const MAX_COMMANDS: usize> {
    world: World<MAX_ENTITIES, MAX_COMMANDS>,
    next_command: u64,
    next_entity: u64,
}

impl<const MAX_ENTITIES: usize, const MAX_COMMANDS: usize>
    ProjectService<MAX_ENTITIES, MAX_COMMANDS>
{
    /// Creates an empty project service.
    #[must_use]
    pub fn new() -> Self {
        Self {
            world: World::new(),
            next_command: 1,
            next_entity: 1,
        }
    }

    /// Hydrates project components from the authoritative `SpacetimeDB`
    /// subscription before HTTP admission begins.
    ///
    /// # Errors
    ///
    /// Returns a snapshot error when a durable row is outside the bounded ECS
    /// capacity or violates project/world invariants.
    pub fn hydrate_projects<I>(&mut self, projects: I) -> Result<usize, SnapshotError>
    where
        I: IntoIterator<Item = Project>,
    {
        let mut slots = vec![None; MAX_ENTITIES];
        let mut hydrated = 0_usize;
        let mut next_entity = 1_u64;
        for project in projects {
            project.validate().map_err(|_| SnapshotError::Capacity)?;
            let index = usize::try_from(project.id.0).map_err(|_| SnapshotError::Capacity)?;
            let slot = slots.get_mut(index).ok_or(SnapshotError::Capacity)?;
            if slot.is_some() {
                return Err(SnapshotError::Capacity);
            }
            next_entity = next_entity.max(project.id.0.saturating_add(1));
            *slot = Some(project);
            hydrated = hydrated.saturating_add(1);
        }
        self.restore(WorldSnapshot {
            version: ProtocolVersion(crate::CURRENT_PROTOCOL_VERSION),
            tick: Tick(0),
            builds: vec![None; MAX_ENTITIES],
            deployments: vec![None; MAX_ENTITIES],
            operations: vec![None; MAX_ENTITIES],
            projects: slots,
            seen_commands: Vec::with_capacity(MAX_COMMANDS),
            control_mode: ControlMode::Running,
        })?;
        self.next_entity = next_entity;
        Ok(hydrated)
    }

    /// Lists only projects belonging to the authenticated tenant.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn list(&self, principal: &Principal) -> Result<Vec<Project>, ProjectServiceError> {
        principal
            .authorize(principal.tenant, crate::Permission::ManageProjects)
            .map_err(|_| ProjectServiceError::Authorization)?;
        Ok((0..MAX_ENTITIES)
            .filter_map(|index| {
                self.world
                    .project(EntityId(u64::try_from(index).unwrap_or(u64::MAX)))
            })
            .filter(|project| project.tenant == principal.tenant)
            .cloned()
            .collect())
    }

    /// Reads one tenant-owned project for repository and HTTP adapters.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn project(
        &self,
        principal: &Principal,
        project: EntityId,
    ) -> Result<Project, ProjectServiceError> {
        principal
            .authorize(principal.tenant, crate::Permission::ManageProjects)
            .map_err(|_| ProjectServiceError::Authorization)?;
        self.world
            .project(project)
            .filter(|value| value.tenant == principal.tenant)
            .cloned()
            .ok_or(ProjectServiceError::NotFound)
    }

    /// Creates a tenant-owned project through the ECS command boundary.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn create(
        &mut self,
        principal: &Principal,
        mut project: Project,
    ) -> Result<EntityId, ProjectServiceError> {
        project.tenant = principal.tenant;
        project.id = self.allocate_entity()?;
        project
            .validate()
            .map_err(ProjectServiceError::InvalidProject)?;
        let id = project.id;
        let command_id = self.command_id();
        self.submit(
            principal,
            Command::CreateProject {
                command_id,
                project: Box::new(project),
            },
        )?;
        if self.world.project(id).is_some() {
            Ok(id)
        } else {
            Err(ProjectServiceError::Rejected)
        }
    }

    /// Updates a project's repository through the ECS command boundary.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn update_repository(
        &mut self,
        principal: &Principal,
        project: EntityId,
        repo_provider: String,
        repo_url: String,
        repo_branch: String,
    ) -> Result<(), ProjectServiceError> {
        let current = self
            .world
            .project(project)
            .ok_or(ProjectServiceError::NotFound)?;
        if current.tenant != principal.tenant {
            return Err(ProjectServiceError::Authorization);
        }
        let command_id = self.command_id();
        self.submit(
            principal,
            Command::UpdateProjectRepository {
                command_id,
                project,
                tenant: principal.tenant,
                repo_provider,
                repo_url,
                repo_branch,
            },
        )?;
        Ok(())
    }

    /// Deletes a project through the ECS command boundary.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn delete(
        &mut self,
        principal: &Principal,
        project: EntityId,
    ) -> Result<(), ProjectServiceError> {
        let current = self
            .world
            .project(project)
            .ok_or(ProjectServiceError::NotFound)?;
        if current.tenant != principal.tenant {
            return Err(ProjectServiceError::Authorization);
        }
        let command_id = self.command_id();
        self.submit(
            principal,
            Command::DeleteProject {
                command_id,
                project,
                tenant: principal.tenant,
            },
        )?;
        if self.world.project(project).is_none() {
            Ok(())
        } else {
            Err(ProjectServiceError::Rejected)
        }
    }

    /// Exposes the world snapshot for the persistence adapter.
    #[must_use]
    pub fn snapshot(&self) -> crate::WorldSnapshot {
        self.world.snapshot()
    }

    /// Restores the service after a failed durable persistence attempt.
    ///
    /// # Errors
    ///
    /// Returns an error when the snapshot violates the world contract.
    pub fn restore(&mut self, snapshot: crate::WorldSnapshot) -> Result<(), crate::SnapshotError> {
        self.world.restore(snapshot)
    }

    fn submit(
        &mut self,
        principal: &Principal,
        command: Command,
    ) -> Result<(), ProjectServiceError> {
        self.world
            .enqueue_authorized(principal, command)
            .map_err(|error| match error {
                crate::AdmissionError::Authorization(_) => ProjectServiceError::Authorization,
                crate::AdmissionError::Capacity => ProjectServiceError::Capacity,
            })?;
        let mut trace = NoopTrace;
        self.world.tick(&mut trace);
        Ok(())
    }

    const fn command_id(&mut self) -> CommandId {
        let id = CommandId(self.next_command);
        self.next_command = self.next_command.saturating_add(1);
        id
    }

    fn allocate_entity(&mut self) -> Result<EntityId, ProjectServiceError> {
        let max_entities = u64::try_from(MAX_ENTITIES).unwrap_or(u64::MAX);
        while self.next_entity < max_entities {
            let candidate = EntityId(self.next_entity);
            self.next_entity = self.next_entity.saturating_add(1);
            if self.world.project(candidate).is_none() {
                return Ok(candidate);
            }
        }
        Err(ProjectServiceError::Capacity)
    }
}

impl<const MAX_ENTITIES: usize, const MAX_COMMANDS: usize> Default
    for ProjectService<MAX_ENTITIES, MAX_COMMANDS>
{
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::TenantId;

    fn principal(tenant: u32) -> Principal {
        Principal::new(crate::SubjectId(1), TenantId(tenant))
            .with_permission(crate::Permission::ManageProjects)
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
            repo_url: "https://example.test/janus".to_owned(),
            repo_branch: "main".to_owned(),
            repo_check: None,
            created_at: 1,
            updated_at: 1,
        }
    }

    #[test]
    fn project_use_cases_preserve_tenant_and_lifecycle_boundaries() {
        let mut service = ProjectService::<4, 8>::new();
        let owner = principal(7);
        let other = principal(8);
        let id = service.create(&owner, project()).unwrap();
        assert_eq!(service.list(&owner).unwrap().len(), 1);
        assert!(service.list(&other).unwrap().is_empty());
        assert_eq!(
            service.delete(&other, id),
            Err(ProjectServiceError::Authorization)
        );
        service
            .update_repository(
                &owner,
                id,
                "gitlab".to_owned(),
                "https://example.test/updated".to_owned(),
                "release".to_owned(),
            )
            .unwrap();
        assert_eq!(service.list(&owner).unwrap()[0].repo_branch, "release");
        service.delete(&owner, id).unwrap();
        assert!(service.list(&owner).unwrap().is_empty());
    }
}
