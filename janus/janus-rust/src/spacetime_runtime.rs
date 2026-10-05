//! Managed connection boundary for the generated `SpacetimeDB` client.

use std::sync::{mpsc, Arc};
use std::time::Duration;

use spacetimedb_sdk::{DbContext, Table};

use crate::module_bindings::{
    admit_command, append_audit_record, append_event, append_log_record, append_trace_span,
    claim_email, claim_job, claim_runner, complete_job, create_build, create_deployment,
    create_operation, create_project, delete_artifact_metadata, delete_object_metadata,
    delete_project, enqueue_email, enqueue_job, expire_artifact_metadata, expire_object_metadata,
    expire_runners, fail_email, fail_job, issue_auth_session, issue_email_otp, mark_email_sent,
    put_artifact_metadata, put_credential, put_mfa_state, put_object_metadata, reconcile_jobs,
    record_runtime_health, record_telemetry, register_runner, release_runner, revoke_auth_session,
    transition_build, transition_command, transition_deployment, transition_operation,
    update_deployment_runtime, update_project, upsert_account, upsert_membership, upsert_tenant,
    verify_email_otp, verify_password, AccountMutation, AccountTableAccess,
    ArtifactMetadataTableAccess, AuthSessionTableAccess, BuildMutation, BuildTableAccess,
    CommandRecord, CommandRecordTableAccess, CredentialMutation, DbConnection, DeploymentMutation,
    DeploymentRuntimeMutation, DeploymentTableAccess, JobTableAccess, LogRecordTableAccess,
    MembershipMutation, MembershipTableAccess, MfaMutation, ObjectMetadataTableAccess,
    OperationTableAccess, ProfileIndexTableAccess, Project, ProjectMutation, ProjectTableAccess,
    RunnerTableAccess, SubscriptionHandle, TelemetrySampleTableAccess, TenantMutation,
    TenantTableAccess, TraceSpanTableAccess,
};
use crate::{
    durable_worker::DurableJobPort, BuildWrite, DeploymentRuntimeWrite, DeploymentWrite,
    ProjectReducerPort, ProjectWrite, SpacetimeDbError, TenantId,
};

const REDUCER_RESPONSE_TIMEOUT: Duration = Duration::from_secs(5);

/// Bounded runtime health projection.
#[derive(Clone, Debug)]
pub struct RuntimeHealthWrite<'a> {
    /// Deployment identity.
    pub deployment_id: u64,
    /// Owning tenant.
    pub tenant: TenantId,
    /// Health state.
    pub status: &'a str,
    /// Probe timestamp.
    pub checked_at: u64,
    /// Probe latency.
    pub latency_ms: u64,
    /// Redacted diagnostic message.
    pub message: &'a str,
}

/// Bounded private MFA projection write.
#[derive(Clone, Debug)]
pub struct MfaStateWrite<'a> {
    /// Account identity.
    pub subject_id: u64,
    /// Whether MFA is required for the account.
    pub enabled: bool,
    /// Whether TOTP is configured.
    pub totp_enabled: bool,
    /// Whether email OTP is configured.
    pub email_otp_enabled: bool,
    /// Protected TOTP secret or provider reference.
    pub totp_secret: &'a str,
    /// Remaining recovery-code count.
    pub recovery_codes: u16,
    /// Update timestamp.
    pub updated_at: u64,
}

/// Bounded trace span projection.
#[derive(Clone, Debug)]
pub struct TraceSpanWrite<'a> {
    /// Owning tenant.
    pub tenant: TenantId,
    /// Trace identity.
    pub trace_id: &'a str,
    /// Span identity.
    pub span_id: &'a str,
    /// Parent span identity.
    pub parent_span_id: &'a str,
    /// Correlated operation.
    pub operation_id: Option<u64>,
    /// Span name.
    pub name: &'a str,
    /// Start timestamp.
    pub started_at: u64,
    /// Duration in microseconds.
    pub duration_us: u64,
    /// Span state.
    pub status: &'a str,
    /// Bounded attributes.
    pub attributes: &'a [u8],
}

/// Bounded searchable log projection.
#[derive(Clone, Debug)]
pub struct LogRecordWrite<'a> {
    /// Owning tenant.
    pub tenant: TenantId,
    /// Correlated operation.
    pub operation_id: Option<u64>,
    /// Trace identity.
    pub trace_id: &'a str,
    /// Log level.
    pub level: &'a str,
    /// Logger target.
    pub target: &'a str,
    /// Redacted message.
    pub message: &'a str,
    /// Timestamp.
    pub timestamp: u64,
}

/// Immutable redacted audit projection.
#[derive(Clone, Debug)]
pub struct AuditRecordWrite<'a> {
    /// Owning tenant.
    pub tenant: TenantId,
    /// Actor identity.
    pub actor_id: u64,
    /// Action identifier.
    pub action: &'a str,
    /// Entity category.
    pub entity_type: &'a str,
    /// Entity identity.
    pub entity_id: &'a str,
    /// Request correlation identity.
    pub correlation_id: &'a str,
    /// Causal parent identity.
    pub causation_id: &'a str,
    /// Payload schema version.
    pub schema_version: u16,
    /// Event timestamp.
    pub timestamp: u64,
    /// Redacted payload.
    pub payload: &'a [u8],
}

/// Durable metadata for one external object body.
#[derive(Clone, Debug)]
pub struct ObjectMetadataWrite<'a> {
    /// Tenant-qualified object key.
    pub key: &'a str,
    /// Owning tenant.
    pub tenant: TenantId,
    /// External body length.
    pub size: u64,
    /// Content type.
    pub content_type: &'a str,
    /// Integrity checksum.
    pub checksum: &'a str,
    /// Creation timestamp.
    pub created_at: u64,
    /// Optional retention deadline.
    pub retention_until: u64,
}

