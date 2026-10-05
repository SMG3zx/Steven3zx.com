//! Bounded event journal and snapshot envelope primitives.

use std::fs::{create_dir_all, read, read_to_string, OpenOptions};
use std::io::{self, Write};
use std::path::Path;

use crate::{Event, ProtocolVersion, CURRENT_PROTOCOL_VERSION};

/// One ordered, versioned domain event.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct EventEnvelope {
    /// Event schema version.
    pub version: ProtocolVersion,
    /// Strictly increasing journal sequence.
    pub sequence: u64,
    /// Domain transition.
    pub event: Event,
}

/// Journal append failure.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum JournalError {
    /// The journal reached configured capacity.
    Capacity,
    /// The supplied sequence would create a gap or duplicate.
    SequenceConflict,
    /// The event schema is not supported.
    UnsupportedVersion(ProtocolVersion),
}

/// Bounded in-memory journal used by simulation and as the persistence port.
pub struct EventJournal<const MAX_EVENTS: usize> {
    events: Vec<EventEnvelope>,
    next_sequence: u64,
}

impl<const MAX_EVENTS: usize> EventJournal<MAX_EVENTS> {
    /// Creates an empty journal.
    #[must_use]
    pub fn new() -> Self {
        Self {
            events: Vec::with_capacity(MAX_EVENTS),
            next_sequence: 0,
        }
    }

    /// Appends an event with the next sequence number.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn append(&mut self, event: Event) -> Result<EventEnvelope, JournalError> {
        if self.events.len() >= MAX_EVENTS {
            return Err(JournalError::Capacity);
        }
        let envelope = EventEnvelope {
            version: ProtocolVersion(CURRENT_PROTOCOL_VERSION),
            sequence: self.next_sequence,
            event,
        };
        self.next_sequence = self.next_sequence.saturating_add(1);
        self.events.push(envelope.clone());
        Ok(envelope)
    }

    /// Appends a previously persisted envelope during recovery.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn restore_append(&mut self, envelope: EventEnvelope) -> Result<(), JournalError> {
        if envelope.version.0 != CURRENT_PROTOCOL_VERSION {
            return Err(JournalError::UnsupportedVersion(envelope.version));
        }
        if envelope.sequence != self.next_sequence {
            return Err(JournalError::SequenceConflict);
        }
        if self.events.len() >= MAX_EVENTS {
            return Err(JournalError::Capacity);
        }
        self.next_sequence = self.next_sequence.saturating_add(1);
        self.events.push(envelope);
        Ok(())
    }

    /// Returns events in durable order.
    #[must_use]
    pub fn events(&self) -> &[EventEnvelope] {
        &self.events
    }

    /// Returns the next sequence that will be assigned.
    #[must_use]
    pub const fn next_sequence(&self) -> u64 {
        self.next_sequence
    }
}

impl<const MAX_EVENTS: usize> Default for EventJournal<MAX_EVENTS> {
    fn default() -> Self {
        Self::new()
    }
}

/// Failure while opening or appending the bounded file journal.
#[derive(Debug)]
pub enum FileJournalError {
    /// The underlying file operation failed.
    Io(io::Error),
    /// The journal reached its compile-time event capacity.
    Capacity,
    /// A persisted line was malformed or out of order.
    Corrupt,
}

/// Restartable append-only journal for the Rust local and production adapter.
pub struct FileEventJournal<const MAX_EVENTS: usize> {
    path: String,
    events: Vec<EventEnvelope>,
}

/// Maximum encoded snapshot size accepted by the local file adapter.
pub const MAX_SNAPSHOT_BYTES: usize = 8 * 1024 * 1024;

/// Failure while saving or loading a durable ECS snapshot.
#[derive(Debug)]
pub enum SnapshotStoreError {
    /// The underlying file operation failed.
    Io(io::Error),
    /// The snapshot header, fields, or enum discriminant was malformed.
    Corrupt,
    /// The snapshot schema is not supported by this binary.
    UnsupportedVersion(ProtocolVersion),
    /// The snapshot exceeded the bounded file adapter limits.
    Capacity,
}

/// Versioned file-backed snapshot store for crash recovery.
pub struct FileSnapshotStore {
    path: String,
}

impl FileSnapshotStore {
    /// Creates a snapshot store at the supplied path.
    pub fn new(path: impl Into<String>) -> Self {
        Self { path: path.into() }
    }

