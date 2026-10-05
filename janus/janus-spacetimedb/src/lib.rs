//! Durable Janus tables and reducer entry points for SpacetimeDB 2.
//!
//! Authentication and tenant claims are validated by the Rust HTTP/client
//! boundary before these reducers are called. Reducers still validate row
//! shape, idempotency, state transitions, and bounded payloads because the
//! database is the authoritative mutation boundary.

use spacetimedb::{reducer, table, ReducerContext, SpacetimeType, Table};

const MAX_KIND_BYTES: usize = 128;
const MAX_PAYLOAD_BYTES: usize = 64 * 1024;
const MAX_TELEMETRY_SAMPLES: usize = 4096;
const MAX_JOB_ROWS: usize = 4096;
const MAX_RUNNER_CAPABILITY_BYTES: usize = 64;

fn build_transition_allowed(from: &str, to: &str) -> bool {
    matches!(
        (from, to),
        ("pending", "running")
            | ("pending", "cancelled")
            | ("running", "succeeded")
            | ("running", "failed")
            | ("running", "cancelled")
    )
}

fn deployment_transition_allowed(from: &str, to: &str) -> bool {
    matches!(
        (from, to),
        ("pending", "starting")
            | ("pending", "stopped")
            | ("starting", "running")
            | ("starting", "failed")
            | ("running", "failed")
            | ("running", "stopped")
    )
}

fn operation_transition_allowed(from: &str, to: &str) -> bool {
    matches!(
        (from, to),
        ("pending", "processing")
            | ("pending", "succeeded")
            | ("pending", "failed")
            | ("pending", "dead_lettered")
            | ("pending", "expired")
            | ("processing", "processing")
            | ("processing", "succeeded")
            | ("processing", "failed")
            | ("processing", "dead_lettered")
            | ("processing", "expired")
    )
}

fn valid_project_fields(repository: &str, branch: &str, status: &str) -> bool {
    !repository.trim().is_empty()
        && !branch.trim().is_empty()
        && !status.trim().is_empty()
        && repository.len() <= MAX_KIND_BYTES * 8
        && branch.len() <= MAX_KIND_BYTES
        && status.len() <= MAX_KIND_BYTES
}

fn valid_deployment_metadata(metadata: &DeploymentMutation) -> bool {
    (metadata.target_type.is_empty()
        || matches!(metadata.target_type.as_str(), "preview" | "production"))
        && metadata.target_type.len() <= MAX_KIND_BYTES
        && metadata.target_ref.len() <= MAX_PAYLOAD_BYTES
        && metadata.preferred_runner.len() <= MAX_PAYLOAD_BYTES
        && metadata.environment.len() <= MAX_PAYLOAD_BYTES
        && metadata.runtime_id.len() <= MAX_PAYLOAD_BYTES
        && metadata.runtime_mode.len() <= MAX_KIND_BYTES
        && metadata.runtime_endpoint.len() <= MAX_PAYLOAD_BYTES
        && metadata.runtime_status.len() <= MAX_KIND_BYTES
}

fn valid_deployment_runtime(runtime: &DeploymentRuntimeMutation) -> bool {
    runtime.runtime_id.len() <= MAX_PAYLOAD_BYTES
        && runtime.runtime_mode.len() <= MAX_KIND_BYTES
        && runtime.runtime_endpoint.len() <= MAX_PAYLOAD_BYTES
        && runtime.runtime_status.len() <= MAX_KIND_BYTES
}

fn valid_text(value: &str, limit: usize, required: bool) -> bool {
    (!required || !value.trim().is_empty()) && value.len() <= limit
}

fn valid_tenant_timestamp(tenant_id: u32, timestamp: u64) -> bool {
    tenant_id != 0 && timestamp != 0
}

fn valid_email(email: &str) -> bool {
    valid_text(email, MAX_KIND_BYTES * 2, true) && email.contains('@')
}

/// Complete project mutation payload shared by create and update reducers.
#[derive(SpacetimeType)]
pub struct ProjectMutation {
    /// Human-readable project name.
    pub name: String,
    /// Stable tenant-local project slug.
    pub slug: String,
    /// Human-readable project description.
    pub description: String,
    /// Repository provider identifier.
    pub repo_provider: String,
    /// Repository URL.
    pub repository: String,
    /// Repository branch.
    pub branch: String,
    /// Creation timestamp for a new row.
    pub created_at: u64,
    /// Last update timestamp.
    pub updated_at: u64,
}

/// Durable project state.
#[table(accessor = project, public)]
pub struct Project {
    /// Stable project identity.
    #[primary_key]
    pub id: u64,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Human-readable project name.
    pub name: String,
    /// Stable tenant-local project slug.
    pub slug: String,
    /// Human-readable project description.
    pub description: String,
    /// Repository provider identifier.
    pub repo_provider: String,
    /// Repository URL.
    pub repository: String,
    /// Repository branch.
    pub branch: String,
    /// Lifecycle status.
    pub status: String,
    /// Creation timestamp supplied by the application boundary.
    pub created_at: u64,
    /// Last update timestamp supplied by the application boundary.
    pub updated_at: u64,
}

/// Complete build mutation payload shared by create and update-capable
/// integration boundaries.
#[derive(SpacetimeType)]
pub struct BuildMutation {
    /// Source bundle, repository, or upload identity.
    pub source: String,
    /// Project owning the build when it came from a project workflow.
    pub project_id: Option<u64>,
    /// Branch, tag, or upload reference used for the build.
    pub source_ref: String,
}

/// Durable build lifecycle state.
#[table(accessor = build, public)]
pub struct Build {
    /// Stable build identity.
    #[primary_key]
    pub id: u64,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Repository source reference.
    pub source: String,
    /// Project owning the build, when submitted from a project workflow.
    pub project_id: Option<u64>,
    /// Branch, tag, or upload reference used for the build.
    pub source_ref: String,
    /// Pending, running, succeeded, failed, or cancelled.
    pub status: String,
    /// Worker generation fencing callbacks for this build.
    pub generation: u64,
}

/// Complete deployment metadata admitted by the durable create reducer.
#[derive(SpacetimeType)]
pub struct DeploymentMutation {
    /// Project owning the deployment, when present.
    pub project_id: Option<u64>,
    /// Monotonic project deployment revision.
    pub revision: u64,
    /// Deployment target category.
    pub target_type: String,
    /// Deployment target reference.
    pub target_ref: String,
    /// Preferred runner identity.
    pub preferred_runner: String,
    /// Bounded encoded environment entries.
    pub environment: Vec<u8>,
    /// Stable runtime identity assigned by the runner.
    pub runtime_id: String,
    /// Runtime execution mode.
    pub runtime_mode: String,
    /// Runtime HTTP endpoint.
    pub runtime_endpoint: String,
    /// Runtime-specific status.
    pub runtime_status: String,
}

/// Runtime projection applied after a generation-fenced lifecycle callback.
#[derive(SpacetimeType)]
pub struct DeploymentRuntimeMutation {
    /// Stable runtime identity assigned by the runner.
    pub runtime_id: String,
    /// Runtime execution mode.
    pub runtime_mode: String,
    /// Runtime HTTP endpoint.
    pub runtime_endpoint: String,
    /// Runtime-specific status.
    pub runtime_status: String,
}

/// Durable deployment lifecycle state.
#[table(accessor = deployment, public)]
pub struct Deployment {
    /// Stable deployment identity.
    #[primary_key]
    pub id: u64,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Source build identity.
    pub build_id: u64,
    /// Project owning the deployed build, when present.
    pub project_id: Option<u64>,
    /// Monotonic project deployment revision.
    pub revision: u64,
    /// Deployment target category.
    pub target_type: String,
    /// Deployment target reference.
    pub target_ref: String,
    /// Preferred runner identity.
    pub preferred_runner: String,
    /// Bounded encoded environment entries.
    pub environment: Vec<u8>,
    /// Stable runtime identity assigned by the runner.
    pub runtime_id: String,
    /// Runtime execution mode.
    pub runtime_mode: String,
    /// Runtime HTTP endpoint.
    pub runtime_endpoint: String,
    /// Runtime-specific status.
    pub runtime_status: String,
    /// Pending, starting, running, failed, or stopped.
    pub status: String,
    /// Runtime generation fencing callbacks for this deployment.
    pub generation: u64,
}

/// Durable operation lifecycle state.
#[table(accessor = operation, public)]
pub struct Operation {
    /// Stable operation identity.
    #[primary_key]
    pub id: u64,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Operation kind.
    pub kind: String,
    /// Request correlation identity used to join adapter and worker traces.
    pub correlation_id: String,
    /// Pending, processing, succeeded, failed, dead-lettered, or expired.
    pub status: String,
    /// Creation timestamp supplied by the application boundary.
    pub created_at: u64,
    /// Last lifecycle transition timestamp supplied by the application boundary.
    pub updated_at: u64,
    /// Bounded successful result payload.
    pub result: Vec<u8>,
    /// Bounded failure payload.
    pub failure: Vec<u8>,
}

/// Durable worker registration and heartbeat state.
#[table(accessor = runner, public)]
pub struct Runner {
    /// Stable runner identity.
    #[primary_key]
    pub id: u64,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Advertised capability names.
    pub capabilities: Vec<String>,
    /// Available, busy, draining, or offline.
    pub status: String,
    /// Last deterministic heartbeat timestamp.
    pub heartbeat_at: u64,
    /// Timestamp after which the runner is considered offline.
    pub lease_until: u64,
}

