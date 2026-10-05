//! Bounded email outbox and provider-neutral delivery state.

/// Maximum email address length accepted by the outbox.
pub const MAX_EMAIL_ADDRESS_BYTES: usize = 320;
/// Maximum subject length accepted by the outbox.
pub const MAX_EMAIL_SUBJECT_BYTES: usize = 998;
/// Maximum body length accepted by the outbox.
pub const MAX_EMAIL_BODY_BYTES: usize = 64 * 1024;

/// Delivery state for one outbox message.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum EmailState {
    /// Waiting for a provider claim.
    Pending,
    /// Claimed by a provider worker.
    Sending,
    /// Accepted by the provider.
    Sent,
    /// Failed and eligible for retry or terminal failure.
    Failed,
}

/// Result of one provider delivery attempt.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum EmailDelivery {
    /// The provider accepted the message.
    Sent,
    /// The provider failed, but another attempt remains.
    Retry,
    /// The provider failed and the message is terminally failed.
    Failed,
}

/// Provider transport port implemented by SMTP or another mail service.
pub trait EmailTransport {
    /// Sends one claimed message and returns a provider error on rejection.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn send(&mut self, message: &EmailMessage) -> Result<(), String>;
}

/// Durable email intent.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct EmailMessage {
    /// Idempotent message identity.
    pub id: u64,
    /// Recipient address.
    pub to: String,
    /// Message subject.
    pub subject: String,
    /// Plaintext body.
    pub body: String,
    /// Current delivery state.
    pub state: EmailState,
    /// Number of provider attempts.
    pub attempts: u16,
    /// Maximum provider attempts.
    pub max_attempts: u16,
    /// Last provider error, when delivery failed.
    pub last_error: Option<String>,
}

/// Email outbox failure.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum EmailError {
    /// Required field is empty or exceeds its bound.
    InvalidMessage,
    /// Outbox capacity is exhausted.
    Capacity,
    /// Message does not exist.
    NotFound,
    /// Message is not eligible for this operation.
    InvalidState,
}

/// Bounded idempotent email outbox.
pub struct EmailOutbox<const MAX_MESSAGES: usize> {
    messages: Vec<Option<EmailMessage>>,
}

impl<const MAX_MESSAGES: usize> EmailOutbox<MAX_MESSAGES> {
    /// Creates an empty outbox.
    #[must_use]
    pub fn new() -> Self {
        Self {
            messages: vec![None; MAX_MESSAGES],
        }
    }

    /// Enqueues one message, treating duplicate IDs as idempotent success.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn enqueue(
        &mut self,
        id: u64,
        to: impl Into<String>,
        subject: impl Into<String>,
        body: impl Into<String>,
        max_attempts: u16,
    ) -> Result<(), EmailError> {
        let to = to.into().trim().to_owned();
        let subject = subject.into().trim().to_owned();
        let body = body.into();
        if to.is_empty()
            || to.len() > MAX_EMAIL_ADDRESS_BYTES
            || subject.is_empty()
            || subject.len() > MAX_EMAIL_SUBJECT_BYTES
            || body.is_empty()
            || body.len() > MAX_EMAIL_BODY_BYTES
            || max_attempts == 0
        {
            return Err(EmailError::InvalidMessage);
        }
        if self
            .messages
            .iter()
            .flatten()
            .any(|message| message.id == id)
        {
            return Ok(());
        }
        let slot = self
            .messages
            .iter_mut()
            .find(|entry| entry.is_none())
            .ok_or(EmailError::Capacity)?;
        *slot = Some(EmailMessage {
            id,
            to,
            subject,
            body,
            state: EmailState::Pending,
            attempts: 0,
            max_attempts,
            last_error: None,
        });
        Ok(())
    }

    /// Claims the next pending message for one provider attempt.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn claim(&mut self, id: u64) -> Result<EmailMessage, EmailError> {
        let message = self.find_mut(id)?;
        if message.state != EmailState::Pending || message.attempts >= message.max_attempts {
            return Err(EmailError::InvalidState);
        }
        message.state = EmailState::Sending;
        message.attempts = message.attempts.saturating_add(1);
        Ok(message.clone())
    }

    /// Records successful provider acceptance.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn mark_sent(&mut self, id: u64) -> Result<(), EmailError> {
        let message = self.find_mut(id)?;
        if message.state != EmailState::Sending {
            return Err(EmailError::InvalidState);
        }
        message.state = EmailState::Sent;
        message.last_error = None;
        Ok(())
    }

    /// Records a provider failure and returns whether a retry remains.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn mark_failed(&mut self, id: u64, error: impl Into<String>) -> Result<bool, EmailError> {
        let message = self.find_mut(id)?;
        if message.state != EmailState::Sending {
            return Err(EmailError::InvalidState);
        }
        message.last_error = Some(error.into());
        let retry = message.attempts < message.max_attempts;
        message.state = if retry {
            EmailState::Pending
        } else {
            EmailState::Failed
        };
        Ok(retry)
    }

    /// Claims, sends, and records one provider attempt through the transport port.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn deliver<T: EmailTransport>(
        &mut self,
        id: u64,
        transport: &mut T,
    ) -> Result<EmailDelivery, EmailError> {
        let message = self.claim(id)?;
        match transport.send(&message) {
            Ok(()) => {
                self.mark_sent(id)?;
                Ok(EmailDelivery::Sent)
            }
            Err(error) => {
                let retry = self.mark_failed(id, error)?;
                Ok(if retry {
                    EmailDelivery::Retry
                } else {
                    EmailDelivery::Failed
                })
            }
        }
    }

    /// Returns one message by identity.
    #[must_use]
    pub fn get(&self, id: u64) -> Option<&EmailMessage> {
        self.messages
            .iter()
            .flatten()
            .find(|message| message.id == id)
    }

    fn find_mut(&mut self, id: u64) -> Result<&mut EmailMessage, EmailError> {
        self.messages
            .iter_mut()
            .flatten()
            .find(|message| message.id == id)
            .ok_or(EmailError::NotFound)
    }
}

