//! Bounded actor mailboxes and supervisor state.

use std::collections::VecDeque;

/// Maximum retained supervisor transition events before they must be drained.
pub const MAX_SUPERVISOR_EVENTS: usize = 4096;

/// Stable identity for an actor in a supervisor tree.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub struct ActorId(pub u32);

/// Lifecycle state of a supervised actor.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ActorStatus {
    /// Registered but not running.
    Stopped,
    /// Processing messages.
    Running,
    /// Rejecting new work while existing work drains.
    Draining,
    /// Exhausted its restart policy.
    Failed,
}

/// Restart policy applied after an actor fault.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RestartPolicy {
    /// Never restart after a fault.
    Never,
    /// Restart at most the configured number of times.
    Limited {
        /// Maximum number of restarts.
        max_restarts: u32,
    },
}

/// Reason an actor stopped.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ExitReason {
    /// Normal completion.
    Completed,
    /// Explicit shutdown.
    Shutdown,
    /// Unexpected actor failure.
    Fault,
    /// A bounded resource was exhausted.
    Capacity,
}

/// Immutable supervisor registration for one actor.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ActorSpec {
    /// Actor identity.
    pub id: ActorId,
    /// Restart behavior after faults.
    pub restart_policy: RestartPolicy,
}

/// Observable actor state.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ActorRecord {
    /// Actor identity.
    pub id: ActorId,
    /// Current status.
    pub status: ActorStatus,
    /// Configured restart behavior.
    pub restart_policy: RestartPolicy,
    /// Number of restarts already consumed.
    pub restart_count: u32,
}

/// Supervisor transition event.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum SupervisorEvent {
    /// Actor entered the running state.
    Started {
        /// Actor identity.
        actor: ActorId,
    },
    /// Actor restarted after a fault.
    Restarted {
        /// Actor identity.
        actor: ActorId,
        /// Restart attempt number.
        attempt: u32,
    },
    /// Actor stopped normally.
    Stopped {
        /// Actor identity.
        actor: ActorId,
        /// Stop reason.
        reason: ExitReason,
    },
    /// Actor failed and will not restart.
    Failed {
        /// Actor identity.
        actor: ActorId,
        /// Failure reason.
        reason: ExitReason,
    },
}

/// Supervisor error.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum SupervisorError {
    /// No registration slots remain.
    Capacity,
    /// The actor identity is already registered.
    AlreadyExists,
    /// The actor identity is not registered.
    NotFound,
    /// The requested transition is not valid.
    InvalidTransition,
}

/// Bounded FIFO mailbox for one actor.
pub struct Mailbox<T, const CAPACITY: usize> {
    queue: VecDeque<T>,
}

impl<T, const CAPACITY: usize> Mailbox<T, CAPACITY> {
    /// Creates an empty mailbox.
    #[must_use]
    pub fn new() -> Self {
        Self {
            queue: VecDeque::with_capacity(CAPACITY),
        }
    }

    /// Enqueues a message or returns it unchanged when full.
    ///
    /// # Errors
    ///
    /// Returns the message when the mailbox has reached `CAPACITY`.
    pub fn send(&mut self, message: T) -> Result<(), T> {
        if self.queue.len() >= CAPACITY {
            return Err(message);
        }
        self.queue.push_back(message);
        Ok(())
    }

    /// Removes at most `limit` messages in FIFO order.
    pub fn drain_batch(&mut self, limit: usize) -> Vec<T> {
        let count = limit.min(self.queue.len());
        let mut messages = Vec::with_capacity(count.min(CAPACITY));
        for _ in 0..count {
            if let Some(message) = self.queue.pop_front() {
                messages.push(message);
            }
        }
        messages
    }

    /// Returns the number of queued messages.
    #[must_use]
    pub fn len(&self) -> usize {
        self.queue.len()
    }

    /// Returns whether the mailbox is empty.
    #[must_use]
    pub fn is_empty(&self) -> bool {
        self.queue.is_empty()
    }
}

impl<T, const CAPACITY: usize> Default for Mailbox<T, CAPACITY> {
    fn default() -> Self {
        Self::new()
    }
}

/// Fixed-capacity supervisor registry.
pub struct Supervisor<const MAX_ACTORS: usize> {
    actors: Vec<Option<ActorRecord>>,
    events: Vec<SupervisorEvent>,
}

impl<const MAX_ACTORS: usize> Supervisor<MAX_ACTORS> {
    /// Creates an empty supervisor.
    #[must_use]
    pub fn new() -> Self {
        Self {
            actors: vec![None; MAX_ACTORS],
            events: Vec::with_capacity(MAX_SUPERVISOR_EVENTS),
        }
    }

    /// Registers an actor in the stopped state.
    ///
    /// # Errors
    ///
    /// Returns [`SupervisorError::Capacity`] when the actor id is outside the
    /// fixed registry, or [`SupervisorError::AlreadyExists`] when occupied.
    pub fn register(&mut self, spec: ActorSpec) -> Result<(), SupervisorError> {
        let index = usize::try_from(spec.id.0).unwrap_or(usize::MAX);
        if index >= MAX_ACTORS {
            return Err(SupervisorError::Capacity);
        }
        if self.actors[index].is_some() {
            return Err(SupervisorError::AlreadyExists);
        }
        self.actors[index] = Some(ActorRecord {
            id: spec.id,
            status: ActorStatus::Stopped,
            restart_policy: spec.restart_policy,
            restart_count: 0,
        });
        Ok(())
    }