    /// Replaces the durable snapshot after validating its protocol version.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn save(&self, snapshot: &crate::WorldSnapshot) -> Result<(), SnapshotStoreError> {
        if snapshot.version.0 != CURRENT_PROTOCOL_VERSION {
            return Err(SnapshotStoreError::UnsupportedVersion(snapshot.version));
        }
        let mut encoder = Encoder::new(MAX_SNAPSHOT_BYTES);
        encoder.bytes(b"JNS-SNAPSHOT");
        encoder.u16(snapshot.version.0);
        encoder.u64(snapshot.tick.0);
        encode_optional_vec(&mut encoder, &snapshot.builds, encode_build);
        encode_optional_vec(&mut encoder, &snapshot.deployments, encode_deployment);
        encode_optional_vec(&mut encoder, &snapshot.operations, encode_operation);
        encode_optional_vec(&mut encoder, &snapshot.projects, encode_project);
        encoder.u32(u32::try_from(snapshot.seen_commands.len()).unwrap_or(u32::MAX));
        for command in &snapshot.seen_commands {
            encoder.u64(command.0);
        }
        encoder.u8(control_mode_tag(snapshot.control_mode));
        if encoder.overflowed {
            return Err(SnapshotStoreError::Capacity);
        }
        let path = Path::new(&self.path);
        if let Some(parent) = path.parent() {
            if !parent.as_os_str().is_empty() {
                create_dir_all(parent).map_err(SnapshotStoreError::Io)?;
            }
        }
        let mut file = OpenOptions::new()
            .create(true)
            .truncate(true)
            .write(true)
            .open(path)
            .map_err(SnapshotStoreError::Io)?;
        file.write_all(&encoder.data)
            .map_err(SnapshotStoreError::Io)?;
        file.sync_all().map_err(SnapshotStoreError::Io)
    }

    /// Loads and validates one durable ECS snapshot.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn load(&self) -> Result<Option<crate::WorldSnapshot>, SnapshotStoreError> {
        let data = match read(&self.path) {
            Ok(data) => data,
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
            Err(error) => return Err(SnapshotStoreError::Io(error)),
        };
        if data.len() > MAX_SNAPSHOT_BYTES {
            return Err(SnapshotStoreError::Capacity);
        }
        let mut decoder = Decoder::new(&data);
        if decoder.bytes(12).as_deref() != Some(b"JNS-SNAPSHOT") {
            return Err(SnapshotStoreError::Corrupt);
        }
        let version = ProtocolVersion(decoder.u16().ok_or(SnapshotStoreError::Corrupt)?);
        if version.0 != CURRENT_PROTOCOL_VERSION {
            return Err(SnapshotStoreError::UnsupportedVersion(version));
        }
        let snapshot = crate::WorldSnapshot {
            version,
            tick: crate::Tick(decoder.u64().ok_or(SnapshotStoreError::Corrupt)?),
            builds: decode_optional_vec(&mut decoder, decode_build)?,
            deployments: decode_optional_vec(&mut decoder, decode_deployment)?,
            operations: decode_optional_vec(&mut decoder, decode_operation)?,
            projects: decode_optional_vec(&mut decoder, decode_project)?,
            seen_commands: decode_commands(&mut decoder)?,
            control_mode: decode_control_mode(decoder.u8().ok_or(SnapshotStoreError::Corrupt)?)?,
        };
        if !decoder.finished() {
            return Err(SnapshotStoreError::Corrupt);
        }
        Ok(Some(snapshot))
    }
}

struct Encoder {
    data: Vec<u8>,
    limit: usize,
    overflowed: bool,
}

impl Encoder {
    fn new(limit: usize) -> Self {
        Self {
            data: Vec::from([]),
            limit,
            overflowed: false,
        }
    }

    fn bytes(&mut self, value: &[u8]) {
        let Some(end) = self.data.len().checked_add(value.len()) else {
            self.overflowed = true;
            return;
        };
        if end > self.limit {
            self.overflowed = true;
            return;
        }
        self.data.extend_from_slice(value);
    }

    fn u8(&mut self, value: u8) {
        self.data.push(value);
    }

    fn u16(&mut self, value: u16) {
        self.data.extend_from_slice(&value.to_le_bytes());
    }

    fn u32(&mut self, value: u32) {
        self.data.extend_from_slice(&value.to_le_bytes());
    }

    fn u64(&mut self, value: u64) {
        self.data.extend_from_slice(&value.to_le_bytes());
    }

    fn string(&mut self, value: &str) {
        self.u32(u32::try_from(value.len()).unwrap_or(u32::MAX));
        self.bytes(value.as_bytes());
    }

    fn optional_string(&mut self, value: Option<&String>) {
        match value {
            Some(value) => {
                self.u8(1);
                self.string(value);
            }
            None => self.u8(0),
        }
    }
}

struct Decoder<'a> {
    data: &'a [u8],
    offset: usize,
}

impl<'a> Decoder<'a> {
    const fn new(data: &'a [u8]) -> Self {
        Self { data, offset: 0 }
    }