/// Durable metadata for one externally stored source or artifact projection.
#[derive(Clone, Debug)]
pub struct ArtifactMetadataWrite<'a> {
    /// Stable metadata identity.
    pub id: &'a str,
    /// Owning tenant.
    pub tenant: TenantId,
    /// Optional producing build.
    pub build_id: Option<u64>,
    /// Optional consuming deployment.
    pub deployment_id: Option<u64>,
    /// Source or artifact category.
    pub kind: &'a str,
    /// External object-store key.
    pub object_key: &'a str,
    /// External body length.
    pub size: u64,
    /// Integrity checksum.
    pub checksum: &'a str,
    /// Content type.
    pub content_type: &'a str,
    /// Lifecycle status.
    pub status: &'a str,
    /// Creation timestamp.
    pub created_at: u64,
    /// Optional retention deadline.
    pub retention_until: u64,
}

/// Durable tenant identity projection.
#[derive(Clone, Debug)]
pub struct TenantWrite<'a> {
    /// Tenant identity.
    pub id: u32,
    /// Tenant name.
    pub name: &'a str,
    /// Tenant status.
    pub status: &'a str,
    /// Creation timestamp.
    pub created_at: u64,
}

/// Durable account identity projection.
#[derive(Clone, Debug)]
pub struct AccountWrite<'a> {
    /// Account identity.
    pub id: u64,
    /// Owning tenant.
    pub tenant: TenantId,
    /// Normalized email.
    pub email: &'a str,
    /// Account status.
    pub status: &'a str,
    /// MFA requirement.
    pub mfa_required: bool,
    /// Creation timestamp.
    pub created_at: u64,
}

/// Durable authorization membership projection.
#[derive(Clone, Debug)]
pub struct MembershipWrite<'a> {
    /// Composite membership identity.
    pub id: &'a str,
    /// Account identity.
    pub subject_id: u64,
    /// Owning tenant.
    pub tenant: TenantId,
    /// Permission bitset.
    pub permissions: u32,
    /// Membership status.
    pub status: &'a str,
    /// Update timestamp.
    pub updated_at: u64,
}

/// A live connection to the authoritative Janus `SpacetimeDB` module.
///
/// The generated SDK owns the WebSocket cache and reducer transport. This
/// wrapper owns the process-level connection lifetime so HTTP handlers and
/// startup reconciliation can share one connection instead of creating
/// independent database authorities.
#[derive(Clone)]
pub struct SpacetimeRuntime {
    connection: Arc<DbConnection>,
    _subscription: SubscriptionHandle,
}

impl SpacetimeRuntime {
    /// Connects to a `SpacetimeDB` module and starts its message pump.
    ///
    /// # Errors
    ///
    /// Returns a descriptive error when the SDK cannot establish the initial
    /// WebSocket connection.
    pub fn connect(uri: &str, database: &str, token: Option<&str>) -> Result<Self, String> {
        if uri.trim().is_empty() || database.trim().is_empty() {
            return Err("SpacetimeDB URI and database are required".to_owned());
        }
        // The native SDK uses a blocking WebSocket bootstrap internally. Actix
        // runs its entry point on a single-thread Tokio runtime, where that
        // SDK path is intentionally rejected. Construct the connection on a
        // dedicated OS thread, then return the fully initialized client to the
        // Actix process.
        let uri = uri.to_owned();
        let database = database.to_owned();
        let token = token.map(str::to_owned);
        let connection = std::thread::spawn(move || {
            DbConnection::builder()
                .with_uri(&uri)
                .with_database_name(&database)
                .with_token(token.as_deref())
                .with_confirmed_reads(true)
                .build()
        })
        .join()
        .map_err(|_| "SpacetimeDB connection thread panicked".to_owned())?
        .map_err(|error| format!("SpacetimeDB connection failed: {error:?}"))?;
        let connection = Arc::new(connection);
        let (applied_sender, applied_receiver) = mpsc::sync_channel(1);
        let subscription = connection
            .subscription_builder()
            .on_applied(move |_| {
                if applied_sender.send(()).is_err() {
                    // The bounded startup waiter has already timed out.
                }
            })
            .subscribe_to_all_tables();
        let pump = Arc::clone(&connection);
        std::thread::spawn(move || {
            let handle = pump.run_threaded();
            if handle.join().is_err() {
                eprintln!("SpacetimeDB message pump terminated unexpectedly");
            }
        });
        applied_receiver
            .recv_timeout(REDUCER_RESPONSE_TIMEOUT)
            .map_err(|_| "SpacetimeDB initial subscription timed out".to_owned())?;
        Ok(Self {
            connection,
            _subscription: subscription,
        })
    }

    /// Returns whether the generated SDK connection is currently active.
    #[must_use]
    pub fn is_active(&self) -> bool {
        self.connection.is_active()
    }

    /// Provides the generated client for typed reducers and subscriptions.
    #[must_use]
    pub fn connection(&self) -> &DbConnection {
        &self.connection
    }

    /// Returns the currently materialized project rows from the subscribed
    /// `SpacetimeDB` client cache.
    #[must_use]
    pub fn projects(&self) -> Vec<Project> {
        self.connection.db.project().iter().collect()
    }

    /// Projects the subscribed durable rows into validated ECS components for
    /// startup hydration.
    #[must_use]
    pub fn project_components(&self) -> Vec<crate::Project> {
        self.projects()
            .into_iter()
            .map(|project| crate::Project {
                id: crate::EntityId(project.id),
                tenant: TenantId(project.tenant_id),
                name: project.name,
                slug: project.slug,
                description: project.description,
                status: project.status,
                repo_provider: project.repo_provider,
                repo_url: project.repository,
                repo_branch: project.branch,
                repo_check: None,
                created_at: project.created_at,
                updated_at: project.updated_at,
            })
            .collect()
    }

