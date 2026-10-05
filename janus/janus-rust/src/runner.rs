//! Bounded runner registration, capability, heartbeat, and lease state.

/// Maximum runner capability name length.
pub const MAX_RUNNER_CAPABILITY_BYTES: usize = 64;

/// Runner availability state.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RunnerState {
    /// Registered and available for work.
    Available,
    /// Currently executing one claimed job.
    Busy,
    /// No new work should be assigned.
    Draining,
    /// Heartbeat lease expired.
    Offline,
}

/// Tenant-owned runner registration.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Runner {
    /// Stable runner identity.
    pub id: u64,
    /// Owning tenant.
    pub tenant: crate::TenantId,
    /// Current runner state.
    pub state: RunnerState,
    /// Advertised capabilities.
    pub capabilities: Vec<String>,
    /// Last heartbeat timestamp.
    pub last_heartbeat: u64,
    /// Timestamp after which the runner is considered offline.
    pub lease_until: u64,
}

/// Runner registry operation failure.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RunnerError {
    /// Runner identity already exists.
    AlreadyExists,
    /// Registry capacity is exhausted.
    Capacity,
    /// Runner does not exist.
    NotFound,
    /// Runner belongs to another tenant.
    TenantBoundary,
    /// Capability list is empty, duplicated, or oversized.
    InvalidCapabilities,
    /// Runner is not eligible for this transition.
    InvalidState,
}

/// Bounded runner registry with heartbeat leases.
pub struct RunnerRegistry<const MAX_RUNNERS: usize> {
    runners: Vec<Option<Runner>>,
}

impl<const MAX_RUNNERS: usize> RunnerRegistry<MAX_RUNNERS> {
    /// Creates an empty registry.
    #[must_use]
    pub fn new() -> Self {
        Self {
            runners: vec![None; MAX_RUNNERS],
        }
    }

    /// Hydrates runner leases from the authoritative `SpacetimeDB` projection.
    ///
    /// # Errors
    ///
    /// Returns an error when a durable runner exceeds capacity or contains an
    /// invalid lifecycle state.
    pub fn hydrate(
        &mut self,
        runners: impl IntoIterator<Item = Runner>,
    ) -> Result<(), RunnerError> {
        let mut slots: Vec<Option<Runner>> = vec![None; MAX_RUNNERS];
        for runner in runners {
            if runner.id == 0
                || runner.tenant.0 == 0
                || runner.capabilities.is_empty()
                || runner
                    .capabilities
                    .iter()
                    .any(|capability| capability.trim().is_empty())
                || slots
                    .iter()
                    .flatten()
                    .any(|current| current.id == runner.id)
            {
                return Err(RunnerError::InvalidCapabilities);
            }
            if !matches!(
                runner.state,
                RunnerState::Available
                    | RunnerState::Busy
                    | RunnerState::Draining
                    | RunnerState::Offline
            ) {
                return Err(RunnerError::InvalidState);
            }
            let slot = slots
                .iter_mut()
                .find(|slot| slot.is_none())
                .ok_or(RunnerError::Capacity)?;
            *slot = Some(runner);
        }
        self.runners = slots;
        Ok(())
    }