    fn take(&mut self, count: usize) -> Option<&'a [u8]> {
        let end = self.offset.checked_add(count)?;
        let result = self.data.get(self.offset..end)?;
        self.offset = end;
        Some(result)
    }

    fn bytes(&mut self, count: usize) -> Option<Vec<u8>> {
        Some(self.take(count)?.to_vec())
    }

    fn u8(&mut self) -> Option<u8> {
        Some(*self.take(1)?.first()?)
    }

    fn u16(&mut self) -> Option<u16> {
        Some(u16::from_le_bytes(self.take(2)?.try_into().ok()?))
    }

    fn u32(&mut self) -> Option<u32> {
        Some(u32::from_le_bytes(self.take(4)?.try_into().ok()?))
    }

    fn u64(&mut self) -> Option<u64> {
        Some(u64::from_le_bytes(self.take(8)?.try_into().ok()?))
    }

    fn string(&mut self) -> Option<String> {
        let length = usize::try_from(self.u32()?).ok()?;
        String::from_utf8(self.take(length)?.to_vec()).ok()
    }

    fn optional_string(&mut self) -> Result<Option<String>, ()> {
        match self.u8() {
            Some(0) => Ok(None),
            Some(1) => self.string().map(Some).ok_or(()),
            Some(_) | None => Err(()),
        }
    }

    const fn finished(&self) -> bool {
        self.offset == self.data.len()
    }
}

fn encode_optional_vec<T>(
    encoder: &mut Encoder,
    values: &[Option<T>],
    encode: fn(&mut Encoder, &T),
) {
    encoder.u32(u32::try_from(values.len()).unwrap_or(u32::MAX));
    for value in values {
        match value {
            Some(value) => {
                encoder.u8(1);
                encode(encoder, value);
            }
            None => encoder.u8(0),
        }
    }
}