impl<const MAX_MESSAGES: usize> Default for EmailOutbox<MAX_MESSAGES> {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn outbox_is_idempotent_and_retries_provider_failures() {
        let mut outbox = EmailOutbox::<1>::new();
        outbox
            .enqueue(1, "user@example.com", "Verify", "code 123456", 2)
            .unwrap();
        outbox
            .enqueue(1, "user@example.com", "Verify", "duplicate", 2)
            .unwrap();
        assert_eq!(outbox.claim(1).unwrap().attempts, 1);
        assert_eq!(outbox.mark_failed(1, "smtp unavailable"), Ok(true));
        assert_eq!(outbox.claim(1).unwrap().attempts, 2);
        assert_eq!(outbox.mark_failed(1, "smtp unavailable"), Ok(false));
        assert_eq!(
            outbox.get(1).map(|message| message.state),
            Some(EmailState::Failed)
        );
    }

    #[test]
    fn outbox_rejects_invalid_messages_and_invalid_transitions() {
        let mut outbox = EmailOutbox::<1>::new();
        assert_eq!(
            outbox.enqueue(1, "", "Verify", "body", 1),
            Err(EmailError::InvalidMessage)
        );
        outbox
            .enqueue(1, "user@example.com", "Verify", "body", 1)
            .unwrap();
        assert_eq!(outbox.mark_sent(1), Err(EmailError::InvalidState));
        outbox.claim(1).unwrap();
        outbox.mark_sent(1).unwrap();
        assert_eq!(outbox.claim(1), Err(EmailError::InvalidState));
    }

    struct TestTransport {
        failures_remaining: u8,
    }

    impl EmailTransport for TestTransport {
        fn send(&mut self, _message: &EmailMessage) -> Result<(), String> {
            if self.failures_remaining > 0 {
                self.failures_remaining -= 1;
                Err("provider unavailable".to_owned())
            } else {
                Ok(())
            }
        }
    }

    #[test]
    fn delivery_port_records_retry_then_provider_success() {
        let mut outbox = EmailOutbox::<1>::new();
        outbox
            .enqueue(1, "user@example.com", "Verify", "body", 2)
            .unwrap();
        let mut transport = TestTransport {
            failures_remaining: 1,
        };
        assert_eq!(outbox.deliver(1, &mut transport), Ok(EmailDelivery::Retry));
        assert_eq!(outbox.deliver(1, &mut transport), Ok(EmailDelivery::Sent));
        assert_eq!(
            outbox.get(1).map(|message| message.state),
            Some(EmailState::Sent)
        );
    }
}