/// Durable idempotency and lifecycle event state.
#[table(accessor = event_log, public)]
pub struct EventLog {
    /// Monotonic event identity.
    #[primary_key]
    #[auto_inc]
    pub id: u64,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Originating command identity.
    pub command_id: u64,
    /// Stable event kind.
    pub kind: String,
    /// Serialized bounded event payload.
    pub payload: Vec<u8>,
}

/// Durable command admission and idempotency record.
#[table(accessor = command_record, public)]
pub struct CommandRecord {
    /// Stable command identity supplied by the caller.
    #[primary_key]
    pub id: u64,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Command kind.
    pub kind: String,
    /// Request correlation identity.
    pub correlation_id: String,
    /// Bounded request payload.
    pub payload: Vec<u8>,
    /// Pending, processing, succeeded, failed, or expired.
    pub status: String,
    /// Bounded successful result payload.
    pub result: Vec<u8>,
    /// Bounded failure payload.
    pub failure: Vec<u8>,
    /// Admission timestamp.
    pub created_at: u64,
    /// Last state transition timestamp.
    pub updated_at: u64,
}

/// Durable tenant identity and lifecycle state.
#[table(accessor = tenant, public)]
pub struct Tenant {
    /// Stable tenant identity.
    #[primary_key]
    pub id: u32,
    /// Normalized tenant name.
    pub name: String,
    /// Active or suspended.
    pub status: String,
    /// Creation timestamp.
    pub created_at: u64,
}

/// Normalized account identity without credential material.
#[table(accessor = account, public)]
pub struct Account {
    /// Stable subject identity.
    #[primary_key]
    pub id: u64,
    /// Primary tenant identity.
    pub tenant_id: u32,
    /// Normalized email address.
    pub email: String,
    /// Active, disabled, or pending.
    pub status: String,
    /// Whether MFA is required.
    pub mfa_required: bool,
    /// Creation timestamp.
    pub created_at: u64,
}

/// Tenant membership and permission projection.
#[table(accessor = membership, public)]
pub struct Membership {
    /// Subject/tenant composite identity.
    #[primary_key]
    pub id: String,
    /// Subject identity.
    pub subject_id: u64,
    /// Tenant identity.
    pub tenant_id: u32,
    /// Permission bitset.
    pub permissions: u32,
    /// Active or revoked.
    pub status: String,
    /// Last authorization update.
    pub updated_at: u64,
}

/// Credential material is private to reducers; clients receive only auth results.
#[table(accessor = credential, private)]
pub struct Credential {
    /// Account identity.
    #[primary_key]
    pub subject_id: u64,
    /// Password hash or external verifier reference.
    pub password_hash: String,
    /// Credential version.
    pub version: u32,
    /// Last credential update.
    pub updated_at: u64,
}

/// Private MFA and recovery state.
#[table(accessor = mfa_state, private)]
pub struct MfaState {
    /// Account identity.
    #[primary_key]
    pub subject_id: u64,
    /// Whether MFA is enabled.
    pub enabled: bool,
    /// Whether TOTP is enabled.
    pub totp_enabled: bool,
    /// Whether email OTP is enabled.
    pub email_otp_enabled: bool,
    /// Protected TOTP secret or provider reference.
    pub totp_secret: String,
    /// Number of remaining recovery codes.
    pub recovery_codes: u16,
    /// Last MFA update.
    pub updated_at: u64,
}

/// Private one-time email challenge material.
#[table(accessor = email_otp_challenge, private)]
pub struct EmailOtpChallenge {
    /// Account identity.
    #[primary_key]
    pub subject_id: u64,
    /// SHA-256 digest of the six-digit code.
    pub code_hash: Vec<u8>,
    /// Deterministic expiration timestamp.
    pub expires_at: u64,
}

/// Bounded tenant mutation payload.
#[derive(SpacetimeType)]
pub struct TenantMutation {
    /// Tenant name.
    pub name: String,
    /// Tenant lifecycle status.
    pub status: String,
    /// Creation timestamp.
    pub created_at: u64,
}

/// Bounded normalized account mutation payload.
#[derive(SpacetimeType)]
pub struct AccountMutation {
    /// Tenant identity.
    pub tenant_id: u32,
    /// Normalized email.
    pub email: String,
    /// Account status.
    pub status: String,
    /// MFA requirement.
    pub mfa_required: bool,
    /// Creation timestamp.
    pub created_at: u64,
}

/// Bounded membership mutation payload.
#[derive(SpacetimeType)]
pub struct MembershipMutation {
    /// Subject identity.
    pub subject_id: u64,
    /// Tenant identity.
    pub tenant_id: u32,
    /// Permission bitset.
    pub permissions: u32,
    /// Membership status.
    pub status: String,
    /// Update timestamp.
    pub updated_at: u64,
}

/// Private credential mutation payload.
#[derive(SpacetimeType)]
pub struct CredentialMutation {
    /// Password hash or external verifier reference.
    pub password_hash: String,
    /// Credential version.
    pub version: u32,
    /// Update timestamp.
    pub updated_at: u64,
}

/// Private MFA mutation payload.
#[derive(SpacetimeType)]
pub struct MfaMutation {
    /// MFA enabled state.
    pub enabled: bool,
    /// TOTP enabled state.
    pub totp_enabled: bool,
    /// Email OTP enabled state.
    pub email_otp_enabled: bool,
    /// Protected TOTP secret or provider reference.
    pub totp_secret: String,
    /// Remaining recovery code count.
    pub recovery_codes: u16,
    /// Update timestamp.
    pub updated_at: u64,
}

/// Durable worker queue state.
#[table(accessor = job, public)]
pub struct Job {
    /// Idempotent job identity.
    #[primary_key]
    pub id: u64,
    /// Dispatch kind.
    pub kind: String,
    /// Bounded target entity owned by the job.
    pub target_id: u64,
    /// Owning tenant boundary.
    pub tenant_id: u64,
    /// Generation fence carried into worker recovery.
    pub generation: u64,
    /// Pending, claimed, completed, or dead-lettered.
    pub state: String,
    /// Number of claims attempted.
    pub attempts: u16,
    /// Maximum attempts.
    pub max_attempts: u16,
    /// Lease expiry, in deterministic Unix seconds.
    pub lease_until: u64,
    /// Worker currently holding the lease.
    pub worker_id: u64,
}

/// Durable authentication snapshot envelope.
#[table(accessor = auth_snapshot, public)]
pub struct AuthSnapshot {
    /// Singleton row identity.
    #[primary_key]
    pub id: u8,
    /// Snapshot schema version.
    pub version: u16,
    /// Bounded serialized snapshot.
    pub payload: Vec<u8>,
}

/// Durable authenticated session state.
#[table(accessor = auth_session, public)]
pub struct AuthSession {
    /// Opaque session token identity.
    #[primary_key]
    pub token: String,
    /// Authenticated subject identity.
    pub subject_id: u64,
    /// Tenant boundary selected at issuance.
    pub tenant_id: u32,
    /// Deterministic expiry timestamp.
    pub expires_at: u64,
    /// Whether MFA has been satisfied.
    pub mfa_verified: bool,
    /// Revocation marker retained for auditability.
    pub revoked: bool,
}

/// Durable email delivery intent and retry state.
#[table(accessor = email_outbox, public)]
pub struct EmailOutbox {
    /// Idempotent message identity.
    #[primary_key]
    pub id: u64,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Recipient address.
    pub recipient: String,
    /// Bounded subject.
    pub subject: String,
    /// Bounded message body.
    pub body: String,
    /// Pending, claimed, sent, or failed.
    pub state: String,
    /// Number of provider attempts.
    pub attempts: u16,
    /// Maximum provider attempts.
    pub max_attempts: u16,
    /// Worker/provider holding the claim.
    pub claimed_by: u64,
}

/// Durable bounded telemetry sample.
#[table(accessor = telemetry_sample, public)]
pub struct TelemetrySample {
    /// Monotonic sample identity.
    #[primary_key]
    #[auto_inc]
    pub id: u64,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Metric name.
    pub name: String,
    /// Finite metric value.
    pub value: f64,
    /// Deterministic sample timestamp.
    pub timestamp: u64,
}

/// Durable object metadata; object bytes remain in an object-store adapter.
#[table(accessor = object_metadata, public)]
pub struct ObjectMetadata {
    /// Tenant-qualified object key.
    #[primary_key]
    pub key: String,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Byte length of the external object.
    pub size: u64,
    /// Bounded content type.
    pub content_type: String,
    /// Bounded integrity digest or provider version.
    pub checksum: String,
    /// Available, expired, or deleted.
    pub status: String,
    /// Creation timestamp.
    pub created_at: u64,
    /// Optional retention deadline.
    pub retention_until: u64,
}

