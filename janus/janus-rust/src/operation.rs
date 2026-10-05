//! Monotonic operation lifecycle shared by adapters and local verification.

/// Terminal or in-flight status of a durable operation.
#[derive(
    Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, serde::Deserialize, serde::Serialize,
)]
pub enum OperationStatus {
    /// Operation has been accepted but not started.
    Pending,
    /// Operation is being processed.
    Processing,
    /// Operation completed successfully.
    Succeeded,
    /// Operation completed with a failure.
    Failed,
    /// Operation exhausted its retry budget.
    DeadLettered,
    /// Operation exceeded its allowed lifetime.
    Expired,
}

impl OperationStatus {
    /// Returns the monotonic lifecycle rank used by transition validation.
    #[must_use]
    pub const fn rank(self) -> u8 {
        match self {
            Self::Pending => 0,
            Self::Processing => 1,
            Self::Succeeded | Self::Failed | Self::DeadLettered | Self::Expired => 2,
        }
    }
}

/// Failure returned when an operation cannot make the requested transition.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum OperationError {
    /// The operation kind was empty after trimming.
    KindRequired,
    /// A terminal operation was asked to transition again.
    InvalidTransition,
}

/// Durable operation state with bounded, owned text fields.
#[derive(Clone, Debug, Eq, PartialEq, serde::Deserialize, serde::Serialize)]
pub struct Operation {
    /// Stable operation identifier.
    pub id: String,
    /// Operation kind used by the worker dispatcher.
    pub kind: String,
    /// Request correlation identifier.
    pub correlation_id: String,
    /// Owning tenant identifier.
    pub tenant_id: String,
    /// Current monotonic status.
    pub status: OperationStatus,
    /// Creation timestamp supplied by the caller.
    pub created_at: u64,
    /// Last transition timestamp supplied by the caller.
    pub updated_at: u64,
    /// Successful result payload, if any.
    pub result: Option<String>,
    /// Failure payload, if any.
    pub failure: Option<String>,
}

impl Operation {
    /// Creates a pending operation after validating its kind.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn new(
        id: impl Into<String>,
        kind: impl Into<String>,
        correlation_id: impl Into<String>,
        tenant_id: impl Into<String>,
        now: u64,
    ) -> Result<Self, OperationError> {
        let kind = kind.into().trim().to_owned();
        if kind.is_empty() {
            return Err(OperationError::KindRequired);
        }
        Ok(Self {
            id: id.into().trim().to_owned(),
            kind,
            correlation_id: correlation_id.into().trim().to_owned(),
            tenant_id: tenant_id.into().trim().to_owned(),
            status: OperationStatus::Pending,
            created_at: now,
            updated_at: now,
            result: None,
            failure: None,
        })
    }

    /// Moves a pending operation into processing.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn mark_processing(mut self, now: u64) -> Result<Self, OperationError> {
        if !matches!(
            self.status,
            OperationStatus::Pending | OperationStatus::Processing
        ) {
            return Err(OperationError::InvalidTransition);
        }
        self.status = OperationStatus::Processing;
        self.updated_at = now;
        Ok(self)
    }

    /// Completes a pending or processing operation successfully.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn complete_success(
        mut self,
        result: impl Into<String>,
        now: u64,
    ) -> Result<Self, OperationError> {
        self.ensure_in_flight()?;
        self.status = OperationStatus::Succeeded;
        self.result = Some(result.into());
        self.failure = None;
        self.updated_at = now;
        Ok(self)
    }

    /// Completes a pending or processing operation with a failure.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn complete_failure(
        mut self,
        failure: impl Into<String>,
        now: u64,
    ) -> Result<Self, OperationError> {
        self.ensure_in_flight()?;
        self.status = OperationStatus::Failed;
        self.failure = Some(failure.into());
        self.result = None;
        self.updated_at = now;
        Ok(self)
    }

    const fn ensure_in_flight(&self) -> Result<(), OperationError> {
        if matches!(
            self.status,
            OperationStatus::Pending | OperationStatus::Processing
        ) {
            Ok(())
        } else {
            Err(OperationError::InvalidTransition)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lifecycle_is_monotonic_and_clears_opposite_payloads() {
        let operation = Operation::new("op-1", " build.enqueue ", "req-1", "tenant-1", 10)
            .unwrap()
            .mark_processing(11)
            .unwrap()
            .complete_failure("worker failed", 12)
            .unwrap();
        assert_eq!(operation.status, OperationStatus::Failed);
        assert_eq!(operation.status.rank(), 2);
        assert_eq!(operation.failure.as_deref(), Some("worker failed"));
        assert!(operation.result.is_none());
        assert!(operation.complete_success("late", 13).is_err());
    }

    #[test]
    fn pending_operations_can_complete_without_an_explicit_processing_step() {
        let operation = Operation::new("op-2", "build.enqueue", "", "tenant-1", 20)
            .unwrap()
            .complete_success("accepted", 21)
            .unwrap();
        assert_eq!(operation.status, OperationStatus::Succeeded);
        assert_eq!(operation.result.as_deref(), Some("accepted"));
    }

    #[test]
    fn empty_kinds_are_rejected() {
        assert_eq!(
            Operation::new("op-3", "  ", "", "tenant-1", 0),
            Err(OperationError::KindRequired)
        );
    }
}
