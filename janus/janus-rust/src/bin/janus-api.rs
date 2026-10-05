//! Minimal Rust HTTP entry point for the staged Janus migration.
#![expect(
    clippy::future_not_send,
    reason = "Actix's !Send futures are intentional at this boundary."
)]

use std::collections::HashMap;
use std::io;
use std::path::Path;
use std::sync::Mutex;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use actix_web::{cookie::Cookie, web, App, HttpRequest, HttpResponse, HttpServer};
use base64::{engine::general_purpose::STANDARD_NO_PAD, Engine};
use futures_util::StreamExt;
use janus_core::durable_worker::{self, DeterministicBuildJobHandler, WorkerPoll};
use janus_core::{
    bearer_token, AccountWrite, ArtifactMetadataWrite, AuditRecordWrite, AuthDirectory,
    BcryptPasswordVerifier, BuildService, BuildServiceError, BuildWrite, DeploymentMetadata,
    DeploymentRuntimeWrite, DeploymentWrite, EmailOtpStore, EntityId, FileAuthStore,
    HttpRequest as JanusRequest, HttpService, LocalObjectStore, LocalRepositoryValidator,
    MembershipWrite, Method, MfaStateWrite, ObjectMetadataWrite, ObjectStore, Operation,
    OperationBackend, Permission, Principal, Project, ProjectReducerPort, ProjectService,
    ProjectServiceError, ProjectWrite, PrometheusTextExporter, RepositoryValidator, RunnerError,
    RunnerRegistry, SessionRecord, SpacetimeRuntime, TelemetryBuffer, TelemetryExporter, TenantId,
    TenantWrite, UserAccount, WorldSnapshot,
};
use rand::{distributions::Alphanumeric, Rng};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

type Projects = ProjectService<128, 256>;
type Builds = BuildService<128, 256>;
type Runners = RunnerRegistry<128>;
type Operations = OperationBackend<256>;
type Auth = AuthDirectory<128, 256>;
type EmailOtps = EmailOtpStore<128>;
type Objects = Box<dyn ObjectStore + Send>;

const MAX_SOURCE_UPLOAD_BYTES: usize = 128 * 1024 * 1024;
const MAX_ARTIFACT_UPLOAD_BYTES: usize = 64 * 1024 * 1024;
const MAX_DURABLE_TELEMETRY_SAMPLES: usize = 4096;
const MAX_DURABLE_OBSERVABILITY_ROWS: usize = 4096;
const MAX_MULTIPART_OVERHEAD_BYTES: usize = 1024 * 1024;
const MULTIPART_INITIAL_CAPACITY_BYTES: usize = 64 * 1024;
const SPEC_CONTENT_SECURITY_POLICY: &str = concat!(
    "default-src 'self'; ",
    "style-src 'self' 'unsafe-inline'; ",
    "script-src 'self' 'unsafe-inline'; ",
    "img-src 'self' data:; ",
    "frame-ancestors 'self'",
);

struct ApiState {
    projects: Mutex<Projects>,
    durable_projects: Mutex<Option<Box<dyn ProjectReducerPort>>>,
    builds: Mutex<Builds>,
    runners: Mutex<Runners>,
    operations: Mutex<Operations>,
    auth: Mutex<Auth>,
    auth_store: Option<FileAuthStore>,
    email_otps: Mutex<EmailOtps>,
    telemetry: Mutex<TelemetryBuffer<256>>,
    object_store: Mutex<Objects>,
    spacetime_runtime: Option<SpacetimeRuntime>,
    spacetime_required: bool,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum AuthMutationError {
    Domain(janus_core::AuthError),
    Persistence,
}

fn mutate_auth<T, F>(state: &web::Data<ApiState>, mutation: F) -> Result<T, AuthMutationError>
where
    F: FnOnce(&mut Auth) -> Result<T, janus_core::AuthError>,
{
    let result = {
        let Ok(mut auth) = state.auth.lock() else {
            return Err(AuthMutationError::Persistence);
        };
        let snapshot = auth.snapshot();
        let result = mutation(&mut auth).map_err(AuthMutationError::Domain)?;
        if let Some(store) = &state.auth_store {
            if store.save(&auth).is_err() {
                if auth.restore(&snapshot).is_err() {
                    return Err(AuthMutationError::Persistence);
                }
                return Err(AuthMutationError::Persistence);
            }
        }
        drop(auth);
        result
    };
    Ok(result)
}

#[derive(Deserialize, Serialize)]
struct ProjectInput {
    name: String,
    slug: String,
    #[serde(default)]
    description: String,
    #[serde(rename = "repoProvider", alias = "repo_provider")]
    repo_provider: String,
    #[serde(rename = "repoUrl", alias = "repo_url")]
    repo_url: String,
    #[serde(rename = "repoBranch", alias = "repo_branch")]
    repo_branch: String,
}

#[derive(Serialize)]
struct ProjectView {
    id: u64,
    tenant: u32,
    name: String,
    slug: String,
    description: String,
    status: String,
    #[serde(rename = "repoProvider")]
    repo_provider: String,
    #[serde(rename = "repoUrl")]
    repo_url: String,
    #[serde(rename = "repoBranch")]
    repo_branch: String,
}

#[derive(Deserialize, Serialize)]
struct DeploymentInput {
    #[serde(rename = "buildId", alias = "build_id")]
    build_id: Option<u64>,
    #[serde(rename = "buildJobId")]
    build_job_id: Option<u64>,
    #[serde(rename = "projectId")]
    project_id: Option<u64>,
    #[serde(default, rename = "targetType", alias = "target_type")]
    target_type: String,
    #[serde(default, rename = "targetRef", alias = "target_ref")]
    target_ref: String,
    #[serde(default, rename = "runnerId")]
    runner_id: String,
    #[serde(default)]
    env: HashMap<String, String>,
}

struct ValidatedDeploymentInput {
    build_id: u64,
    target_type: String,
    environment: Vec<(String, String)>,
}

#[derive(Deserialize)]
struct BuildListQuery {
    #[serde(rename = "projectId")]
    project_id: Option<u64>,
}

#[derive(Deserialize)]
struct RunnerRegisterInput {
    id: u64,
    capabilities: Vec<String>,
    lease_seconds: u64,
}

#[derive(Deserialize)]
struct RunnerHeartbeatInput {
    id: u64,
    lease_seconds: u64,
}

#[derive(Deserialize)]
struct RepositoryValidationInput {
    project_id: u64,
}

#[derive(Deserialize, Serialize)]
struct RepoImportInput {
    #[serde(rename = "projectId")]
    project_id: u64,
    #[serde(default)]
    provider: String,
    #[serde(rename = "repoUrl")]
    repo_url: String,
    #[serde(default)]
    branch: String,
    #[serde(default, rename = "runnerId")]
    _runner_id: String,
}

struct MultipartData {
    fields: HashMap<String, String>,
    filename: String,
    bytes: Vec<u8>,
}

struct UploadMetadata<'a> {
    key: &'a str,
    tenant: TenantId,
    size: u64,
    content_type: &'a str,
    checksum: &'a str,
    created_at: u64,
    retention_until: u64,
}

struct ValidatedUpload {
    project_id: EntityId,
    source_ref: String,
    key: String,
}

#[derive(Deserialize)]
struct PasswordSigninInput {
    email: String,
    password: String,
}

#[derive(Deserialize)]
struct SignupInput {
    email: String,
    password: String,
    #[serde(default, rename = "name")]
    _name: String,
}

#[derive(Deserialize)]
struct TotpVerifyInput {
    code: String,
}

impl From<&Project> for ProjectView {
    fn from(project: &Project) -> Self {
        Self {
            id: project.id.0,
            tenant: project.tenant.0,
            name: project.name.clone(),
            slug: project.slug.clone(),
            description: project.description.clone(),
            status: project.status.clone(),
            repo_provider: project.repo_provider.clone(),
            repo_url: project.repo_url.clone(),
            repo_branch: project.repo_branch.clone(),
        }
    }
}

fn project_write(project: &Project) -> ProjectWrite {
    ProjectWrite {
        name: project.name.clone(),
        slug: project.slug.clone(),
        description: project.description.clone(),
        repo_provider: project.repo_provider.clone(),
        repository: project.repo_url.clone(),
        branch: project.repo_branch.clone(),
        created_at: project.created_at,
        updated_at: project.updated_at,
    }
}

fn env_value(name: &str) -> Option<String> {
    std::env::var(name).ok() // tigerstyle: allow-direct-env — process configuration adapter
}

fn spacetime_runtime_from_env() -> io::Result<(Option<SpacetimeRuntime>, bool)> {
    let required = env_value("JANUS_SPACETIME_REQUIRED")
        .is_some_and(|value| value.eq_ignore_ascii_case("true"));
    let runtime = match env_value("JANUS_SPACETIME_URI") {
        Some(uri) => {
            let database = env_value("JANUS_SPACETIME_DATABASE").ok_or_else(|| {
                io::Error::new(
                    io::ErrorKind::InvalidInput,
                    "JANUS_SPACETIME_DATABASE is required with JANUS_SPACETIME_URI",
                )
            })?;
            let token = env_value("JANUS_SPACETIME_TOKEN");
            Some(
                SpacetimeRuntime::connect(&uri, &database, token.as_deref())
                    .map_err(|error| io::Error::new(io::ErrorKind::ConnectionAborted, error))?,
            )
        }
        None if required => {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "JANUS_SPACETIME_URI is required when JANUS_SPACETIME_REQUIRED=true",
            ));
        }
        None => None,
    };
    Ok((runtime, required))
}

fn auth_store_from_env() -> io::Result<Option<FileAuthStore>> {
    env_value("JANUS_AUTH_FILE")
        .map(|path| {
            let secret = env_value("JANUS_MFA_ENCRYPTION_KEY")
                .or_else(|| env_value("JANUS_JWT_SECRET"))
                .or_else(|| {
                    let environment = env_value("JANUS_ENV").unwrap_or_default();
                    if environment.trim().is_empty()
                        || environment.eq_ignore_ascii_case("dev")
                        || environment.eq_ignore_ascii_case("development")
                    {
                        Some("janus-dev-insecure-mfa-key".to_owned())
                    } else {
                        None
                    }
                })
                .ok_or_else(|| {
                    io::Error::new(
                        io::ErrorKind::InvalidInput,
                        "JANUS_AUTH_FILE requires a production MFA encryption secret",
                    )
                })?;
            Ok::<FileAuthStore, io::Error>(FileAuthStore::with_secret(path, &secret))
        })
        .transpose()
}

fn operations_from_env() -> io::Result<Operations> {
    env_value("JANUS_OPERATION_FILE").map_or_else(
        || Ok(Operations::default()),
        |path| {
            OperationBackend::open_file(path).map_err(|error| {
                io::Error::new(
                    io::ErrorKind::InvalidData,
                    format!("operation load: {error:?}"),
                )
            })
        },
    )
}

fn hydrate_services(runtime: Option<&SpacetimeRuntime>) -> io::Result<(Projects, Builds, Runners)> {
    let mut projects = Projects::new();
    let mut builds = Builds::new();
    let mut runners = Runners::new();
    let Some(runtime) = runtime else {
        return Ok((projects, builds, runners));
    };
    projects
        .hydrate_projects(runtime.project_components())
        .map_err(|error| {
            io::Error::new(
                io::ErrorKind::InvalidData,
                format!("project hydration: {error:?}"),
            )
        })?;
    let durable_builds = runtime.build_components().map_err(|error| {
        io::Error::new(
            io::ErrorKind::InvalidData,
            format!("build hydration: {error}"),
        )
    })?;
    let durable_deployments = runtime.deployment_components().map_err(|error| {
        io::Error::new(
            io::ErrorKind::InvalidData,
            format!("deployment hydration: {error}"),
        )
    })?;
    builds
        .hydrate_durable(durable_builds, durable_deployments)
        .map_err(|error| {
            io::Error::new(
                io::ErrorKind::InvalidData,
                format!("ECS hydration: {error:?}"),
            )
        })?;
    let durable_runners = runtime.runner_components().map_err(|error| {
        io::Error::new(
            io::ErrorKind::InvalidData,
            format!("runner hydration: {error}"),
        )
    })?;
    runners.hydrate(durable_runners).map_err(|error| {
        io::Error::new(
            io::ErrorKind::InvalidData,
            format!("runner hydration: {error:?}"),
        )
    })?;
    Ok((projects, builds, runners))
}

fn hydrate_auth(runtime: &SpacetimeRuntime, auth: &mut Auth) -> io::Result<()> {
    let memberships = runtime.memberships();
    let accounts = runtime.accounts();
    if accounts.len() > 128 {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "authoritative account projection exceeds the bounded directory",
        ));
    }
    let mut users = Vec::with_capacity(128);
    for account in accounts
        .into_iter()
        .filter(|account| account.status == "active")
    {
        let permissions = memberships
            .iter()
            .filter(|membership| {
                membership.subject_id.eq(&account.id)
                    && membership.tenant_id.eq(&account.tenant_id)
                    && membership.status.eq("active")
            })
            .fold(0_u32, |bits, membership| bits | membership.permissions);
        let user = UserAccount {
            subject: janus_core::SubjectId(account.id),
            tenant: TenantId(account.tenant_id),
            email: account.email,
            // The module verifies the private credential through its
            // reducer. This sentinel is never passed to a local verifier.
            password_hash: "spacetime-managed-credential".to_owned(),
            mfa_enabled: account.mfa_required,
            totp_enabled: false,
            totp_secret: "spacetime-managed-mfa".to_owned(),
            email_otp_enabled: false,
            recovery_codes: 0,
            permissions,
        };
        users.push(user);
    }
    let auth_sessions = runtime.auth_sessions();
    if auth_sessions.len() > 256 {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "authoritative session projection exceeds the bounded directory",
        ));
    }
    let mut sessions = Vec::with_capacity(256);
    for session in auth_sessions.into_iter().filter(|session| !session.revoked) {
        sessions.push(SessionRecord {
            token: session.token,
            subject: janus_core::SubjectId(session.subject_id),
            tenant: TenantId(session.tenant_id),
            expires_at: session.expires_at,
            mfa_verified: session.mfa_verified,
        });
    }
    auth.hydrate_projection(users, sessions).map_err(|error| {
        io::Error::new(
            io::ErrorKind::InvalidData,
            format!("authentication hydration: {error:?}"),
        )
    })
}

#[actix_web::main]
async fn main() -> io::Result<()> {
    let address = env_value("JANUS_HTTP_ADDRESS").unwrap_or_else(|| "127.0.0.1:8080".to_owned());
    let service = HttpService::new().with_ready(true);
    let (spacetime_runtime, spacetime_required) = spacetime_runtime_from_env()?;
    reconcile_startup_jobs(spacetime_runtime.as_ref())?;
    reconcile_startup_runners(spacetime_runtime.as_ref())?;
    let mut auth = Auth::new();
    let auth_store = if spacetime_runtime.is_some() {
        None
    } else {
        auth_store_from_env()?
    };
    if let Some(store) = &auth_store {
        store.load(&mut auth).map_err(|error| {
            io::Error::new(io::ErrorKind::InvalidData, format!("auth load: {error:?}"))
        })?;
    }
    if let Some(runtime) = &spacetime_runtime {
        hydrate_auth(runtime, &mut auth)?;
    }
    eprintln!("janus-api listening on {address}");
    let mut operations = operations_from_env()?;
    let mut object_store = open_object_store()?;
    reconcile_startup_objects(spacetime_runtime.as_ref(), &mut object_store)?;
    let (projects, builds, runners) = hydrate_services(spacetime_runtime.as_ref())?;
    if let Some(runtime) = &spacetime_runtime {
        operations = Operations::default();
        for operation in runtime.operation_components().map_err(|error| {
            io::Error::new(
                io::ErrorKind::InvalidData,
                format!("operation hydration: {error}"),
            )
        })? {
            operations.insert(operation).map_err(|error| {
                io::Error::new(
                    io::ErrorKind::InvalidData,
                    format!("operation hydration: {error:?}"),
                )
            })?;
        }
    }
    let durable_projects = spacetime_runtime
        .as_ref()
        .map(|runtime| Box::new(runtime.clone()) as Box<dyn ProjectReducerPort>);
    let state = web::Data::new(ApiState {
        projects: Mutex::new(projects),
        durable_projects: Mutex::new(durable_projects),
        builds: Mutex::new(builds),
        runners: Mutex::new(runners),
        operations: Mutex::new(operations),
        auth: Mutex::new(auth),
        auth_store,
        email_otps: Mutex::new(EmailOtps::new()),
        telemetry: Mutex::new(TelemetryBuffer::new()),
        object_store: Mutex::new(Box::new(object_store)),
        spacetime_runtime,
        spacetime_required,
    });
    start_durable_worker(state.spacetime_runtime.clone());
    HttpServer::new(move || {
        App::new()
            .app_data(web::Data::new(service))
            .app_data(state.clone())
            .configure(configure)
    })
    .bind(address)?
    .run()
    .await
}

fn start_durable_worker(runtime: Option<SpacetimeRuntime>) {
    let Some(runtime) = runtime else {
        return;
    };
    std::thread::spawn(move || loop {
        let now = unix_now();
        let mut handler = DeterministicBuildJobHandler {
            runtime: &runtime,
            now,
        };
        match durable_worker::poll_once(&runtime, &mut handler, 1, now, 30) {
            Ok(WorkerPoll::Idle | WorkerPoll::Completed { .. } | WorkerPoll::Failed { .. })
            | Err(_) => std::thread::park_timeout(Duration::from_secs(1)),
        }
    });
}

fn reconcile_startup_jobs(runtime: Option<&SpacetimeRuntime>) -> io::Result<()> {
    let Some(runtime) = runtime else {
        return Ok(());
    };
    runtime
        .reconcile_jobs(unix_now())
        .map_err(|error| io::Error::other(format!("job reconciliation: {error:?}")))
}