fn decode_optional_vec<T>(
    decoder: &mut Decoder<'_>,
    decode: fn(&mut Decoder<'_>) -> Option<T>,
) -> Result<Vec<Option<T>>, SnapshotStoreError> {
    let length = usize::try_from(decoder.u32().ok_or(SnapshotStoreError::Corrupt)?)
        .map_err(|_| SnapshotStoreError::Corrupt)?;
    if length > MAX_SNAPSHOT_BYTES {
        return Err(SnapshotStoreError::Capacity);
    }
    let mut values = Vec::with_capacity(length.min(MAX_SNAPSHOT_BYTES));
    for _ in 0..length {
        values.push(match decoder.u8().ok_or(SnapshotStoreError::Corrupt)? {
            0 => None,
            1 => Some(decode(decoder).ok_or(SnapshotStoreError::Corrupt)?),
            _ => return Err(SnapshotStoreError::Corrupt),
        });
    }
    Ok(values)
}

fn encode_build(encoder: &mut Encoder, build: &crate::Build) {
    encoder.u32(build.tenant.0);
    encoder.u8(build_state_tag(build.state));
    encoder.u64(build.generation.0);
    match build.project {
        Some(project) => {
            encoder.u8(1);
            encoder.u64(project.0);
        }
        None => encoder.u8(0),
    }
    encoder.optional_string(build.source_ref.as_ref());
}

fn decode_build(decoder: &mut Decoder<'_>) -> Option<crate::Build> {
    Some(crate::Build {
        tenant: crate::TenantId(decoder.u32()?),
        state: decode_build_state(decoder.u8()?)?,
        generation: crate::Generation(decoder.u64()?),
        project: match decoder.u8()? {
            0 => None,
            1 => Some(crate::EntityId(decoder.u64()?)),
            _ => return None,
        },
        source_ref: decoder.optional_string().ok()?,
    })
}

fn encode_deployment(encoder: &mut Encoder, deployment: &crate::Deployment) {
    encoder.u32(deployment.tenant.0);
    encoder.u64(deployment.build.0);
    match deployment.project {
        Some(project) => {
            encoder.u8(1);
            encoder.u64(project.0);
        }
        None => encoder.u8(0),
    }
    encoder.u64(deployment.revision);
    encoder.string(&deployment.target_type);
    encoder.string(&deployment.target_ref);
    encoder.string(&deployment.preferred_runner);
    encoder.u32(u32::try_from(deployment.environment.len()).unwrap_or(u32::MAX));
    for (key, value) in &deployment.environment {
        encoder.string(key);
        encoder.string(value);
    }
    encoder.optional_string(Some(&deployment.runtime_id));
    encoder.optional_string(Some(&deployment.runtime_mode));
    encoder.optional_string(Some(&deployment.runtime_endpoint));
    encoder.optional_string(Some(&deployment.runtime_status));
    encoder.u8(deployment_state_tag(deployment.state));
    encoder.u64(deployment.generation.0);
    match deployment.resource_claim {
        Some(claim) => {
            encoder.u8(1);
            encode_claim(encoder, claim);
        }
        None => encoder.u8(0),
    }
}

fn decode_deployment(decoder: &mut Decoder<'_>) -> Option<crate::Deployment> {
    Some(crate::Deployment {
        tenant: crate::TenantId(decoder.u32()?),
        build: crate::EntityId(decoder.u64()?),
        project: match decoder.u8()? {
            0 => None,
            1 => Some(crate::EntityId(decoder.u64()?)),
            _ => return None,
        },
        revision: decoder.u64()?,
        target_type: decoder.string()?,
        target_ref: decoder.string()?,
        preferred_runner: decoder.string()?,
        environment: {
            let length = usize::try_from(decoder.u32()?).ok()?;
            if length > crate::MAX_DEPLOYMENT_ENV_ENTRIES {
                return None;
            }
            let mut entries = Vec::with_capacity(length.min(crate::MAX_DEPLOYMENT_ENV_ENTRIES));
            for _ in 0..length {
                entries.push((decoder.string()?, decoder.string()?));
            }
            entries
        },
        runtime_id: decoder.optional_string().ok()?.unwrap_or_default(),
        runtime_mode: decoder.optional_string().ok()?.unwrap_or_default(),
        runtime_endpoint: decoder.optional_string().ok()?.unwrap_or_default(),
        runtime_status: decoder.optional_string().ok()?.unwrap_or_default(),
        state: decode_deployment_state(decoder.u8()?)?,
        generation: crate::Generation(decoder.u64()?),
        resource_claim: match decoder.u8()? {
            0 => None,
            1 => Some(decode_claim(decoder)?),
            _ => return None,
        },
    })
}

fn encode_claim(encoder: &mut Encoder, claim: crate::ResourceClaim) {
    encoder.u64(claim.resource.0);
    encoder.u32(claim.tenant.0);
    encoder.u64(claim.owner.0);
    encoder.u64(claim.generation.0);
    encoder.u8(resource_kind_tag(claim.kind));
}

fn decode_claim(decoder: &mut Decoder<'_>) -> Option<crate::ResourceClaim> {
    Some(crate::ResourceClaim {
        resource: crate::EntityId(decoder.u64()?),
        tenant: crate::TenantId(decoder.u32()?),
        owner: crate::EntityId(decoder.u64()?),
        generation: crate::Generation(decoder.u64()?),
        kind: decode_resource_kind(decoder.u8()?)?,
    })
}

fn encode_operation(encoder: &mut Encoder, operation: &crate::Operation) {
    encoder.string(&operation.id);
    encoder.string(&operation.kind);
    encoder.string(&operation.correlation_id);
    encoder.string(&operation.tenant_id);
    encoder.u8(operation_status_tag(operation.status));
    encoder.u64(operation.created_at);
    encoder.u64(operation.updated_at);
    encoder.optional_string(operation.result.as_ref());
    encoder.optional_string(operation.failure.as_ref());
}

fn decode_operation(decoder: &mut Decoder<'_>) -> Option<crate::Operation> {
    Some(crate::Operation {
        id: decoder.string()?,
        kind: decoder.string()?,
        correlation_id: decoder.string()?,
        tenant_id: decoder.string()?,
        status: decode_operation_status(decoder.u8()?)?,
        created_at: decoder.u64()?,
        updated_at: decoder.u64()?,
        result: match decoder.u8()? {
            0 => None,
            1 => decoder.string(),
            _ => return None,
        },
        failure: match decoder.u8()? {
            0 => None,
            1 => decoder.string(),
            _ => return None,
        },
    })
}

fn encode_project(encoder: &mut Encoder, project: &crate::Project) {
    encoder.u64(project.id.0);
    encoder.u32(project.tenant.0);
    for value in [
        &project.name,
        &project.slug,
        &project.description,
        &project.status,
        &project.repo_provider,
        &project.repo_url,
        &project.repo_branch,
    ] {
        encoder.string(value);
    }
    match &project.repo_check {
        Some(result) => {
            encoder.u8(1);
            encoder.u8(u8::from(result.ok));
            for value in [
                &result.status,
                &result.message,
                &result.repo_url,
                &result.branch,
            ] {
                encoder.string(value);
            }
            encoder.u32(u32::try_from(result.stages.len()).unwrap_or(u32::MAX));
            for stage in &result.stages {
                encoder.string(&stage.name);
                encoder.string(&stage.status);
                encoder.string(&stage.message);
            }
            encoder.u64(result.checked_at);
        }
        None => encoder.u8(0),
    }
    encoder.u64(project.created_at);
    encoder.u64(project.updated_at);
}

fn decode_project(decoder: &mut Decoder<'_>) -> Option<crate::Project> {
    let id = crate::EntityId(decoder.u64()?);
    let tenant = crate::TenantId(decoder.u32()?);
    let name = decoder.string()?;
    let slug = decoder.string()?;
    let description = decoder.string()?;
    let status = decoder.string()?;
    let repo_provider = decoder.string()?;
    let repo_url = decoder.string()?;
    let repo_branch = decoder.string()?;
    let repo_check = match decoder.u8()? {
        0 => None,
        1 => {
            let ok = decoder.u8()? == 1;
            let status = decoder.string()?;
            let message = decoder.string()?;
            let repo_url = decoder.string()?;
            let branch = decoder.string()?;
            let length = usize::try_from(decoder.u32()?).ok()?;
            if length > crate::MAX_REPO_CHECK_STAGES {
                return None;
            }
            let mut stages = Vec::with_capacity(length.min(crate::MAX_REPO_CHECK_STAGES));
            for _ in 0..length {
                stages.push(crate::RepoCheckStage {
                    name: decoder.string()?,
                    status: decoder.string()?,
                    message: decoder.string()?,
                });
            }
            Some(crate::RepoCheckResult {
                ok,
                status,
                message,
                repo_url,
                branch,
                stages,
                checked_at: decoder.u64()?,
            })
        }
        _ => return None,
    };
    Some(crate::Project {
        id,
        tenant,
        name,
        slug,
        description,
        status,
        repo_provider,
        repo_url,
        repo_branch,
        repo_check,
        created_at: decoder.u64()?,
        updated_at: decoder.u64()?,
    })
}

fn decode_commands(decoder: &mut Decoder<'_>) -> Result<Vec<crate::CommandId>, SnapshotStoreError> {
    let length = usize::try_from(decoder.u32().ok_or(SnapshotStoreError::Corrupt)?)
        .map_err(|_| SnapshotStoreError::Corrupt)?;
    if length > MAX_SNAPSHOT_BYTES / 8 {
        return Err(SnapshotStoreError::Capacity);
    }
    (0..length)
        .map(|_| {
            decoder
                .u64()
                .map(crate::CommandId)
                .ok_or(SnapshotStoreError::Corrupt)
        })
        .collect()
}

const fn control_mode_tag(value: crate::ControlMode) -> u8 {
    match value {
        crate::ControlMode::Running => 0,
        crate::ControlMode::Paused => 1,
        crate::ControlMode::Draining => 2,
    }
}

const fn decode_control_mode(value: u8) -> Result<crate::ControlMode, SnapshotStoreError> {
    match value {
        0 => Ok(crate::ControlMode::Running),
        1 => Ok(crate::ControlMode::Paused),
        2 => Ok(crate::ControlMode::Draining),
        _ => Err(SnapshotStoreError::Corrupt),
    }
}

const fn build_state_tag(value: crate::BuildState) -> u8 {
    match value {
        crate::BuildState::Pending => 0,
        crate::BuildState::Running => 1,
        crate::BuildState::Succeeded => 2,
        crate::BuildState::Failed => 3,
        crate::BuildState::Cancelled => 4,
    }
}

const fn decode_build_state(value: u8) -> Option<crate::BuildState> {
    Some(match value {
        0 => crate::BuildState::Pending,
        1 => crate::BuildState::Running,
        2 => crate::BuildState::Succeeded,
        3 => crate::BuildState::Failed,
        4 => crate::BuildState::Cancelled,
        _ => return None,
    })
}

const fn deployment_state_tag(value: crate::DeploymentState) -> u8 {
    match value {
        crate::DeploymentState::Pending => 0,
        crate::DeploymentState::Starting => 1,
        crate::DeploymentState::Running => 2,
        crate::DeploymentState::Failed => 3,
        crate::DeploymentState::Stopped => 4,
    }
}

const fn decode_deployment_state(value: u8) -> Option<crate::DeploymentState> {
    Some(match value {
        0 => crate::DeploymentState::Pending,
        1 => crate::DeploymentState::Starting,
        2 => crate::DeploymentState::Running,
        3 => crate::DeploymentState::Failed,
        4 => crate::DeploymentState::Stopped,
        _ => return None,
    })
}

const fn resource_kind_tag(value: crate::ResourceKind) -> u8 {
    match value {
        crate::ResourceKind::Process => 0,
        crate::ResourceKind::Socket => 1,
        crate::ResourceKind::Artifact => 2,
        crate::ResourceKind::Lease => 3,
    }
}

const fn decode_resource_kind(value: u8) -> Option<crate::ResourceKind> {
    Some(match value {
        0 => crate::ResourceKind::Process,
        1 => crate::ResourceKind::Socket,
        2 => crate::ResourceKind::Artifact,
        3 => crate::ResourceKind::Lease,
        _ => return None,
    })
}

const fn operation_status_tag(value: crate::OperationStatus) -> u8 {
    match value {
        crate::OperationStatus::Pending => 0,
        crate::OperationStatus::Processing => 1,
        crate::OperationStatus::Succeeded => 2,
        crate::OperationStatus::Failed => 3,
        crate::OperationStatus::DeadLettered => 4,
        crate::OperationStatus::Expired => 5,
    }
}

const fn decode_operation_status(value: u8) -> Option<crate::OperationStatus> {
    Some(match value {
        0 => crate::OperationStatus::Pending,
        1 => crate::OperationStatus::Processing,
        2 => crate::OperationStatus::Succeeded,
        3 => crate::OperationStatus::Failed,
        4 => crate::OperationStatus::DeadLettered,
        5 => crate::OperationStatus::Expired,
        _ => return None,
    })
}

impl<const MAX_EVENTS: usize> FileEventJournal<MAX_EVENTS> {
    /// Opens an existing journal or creates an empty one.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn open(path: impl Into<String>) -> Result<Self, FileJournalError> {
        let path = path.into();
        let contents = match read_to_string(&path) {
            Ok(contents) => contents,
            Err(error) if error.kind() == io::ErrorKind::NotFound => String::default(),
            Err(error) => return Err(FileJournalError::Io(error)),
        };
        let mut events = Vec::with_capacity(MAX_EVENTS);
        for (sequence, line) in contents.lines().enumerate() {
            let envelope = decode_line(line).ok_or(FileJournalError::Corrupt)?;
            if envelope.sequence != u64::try_from(sequence).unwrap_or(u64::MAX)
                || events.len() >= MAX_EVENTS
            {
                return Err(FileJournalError::Corrupt);
            }
            events.push(envelope);
        }
        Ok(Self { path, events })
    }

    /// Appends one event after validating capacity and durable ordering.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn append(&mut self, event: Event) -> Result<EventEnvelope, FileJournalError> {
        if self.events.len() >= MAX_EVENTS {
            return Err(FileJournalError::Capacity);
        }
        let envelope = EventEnvelope {
            version: ProtocolVersion(CURRENT_PROTOCOL_VERSION),
            sequence: u64::try_from(self.events.len()).unwrap_or(u64::MAX),
            event,
        };
        let path = Path::new(&self.path);
        if let Some(parent) = path.parent() {
            if !parent.as_os_str().is_empty() {
                create_dir_all(parent).map_err(FileJournalError::Io)?;
            }
        }
        let mut file = OpenOptions::new()
            .create(true)
            .append(true)
            .open(path)
            .map_err(FileJournalError::Io)?;
        let line = encode_line(&envelope);
        file.write_all(line.as_bytes())
            .map_err(FileJournalError::Io)?;
        file.write_all(b"\n").map_err(FileJournalError::Io)?;
        self.events.push(envelope.clone());
        Ok(envelope)
    }

    /// Returns the recovered events in strict durable order.
    #[must_use]
    pub fn events(&self) -> &[EventEnvelope] {
        &self.events
    }
}