    /// Projects subscribed durable build rows into ECS components.
    ///
    /// # Errors
    ///
    /// Returns an error when a durable lifecycle status is unknown.
    pub fn build_components(&self) -> Result<Vec<(crate::EntityId, crate::Build)>, String> {
        self.connection
            .db
            .build()
            .iter()
            .map(|build| {
                let state = match build.status.as_str() {
                    "pending" => crate::BuildState::Pending,
                    "running" => crate::BuildState::Running,
                    "succeeded" => crate::BuildState::Succeeded,
                    "failed" => crate::BuildState::Failed,
                    "cancelled" => crate::BuildState::Cancelled,
                    _ => return Err("invalid durable build status".to_owned()),
                };
                Ok((
                    crate::EntityId(build.id),
                    crate::Build {
                        tenant: TenantId(build.tenant_id),
                        state,
                        generation: crate::Generation(build.generation),
                        project: build.project_id.map(crate::EntityId),
                        source_ref: Some(build.source_ref),
                    },
                ))
            })
            .collect()
    }

    /// Projects subscribed durable deployment rows into ECS components.
    ///
    /// # Errors
    ///
    /// Returns an error when a durable lifecycle status or environment is
    /// malformed.
    pub fn deployment_components(
        &self,
    ) -> Result<Vec<(crate::EntityId, crate::Deployment)>, String> {
        self.connection
            .db
            .deployment()
            .iter()
            .map(|deployment| {
                let state = match deployment.status.as_str() {
                    "pending" => crate::DeploymentState::Pending,
                    "starting" => crate::DeploymentState::Starting,
                    "running" => crate::DeploymentState::Running,
                    "failed" => crate::DeploymentState::Failed,
                    "stopped" => crate::DeploymentState::Stopped,
                    _ => return Err("invalid durable deployment status".to_owned()),
                };
                let environment = serde_json::from_slice(&deployment.environment)
                    .map_err(|_| "invalid durable deployment environment".to_owned())?;
                Ok((
                    crate::EntityId(deployment.id),
                    crate::Deployment {
                        tenant: TenantId(deployment.tenant_id),
                        build: crate::EntityId(deployment.build_id),
                        project: deployment.project_id.map(crate::EntityId),
                        revision: deployment.revision,
                        target_type: deployment.target_type,
                        target_ref: deployment.target_ref,
                        preferred_runner: deployment.preferred_runner,
                        environment,
                        runtime_id: deployment.runtime_id,
                        runtime_mode: deployment.runtime_mode,
                        runtime_endpoint: deployment.runtime_endpoint,
                        runtime_status: deployment.runtime_status,
                        state,
                        generation: crate::Generation(deployment.generation),
                        resource_claim: None,
                    },
                ))
            })
            .collect()
    }

    /// Projects subscribed durable runner rows into lease-aware registry rows.
    ///
    /// # Errors
    ///
    /// Returns an error when a durable runner status is unknown.
    pub fn runner_components(&self) -> Result<Vec<crate::Runner>, String> {
        self.connection
            .db
            .runner()
            .iter()
            .map(|runner| {
                let state = match runner.status.as_str() {
                    "available" => crate::RunnerState::Available,
                    "busy" => crate::RunnerState::Busy,
                    "draining" => crate::RunnerState::Draining,
                    "offline" => crate::RunnerState::Offline,
                    _ => return Err("invalid durable runner status".to_owned()),
                };
                Ok(crate::Runner {
                    id: runner.id,
                    tenant: TenantId(runner.tenant_id),
                    state,
                    capabilities: runner.capabilities,
                    last_heartbeat: runner.heartbeat_at,
                    lease_until: runner.lease_until,
                })
            })
            .collect()
    }