fn reconcile_startup_runners(runtime: Option<&SpacetimeRuntime>) -> io::Result<()> {
    let Some(runtime) = runtime else {
        return Ok(());
    };
    runtime
        .expire_runners(unix_now())
        .map_err(|error| io::Error::other(format!("runner reconciliation: {error:?}")))
}

fn open_object_store() -> io::Result<LocalObjectStore<256, { MAX_SOURCE_UPLOAD_BYTES * 2 }>> {
    let object_root =
        env_value("JANUS_OBJECT_ROOT").unwrap_or_else(|| "janus-artifacts".to_owned());
    LocalObjectStore::new(object_root)
        .map_err(|error| io::Error::other(format!("object store: {error:?}")))
}

fn reconcile_startup_objects(
    runtime: Option<&SpacetimeRuntime>,
    object_store: &mut LocalObjectStore<256, { MAX_SOURCE_UPLOAD_BYTES * 2 }>,
) -> io::Result<()> {
    let Some(runtime) = runtime else {
        return Ok(());
    };
    runtime
        .expire_object_metadata(unix_now())
        .map_err(|error| io::Error::other(format!("object expiration: {error:?}")))?;
    runtime
        .expire_artifact_metadata(unix_now())
        .map_err(|error| io::Error::other(format!("artifact expiration: {error:?}")))?;
    for object in runtime
        .object_metadata()
        .into_iter()
        .filter(|object| object.status == "expired")
    {
        if object_store.delete(&object.key).is_ok() {
            runtime
                .delete_artifact_metadata(&object.key, TenantId(object.tenant_id))
                .map_err(|error| {
                    io::Error::other(format!("artifact metadata cleanup: {error:?}"))
                })?;
            runtime
                .delete_object_metadata(&object.key, TenantId(object.tenant_id))
                .map_err(|error| io::Error::other(format!("object metadata cleanup: {error:?}")))?;
        }
    }
    for artifact in runtime
        .artifact_metadata()
        .into_iter()
        .filter(|artifact| artifact.status == "expired")
    {
        if object_store.delete(&artifact.object_key).is_ok() {
            runtime
                .delete_object_metadata(&artifact.object_key, TenantId(artifact.tenant_id))
                .map_err(|error| io::Error::other(format!("object cleanup: {error:?}")))?;
            runtime
                .delete_artifact_metadata(&artifact.id, TenantId(artifact.tenant_id))
                .map_err(|error| io::Error::other(format!("artifact cleanup: {error:?}")))?;
        }
    }
    Ok(())
}

fn configure(config: &mut web::ServiceConfig) {
    config
        .service(web::resource("/healthz").route(web::get().to(healthz)))
        .service(web::resource("/readyz").route(web::get().to(readyz)))
        .service(web::resource("/").route(web::get().to(frontend_page)))
        .service(web::resource("/admin").route(web::get().to(frontend_page)))
        .service(web::resource("/app.css").route(web::get().to(frontend_css)))
        .service(web::resource("/app.js").route(web::get().to(frontend_js)))
        .service(web::resource("/metrics").route(web::get().to(prometheus_metrics)))
        .service(web::resource("/api/v1/auth/signup").route(web::post().to(auth_signup)))
        .service(web::resource("/api/v1/auth/signout").route(web::post().to(auth_signout)))
        .service(web::resource("/api/v1/auth/signin").route(web::post().to(auth_signin)))
        .service(web::resource("/api/v1/auth/signin/password").route(web::post().to(auth_signin)))
        .service(web::resource("/api/v1/auth/refresh").route(web::post().to(auth_refresh)))
        .service(web::resource("/api/v1/mfa/status").route(web::get().to(mfa_status)))
        .service(web::resource("/api/v1/mfa/totp/enroll").route(web::post().to(enroll_totp)))
        .service(web::resource("/api/v1/mfa/totp/verify").route(web::post().to(verify_totp)))
        .service(web::resource("/api/v1/mfa/email/enroll").route(web::post().to(enroll_email_otp)))
        .service(web::resource("/api/v1/mfa/email/send").route(web::post().to(send_email_otp)))
        .service(web::resource("/api/v1/mfa/email/verify").route(web::post().to(verify_email_otp)))
        .service(web::resource("/api/v1/mfa/totp/disable").route(web::post().to(disable_totp)))
        .service(
            web::resource("/api/v1/mfa/email/disable").route(web::post().to(disable_email_otp)),
        )
        .service(web::resource("/api/v1/operations/{id}").route(web::get().to(get_operation)))
        .service(web::resource("/api/v1/auth/me").route(web::get().to(auth_me)))
        .service(web::resource("/api/v1/admin/spec").route(web::get().to(admin_spec)))
        .service(web::resource("/api/v1/telemetry").route(web::get().to(telemetry_snapshot)))
        .service(web::resource("/api/v1/domains").route(web::get().to(list_domains)))
        .service(web::resource("/api/v1/builds").route(web::get().to(list_builds)))
        .service(web::resource("/api/v1/builds/{id}/logs").route(web::get().to(get_build_logs)))
        .service(web::resource("/api/v1/builds/{id}").route(web::get().to(get_build)))
        .service(
            web::resource("/api/v1/runners")
                .route(web::get().to(list_runners))
                .route(web::post().to(register_runner)),
        )
        .service(web::resource("/api/v1/runners/register").route(web::post().to(register_runner)))
        .service(web::resource("/api/v1/runners/heartbeat").route(web::post().to(heartbeat_runner)))
        .service(
            web::resource("/api/v1/deployments")
                .route(web::get().to(list_deployments))
                .route(web::post().to(create_deployment)),
        )
        .service(
            web::resource("/api/v1/deployments/{id}")
                .route(web::get().to(get_deployment))
                .route(web::delete().to(delete_deployment)),
        )
        .service(
            web::resource("/api/v1/projects")
                .route(web::get().to(list_projects))
                .route(web::post().to(create_project)),
        )
        .service(
            web::resource("/api/v1/projects/{id}")
                .route(web::patch().to(update_project))
                .route(web::delete().to(delete_project)),
        )
        .service(web::resource("/api/v1/repos/import").route(web::post().to(import_repository)))
        .service(web::resource("/api/v1/repos/validate").route(web::post().to(validate_repository)))
        .service(
            web::resource("/api/v1/uploads/source-bundles")
                .route(web::post().to(upload_source_bundle)),
        )
        .service(web::resource("/api/v1/uploads/artifacts").route(web::post().to(upload_artifact)))
        .service(web::resource("/api/v1/git/providers").route(web::get().to(list_git_providers)))
        .default_service(web::route().to(dispatch));
}

async fn healthz() -> HttpResponse {
    HttpResponse::Ok().json(serde_json::json!({ "status": "ok" }))
}

async fn frontend_page() -> HttpResponse {
    HttpResponse::Ok()
        .content_type("text/html; charset=utf-8")
        .body(include_str!("../../web/index.html"))
}

async fn frontend_css() -> HttpResponse {
    HttpResponse::Ok()
        .content_type("text/css; charset=utf-8")
        .body(include_str!("../../web/app.css"))
}

async fn frontend_js() -> HttpResponse {
    HttpResponse::Ok()
        .content_type("text/javascript; charset=utf-8")
        .body(include_str!("../../web/app.js"))
}

async fn readyz(service: web::Data<HttpService>, state: web::Data<ApiState>) -> HttpResponse {
    let spacetime_ready = !state.spacetime_required
        || state
            .spacetime_runtime
            .as_ref()
            .is_some_and(SpacetimeRuntime::is_active);
    if service.is_ready() && spacetime_ready {
        HttpResponse::Ok().json(serde_json::json!({ "status": "ready" }))
    } else {
        HttpResponse::ServiceUnavailable().json(serde_json::json!({ "status": "not_ready" }))
    }
}

async fn prometheus_metrics(state: web::Data<ApiState>) -> HttpResponse {
    let Ok(samples) = telemetry_samples(&state) else {
        return internal_error();
    };
    let mut exporter = PrometheusTextExporter::new();
    for sample in samples {
        if exporter.export(sample).is_err() {
            return internal_error();
        }
    }
    HttpResponse::Ok()
        .content_type("text/plain; version=0.0.4")
        .body(exporter.output().to_owned())
}

fn telemetry_samples(state: &ApiState) -> Result<Vec<janus_core::MetricSample>, ()> {
    if let Some(runtime) = &state.spacetime_runtime {
        let samples = runtime.telemetry_samples();
        let mut metrics = Vec::with_capacity(MAX_DURABLE_TELEMETRY_SAMPLES);
        for sample in samples {
            metrics.push(janus_core::MetricSample {
                name: sample.name,
                kind: janus_core::MetricKind::Gauge,
                value: sample.value,
                timestamp: sample.timestamp,
                labels: Vec::with_capacity(1),
            });
        }
        return Ok(metrics);
    }
    state
        .telemetry
        .lock()
        .map(|buffer| buffer.samples().to_vec())
        .map_err(|_| ())
}

fn persist_mfa_projection(
    state: &web::Data<ApiState>,
    auth: &Auth,
    subject: janus_core::SubjectId,
) -> Result<(), ()> {
    let Some(runtime) = &state.spacetime_runtime else {
        return Ok(());
    };
    let Some(account) = auth.user(subject) else {
        return Err(());
    };
    runtime
        .put_mfa_state(&MfaStateWrite {
            subject_id: account.subject.0,
            enabled: account.mfa_enabled,
            totp_enabled: account.totp_enabled,
            email_otp_enabled: account.email_otp_enabled,
            totp_secret: account.totp_secret.as_str(),
            recovery_codes: account.recovery_codes,
            updated_at: unix_now(),
        })
        .map_err(|_| ())
}

fn persist_durable_account(
    runtime: &SpacetimeRuntime,
    account: &janus_core::UserAccount,
) -> Result<(), ()> {
    let tenant_name = format!("tenant-{}", account.tenant.0);
    let membership_id = format!("{}:{}", account.subject.0, account.tenant.0);
    runtime
        .upsert_tenant(&TenantWrite {
            id: account.tenant.0,
            name: &tenant_name,
            status: "active",
            created_at: unix_now(),
        })
        .and_then(|()| {
            runtime.upsert_account(&AccountWrite {
                id: account.subject.0,
                tenant: account.tenant,
                email: &account.email,
                status: "active",
                mfa_required: account.mfa_enabled,
                created_at: unix_now(),
            })
        })
        .and_then(|()| {
            runtime.upsert_membership(&MembershipWrite {
                id: &membership_id,
                subject_id: account.subject.0,
                tenant: account.tenant,
                permissions: account.permissions,
                status: "active",
                updated_at: unix_now(),
            })
        })
        .and_then(|()| {
            runtime.put_credential(account.subject.0, &account.password_hash, 1, unix_now())
        })
        .map_err(|_| ())
}

fn persist_durable_session(
    state: &web::Data<ApiState>,
    token: &str,
    account: &janus_core::UserAccount,
    expires_at: u64,
) -> Result<(), ()> {
    let Some(runtime) = &state.spacetime_runtime else {
        return Ok(());
    };
    if runtime
        .issue_auth_session(
            token,
            account.subject.0,
            account.tenant,
            expires_at,
            !account.mfa_enabled,
        )
        .is_ok()
    {
        return Ok(());
    }
    let _rollback = mutate_auth(state, |auth| {
        auth.revoke_session(token).map(|()| account.subject)
    });
    Err(())
}

async fn auth_signup(input: web::Json<SignupInput>, state: web::Data<ApiState>) -> HttpResponse {
    if input.email.trim().is_empty() || input.password.is_empty() {
        return HttpResponse::BadRequest().json(serde_json::json!({
            "error": "email and password are required"
        }));
    }
    let Ok(password_hash) = bcrypt::hash(&input.password, bcrypt::DEFAULT_COST) else {
        return HttpResponse::BadRequest().json(serde_json::json!({
            "error": "password could not be accepted"
        }));
    };
    let account = match state.auth.lock() {
        Ok(mut auth) => {
            let snapshot = auth.snapshot();
            match auth.register_new_account(&input.email, password_hash) {
                Ok(account) => {
                    if let Some(runtime) = &state.spacetime_runtime {
                        if persist_durable_account(runtime, &account).is_err() {
                            if auth.restore(&snapshot).is_err() {
                                return internal_error();
                            }
                            return internal_error();
                        }
                    }
                    if let Some(store) = &state.auth_store {
                        if store.save(&auth).is_err() {
                            if auth.restore(&snapshot).is_err() {
                                return internal_error();
                            }
                            return internal_error();
                        }
                    }
                    account
                }
                Err(janus_core::AuthError::AlreadyExists) => {
                    return HttpResponse::Conflict().json(serde_json::json!({
                        "error": "email already registered"
                    }));
                }
                Err(janus_core::AuthError::Capacity) => return internal_error(),
                Err(_) => {
                    return HttpResponse::BadRequest().json(serde_json::json!({
                        "error": "email and password are required"
                    }))
                }
            }
        }
        Err(_) => return internal_error(),
    };
    HttpResponse::Created().json(serde_json::json!({
        "user": {
            "id": account.subject.0,
            "email": account.email,
            "tenantId": account.tenant.0,
            "mfaEnabled": false,
        }
    }))
}

async fn enroll_totp(request: HttpRequest, state: web::Data<ApiState>) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let response = match state.auth.lock() {
        Ok(mut auth) => {
            let snapshot = auth.snapshot();
            match auth.enroll_totp(principal.subject) {
                Ok(qr_code_uri) => {
                    let Some(account) = auth.user(principal.subject).cloned() else {
                        return unauthorized();
                    };
                    if persist_mfa_projection(&state, &auth, principal.subject).is_err() {
                        if auth.restore(&snapshot).is_err() {
                            return internal_error();
                        }
                        return internal_error();
                    }
                    if let Some(store) = &state.auth_store {
                        if store.save(&auth).is_err() {
                            if auth.restore(&snapshot).is_err() {
                                return internal_error();
                            }
                            return internal_error();
                        }
                    }
                    serde_json::json!({
                        "secret": account.totp_secret,
                        "qrCodeUri": qr_code_uri,
                        "totpEnabled": false,
                    })
                }
                Err(_) => return unauthorized(),
            }
        }
        Err(_) => return internal_error(),
    };
    HttpResponse::Ok().json(response)
}

async fn verify_totp(
    request: HttpRequest,
    input: web::Json<TotpVerifyInput>,
    state: web::Data<ApiState>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    if input.code.trim().is_empty() {
        return HttpResponse::BadRequest().json(serde_json::json!({
            "error": "code is required"
        }));
    }
    let result = match state.auth.lock() {
        Ok(mut auth) => {
            let snapshot = auth.snapshot();
            let result = auth.verify_totp(principal.subject, &input.code, unix_now());
            if result.is_ok() {
                if persist_mfa_projection(&state, &auth, principal.subject).is_err() {
                    if auth.restore(&snapshot).is_err() {
                        return internal_error();
                    }
                    return internal_error();
                }
                if let Some(store) = &state.auth_store {
                    if store.save(&auth).is_err() {
                        if auth.restore(&snapshot).is_err() {
                            return internal_error();
                        }
                        return internal_error();
                    }
                }
            }
            result
        }
        Err(_) => return internal_error(),
    };
    match result {
        Ok(()) => HttpResponse::Ok().json(serde_json::json!({
            "ok": true,
            "totpEnabled": true,
            "message": "TOTP successfully enabled",
        })),
        Err(_) => HttpResponse::BadRequest().json(serde_json::json!({
            "error": "verification failed"
        })),
    }
}

async fn enroll_email_otp(request: HttpRequest, state: web::Data<ApiState>) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let code = issue_email_otp_for_request(&state, principal.subject);
    match code {
        Ok(()) => HttpResponse::Ok().json(serde_json::json!({
            "ok": true,
            "expiresInMinutes": 10,
            "message": "Email OTP challenge created for the account delivery adapter",
        })),
        Err(()) => internal_error(),
    }
}

async fn send_email_otp(request: HttpRequest, state: web::Data<ApiState>) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let enabled = match state.auth.lock() {
        Ok(auth) => auth
            .user(principal.subject)
            .is_some_and(|account| account.email_otp_enabled),
        Err(_) => return internal_error(),
    };
    if !enabled {
        return HttpResponse::BadRequest().json(serde_json::json!({
            "error": "Email OTP is not enabled"
        }));
    }
    match issue_email_otp_for_request(&state, principal.subject) {
        Ok(()) => HttpResponse::Ok().json(serde_json::json!({
            "ok": true,
            "expiresInMinutes": 10,
            "message": "Email OTP challenge created for the account delivery adapter",
        })),
        Err(()) => internal_error(),
    }
}

async fn verify_email_otp(
    request: HttpRequest,
    input: web::Json<TotpVerifyInput>,
    state: web::Data<ApiState>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    if input.code.trim().is_empty() {
        return HttpResponse::BadRequest().json(serde_json::json!({
            "error": "code is required"
        }));
    }
    let verified = verify_email_otp_for_request(&state, principal.subject, &input.code).is_ok();
    if !verified {
        return HttpResponse::BadRequest().json(serde_json::json!({
            "error": "invalid or expired code"
        }));
    }
    let result = match state.auth.lock() {
        Ok(mut auth) => {
            let snapshot = auth.snapshot();
            let result = auth.enable_email_otp(principal.subject);
            if result.is_ok() {
                if persist_mfa_projection(&state, &auth, principal.subject).is_err() {
                    if auth.restore(&snapshot).is_err() {
                        return internal_error();
                    }
                    return internal_error();
                }
                if let Some(store) = &state.auth_store {
                    if store.save(&auth).is_err() {
                        if auth.restore(&snapshot).is_err() {
                            return internal_error();
                        }
                        return internal_error();
                    }
                }
            }
            result
        }
        Err(_) => return internal_error(),
    };
    match result {
        Ok(()) => HttpResponse::Ok().json(serde_json::json!({
            "ok": true,
            "emailOtpEnabled": true,
            "message": "Email OTP successfully enabled",
        })),
        Err(_) => unauthorized(),
    }
}