/// Encodes one journal envelope as the bounded line format used by the file journal.
#[must_use]
pub fn encode_line(envelope: &EventEnvelope) -> String {
    let (tag, fields, field_count) = encode_event_fields(&envelope.event);
    format!(
        "{}|{}|{}|{}",
        envelope.version.0,
        envelope.sequence,
        tag,
        fields[..field_count].join("|")
    )
}

type EncodedEventFields = (&'static str, [String; 3], usize);

struct EventFieldBuilder {
    fields: [String; 3],
    count: usize,
}

impl EventFieldBuilder {
    fn new() -> Self {
        Self {
            fields: [String::default(), String::default(), String::default()],
            count: 0,
        }
    }

    fn add(&mut self, value: String) {
        self.fields[self.count] = value;
        self.count += 1;
    }

    fn finish(self, tag: &'static str) -> EncodedEventFields {
        (tag, self.fields, self.count)
    }
}

fn encode_event_fields(event: &Event) -> EncodedEventFields {
    if let Some(encoded) = encode_project_event(event) {
        return encoded;
    }
    if let Some(encoded) = encode_build_event(event) {
        return encoded;
    }
    if let Some(encoded) = encode_deployment_event(event) {
        return encoded;
    }
    encode_control_event(event)
}

fn encode_project_event(event: &Event) -> Option<EncodedEventFields> {
    let mut fields = EventFieldBuilder::new();
    let tag = match event {
        Event::ProjectCreated { project, tenant } => {
            fields.add(project.0.to_string());
            fields.add(tenant.0.to_string());
            "ProjectCreated"
        }
        Event::ProjectRepositoryUpdated { project } => {
            fields.add(project.0.to_string());
            "ProjectRepositoryUpdated"
        }
        Event::ProjectDeleted { project } => {
            fields.add(project.0.to_string());
            "ProjectDeleted"
        }
        _ => return None,
    };
    Some(fields.finish(tag))
}

fn encode_build_event(event: &Event) -> Option<EncodedEventFields> {
    let mut fields = EventFieldBuilder::new();
    let tag = match event {
        Event::BuildAccepted { build, tenant } => {
            fields.add(build.0.to_string());
            fields.add(tenant.0.to_string());
            "BuildAccepted"
        }
        Event::BuildStarted { build, generation } => {
            fields.add(build.0.to_string());
            fields.add(generation.0.to_string());
            "BuildStarted"
        }
        Event::BuildSucceeded { build, generation } => {
            fields.add(build.0.to_string());
            fields.add(generation.0.to_string());
            "BuildSucceeded"
        }
        Event::BuildFailed {
            build,
            generation,
            exit_code,
        } => {
            fields.add(build.0.to_string());
            fields.add(generation.0.to_string());
            fields.add(exit_code.to_string());
            "BuildFailed"
        }
        Event::BuildCancelled { build } => {
            fields.add(build.0.to_string());
            "BuildCancelled"
        }
        _ => return None,
    };
    Some(fields.finish(tag))
}

fn encode_deployment_event(event: &Event) -> Option<EncodedEventFields> {
    let mut fields = EventFieldBuilder::new();
    let tag = match event {
        Event::DeploymentAccepted {
            deployment,
            tenant,
            build,
        } => {
            fields.add(deployment.0.to_string());
            fields.add(tenant.0.to_string());
            fields.add(build.0.to_string());
            "DeploymentAccepted"
        }
        Event::DeploymentStarting {
            deployment,
            generation,
        } => {
            fields.add(deployment.0.to_string());
            fields.add(generation.0.to_string());
            "DeploymentStarting"
        }
        Event::DeploymentRunning {
            deployment,
            generation,
        } => {
            fields.add(deployment.0.to_string());
            fields.add(generation.0.to_string());
            "DeploymentRunning"
        }
        Event::DeploymentFailed {
            deployment,
            generation,
        } => {
            fields.add(deployment.0.to_string());
            fields.add(generation.0.to_string());
            "DeploymentFailed"
        }
        Event::DeploymentStopped { deployment } => {
            fields.add(deployment.0.to_string());
            "DeploymentStopped"
        }
        _ => return None,
    };
    Some(fields.finish(tag))
}

fn encode_control_event(event: &Event) -> EncodedEventFields {
    let mut fields = EventFieldBuilder::new();
    let tag = match event {
        Event::ControlPaused => "ControlPaused",
        Event::ControlResumed => "ControlResumed",
        Event::ControlDraining => "ControlDraining",
        Event::CommandRejected { command_id, reason } => {
            fields.add(command_id.0.to_string());
            fields.add(reject_reason_name(*reason).to_owned());
            "CommandRejected"
        }
        _ => unreachable!("event family was pre-dispatched"), // tigerstyle: invariant-checked
    };
    fields.finish(tag)
}

/// Decodes one journal line, returning `None` when its version, fields, or event tag is invalid.
#[must_use]
pub fn decode_line(line: &str) -> Option<EventEnvelope> {
    let mut parts = line.split('|');
    let version = parts.next()?.parse().ok()?;
    let sequence = parts.next()?.parse().ok()?;
    let tag = parts.next()?;
    let mut fields = Vec::from([]);
    for field in parts {
        if fields.len() >= 3 {
            return None;
        }
        fields.push(field);
    }
    let event = decode_event(tag, &fields)?;
    Some(EventEnvelope {
        version: ProtocolVersion(version),
        sequence,
        event,
    })
}

fn decode_event(tag: &str, fields: &[&str]) -> Option<Event> {
    let number = |index: usize| fields.get(index)?.parse().ok();
    let number32 = |index: usize| fields.get(index)?.parse().ok();
    let entity = |index: usize| Some(crate::EntityId(number(index)?));
    let generation = |index: usize| Some(crate::Generation(number(index)?));
    let tenant = |index: usize| Some(crate::TenantId(number32(index)?));
    let event = match tag {
        "ProjectCreated" => Event::ProjectCreated {
            project: entity(0)?,
            tenant: tenant(1)?,
        },
        "ProjectRepositoryUpdated" => Event::ProjectRepositoryUpdated {
            project: entity(0)?,
        },
        "ProjectDeleted" => Event::ProjectDeleted {
            project: entity(0)?,
        },
        "BuildAccepted" => Event::BuildAccepted {
            build: entity(0)?,
            tenant: tenant(1)?,
        },
        "BuildStarted" => Event::BuildStarted {
            build: entity(0)?,
            generation: generation(1)?,
        },
        "BuildSucceeded" => Event::BuildSucceeded {
            build: entity(0)?,
            generation: generation(1)?,
        },
        "BuildFailed" => Event::BuildFailed {
            build: entity(0)?,
            generation: generation(1)?,
            exit_code: number32(2)?,
        },
        "BuildCancelled" => Event::BuildCancelled { build: entity(0)? },
        "DeploymentAccepted" => Event::DeploymentAccepted {
            deployment: entity(0)?,
            tenant: tenant(1)?,
            build: entity(2)?,
        },
        "DeploymentStarting" => Event::DeploymentStarting {
            deployment: entity(0)?,
            generation: generation(1)?,
        },
        "DeploymentRunning" => Event::DeploymentRunning {
            deployment: entity(0)?,
            generation: generation(1)?,
        },
        "DeploymentFailed" => Event::DeploymentFailed {
            deployment: entity(0)?,
            generation: generation(1)?,
        },
        "DeploymentStopped" => Event::DeploymentStopped {
            deployment: entity(0)?,
        },
        "ControlPaused" => Event::ControlPaused,
        "ControlResumed" => Event::ControlResumed,
        "ControlDraining" => Event::ControlDraining,
        "CommandRejected" => Event::CommandRejected {
            command_id: crate::CommandId(number(0)?),
            reason: parse_reject_reason(fields.get(1)?)?,
        },
        _ => return None,
    };
    Some(event)
}

const fn reject_reason_name(reason: crate::RejectReason) -> &'static str {
    match reason {
        crate::RejectReason::NotFound => "NotFound",
        crate::RejectReason::TenantBoundary => "TenantBoundary",
        crate::RejectReason::InvalidTransition => "InvalidTransition",
        crate::RejectReason::StaleGeneration => "StaleGeneration",
        crate::RejectReason::Capacity => "Capacity",
        crate::RejectReason::BuildNotSucceeded => "BuildNotSucceeded",
        crate::RejectReason::Paused => "Paused",
        crate::RejectReason::Draining => "Draining",
    }
}

