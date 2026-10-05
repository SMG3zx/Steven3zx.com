//! Validated, immutable startup configuration.

use crate::Effect;

/// Fixed capacities required by the bounded control plane.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct CapacityConfig {
    /// Maximum build entities in one world.
    pub max_builds: u32,
    /// Maximum mailbox commands in one world.
    pub max_commands: u32,
    /// Maximum external resource claims.
    pub max_resources: u32,
    /// Maximum messages processed per actor tick.
    pub max_messages_per_tick: u32,
}

/// Per-tenant admission quota.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct TenantQuota {
    /// Maximum build entities.
    pub max_builds: u32,
    /// Maximum deployment entities.
    pub max_deployments: u32,
    /// Maximum active runtime processes.
    pub max_running_processes: u32,
}

/// Capabilities required to execute external effects.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct EffectCapabilities {
    /// May create and terminate child processes.
    pub process: bool,
    /// May read or write the configured artifact area.
    pub filesystem: bool,
    /// May make outbound network connections.
    pub network: bool,
}

/// Capabilities granted to an effect actor.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct Capabilities {
    /// Capabilities required to execute external effects.
    pub effects: EffectCapabilities,
    /// May append durable events and snapshots.
    pub persistence: bool,
}

/// Configuration validation failure.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ConfigError {
    /// A capacity was zero.
    ZeroCapacity,
    /// A tenant quota exceeds its world capacity.
    QuotaExceedsCapacity,
    /// A required effect capability was not granted.
    MissingCapability,
    /// Configured capacities exceed the actor's compile-time bounds.
    CapacityMismatch,
}

/// Immutable Janus startup configuration.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Config {
    /// Global fixed capacities.
    pub capacities: CapacityConfig,
    /// Default tenant quota.
    pub default_quota: TenantQuota,
    /// Capabilities held by the configured runtime.
    pub capabilities: Capabilities,
}

impl Config {
    /// Validates boundedness, quota relationships, and required capabilities.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub const fn validate(&self) -> Result<(), ConfigError> {
        let capacities = self.capacities;
        if capacities.max_builds == 0
            || capacities.max_commands == 0
            || capacities.max_resources == 0
            || capacities.max_messages_per_tick == 0
        {
            return Err(ConfigError::ZeroCapacity);
        }
        let quota = self.default_quota;
        if quota.max_builds > capacities.max_builds {
            return Err(ConfigError::QuotaExceedsCapacity);
        }
        if quota.max_deployments > capacities.max_builds {
            return Err(ConfigError::QuotaExceedsCapacity);
        }
        if quota.max_running_processes > capacities.max_resources {
            return Err(ConfigError::QuotaExceedsCapacity);
        }
        if !self.capabilities.persistence {
            return Err(ConfigError::MissingCapability);
        }
        Ok(())
    }

    /// Validates configuration against one actor's compile-time capacities.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn validate_for<const MAX_BUILDS: usize, const MAX_COMMANDS: usize>(
        &self,
    ) -> Result<(), ConfigError> {
        self.validate()?;
        let max_builds = usize::try_from(self.capacities.max_builds)
            .map_err(|_| ConfigError::CapacityMismatch)?;
        let max_commands = usize::try_from(self.capacities.max_commands)
            .map_err(|_| ConfigError::CapacityMismatch)?;
        if max_builds > MAX_BUILDS || max_commands > MAX_COMMANDS {
            return Err(ConfigError::CapacityMismatch);
        }
        if !self.capabilities.effects.process
            || !self.capabilities.effects.filesystem
            || !self.capabilities.effects.network
        {
            return Err(ConfigError::MissingCapability);
        }
        Ok(())
    }

    /// Checks whether configured capabilities may execute one external effect.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub const fn validate_effect(&self, effect: &Effect) -> Result<(), ConfigError> {
        let permitted = match effect {
            Effect::RunBuild { .. } => {
                self.capabilities.effects.process && self.capabilities.effects.filesystem
            }
            Effect::StartRuntime { .. } => {
                self.capabilities.effects.process && self.capabilities.effects.network
            }
            Effect::StopRuntime { .. } => self.capabilities.effects.process,
        };
        if permitted {
            Ok(())
        } else {
            Err(ConfigError::MissingCapability)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn valid() -> Config {
        Config {
            capacities: CapacityConfig {
                max_builds: 8,
                max_commands: 16,
                max_resources: 8,
                max_messages_per_tick: 4,
            },
            default_quota: TenantQuota {
                max_builds: 4,
                max_deployments: 4,
                max_running_processes: 4,
            },
            capabilities: Capabilities {
                effects: EffectCapabilities {
                    process: true,
                    filesystem: true,
                    network: true,
                },
                persistence: true,
            },
        }
    }

    #[test]
    fn configuration_rejects_unbounded_or_missing_resources() {
        let mut config = valid();
        config.capacities.max_commands = 0;
        assert_eq!(config.validate(), Err(ConfigError::ZeroCapacity));
        config = valid();
        config.capabilities.persistence = false;
        assert_eq!(config.validate(), Err(ConfigError::MissingCapability));
    }

    #[test]
    fn configuration_must_fit_the_actor_type_bounds() {
        assert_eq!(
            valid().validate_for::<4, 16>(),
            Err(ConfigError::CapacityMismatch)
        );
        assert!(valid().validate_for::<8, 16>().is_ok());
    }

    #[test]
    fn effect_capabilities_are_checked_per_operation() {
        let mut config = valid();
        config.capabilities.effects.process = false;
        let effect = Effect::RunBuild {
            build: crate::EntityId(1),
            tenant: crate::TenantId(1),
            generation: crate::Generation(1),
        };
        assert_eq!(
            config.validate_effect(&effect),
            Err(ConfigError::MissingCapability)
        );
        config.capabilities.effects.process = true;
        config.capabilities.effects.filesystem = true;
        assert!(config.validate_effect(&effect).is_ok());
    }
}
