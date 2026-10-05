//! Bounded runtime isolation policy and generation-fenced instance state.

/// Runtime execution mode.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RuntimeMode {
    /// WebAssembly/WASI-style restricted execution.
    Wasi,
    /// Native process execution, requiring an explicit process capability.
    Native,
}

/// Capabilities granted to one runtime instance.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct RuntimeCapabilities {
    /// May access the configured artifact filesystem area.
    pub filesystem: bool,
    /// May open outbound network connections.
    pub network: bool,
    /// May create or terminate a native process.
    pub process: bool,
}

/// Runtime state observed by the control plane.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RuntimeState {
    /// Instance is being provisioned.
    Starting,
    /// Instance passed its launch boundary.
    Running,
    /// Instance stopped intentionally.
    Stopped,
    /// Instance failed to launch or remain healthy.
    Failed,
}

/// Bounded runtime launch specification.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RuntimeSpec {
    /// Runtime identity.
    pub id: u64,
    /// Owning tenant.
    pub tenant: crate::TenantId,
    /// Deployment identity.
    pub deployment: crate::EntityId,
    /// Runtime mode.
    pub mode: RuntimeMode,
    /// Artifact key resolved through the object-store adapter.
    pub artifact_key: String,
    /// Capability policy.
    pub capabilities: RuntimeCapabilities,
    /// Generation fencing callbacks from stale instances.
    pub generation: crate::Generation,
}

/// Runtime isolation failure.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RuntimeError {
    /// Artifact key is empty, absolute, or contains traversal.
    InvalidArtifactKey,
    /// The requested policy does not grant a required capability.
    MissingCapability,
    /// Runtime capacity is exhausted.
    Capacity,
    /// Runtime identity does not exist.
    NotFound,
    /// Runtime belongs to another tenant.
    TenantBoundary,
    /// Callback generation is stale.
    StaleGeneration,
    /// Runtime state does not accept the operation.
    InvalidState,
}

/// Provider-neutral runtime launcher boundary.
pub trait RuntimeLauncher {
    /// Validates the launch policy and prepares an instance.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn launch(&self, spec: &RuntimeSpec) -> Result<(), RuntimeError>;
    /// Stops an instance after generation and ownership checks.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn stop(&self, spec: &RuntimeSpec) -> Result<(), RuntimeError>;
}

/// Deterministic local launcher that validates policy without spawning a process.
#[derive(Clone, Copy, Debug, Default)]
pub struct LocalRuntimeLauncher;

impl RuntimeLauncher for LocalRuntimeLauncher {
    fn launch(&self, spec: &RuntimeSpec) -> Result<(), RuntimeError> {
        validate_spec(spec)
    }

    fn stop(&self, _spec: &RuntimeSpec) -> Result<(), RuntimeError> {
        Ok(())
    }
}

/// Runtime component stored by the bounded registry.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RuntimeInstance {
    /// Original launch specification.
    pub spec: RuntimeSpec,
    /// Current lifecycle state.
    pub state: RuntimeState,
}

/// Bounded runtime registry with explicit generation checks.
pub struct RuntimeRegistry<const MAX_RUNTIMES: usize> {
    runtimes: Vec<Option<RuntimeInstance>>,
}

impl<const MAX_RUNTIMES: usize> RuntimeRegistry<MAX_RUNTIMES> {
    /// Creates an empty registry.
    #[must_use]
    pub fn new() -> Self {
        Self {
            runtimes: vec![None; MAX_RUNTIMES],
        }
    }

