//! Bounded HTTP-facing operation registry.

use std::fs;
use std::io::{self, Read};
use std::path::Path;

use crate::{Operation, OperationError};

/// Maximum encoded operation projection accepted by the file adapter.
pub const MAX_OPERATION_FILE_BYTES: usize = 4 * 1024 * 1024;

/// Failure returned by the bounded operation registry.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum OperationRegistryError {
    /// The registry has no free slot.
    Capacity,
    /// An operation with the same identifier already exists.
    AlreadyExists,
}

/// Failure while opening or persisting an operation projection.
#[derive(Debug)]
pub enum FileOperationError {
    /// The underlying file operation failed.
    Io(io::Error),
    /// The file was malformed or contained duplicate operation IDs.
    Corrupt,
    /// The encoded file or registry exceeded its bound.
    Capacity,
    /// The requested lifecycle transition is invalid.
    InvalidTransition,
}

/// Adapter selected by the HTTP process for operation projections.
pub enum OperationBackend<const MAX_OPERATIONS: usize> {
    /// Bounded process-local fallback for offline development.
    Memory(OperationRegistry<MAX_OPERATIONS>),
    /// Restartable file-backed projection for local production-like runs.
    File(FileOperationStore<MAX_OPERATIONS>),
}

impl<const MAX_OPERATIONS: usize> OperationBackend<MAX_OPERATIONS> {
    /// Opens a file-backed operation projection.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn open_file(path: impl Into<String>) -> Result<Self, FileOperationError> {
        Ok(Self::File(FileOperationStore::open(path)?))
    }

    /// Reads one operation from the selected adapter.
    #[must_use]
    pub fn get(&self, id: &str) -> Option<&Operation> {
        match self {
            Self::Memory(registry) => registry.get(id),
            Self::File(store) => store.get(id),
        }
    }

    /// Inserts one operation into the selected adapter.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn insert(&mut self, operation: Operation) -> Result<(), FileOperationError> {
        match self {
            Self::Memory(registry) => registry.insert(operation).map_err(|error| match error {
                OperationRegistryError::Capacity => FileOperationError::Capacity,
                OperationRegistryError::AlreadyExists => FileOperationError::Corrupt,
            }),
            Self::File(store) => store.insert(operation),
        }
    }

    /// Applies one monotonic operation transition through the selected adapter.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation is missing, the transition is
    /// invalid, or the durable projection cannot be rewritten.
    pub fn transition<F>(&mut self, id: &str, transition: F) -> Result<(), FileOperationError>
    where
        F: FnOnce(Operation) -> Result<Operation, OperationError>,
    {
        match self {
            Self::Memory(registry) => registry.transition(id, transition),
            Self::File(store) => store.transition(id, transition),
        }
    }
}

impl<const MAX_OPERATIONS: usize> Default for OperationBackend<MAX_OPERATIONS> {
    fn default() -> Self {
        Self::Memory(OperationRegistry::new())
    }
}

/// In-memory operation projection used by the HTTP adapter.
#[derive(Clone)]
pub struct OperationRegistry<const MAX_OPERATIONS: usize> {
    operations: Vec<Option<Operation>>,
}

impl<const MAX_OPERATIONS: usize> OperationRegistry<MAX_OPERATIONS> {
    /// Creates an empty bounded operation registry.
    #[must_use]
    pub fn new() -> Self {
        Self {
            operations: vec![None; MAX_OPERATIONS],
        }
    }