fn issue_email_otp_for_request(
    state: &web::Data<ApiState>,
    subject: janus_core::SubjectId,
) -> Result<(), ()> {
    let now = unix_now();
    if let Some(runtime) = &state.spacetime_runtime {
        let (tenant, recipient) = state
            .auth
            .lock()
            .map_err(|_| ())?
            .user(subject)
            .map(|account| (account.tenant, account.email.clone()))
            .ok_or(())?;
        let mut raw = [0_u8; 4];
        rand::thread_rng() // tigerstyle: allow-direct-randomness — email OTP adapter
            .fill(&mut raw);
        let code = format!("{:06}", u32::from_le_bytes(raw) % 1_000_000);
        runtime
            .issue_email_otp(subject.0, &email_otp_hash(&code), now.saturating_add(600))
            .map_err(|_| ())?;
        return runtime
            .enqueue_email(
                durable_email_id(subject, now),
                tenant,
                &recipient,
                "Janus email verification code",
                &format!("Your Janus verification code is {code}."),
                3,
            )
            .map_err(|_| ());
    }
    state
        .email_otps
        .lock()
        .map_err(|_| ())?
        .issue(subject, now, 600)
        .map(|_| ())
        .map_err(|_| ())
}

fn verify_email_otp_for_request(
    state: &web::Data<ApiState>,
    subject: janus_core::SubjectId,
    code: &str,
) -> Result<(), ()> {
    let now = unix_now();
    if let Some(runtime) = &state.spacetime_runtime {
        return runtime
            .verify_email_otp(subject.0, &email_otp_hash(code), now)
            .map_err(|_| ());
    }
    state
        .email_otps
        .lock()
        .map_err(|_| ())?
        .verify(subject, code, now)
        .then_some(())
        .ok_or(())
}

fn email_otp_hash(code: &str) -> [u8; 32] {
    let mut hash = [0_u8; 32];
    hash.copy_from_slice(Sha256::digest(code.trim().as_bytes()).as_slice());
    hash
}

async fn auth_me(request: HttpRequest, state: web::Data<ApiState>) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let account = match state.auth.lock() {
        Ok(auth) => auth.user(principal.subject).cloned(),
        Err(_) => return internal_error(),
    };
    let Some(account) = account else {
        return unauthorized();
    };
    HttpResponse::Ok().json(serde_json::json!({
        "user": {
            "id": account.subject.0,
            "email": account.email,
            "tenantId": account.tenant.0,
            "mfaEnabled": account.mfa_enabled,
            "permissions": account.permissions,
        }
    }))
}

async fn admin_spec(request: HttpRequest, state: web::Data<ApiState>) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    if principal
        .authorize(principal.tenant, Permission::OperateControlPlane)
        .is_err()
    {
        return HttpResponse::Forbidden().json(serde_json::json!({
            "error": "admin permission required"
        }));
    }
    HttpResponse::Ok()
        .insert_header(("Content-Security-Policy", SPEC_CONTENT_SECURITY_POLICY))
        .content_type("text/html; charset=utf-8")
        .body(include_str!("../../../Janus_Rust_System_Spec.html"))
}

fn authenticate_signin(
    input: &PasswordSigninInput,
    state: &web::Data<ApiState>,
    token: &str,
    expires_at: u64,
) -> Result<janus_core::SubjectId, Box<HttpResponse>> {
    if let Some(runtime) = &state.spacetime_runtime {
        let subject = match state.auth.lock() {
            Ok(auth) => auth
                .users_by_email(&input.email)
                .map(|account| account.subject),
            Err(_) => return Err(Box::new(internal_error())),
        };
        let Some(subject) = subject else {
            return Err(Box::new(unauthorized()));
        };
        if runtime.verify_password(subject.0, &input.password).is_err() {
            return Err(Box::new(unauthorized()));
        }
        match mutate_auth(state, |auth| {
            auth.issue_session(token, subject, expires_at, false)
        }) {
            Ok(()) => Ok(subject),
            Err(AuthMutationError::Domain(_)) => Err(Box::new(unauthorized())),
            Err(AuthMutationError::Persistence) => Err(Box::new(internal_error())),
        }
    } else {
        match mutate_auth(state, |auth| {
            auth.authenticate_password(
                &input.email,
                &input.password,
                &BcryptPasswordVerifier,
                token.to_owned(),
                expires_at,
            )
        }) {
            Ok(subject) => Ok(subject),
            Err(AuthMutationError::Domain(_)) => Err(Box::new(unauthorized())),
            Err(AuthMutationError::Persistence) => Err(Box::new(internal_error())),
        }
    }
}

async fn auth_signin(
    input: web::Json<PasswordSigninInput>,
    state: web::Data<ApiState>,
) -> HttpResponse {
    if input.email.trim().is_empty() || input.password.is_empty() {
        return HttpResponse::BadRequest().json(serde_json::json!({
            "error": "email and password are required"
        }));
    }
    let token: String = rand::thread_rng() // tigerstyle: allow-direct-randomness — HTTP session-token adapter
        .sample_iter(Alphanumeric)
        .take(48)
        .map(char::from)
        .collect();
    let expires_at = unix_now().saturating_add(3600);
    let subject = match authenticate_signin(&input, &state, &token, expires_at) {
        Ok(subject) => subject,
        Err(response) => return *response,
    };
    let account = match state.auth.lock() {
        Ok(auth) => auth.user(subject).cloned(),
        Err(_) => return internal_error(),
    };
    let Some(account) = account else {
        return unauthorized();
    };
    if persist_durable_session(&state, &token, &account, expires_at).is_err() {
        return internal_error();
    }
    HttpResponse::Ok()
        .cookie(
            Cookie::build("janus_access", token.clone())
                .path("/")
                .http_only(true)
                .same_site(actix_web::cookie::SameSite::Lax)
                .finish(),
        )
        .cookie(
            Cookie::build("janus_tenant", account.tenant.0.to_string())
                .path("/")
                .http_only(true)
                .same_site(actix_web::cookie::SameSite::Lax)
                .finish(),
        )
        .json(serde_json::json!({
            "token": token,
            "expiresAt": expires_at,
            "mfaRequired": account.mfa_enabled,
            "user": {
                "id": account.subject.0,
                "email": account.email,
                "tenantId": account.tenant.0,
                "permissions": account.permissions,
            }
        }))
}

async fn auth_refresh(request: HttpRequest, state: web::Data<ApiState>) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let token: String = rand::thread_rng() // tigerstyle: allow-direct-randomness — HTTP session refresh adapter
        .sample_iter(Alphanumeric)
        .take(48)
        .map(char::from)
        .collect();
    let expires_at = unix_now().saturating_add(3600);
    let result = match mutate_auth(&state, |auth| {
        auth.issue_session(&token, principal.subject, expires_at, true)
    }) {
        Ok(()) => Ok(()),
        Err(AuthMutationError::Domain(error)) => Err(error),
        Err(AuthMutationError::Persistence) => return internal_error(),
    };
    if result.is_ok() {
        if let Some(runtime) = &state.spacetime_runtime {
            if runtime
                .issue_auth_session(
                    &token,
                    principal.subject.0,
                    principal.tenant,
                    expires_at,
                    true,
                )
                .is_err()
            {
                let _rollback = mutate_auth(&state, |auth| auth.revoke_session(&token));
                return internal_error();
            }
        }
    }
    match result {
        Ok(()) => HttpResponse::Ok().json(serde_json::json!({
            "token": token,
            "expiresAt": expires_at,
        })),
        Err(_) => unauthorized(),
    }
}

async fn auth_signout(request: HttpRequest, state: web::Data<ApiState>) -> HttpResponse {
    if let Some(authorization) = request.headers().get("authorization") {
        if let Ok(value) = authorization.to_str() {
            if let Some(token) = bearer_token(value) {
                if let Some(runtime) = &state.spacetime_runtime {
                    if runtime.revoke_auth_session(token).is_err() {
                        return internal_error();
                    }
                }
                if let Ok(mut auth) = state.auth.lock() {
                    let snapshot = auth.snapshot();
                    if let Err(error) = auth.revoke_session(token) {
                        if !matches!(error, janus_core::AuthError::NotFound) {
                            return internal_error();
                        }
                    }
                    if let Some(store) = &state.auth_store {
                        if store.save(&auth).is_err() {
                            if auth.restore(&snapshot).is_err() {
                                return internal_error();
                            }
                            return internal_error();
                        }
                    }
                }
            }
        }
    }
    HttpResponse::Ok()
        .cookie(
            Cookie::build("janus_access", "")
                .path("/")
                .http_only(true)
                .max_age(actix_web::cookie::time::Duration::ZERO)
                .finish(),
        )
        .json(serde_json::json!({ "ok": true }))
}

async fn mfa_status(request: HttpRequest, state: web::Data<ApiState>) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let account = match state.auth.lock() {
        Ok(auth) => auth.user(principal.subject).cloned(),
        Err(_) => return internal_error(),
    };
    let Some(account) = account else {
        return unauthorized();
    };
    HttpResponse::Ok().json(serde_json::json!({
        "mfaRequired": true,
        "mfaConfigured": account.totp_enabled || account.email_otp_enabled,
        "totpEnabled": account.totp_enabled,
        "emailOtpEnabled": account.email_otp_enabled,
        "recoveryCodesRemaining": account.recovery_codes,
    }))
}

async fn telemetry_snapshot(request: HttpRequest, state: web::Data<ApiState>) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    if let Some(runtime) = &state.spacetime_runtime {
        if runtime
            .record_telemetry(
                principal.tenant,
                "janus.telemetry.snapshot",
                1.0,
                unix_now(),
            )
            .is_err()
        {
            return internal_error();
        }
    }
    let Ok(metrics) = telemetry_samples(&state) else {
        return internal_error();
    };
    let (traces, logs, profiles) = durable_observability(&state, principal.tenant);
    HttpResponse::Ok().json(serde_json::json!({
        "traces": traces,
        "metrics": metrics,
        "logs": logs,
        "profiles": profiles,
        "baggage": [],
    }))
}

fn durable_observability(
    state: &web::Data<ApiState>,
    tenant: TenantId,
) -> (
    Vec<serde_json::Value>,
    Vec<serde_json::Value>,
    Vec<serde_json::Value>,
) {
    let Some(runtime) = &state.spacetime_runtime else {
        return (
            Vec::with_capacity(MAX_DURABLE_OBSERVABILITY_ROWS),
            Vec::with_capacity(MAX_DURABLE_OBSERVABILITY_ROWS),
            Vec::with_capacity(MAX_DURABLE_OBSERVABILITY_ROWS),
        );
    };
    (
        durable_trace_views(runtime, tenant),
        durable_log_views(runtime, tenant),
        durable_profile_views(runtime, tenant),
    )
}

fn durable_trace_views(runtime: &SpacetimeRuntime, tenant: TenantId) -> Vec<serde_json::Value> {
    let mut traces = Vec::with_capacity(MAX_DURABLE_OBSERVABILITY_ROWS);
    traces.extend(
        runtime
            .trace_spans()
            .into_iter()
            .filter(|span| span.tenant_id == tenant.0)
            .take(MAX_DURABLE_OBSERVABILITY_ROWS)
            .map(|span| {
                serde_json::json!({
                    "traceId": span.trace_id,
                    "spanId": span.span_id,
                    "parentSpanId": span.parent_span_id,
                    "operationId": span.operation_id,
                    "name": span.name,
                    "startedAt": span.started_at,
                    "durationUs": span.duration_us,
                    "status": span.status,
                    "attributes": String::from_utf8_lossy(&span.attributes),
                })
            }),
    );
    traces
}

fn durable_log_views(runtime: &SpacetimeRuntime, tenant: TenantId) -> Vec<serde_json::Value> {
    let mut logs = Vec::with_capacity(MAX_DURABLE_OBSERVABILITY_ROWS);
    logs.extend(
        runtime
            .log_records()
            .into_iter()
            .filter(|record| record.tenant_id == tenant.0)
            .take(MAX_DURABLE_OBSERVABILITY_ROWS)
            .map(|record| {
                serde_json::json!({
                    "operationId": record.operation_id,
                    "traceId": record.trace_id,
                    "level": record.level,
                    "target": record.target,
                    "message": record.message,
                    "timestamp": record.timestamp,
                })
            }),
    );
    logs
}

fn durable_profile_views(runtime: &SpacetimeRuntime, tenant: TenantId) -> Vec<serde_json::Value> {
    let mut profiles = Vec::with_capacity(MAX_DURABLE_OBSERVABILITY_ROWS);
    profiles.extend(
        runtime
            .profile_indexes()
            .into_iter()
            .filter(|profile| profile.tenant_id == tenant.0)
            .take(MAX_DURABLE_OBSERVABILITY_ROWS)
            .map(|profile| {
                serde_json::json!({
                    "profileId": profile.profile_id,
                    "objectKey": profile.object_key,
                    "profileType": profile.profile_type,
                    "startedAt": profile.started_at,
                    "durationUs": profile.duration_us,
                    "checksum": profile.checksum,
                })
            }),
    );
    profiles
}

async fn disable_totp(request: HttpRequest, state: web::Data<ApiState>) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let result = match state.auth.lock() {
        Ok(mut auth) => {
            let snapshot = auth.snapshot();
            let result = auth.disable_totp(principal.subject);
            if result.is_ok() {
                if persist_mfa_projection(&state, &auth, principal.subject).is_err() {
                    if auth.restore(&snapshot).is_err() {
                        return internal_error();
                    }
                    return internal_error();
                }
                if let Some(store) = &state.auth_store {
                    if store.save(&auth).is_err() {
                        if auth.restore(&snapshot).is_err() {
                            return internal_error();
                        }
                        return internal_error();
                    }
                }
            }
            result
        }
        Err(_) => return internal_error(),
    };
    match result {
        Ok(()) => HttpResponse::Ok().json(serde_json::json!({
            "ok": true,
            "totpEnabled": false,
        })),
        Err(_) => unauthorized(),
    }
}

async fn disable_email_otp(request: HttpRequest, state: web::Data<ApiState>) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let result = match state.auth.lock() {
        Ok(mut auth) => {
            let snapshot = auth.snapshot();
            let result = auth.disable_email_otp(principal.subject);
            if result.is_ok() {
                if persist_mfa_projection(&state, &auth, principal.subject).is_err() {
                    if auth.restore(&snapshot).is_err() {
                        return internal_error();
                    }
                    return internal_error();
                }
                if let Some(store) = &state.auth_store {
                    if store.save(&auth).is_err() {
                        if auth.restore(&snapshot).is_err() {
                            return internal_error();
                        }
                        return internal_error();
                    }
                }
            }
            result
        }
        Err(_) => return internal_error(),
    };
    match result {
        Ok(()) => HttpResponse::Ok().json(serde_json::json!({
            "ok": true,
            "emailOtpEnabled": false,
        })),
        Err(_) => unauthorized(),
    }
}

async fn get_operation(path: web::Path<String>, state: web::Data<ApiState>) -> HttpResponse {
    let operation_id = path.into_inner();
    let operation = state
        .spacetime_runtime
        .as_ref()
        .and_then(|runtime| runtime.operation_by_correlation(&operation_id))
        .or_else(|| {
            state
                .operations
                .lock()
                .map_or(None, |registry| registry.get(&operation_id).cloned())
        });
    let Some(operation) = operation else {
        return HttpResponse::NotFound().json(serde_json::json!({
            "error": "operation not found"
        }));
    };
    HttpResponse::Ok().json(serde_json::json!({
        "id": operation.id,
        "kind": operation.kind,
        "status": format!("{:?}", operation.status).to_ascii_lowercase(),
        "correlationId": operation.correlation_id,
        "tenantId": operation.tenant_id,
        "createdAt": operation.created_at,
        "updatedAt": operation.updated_at,
        "result": operation.result,
        "failure": operation.failure,
    }))
}

fn record_completed_operation(
    state: &web::Data<ApiState>,
    principal: Principal,
    id: u64,
    kind: &str,
) -> Result<String, ()> {
    let operation_id = format!("{kind}-{id}");
    let tenant_id = format!("tenant-{}", principal.tenant.0);
    let operation = Operation::new(
        operation_id.clone(),
        kind,
        operation_id.clone(),
        tenant_id,
        unix_now(),
    )
    .map_err(|_| ())?;
    if let Some(runtime) = &state.spacetime_runtime {
        runtime
            .create_operation(
                durable_operation_id(&operation_id),
                principal.tenant,
                kind,
                &operation_id,
                operation.created_at,
            )
            .map_err(|_| ())?;
        runtime
            .transition_operation(
                durable_operation_id(&operation_id),
                principal.tenant,
                "succeeded",
                b"accepted",
                operation.updated_at,
            )
            .map_err(|_| ())?;
        persist_operation_evidence(runtime, principal, &operation_id, kind, "succeeded")?;
    }
    {
        let mut registry = state.operations.lock().map_err(|_| ())?;
        registry.insert(operation).map_err(|_| ())?;
        registry
            .transition(&operation_id, |operation| {
                operation
                    .complete_success("accepted", unix_now())
                    .map_err(|_| janus_core::OperationError::InvalidTransition)
            })
            .map_err(|_| ())?;
    }
    Ok(operation_id)
}

enum CommandAdmission {
    New(Option<String>),
    Replay(HttpResponse),
}