/// Durable metadata for an external source, artifact, log, trace, or profile
/// body. The bytes remain in the object/telemetry store.
#[table(accessor = artifact_metadata, public)]
pub struct ArtifactMetadata {
    /// Stable tenant-qualified artifact identity.
    #[primary_key]
    pub id: String,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Build that produced the artifact, when applicable.
    pub build_id: Option<u64>,
    /// Deployment that consumes the artifact, when applicable.
    pub deployment_id: Option<u64>,
    /// Artifact, source, log, trace, or profile category.
    pub kind: String,
    /// External object-store key.
    pub object_key: String,
    /// External body length.
    pub size: u64,
    /// Integrity digest.
    pub checksum: String,
    /// Content type or profile format.
    pub content_type: String,
    /// Pending, available, deleted, or expired.
    pub status: String,
    /// Creation timestamp.
    pub created_at: u64,
    /// Optional retention deadline.
    pub retention_until: u64,
}

/// Searchable metadata for externally stored build logs.
#[table(accessor = build_log_index, public)]
pub struct BuildLogIndex {
    /// Stable log identity.
    #[primary_key]
    pub id: u64,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Build identity.
    pub build_id: u64,
    /// External log object key.
    pub object_key: String,
    /// Number of indexed log lines.
    pub line_count: u64,
    /// Integrity digest.
    pub checksum: String,
    /// Creation timestamp.
    pub created_at: u64,
}

/// Durable route projection for a deployment runtime.
#[table(accessor = deployment_route, public)]
pub struct DeploymentRoute {
    /// Stable hostname/path identity.
    #[primary_key]
    pub id: String,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Deployment identity.
    pub deployment_id: u64,
    /// Hostname exposed by the route.
    pub hostname: String,
    /// Bounded route path.
    pub path: String,
    /// Runtime target port.
    pub target_port: u16,
    /// Pending, active, draining, or removed.
    pub status: String,
    /// Last route projection timestamp.
    pub updated_at: u64,
}

/// Latest bounded health observation for a deployment runtime.
#[table(accessor = runtime_health, public)]
pub struct RuntimeHealth {
    /// One health row per deployment.
    #[primary_key]
    pub deployment_id: u64,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Healthy, degraded, or failed.
    pub status: String,
    /// Probe timestamp.
    pub checked_at: u64,
    /// Probe latency in milliseconds.
    pub latency_ms: u64,
    /// Bounded diagnostic message.
    pub message: String,
}

/// Searchable trace-span index; span bodies and large attributes remain
/// external when they exceed the bounded metadata payload.
#[table(accessor = trace_span, public)]
pub struct TraceSpan {
    /// Monotonic span identity.
    #[primary_key]
    #[auto_inc]
    pub id: u64,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Trace identity.
    pub trace_id: String,
    /// Span identity within the trace.
    pub span_id: String,
    /// Optional parent span identity.
    pub parent_span_id: String,
    /// Correlated operation identity.
    pub operation_id: Option<u64>,
    /// Span name.
    pub name: String,
    /// Start timestamp in deterministic microseconds.
    pub started_at: u64,
    /// Span duration in microseconds.
    pub duration_us: u64,
    /// Ok, error, or unset.
    pub status: String,
    /// Bounded serialized attributes.
    pub attributes: Vec<u8>,
}

/// Searchable profile artifact index.
#[table(accessor = profile_index, public)]
pub struct ProfileIndex {
    /// Monotonic profile identity.
    #[primary_key]
    #[auto_inc]
    pub id: u64,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Profile session identity.
    pub profile_id: String,
    /// External profile body key.
    pub object_key: String,
    /// CPU, allocation, lock, or custom profile type.
    pub profile_type: String,
    /// Start timestamp.
    pub started_at: u64,
    /// Profile duration in microseconds.
    pub duration_us: u64,
    /// Integrity digest.
    pub checksum: String,
}

/// Bounded searchable log record metadata.
#[table(accessor = log_record, public)]
pub struct LogRecord {
    /// Monotonic log identity.
    #[primary_key]
    #[auto_inc]
    pub id: u64,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Correlated operation identity.
    pub operation_id: Option<u64>,
    /// Trace identity when available.
    pub trace_id: String,
    /// Log severity.
    pub level: String,
    /// Bounded logger target.
    pub target: String,
    /// Bounded redacted message.
    pub message: String,
    /// Timestamp.
    pub timestamp: u64,
}

/// Immutable security and lifecycle audit record.
#[table(accessor = audit_record, public)]
pub struct AuditRecord {
    /// Monotonic audit identity.
    #[primary_key]
    #[auto_inc]
    pub id: u64,
    /// Tenant owner.
    pub tenant_id: u32,
    /// Subject that caused the action.
    pub actor_id: u64,
    /// Stable action identifier.
    pub action: String,
    /// Entity category affected by the action.
    pub entity_type: String,
    /// Entity identity encoded as a bounded string.
    pub entity_id: String,
    /// Request correlation identity.
    pub correlation_id: String,
    /// Causal parent event identity.
    pub causation_id: String,
    /// Audit payload schema version.
    pub schema_version: u16,
    /// Event timestamp.
    pub timestamp: u64,
    /// Bounded redacted event payload.
    pub payload: Vec<u8>,
}

/// Creates or idempotently replays a tenant identity.
#[reducer]
pub fn upsert_tenant(ctx: &ReducerContext, id: u32, input: TenantMutation) -> Result<(), String> {
    if id == 0
        || input.created_at == 0
        || !valid_text(&input.name, MAX_KIND_BYTES, true)
        || !valid_text(&input.status, MAX_KIND_BYTES, true)
    {
        return Err("invalid_tenant".to_owned());
    }
    if let Some(existing) = ctx.db.tenant().id().find(id) {
        if existing.name == input.name
            && existing.status == input.status
            && existing.created_at == input.created_at
        {
            return Ok(());
        }
        ctx.db.tenant().id().delete(id);
    }
    ctx.db.tenant().insert(Tenant {
        id,
        name: input.name,
        status: input.status,
        created_at: input.created_at,
    });
    Ok(())
}

/// Creates or idempotently replays a normalized account identity.
#[reducer]
pub fn upsert_account(ctx: &ReducerContext, id: u64, input: AccountMutation) -> Result<(), String> {
    if id == 0
        || input.tenant_id == 0
        || input.created_at == 0
        || !valid_email(&input.email)
        || !valid_text(&input.status, MAX_KIND_BYTES, true)
    {
        return Err("invalid_account".to_owned());
    }
    if let Some(existing) = ctx.db.account().id().find(id) {
        if existing.tenant_id == input.tenant_id
            && existing.email == input.email
            && existing.status == input.status
            && existing.mfa_required == input.mfa_required
            && existing.created_at == input.created_at
        {
            return Ok(());
        }
        ctx.db.account().id().delete(id);
    }
    ctx.db.account().insert(Account {
        id,
        tenant_id: input.tenant_id,
        email: input.email,
        status: input.status,
        mfa_required: input.mfa_required,
        created_at: input.created_at,
    });
    Ok(())
}

/// Creates or replaces one tenant membership projection.
#[reducer]
pub fn upsert_membership(
    ctx: &ReducerContext,
    id: String,
    input: MembershipMutation,
) -> Result<(), String> {
    if input.subject_id == 0
        || input.tenant_id == 0
        || input.updated_at == 0
        || !valid_text(&id, MAX_KIND_BYTES * 2, true)
        || !valid_text(&input.status, MAX_KIND_BYTES, true)
    {
        return Err("invalid_membership".to_owned());
    }
    if let Some(existing) = ctx.db.membership().id().find(id.clone()) {
        if existing.subject_id == input.subject_id
            && existing.tenant_id == input.tenant_id
            && existing.permissions == input.permissions
            && existing.status == input.status
            && existing.updated_at == input.updated_at
        {
            return Ok(());
        }
        ctx.db.membership().id().delete(id.clone());
    }
    ctx.db.membership().insert(Membership {
        id,
        subject_id: input.subject_id,
        tenant_id: input.tenant_id,
        permissions: input.permissions,
        status: input.status,
        updated_at: input.updated_at,
    });
    Ok(())
}

/// Replaces private credential material for an account.
#[reducer]
pub fn put_credential(
    ctx: &ReducerContext,
    subject_id: u64,
    input: CredentialMutation,
) -> Result<(), String> {
    if subject_id == 0
        || input.version == 0
        || input.updated_at == 0
        || !valid_text(&input.password_hash, MAX_PAYLOAD_BYTES, true)
    {
        return Err("invalid_credential".to_owned());
    }
    if ctx.db.credential().subject_id().find(subject_id).is_some() {
        ctx.db.credential().subject_id().delete(subject_id);
    }
    ctx.db.credential().insert(Credential {
        subject_id,
        password_hash: input.password_hash,
        version: input.version,
        updated_at: input.updated_at,
    });
    Ok(())
}

/// Verifies a password against private credential material without exposing
/// the stored hash to the generated client.
#[reducer]
pub fn verify_password(
    ctx: &ReducerContext,
    subject_id: u64,
    password: String,
) -> Result<(), String> {
    if subject_id == 0 || password.is_empty() || password.len() > MAX_PAYLOAD_BYTES {
        return Err("invalid_credentials".to_owned());
    }
    let credential = ctx
        .db
        .credential()
        .subject_id()
        .find(subject_id)
        .ok_or_else(|| "invalid_credentials".to_owned())?;
    if bcrypt::verify(&password, &credential.password_hash).unwrap_or(false) {
        Ok(())
    } else {
        Err("invalid_credentials".to_owned())
    }
}

