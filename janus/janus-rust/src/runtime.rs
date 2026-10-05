//! Tokio adapter for running one actor-owned world in production.

use std::time::Duration;

use tokio::sync::{mpsc, oneshot};
use tokio::task::JoinHandle;

use crate::{
    record_performance, AuthorizationError, Command, Config, ConfigError, Effect, Event, Principal,
    TracingSink, World,
};

/// Handle used by an HTTP or supervisor adapter to submit commands.
pub struct ControlActorHandle {
    commands: mpsc::Sender<Command>,
    shutdown: Option<oneshot::Sender<()>>,
}

/// Channels and join handle returned when a control actor is spawned.
pub type ControlActorParts<const MAX_BUILDS: usize, const MAX_COMMANDS: usize> = (
    ControlActorHandle,
    mpsc::Receiver<Event>,
    mpsc::Receiver<Effect>,
    JoinHandle<World<MAX_BUILDS, MAX_COMMANDS>>,
);

/// Failure while requesting actor shutdown.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ShutdownError {
    /// Shutdown was already requested for this handle.
    AlreadyRequested,
    /// The actor task has already stopped.
    ActorUnavailable,
}

/// Failure while authorizing or delivering a command to the Tokio actor.
#[derive(Debug, Eq, PartialEq)]
pub enum AuthorizedSendError {
    /// The principal cannot perform the requested operation.
    Authorization(AuthorizationError),
    /// The actor task has stopped before accepting the command.
    ActorUnavailable(Box<Command>),
}

impl ControlActorHandle {
    /// Authorizes a command before applying async mailbox backpressure.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub async fn send_authorized(
        &self,
        principal: &Principal,
        command: Command,
    ) -> Result<(), AuthorizedSendError> {
        let (tenant, permission) = command.authorization();
        let authorization = tenant.map_or_else(
            || principal.authorize(principal.tenant, permission),
            |tenant| principal.authorize(tenant, permission),
        );
        authorization.map_err(AuthorizedSendError::Authorization)?;
        self.commands
            .send(command)
            .await
            .map_err(|error| AuthorizedSendError::ActorUnavailable(Box::new(error.0)))
    }

    /// Requests a graceful actor shutdown.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn shutdown(&mut self) -> Result<(), ShutdownError> {
        self.shutdown
            .take()
            .ok_or(ShutdownError::AlreadyRequested)
            .and_then(|sender| {
                sender
                    .send(())
                    .map_err(|()| ShutdownError::ActorUnavailable)
            })
    }
}

/// Spawns a Tokio actor around one deterministic ECS world.
#[must_use]
pub fn spawn_control_actor<const MAX_BUILDS: usize, const MAX_COMMANDS: usize>(
    world: World<MAX_BUILDS, MAX_COMMANDS>,
    mailbox_capacity: usize,
    tick_interval: Duration,
) -> ControlActorParts<MAX_BUILDS, MAX_COMMANDS> {
    spawn_control_actor_inner(world, mailbox_capacity, tick_interval, None)
}

fn spawn_control_actor_inner<const MAX_BUILDS: usize, const MAX_COMMANDS: usize>(
    world: World<MAX_BUILDS, MAX_COMMANDS>,
    mailbox_capacity: usize,
    tick_interval: Duration,
    config: Option<Config>,
) -> ControlActorParts<MAX_BUILDS, MAX_COMMANDS> {
    let capacity = mailbox_capacity.max(1);
    let (commands, inbox) = mpsc::channel(capacity);
    let (events, output_events) = mpsc::channel(capacity);
    let (effects, output) = mpsc::channel(capacity);
    let (shutdown, stop) = oneshot::channel();
    let handle = ControlActorHandle {
        commands,
        shutdown: Some(shutdown),
    };
    let interval = if tick_interval.is_zero() {
        Duration::from_millis(1)
    } else {
        tick_interval
    };
    let join = tokio::spawn(run_control_actor(
        world, inbox, events, effects, stop, interval, config,
    ));
    (handle, output_events, output, join)
}