fn admit_durable_command(
    state: &web::Data<ApiState>,
    principal: Principal,
    operation_id: &str,
    key: &str,
    kind: &str,
    correlation_id: &str,
    payload: &[u8],
) -> Result<Option<CommandAdmission>, Box<HttpResponse>> {
    let Some(runtime) = &state.spacetime_runtime else {
        return Ok(None);
    };
    let command_id = durable_command_id(key);
    if let Some(existing) = runtime.command(command_id) {
        if existing.tenant_id != principal.tenant.0 || existing.correlation_id != correlation_id {
            return Err(Box::new(HttpResponse::Conflict().json(serde_json::json!({
                "error": "command_idempotency_conflict"
            }))));
        }
        if existing.status == "succeeded" {
            if let Ok(result) = String::from_utf8(existing.result) {
                return Ok(Some(CommandAdmission::Replay(
                    HttpResponse::Created()
                        .content_type("application/json")
                        .body(result),
                )));
            }
        }
        return Ok(Some(CommandAdmission::Replay(
            HttpResponse::Accepted()
                .json(serde_json::json!({"operationId": operation_id, "status": existing.status})),
        )));
    }
    runtime
        .admit_command(
            command_id,
            principal.tenant,
            kind,
            correlation_id,
            payload,
            unix_now(),
        )
        .map_err(|_| Box::new(internal_error()))?;
    Ok(None)
}

fn admit_local_command(
    state: &web::Data<ApiState>,
    operation_id: String,
    kind: &str,
    correlation_id: String,
    tenant_id: String,
) -> Result<CommandAdmission, Box<HttpResponse>> {
    let mut operations = state
        .operations
        .lock()
        .map_err(|_| Box::new(internal_error()))?;
    if let Some(existing) = operations.get(&operation_id) {
        if existing.tenant_id != tenant_id || existing.correlation_id != correlation_id {
            return Err(Box::new(HttpResponse::Conflict().json(serde_json::json!({
                "error": "command_idempotency_conflict"
            }))));
        }
        if matches!(existing.status, janus_core::OperationStatus::Succeeded) {
            if let Some(result) = existing.result.clone() {
                return Ok(CommandAdmission::Replay(
                    HttpResponse::Created()
                        .content_type("application/json")
                        .body(result),
                ));
            }
        }
        return Ok(CommandAdmission::Replay(HttpResponse::Accepted().json(
            serde_json::json!({"operationId": operation_id, "status": "pending"}),
        )));
    }
    let operation = Operation::new(
        operation_id.clone(),
        kind,
        correlation_id,
        tenant_id,
        unix_now(),
    )
    .map_err(|_| Box::new(internal_error()))?;
    operations
        .insert(operation)
        .map_err(|_| Box::new(internal_error()))?;
    drop(operations);
    Ok(CommandAdmission::New(Some(operation_id)))
}

fn admit_http_command(
    state: &web::Data<ApiState>,
    principal: Principal,
    request: &HttpRequest,
    kind: &str,
    payload: &[u8],
) -> Result<CommandAdmission, Box<HttpResponse>> {
    let Some(header) = request.headers().get("Idempotency-Key") else {
        return Ok(CommandAdmission::New(None));
    };
    let key = header
        .to_str()
        .map_err(|_| {
            Box::new(HttpResponse::BadRequest().json(serde_json::json!({
                "error": "invalid idempotency key"
            })))
        })?
        .trim();
    if key.is_empty() || key.len() > 128 {
        return Err(Box::new(HttpResponse::BadRequest().json(
            serde_json::json!({
                "error": "idempotency key must be between 1 and 128 bytes"
            }),
        )));
    }
    let operation_id = format!("command-{kind}-{key}");
    let fingerprint = STANDARD_NO_PAD.encode(Sha256::digest(payload));
    let correlation_id = format!("{key}:{fingerprint}");
    let tenant_id = format!("tenant-{}", principal.tenant.0);
    if let Some(admission) = admit_durable_command(
        state,
        principal,
        &operation_id,
        key,
        kind,
        &correlation_id,
        payload,
    )? {
        return Ok(admission);
    }
    admit_local_command(state, operation_id, kind, correlation_id, tenant_id)
}

fn complete_http_command(
    state: &web::Data<ApiState>,
    operation_id: Option<&str>,
    result: &str,
    actor_id: u64,
) -> Result<(), ()> {
    let Some(operation_id) = operation_id else {
        return Ok(());
    };
    let local_operation = state
        .operations
        .lock()
        .map_err(|_| ())?
        .get(operation_id)
        .cloned();
    if let Some(runtime) = &state.spacetime_runtime {
        let command = runtime.command_for_operation(operation_id).ok_or(())?;
        complete_durable_http_command(runtime, &command, operation_id, result, actor_id)?;
    }
    if local_operation.is_some() {
        let mut operations = state.operations.lock().map_err(|_| ())?;
        operations
            .transition(operation_id, |operation| {
                operation
                    .complete_success(result, unix_now())
                    .map_err(|_| janus_core::OperationError::InvalidTransition)
            })
            .map_err(|_| ())?;
    }
    Ok(())
}

fn complete_durable_http_command(
    runtime: &SpacetimeRuntime,
    command: &janus_core::module_bindings::CommandRecord,
    operation_id: &str,
    result: &str,
    actor_id: u64,
) -> Result<(), ()> {
    let tenant = TenantId(command.tenant_id);
    let key = command
        .correlation_id
        .split_once(':')
        .map(|(key, _)| key)
        .ok_or(())?;
    runtime
        .transition_command(
            command.id,
            tenant,
            "succeeded",
            result.as_bytes(),
            unix_now(),
        )
        .map_err(|_| ())?;
    let audit_payload = br#"{"status":"succeeded"}"#;
    runtime
        .append_event(tenant, command.id, &command.kind, audit_payload)
        .map_err(|_| ())?;
    runtime
        .append_audit_record(&AuditRecordWrite {
            tenant,
            actor_id,
            action: &command.kind,
            entity_type: "http_command",
            entity_id: operation_id,
            correlation_id: &command.correlation_id,
            causation_id: key,
            schema_version: 1,
            timestamp: unix_now(),
            payload: audit_payload,
        })
        .map_err(|_| ())
}

fn complete_failed_project_command(
    state: &web::Data<ApiState>,
    operation_id: &str,
    actor_id: u64,
) -> Result<(), ()> {
    complete_http_command(
        state,
        Some(operation_id),
        "project creation failed",
        actor_id,
    )
}

fn require_http_command_completion(
    state: &web::Data<ApiState>,
    operation_id: Option<&str>,
    body: &str,
    actor_id: u64,
) -> bool {
    complete_http_command(state, operation_id, body, actor_id).is_ok()
}

fn record_pending_operation(
    state: &web::Data<ApiState>,
    principal: Principal,
    id: u64,
    kind: &str,
) -> Result<String, ()> {
    let operation_id = format!("{kind}-{id}");
    record_pending_operation_named(state, principal, operation_id, id)
}

fn record_pending_operation_named(
    state: &web::Data<ApiState>,
    principal: Principal,
    operation_id: String,
    target_id: u64,
) -> Result<String, ()> {
    let operation = Operation::new(
        operation_id.clone(),
        operation_id.split('-').next().ok_or(())?,
        operation_id.clone(),
        format!("tenant-{}", principal.tenant.0),
        unix_now(),
    )
    .map_err(|_| ())?;
    if let Some(runtime) = &state.spacetime_runtime {
        runtime
            .create_operation(
                durable_operation_id(&operation_id),
                principal.tenant,
                &operation.kind,
                &operation_id,
                operation.created_at,
            )
            .map_err(|_| ())?;
        runtime
            .enqueue_job(
                durable_operation_id(&operation_id),
                &operation.kind,
                target_id,
                principal.tenant,
                1,
                3,
            )
            .map_err(|_| ())?;
        persist_operation_evidence(
            runtime,
            principal,
            &operation_id,
            &operation.kind,
            "pending",
        )?;
    }
    {
        let mut registry = state.operations.lock().map_err(|_| ())?;
        registry.insert(operation).map_err(|_| ())?;
    }
    Ok(operation_id)
}

fn persist_operation_evidence(
    runtime: &SpacetimeRuntime,
    principal: Principal,
    operation_id: &str,
    kind: &str,
    status: &str,
) -> Result<(), ()> {
    let payload = serde_json::to_vec(&serde_json::json!({ "status": status })).map_err(|_| ())?;
    let evidence_id = format!("{operation_id}:{status}");
    let evidence_command = durable_operation_id(&evidence_id);
    runtime
        .append_event(
            principal.tenant,
            evidence_command,
            &format!("{kind}.{status}"),
            &payload,
        )
        .map_err(|_| ())?;
    let action = format!("{kind}.{status}");
    runtime
        .append_audit_record(&AuditRecordWrite {
            tenant: principal.tenant,
            actor_id: principal.subject.0,
            action: &action,
            entity_type: "operation",
            entity_id: operation_id,
            correlation_id: operation_id,
            causation_id: &evidence_id,
            schema_version: 1,
            timestamp: unix_now(),
            payload: &payload,
        })
        .map_err(|_| ())
}

async fn list_builds(
    request: HttpRequest,
    state: web::Data<ApiState>,
    query: web::Query<BuildListQuery>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let project = query.project_id.map(EntityId);
    if let Some(runtime) = &state.spacetime_runtime {
        let builds = match runtime.build_components() {
            Ok(builds) => builds
                .into_iter()
                .filter(|(_, build)| {
                    build.tenant == principal.tenant
                        && project.is_none_or(|project_id| build.project == Some(project_id))
                })
                .map(|(id, build)| {
                    serde_json::json!({
                        "id": id.0,
                        "tenant": build.tenant.0,
                        "state": format!("{:?}", build.state).to_ascii_lowercase(),
                        "generation": build.generation.0,
                        "projectId": build.project.map(|value| value.0),
                        "sourceRef": build.source_ref,
                    })
                })
                .collect::<Vec<_>>(),
            Err(_) => return internal_error(),
        };
        return HttpResponse::Ok().json(serde_json::json!({ "builds": builds }));
    }
    let builds = match state.builds.lock() {
        Ok(service) => service.list_for_project(&principal, project),
        Err(_) => return internal_error(),
    };
    builds.map_or_else(
        |_| unauthorized(),
        |builds| {
            HttpResponse::Ok().json(serde_json::json!({
                "builds": builds.into_iter().map(|(id, build)| serde_json::json!({
                    "id": id.0,
                    "tenant": build.tenant.0,
                    "state": format!("{:?}", build.state).to_ascii_lowercase(),
                    "generation": build.generation.0,
                    "projectId": build.project.map(|value| value.0),
                    "sourceRef": build.source_ref,
                })).collect::<Vec<_>>()
            }))
        },
    )
}

async fn get_build(
    request: HttpRequest,
    state: web::Data<ApiState>,
    path: web::Path<u64>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let build_id = path.into_inner();
    if let Some(runtime) = &state.spacetime_runtime {
        let build = match runtime.build_components() {
            Ok(builds) => builds
                .into_iter()
                .find(|(id, build)| id.0 == build_id && build.tenant == principal.tenant),
            Err(_) => return internal_error(),
        };
        return match build {
            Some((_, build)) => HttpResponse::Ok().json(serde_json::json!({
                "id": build_id,
                "tenant": build.tenant.0,
                "state": format!("{:?}", build.state).to_ascii_lowercase(),
                "generation": build.generation.0,
            })),
            None => build_error(BuildServiceError::NotFound),
        };
    }
    let build = match state.builds.lock() {
        Ok(service) => service.build(&principal, EntityId(build_id)),
        Err(_) => return internal_error(),
    };
    match build {
        Ok(build) => HttpResponse::Ok().json(serde_json::json!({
            "id": build_id,
            "tenant": build.tenant.0,
            "state": format!("{:?}", build.state).to_ascii_lowercase(),
            "generation": build.generation.0,
        })),
        Err(error) => build_error(error),
    }
}

async fn get_build_logs(
    request: HttpRequest,
    state: web::Data<ApiState>,
    path: web::Path<u64>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let build_id = path.into_inner();
    if let Some(runtime) = &state.spacetime_runtime {
        let build = match runtime.build_components() {
            Ok(builds) => builds
                .into_iter()
                .find(|(id, build)| id.0 == build_id && build.tenant == principal.tenant),
            Err(_) => return internal_error(),
        };
        return match build {
            Some((_, build)) => HttpResponse::Ok().json(serde_json::json!({
                "buildId": build_id,
                "status": format!("{:?}", build.state).to_ascii_lowercase(),
                "lines": [],
                "statusEvents": [],
            })),
            None => build_error(BuildServiceError::NotFound),
        };
    }
    let build = match state.builds.lock() {
        Ok(service) => service.build(&principal, EntityId(build_id)),
        Err(_) => return internal_error(),
    };
    match build {
        Ok(build) => HttpResponse::Ok().json(serde_json::json!({
            "buildId": build_id,
            "status": format!("{:?}", build.state).to_ascii_lowercase(),
            "lines": [],
            "statusEvents": [],
        })),
        Err(error) => build_error(error),
    }
}

async fn list_runners(request: HttpRequest, state: web::Data<ApiState>) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let runners = if let Some(runtime) = &state.spacetime_runtime {
        match runtime.runner_components() {
            Ok(runners) => runners
                .into_iter()
                .filter(|runner| runner.tenant == principal.tenant)
                .collect(),
            Err(_) => return internal_error(),
        }
    } else {
        match state.runners.lock() {
            Ok(registry) => registry.list_for_tenant(principal.tenant),
            Err(_) => return internal_error(),
        }
    };
    HttpResponse::Ok().json(serde_json::json!({
            "runners": runners.into_iter().map(|runner| serde_json::json!({
                "id": runner.id,
                "tenant": runner.tenant.0,
                "state": format!("{:?}", runner.state).to_ascii_lowercase(),
                "capabilities": runner.capabilities,
                "last_heartbeat": runner.last_heartbeat,
                "lease_until": runner.lease_until,
            })).collect::<Vec<_>>()
    }))
}

async fn register_runner(
    request: HttpRequest,
    state: web::Data<ApiState>,
    input: web::Json<RunnerRegisterInput>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let now = unix_now();
    if let Some(runtime) = &state.spacetime_runtime {
        if runtime
            .register_runner(
                input.id,
                principal.tenant,
                &input.capabilities,
                now,
                input.lease_seconds,
            )
            .is_err()
        {
            return internal_error();
        }
    }
    let result = match state.runners.lock() {
        Ok(mut registry) => registry.register(
            input.id,
            principal.tenant,
            input.capabilities.clone(),
            now,
            input.lease_seconds,
        ),
        Err(_) => return internal_error(),
    };
    match result {
        Ok(()) => HttpResponse::Ok().json(serde_json::json!({ "id": input.id })),
        Err(error) => runner_error(error),
    }
}

async fn heartbeat_runner(
    request: HttpRequest,
    state: web::Data<ApiState>,
    input: web::Json<RunnerHeartbeatInput>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    if let Some(runtime) = &state.spacetime_runtime {
        let capabilities = match runtime.runner_components() {
            Ok(runners) => match runners
                .into_iter()
                .find(|runner| runner.id == input.id && runner.tenant == principal.tenant)
            {
                Some(runner) => runner.capabilities,
                None => {
                    return HttpResponse::NotFound().json(serde_json::json!({
                        "error": "runner not found"
                    }))
                }
            },
            Err(_) => return internal_error(),
        };
        if runtime
            .register_runner(
                input.id,
                principal.tenant,
                &capabilities,
                unix_now(),
                input.lease_seconds,
            )
            .is_err()
        {
            return internal_error();
        }
    }
    let result = match state.runners.lock() {
        Ok(mut registry) => {
            registry.heartbeat(input.id, principal.tenant, unix_now(), input.lease_seconds)
        }
        Err(_) => return internal_error(),
    };
    match result {
        Ok(()) => HttpResponse::Ok().json(serde_json::json!({ "id": input.id, "status": "ok" })),
        Err(error) => runner_error(error),
    }
}

async fn list_projects(request: HttpRequest, state: web::Data<ApiState>) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    if let Some(runtime) = &state.spacetime_runtime {
        let projects = runtime
            .project_components()
            .into_iter()
            .filter(|project| project.tenant == principal.tenant)
            .map(|project| ProjectView::from(&project))
            .collect::<Vec<_>>();
        return HttpResponse::Ok().json(serde_json::json!({ "projects": projects }));
    }
    list_projects_offline(&state, principal)
}

fn list_projects_offline(state: &web::Data<ApiState>, principal: Principal) -> HttpResponse {
    state.projects.lock().map_or_else(
        |_| internal_error(),
        |service| match service.list(&principal) {
            Ok(projects) => HttpResponse::Ok().json(serde_json::json!({
                "projects": projects.iter().map(ProjectView::from).collect::<Vec<_>>()
            })),
            Err(error) => project_error(error),
        },
    )
}

fn durable_project(
    state: &web::Data<ApiState>,
    principal: Principal,
    project_id: EntityId,
) -> Option<Project> {
    state.spacetime_runtime.as_ref().and_then(|runtime| {
        runtime
            .project_components()
            .into_iter()
            .find(|project| project.id == project_id && project.tenant == principal.tenant)
    })
}

async fn validate_repository(
    request: HttpRequest,
    state: web::Data<ApiState>,
    input: web::Json<RepositoryValidationInput>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    if input.project_id == 0 {
        return HttpResponse::BadRequest().json(serde_json::json!({
            "error": "projectId is required"
        }));
    }
    let project = if state.spacetime_runtime.is_some() {
        match durable_project(&state, principal, EntityId(input.project_id)) {
            Some(project) => project,
            None => {
                return HttpResponse::NotFound().json(serde_json::json!({
                    "error": "project not found"
                }));
            }
        }
    } else {
        match state.projects.lock() {
            Ok(service) => match service.project(&principal, EntityId(input.project_id)) {
                Ok(project) => project,
                Err(ProjectServiceError::NotFound) => {
                    return HttpResponse::NotFound().json(serde_json::json!({
                        "error": "project not found"
                    }));
                }
                Err(error) => return project_error(error),
            },
            Err(_) => return internal_error(),
        }
    };
    let result =
        LocalRepositoryValidator.validate(&project.repo_url, &project.repo_branch, unix_now());
    HttpResponse::Ok().json(serde_json::json!({ "result": result }))
}