/// Replaces private MFA configuration for an account.
#[reducer]
pub fn put_mfa_state(
    ctx: &ReducerContext,
    subject_id: u64,
    input: MfaMutation,
) -> Result<(), String> {
    if subject_id == 0 || input.updated_at == 0 || input.totp_secret.len() > MAX_PAYLOAD_BYTES {
        return Err("invalid_mfa_state".to_owned());
    }
    if ctx.db.mfa_state().subject_id().find(subject_id).is_some() {
        ctx.db.mfa_state().subject_id().delete(subject_id);
    }
    ctx.db.mfa_state().insert(MfaState {
        subject_id,
        enabled: input.enabled,
        totp_enabled: input.totp_enabled,
        email_otp_enabled: input.email_otp_enabled,
        totp_secret: input.totp_secret,
        recovery_codes: input.recovery_codes,
        updated_at: input.updated_at,
    });
    Ok(())
}

/// Replaces the private email OTP challenge for an account.
#[reducer]
pub fn issue_email_otp(
    ctx: &ReducerContext,
    subject_id: u64,
    code_hash: Vec<u8>,
    expires_at: u64,
) -> Result<(), String> {
    if subject_id == 0 || code_hash.len() != 32 || expires_at == 0 {
        return Err("invalid_email_otp_challenge".to_owned());
    }
    if ctx
        .db
        .email_otp_challenge()
        .subject_id()
        .find(subject_id)
        .is_some()
    {
        ctx.db.email_otp_challenge().subject_id().delete(subject_id);
    }
    ctx.db.email_otp_challenge().insert(EmailOtpChallenge {
        subject_id,
        code_hash,
        expires_at,
    });
    Ok(())
}

/// Verifies and consumes one private email OTP challenge.
#[reducer]
pub fn verify_email_otp(
    ctx: &ReducerContext,
    subject_id: u64,
    code_hash: Vec<u8>,
    now: u64,
) -> Result<(), String> {
    if subject_id == 0 || code_hash.len() != 32 || now == 0 {
        return Err("invalid_email_otp".to_owned());
    }
    let challenge = ctx
        .db
        .email_otp_challenge()
        .subject_id()
        .find(subject_id)
        .ok_or_else(|| "invalid_email_otp".to_owned())?;
    if challenge.expires_at <= now || challenge.code_hash != code_hash {
        return Err("invalid_email_otp".to_owned());
    }
    ctx.db.email_otp_challenge().subject_id().delete(subject_id);
    Ok(())
}

/// Inserts a tenant-scoped project after validating bounded fields.
#[reducer]
pub fn create_project(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    input: ProjectMutation,
) -> Result<(), String> {
    if id == 0 || tenant_id == 0 {
        return Err("invalid_project".to_owned());
    }
    if !valid_project_fields(input.repository.as_str(), input.branch.as_str(), "pending")
        || input.name.trim().is_empty()
        || input.name.len() > MAX_KIND_BYTES
        || input.slug.trim().is_empty()
        || input.slug.len() > MAX_KIND_BYTES
        || input.description.len() > MAX_PAYLOAD_BYTES
        || input.repo_provider.len() > MAX_KIND_BYTES
        || input.created_at == 0
        || input.updated_at < input.created_at
    {
        return Err("project_field_too_large".to_owned());
    }
    if ctx.db.project().id().find(id).is_some() {
        return Err("project_exists".to_owned());
    }
    ctx.db.project().insert(Project {
        id,
        tenant_id,
        name: input.name,
        slug: input.slug,
        description: input.description,
        repo_provider: input.repo_provider,
        repository: input.repository,
        branch: input.branch,
        status: "pending".to_owned(),
        created_at: input.created_at,
        updated_at: input.updated_at,
    });
    Ok(())
}

/// Updates a project only when the caller owns its tenant row.
///
/// Repeating the same update is successful so a lost reducer acknowledgement
/// can be retried safely after the mutation has committed.
#[reducer]
pub fn update_project(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    status: String,
    input: ProjectMutation,
) -> Result<(), String> {
    if id == 0
        || tenant_id == 0
        || !valid_project_fields(&input.repository, &input.branch, &status)
        || input.name.trim().is_empty()
        || input.name.len() > MAX_KIND_BYTES
        || input.slug.trim().is_empty()
        || input.slug.len() > MAX_KIND_BYTES
        || input.description.len() > MAX_PAYLOAD_BYTES
        || input.repo_provider.len() > MAX_KIND_BYTES
        || input.updated_at == 0
    {
        return Err("invalid_project".to_owned());
    }
    let mut project = ctx
        .db
        .project()
        .id()
        .find(id)
        .ok_or_else(|| "project_not_found".to_owned())?;
    if project.tenant_id != tenant_id {
        return Err("project_tenant_mismatch".to_owned());
    }
    if project.name == input.name
        && project.slug == input.slug
        && project.description == input.description
        && project.repo_provider == input.repo_provider
        && project.repository == input.repository
        && project.branch == input.branch
        && project.status == status
        && project.updated_at == input.updated_at
    {
        return Ok(());
    }
    project.name = input.name;
    project.slug = input.slug;
    project.description = input.description;
    project.repo_provider = input.repo_provider;
    project.repository = input.repository;
    project.branch = input.branch;
    project.status = status;
    project.updated_at = input.updated_at;
    ctx.db.project().id().update(project);
    Ok(())
}

/// Deletes a project only when the caller owns its tenant row.
///
/// Deleting an already absent row is successful so retries converge after a
/// committed delete whose acknowledgement was lost.
#[reducer]
pub fn delete_project(ctx: &ReducerContext, id: u64, tenant_id: u32) -> Result<(), String> {
    if id == 0 || tenant_id == 0 {
        return Err("invalid_project".to_owned());
    }
    let Some(project) = ctx.db.project().id().find(id) else {
        return Ok(());
    };
    if project.tenant_id != tenant_id {
        return Err("project_tenant_mismatch".to_owned());
    }
    ctx.db.project().id().delete(id);
    Ok(())
}

/// Creates a pending build owned by one tenant.
#[reducer]
pub fn create_build(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    mutation: BuildMutation,
) -> Result<(), String> {
    if id == 0
        || tenant_id == 0
        || mutation.source.trim().is_empty()
        || mutation.source.len() > MAX_PAYLOAD_BYTES
        || mutation.source_ref.len() > MAX_PAYLOAD_BYTES
    {
        return Err("invalid_build".to_owned());
    }
    if ctx.db.build().id().find(id).is_some() {
        return Err("build_exists".to_owned());
    }
    ctx.db.build().insert(Build {
        id,
        tenant_id,
        source: mutation.source,
        project_id: mutation.project_id,
        source_ref: mutation.source_ref,
        status: "pending".to_owned(),
        generation: 0,
    });
    Ok(())
}

/// Applies a generation-fenced build lifecycle transition.
#[reducer]
pub fn transition_build(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    generation: u64,
    status: String,
) -> Result<(), String> {
    if tenant_id == 0 || status.trim().is_empty() || status.len() > MAX_KIND_BYTES {
        return Err("invalid_build_transition".to_owned());
    }
    let mut build = ctx
        .db
        .build()
        .id()
        .find(id)
        .ok_or_else(|| "build_not_found".to_owned())?;
    if build.tenant_id != tenant_id
        || build.generation != generation
        || !build_transition_allowed(&build.status, &status)
    {
        return Err("invalid_build_transition".to_owned());
    }
    build.status = status;
    ctx.db.build().id().update(build);
    Ok(())
}

/// Requeues a running build after its worker was lost.
#[reducer]
pub fn recover_build(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    generation: u64,
) -> Result<(), String> {
    if tenant_id == 0 {
        return Err("invalid_build_recovery".to_owned());
    }
    let mut build = ctx
        .db
        .build()
        .id()
        .find(id)
        .ok_or_else(|| "build_not_found".to_owned())?;
    if build.tenant_id != tenant_id || build.generation != generation || build.status != "running" {
        return Err("invalid_build_recovery".to_owned());
    }
    build.status = "pending".to_owned();
    ctx.db.build().id().update(build);
    Ok(())
}

/// Creates a deployment only for an existing build.
#[reducer]
pub fn create_deployment(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    build_id: u64,
    mutation: DeploymentMutation,
) -> Result<(), String> {
    if id == 0 || tenant_id == 0 || build_id == 0 || !valid_deployment_metadata(&mutation) {
        return Err("invalid_deployment".to_owned());
    }
    if ctx.db.deployment().id().find(id).is_some() {
        return Err("deployment_exists".to_owned());
    }
    let build = ctx
        .db
        .build()
        .id()
        .find(build_id)
        .ok_or_else(|| "build_not_found".to_owned())?;
    if build.tenant_id != tenant_id || build.status != "succeeded" {
        return Err("build_not_deployable".to_owned());
    }
    ctx.db.deployment().insert(Deployment {
        id,
        tenant_id,
        build_id,
        project_id: mutation.project_id,
        revision: mutation.revision,
        target_type: mutation.target_type,
        target_ref: mutation.target_ref,
        preferred_runner: mutation.preferred_runner,
        environment: mutation.environment,
        runtime_id: mutation.runtime_id,
        runtime_mode: mutation.runtime_mode,
        runtime_endpoint: mutation.runtime_endpoint,
        runtime_status: mutation.runtime_status,
        status: "pending".to_owned(),
        generation: 0,
    });
    Ok(())
}