fn parse_reject_reason(value: &str) -> Option<crate::RejectReason> {
    Some(match value {
        "NotFound" => crate::RejectReason::NotFound,
        "TenantBoundary" => crate::RejectReason::TenantBoundary,
        "InvalidTransition" => crate::RejectReason::InvalidTransition,
        "StaleGeneration" => crate::RejectReason::StaleGeneration,
        "Capacity" => crate::RejectReason::Capacity,
        "BuildNotSucceeded" => crate::RejectReason::BuildNotSucceeded,
        "Paused" => crate::RejectReason::Paused,
        "Draining" => crate::RejectReason::Draining,
        _ => return None,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn journal_is_ordered_and_versioned() {
        let mut journal = EventJournal::<2>::new();
        let first = journal.append(Event::ControlPaused).unwrap();
        assert_eq!(first.sequence, 0);
        assert_eq!(journal.next_sequence(), 1);
        assert_eq!(
            journal.restore_append(EventEnvelope {
                version: ProtocolVersion(CURRENT_PROTOCOL_VERSION),
                sequence: 9,
                event: Event::ControlResumed
            }),
            Err(JournalError::SequenceConflict)
        );
        journal.append(Event::ControlResumed).unwrap();
        assert_eq!(
            journal.append(Event::ControlDraining),
            Err(JournalError::Capacity)
        );
    }

    #[test]
    fn file_journal_recovers_ordered_events_after_restart() {
        let path = std::env::temp_dir().join(format!("janus-journal-{}.log", std::process::id()));
        let _ = std::fs::remove_file(&path);
        let path_text = path.to_string_lossy().into_owned();
        {
            let mut journal = FileEventJournal::<4>::open(&path_text).unwrap();
            journal
                .append(Event::BuildAccepted {
                    build: crate::EntityId(3),
                    tenant: crate::TenantId(9),
                })
                .unwrap();
            journal.append(Event::ControlPaused).unwrap();
        }
        let journal = FileEventJournal::<4>::open(&path_text).unwrap();
        assert_eq!(journal.events().len(), 2);
        assert_eq!(journal.events()[1].event, Event::ControlPaused);
        let _ = std::fs::remove_file(path);
    }

    #[test]
    fn file_journal_rejects_corrupt_or_over_capacity_state() {
        let path =
            std::env::temp_dir().join(format!("janus-journal-corrupt-{}.log", std::process::id()));
        let path_text = path.to_string_lossy().into_owned();
        std::fs::write(&path, "1|4|ControlPaused|\n").unwrap();
        assert!(matches!(
            FileEventJournal::<4>::open(&path_text),
            Err(FileJournalError::Corrupt)
        ));
        let _ = std::fs::remove_file(path);
    }

    #[test]
    fn journal_decoder_rejects_more_than_three_event_fields() {
        assert!(decode_line("5|4|BuildFailed|3|7|1|unexpected").is_none());
    }

    #[test]
    fn file_snapshot_round_trips_ecs_state_and_rejects_trailing_bytes() {
        let path = std::env::temp_dir().join(format!("janus-snapshot-{}.bin", std::process::id()));
        let _ = std::fs::remove_file(&path);
        let mut world = crate::World::<4, 4>::new();
        world
            .insert_operation(
                crate::EntityId(1),
                crate::Operation::new("op-1", "project.create", "request-1", "tenant-1", 7)
                    .unwrap(),
            )
            .unwrap();
        world
            .insert_project(crate::Project {
                id: crate::EntityId(2),
                tenant: crate::TenantId(7),
                name: "Janus".to_owned(),
                slug: "janus".to_owned(),
                description: "control plane".to_owned(),
                status: "active".to_owned(),
                repo_provider: "github".to_owned(),
                repo_url: "https://example.test/janus".to_owned(),
                repo_branch: "main".to_owned(),
                repo_check: None,
                created_at: 7,
                updated_at: 7,
            })
            .unwrap();
        let snapshot = world.snapshot();
        let store = FileSnapshotStore::new(path.to_string_lossy().into_owned());
        store.save(&snapshot).unwrap();
        assert_eq!(store.load().unwrap(), Some(snapshot));
        std::fs::OpenOptions::new()
            .append(true)
            .open(&path)
            .unwrap()
            .write_all(b"x")
            .unwrap();
        assert!(matches!(store.load(), Err(SnapshotStoreError::Corrupt)));
        let _ = std::fs::remove_file(path);
    }
}
