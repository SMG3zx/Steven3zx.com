//! Provider-neutral object storage with a bounded local implementation.

use std::fs::{create_dir_all, read, remove_file, write};
use std::io;
use std::path::{Path, PathBuf};

/// Maximum key length accepted by the object-store boundary.
pub const MAX_OBJECT_KEY_BYTES: usize = 512;

/// Object metadata returned by storage adapters.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ObjectMetadata {
    /// Validated object key.
    pub key: String,
    /// Stored byte count.
    pub size: usize,
}

/// Object-storage failure.
#[derive(Debug)]
pub enum ObjectStoreError {
    /// The key is empty, too long, absolute, or escapes the store root.
    InvalidKey,
    /// The object exceeds the configured per-object limit.
    ObjectTooLarge,
    /// The store has insufficient configured capacity.
    Capacity,
    /// The object was not found.
    NotFound,
    /// The underlying filesystem operation failed.
    Io(io::Error),
}

/// Provider-neutral object storage contract.
pub trait ObjectStore {
    /// Stores or replaces one object.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn put(&mut self, key: &str, data: &[u8]) -> Result<ObjectMetadata, ObjectStoreError>;
    /// Reads one object into bounded memory.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn get(&self, key: &str) -> Result<Vec<u8>, ObjectStoreError>;
    /// Deletes one object.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn delete(&mut self, key: &str) -> Result<(), ObjectStoreError>;
}

/// Bounded filesystem-backed object store for local operation and tests.
pub struct LocalObjectStore<const MAX_OBJECTS: usize, const MAX_BYTES: usize> {
    root: PathBuf,
    used_bytes: usize,
    object_count: usize,
}

impl<const MAX_OBJECTS: usize, const MAX_BYTES: usize> LocalObjectStore<MAX_OBJECTS, MAX_BYTES> {
    /// Creates a store rooted at the supplied directory.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn new(root: impl Into<PathBuf>) -> Result<Self, ObjectStoreError> {
        let root = root.into();
        create_dir_all(&root).map_err(ObjectStoreError::Io)?;
        Ok(Self {
            root,
            used_bytes: 0,
            object_count: 0,
        })
    }

    /// Returns the currently accounted byte usage.
    #[must_use]
    pub const fn used_bytes(&self) -> usize {
        self.used_bytes
    }

    fn object_path(&self, key: &str) -> Result<PathBuf, ObjectStoreError> {
        validate_key(key)?;
        Ok(self.root.join(key.replace('/', "_")))
    }
}

impl<const MAX_OBJECTS: usize, const MAX_BYTES: usize> ObjectStore
    for LocalObjectStore<MAX_OBJECTS, MAX_BYTES>
{
    fn put(&mut self, key: &str, data: &[u8]) -> Result<ObjectMetadata, ObjectStoreError> {
        if data.len() > MAX_BYTES {
            return Err(ObjectStoreError::ObjectTooLarge);
        }
        let path = self.object_path(key)?;
        let is_new = !path.exists();
        let old_size = match std::fs::metadata(&path) {
            Ok(metadata) => usize::try_from(metadata.len()).unwrap_or(usize::MAX),
            Err(error) if error.kind() == io::ErrorKind::NotFound => 0,
            Err(error) => return Err(ObjectStoreError::Io(error)),
        };
        let next_total = self
            .used_bytes
            .saturating_sub(old_size)
            .saturating_add(data.len());
        if next_total > MAX_BYTES || (is_new && self.object_count >= MAX_OBJECTS) {
            return Err(ObjectStoreError::Capacity);
        }
        write(&path, data).map_err(ObjectStoreError::Io)?;
        self.used_bytes = next_total;
        if is_new {
            self.object_count = self.object_count.saturating_add(1);
        }
        Ok(ObjectMetadata {
            key: key.to_owned(),
            size: data.len(),
        })
    }

    fn get(&self, key: &str) -> Result<Vec<u8>, ObjectStoreError> {
        let path = self.object_path(key)?;
        read(path).map_err(|error| match error.kind() {
            io::ErrorKind::NotFound => ObjectStoreError::NotFound,
            _ => ObjectStoreError::Io(error),
        })
    }

    fn delete(&mut self, key: &str) -> Result<(), ObjectStoreError> {
        let path = self.object_path(key)?;
        let size = std::fs::metadata(&path)
            .map_err(|error| match error.kind() {
                io::ErrorKind::NotFound => ObjectStoreError::NotFound,
                _ => ObjectStoreError::Io(error),
            })?
            .len();
        let size = usize::try_from(size).unwrap_or(usize::MAX);
        remove_file(path).map_err(ObjectStoreError::Io)?;
        self.used_bytes = self.used_bytes.saturating_sub(size);
        self.object_count = self.object_count.saturating_sub(1);
        Ok(())
    }
}

fn validate_key(key: &str) -> Result<(), ObjectStoreError> {
    let path = Path::new(key);
    if key.trim().is_empty()
        || key.len() > MAX_OBJECT_KEY_BYTES
        || path.is_absolute()
        || key.contains('\\')
        || key
            .split('/')
            .any(|part| part.is_empty() || part == "." || part == "..")
    {
        return Err(ObjectStoreError::InvalidKey);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn root(name: &str) -> PathBuf {
        std::env::temp_dir().join(format!("janus-object-store-{name}-{}", std::process::id()))
    }

    #[test]
    fn local_store_puts_gets_replaces_and_deletes_bounded_objects() {
        let path = root("lifecycle");
        let _ = std::fs::remove_dir_all(&path);
        let mut store = LocalObjectStore::<2, 8>::new(&path).unwrap();
        assert_eq!(store.put("artifacts/a", b"abc").unwrap().size, 3);
        assert_eq!(store.get("artifacts/a").unwrap(), b"abc");
        store.put("artifacts/a", b"abcd").unwrap();
        assert_eq!(store.used_bytes(), 4);
        store.delete("artifacts/a").unwrap();
        assert_eq!(store.used_bytes(), 0);
        let _ = std::fs::remove_dir_all(path);
    }

    #[test]
    fn local_store_rejects_traversal_and_capacity_overflow() {
        let path = root("bounds");
        let _ = std::fs::remove_dir_all(&path);
        let mut store = LocalObjectStore::<1, 4>::new(&path).unwrap();
        assert!(matches!(
            store.put("../escape", b"x"),
            Err(ObjectStoreError::InvalidKey)
        ));
        assert!(matches!(
            store.put("artifact", b"12345"),
            Err(ObjectStoreError::ObjectTooLarge)
        ));
        store.put("artifact", b"1234").unwrap();
        assert!(matches!(
            store.put("other", b"x"),
            Err(ObjectStoreError::Capacity)
        ));
        let _ = std::fs::remove_dir_all(path);
    }

    #[test]
    fn zero_byte_objects_consume_and_release_object_capacity() {
        let path = root("zero-byte");
        let _ = std::fs::remove_dir_all(&path);
        let mut store = LocalObjectStore::<1, 4>::new(&path).unwrap();
        store.put("empty", b"").unwrap();
        assert!(matches!(
            store.put("second", b"x"),
            Err(ObjectStoreError::Capacity)
        ));
        store.delete("empty").unwrap();
        store.put("second", b"x").unwrap();
        assert_eq!(store.used_bytes(), 1);
        let _ = std::fs::remove_dir_all(path);
    }
}