fn validate_repo_import(input: &RepoImportInput) -> Result<(), Box<HttpResponse>> {
    if input.project_id == 0 || input.repo_url.trim().is_empty() {
        let error = if input.project_id == 0 {
            "projectId is required"
        } else {
            "repoUrl is required"
        };
        return Err(Box::new(
            HttpResponse::BadRequest().json(serde_json::json!({ "error": error })),
        ));
    }
    let provider = if input.provider.trim().is_empty() {
        "github"
    } else {
        input.provider.trim()
    };
    if !provider.eq_ignore_ascii_case("github") {
        return Err(Box::new(HttpResponse::BadRequest().json(
            serde_json::json!({
                "error": "unsupported provider"
            }),
        )));
    }
    if !is_github_https_url(&input.repo_url) {
        return Err(Box::new(HttpResponse::BadRequest().json(
            serde_json::json!({
                "error": "repoUrl must be a GitHub URL (github.com) using HTTPS"
            }),
        )));
    }
    Ok(())
}

fn enqueue_repo_build(
    state: &web::Data<ApiState>,
    principal: Principal,
    input: &RepoImportInput,
) -> Result<EntityId, Box<HttpResponse>> {
    let project_id = EntityId(input.project_id);
    if state.spacetime_runtime.is_some() {
        if durable_project(state, principal, project_id).is_none() {
            return Err(Box::new(HttpResponse::NotFound().json(serde_json::json!({
                "error": "project not found"
            }))));
        }
    } else {
        match state.projects.lock() {
            Ok(service) => match service.project(&principal, project_id) {
                Ok(_) => {}
                Err(error) => return Err(Box::new(project_error(error))),
            },
            Err(_) => return Err(Box::new(internal_error())),
        }
    }
    let mut service = state
        .builds
        .lock()
        .map_err(|_| Box::new(internal_error()))?;
    let snapshot = service.snapshot();
    let build_id = service
        .submit_for_project(
            &principal,
            project_id,
            if input.branch.trim().is_empty() {
                "main"
            } else {
                input.branch.trim()
            },
        )
        .map_err(|error| Box::new(build_error(error)))?;
    drop(service);
    if let Some(runtime) = &state.spacetime_runtime {
        let branch = if input.branch.trim().is_empty() {
            "main"
        } else {
            input.branch.trim()
        };
        if runtime
            .create_build(
                build_id.0,
                principal.tenant,
                BuildWrite {
                    source: input.repo_url.trim().to_owned(),
                    project_id: Some(project_id.0),
                    source_ref: branch.to_owned(),
                },
            )
            .is_err()
        {
            let mut service = state
                .builds
                .lock()
                .map_err(|_| Box::new(internal_error()))?;
            service
                .restore_snapshot(snapshot)
                .map_err(|_| Box::new(internal_error()))?;
            drop(service);
            return Err(Box::new(internal_error()));
        }
    }
    Ok(build_id)
}

async fn import_repository(
    request: HttpRequest,
    state: web::Data<ApiState>,
    input: web::Json<RepoImportInput>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    if let Err(response) = validate_repo_import(&input) {
        return *response;
    }
    let command_id = match serde_json::to_vec(&*input)
        .map_err(|_| Box::new(internal_error()))
        .and_then(|payload| {
            admit_http_command(&state, principal, &request, "build.submit", &payload)
        }) {
        Ok(CommandAdmission::New(command_id)) => command_id,
        Ok(CommandAdmission::Replay(response)) => return response,
        Err(response) => return *response,
    };
    let project_id = EntityId(input.project_id);
    let build_id = match enqueue_repo_build(&state, principal, &input) {
        Ok(id) => id,
        Err(response) => return *response,
    };
    let operation_id = match command_id.clone() {
        Some(operation_id) => operation_id,
        None => match record_pending_operation(&state, principal, build_id.0, "build.enqueue") {
            Ok(operation_id) => operation_id,
            Err(()) => return internal_error(),
        },
    };
    let body = serde_json::json!({
        "operationId": operation_id,
        "status": "pending",
        "submittedAt": unix_now(),
        "pollUrl": format!("/api/v1/operations/{operation_id}"),
        "projectId": project_id.0,
        "provider": "github",
        "repoUrl": input.repo_url.trim(),
        "branch": if input.branch.trim().is_empty() { "main" } else { input.branch.trim() },
    });
    let body = serde_json::to_string(&body).map_err(|_| ()).ok();
    match body {
        Some(body) => {
            if complete_http_command(&state, command_id.as_deref(), &body, principal.subject.0)
                .is_err()
            {
                return internal_error();
            }
            HttpResponse::Accepted()
                .content_type("application/json")
                .body(body)
        }
        None => internal_error(),
    }
}

fn is_github_https_url(value: &str) -> bool {
    let Some(authority) = value.trim().strip_prefix("https://") else {
        return false;
    };
    let host = authority
        .split(['/', '?', '#'])
        .next()
        .unwrap_or_default()
        .trim()
        .to_ascii_lowercase();
    host == "github.com" || host.ends_with(".github.com")
}

async fn upload_source_bundle(
    request: HttpRequest,
    state: web::Data<ApiState>,
    payload: web::Payload,
) -> HttpResponse {
    upload_file(request, state, payload, UploadKind::Source).await
}

async fn upload_artifact(
    request: HttpRequest,
    state: web::Data<ApiState>,
    payload: web::Payload,
) -> HttpResponse {
    upload_file(request, state, payload, UploadKind::Artifact).await
}

#[derive(Clone, Copy)]
enum UploadKind {
    Source,
    Artifact,
}

#[derive(Clone, Copy)]
enum UploadMetadataError {
    MetadataAndCleanup,
    MetadataOnly,
}

async fn upload_file(
    request: HttpRequest,
    state: web::Data<ApiState>,
    payload: web::Payload,
    kind: UploadKind,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let max_bytes = match kind {
        UploadKind::Source => MAX_SOURCE_UPLOAD_BYTES,
        UploadKind::Artifact => MAX_ARTIFACT_UPLOAD_BYTES,
    };
    let form = match read_multipart(&request, payload, max_bytes).await {
        Ok(form) => form,
        Err(response) => return *response,
    };
    let upload = match validate_upload(&state, &principal, &form, kind) {
        Ok(upload) => upload,
        Err(response) => return *response,
    };
    if !store_upload(&state, &upload.key, &form.bytes) {
        return HttpResponse::ServiceUnavailable().json(serde_json::json!({
            "error": "failed to store upload"
        }));
    }
    if let Err(error) = persist_upload_metadata(&state, &principal, &form, &upload.key) {
        return upload_metadata_error_response(error);
    }
    let operation_id = match record_upload_operation(
        &state,
        &principal,
        upload.project_id,
        kind,
        &upload.source_ref,
        &upload.key,
    ) {
        Ok(operation_id) => operation_id,
        Err(response) => {
            return upload_operation_error_response(
                &state,
                principal.tenant,
                &upload.key,
                *response,
            );
        }
    };
    let runtime = form
        .fields
        .get("artifactRuntime")
        .filter(|value| !value.trim().is_empty())
        .cloned()
        .unwrap_or_else(|| detect_uploaded_runtime(&form.bytes).to_owned());
    let message = match kind {
        UploadKind::Source => "source bundle accepted",
        UploadKind::Artifact => "artifact accepted",
    };
    HttpResponse::Accepted().json(serde_json::json!({
        "operationId": operation_id,
        "status": "pending",
        "submittedAt": unix_now(),
        "pollUrl": format!("/api/v1/operations/{operation_id}"),
        "message": message,
        "objectKey": upload.key,
        "artifactRuntime": runtime,
    }))
}

fn upload_metadata_error_response(error: UploadMetadataError) -> HttpResponse {
    let message = match error {
        UploadMetadataError::MetadataAndCleanup => {
            "object metadata failed and upload cleanup failed"
        }
        UploadMetadataError::MetadataOnly => "failed to persist object metadata",
    };
    HttpResponse::ServiceUnavailable().json(serde_json::json!({ "error": message }))
}

fn upload_operation_error_response(
    state: &web::Data<ApiState>,
    tenant: TenantId,
    key: &str,
    response: HttpResponse,
) -> HttpResponse {
    let cleanup_succeeded = state
        .object_store
        .lock()
        .is_ok_and(|mut store| store.delete(key).is_ok());
    if cleanup_succeeded {
        if let Some(runtime) = &state.spacetime_runtime {
            if runtime.delete_object_metadata(key, tenant).is_err()
                || runtime.delete_artifact_metadata(key, tenant).is_err()
            {
                return HttpResponse::ServiceUnavailable().json(serde_json::json!({
                    "error": "object metadata cleanup failed"
                }));
            }
        }
        response
    } else {
        HttpResponse::ServiceUnavailable().json(serde_json::json!({
            "error": "upload cleanup failed"
        }))
    }
}

fn validate_upload(
    state: &web::Data<ApiState>,
    principal: &Principal,
    form: &MultipartData,
    kind: UploadKind,
) -> Result<ValidatedUpload, Box<HttpResponse>> {
    let Some(project_id) = form
        .fields
        .get("projectId")
        .and_then(|value| value.trim().parse::<u64>().ok())
        .filter(|value| *value != 0)
    else {
        return Err(Box::new(HttpResponse::BadRequest().json(
            serde_json::json!({
                "error": "projectId is required"
            }),
        )));
    };
    let project_id = EntityId(project_id);
    authorize_upload_project(state, principal, project_id)?;
    if form.filename.trim().is_empty() {
        return Err(Box::new(HttpResponse::BadRequest().json(
            serde_json::json!({
                "error": "file is required"
            }),
        )));
    }
    if matches!(kind, UploadKind::Source) && !is_valid_source_bundle_filename(&form.filename) {
        return Err(Box::new(HttpResponse::BadRequest().json(
            serde_json::json!({
                "error": "invalid file type: source bundles must be .zip, .tar.gz, or .tgz"
            }),
        )));
    }
    let filename = safe_upload_filename(&form.filename);
    let source_ref = form
        .fields
        .get("sourceRef")
        .map_or("upload", String::as_str)
        .trim()
        .to_owned();
    let prefix = match kind {
        UploadKind::Source => "source",
        UploadKind::Artifact => "artifact",
    };
    let key = format!(
        "{prefix}/{}/{}/{}-{filename}",
        principal.tenant.0,
        project_id.0,
        unix_now(),
    );
    Ok(ValidatedUpload {
        project_id,
        source_ref,
        key,
    })
}

fn authorize_upload_project(
    state: &web::Data<ApiState>,
    principal: &Principal,
    project_id: EntityId,
) -> Result<(), Box<HttpResponse>> {
    if state.spacetime_runtime.is_some() {
        if durable_project(state, *principal, project_id).is_none() {
            return Err(Box::new(HttpResponse::NotFound().json(serde_json::json!({
                "error": "project not found"
            }))));
        }
        return Ok(());
    }
    state.projects.lock().map_or_else(
        |_| Err(Box::new(internal_error())),
        |service| {
            service
                .project(principal, project_id)
                .map_err(|error| Box::new(project_error(error)))
                .map(|_| ())
        },
    )
}

fn store_upload(state: &web::Data<ApiState>, key: &str, bytes: &[u8]) -> bool {
    state
        .object_store
        .lock()
        .map_err(|_| ())
        .and_then(|mut store| store.put(key, bytes).map(|_| ()).map_err(|_| ()))
        .is_ok()
}

fn persist_upload_metadata(
    state: &web::Data<ApiState>,
    principal: &Principal,
    form: &MultipartData,
    key: &str,
) -> Result<(), UploadMetadataError> {
    let Some(runtime) = &state.spacetime_runtime else {
        return Ok(());
    };
    let content_type = form
        .fields
        .get("contentType")
        .map_or("application/octet-stream", String::as_str);
    let checksum = format!("{:x}", Sha256::digest(&form.bytes));
    let created_at = unix_now();
    let retention_until = env_value("JANUS_OBJECT_RETENTION_SECONDS")
        .and_then(|value| value.parse::<u64>().ok())
        .filter(|seconds| *seconds != 0)
        .map_or(0, |seconds| created_at.saturating_add(seconds));
    let size = u64::try_from(form.bytes.len()).unwrap_or(u64::MAX);
    let metadata = UploadMetadata {
        key,
        tenant: principal.tenant,
        size,
        content_type,
        checksum: &checksum,
        created_at,
        retention_until,
    };
    if runtime
        .put_object_metadata(&ObjectMetadataWrite {
            key: metadata.key,
            tenant: metadata.tenant,
            size: metadata.size,
            content_type: metadata.content_type,
            checksum: metadata.checksum,
            created_at: metadata.created_at,
            retention_until: metadata.retention_until,
        })
        .is_ok()
        && persist_artifact_metadata(runtime, &metadata)
    {
        return Ok(());
    }
    let cleanup_succeeded = state
        .object_store
        .lock()
        .is_ok_and(|mut store| store.delete(key).is_ok());
    if cleanup_succeeded {
        let metadata_cleanup_succeeded = runtime
            .delete_object_metadata(key, principal.tenant)
            .is_ok();
        if metadata_cleanup_succeeded {
            Err(UploadMetadataError::MetadataOnly)
        } else {
            Err(UploadMetadataError::MetadataAndCleanup)
        }
    } else {
        Err(UploadMetadataError::MetadataAndCleanup)
    }
}

fn persist_artifact_metadata(runtime: &SpacetimeRuntime, metadata: &UploadMetadata<'_>) -> bool {
    let kind = if metadata.key.starts_with("source/") {
        "source"
    } else {
        "artifact"
    };
    runtime
        .put_artifact_metadata(&ArtifactMetadataWrite {
            id: metadata.key,
            tenant: metadata.tenant,
            build_id: None,
            deployment_id: None,
            kind,
            object_key: metadata.key,
            size: metadata.size,
            checksum: metadata.checksum,
            content_type: metadata.content_type,
            status: "available",
            created_at: metadata.created_at,
            retention_until: metadata.retention_until,
        })
        .is_ok()
}

fn record_upload_operation(
    state: &web::Data<ApiState>,
    principal: &Principal,
    project_id: EntityId,
    kind: UploadKind,
    source_ref: &str,
    source_key: &str,
) -> Result<String, Box<HttpResponse>> {
    match kind {
        UploadKind::Source => {
            let source_ref = if source_ref.is_empty() {
                "main"
            } else {
                source_ref
            };
            let (build_id, snapshot) = match state.builds.lock() {
                Ok(mut service) => {
                    let snapshot = service.snapshot();
                    let build_id = service
                        .submit_for_project(principal, project_id, source_ref)
                        .map_err(build_error)?;
                    (build_id, snapshot)
                }
                Err(_) => return Err(Box::new(internal_error())),
            };
            if let Some(runtime) = &state.spacetime_runtime {
                if runtime
                    .create_build(
                        build_id.0,
                        principal.tenant,
                        BuildWrite {
                            source: source_key.to_owned(),
                            project_id: Some(project_id.0),
                            source_ref: source_ref.to_owned(),
                        },
                    )
                    .is_err()
                {
                    let mut service = state
                        .builds
                        .lock()
                        .map_err(|_| Box::new(internal_error()))?;
                    service
                        .restore_snapshot(snapshot)
                        .map_err(|_| Box::new(internal_error()))?;
                    drop(service);
                    return Err(Box::new(internal_error()));
                }
            }
            record_pending_operation(state, *principal, build_id.0, "build.enqueue")
                .map_err(|()| Box::new(internal_error()))
        }
        UploadKind::Artifact => {
            let operation_id = format!("artifact.upload-{}-{}", project_id.0, unix_now());
            record_pending_operation_named(state, *principal, operation_id, project_id.0)
                .map_err(|()| Box::new(internal_error()))
        }
    }
}

async fn read_multipart(
    request: &HttpRequest,
    mut payload: web::Payload,
    max_bytes: usize,
) -> Result<MultipartData, Box<HttpResponse>> {
    let content_type = request
        .headers()
        .get("content-type")
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default();
    let Some(boundary) = content_type.split(';').find_map(|part| {
        part.trim()
            .strip_prefix("boundary=")
            .map(|value| value.trim_matches('"').to_owned())
    }) else {
        return Err(Box::new(HttpResponse::BadRequest().json(
            serde_json::json!({
                "error": "invalid multipart form"
            }),
        )));
    };
    if boundary.is_empty() || boundary.len() > 200 {
        return Err(Box::new(HttpResponse::BadRequest().json(
            serde_json::json!({
                "error": "invalid multipart form"
            }),
        )));
    }
    let mut body = Vec::with_capacity(MULTIPART_INITIAL_CAPACITY_BYTES);
    while let Some(chunk) = payload.next().await {
        let Ok(chunk) = chunk else {
            return Err(Box::new(HttpResponse::BadRequest().json(
                serde_json::json!({
                    "error": "invalid multipart form"
                }),
            )));
        };
        if body.len().saturating_add(chunk.len()) > max_bytes + MAX_MULTIPART_OVERHEAD_BYTES {
            return Err(Box::new(HttpResponse::PayloadTooLarge().json(
                serde_json::json!({
                    "error": "upload exceeds configured size limit"
                }),
            )));
        }
        body.extend_from_slice(&chunk);
    }
    parse_multipart(&body, &boundary).ok_or_else(|| {
        Box::new(HttpResponse::BadRequest().json(serde_json::json!({
            "error": "invalid multipart form"
        })))
    })
}