/// Applies a generation-fenced deployment lifecycle transition.
#[reducer]
pub fn transition_deployment(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    generation: u64,
    status: String,
) -> Result<(), String> {
    if tenant_id == 0 || status.trim().is_empty() || status.len() > MAX_KIND_BYTES {
        return Err("invalid_deployment_transition".to_owned());
    }
    let mut deployment = ctx
        .db
        .deployment()
        .id()
        .find(id)
        .ok_or_else(|| "deployment_not_found".to_owned())?;
    if deployment.tenant_id != tenant_id
        || deployment.generation != generation
        || !deployment_transition_allowed(&deployment.status, &status)
    {
        return Err("invalid_deployment_transition".to_owned());
    }
    deployment.status = status;
    ctx.db.deployment().id().update(deployment);
    Ok(())
}

/// Requeues a deployment whose runtime was lost while starting.
#[reducer]
pub fn recover_deployment(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    generation: u64,
) -> Result<(), String> {
    if tenant_id == 0 {
        return Err("invalid_deployment_recovery".to_owned());
    }
    let mut deployment = ctx
        .db
        .deployment()
        .id()
        .find(id)
        .ok_or_else(|| "deployment_not_found".to_owned())?;
    if deployment.tenant_id != tenant_id
        || deployment.generation != generation
        || deployment.status != "starting"
    {
        return Err("invalid_deployment_recovery".to_owned());
    }
    deployment.status = "pending".to_owned();
    deployment.runtime_id = String::default();
    deployment.runtime_mode = String::default();
    deployment.runtime_endpoint = String::default();
    deployment.runtime_status = String::default();
    ctx.db.deployment().id().update(deployment);
    Ok(())
}

/// Applies a generation-fenced deployment status and runtime projection.
#[reducer]
pub fn update_deployment_runtime(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    generation: u64,
    status: String,
    runtime: DeploymentRuntimeMutation,
) -> Result<(), String> {
    if tenant_id == 0
        || status.trim().is_empty()
        || status.len() > MAX_KIND_BYTES
        || !valid_deployment_runtime(&runtime)
    {
        return Err("invalid_deployment_runtime".to_owned());
    }
    let mut deployment = ctx
        .db
        .deployment()
        .id()
        .find(id)
        .ok_or_else(|| "deployment_not_found".to_owned())?;
    if deployment.tenant_id != tenant_id
        || deployment.generation != generation
        || !deployment_transition_allowed(&deployment.status, &status)
    {
        return Err("invalid_deployment_runtime".to_owned());
    }
    deployment.status = status;
    deployment.runtime_id = runtime.runtime_id;
    deployment.runtime_mode = runtime.runtime_mode;
    deployment.runtime_endpoint = runtime.runtime_endpoint;
    deployment.runtime_status = runtime.runtime_status;
    ctx.db.deployment().id().update(deployment);
    Ok(())
}

/// Creates a pending operation with a bounded payload.
#[reducer]
pub fn create_operation(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    kind: String,
    correlation_id: String,
    created_at: u64,
) -> Result<(), String> {
    if id == 0
        || tenant_id == 0
        || kind.trim().is_empty()
        || kind.len() > MAX_KIND_BYTES
        || correlation_id.len() > MAX_PAYLOAD_BYTES
        || created_at == 0
    {
        return Err("invalid_operation".to_owned());
    }
    if ctx.db.operation().id().find(id).is_some() {
        return Err("operation_exists".to_owned());
    }
    ctx.db.operation().insert(Operation {
        id,
        tenant_id,
        kind,
        correlation_id,
        status: "pending".to_owned(),
        created_at,
        updated_at: created_at,
        result: Vec::from([]),
        failure: Vec::from([]),
    });
    Ok(())
}

/// Applies a monotonic tenant-fenced operation transition and payload.
#[reducer]
pub fn transition_operation(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    status: String,
    payload: Vec<u8>,
    updated_at: u64,
) -> Result<(), String> {
    if tenant_id == 0
        || status.trim().is_empty()
        || status.len() > MAX_KIND_BYTES
        || payload.len() > MAX_PAYLOAD_BYTES
        || updated_at == 0
    {
        return Err("invalid_operation_transition".to_owned());
    }
    let mut operation = ctx
        .db
        .operation()
        .id()
        .find(id)
        .ok_or_else(|| "operation_not_found".to_owned())?;
    if operation.tenant_id != tenant_id || !operation_transition_allowed(&operation.status, &status)
    {
        return Err("invalid_operation_transition".to_owned());
    }
    operation.status = status;
    operation.updated_at = updated_at;
    if operation.status == "succeeded" {
        operation.result = payload;
        operation.failure.clear();
    } else if matches!(
        operation.status.as_str(),
        "failed" | "dead_lettered" | "expired"
    ) {
        operation.failure = payload;
        operation.result.clear();
    } else {
        operation.result.clear();
        operation.failure.clear();
    }
    ctx.db.operation().id().update(operation);
    Ok(())
}

/// Registers or refreshes a tenant-scoped runner heartbeat.
#[reducer]
pub fn register_runner(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    capabilities: Vec<String>,
    heartbeat_at: u64,
    lease_seconds: u64,
) -> Result<(), String> {
    if id == 0
        || tenant_id == 0
        || heartbeat_at == 0
        || lease_seconds == 0
        || capabilities.is_empty()
        || capabilities.iter().any(|capability| {
            capability.trim().is_empty() || capability.len() > MAX_RUNNER_CAPABILITY_BYTES
        })
        || has_duplicate_strings(&capabilities)
    {
        return Err("invalid_runner".to_owned());
    }
    if let Some(mut runner) = ctx.db.runner().id().find(id) {
        if runner.tenant_id != tenant_id {
            return Err("runner_tenant_mismatch".to_owned());
        }
        runner.capabilities = capabilities;
        runner.status = "available".to_owned();
        runner.heartbeat_at = heartbeat_at;
        runner.lease_until = heartbeat_at.saturating_add(lease_seconds);
        ctx.db.runner().id().update(runner);
    } else {
        ctx.db.runner().insert(Runner {
            id,
            tenant_id,
            capabilities,
            status: "available".to_owned(),
            heartbeat_at,
            lease_until: heartbeat_at.saturating_add(lease_seconds),
        });
    }
    Ok(())
}

fn has_duplicate_strings(values: &[String]) -> bool {
    values
        .iter()
        .enumerate()
        .any(|(index, value)| values[index + 1..].iter().any(|other| other == value))
}

/// Claims one available tenant runner with the requested capability.
#[reducer]
pub fn claim_runner(
    ctx: &ReducerContext,
    tenant_id: u32,
    capability: String,
    now: u64,
) -> Result<(), String> {
    if tenant_id == 0
        || capability.trim().is_empty()
        || capability.len() > MAX_RUNNER_CAPABILITY_BYTES
    {
        return Err("invalid_runner_claim".to_owned());
    }
    let mut runner = ctx
        .db
        .runner()
        .iter()
        .filter(|runner| {
            runner.tenant_id == tenant_id
                && runner.status == "available"
                && runner.lease_until > now
                && runner.capabilities.iter().any(|value| value == &capability)
        })
        .min_by_key(|runner| runner.id)
        .ok_or_else(|| "runner_not_available".to_owned())?;
    runner.status = "busy".to_owned();
    ctx.db.runner().id().update(runner);
    Ok(())
}

/// Releases a busy tenant runner back to the available pool.
#[reducer]
pub fn release_runner(ctx: &ReducerContext, id: u64, tenant_id: u32) -> Result<(), String> {
    let mut runner = ctx
        .db
        .runner()
        .id()
        .find(id)
        .ok_or_else(|| "runner_not_found".to_owned())?;
    if runner.tenant_id != tenant_id || runner.status != "busy" {
        return Err("invalid_runner_release".to_owned());
    }
    runner.status = "available".to_owned();
    ctx.db.runner().id().update(runner);
    Ok(())
}

/// Marks heartbeat-expired runners offline.
#[reducer]
pub fn expire_runners(ctx: &ReducerContext, now: u64) -> Result<(), String> {
    for mut runner in ctx.db.runner().iter() {
        if runner.status != "offline" && runner.lease_until <= now {
            runner.status = "offline".to_owned();
            ctx.db.runner().id().update(runner);
        }
    }
    Ok(())
}

/// Appends an idempotent event row.
#[reducer]
pub fn append_event(
    ctx: &ReducerContext,
    tenant_id: u32,
    command_id: u64,
    kind: String,
    payload: Vec<u8>,
) -> Result<(), String> {
    if tenant_id == 0 || command_id == 0 || kind.trim().is_empty() {
        return Err("invalid_event".to_owned());
    }
    if kind.len() > MAX_KIND_BYTES || payload.len() > MAX_PAYLOAD_BYTES {
        return Err("event_too_large".to_owned());
    }
    if let Some(existing) = ctx
        .db
        .event_log()
        .iter()
        .find(|event| event.command_id == command_id)
    {
        if existing.tenant_id == tenant_id && existing.kind == kind && existing.payload == payload {
            return Ok(());
        }
        return Err("event_idempotency_conflict".to_owned());
    }
    ctx.db.event_log().insert(EventLog {
        id: 0,
        tenant_id,
        command_id,
        kind,
        payload,
    });
    Ok(())
}