    /// Inserts one operation without allowing identifier replacement.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn insert(&mut self, operation: Operation) -> Result<(), OperationRegistryError> {
        if self
            .operations
            .iter()
            .flatten()
            .any(|current| current.id == operation.id)
        {
            return Err(OperationRegistryError::AlreadyExists);
        }
        let slot = self
            .operations
            .iter_mut()
            .find(|slot| slot.is_none())
            .ok_or(OperationRegistryError::Capacity)?;
        *slot = Some(operation);
        Ok(())
    }

    /// Applies a transition without exposing a mutable operation reference.
    ///
    /// The replacement is committed only after the transition succeeds, so a
    /// rejected lifecycle operation cannot partially mutate the projection.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation is missing or the transition is
    /// invalid.
    pub fn transition<F>(&mut self, id: &str, transition: F) -> Result<(), FileOperationError>
    where
        F: FnOnce(Operation) -> Result<Operation, OperationError>,
    {
        let index = self
            .operations
            .iter()
            .position(|slot| slot.as_ref().is_some_and(|operation| operation.id == id))
            .ok_or(FileOperationError::Corrupt)?;
        let operation = self.operations[index]
            .take()
            .ok_or(FileOperationError::Corrupt)?;
        let original = operation.clone();
        let Ok(replacement) = transition(operation) else {
            self.operations[index] = Some(original);
            return Err(FileOperationError::InvalidTransition);
        };
        self.operations[index] = Some(replacement);
        Ok(())
    }

    /// Finds an operation by its opaque public identifier.
    #[must_use]
    pub fn get(&self, id: &str) -> Option<&Operation> {
        self.operations
            .iter()
            .flatten()
            .find(|operation| operation.id == id)
    }

    /// Returns the projection in deterministic insertion order.
    #[must_use]
    pub fn entries(&self) -> Vec<Operation> {
        self.operations.iter().flatten().cloned().collect()
    }
}

impl<const MAX_OPERATIONS: usize> Default for OperationRegistry<MAX_OPERATIONS> {
    fn default() -> Self {
        Self::new()
    }
}

/// Restartable bounded operation projection for the HTTP adapter.
pub struct FileOperationStore<const MAX_OPERATIONS: usize> {
    path: String,
    registry: OperationRegistry<MAX_OPERATIONS>,
}

impl<const MAX_OPERATIONS: usize> FileOperationStore<MAX_OPERATIONS> {
    fn read_bounded(path: &str) -> Result<Vec<u8>, FileOperationError> {
        const CHUNK_BYTES: usize = 8 * 1024;
        let mut file = match fs::File::open(path) {
            Ok(file) => file,
            Err(error) if error.kind() == io::ErrorKind::NotFound => {
                return Ok(Vec::from([]));
            }
            Err(error) => return Err(FileOperationError::Io(error)),
        };
        let mut data = Vec::from([]);
        let max_chunks = (MAX_OPERATION_FILE_BYTES / CHUNK_BYTES) + 1;
        for _ in 0..=max_chunks {
            let mut chunk = [0_u8; CHUNK_BYTES];
            let bytes_read = file.read(&mut chunk).map_err(FileOperationError::Io)?;
            if bytes_read == 0 {
                return Ok(data);
            }
            if data.len() + bytes_read > MAX_OPERATION_FILE_BYTES {
                return Err(FileOperationError::Capacity);
            }
            data.extend_from_slice(&chunk[..bytes_read]);
        }
        Err(FileOperationError::Capacity)
    }