fn parse_multipart(body: &[u8], boundary: &str) -> Option<MultipartData> {
    let marker = format!("--{boundary}").into_bytes();
    let mut cursor = 0;
    let mut fields = HashMap::with_capacity(8);
    let mut file = None;
    let mut filename = String::with_capacity(128);
    while let Some(start) = find_bytes(body, &marker, cursor) {
        let content_start = start.checked_add(marker.len())?;
        let separator_end = content_start.checked_add(2)?;
        if body.get(content_start..separator_end) == Some(b"--") {
            break;
        }
        let content_start = separator_end;
        let end = find_bytes(body, &marker, content_start)?;
        let part = body.get(content_start..end)?.strip_suffix(b"\r\n")?;
        let header_end = find_bytes(part, b"\r\n\r\n", 0)?;
        let headers = std::str::from_utf8(part.get(..header_end)?).ok()?;
        let data_start = header_end.checked_add(4)?;
        let data = part.get(data_start..)?;
        let name = disposition_value(headers, "name")?;
        if name == "file" {
            filename = disposition_value(headers, "filename").unwrap_or_default();
            file = Some(data.to_vec());
        } else {
            fields.insert(name, String::from_utf8(data.to_vec()).ok()?);
        }
        cursor = end;
    }
    Some(MultipartData {
        fields,
        filename,
        bytes: file?,
    })
}

fn find_bytes(haystack: &[u8], needle: &[u8], start: usize) -> Option<usize> {
    haystack
        .get(start..)?
        .windows(needle.len())
        .position(|window| window == needle)
        .map(|offset| start + offset)
}

fn disposition_value(headers: &str, key: &str) -> Option<String> {
    let marker = format!("{key}=\"");
    let start = headers.find(&marker)?.checked_add(marker.len())?;
    let end = headers.get(start..)?.find('"')?;
    Some(headers.get(start..start + end)?.to_owned())
}

fn safe_upload_filename(value: &str) -> String {
    value
        .rsplit(['/', '\\'])
        .next()
        .unwrap_or("upload.bin")
        .chars()
        .map(|character| {
            if character.is_ascii_alphanumeric() || matches!(character, '.' | '-' | '_') {
                character
            } else {
                '_'
            }
        })
        .take(128)
        .collect::<String>()
}

fn is_valid_source_bundle_filename(value: &str) -> bool {
    let value = value.to_ascii_lowercase();
    value.ends_with(".tar.gz")
        || Path::new(&value)
            .extension()
            .is_some_and(|extension| extension == "zip" || extension == "tgz")
}

fn detect_uploaded_runtime(bytes: &[u8]) -> &'static str {
    let text = String::from_utf8_lossy(bytes).to_ascii_lowercase();
    if text.contains("wasi:http/") || text.contains("wasi:http") {
        "wasm/wasi-http-component"
    } else {
        "wasm/wasi-command"
    }
}

async fn list_git_providers(request: HttpRequest, state: web::Data<ApiState>) -> HttpResponse {
    let Some(_principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    HttpResponse::Ok().json(serde_json::json!({
        "providers": [{
            "id": "github",
            "name": "GitHub",
            "connected": false,
            "githubStartUrl": "/api/v1/git/providers/github/oauth/start",
        }]
    }))
}

async fn list_deployments(
    request: HttpRequest,
    state: web::Data<ApiState>,
    query: web::Query<BuildListQuery>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    if let Some(runtime) = &state.spacetime_runtime {
        let Ok(deployments) =
            durable_deployment_views(runtime, principal.tenant, query.project_id.map(EntityId))
        else {
            return internal_error();
        };
        return HttpResponse::Ok().json(serde_json::json!({ "deployments": deployments }));
    }
    let deployments = match state.builds.lock() {
        Ok(service) => {
            service.list_deployments_for_project(&principal, query.project_id.map(EntityId))
        }
        Err(_) => return internal_error(),
    };
    deployments.map_or_else(
        |_| unauthorized(),
        |deployments| {
            HttpResponse::Ok().json(serde_json::json!({
                "deployments": deployments.into_iter().map(|(id, deployment)| serde_json::json!({
                    "id": id.0,
                    "tenant": deployment.tenant.0,
                    "build": deployment.build.0,
                    "projectId": deployment.project.map(|project| project.0),
                    "revision": deployment.revision,
                    "targetType": deployment.target_type,
                    "targetRef": deployment.target_ref,
                    "runnerId": deployment.preferred_runner,
                    "env": environment_json(&deployment.environment),
                    "state": format!("{:?}", deployment.state).to_ascii_lowercase(),
                    "generation": deployment.generation.0,
                    "runtimeId": optional_json_string(&deployment.runtime_id),
                    "runtimeMode": optional_json_string(&deployment.runtime_mode),
                    "runtimeStatus": optional_json_string(&deployment.runtime_status),
                    "runtimeEndpoint": optional_json_string(&deployment.runtime_endpoint),
                })).collect::<Vec<_>>()
            }))
        },
    )
}

fn durable_deployment_views(
    runtime: &SpacetimeRuntime,
    tenant: TenantId,
    project: Option<EntityId>,
) -> Result<Vec<serde_json::Value>, String> {
    runtime.deployment_components().map(|deployments| {
        deployments
            .into_iter()
            .filter(|(_, deployment)| {
                deployment.tenant == tenant
                    && project.is_none_or(|id| deployment.project == Some(id))
            })
            .map(|(id, deployment)| {
                serde_json::json!({
                    "id": id.0,
                    "tenant": deployment.tenant.0,
                    "build": deployment.build.0,
                    "projectId": deployment.project.map(|project| project.0),
                    "revision": deployment.revision,
                    "targetType": deployment.target_type,
                    "targetRef": deployment.target_ref,
                    "runnerId": deployment.preferred_runner,
                    "env": environment_json(&deployment.environment),
                    "state": format!("{:?}", deployment.state).to_ascii_lowercase(),
                    "generation": deployment.generation.0,
                    "runtimeId": optional_json_string(&deployment.runtime_id),
                    "runtimeMode": optional_json_string(&deployment.runtime_mode),
                    "runtimeStatus": optional_json_string(&deployment.runtime_status),
                    "runtimeEndpoint": optional_json_string(&deployment.runtime_endpoint),
                })
            })
            .collect()
    })
}

fn optional_json_string(value: &str) -> serde_json::Value {
    if value.is_empty() {
        serde_json::Value::Null
    } else {
        serde_json::Value::String(value.to_owned())
    }
}

fn environment_json(entries: &[(String, String)]) -> serde_json::Value {
    let mut environment = serde_json::Map::with_capacity(entries.len());
    for (key, value) in entries {
        environment.insert(key.clone(), serde_json::Value::String(value.clone()));
    }
    serde_json::Value::Object(environment)
}

async fn list_domains(request: HttpRequest, state: web::Data<ApiState>) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    if let Some(runtime) = &state.spacetime_runtime {
        let Ok(domains) = durable_domain_views(runtime, principal.tenant) else {
            return internal_error();
        };
        return HttpResponse::Ok().json(serde_json::json!({ "domains": domains }));
    }
    let deployments = match state.builds.lock() {
        Ok(service) => service.list_deployment_projects(&principal),
        Err(_) => return internal_error(),
    };
    let deployments = match deployments {
        Ok(deployments) => deployments,
        Err(error) => return deployment_error(error),
    };
    let Ok(projects) = state.projects.lock() else {
        return internal_error();
    };
    let domains = deployments
        .into_iter()
        .filter_map(|(deployment_id, deployment, project_id)| {
            let project_id = project_id?;
            let project = projects.project(&principal, project_id).ok()?;
            let revision = deployment.revision.max(1);
            Some(serde_json::json!({
                "id": format!("domain-{}", deployment_id.0),
                "deploymentId": deployment_id.0,
                "domain": format!("{}-r{revision}.janus.local", project.slug),
                "type": "temporary",
                "projectId": project_id.0,
                "deploymentStatus": format!("{:?}", deployment.state).to_ascii_lowercase(),
                "revision": revision,
                "runtimeStatus": optional_json_string(&deployment.runtime_status),
                "runtimeMode": optional_json_string(&deployment.runtime_mode),
                "runtimeEndpoint": optional_json_string(&deployment.runtime_endpoint),
            }))
        })
        .collect::<Vec<_>>();
    HttpResponse::Ok().json(serde_json::json!({ "domains": domains }))
}

fn durable_domain_views(
    runtime: &SpacetimeRuntime,
    tenant: TenantId,
) -> Result<Vec<serde_json::Value>, String> {
    let projects = runtime
        .project_components()
        .into_iter()
        .filter(|project| project.tenant == tenant)
        .collect::<Vec<_>>();
    runtime.deployment_components().map(|deployments| {
        deployments
            .into_iter()
            .filter(|(_, deployment)| deployment.tenant == tenant)
            .filter_map(|(deployment_id, deployment)| {
                let project_id = deployment.project?;
                let project = projects.iter().find(|project| project.id == project_id)?;
                let revision = deployment.revision.max(1);
                Some(serde_json::json!({
                    "id": format!("domain-{}", deployment_id.0),
                    "deploymentId": deployment_id.0,
                    "domain": format!("{}-r{revision}.janus.local", project.slug),
                    "type": "temporary",
                    "projectId": project_id.0,
                    "deploymentStatus": format!("{:?}", deployment.state).to_ascii_lowercase(),
                    "revision": revision,
                    "runtimeStatus": optional_json_string(&deployment.runtime_status),
                    "runtimeMode": optional_json_string(&deployment.runtime_mode),
                    "runtimeEndpoint": optional_json_string(&deployment.runtime_endpoint),
                }))
            })
            .collect()
    })
}

fn create_deployment_local(
    state: &web::Data<ApiState>,
    principal: Principal,
    input: &DeploymentInput,
    build_id: u64,
    target_type: &str,
    environment: Vec<(String, String)>,
) -> Result<EntityId, Box<HttpResponse>> {
    let mut service = state
        .builds
        .lock()
        .map_err(|_| Box::new(internal_error()))?;
    service
        .create_deployment_with_metadata(
            &principal,
            input.project_id.map(EntityId),
            EntityId(build_id),
            DeploymentMetadata {
                target_type: target_type.to_owned(),
                target_ref: input.target_ref.trim().to_owned(),
                preferred_runner: input.runner_id.trim().to_owned(),
                environment,
            },
        )
        .map_err(|error| Box::new(deployment_error(error)))
}

fn persist_durable_deployment(
    state: &web::Data<ApiState>,
    principal: Principal,
    input: &DeploymentInput,
    validated: &ValidatedDeploymentInput,
    id: EntityId,
) -> Result<(), ()> {
    let Some(runtime) = &state.spacetime_runtime else {
        return Ok(());
    };
    let environment = serde_json::to_vec(&validated.environment).map_err(|_| ())?;
    runtime
        .create_deployment(
            id.0,
            principal.tenant,
            validated.build_id,
            DeploymentWrite {
                project_id: input.project_id,
                revision: 1,
                target_type: validated.target_type.clone(),
                target_ref: input.target_ref.trim().to_owned(),
                preferred_runner: input.runner_id.trim().to_owned(),
                environment,
                runtime_id: String::with_capacity(1),
                runtime_mode: String::with_capacity(1),
                runtime_endpoint: String::with_capacity(1),
                runtime_status: String::with_capacity(1),
            },
        )
        .map_err(|_| ())
}

fn validate_deployment_input(
    input: &DeploymentInput,
) -> Result<ValidatedDeploymentInput, Box<HttpResponse>> {
    let legacy_payload = input.build_id.is_some() && input.build_job_id.is_none();
    if !legacy_payload && input.project_id.is_none() {
        return Err(Box::new(HttpResponse::BadRequest().json(
            serde_json::json!({
                "error": "projectId is required"
            }),
        )));
    }
    let Some(build_id) = input.build_id.or(input.build_job_id) else {
        return Err(Box::new(HttpResponse::BadRequest().json(
            serde_json::json!({
                "error": "buildJobId is required"
            }),
        )));
    };
    let target_type = input.target_type.trim();
    if !target_type.is_empty() && !matches!(target_type, "preview" | "production") {
        return Err(Box::new(HttpResponse::BadRequest().json(
            serde_json::json!({
                "error": "targetType must be preview or production"
            }),
        )));
    }
    if input.target_ref.len() > 256 || input.runner_id.len() > 256 {
        return Err(Box::new(HttpResponse::BadRequest().json(
            serde_json::json!({
                "error": "deployment target metadata is too large"
            }),
        )));
    }
    let mut environment = input
        .env
        .iter()
        .map(|(key, value)| (key.trim().to_owned(), value.clone()))
        .collect::<Vec<_>>();
    environment.sort_by(|left, right| left.0.cmp(&right.0));
    if environment.len() > janus_core::MAX_DEPLOYMENT_ENV_ENTRIES
        || environment.iter().any(|(key, value)| {
            !janus_core::valid_deployment_env_key(key) || key.len() > 128 || value.len() > 4096
        })
    {
        return Err(Box::new(HttpResponse::BadRequest().json(
            serde_json::json!({
                "error": "deployment environment is invalid or too large"
            }),
        )));
    }
    Ok(ValidatedDeploymentInput {
        build_id,
        target_type: target_type.to_owned(),
        environment,
    })
}

fn create_and_persist_deployment(
    state: &web::Data<ApiState>,
    principal: Principal,
    input: &DeploymentInput,
    validated: &ValidatedDeploymentInput,
) -> Result<EntityId, Box<HttpResponse>> {
    let snapshot = match state.builds.lock() {
        Ok(service) => service.snapshot(),
        Err(_) => return Err(Box::new(internal_error())),
    };
    let result = create_deployment_local(
        state,
        principal,
        input,
        validated.build_id,
        &validated.target_type,
        validated.environment.clone(),
    );
    let id = result?;
    if persist_durable_deployment(state, principal, input, validated, id).is_err() {
        if let Ok(mut service) = state.builds.lock() {
            if service.restore_snapshot(snapshot).is_err() {
                drop(service);
                return Err(Box::new(internal_error()));
            }
            drop(service);
        }
        return Err(Box::new(internal_error()));
    }
    Ok(id)
}

fn restore_build_snapshot(
    state: &web::Data<ApiState>,
    snapshot: (WorldSnapshot, u64, u64),
) -> Result<(), ()> {
    let mut service = state.builds.lock().map_err(|_| ())?;
    service.restore_snapshot(snapshot).map_err(|_| ())?;
    drop(service);
    Ok(())
}

async fn create_deployment(
    request: HttpRequest,
    state: web::Data<ApiState>,
    input: web::Json<DeploymentInput>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let validated = match validate_deployment_input(&input) {
        Ok(values) => values,
        Err(response) => return *response,
    };
    let command_id = match serde_json::to_vec(&*input)
        .map_err(|_| Box::new(internal_error()))
        .and_then(|payload| {
            admit_http_command(&state, principal, &request, "deployment.create", &payload)
        }) {
        Ok(CommandAdmission::New(command_id)) => command_id,
        Ok(CommandAdmission::Replay(response)) => return response,
        Err(response) => return *response,
    };
    let result = create_and_persist_deployment(&state, principal, &input, &validated);
    match result {
        Ok(id) => {
            let operation_id = match command_id.clone() {
                Some(operation_id) => operation_id,
                None => match record_pending_operation_named(
                    &state,
                    principal,
                    format!("deployment.create-{}", id.0),
                    id.0,
                ) {
                    Ok(operation_id) => operation_id,
                    Err(()) => return internal_error(),
                },
            };
            let body = serde_json::json!({"id": id.0, "operationId": operation_id});
            let body = serde_json::to_string(&body).map_err(|_| ()).ok();
            match body {
                Some(body) => {
                    if complete_http_command(
                        &state,
                        command_id.as_deref(),
                        &body,
                        principal.subject.0,
                    )
                    .is_err()
                    {
                        return internal_error();
                    }
                    HttpResponse::Created()
                        .content_type("application/json")
                        .body(body)
                }
                None => internal_error(),
            }
        }
        Err(error) => *error,
    }
}

async fn get_deployment(
    request: HttpRequest,
    state: web::Data<ApiState>,
    path: web::Path<u64>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let deployment_id = EntityId(path.into_inner());
    if let Some(runtime) = &state.spacetime_runtime {
        let deployment = match runtime.deployment_components() {
            Ok(deployments) => deployments
                .into_iter()
                .find(|(id, deployment)| {
                    *id == deployment_id && deployment.tenant == principal.tenant
                })
                .map(|(_, deployment)| deployment),
            Err(_) => return internal_error(),
        };
        return match deployment {
            Some(deployment) => HttpResponse::Ok().json(serde_json::json!({
                "tenant": deployment.tenant.0,
                "build": deployment.build.0,
                "projectId": deployment.project.map(|project| project.0),
                "revision": deployment.revision,
                "targetType": deployment.target_type,
                "targetRef": deployment.target_ref,
                "runnerId": deployment.preferred_runner,
                "env": environment_json(&deployment.environment),
                "state": format!("{:?}", deployment.state).to_ascii_lowercase(),
                "generation": deployment.generation.0,
                "runtimeId": optional_json_string(&deployment.runtime_id),
                "runtimeMode": optional_json_string(&deployment.runtime_mode),
                "runtimeStatus": optional_json_string(&deployment.runtime_status),
                "runtimeEndpoint": optional_json_string(&deployment.runtime_endpoint),
            })),
            None => deployment_error(BuildServiceError::NotFound),
        };
    }
    let deployment = match state.builds.lock() {
        Ok(service) => service.deployment(&principal, deployment_id),
        Err(_) => return internal_error(),
    };
    match deployment {
        Ok(deployment) => HttpResponse::Ok().json(serde_json::json!({
            "tenant": deployment.tenant.0,
            "build": deployment.build.0,
            "projectId": deployment.project.map(|project| project.0),
            "revision": deployment.revision,
            "targetType": deployment.target_type,
            "targetRef": deployment.target_ref,
            "runnerId": deployment.preferred_runner,
            "env": environment_json(&deployment.environment),
            "state": format!("{:?}", deployment.state).to_ascii_lowercase(),
            "generation": deployment.generation.0,
            "runtimeId": optional_json_string(&deployment.runtime_id),
            "runtimeMode": optional_json_string(&deployment.runtime_mode),
            "runtimeStatus": optional_json_string(&deployment.runtime_status),
            "runtimeEndpoint": optional_json_string(&deployment.runtime_endpoint),
        })),
        Err(error) => deployment_error(error),
    }
}