    /// Validates and starts one runtime in a bounded slot.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn start<L: RuntimeLauncher>(
        &mut self,
        launcher: &L,
        spec: RuntimeSpec,
    ) -> Result<(), RuntimeError> {
        validate_spec(&spec)?;
        if self
            .runtimes
            .iter()
            .flatten()
            .any(|runtime| runtime.spec.id == spec.id)
        {
            return Err(RuntimeError::Capacity);
        }
        launcher.launch(&spec)?;
        let slot = self
            .runtimes
            .iter_mut()
            .find(|entry| entry.is_none())
            .ok_or(RuntimeError::Capacity)?;
        *slot = Some(RuntimeInstance {
            spec,
            state: RuntimeState::Starting,
        });
        Ok(())
    }

    /// Marks a runtime running only for its current tenant and generation.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn mark_running(
        &mut self,
        id: u64,
        tenant: crate::TenantId,
        generation: crate::Generation,
    ) -> Result<(), RuntimeError> {
        let runtime = self.find_mut(id)?;
        check_owner(runtime, tenant, generation)?;
        if runtime.state != RuntimeState::Starting {
            return Err(RuntimeError::InvalidState);
        }
        runtime.state = RuntimeState::Running;
        Ok(())
    }

    /// Stops a runtime after fencing stale callbacks.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn stop<L: RuntimeLauncher>(
        &mut self,
        launcher: &L,
        id: u64,
        tenant: crate::TenantId,
        generation: crate::Generation,
    ) -> Result<(), RuntimeError> {
        let runtime = self.find_mut(id)?;
        check_owner(runtime, tenant, generation)?;
        if runtime.state != RuntimeState::Starting && runtime.state != RuntimeState::Running {
            return Err(RuntimeError::InvalidState);
        }
        launcher.stop(&runtime.spec)?;
        runtime.state = RuntimeState::Stopped;
        Ok(())
    }

    /// Reads one runtime instance.
    #[must_use]
    pub fn get(&self, id: u64) -> Option<&RuntimeInstance> {
        self.runtimes
            .iter()
            .flatten()
            .find(|runtime| runtime.spec.id == id)
    }

    fn find_mut(&mut self, id: u64) -> Result<&mut RuntimeInstance, RuntimeError> {
        self.runtimes
            .iter_mut()
            .flatten()
            .find(|runtime| runtime.spec.id == id)
            .ok_or(RuntimeError::NotFound)
    }
}

impl<const MAX_RUNTIMES: usize> Default for RuntimeRegistry<MAX_RUNTIMES> {
    fn default() -> Self {
        Self::new()
    }
}

fn validate_spec(spec: &RuntimeSpec) -> Result<(), RuntimeError> {
    let key = spec.artifact_key.as_str();
    if key.trim().is_empty()
        || key.starts_with('/')
        || key.contains('\\')
        || key
            .split('/')
            .any(|part| part.is_empty() || part == "." || part == "..")
    {
        return Err(RuntimeError::InvalidArtifactKey);
    }
    if !spec.capabilities.filesystem {
        return Err(RuntimeError::MissingCapability);
    }
    if spec.mode == RuntimeMode::Native && !spec.capabilities.process {
        return Err(RuntimeError::MissingCapability);
    }
    Ok(())
}

fn check_owner(
    runtime: &RuntimeInstance,
    tenant: crate::TenantId,
    generation: crate::Generation,
) -> Result<(), RuntimeError> {
    if runtime.spec.tenant != tenant {
        return Err(RuntimeError::TenantBoundary);
    }
    if runtime.spec.generation != generation {
        return Err(RuntimeError::StaleGeneration);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn spec(mode: RuntimeMode) -> RuntimeSpec {
        RuntimeSpec {
            id: 1,
            tenant: crate::TenantId(7),
            deployment: crate::EntityId(4),
            mode,
            artifact_key: "artifacts/build-1".to_owned(),
            capabilities: RuntimeCapabilities {
                filesystem: true,
                network: false,
                process: mode == RuntimeMode::Native,
            },
            generation: crate::Generation(2),
        }
    }

    #[test]
    fn runtime_is_generation_fenced_and_policy_bounded() {
        let launcher = LocalRuntimeLauncher;
        let mut registry = RuntimeRegistry::<1>::new();
        registry.start(&launcher, spec(RuntimeMode::Wasi)).unwrap();
        assert_eq!(
            registry.mark_running(1, crate::TenantId(7), crate::Generation(1)),
            Err(RuntimeError::StaleGeneration)
        );
        registry
            .mark_running(1, crate::TenantId(7), crate::Generation(2))
            .unwrap();
        registry
            .stop(&launcher, 1, crate::TenantId(7), crate::Generation(2))
            .unwrap();
        assert_eq!(
            registry.get(1).map(|runtime| runtime.state),
            Some(RuntimeState::Stopped)
        );
    }

    #[test]
    fn native_runtime_requires_process_and_safe_artifact_policy() {
        let launcher = LocalRuntimeLauncher;
        let mut registry = RuntimeRegistry::<2>::new();
        let mut native = spec(RuntimeMode::Native);
        native.capabilities.process = false;
        assert_eq!(
            registry.start(&launcher, native),
            Err(RuntimeError::MissingCapability)
        );
        let mut traversal = spec(RuntimeMode::Wasi);
        traversal.artifact_key = "../escape".to_owned();
        assert_eq!(
            registry.start(&launcher, traversal),
            Err(RuntimeError::InvalidArtifactKey)
        );
    }
}