/// Admits one command and makes retries idempotent by command identity.
#[reducer]
pub fn admit_command(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    kind: String,
    correlation_id: String,
    payload: Vec<u8>,
    created_at: u64,
) -> Result<(), String> {
    if id == 0
        || tenant_id == 0
        || kind.trim().is_empty()
        || kind.len() > MAX_KIND_BYTES
        || correlation_id.len() > MAX_PAYLOAD_BYTES
        || payload.len() > MAX_PAYLOAD_BYTES
        || created_at == 0
    {
        return Err("invalid_command".to_owned());
    }
    if let Some(existing) = ctx.db.command_record().id().find(id) {
        if existing.tenant_id == tenant_id
            && existing.kind == kind
            && existing.correlation_id == correlation_id
            && existing.payload == payload
        {
            return Ok(());
        }
        return Err("command_idempotency_conflict".to_owned());
    }
    ctx.db.command_record().insert(CommandRecord {
        id,
        tenant_id,
        kind,
        correlation_id,
        payload,
        status: "pending".to_owned(),
        result: Vec::with_capacity(0),
        failure: Vec::with_capacity(0),
        created_at,
        updated_at: created_at,
    });
    Ok(())
}

/// Applies a monotonic command result transition.
#[reducer]
pub fn transition_command(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    status: String,
    payload: Vec<u8>,
    updated_at: u64,
) -> Result<(), String> {
    if tenant_id == 0
        || status.trim().is_empty()
        || status.len() > MAX_KIND_BYTES
        || payload.len() > MAX_PAYLOAD_BYTES
        || updated_at == 0
        || !matches!(
            status.as_str(),
            "processing" | "succeeded" | "failed" | "expired"
        )
    {
        return Err("invalid_command_transition".to_owned());
    }
    let mut command = ctx
        .db
        .command_record()
        .id()
        .find(id)
        .ok_or_else(|| "command_not_found".to_owned())?;
    if command.tenant_id != tenant_id
        || command.updated_at > updated_at
        || (command.status == "pending"
            && status != "processing"
            && status != "succeeded"
            && status != "failed"
            && status != "expired")
        || (command.status == "processing" && status == "processing")
        || matches!(command.status.as_str(), "succeeded" | "failed" | "expired")
    {
        return Err("invalid_command_transition".to_owned());
    }
    command.status = status;
    command.updated_at = updated_at;
    if command.status == "succeeded" {
        command.result = payload;
        command.failure.clear();
    } else if matches!(command.status.as_str(), "failed" | "expired") {
        command.failure = payload;
        command.result.clear();
    } else {
        command.result.clear();
        command.failure.clear();
    }
    ctx.db.command_record().id().update(command);
    Ok(())
}

/// Enqueues an idempotent job row.
#[reducer]
pub fn enqueue_job(
    ctx: &ReducerContext,
    id: u64,
    kind: String,
    target_id: u64,
    tenant_id: u64,
    generation: u64,
    max_attempts: u16,
) -> Result<(), String> {
    if id == 0
        || target_id == 0
        || tenant_id == 0
        || generation == 0
        || kind.trim().is_empty()
        || kind.len() > MAX_KIND_BYTES
        || max_attempts == 0
    {
        return Err("invalid_job".to_owned());
    }
    if ctx.db.job().id().find(id).is_some() {
        return Ok(());
    }
    if ctx.db.job().iter().count() >= MAX_JOB_ROWS {
        return Err("job_capacity_exceeded".to_owned());
    }
    ctx.db.job().insert(Job {
        id,
        kind,
        target_id,
        tenant_id,
        generation,
        state: "pending".to_owned(),
        attempts: 0,
        max_attempts,
        lease_until: 0,
        worker_id: 0,
    });
    Ok(())
}

/// Reconciles expired worker leases during process startup or recovery.
#[reducer]
pub fn reconcile_jobs(ctx: &ReducerContext, now: u64) -> Result<(), String> {
    if now == 0 {
        return Err("invalid_reconciliation_time".to_owned());
    }
    let expired: Vec<Job> = ctx
        .db
        .job()
        .iter()
        .filter(|job| job.state == "claimed" && job.lease_until <= now)
        .collect();
    for mut job in expired {
        job.state = if job.attempts >= job.max_attempts {
            "dead_lettered".to_owned()
        } else {
            "pending".to_owned()
        };
        job.lease_until = 0;
        job.worker_id = 0;
        ctx.db.job().id().update(job);
    }
    Ok(())
}

/// Claims the oldest pending or expired job with a worker lease.
#[reducer]
pub fn claim_job(
    ctx: &ReducerContext,
    worker_id: u64,
    now: u64,
    lease_seconds: u64,
) -> Result<(), String> {
    if worker_id == 0 || lease_seconds == 0 {
        return Err("invalid_lease".to_owned());
    }
    let candidate = ctx
        .db
        .job()
        .iter()
        .filter(|job| {
            (job.state == "pending") || (job.state == "claimed" && job.lease_until <= now)
        })
        .min_by_key(|job| job.id)
        .ok_or_else(|| "no_job_available".to_owned())?;
    if candidate.attempts >= candidate.max_attempts {
        let mut dead = candidate;
        dead.state = "dead_lettered".to_owned();
        dead.lease_until = 0;
        dead.worker_id = 0;
        ctx.db.job().id().update(dead);
        return Err("job_dead_lettered".to_owned());
    }
    let mut claimed = candidate;
    claimed.state = "claimed".to_owned();
    claimed.attempts = claimed.attempts.saturating_add(1);
    claimed.lease_until = now.saturating_add(lease_seconds);
    claimed.worker_id = worker_id;
    ctx.db.job().id().update(claimed);
    Ok(())
}

/// Completes a job only while its worker lease is valid.
#[reducer]
pub fn complete_job(ctx: &ReducerContext, id: u64, worker_id: u64, now: u64) -> Result<(), String> {
    let mut job = ctx
        .db
        .job()
        .id()
        .find(id)
        .ok_or_else(|| "job_not_found".to_owned())?;
    if job.state != "claimed" || job.worker_id != worker_id || job.lease_until <= now {
        return Err("lease_mismatch".to_owned());
    }
    job.state = "completed".to_owned();
    job.lease_until = 0;
    job.worker_id = 0;
    ctx.db.job().id().update(job);
    Ok(())
}

/// Fails a claimed job, returning it to pending or dead-lettering it.
#[reducer]
pub fn fail_job(ctx: &ReducerContext, id: u64, worker_id: u64, now: u64) -> Result<(), String> {
    let mut job = ctx
        .db
        .job()
        .id()
        .find(id)
        .ok_or_else(|| "job_not_found".to_owned())?;
    if job.state != "claimed" || job.worker_id != worker_id || job.lease_until <= now {
        return Err("lease_mismatch".to_owned());
    }
    job.state = if job.attempts >= job.max_attempts {
        "dead_lettered".to_owned()
    } else {
        "pending".to_owned()
    };
    job.lease_until = 0;
    job.worker_id = 0;
    ctx.db.job().id().update(job);
    Ok(())
}

/// Saves the singleton authentication snapshot.
#[reducer]
pub fn save_auth_snapshot(
    ctx: &ReducerContext,
    version: u16,
    payload: Vec<u8>,
) -> Result<(), String> {
    if version == 0 || payload.len() > MAX_PAYLOAD_BYTES {
        return Err("invalid_auth_snapshot".to_owned());
    }
    if ctx.db.auth_snapshot().id().find(1).is_some() {
        ctx.db.auth_snapshot().id().delete(1);
    }
    ctx.db.auth_snapshot().insert(AuthSnapshot {
        id: 1,
        version,
        payload,
    });
    Ok(())
}

/// Issues a tenant-scoped authenticated session.
#[reducer]
pub fn issue_auth_session(
    ctx: &ReducerContext,
    token: String,
    subject_id: u64,
    tenant_id: u32,
    expires_at: u64,
    mfa_verified: bool,
) -> Result<(), String> {
    if token.trim().is_empty()
        || token.len() > MAX_KIND_BYTES * 8
        || subject_id == 0
        || tenant_id == 0
        || expires_at == 0
    {
        return Err("invalid_auth_session".to_owned());
    }
    if ctx.db.auth_session().token().find(token.clone()).is_some() {
        return Err("session_exists".to_owned());
    }
    ctx.db.auth_session().insert(AuthSession {
        token,
        subject_id,
        tenant_id,
        expires_at,
        mfa_verified,
        revoked: false,
    });
    Ok(())
}

/// Revokes a session without deleting its audit record.
#[reducer]
pub fn revoke_auth_session(ctx: &ReducerContext, token: String) -> Result<(), String> {
    let mut session = ctx
        .db
        .auth_session()
        .token()
        .find(token)
        .ok_or_else(|| "session_not_found".to_owned())?;
    session.revoked = true;
    ctx.db.auth_session().token().update(session);
    Ok(())
}

/// Creates an idempotent email delivery intent.
#[reducer]
pub fn enqueue_email(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    recipient: String,
    subject: String,
    body: String,
    max_attempts: u16,
) -> Result<(), String> {
    if id == 0
        || tenant_id == 0
        || recipient.trim().is_empty()
        || subject.trim().is_empty()
        || body.trim().is_empty()
        || recipient.len() > MAX_KIND_BYTES * 2
        || subject.len() > MAX_KIND_BYTES
        || body.len() > MAX_PAYLOAD_BYTES
        || max_attempts == 0
    {
        return Err("invalid_email".to_owned());
    }
    if ctx.db.email_outbox().id().find(id).is_some() {
        return Ok(());
    }
    ctx.db.email_outbox().insert(EmailOutbox {
        id,
        tenant_id,
        recipient,
        subject,
        body,
        state: "pending".to_owned(),
        attempts: 0,
        max_attempts,
        claimed_by: 0,
    });
    Ok(())
}