    /// Registers one runner with a heartbeat lease.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn register(
        &mut self,
        id: u64,
        tenant: crate::TenantId,
        capabilities: Vec<String>,
        now: u64,
        lease_seconds: u64,
    ) -> Result<(), RunnerError> {
        if self.runners.iter().flatten().any(|runner| runner.id == id) {
            return Err(RunnerError::AlreadyExists);
        }
        if capabilities.is_empty()
            || capabilities.iter().any(|capability| {
                capability.trim().is_empty() || capability.len() > MAX_RUNNER_CAPABILITY_BYTES
            })
            || has_duplicate_capabilities(&capabilities)
        {
            return Err(RunnerError::InvalidCapabilities);
        }
        let slot = self
            .runners
            .iter_mut()
            .find(|entry| entry.is_none())
            .ok_or(RunnerError::Capacity)?;
        *slot = Some(Runner {
            id,
            tenant,
            state: RunnerState::Available,
            capabilities,
            last_heartbeat: now,
            lease_until: now.saturating_add(lease_seconds),
        });
        Ok(())
    }

    /// Renews a runner heartbeat and restores an offline runner to available.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn heartbeat(
        &mut self,
        id: u64,
        tenant: crate::TenantId,
        now: u64,
        lease_seconds: u64,
    ) -> Result<(), RunnerError> {
        let runner = self.find_mut(id)?;
        if runner.tenant != tenant {
            return Err(RunnerError::TenantBoundary);
        }
        runner.last_heartbeat = now;
        runner.lease_until = now.saturating_add(lease_seconds);
        if runner.state == RunnerState::Offline {
            runner.state = RunnerState::Available;
        }
        Ok(())
    }

    /// Claims one available runner for a tenant and required capability.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn claim(
        &mut self,
        tenant: crate::TenantId,
        capability: &str,
        now: u64,
    ) -> Result<u64, RunnerError> {
        let runner = self
            .runners
            .iter_mut()
            .flatten()
            .find(|runner| {
                runner.tenant == tenant
                    && runner.state == RunnerState::Available
                    && runner.lease_until > now
                    && runner.capabilities.iter().any(|value| value == capability)
            })
            .ok_or(RunnerError::NotFound)?;
        runner.state = RunnerState::Busy;
        Ok(runner.id)
    }

    /// Releases a busy runner back to the available pool.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn release(&mut self, id: u64, tenant: crate::TenantId) -> Result<(), RunnerError> {
        let runner = self.find_mut(id)?;
        if runner.tenant != tenant {
            return Err(RunnerError::TenantBoundary);
        }
        if runner.state != RunnerState::Busy {
            return Err(RunnerError::InvalidState);
        }
        runner.state = RunnerState::Available;
        Ok(())
    }

    /// Marks heartbeat-expired runners offline.
    pub fn expire(&mut self, now: u64) {
        for runner in self.runners.iter_mut().flatten() {
            if runner.lease_until <= now && runner.state != RunnerState::Offline {
                runner.state = RunnerState::Offline;
            }
        }
    }

    /// Reads one runner registration.
    #[must_use]
    pub fn get(&self, id: u64) -> Option<&Runner> {
        self.runners.iter().flatten().find(|runner| runner.id == id)
    }

    /// Returns a bounded snapshot of runners owned by one tenant.
    #[must_use]
    pub fn list_for_tenant(&self, tenant: crate::TenantId) -> Vec<Runner> {
        self.runners
            .iter()
            .flatten()
            .filter(|runner| runner.tenant == tenant)
            .cloned()
            .collect()
    }

    fn find_mut(&mut self, id: u64) -> Result<&mut Runner, RunnerError> {
        self.runners
            .iter_mut()
            .flatten()
            .find(|runner| runner.id == id)
            .ok_or(RunnerError::NotFound)
    }
}

impl<const MAX_RUNNERS: usize> Default for RunnerRegistry<MAX_RUNNERS> {
    fn default() -> Self {
        Self::new()
    }
}

fn has_duplicate_capabilities(capabilities: &[String]) -> bool {
    capabilities.iter().enumerate().any(|(index, capability)| {
        capabilities[..index]
            .iter()
            .any(|previous| previous == capability)
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn runner_heartbeats_claims_releases_and_expires() {
        let mut registry = RunnerRegistry::<2>::new();
        registry
            .register(1, crate::TenantId(7), vec!["wasm".to_owned()], 10, 5)
            .unwrap();
        assert_eq!(registry.claim(crate::TenantId(7), "wasm", 14), Ok(1));
        assert_eq!(registry.release(1, crate::TenantId(7)), Ok(()));
        registry.expire(15);
        assert_eq!(
            registry.get(1).map(|runner| runner.state),
            Some(RunnerState::Offline)
        );
        registry.heartbeat(1, crate::TenantId(7), 16, 5).unwrap();
        assert_eq!(
            registry.get(1).map(|runner| runner.state),
            Some(RunnerState::Available)
        );
    }

    #[test]
    fn runner_registry_enforces_tenant_and_capability_boundaries() {
        let mut registry = RunnerRegistry::<1>::new();
        assert_eq!(
            registry.register(
                1,
                crate::TenantId(7),
                vec!["wasm".to_owned(), "wasm".to_owned()],
                0,
                5
            ),
            Err(RunnerError::InvalidCapabilities)
        );
        registry
            .register(1, crate::TenantId(7), vec!["wasm".to_owned()], 0, 5)
            .unwrap();
        assert_eq!(
            registry.claim(crate::TenantId(8), "wasm", 1),
            Err(RunnerError::NotFound)
        );
        assert_eq!(
            registry.heartbeat(1, crate::TenantId(8), 1, 5),
            Err(RunnerError::TenantBoundary)
        );
    }
}