    /// Starts a registered actor.
    ///
    /// # Errors
    ///
    /// Returns a supervisor error when the actor is unknown or is not stopped.
    pub fn start(&mut self, actor: ActorId) -> Result<(), SupervisorError> {
        {
            let record = self.record_mut(actor)?;
            if record.status != ActorStatus::Stopped {
                return Err(SupervisorError::InvalidTransition);
            }
            record.status = ActorStatus::Running;
        }
        self.emit(SupervisorEvent::Started { actor });
        Ok(())
    }

    /// Marks an actor as draining.
    ///
    /// # Errors
    ///
    /// Returns a supervisor error when the actor is unknown or is not running.
    pub fn drain(&mut self, actor: ActorId) -> Result<(), SupervisorError> {
        let record = self.record_mut(actor)?;
        if record.status != ActorStatus::Running {
            return Err(SupervisorError::InvalidTransition);
        }
        record.status = ActorStatus::Draining;
        Ok(())
    }

    /// Applies a terminal result and the actor's restart policy.
    ///
    /// # Errors
    ///
    /// Returns a supervisor error when the actor is unknown or has already
    /// reached a terminal state.
    pub fn exit(&mut self, actor: ActorId, reason: ExitReason) -> Result<(), SupervisorError> {
        let event = {
            let record = self.record_mut(actor)?;
            if record.status != ActorStatus::Running && record.status != ActorStatus::Draining {
                return Err(SupervisorError::InvalidTransition);
            }
            let restart = matches!(reason, ExitReason::Fault | ExitReason::Capacity)
                && match record.restart_policy {
                    RestartPolicy::Never => false,
                    RestartPolicy::Limited { max_restarts } => record.restart_count < max_restarts,
                };
            if restart {
                record.restart_count = record.restart_count.saturating_add(1);
                record.status = ActorStatus::Running;
                SupervisorEvent::Restarted {
                    actor,
                    attempt: record.restart_count,
                }
            } else if matches!(reason, ExitReason::Fault | ExitReason::Capacity) {
                record.status = ActorStatus::Failed;
                SupervisorEvent::Failed { actor, reason }
            } else {
                record.status = ActorStatus::Stopped;
                SupervisorEvent::Stopped { actor, reason }
            }
        };
        self.emit(event);
        Ok(())
    }

    /// Returns the current state of an actor.
    pub fn actor(&self, actor: ActorId) -> Option<ActorRecord> {
        self.actors
            .get(usize::try_from(actor.0).unwrap_or(usize::MAX))
            .and_then(Option::as_ref)
            .copied()
    }

    /// Returns the number of running or draining actors.
    #[must_use]
    pub fn active_count(&self) -> usize {
        self.actors
            .iter()
            .flatten()
            .filter(|record| {
                record.status == ActorStatus::Running || record.status == ActorStatus::Draining
            })
            .count()
    }

    /// Drains supervisor transition events.
    pub fn drain_events(&mut self) -> Vec<SupervisorEvent> {
        std::mem::take(&mut self.events)
    }

    fn record_mut(&mut self, actor: ActorId) -> Result<&mut ActorRecord, SupervisorError> {
        self.actors
            .get_mut(usize::try_from(actor.0).unwrap_or(usize::MAX))
            .and_then(Option::as_mut)
            .ok_or(SupervisorError::NotFound)
    }

    fn emit(&mut self, event: SupervisorEvent) {
        assert!(
            self.events.len() < MAX_SUPERVISOR_EVENTS,
            "Janus supervisor event capacity exceeded"
        );
        self.events.push(event);
    }
}

impl<const MAX_ACTORS: usize> Default for Supervisor<MAX_ACTORS> {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn mailbox_is_bounded_and_fifo() {
        let mut mailbox = Mailbox::<u32, 2>::new();
        assert!(mailbox.send(1).is_ok());
        assert!(mailbox.send(2).is_ok());
        assert_eq!(mailbox.send(3), Err(3));
        assert_eq!(mailbox.drain_batch(1), vec![1]);
        assert_eq!(mailbox.drain_batch(8), vec![2]);
    }

    #[test]
    fn supervisor_restarts_then_fails_after_budget() {
        let mut supervisor = Supervisor::<4>::new();
        supervisor
            .register(ActorSpec {
                id: ActorId(1),
                restart_policy: RestartPolicy::Limited { max_restarts: 1 },
            })
            .unwrap();
        supervisor.start(ActorId(1)).unwrap();
        supervisor.exit(ActorId(1), ExitReason::Fault).unwrap();
        assert_eq!(
            supervisor.actor(ActorId(1)).map(|value| value.status),
            Some(ActorStatus::Running)
        );
        supervisor.exit(ActorId(1), ExitReason::Fault).unwrap();
        assert_eq!(
            supervisor.actor(ActorId(1)).map(|value| value.status),
            Some(ActorStatus::Failed)
        );
    }
}