    /// Registers or refreshes one tenant-owned runner through `SpacetimeDB`.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer rejects the runner contract.
    pub fn register_runner(
        &self,
        id: u64,
        tenant: TenantId,
        capabilities: &[String],
        heartbeat_at: u64,
        lease_seconds: u64,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let capabilities = capabilities.to_vec();
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.register_runner_then(
                id,
                tenant.0,
                capabilities,
                heartbeat_at,
                lease_seconds,
                callback,
            )
        }))
    }

    /// Claims one available tenant runner by capability.
    ///
    /// # Errors
    ///
    /// Returns an error when no matching runner is available.
    pub fn claim_runner(
        &self,
        tenant: TenantId,
        capability: &str,
        now: u64,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let capability = capability.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .claim_runner_then(tenant.0, capability, now, callback)
        }))
    }

    /// Releases a busy tenant runner through `SpacetimeDB`.
    ///
    /// # Errors
    ///
    /// Returns an error when the runner is not owned by the tenant.
    pub fn release_runner(&self, id: u64, tenant: TenantId) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .release_runner_then(id, tenant.0, callback)
        }))
    }

    /// Marks expired runner leases offline through the authoritative reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer cannot be reached.
    pub fn expire_runners(&self, now: u64) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.expire_runners_then(now, callback)
        }))
    }

    /// Projects subscribed durable operation rows into the HTTP operation
    /// registry representation.
    ///
    /// # Errors
    ///
    /// Returns an error when a durable operation status or payload is invalid.
    pub fn operation_components(&self) -> Result<Vec<crate::Operation>, String> {
        self.connection
            .db
            .operation()
            .iter()
            .map(|operation| {
                let status = match operation.status.as_str() {
                    "pending" => crate::OperationStatus::Pending,
                    "processing" => crate::OperationStatus::Processing,
                    "succeeded" => crate::OperationStatus::Succeeded,
                    "failed" => crate::OperationStatus::Failed,
                    "dead_lettered" => crate::OperationStatus::DeadLettered,
                    "expired" => crate::OperationStatus::Expired,
                    _ => return Err("invalid durable operation status".to_owned()),
                };
                let result = if operation.result.is_empty() {
                    None
                } else {
                    Some(
                        String::from_utf8(operation.result)
                            .map_err(|_| "invalid operation result".to_owned())?,
                    )
                };
                let failure = if operation.failure.is_empty() {
                    None
                } else {
                    Some(
                        String::from_utf8(operation.failure)
                            .map_err(|_| "invalid operation failure".to_owned())?,
                    )
                };
                Ok(crate::Operation {
                    id: operation.correlation_id.clone(),
                    kind: operation.kind,
                    correlation_id: operation.correlation_id,
                    tenant_id: operation.tenant_id.to_string(),
                    status,
                    created_at: operation.created_at,
                    updated_at: operation.updated_at,
                    result,
                    failure,
                })
            })
            .collect()
    }

    /// Finds one durable operation by its stable correlation identity.
    #[must_use]
    pub fn operation_by_correlation(&self, correlation_id: &str) -> Option<crate::Operation> {
        self.connection
            .db
            .operation()
            .iter()
            .find(|operation| operation.correlation_id == correlation_id)
            .and_then(|operation| operation_component(operation, correlation_id).ok())
    }

    /// Creates one durable operation projection.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation reducer rejects the bounded row.
    pub fn create_operation(
        &self,
        id: u64,
        tenant: TenantId,
        kind: &str,
        correlation_id: &str,
        created_at: u64,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let kind = kind.to_owned();
        let correlation_id = correlation_id.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.create_operation_then(
                id,
                tenant.0,
                kind,
                correlation_id,
                created_at,
                callback,
            )
        }))
    }

    /// Applies one durable operation lifecycle transition.
    ///
    /// # Errors
    ///
    /// Returns an error when the transition is stale or rejected.
    pub fn transition_operation(
        &self,
        id: u64,
        tenant: TenantId,
        status: &str,
        payload: &[u8],
        updated_at: u64,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let status = status.to_owned();
        let payload = payload.to_vec();
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .transition_operation_then(id, tenant.0, status, payload, updated_at, callback)
        }))
    }

    /// Persists durable metadata for an external object body.
    ///
    /// # Errors
    ///
    /// Returns an error when the metadata reducer rejects the tenant-owned
    /// object projection.
    pub fn put_object_metadata(
        &self,
        write: &ObjectMetadataWrite<'_>,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let key = write.key.to_owned();
        let tenant = write.tenant;
        let size = write.size;
        let content_type = write.content_type.to_owned();
        let checksum = write.checksum.to_owned();
        let lifecycle = format!("{}:{}", write.created_at, write.retention_until);
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.put_object_metadata_then(
                key,
                tenant.0,
                size,
                content_type,
                checksum,
                lifecycle,
                callback,
            )
        }))
    }

    /// Persists durable metadata for one external source or artifact body.
    ///
    /// # Errors
    ///
    /// Returns an error when the metadata reducer rejects the projection.
    pub fn put_artifact_metadata(
        &self,
        write: &ArtifactMetadataWrite<'_>,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let id = write.id.to_owned();
        let tenant_id = write.tenant.0;
        let build_id = write.build_id;
        let deployment_id = write.deployment_id;
        let kind = write.kind.to_owned();
        let object_key = write.object_key.to_owned();
        let size = write.size;
        let checksum = write.checksum.to_owned();
        let content_type = write.content_type.to_owned();
        let status = write.status.to_owned();
        let created_at = write.created_at;
        let retention_until = write.retention_until;
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.put_artifact_metadata_then(
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
                callback,
            )
        }))
    }

    /// Removes tenant-owned artifact metadata after external-body cleanup.
    ///
    /// # Errors
    ///
    /// Returns an error when the tenant boundary or reducer call fails.
    pub fn delete_artifact_metadata(
        &self,
        id: &str,
        tenant: TenantId,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let id = id.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .delete_artifact_metadata_then(id, tenant.0, callback)
        }))
    }

    /// Marks expired artifact metadata for external-body cleanup.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer rejects the timestamp.
    pub fn expire_artifact_metadata(&self, now: u64) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .expire_artifact_metadata_then(now, callback)
        }))
    }

    /// Removes tenant-owned object metadata after external-body deletion.
    ///
    /// # Errors
    ///
    /// Returns an error when the tenant boundary or reducer contract fails.
    pub fn delete_object_metadata(
        &self,
        key: &str,
        tenant: TenantId,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let key = key.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .delete_object_metadata_then(key, tenant.0, callback)
        }))
    }

    /// Marks expired durable object metadata for external cleanup.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer rejects the timestamp.
    pub fn expire_object_metadata(&self, now: u64) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .expire_object_metadata_then(now, callback)
        }))
    }

    /// Returns materialized durable object metadata rows.
    #[must_use]
    pub fn object_metadata(&self) -> Vec<crate::module_bindings::ObjectMetadata> {
        self.connection.db.object_metadata().iter().collect()
    }

    /// Returns materialized durable source and artifact metadata rows.
    #[must_use]
    pub fn artifact_metadata(&self) -> Vec<crate::module_bindings::ArtifactMetadata> {
        self.connection.db.artifact_metadata().iter().collect()
    }

    /// Returns materialized public tenant rows.
    #[must_use]
    pub fn tenants(&self) -> Vec<crate::module_bindings::Tenant> {
        self.connection.db.tenant().iter().collect()
    }

    /// Returns materialized public account rows.
    #[must_use]
    pub fn accounts(&self) -> Vec<crate::module_bindings::Account> {
        self.connection.db.account().iter().collect()
    }

    /// Returns materialized public membership rows.
    #[must_use]
    pub fn memberships(&self) -> Vec<crate::module_bindings::Membership> {
        self.connection.db.membership().iter().collect()
    }

    /// Returns materialized public authenticated-session rows.
    #[must_use]
    pub fn auth_sessions(&self) -> Vec<crate::module_bindings::AuthSession> {
        self.connection.db.auth_session().iter().collect()
    }

    /// Returns one materialized durable command record.
    #[must_use]
    pub fn command(&self, id: u64) -> Option<CommandRecord> {
        self.connection.db.command_record().id().find(&id)
    }

    /// Finds a durable HTTP command by its stable operation identity.
    #[must_use]
    pub fn command_for_operation(&self, operation_id: &str) -> Option<CommandRecord> {
        self.connection.db.command_record().iter().find(|command| {
            let Some((key, _fingerprint)) = command.correlation_id.split_once(':') else {
                return false;
            };
            format!("command-{}-{key}", command.kind) == operation_id
        })
    }

    /// Returns the materialized durable worker queue.
    #[must_use]
    pub fn jobs(&self) -> Vec<crate::module_bindings::Job> {
        self.connection.db.job().iter().collect()
    }

    /// Returns the bounded durable telemetry projection.
    #[must_use]
    pub fn telemetry_samples(&self) -> Vec<crate::module_bindings::TelemetrySample> {
        self.connection.db.telemetry_sample().iter().collect()
    }

    /// Returns materialized trace-span index rows.
    #[must_use]
    pub fn trace_spans(&self) -> Vec<crate::module_bindings::TraceSpan> {
        self.connection.db.trace_span().iter().collect()
    }

    /// Returns materialized searchable log rows.
    #[must_use]
    pub fn log_records(&self) -> Vec<crate::module_bindings::LogRecord> {
        self.connection.db.log_record().iter().collect()
    }

    /// Returns materialized profile index rows.
    #[must_use]
    pub fn profile_indexes(&self) -> Vec<crate::module_bindings::ProfileIndex> {
        self.connection.db.profile_index().iter().collect()
    }

    /// Records one bounded tenant-scoped telemetry sample through `SpacetimeDB`.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer rejects the sample or the connection
    /// cannot deliver it.
    pub fn record_telemetry(
        &self,
        tenant: TenantId,
        name: &str,
        value: f64,
        timestamp: u64,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let name = name.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .record_telemetry_then(tenant.0, name, value, timestamp, callback)
        }))
    }

    /// Replaces the private durable MFA projection for one account.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer rejects the bounded MFA state or the
    /// connection cannot deliver it.
    pub fn put_mfa_state(&self, write: &MfaStateWrite<'_>) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let subject_id = write.subject_id;
        let input = MfaMutation {
            enabled: write.enabled,
            totp_enabled: write.totp_enabled,
            email_otp_enabled: write.email_otp_enabled,
            totp_secret: write.totp_secret.to_owned(),
            recovery_codes: write.recovery_codes,
            updated_at: write.updated_at,
        };
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .put_mfa_state_then(subject_id, input, callback)
        }))
    }

    /// Replaces the private durable email OTP challenge for one account.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer rejects the bounded challenge.
    pub fn issue_email_otp(
        &self,
        subject_id: u64,
        code_hash: &[u8],
        expires_at: u64,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let code_hash = code_hash.to_vec();
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .issue_email_otp_then(subject_id, code_hash, expires_at, callback)
        }))
    }

    /// Verifies and consumes a private durable email OTP challenge.
    ///
    /// # Errors
    ///
    /// Returns an error when the code is invalid, expired, or cannot be
    /// delivered to the reducer.
    pub fn verify_email_otp(
        &self,
        subject_id: u64,
        code_hash: &[u8],
        now: u64,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let code_hash = code_hash.to_vec();
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .verify_email_otp_then(subject_id, code_hash, now, callback)
        }))
    }

    /// Enqueues a tenant-scoped durable email delivery intent.
    ///
    /// # Errors
    ///
    /// Returns an error when the bounded outbox reducer rejects the message.
    pub fn enqueue_email(
        &self,
        id: u64,
        tenant: TenantId,
        recipient: &str,
        subject: &str,
        body: &str,
        max_attempts: u16,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let recipient = recipient.to_owned();
        let subject = subject.to_owned();
        let body = body.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.enqueue_email_then(
                id,
                tenant.0,
                recipient,
                subject,
                body,
                max_attempts,
                callback,
            )
        }))
    }

    /// Claims one durable email for a provider attempt.
    ///
    /// # Errors
    ///
    /// Returns an error when the provider claim is rejected.
    pub fn claim_email(&self, id: u64, provider_id: u64) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .claim_email_then(id, provider_id, callback)
        }))
    }

    /// Marks one provider-owned email as sent.
    ///
    /// # Errors
    ///
    /// Returns an error when the provider does not own the claim.
    pub fn mark_email_sent(&self, id: u64, provider_id: u64) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .mark_email_sent_then(id, provider_id, callback)
        }))
    }

    /// Returns one claimed email to pending or failed state.
    ///
    /// # Errors
    ///
    /// Returns an error when the provider does not own the claim.
    pub fn fail_email(&self, id: u64, provider_id: u64) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .fail_email_then(id, provider_id, callback)
        }))
    }

    /// Admits generation-fenced work to the authoritative worker queue.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer rejects the bounded queue contract or
    /// the connection cannot deliver the reducer call.
    pub fn enqueue_job(
        &self,
        id: u64,
        kind: &str,
        target_id: u64,
        tenant: TenantId,
        generation: u64,
        max_attempts: u16,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let kind = kind.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.enqueue_job_then(
                id,
                kind,
                target_id,
                u64::from(tenant.0),
                generation,
                max_attempts,
                callback,
            )
        }))
    }

    /// Completes a claimed durable worker job under its lease.
    ///
    /// # Errors
    ///
    /// Returns an error when the lease is stale or the reducer cannot be
    /// reached.
    pub fn complete_job(&self, id: u64, worker_id: u64, now: u64) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .complete_job_then(id, worker_id, now, callback)
        }))
    }

    /// Applies a generation-fenced build lifecycle transition.
    ///
    /// # Errors
    ///
    /// Returns an error when the build is missing, stale, or the reducer cannot
    /// be reached.
    pub fn transition_build(
        &self,
        id: u64,
        tenant: TenantId,
        generation: u64,
        status: &str,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let status = status.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .transition_build_then(id, tenant.0, generation, status, callback)
        }))
    }

    /// Creates a durable build projection before local cache refresh.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer rejects the bounded build payload.
    pub fn create_build(
        &self,
        id: u64,
        tenant: TenantId,
        build: BuildWrite,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let mutation = BuildMutation {
            source: build.source,
            project_id: build.project_id,
            source_ref: build.source_ref,
        };
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .create_build_then(id, tenant.0, mutation, callback)
        }))
    }

    /// Creates a durable deployment projection before local cache refresh.
    ///
    /// # Errors
    ///
    /// Returns an error when the environment cannot be encoded or the reducer
    /// rejects the bounded deployment payload.
    pub fn create_deployment(
        &self,
        id: u64,
        tenant: TenantId,
        build_id: u64,
        deployment: DeploymentWrite,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let environment = deployment.environment;
        let mutation = DeploymentMutation {
            project_id: deployment.project_id,
            revision: deployment.revision,
            target_type: deployment.target_type,
            target_ref: deployment.target_ref,
            preferred_runner: deployment.preferred_runner,
            environment,
            runtime_id: deployment.runtime_id,
            runtime_mode: deployment.runtime_mode,
            runtime_endpoint: deployment.runtime_endpoint,
            runtime_status: deployment.runtime_status,
        };
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .create_deployment_then(id, tenant.0, build_id, mutation, callback)
        }))
    }

    /// Applies a generation-fenced deployment lifecycle transition.
    ///
    /// # Errors
    ///
    /// Returns an error when the deployment state transition is invalid.
    pub fn transition_deployment(
        &self,
        id: u64,
        tenant: TenantId,
        generation: u64,
        status: &str,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let status = status.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .transition_deployment_then(id, tenant.0, generation, status, callback)
        }))
    }

    /// Applies a generation-fenced deployment runtime projection.
    ///
    /// # Errors
    ///
    /// Returns an error when the deployment runtime transition is invalid.
    pub fn update_deployment_runtime(
        &self,
        id: u64,
        tenant: TenantId,
        generation: u64,
        status: &str,
        runtime: DeploymentRuntimeWrite,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let status = status.to_owned();
        let runtime = DeploymentRuntimeMutation {
            runtime_id: runtime.runtime_id,
            runtime_mode: runtime.runtime_mode,
            runtime_endpoint: runtime.runtime_endpoint,
            runtime_status: runtime.runtime_status,
        };
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .update_deployment_runtime_then(id, tenant.0, generation, status, runtime, callback)
        }))
    }

    /// Claims the oldest available durable job and returns its materialized
    /// leased row.
    ///
    /// # Errors
    ///
    /// Returns an error when no job is available, the lease is invalid, or the
    /// reducer cannot be reached.
    pub fn claim_job(
        &self,
        worker_id: u64,
        now: u64,
        lease_seconds: u64,
    ) -> Result<Option<crate::module_bindings::Job>, SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .claim_job_then(worker_id, now, lease_seconds, callback)
        }))?;
        Ok(self
            .jobs()
            .into_iter()
            .find(|job| job.state == "claimed" && job.worker_id == worker_id))
    }

    /// Returns a claimed durable worker job to pending or dead-lettered state.
    ///
    /// # Errors
    ///
    /// Returns an error when the lease is stale or the reducer cannot be
    /// reached.
    pub fn fail_job(&self, id: u64, worker_id: u64, now: u64) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .fail_job_then(id, worker_id, now, callback)
        }))
    }

    /// Reclaims expired durable worker leases through the authoritative reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer rejects the timestamp or cannot be
    /// reached.
    pub fn reconcile_jobs(&self, now: u64) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.reconcile_jobs_then(now, callback)
        }))
    }

    /// Admits an idempotent HTTP command through the authoritative reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer rejects the command or the connection
    /// cannot deliver it.
    pub fn admit_command(
        &self,
        id: u64,
        tenant: TenantId,
        kind: &str,
        correlation_id: &str,
        payload: &[u8],
        created_at: u64,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let kind = kind.to_owned();
        let correlation_id = correlation_id.to_owned();
        let payload = payload.to_vec();
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.admit_command_then(
                id,
                tenant.0,
                kind,
                correlation_id,
                payload,
                created_at,
                callback,
            )
        }))
    }

    /// Commits an idempotent HTTP command result through the authoritative
    /// reducer.
    ///
    /// # Errors
    ///
    /// Returns an error when the lifecycle transition is stale or rejected.
    pub fn transition_command(
        &self,
        id: u64,
        tenant: TenantId,
        status: &str,
        payload: &[u8],
        updated_at: u64,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let status = status.to_owned();
        let payload = payload.to_vec();
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .transition_command_then(id, tenant.0, status, payload, updated_at, callback)
        }))
    }

    /// Persists a normalized tenant projection.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer cannot be reached or rejects the
    /// bounded projection.
    pub fn upsert_tenant(&self, write: &TenantWrite<'_>) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let id = write.id;
        let input = TenantMutation {
            name: write.name.to_owned(),
            status: write.status.to_owned(),
            created_at: write.created_at,
        };
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.upsert_tenant_then(id, input, callback)
        }))
    }

    /// Persists a normalized account projection.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer cannot be reached or rejects the
    /// bounded projection.
    pub fn upsert_account(&self, write: &AccountWrite<'_>) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let id = write.id;
        let input = AccountMutation {
            tenant_id: write.tenant.0,
            email: write.email.to_owned(),
            status: write.status.to_owned(),
            mfa_required: write.mfa_required,
            created_at: write.created_at,
        };
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.upsert_account_then(id, input, callback)
        }))
    }

    /// Persists a tenant authorization membership projection.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer cannot be reached or rejects the
    /// bounded projection.
    pub fn upsert_membership(&self, write: &MembershipWrite<'_>) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let id = write.id.to_owned();
        let subject_id = write.subject_id;
        let tenant_id = write.tenant.0;
        let permissions = write.permissions;
        let updated_at = write.updated_at;
        let status = write.status.to_owned();
        let input = MembershipMutation {
            subject_id,
            tenant_id,
            permissions,
            status,
            updated_at,
        };
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .upsert_membership_then(id, input, callback)
        }))
    }

    /// Persists private password-verifier material for an account.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer cannot be reached or rejects the
    /// bounded credential payload.
    pub fn put_credential(
        &self,
        subject_id: u64,
        password_hash: &str,
        version: u32,
        updated_at: u64,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let password_hash = password_hash.to_owned();
        let input = CredentialMutation {
            password_hash,
            version,
            updated_at,
        };
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .put_credential_then(subject_id, input, callback)
        }))
    }

    /// Verifies a password inside the private `SpacetimeDB` credential boundary.
    ///
    /// # Errors
    ///
    /// Returns a generic authentication error when the reducer rejects the
    /// credential or the connection cannot deliver the request.
    pub fn verify_password(&self, subject_id: u64, password: &str) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let password = password.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .verify_password_then(subject_id, password, callback)
        }))
    }

    /// Persists an authenticated session in the authoritative session table.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer cannot be reached or rejects the
    /// session identity or tenant boundary.
    pub fn issue_auth_session(
        &self,
        token: &str,
        subject_id: u64,
        tenant: TenantId,
        expires_at: u64,
        mfa_verified: bool,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let token = token.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.issue_auth_session_then(
                token,
                subject_id,
                tenant.0,
                expires_at,
                mfa_verified,
                callback,
            )
        }))
    }

    /// Revokes an authoritative session.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer cannot be reached or the session is
    /// not present.
    pub fn revoke_auth_session(&self, token: &str) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let token = token.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .revoke_auth_session_then(token, callback)
        }))
    }

    /// Records a runtime health projection in the authoritative database.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer cannot be reached or rejects the
    /// bounded projection.
    pub fn record_runtime_health(
        &self,
        write: &RuntimeHealthWrite<'_>,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let deployment_id = write.deployment_id;
        let tenant_id = write.tenant.0;
        let checked_at = write.checked_at;
        let latency_ms = write.latency_ms;
        let status = write.status.to_owned();
        let message = write.message.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.record_runtime_health_then(
                deployment_id,
                tenant_id,
                status,
                checked_at,
                latency_ms,
                message,
                callback,
            )
        }))
    }

    /// Appends a trace span index row.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer cannot be reached or rejects the
    /// bounded projection.
    pub fn append_trace_span(&self, write: &TraceSpanWrite<'_>) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let tenant_id = write.tenant.0;
        let operation_id = write.operation_id;
        let started_at = write.started_at;
        let duration_us = write.duration_us;
        let attributes = write.attributes.to_vec();
        let trace_id = write.trace_id.to_owned();
        let span_id = write.span_id.to_owned();
        let parent_span_id = write.parent_span_id.to_owned();
        let name = write.name.to_owned();
        let status = write.status.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.append_trace_span_then(
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
                callback,
            )
        }))
    }

    /// Appends a searchable log record.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer cannot be reached or rejects the
    /// bounded projection.
    pub fn append_log_record(&self, write: &LogRecordWrite<'_>) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let tenant_id = write.tenant.0;
        let operation_id = write.operation_id;
        let timestamp = write.timestamp;
        let trace_id = write.trace_id.to_owned();
        let level = write.level.to_owned();
        let target = write.target.to_owned();
        let message = write.message.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.append_log_record_then(
                tenant_id,
                operation_id,
                trace_id,
                level,
                target,
                message,
                timestamp,
                callback,
            )
        }))
    }

    /// Appends a redacted audit record.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer cannot be reached or rejects the
    /// bounded projection.
    pub fn append_audit_record(
        &self,
        write: &AuditRecordWrite<'_>,
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let tenant_id = write.tenant.0;
        let actor_id = write.actor_id;
        let schema_version = write.schema_version;
        let timestamp = write.timestamp;
        let payload = write.payload.to_vec();
        let action = write.action.to_owned();
        let entity_type = write.entity_type.to_owned();
        let entity_id = write.entity_id.to_owned();
        let correlation_id = write.correlation_id.to_owned();
        let causation_id = write.causation_id.to_owned();
        wait_for_project_reducer(Box::new(move |callback| {
            connection.reducers.append_audit_record_then(
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
                callback,
            )
        }))
    }

    /// Appends one idempotent tenant-scoped lifecycle event.
    ///
    /// # Errors
    ///
    /// Returns an error when the reducer rejects the bounded event or the
    /// connection cannot deliver it.
    pub fn append_event(
        &self,
        tenant: TenantId,
        command_id: u64,
        kind: &str,
        payload: &[u8],
    ) -> Result<(), SpacetimeDbError> {
        let connection = Arc::clone(&self.connection);
        let kind = kind.to_owned();
        let payload = payload.to_vec();
        wait_for_project_reducer(Box::new(move |callback| {
            connection
                .reducers
                .append_event_then(tenant.0, command_id, kind, payload, callback)
        }))
    }
}