/// Claims one pending email for a provider attempt.
#[reducer]
pub fn claim_email(ctx: &ReducerContext, id: u64, provider_id: u64) -> Result<(), String> {
    if provider_id == 0 {
        return Err("invalid_provider".to_owned());
    }
    let mut email = ctx
        .db
        .email_outbox()
        .id()
        .find(id)
        .ok_or_else(|| "email_not_found".to_owned())?;
    if email.state != "pending" || email.attempts >= email.max_attempts {
        return Err("email_not_claimable".to_owned());
    }
    email.state = "claimed".to_owned();
    email.attempts = email.attempts.saturating_add(1);
    email.claimed_by = provider_id;
    ctx.db.email_outbox().id().update(email);
    Ok(())
}

/// Marks a claimed email as sent when the provider owns its claim.
#[reducer]
pub fn mark_email_sent(ctx: &ReducerContext, id: u64, provider_id: u64) -> Result<(), String> {
    let mut email = ctx
        .db
        .email_outbox()
        .id()
        .find(id)
        .ok_or_else(|| "email_not_found".to_owned())?;
    if email.state != "claimed" || email.claimed_by != provider_id {
        return Err("email_claim_mismatch".to_owned());
    }
    email.state = "sent".to_owned();
    email.claimed_by = 0;
    ctx.db.email_outbox().id().update(email);
    Ok(())
}

/// Fails a claimed email, returning it to pending or dead-lettering it.
#[reducer]
pub fn fail_email(ctx: &ReducerContext, id: u64, provider_id: u64) -> Result<(), String> {
    let mut email = ctx
        .db
        .email_outbox()
        .id()
        .find(id)
        .ok_or_else(|| "email_not_found".to_owned())?;
    if email.state != "claimed" || email.claimed_by != provider_id {
        return Err("email_claim_mismatch".to_owned());
    }
    email.state = if email.attempts >= email.max_attempts {
        "failed".to_owned()
    } else {
        "pending".to_owned()
    };
    email.claimed_by = 0;
    ctx.db.email_outbox().id().update(email);
    Ok(())
}

/// Records one finite, bounded telemetry sample.
#[reducer]
pub fn record_telemetry(
    ctx: &ReducerContext,
    tenant_id: u32,
    name: String,
    value: f64,
    timestamp: u64,
) -> Result<(), String> {
    if tenant_id == 0
        || name.trim().is_empty()
        || name.len() > MAX_KIND_BYTES
        || !value.is_finite()
        || timestamp == 0
    {
        return Err("invalid_telemetry".to_owned());
    }
    let samples = ctx.db.telemetry_sample().iter().collect::<Vec<_>>();
    if samples.len() >= MAX_TELEMETRY_SAMPLES {
        if let Some(oldest) = samples.into_iter().min_by_key(|sample| sample.id) {
            ctx.db.telemetry_sample().id().delete(oldest.id);
        }
    }
    ctx.db.telemetry_sample().insert(TelemetrySample {
        id: 0,
        tenant_id,
        name,
        value,
        timestamp,
    });
    Ok(())
}

/// Registers or replaces bounded object metadata for one tenant-owned key.
#[reducer]
pub fn put_object_metadata(
    ctx: &ReducerContext,
    key: String,
    tenant_id: u32,
    size: u64,
    content_type: String,
    checksum: String,
    lifecycle: String,
) -> Result<(), String> {
    let (created_at_text, retention_until_text) = lifecycle
        .split_once(':')
        .ok_or_else(|| "invalid_object_lifecycle".to_owned())?;
    let created_at = created_at_text
        .parse::<u64>()
        .map_err(|_| "invalid_object_lifecycle".to_owned())?;
    let retention_until = retention_until_text
        .parse::<u64>()
        .map_err(|_| "invalid_object_lifecycle".to_owned())?;
    if key.trim().is_empty()
        || key.len() > MAX_KIND_BYTES * 8
        || tenant_id == 0
        || content_type.trim().is_empty()
        || content_type.len() > MAX_KIND_BYTES
        || checksum.len() > MAX_KIND_BYTES * 2
        || lifecycle.len() > MAX_KIND_BYTES
        || created_at == 0
        || (retention_until != 0 && retention_until < created_at)
    {
        return Err("invalid_object_metadata".to_owned());
    }
    if ctx.db.object_metadata().key().find(key.clone()).is_some() {
        ctx.db.object_metadata().key().delete(key.clone());
    }
    ctx.db.object_metadata().insert(ObjectMetadata {
        key,
        tenant_id,
        size,
        content_type,
        checksum,
        status: "available".to_owned(),
        created_at,
        retention_until,
    });
    Ok(())
}

/// Marks expired external object bodies for adapter cleanup.
#[reducer]
pub fn expire_object_metadata(ctx: &ReducerContext, now: u64) -> Result<(), String> {
    if now == 0 {
        return Err("invalid_object_expiration_time".to_owned());
    }
    let expired: Vec<ObjectMetadata> = ctx
        .db
        .object_metadata()
        .iter()
        .filter(|object| {
            object.status == "available"
                && object.retention_until != 0
                && object.retention_until <= now
        })
        .collect();
    for mut object in expired {
        object.status = "expired".to_owned();
        ctx.db.object_metadata().key().update(object);
    }
    Ok(())
}

/// Deletes tenant-owned metadata after an external object is removed.
#[reducer]
pub fn delete_object_metadata(
    ctx: &ReducerContext,
    key: String,
    tenant_id: u32,
) -> Result<(), String> {
    if tenant_id == 0 || !valid_text(&key, MAX_KIND_BYTES * 8, true) {
        return Err("invalid_object_metadata_delete".to_owned());
    }
    let Some(existing) = ctx.db.object_metadata().key().find(key) else {
        return Ok(());
    };
    if existing.tenant_id != tenant_id {
        return Err("object_tenant_boundary".to_owned());
    }
    ctx.db.object_metadata().key().delete(existing.key);
    Ok(())
}

/// Registers or replaces metadata for a large external artifact body.
#[expect(
    clippy::too_many_arguments,
    reason = "SpacetimeDB reducer ABI exposes each bounded column as a typed argument"
)]
#[reducer]
pub fn put_artifact_metadata(
    ctx: &ReducerContext,
    id: String,
    tenant_id: u32,
    build_id: Option<u64>,
    deployment_id: Option<u64>,
    kind: String,
    object_key: String,
    size: u64,
    checksum: String,
    content_type: String,
    status: String,
    created_at: u64,
    retention_until: u64,
) -> Result<(), String> {
    if !valid_tenant_timestamp(tenant_id, created_at)
        || !valid_text(&id, MAX_KIND_BYTES * 2, true)
        || !valid_text(&kind, MAX_KIND_BYTES, true)
        || !valid_text(&object_key, MAX_KIND_BYTES * 8, true)
        || !valid_text(&checksum, MAX_KIND_BYTES * 2, false)
        || !valid_text(&content_type, MAX_KIND_BYTES, true)
        || !valid_text(&status, MAX_KIND_BYTES, true)
    {
        return Err("invalid_artifact_metadata".to_owned());
    }
    if ctx.db.artifact_metadata().id().find(id.clone()).is_some() {
        ctx.db.artifact_metadata().id().delete(id.clone());
    }
    ctx.db.artifact_metadata().insert(ArtifactMetadata {
        id,
        tenant_id,
        build_id,
        deployment_id,
        kind,
        object_key,
        size,
        checksum,
        content_type,
        status,
        created_at,
        retention_until,
    });
    Ok(())
}

/// Deletes tenant-owned metadata after an external artifact body is removed.
#[reducer]
pub fn delete_artifact_metadata(
    ctx: &ReducerContext,
    id: String,
    tenant_id: u32,
) -> Result<(), String> {
    if tenant_id == 0 || !valid_text(&id, MAX_KIND_BYTES * 2, true) {
        return Err("invalid_artifact_metadata_delete".to_owned());
    }
    let Some(existing) = ctx.db.artifact_metadata().id().find(id) else {
        return Ok(());
    };
    if existing.tenant_id != tenant_id {
        return Err("artifact_tenant_boundary".to_owned());
    }
    ctx.db.artifact_metadata().id().delete(existing.id);
    Ok(())
}

/// Marks expired external artifact metadata for adapter cleanup.
#[reducer]
pub fn expire_artifact_metadata(ctx: &ReducerContext, now: u64) -> Result<(), String> {
    if now == 0 {
        return Err("invalid_artifact_expiration_time".to_owned());
    }
    let expired: Vec<ArtifactMetadata> = ctx
        .db
        .artifact_metadata()
        .iter()
        .filter(|artifact| {
            artifact.status == "available"
                && artifact.retention_until != 0
                && artifact.retention_until <= now
        })
        .collect();
    for mut artifact in expired {
        artifact.status = "expired".to_owned();
        ctx.db.artifact_metadata().id().update(artifact);
    }
    Ok(())
}