    /// Opens an existing projection or creates an empty one.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn open(path: impl Into<String>) -> Result<Self, FileOperationError> {
        let path = path.into();
        let data = Self::read_bounded(&path)?;
        let entries: Vec<Operation> = if data.is_empty() {
            Vec::from([])
        } else {
            serde_json::from_slice(&data).map_err(|_| FileOperationError::Corrupt)?
        };
        let mut registry = OperationRegistry::new();
        for operation in entries {
            registry
                .insert(operation)
                .map_err(|_| FileOperationError::Corrupt)?;
        }
        Ok(Self { path, registry })
    }

    /// Reads one operation from the recovered projection.
    #[must_use]
    pub fn get(&self, id: &str) -> Option<&Operation> {
        self.registry.get(id)
    }

    /// Inserts and durably replaces the projection atomically at the file level.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn insert(&mut self, operation: Operation) -> Result<(), FileOperationError> {
        let mut candidate = self.registry.clone();
        candidate
            .insert(operation)
            .map_err(|_| FileOperationError::Capacity)?;
        let encoded =
            serde_json::to_vec(&candidate.entries()).map_err(|_| FileOperationError::Corrupt)?;
        if encoded.len() > MAX_OPERATION_FILE_BYTES {
            return Err(FileOperationError::Capacity);
        }
        let path = Path::new(&self.path);
        if let Some(parent) = path.parent() {
            if !parent.as_os_str().is_empty() {
                fs::create_dir_all(parent).map_err(FileOperationError::Io)?;
            }
        }
        let temporary = path.with_extension("tmp");
        fs::write(&temporary, encoded).map_err(FileOperationError::Io)?;
        fs::rename(&temporary, path).map_err(FileOperationError::Io)?;
        self.registry = candidate;
        Ok(())
    }

    /// Applies a transition and atomically rewrites the file projection.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation is missing, the transition is
    /// invalid, or the rewritten projection exceeds its bound.
    pub fn transition<F>(&mut self, id: &str, transition: F) -> Result<(), FileOperationError>
    where
        F: FnOnce(Operation) -> Result<Operation, OperationError>,
    {
        let mut candidate = self.registry.clone();
        candidate.transition(id, transition)?;
        let encoded =
            serde_json::to_vec(&candidate.entries()).map_err(|_| FileOperationError::Corrupt)?;
        if encoded.len() > MAX_OPERATION_FILE_BYTES {
            return Err(FileOperationError::Capacity);
        }
        let path = Path::new(&self.path);
        let temporary = path.with_extension("tmp");
        fs::write(&temporary, encoded).map_err(FileOperationError::Io)?;
        fs::rename(&temporary, path).map_err(FileOperationError::Io)?;
        self.registry = candidate;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn registry_is_bounded_and_rejects_duplicate_ids() {
        let mut registry = OperationRegistry::<1>::new();
        let operation = Operation::new("op-1", "build.enqueue", "req-1", "tenant-1", 1).unwrap();
        assert_eq!(registry.insert(operation.clone()), Ok(()));
        assert_eq!(registry.get("op-1"), Some(&operation));
        assert_eq!(
            registry.insert(operation),
            Err(OperationRegistryError::AlreadyExists)
        );
        let other = Operation::new("op-2", "build.enqueue", "req-2", "tenant-1", 2).unwrap();
        assert_eq!(
            registry.insert(other),
            Err(OperationRegistryError::Capacity)
        );
    }

    #[test]
    fn file_store_recovers_operations_and_rejects_corruption() {
        let path =
            std::env::temp_dir().join(format!("janus-operation-store-{}.json", std::process::id()));
        let path_text = path.to_string_lossy().into_owned();
        let operation = Operation::new("op-1", "project.create", "req-1", "tenant-1", 1)
            .unwrap()
            .complete_success("accepted", 2)
            .unwrap();
        {
            let mut store = FileOperationStore::<4>::open(&path_text).unwrap();
            store.insert(operation.clone()).unwrap();
        }
        let recovered = FileOperationStore::<4>::open(&path_text).unwrap();
        assert_eq!(recovered.get("op-1"), Some(&operation));
        fs::write(&path, b"not-json").unwrap();
        assert!(matches!(
            FileOperationStore::<4>::open(&path_text),
            Err(FileOperationError::Corrupt)
        ));
        let _ = fs::remove_file(path);
    }

    #[test]
    fn transitions_are_atomic_and_clear_opposite_payloads() {
        let mut backend = OperationBackend::<4>::default();
        let operation = Operation::new("op-1", "build.enqueue", "req-1", "tenant-1", 1).unwrap();
        backend.insert(operation).unwrap();
        backend
            .transition("op-1", |operation| {
                operation.complete_success("accepted", 2)
            })
            .unwrap();
        assert_eq!(
            backend
                .get("op-1")
                .and_then(|value| value.result.as_deref()),
            Some("accepted")
        );
        assert!(backend
            .transition("op-1", |operation| operation.complete_failure("late", 3))
            .is_err());
        assert_eq!(
            backend
                .get("op-1")
                .and_then(|value| value.result.as_deref()),
            Some("accepted")
        );
    }

    #[test]
    fn file_transition_rewrites_only_after_valid_lifecycle_change() {
        let path = std::env::temp_dir().join(format!(
            "janus-operation-transition-{}.json",
            std::process::id()
        ));
        let path_text = path.to_string_lossy().into_owned();
        {
            let mut store = FileOperationStore::<4>::open(&path_text).unwrap();
            store
                .insert(Operation::new("op-1", "build.enqueue", "req-1", "tenant-1", 1).unwrap())
                .unwrap();
            store
                .transition("op-1", |operation| {
                    operation.complete_failure("worker failed", 2)
                })
                .unwrap();
        }
        let reopened = FileOperationStore::<4>::open(&path_text).unwrap();
        let operation = reopened.get("op-1").unwrap();
        assert_eq!(operation.failure.as_deref(), Some("worker failed"));
        assert!(operation.result.is_none());
        let _ = fs::remove_file(path);
    }
}