fn project_mutation(project: ProjectWrite) -> ProjectMutation {
    ProjectMutation {
        name: project.name,
        slug: project.slug,
        description: project.description,
        repo_provider: project.repo_provider,
        repository: project.repository,
        branch: project.branch,
        created_at: project.created_at,
        updated_at: project.updated_at,
    }
}

fn operation_component(
    operation: crate::module_bindings::Operation,
    id: &str,
) -> Result<crate::Operation, String> {
    let status = match operation.status.as_str() {
        "pending" => crate::OperationStatus::Pending,
        "processing" => crate::OperationStatus::Processing,
        "succeeded" => crate::OperationStatus::Succeeded,
        "failed" => crate::OperationStatus::Failed,
        "dead_lettered" => crate::OperationStatus::DeadLettered,
        "expired" => crate::OperationStatus::Expired,
        _ => return Err("invalid durable operation status".to_owned()),
    };
    let result = if operation.result.is_empty() {
        None
    } else {
        Some(
            String::from_utf8(operation.result)
                .map_err(|_| "invalid operation result".to_owned())?,
        )
    };
    let failure = if operation.failure.is_empty() {
        None
    } else {
        Some(
            String::from_utf8(operation.failure)
                .map_err(|_| "invalid operation failure".to_owned())?,
        )
    };
    Ok(crate::Operation {
        id: id.to_owned(),
        kind: operation.kind,
        correlation_id: operation.correlation_id,
        tenant_id: operation.tenant_id.to_string(),
        status,
        created_at: operation.created_at,
        updated_at: operation.updated_at,
        result,
        failure,
    })
}