/// Spawns a control actor only after validating operator configuration.
///
/// # Errors
///
/// Returns an error when the operation cannot satisfy its input or
/// bounded-state contract.
pub fn spawn_control_actor_checked<const MAX_BUILDS: usize, const MAX_COMMANDS: usize>(
    config: Config,
    world: World<MAX_BUILDS, MAX_COMMANDS>,
    mailbox_capacity: usize,
    tick_interval: Duration,
) -> Result<ControlActorParts<MAX_BUILDS, MAX_COMMANDS>, ConfigError> {
    config.validate_for::<MAX_BUILDS, MAX_COMMANDS>()?;
    Ok(spawn_control_actor_inner(
        world,
        mailbox_capacity,
        tick_interval,
        Some(config),
    ))
}

async fn run_control_actor<const MAX_BUILDS: usize, const MAX_COMMANDS: usize>(
    mut world: World<MAX_BUILDS, MAX_COMMANDS>,
    mut inbox: mpsc::Receiver<Command>,
    events: mpsc::Sender<Event>,
    effects: mpsc::Sender<Effect>,
    mut stop: oneshot::Receiver<()>,
    tick_interval: Duration,
    config: Option<Config>,
) -> World<MAX_BUILDS, MAX_COMMANDS> {
    let mut ticker = tokio::time::interval(tick_interval);
    let mut trace = TracingSink;
    while stop.try_recv().is_err() {
        for _ in 0..MAX_COMMANDS {
            match inbox.try_recv() {
                Ok(command) => {
                    if world.enqueue(command).is_err() {
                        tracing::warn!(target: "janus", "control actor mailbox capacity reached");
                    }
                }
                Err(mpsc::error::TryRecvError::Empty) => break,
                Err(mpsc::error::TryRecvError::Disconnected) => return world,
            }
        }
        ticker.tick().await;
        if stop.try_recv().is_ok() {
            break;
        }
        let tick_span = tracing::debug_span!(target: "janus", "actor tick", actor = "control");
        tick_span.in_scope(|| {
            world.tick(&mut trace);
            record_performance(world.performance());
        });
        for event in world.drain_events() {
            if events.send(event).await.is_err() {
                break;
            }
        }
        for effect in world.drain_effects() {
            if let Some(config) = config.as_ref() {
                if let Err(error) = config.validate_effect(&effect) {
                    tracing::error!(
                        target: "janus",
                        ?error,
                        "effect rejected by configured capabilities"
                    );
                    continue;
                }
            }
            if effects.send(effect).await.is_err() {
                break;
            }
        }
    }
    world
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{BuildState, CommandId, EntityId, Permission, Principal, SubjectId, TenantId};

    #[test]
    fn tokio_actor_emits_the_same_build_effect_as_simulation() {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_time()
            .build()
            .unwrap();
        runtime.block_on(async {
            let (mut handle, mut events, mut effects, join) =
                spawn_control_actor::<8, 16>(World::new(), 4, Duration::from_millis(1));
            let principal =
                Principal::new(SubjectId(1), TenantId(1)).with_permission(Permission::SubmitBuilds);
            assert_eq!(
                handle
                    .send_authorized(
                        &principal,
                        Command::SubmitBuild {
                            command_id: CommandId(99),
                            build: EntityId(99),
                            tenant: TenantId(2),
                        },
                    )
                    .await,
                Err(AuthorizedSendError::Authorization(
                    crate::AuthorizationError::TenantBoundary
                ))
            );
            handle
                .send_authorized(
                    &principal,
                    Command::SubmitBuild {
                        command_id: CommandId(1),
                        build: EntityId(1),
                        tenant: TenantId(1),
                    },
                )
                .await
                .unwrap();
            assert!(matches!(
                events.recv().await,
                Some(Event::BuildAccepted {
                    build: EntityId(1),
                    ..
                })
            ));
            assert!(matches!(
                effects.recv().await,
                Some(Effect::RunBuild {
                    build: EntityId(1),
                    ..
                })
            ));
            handle.shutdown().unwrap();
            let world = join.await.unwrap();
            assert_eq!(
                world.build(EntityId(1)).map(|build| build.state),
                Some(BuildState::Running)
            );
        });
    }
}