async fn delete_deployment(
    request: HttpRequest,
    state: web::Data<ApiState>,
    path: web::Path<u64>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let deployment_id = EntityId(path.into_inner());
    let snapshot = match state.builds.lock() {
        Ok(service) => service.snapshot(),
        Err(_) => return internal_error(),
    };
    let result = match state.builds.lock() {
        Ok(mut service) => service.stop_deployment(&principal, deployment_id),
        Err(_) => return internal_error(),
    };
    match result {
        Ok(()) => {
            if let Some(runtime) = &state.spacetime_runtime {
                let Some(deployment) = runtime
                    .deployment_components()
                    .ok()
                    .and_then(|deployments| {
                        deployments.into_iter().find(|(id, deployment)| {
                            *id == deployment_id && deployment.tenant == principal.tenant
                        })
                    })
                    .map(|(_, deployment)| deployment)
                else {
                    return internal_error();
                };
                if runtime
                    .update_deployment_runtime(
                        deployment_id.0,
                        principal.tenant,
                        deployment.generation.0,
                        "stopped",
                        DeploymentRuntimeWrite {
                            runtime_id: deployment.runtime_id,
                            runtime_mode: deployment.runtime_mode,
                            runtime_endpoint: deployment.runtime_endpoint,
                            runtime_status: "stopped".to_owned(),
                        },
                    )
                    .is_err()
                {
                    if restore_build_snapshot(&state, snapshot).is_err() {
                        return internal_error();
                    }
                    return internal_error();
                }
            }
            HttpResponse::NoContent().finish()
        }
        Err(error) => deployment_error(error),
    }
}

fn create_project_local(
    state: &web::Data<ApiState>,
    principal: Principal,
    project: Project,
) -> Result<EntityId, ProjectServiceError> {
    let mut service = state
        .projects
        .lock()
        .map_err(|_| ProjectServiceError::Rejected)?;
    let snapshot = service.snapshot();
    let id = service.create(&principal, project)?;
    let projected = service
        .project(&principal, id)
        .map_err(|_| ProjectServiceError::Rejected)?;
    let committed = commit_project(
        &web::Data::clone(state),
        &mut service,
        snapshot,
        |reducer| {
            reducer.create_project(projected.id.0, projected.tenant, project_write(&projected))
        },
    );
    drop(service);
    if committed.is_err() {
        return Err(ProjectServiceError::Rejected);
    }
    Ok(id)
}

fn project_creation_response(
    state: &web::Data<ApiState>,
    principal: Principal,
    command_id: Option<&str>,
    id: EntityId,
) -> HttpResponse {
    let operation_id = match command_id {
        Some(operation_id) => operation_id.to_owned(),
        None => match record_completed_operation(state, principal, id.0, "project.create") {
            Ok(operation_id) => operation_id,
            Err(()) => return internal_error(),
        },
    };
    let body = serde_json::json!({"id": id.0, "operationId": operation_id});
    let Ok(body) = serde_json::to_string(&body) else {
        return internal_error();
    };
    if !require_http_command_completion(state, command_id, &body, principal.subject.0) {
        return internal_error();
    }
    HttpResponse::Created()
        .content_type("application/json")
        .body(body)
}

async fn create_project(
    request: HttpRequest,
    state: web::Data<ApiState>,
    input: web::Json<ProjectInput>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let command_id = match serde_json::to_vec(&*input)
        .map_err(|_| Box::new(internal_error()))
        .and_then(|payload| {
            admit_http_command(&state, principal, &request, "project.create", &payload)
        }) {
        Ok(CommandAdmission::New(command_id)) => command_id,
        Ok(CommandAdmission::Replay(response)) => return response,
        Err(response) => return *response,
    };
    let now = unix_now();
    let project = Project {
        id: EntityId(0),
        tenant: principal.tenant,
        name: input.name.clone(),
        slug: input.slug.clone(),
        description: input.description.clone(),
        status: "active".to_owned(),
        repo_provider: input.repo_provider.clone(),
        repo_url: input.repo_url.clone(),
        repo_branch: input.repo_branch.clone(),
        repo_check: None,
        created_at: now,
        updated_at: now,
    };
    let result = create_project_local(&state, principal, project);
    match result {
        Ok(id) => project_creation_response(&state, principal, command_id.as_deref(), id),
        Err(error) => {
            let response = project_error(error);
            if let Some(operation_id) = command_id.as_deref() {
                if complete_failed_project_command(&state, operation_id, principal.subject.0)
                    .is_err()
                {
                    return internal_error();
                }
            }
            response
        }
    }
}

async fn update_project(
    request: HttpRequest,
    state: web::Data<ApiState>,
    path: web::Path<u64>,
    input: web::Json<ProjectInput>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let project_id = EntityId(path.into_inner());
    let result = match state.projects.lock() {
        Ok(mut service) => {
            let snapshot = service.snapshot();
            let result = service.update_repository(
                &principal,
                project_id,
                input.repo_provider.clone(),
                input.repo_url.clone(),
                input.repo_branch.clone(),
            );
            match result {
                Ok(()) => match service.project(&principal, project_id) {
                    Ok(projected) => {
                        if commit_project(&state, &mut service, snapshot, |reducer| {
                            reducer.update_project(
                                projected.id.0,
                                projected.tenant,
                                project_write(&projected),
                                &projected.status,
                            )
                        })
                        .is_err()
                        {
                            Err(ProjectServiceError::Rejected)
                        } else {
                            Ok(())
                        }
                    }
                    Err(error) => Err(error),
                },
                Err(error) => Err(error),
            }
        }
        Err(_) => return internal_error(),
    };
    match result {
        Ok(()) => HttpResponse::Ok().json(serde_json::json!({ "status": "updated" })),
        Err(error) => project_error(error),
    }
}

async fn delete_project(
    request: HttpRequest,
    state: web::Data<ApiState>,
    path: web::Path<u64>,
) -> HttpResponse {
    let Some(principal) = authenticate(&request, &state) else {
        return unauthorized();
    };
    let project_id = EntityId(path.into_inner());
    let result = match state.projects.lock() {
        Ok(mut service) => {
            let snapshot = service.snapshot();
            match service.delete(&principal, project_id) {
                Ok(()) => {
                    if commit_project(&state, &mut service, snapshot, |reducer| {
                        reducer.delete_project(project_id.0, principal.tenant)
                    })
                    .is_err()
                    {
                        Err(ProjectServiceError::Rejected)
                    } else {
                        Ok(())
                    }
                }
                Err(error) => Err(error),
            }
        }
        Err(_) => return internal_error(),
    };
    match result {
        Ok(()) => HttpResponse::NoContent().finish(),
        Err(error) => project_error(error),
    }
}

fn authenticate(request: &HttpRequest, state: &ApiState) -> Option<Principal> {
    let token = request
        .headers()
        .get("authorization")
        .and_then(|value| value.to_str().ok())
        .and_then(bearer_token)
        .map(str::to_owned)
        .or_else(|| {
            request
                .cookie("janus_access")
                .map(|cookie| cookie.value().to_owned())
        })?;
    let tenant = request
        .headers()
        .get("x-janus-tenant")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.parse().ok())
        .or_else(|| {
            request
                .cookie("janus_tenant")
                .and_then(|cookie| cookie.value().parse().ok())
        })
        .map(TenantId)?;
    let now = unix_now();
    state
        .auth
        .lock()
        .ok()?
        .authenticate_principal(&token, tenant, now)
        .ok()
}

fn project_error(error: ProjectServiceError) -> HttpResponse {
    let (status, body) = match error {
        ProjectServiceError::Authorization => (403, "forbidden"),
        ProjectServiceError::Capacity => (503, "capacity_exhausted"),
        ProjectServiceError::InvalidProject(_) => (400, "invalid_project"),
        ProjectServiceError::NotFound => (404, "project_not_found"),
        ProjectServiceError::Rejected => (409, "project_rejected"),
    };
    HttpResponse::build(response_status(status)).json(serde_json::json!({ "error": body }))
}

fn commit_project<F>(
    state: &web::Data<ApiState>,
    service: &mut Projects,
    snapshot: WorldSnapshot,
    mutation: F,
) -> Result<(), ()>
where
    F: FnOnce(&mut dyn ProjectReducerPort) -> Result<(), janus_core::SpacetimeDbError>,
{
    let Ok(mut durable) = state.durable_projects.lock() else {
        service.restore(snapshot).map_err(|_| ())?;
        return Err(());
    };
    let Some(reducer) = durable.as_mut() else {
        return Ok(());
    };
    if mutation(&mut **reducer).is_err() {
        service.restore(snapshot).map_err(|_| ())?;
        return Err(());
    }
    Ok(())
}

fn deployment_error(error: BuildServiceError) -> HttpResponse {
    let (status, body) = match error {
        BuildServiceError::Authorization => (403, "forbidden"),
        BuildServiceError::Capacity => (503, "capacity_exhausted"),
        BuildServiceError::NotFound => (404, "deployment_not_found"),
        BuildServiceError::Rejected => (409, "deployment_rejected"),
    };
    HttpResponse::build(response_status(status)).json(serde_json::json!({ "error": body }))
}

fn build_error(error: BuildServiceError) -> HttpResponse {
    let (status, body) = match error {
        BuildServiceError::Authorization => (403, "forbidden"),
        BuildServiceError::Capacity => (503, "capacity_exhausted"),
        BuildServiceError::NotFound => (404, "build_not_found"),
        BuildServiceError::Rejected => (409, "build_rejected"),
    };
    HttpResponse::build(response_status(status)).json(serde_json::json!({ "error": body }))
}

fn runner_error(error: RunnerError) -> HttpResponse {
    let (status, body) = match error {
        RunnerError::AlreadyExists => (409, "runner_exists"),
        RunnerError::Capacity => (503, "capacity_exhausted"),
        RunnerError::NotFound => (404, "runner_not_found"),
        RunnerError::TenantBoundary => (403, "forbidden"),
        RunnerError::InvalidCapabilities => (400, "invalid_capabilities"),
        RunnerError::InvalidState => (409, "runner_invalid_state"),
    };
    HttpResponse::build(response_status(status)).json(serde_json::json!({ "error": body }))
}

fn response_status(status: u16) -> actix_web::http::StatusCode {
    actix_web::http::StatusCode::from_u16(status)
        .unwrap_or(actix_web::http::StatusCode::INTERNAL_SERVER_ERROR)
}

fn unauthorized() -> HttpResponse {
    HttpResponse::Unauthorized().json(serde_json::json!({ "error": "authentication_required" }))
}

fn internal_error() -> HttpResponse {
    HttpResponse::InternalServerError().json(serde_json::json!({ "error": "internal_error" }))
}

fn unix_now() -> u64 {
    SystemTime::now() // tigerstyle: allow-direct-time — HTTP adapter clock for session expiry
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs())
}

fn durable_command_id(key: &str) -> u64 {
    let digest = Sha256::digest(key.as_bytes());
    let mut bytes = [0_u8; 8];
    bytes.copy_from_slice(&digest[..8]);
    u64::from_le_bytes(bytes).max(1)
}

fn durable_operation_id(operation_id: &str) -> u64 {
    durable_command_id(operation_id)
}

fn durable_email_id(subject: janus_core::SubjectId, timestamp: u64) -> u64 {
    durable_command_id(&format!("email-otp-{}-{timestamp}", subject.0))
}

#[cfg(test)]
fn test_object_store() -> Objects {
    let root = std::env::temp_dir().join(format!(
        "janus-api-objects-{}-{}",
        std::process::id(),
        unix_now()
    ));
    Box::new(
        LocalObjectStore::<256, { MAX_SOURCE_UPLOAD_BYTES * 2 }>::new(root)
            .expect("test object store creates"),
    )
}