fn wait_for_project_reducer<F>(send: F) -> Result<(), SpacetimeDbError>
where
    F: FnOnce(
        Box<
            dyn FnOnce(
                    &crate::module_bindings::ReducerEventContext,
                    Result<Result<(), String>, spacetimedb_sdk::__codegen::InternalError>,
                ) + Send,
        >,
    ) -> spacetimedb_sdk::Result<()>,
{
    let (sender, receiver) = mpsc::sync_channel(1);
    send(Box::new(move |_, result| {
        let result = match result {
            Ok(Ok(())) => Ok(()),
            Ok(Err(_)) | Err(_) => Err(SpacetimeDbError::InvalidInput),
        };
        if sender.send(result).is_err() {
            // The bounded waiter timed out or the caller was dropped.
        }
    }))
    .map_err(|_| SpacetimeDbError::Unavailable)?;
    receiver
        .recv_timeout(REDUCER_RESPONSE_TIMEOUT)
        .map_err(|_| SpacetimeDbError::Unavailable)?
}

impl ProjectReducerPort for SpacetimeRuntime {
    fn create_project(
        &mut self,
        id: u64,
        tenant: TenantId,
        project: ProjectWrite,
    ) -> Result<(), SpacetimeDbError> {
        let input = project_mutation(project);
        wait_for_project_reducer(Box::new({
            let connection = Arc::clone(&self.connection);
            move |callback| {
                connection
                    .reducers
                    .create_project_then(id, tenant.0, input, callback)
            }
        }))
    }