/// Registers searchable metadata for a build log object.
#[expect(
    clippy::too_many_arguments,
    reason = "SpacetimeDB reducer ABI exposes each bounded column as a typed argument"
)]
#[reducer]
pub fn put_build_log_index(
    ctx: &ReducerContext,
    id: u64,
    tenant_id: u32,
    build_id: u64,
    object_key: String,
    line_count: u64,
    checksum: String,
    created_at: u64,
) -> Result<(), String> {
    if id == 0
        || build_id == 0
        || !valid_tenant_timestamp(tenant_id, created_at)
        || !valid_text(&object_key, MAX_KIND_BYTES * 8, true)
        || !valid_text(&checksum, MAX_KIND_BYTES * 2, false)
    {
        return Err("invalid_build_log_index".to_owned());
    }
    if let Some(existing) = ctx.db.build_log_index().id().find(id) {
        if existing.tenant_id == tenant_id
            && existing.build_id == build_id
            && existing.object_key == object_key
            && existing.line_count == line_count
            && existing.checksum == checksum
        {
            return Ok(());
        }
        ctx.db.build_log_index().id().delete(id);
    }
    ctx.db.build_log_index().insert(BuildLogIndex {
        id,
        tenant_id,
        build_id,
        object_key,
        line_count,
        checksum,
        created_at,
    });
    Ok(())
}

/// Upserts the route projection for a deployment.
#[expect(
    clippy::too_many_arguments,
    reason = "SpacetimeDB reducer ABI exposes each bounded column as a typed argument"
)]
#[reducer]
pub fn upsert_deployment_route(
    ctx: &ReducerContext,
    id: String,
    tenant_id: u32,
    deployment_id: u64,
    hostname: String,
    path: String,
    target_port: u16,
    status: String,
    updated_at: u64,
) -> Result<(), String> {
    if deployment_id == 0
        || !valid_tenant_timestamp(tenant_id, updated_at)
        || !valid_text(&id, MAX_KIND_BYTES * 2, true)
        || !valid_text(&hostname, MAX_KIND_BYTES * 2, true)
        || !valid_text(&path, MAX_KIND_BYTES * 2, true)
        || target_port == 0
        || !valid_text(&status, MAX_KIND_BYTES, true)
    {
        return Err("invalid_deployment_route".to_owned());
    }
    if ctx.db.deployment_route().id().find(id.clone()).is_some() {
        ctx.db.deployment_route().id().delete(id.clone());
    }
    ctx.db.deployment_route().insert(DeploymentRoute {
        id,
        tenant_id,
        deployment_id,
        hostname,
        path,
        target_port,
        status,
        updated_at,
    });
    Ok(())
}

/// Upserts the latest runtime health projection.
#[reducer]
pub fn record_runtime_health(
    ctx: &ReducerContext,
    deployment_id: u64,
    tenant_id: u32,
    status: String,
    checked_at: u64,
    latency_ms: u64,
    message: String,
) -> Result<(), String> {
    if deployment_id == 0
        || !valid_tenant_timestamp(tenant_id, checked_at)
        || !valid_text(&status, MAX_KIND_BYTES, true)
        || !valid_text(&message, MAX_PAYLOAD_BYTES, false)
    {
        return Err("invalid_runtime_health".to_owned());
    }
    if ctx
        .db
        .runtime_health()
        .deployment_id()
        .find(deployment_id)
        .is_some()
    {
        ctx.db
            .runtime_health()
            .deployment_id()
            .delete(deployment_id);
    }
    ctx.db.runtime_health().insert(RuntimeHealth {
        deployment_id,
        tenant_id,
        status,
        checked_at,
        latency_ms,
        message,
    });
    Ok(())
}

/// Appends an idempotent searchable trace span.
#[expect(
    clippy::too_many_arguments,
    reason = "SpacetimeDB reducer ABI exposes each bounded column as a typed argument"
)]
#[reducer]
pub fn append_trace_span(
    ctx: &ReducerContext,
    tenant_id: u32,
    trace_id: String,
    span_id: String,
    parent_span_id: String,
    operation_id: Option<u64>,
    name: String,
    started_at: u64,
    duration_us: u64,
    status: String,
    attributes: Vec<u8>,
) -> Result<(), String> {
    if !valid_tenant_timestamp(tenant_id, started_at)
        || !valid_text(&trace_id, MAX_KIND_BYTES, true)
        || !valid_text(&span_id, MAX_KIND_BYTES, true)
        || !valid_text(&parent_span_id, MAX_KIND_BYTES, false)
        || !valid_text(&name, MAX_KIND_BYTES, true)
        || !valid_text(&status, MAX_KIND_BYTES, true)
        || attributes.len() > MAX_PAYLOAD_BYTES
    {
        return Err("invalid_trace_span".to_owned());
    }
    if let Some(existing) = ctx.db.trace_span().iter().find(|row| {
        row.tenant_id == tenant_id && row.trace_id == trace_id && row.span_id == span_id
    }) {
        if existing.name == name
            && existing.started_at == started_at
            && existing.duration_us == duration_us
        {
            return Ok(());
        }
        return Err("trace_span_idempotency_conflict".to_owned());
    }
    ctx.db.trace_span().insert(TraceSpan {
        id: 0,
        tenant_id,
        trace_id,
        span_id,
        parent_span_id,
        operation_id,
        name,
        started_at,
        duration_us,
        status,
        attributes,
    });
    Ok(())
}

/// Appends an idempotent profile index row.
#[expect(
    clippy::too_many_arguments,
    reason = "SpacetimeDB reducer ABI exposes each bounded column as a typed argument"
)]
#[reducer]
pub fn append_profile_index(
    ctx: &ReducerContext,
    tenant_id: u32,
    profile_id: String,
    object_key: String,
    profile_type: String,
    started_at: u64,
    duration_us: u64,
    checksum: String,
) -> Result<(), String> {
    if !valid_tenant_timestamp(tenant_id, started_at)
        || !valid_text(&profile_id, MAX_KIND_BYTES * 2, true)
        || !valid_text(&object_key, MAX_KIND_BYTES * 8, true)
        || !valid_text(&profile_type, MAX_KIND_BYTES, true)
        || !valid_text(&checksum, MAX_KIND_BYTES * 2, false)
    {
        return Err("invalid_profile_index".to_owned());
    }
    if ctx
        .db
        .profile_index()
        .iter()
        .any(|row| row.tenant_id == tenant_id && row.profile_id == profile_id)
    {
        return Ok(());
    }
    ctx.db.profile_index().insert(ProfileIndex {
        id: 0,
        tenant_id,
        profile_id,
        object_key,
        profile_type,
        started_at,
        duration_us,
        checksum,
    });
    Ok(())
}

/// Appends a bounded, redacted log record.
#[expect(
    clippy::too_many_arguments,
    reason = "SpacetimeDB reducer ABI exposes each bounded column as a typed argument"
)]
#[reducer]
pub fn append_log_record(
    ctx: &ReducerContext,
    tenant_id: u32,
    operation_id: Option<u64>,
    trace_id: String,
    level: String,
    target: String,
    message: String,
    timestamp: u64,
) -> Result<(), String> {
    if !valid_tenant_timestamp(tenant_id, timestamp)
        || !valid_text(&trace_id, MAX_KIND_BYTES, false)
        || !valid_text(&level, MAX_KIND_BYTES, true)
        || !valid_text(&target, MAX_KIND_BYTES, true)
        || !valid_text(&message, MAX_PAYLOAD_BYTES, true)
    {
        return Err("invalid_log_record".to_owned());
    }
    ctx.db.log_record().insert(LogRecord {
        id: 0,
        tenant_id,
        operation_id,
        trace_id,
        level,
        target,
        message,
        timestamp,
    });
    Ok(())
}

/// Appends an immutable redacted audit record.
#[expect(
    clippy::too_many_arguments,
    reason = "SpacetimeDB reducer ABI exposes each bounded column as a typed argument"
)]
#[reducer]
pub fn append_audit_record(
    ctx: &ReducerContext,
    tenant_id: u32,
    actor_id: u64,
    action: String,
    entity_type: String,
    entity_id: String,
    correlation_id: String,
    causation_id: String,
    schema_version: u16,
    timestamp: u64,
    payload: Vec<u8>,
) -> Result<(), String> {
    if !valid_tenant_timestamp(tenant_id, timestamp)
        || actor_id == 0
        || schema_version == 0
        || !valid_text(&action, MAX_KIND_BYTES, true)
        || !valid_text(&entity_type, MAX_KIND_BYTES, true)
        || !valid_text(&entity_id, MAX_KIND_BYTES * 2, true)
        || !valid_text(&correlation_id, MAX_KIND_BYTES * 2, true)
        || !valid_text(&causation_id, MAX_KIND_BYTES * 2, false)
        || payload.len() > MAX_PAYLOAD_BYTES
    {
        return Err("invalid_audit_record".to_owned());
    }
    if ctx.db.audit_record().iter().any(|row| {
        row.tenant_id == tenant_id
            && row.correlation_id == correlation_id
            && row.causation_id == causation_id
    }) {
        return Ok(());
    }
    ctx.db.audit_record().insert(AuditRecord {
        id: 0,
        tenant_id,
        actor_id,
        action,
        entity_type,
        entity_id,
        correlation_id,
        causation_id,
        schema_version,
        timestamp,
        payload,
    });
    Ok(())
}