async fn dispatch(
    request: HttpRequest,
    state: web::Data<ApiState>,
    service: web::Data<HttpService>,
) -> HttpResponse {
    let Some(method) = method(request.method()) else {
        return HttpResponse::BadRequest()
            .content_type("application/json")
            .body(r#"{"error":"bad_request"}"#);
    };
    let principal = authenticate(&request, &state);
    let tenant = principal
        .as_ref()
        .map_or(TenantId(0), |authenticated| authenticated.tenant);
    let request = JanusRequest {
        method,
        path: request.path().to_owned(),
        principal,
        tenant,
    };
    let response = service.dispatch(&request);
    HttpResponse::build(
        actix_web::http::StatusCode::from_u16(response.status)
            .unwrap_or(actix_web::http::StatusCode::INTERNAL_SERVER_ERROR),
    )
    .content_type("application/json")
    .body(response.body)
}

const fn method(method: &actix_web::http::Method) -> Option<Method> {
    match *method {
        actix_web::http::Method::GET => Some(Method::Get),
        actix_web::http::Method::POST => Some(Method::Post),
        actix_web::http::Method::PATCH => Some(Method::Patch),
        actix_web::http::Method::DELETE => Some(Method::Delete),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use actix_web::{
        body::to_bytes,
        dev::{Service, ServiceResponse},
        http::StatusCode,
        test, App,
    };
    use std::fmt::Debug;

    async fn assert_status<S, R, B, E>(
        app: &S,
        request: R,
        expected: StatusCode,
    ) -> ServiceResponse<B>
    where
        S: Service<R, Response = ServiceResponse<B>, Error = E>,
        E: Debug,
    {
        let response = test::call_service(app, request).await;
        assert_eq!(response.status(), expected);
        response
    }

    fn test_api_state(auth: Auth) -> web::Data<ApiState> {
        web::Data::new(ApiState {
            projects: Mutex::new(Projects::new()),
            durable_projects: Mutex::new(None),
            builds: Mutex::new(Builds::new()),
            runners: Mutex::new(Runners::new()),
            operations: Mutex::new(Operations::default()),
            auth: Mutex::new(auth),
            auth_store: None,
            email_otps: Mutex::new(EmailOtps::new()),
            telemetry: Mutex::new(TelemetryBuffer::new()),
            object_store: Mutex::new(test_object_store()),
            spacetime_runtime: None,
            spacetime_required: false,
        })
    }

    fn multipart(fields: &[(&str, &str)], filename: &str, data: &[u8]) -> Vec<u8> {
        let boundary = "janus-test-boundary";
        let mut body = Vec::new();
        for (name, value) in fields {
            body.extend_from_slice(
                format!(
                    "--{boundary}\r\nContent-Disposition: form-data; name=\"{name}\"\r\n\r\n{value}\r\n"
                )
                .as_bytes(),
            );
        }
        body.extend_from_slice(
            format!(
                concat!(
                    "--{}\r\nContent-Disposition: form-data; name=\"file\"; ",
                    "filename=\"{}\"\r\nContent-Type: application/octet-stream\r\n\r\n"
                ),
                boundary, filename
            )
            .as_bytes(),
        );
        body.extend_from_slice(data);
        body.extend_from_slice(format!("\r\n--{boundary}--\r\n").as_bytes());
        body
    }

    #[actix_web::test]
    async fn deployment_environment_is_projected_as_a_json_object() {
        let value = environment_json(&[
            ("BETA".to_owned(), "two".to_owned()),
            ("ALPHA".to_owned(), "one".to_owned()),
        ]);
        assert_eq!(value["ALPHA"], "one");
        assert_eq!(value["BETA"], "two");
    }

    #[actix_web::test]
    async fn project_contract_accepts_camel_case_repository_fields() {
        let input: ProjectInput = serde_json::from_value(serde_json::json!({
            "name": "Janus",
            "slug": "janus",
            "repoProvider": "github",
            "repoUrl": "https://github.com/example/janus",
            "repoBranch": "main"
        }))
        .expect("camel-case project input deserializes");
        assert_eq!(input.repo_provider, "github");
        assert_eq!(input.repo_url, "https://github.com/example/janus");
        assert_eq!(input.repo_branch, "main");

        let view = ProjectView {
            id: 1,
            tenant: 3,
            name: input.name,
            slug: input.slug,
            description: input.description,
            status: "active".to_owned(),
            repo_provider: input.repo_provider,
            repo_url: input.repo_url,
            repo_branch: input.repo_branch,
        };
        let value = serde_json::to_value(view).expect("project view serializes");
        assert_eq!(value["repoProvider"], "github");
        assert_eq!(value["repoUrl"], "https://github.com/example/janus");
        assert_eq!(value["repoBranch"], "main");
        assert!(value.get("repo_provider").is_none());
    }

    use janus_core::{SubjectId, UserAccount};

    async fn compatibility_public_routes<S, B, E>(app: &S)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        B::Error: Debug,
        E: Debug,
    {
        compatibility_health_routes(app).await;
        compatibility_signup_routes(app).await;
    }

    async fn compatibility_health_routes<S, B, E>(app: &S)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        E: Debug,
    {
        let response = assert_status(
            app,
            test::TestRequest::get().uri("/healthz").to_request(),
            StatusCode::OK,
        )
        .await;
        let health: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(health["status"], "ok");

        let response = assert_status(
            app,
            test::TestRequest::get().uri("/readyz").to_request(),
            StatusCode::OK,
        )
        .await;
        let ready: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(ready["status"], "ready");

        let response = assert_status(
            app,
            test::TestRequest::get().uri("/metrics").to_request(),
            StatusCode::OK,
        )
        .await;
        assert!(test::read_body(response).await.is_empty());
    }

    async fn compatibility_signup_routes<S, B, E>(app: &S)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        E: Debug,
    {
        let response = assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/auth/signup")
                .set_json(serde_json::json!({
                    "email": "new@example.com",
                    "password": "a sufficiently long password"
                }))
                .to_request(),
            StatusCode::CREATED,
        )
        .await;
        let signup: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(signup["user"]["email"], "new@example.com");
        assert_eq!(signup["user"]["tenantId"], 4);

        assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/auth/signup")
                .set_json(serde_json::json!({
                    "email": "new@example.com",
                    "password": "a sufficiently long password"
                }))
                .to_request(),
            StatusCode::CONFLICT,
        )
        .await;

        assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/deployments")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .set_json(serde_json::json!({ "buildJobId": 1 }))
                .to_request(),
            StatusCode::BAD_REQUEST,
        )
        .await;
    }

    async fn compatibility_auth_routes<S, B, E>(app: &S)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        E: Debug,
    {
        compatibility_identity_mfa_routes(app).await;
        compatibility_signin_routes(app).await;
    }

    async fn compatibility_identity_mfa_routes<S, B, E>(app: &S)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        E: Debug,
    {
        let response = assert_status(
            app,
            test::TestRequest::get()
                .uri("/api/v1/auth/me")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        let identity: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(identity["user"]["email"], "operator@example.com");
        assert_eq!(identity["user"]["tenantId"], 3);

        let response = assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/mfa/totp/enroll")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        let enrollment: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(enrollment["totpEnabled"], false);
        assert!(enrollment["secret"]
            .as_str()
            .is_some_and(|value| !value.is_empty()));
        assert!(enrollment["qrCodeUri"]
            .as_str()
            .is_some_and(|value| value.starts_with("otpauth://totp/")));

        assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/mfa/totp/verify")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .set_json(serde_json::json!({ "code": "000000" }))
                .to_request(),
            StatusCode::BAD_REQUEST,
        )
        .await;
    }

    async fn compatibility_signin_routes<S, B, E>(app: &S)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        E: Debug,
    {
        let response = assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/auth/signin")
                .set_json(serde_json::json!({
                    "email": "bcrypt@example.com",
                    "password": "password"
                }))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        let signed_in: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(signed_in["user"]["email"], "bcrypt@example.com");
        let signed_in_token = signed_in["token"].as_str().unwrap();

        let response = assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/auth/refresh")
                .insert_header(("Authorization", format!("Bearer {signed_in_token}")))
                .insert_header(("x-janus-tenant", "3"))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        let refreshed: serde_json::Value = test::read_body_json(response).await;
        assert!(refreshed["token"]
            .as_str()
            .is_some_and(|value| !value.is_empty() && value != signed_in_token));
        assert!(refreshed["expiresAt"].as_u64().is_some());

        assert_status(
            app,
            test::TestRequest::get()
                .uri("/api/v1/auth/me")
                .insert_header(("Authorization", format!("Bearer {signed_in_token}")))
                .insert_header(("x-janus-tenant", "3"))
                .to_request(),
            StatusCode::OK,
        )
        .await;

        let response = assert_status(
            app,
            test::TestRequest::get()
                .uri("/api/v1/mfa/status")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        let mfa: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(mfa["mfaRequired"], true);
        assert_eq!(mfa["mfaConfigured"], false);
        assert_eq!(mfa["recoveryCodesRemaining"], 0);
    }

    async fn compatibility_observability_routes<S, B, E>(app: &S)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        E: Debug,
    {
        let response = assert_status(
            app,
            test::TestRequest::get()
                .uri("/api/v1/telemetry")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        let telemetry: serde_json::Value = test::read_body_json(response).await;
        assert!(telemetry["traces"].is_array());
        assert!(telemetry["metrics"].is_array());
        assert!(telemetry["logs"].is_array());

        let response = assert_status(
            app,
            test::TestRequest::get()
                .uri("/api/v1/git/providers")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        let providers: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(providers["providers"][0]["id"], "github");
        assert_eq!(providers["providers"][0]["connected"], false);

        let response = assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/auth/signout")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        let signed_out: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(signed_out["ok"], true);

        assert_status(
            app,
            test::TestRequest::get()
                .uri("/api/v1/auth/me")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .to_request(),
            StatusCode::UNAUTHORIZED,
        )
        .await;
    }

    async fn compatibility_resource_routes<S, B, E>(app: &S, state: &web::Data<ApiState>)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        B::Error: Debug,
        E: Debug,
    {
        compatibility_build_routes(app, state).await;
        compatibility_collection_routes(app).await;
    }

    async fn compatibility_build_routes<S, B, E>(app: &S, state: &web::Data<ApiState>)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        B::Error: Debug,
        E: Debug,
    {
        {
            let mut auth = state.auth.lock().unwrap();
            auth.issue_session("test-token", SubjectId(7), unix_now() + 3600, true)
                .unwrap();
        }

        let response = assert_status(
            app,
            test::TestRequest::get()
                .uri("/api/v1/builds")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        assert_eq!(
            to_bytes(response.into_body()).await.unwrap().as_ref(),
            br#"{"builds":[]}"#
        );

        let build_id = {
            let mut builds = state.builds.lock().unwrap();
            builds
                .submit(
                    &Principal::new(SubjectId(7), TenantId(3))
                        .with_permission(janus_core::Permission::ReadBuilds)
                        .with_permission(janus_core::Permission::SubmitBuilds),
                )
                .unwrap()
        };
        let response = assert_status(
            app,
            test::TestRequest::get()
                .uri(&format!("/api/v1/builds/{}", build_id.0))
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        let build: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(build["id"], build_id.0);
        assert_eq!(build["state"], "running");

        let response = assert_status(
            app,
            test::TestRequest::get()
                .uri(&format!("/api/v1/builds/{}/logs", build_id.0))
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        let logs: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(logs["buildId"], build_id.0);
        assert_eq!(logs["status"], "running");
    }

    async fn compatibility_collection_routes<S, B, E>(app: &S)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        B::Error: Debug,
        E: Debug,
    {
        compatibility_empty_collection_routes(app).await;
        compatibility_runner_routes(app).await;
    }

    async fn compatibility_empty_collection_routes<S, B, E>(app: &S)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        B::Error: Debug,
        E: Debug,
    {
        let expected_empty: [(&str, &[u8]); 3] = [
            ("/api/v1/deployments", br#"{"deployments":[]}"#),
            ("/api/v1/domains", br#"{"domains":[]}"#),
            ("/api/v1/runners", br#"{"runners":[]}"#),
        ];
        for (uri, body) in expected_empty {
            let response = assert_status(
                app,
                test::TestRequest::get()
                    .uri(uri)
                    .insert_header(("Authorization", "Bearer test-token"))
                    .insert_header(("x-janus-tenant", "3"))
                    .to_request(),
                StatusCode::OK,
            )
            .await;
            assert_eq!(to_bytes(response.into_body()).await.unwrap().as_ref(), body);
        }

        assert_status(
            app,
            test::TestRequest::get()
                .uri("/api/v1/operations/missing")
                .to_request(),
            StatusCode::NOT_FOUND,
        )
        .await;
    }

    async fn compatibility_runner_routes<S, B, E>(app: &S)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        B::Error: Debug,
        E: Debug,
    {
        assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/runners/register")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .set_json(serde_json::json!({
                    "id": 7,
                    "capabilities": ["wasm"],
                    "lease_seconds": 60
                }))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/runners/heartbeat")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .set_json(serde_json::json!({ "id": 7, "lease_seconds": 60 }))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/deployments")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .set_json(serde_json::json!({ "build_id": 1 }))
                .to_request(),
            StatusCode::CONFLICT,
        )
        .await;
        for method in ["get", "delete"] {
            let request = match method {
                "get" => test::TestRequest::get()
                    .uri("/api/v1/deployments/1")
                    .insert_header(("Authorization", "Bearer test-token"))
                    .insert_header(("x-janus-tenant", "3"))
                    .to_request(),
                _ => test::TestRequest::delete()
                    .uri("/api/v1/deployments/1")
                    .insert_header(("Authorization", "Bearer test-token"))
                    .insert_header(("x-janus-tenant", "3"))
                    .to_request(),
            };
            assert_status(app, request, StatusCode::NOT_FOUND).await;
        }
    }

    #[actix_web::test]
    async fn authenticated_compatibility_route_reaches_actix_dispatcher() {
        let mut auth = Auth::new();
        auth.register(UserAccount {
            subject: SubjectId(7),
            tenant: TenantId(3),
            email: "operator@example.com".to_owned(),
            password_hash: "adapter-hash".to_owned(),
            mfa_enabled: false,
            totp_enabled: false,
            totp_secret: String::new(),
            email_otp_enabled: false,
            recovery_codes: 0,
            permissions: 9,
        })
        .expect("test account registers");
        auth.register(UserAccount {
            subject: SubjectId(8),
            tenant: TenantId(3),
            email: "bcrypt@example.com".to_owned(),
            password_hash: "$2a$10$raV7DRKl2iEmmd70abrFv.Z9cDK7jQM2TtNHKpuIxLLtkTVrZGIDG"
                .to_owned(),
            mfa_enabled: false,
            totp_enabled: false,
            totp_secret: String::new(),
            email_otp_enabled: false,
            recovery_codes: 0,
            permissions: 9,
        })
        .expect("bcrypt account registers");
        auth.issue_session("test-token", SubjectId(7), unix_now() + 3600, true)
            .expect("test session issues");

        let state = test_api_state(auth);
        let app = test::init_service(
            App::new()
                .app_data(web::Data::new(HttpService::new().with_ready(true)))
                .app_data(state.clone())
                .configure(configure),
        )
        .await;
        compatibility_public_routes(&app).await;
        compatibility_auth_routes(&app).await;
        compatibility_observability_routes(&app).await;
        compatibility_resource_routes(&app, &state).await;
    }

    async fn project_creation_and_repository_checks<S, B, E>(app: &S)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        E: Debug,
    {
        let body = serde_json::json!({
            "name": "Janus",
            "slug": "janus",
            "description": "control plane",
            "repo_provider": "github",
            "repo_url": "https://example.test/janus",
            "repo_branch": "main"
        });
        let response = assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/projects")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .set_json(&body)
                .to_request(),
            StatusCode::CREATED,
        )
        .await;
        let created: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(created["id"], 1);
        assert_eq!(created["operationId"], "project.create-1");

        let response = assert_status(
            app,
            test::TestRequest::get()
                .uri("/api/v1/operations/project.create-1")
                .to_request(),
            StatusCode::OK,
        )
        .await;
        let operation: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(operation["status"], "succeeded");
        assert_eq!(operation["tenantId"], "tenant-3");

        let response = assert_status(
            app,
            test::TestRequest::get()
                .uri("/api/v1/projects")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        let listed: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(listed["projects"][0]["slug"], "janus");

        project_repository_checks(app).await;
    }

    async fn project_repository_checks<S, B, E>(app: &S)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        E: Debug,
    {
        let response = assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/repos/validate")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .set_json(serde_json::json!({ "project_id": 1 }))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        let validation: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(validation["result"]["status"], "passed");
        assert_eq!(validation["result"]["stages"].as_array().unwrap().len(), 2);

        assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/repos/validate")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .set_json(serde_json::json!({}))
                .to_request(),
            StatusCode::BAD_REQUEST,
        )
        .await;

        project_import_and_build_checks(app).await;
    }

    async fn project_import_and_build_checks<S, B, E>(app: &S)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        E: Debug,
    {
        let response = assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/repos/import")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .set_json(serde_json::json!({
                    "projectId": 1,
                    "repoUrl": "https://github.com/example/janus",
                    "branch": "main"
                }))
                .to_request(),
            StatusCode::ACCEPTED,
        )
        .await;
        let imported: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(imported["operationId"], "build.enqueue-1");
        assert_eq!(imported["status"], "pending");

        let response = assert_status(
            app,
            test::TestRequest::get()
                .uri("/api/v1/builds?projectId=1")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .to_request(),
            StatusCode::OK,
        )
        .await;
        let builds: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(builds["builds"][0]["projectId"], 1);
        assert_eq!(builds["builds"][0]["sourceRef"], "main");
    }

    async fn project_upload_and_tenant_checks<S, B, E>(app: &S)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        E: Debug,
    {
        let source_body = multipart(
            &[("projectId", "1"), ("sourceRef", "release")],
            "source.zip",
            b"zip payload",
        );
        let response = assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/uploads/source-bundles")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .insert_header((
                    "content-type",
                    "multipart/form-data; boundary=janus-test-boundary",
                ))
                .set_payload(source_body)
                .to_request(),
            StatusCode::ACCEPTED,
        )
        .await;
        let source_upload: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(source_upload["status"], "pending");
        assert_eq!(source_upload["message"], "source bundle accepted");

        let artifact_body = multipart(
            &[("projectId", "1"), ("artifactRuntime", "wasm/wasi-command")],
            "module.wasm",
            b"wasm payload",
        );
        let response = assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/uploads/artifacts")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .insert_header((
                    "content-type",
                    "multipart/form-data; boundary=janus-test-boundary",
                ))
                .set_payload(artifact_body)
                .to_request(),
            StatusCode::ACCEPTED,
        )
        .await;
        let artifact_upload: serde_json::Value = test::read_body_json(response).await;
        assert_eq!(artifact_upload["status"], "pending");
        assert_eq!(artifact_upload["artifactRuntime"], "wasm/wasi-command");

        project_import_and_tenant_checks(app).await;
    }

    async fn project_import_and_tenant_checks<S, B, E>(app: &S)
    where
        S: Service<actix_http::Request, Response = ServiceResponse<B>, Error = E>,
        B: actix_web::body::MessageBody,
        E: Debug,
    {
        assert_status(
            app,
            test::TestRequest::post()
                .uri("/api/v1/repos/import")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .set_json(serde_json::json!({
                    "projectId": 1,
                    "repoUrl": "http://github.com/example/janus"
                }))
                .to_request(),
            StatusCode::BAD_REQUEST,
        )
        .await;

        assert_status(
            app,
            test::TestRequest::get()
                .uri("/api/v1/projects")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "4"))
                .to_request(),
            StatusCode::UNAUTHORIZED,
        )
        .await;
    }

    #[actix_web::test]
    async fn authenticated_project_crud_uses_rust_ecs_and_tenant_boundary() {
        let mut auth = Auth::new();
        auth.register(UserAccount {
            subject: SubjectId(7),
            tenant: TenantId(3),
            email: "operator@example.com".to_owned(),
            password_hash: "adapter-hash".to_owned(),
            mfa_enabled: false,
            totp_enabled: false,
            totp_secret: String::new(),
            email_otp_enabled: false,
            recovery_codes: 0,
            permissions: 19,
        })
        .expect("test account registers");
        auth.issue_session("test-token", SubjectId(7), unix_now() + 3600, true)
            .expect("test session issues");

        let state = test_api_state(auth);
        let app = test::init_service(
            App::new()
                .app_data(web::Data::new(HttpService::new().with_ready(true)))
                .app_data(state)
                .configure(configure),
        )
        .await;
        project_creation_and_repository_checks(&app).await;
        project_upload_and_tenant_checks(&app).await;
    }

    #[actix_web::test]
    async fn idempotent_project_commands_replay_and_reject_conflicts() {
        let mut auth = Auth::new();
        auth.register(UserAccount {
            subject: SubjectId(7),
            tenant: TenantId(3),
            email: "operator@example.com".to_owned(),
            password_hash: "adapter-hash".to_owned(),
            mfa_enabled: false,
            totp_enabled: false,
            totp_secret: String::new(),
            email_otp_enabled: false,
            recovery_codes: 0,
            permissions: 19,
        })
        .expect("test account registers");
        auth.issue_session("test-token", SubjectId(7), unix_now() + 3600, true)
            .expect("test session issues");
        let state = test_api_state(auth);
        let app = test::init_service(
            App::new()
                .app_data(web::Data::new(HttpService::new().with_ready(true)))
                .app_data(state)
                .configure(configure),
        )
        .await;
        let body = serde_json::json!({
            "name": "Replay",
            "slug": "replay",
            "repoProvider": "github",
            "repoUrl": "https://example.test/replay",
            "repoBranch": "main"
        });
        let request = || {
            test::TestRequest::post()
                .uri("/api/v1/projects")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .insert_header(("Idempotency-Key", "project-replay-1"))
                .set_json(&body)
                .to_request()
        };
        let first = assert_status(&app, request(), StatusCode::CREATED).await;
        let first_body = to_bytes(first.into_body()).await.expect("first body reads");
        let second = assert_status(&app, request(), StatusCode::CREATED).await;
        let second_body = to_bytes(second.into_body())
            .await
            .expect("second body reads");
        assert_eq!(first_body, second_body);
        let conflict = serde_json::json!({
            "name": "Conflict",
            "slug": "conflict",
            "repoProvider": "github",
            "repoUrl": "https://example.test/conflict",
            "repoBranch": "main"
        });
        assert_status(
            &app,
            test::TestRequest::post()
                .uri("/api/v1/projects")
                .insert_header(("Authorization", "Bearer test-token"))
                .insert_header(("x-janus-tenant", "3"))
                .insert_header(("Idempotency-Key", "project-replay-1"))
                .set_json(&conflict)
                .to_request(),
            StatusCode::CONFLICT,
        )
        .await;
    }
}