    fn update_project(
        &mut self,
        id: u64,
        tenant: TenantId,
        project: ProjectWrite,
        status: &str,
    ) -> Result<(), SpacetimeDbError> {
        let input = project_mutation(project);
        let status = status.to_owned();
        wait_for_project_reducer(Box::new({
            let connection = Arc::clone(&self.connection);
            move |callback| {
                connection
                    .reducers
                    .update_project_then(id, tenant.0, status, input, callback)
            }
        }))
    }

    fn delete_project(&mut self, id: u64, tenant: TenantId) -> Result<(), SpacetimeDbError> {
        wait_for_project_reducer(Box::new({
            let connection = Arc::clone(&self.connection);
            move |callback| {
                connection
                    .reducers
                    .delete_project_then(id, tenant.0, callback)
            }
        }))
    }
}

impl DurableJobPort for SpacetimeRuntime {
    fn claim_job(
        &self,
        worker_id: u64,
        now: u64,
        lease_seconds: u64,
    ) -> Result<Option<crate::module_bindings::Job>, SpacetimeDbError> {
        Self::claim_job(self, worker_id, now, lease_seconds)
    }

    fn complete_job(&self, id: u64, worker_id: u64, now: u64) -> Result<(), SpacetimeDbError> {
        Self::complete_job(self, id, worker_id, now)
    }

    fn fail_job(&self, id: u64, worker_id: u64, now: u64) -> Result<(), SpacetimeDbError> {
        Self::fail_job(self, id, worker_id, now)
    }
}

#[cfg(test)]
mod tests {
    use super::SpacetimeRuntime;

    #[test]
    fn connection_configuration_rejects_empty_values_before_network_io() {
        assert!(SpacetimeRuntime::connect("", "janus", None).is_err());
        assert!(SpacetimeRuntime::connect("ws://127.0.0.1", "", None).is_err());
    }
}
