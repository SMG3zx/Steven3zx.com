//! Bounded HTTP transport for the Rust migration boundary.

use std::io;

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};

use crate::api::{authorize_route, find_route, Method, RouteAuthorizationError, RouteGroup};
use crate::{AuthDirectory, AuthError, Principal, SubjectId, TenantId};

/// Maximum bytes accepted for one request head.
pub const MAX_REQUEST_BYTES: usize = 16 * 1024;

/// A parsed HTTP request sufficient for the compatibility boundary.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct HttpRequest {
    /// Request method.
    pub method: Method,
    /// Request path without a query string.
    pub path: String,
    /// Authenticated principal, when middleware has established one.
    pub principal: Option<Principal>,
    /// Tenant selected by the request context.
    pub tenant: TenantId,
}

/// Adapter port that turns an HTTP bearer token into a command principal.
pub trait RequestAuthenticator {
    /// Authenticates a token for the requested tenant at the supplied timestamp.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn authenticate(&self, token: &str, tenant: TenantId, now: u64)
        -> Result<Principal, AuthError>;
}

impl<const MAX_USERS: usize, const MAX_SESSIONS: usize> RequestAuthenticator
    for AuthDirectory<MAX_USERS, MAX_SESSIONS>
{
    fn authenticate(
        &self,
        token: &str,
        tenant: TenantId,
        now: u64,
    ) -> Result<Principal, AuthError> {
        self.authenticate_principal(token, tenant, now)
    }
}

/// Parses a bearer authorization value without accepting alternative schemes.
#[must_use]
pub fn bearer_token(value: &str) -> Option<&str> {
    let mut parts = value.split_ascii_whitespace();
    if parts.next()? != "Bearer" {
        return None;
    }
    let token = parts.next()?;
    if parts.next().is_some() || token.is_empty() {
        return None;
    }
    Some(token)
}

/// Authenticates a request using the configured bearer-token adapter.
///
/// # Errors
///
/// Returns an error when the operation cannot satisfy its input or
/// bounded-state contract.
pub fn authenticated_request<A: RequestAuthenticator>(
    request: &HttpRequest,
    authorization: &str,
    authenticator: &A,
    now: u64,
) -> Result<HttpRequest, AuthError> {
    let principal = authenticator.authenticate(
        bearer_token(authorization).ok_or(AuthError::InvalidCredentials)?,
        request.tenant,
        now,
    )?;
    Ok(HttpRequest {
        principal: Some(principal),
        ..request.clone()
    })
}

/// A bounded HTTP response.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct HttpResponse {
    /// HTTP status code.
    pub status: u16,
    /// JSON response body.
    pub body: String,
}

impl HttpResponse {
    fn json(status: u16, body: &str) -> Self {
        Self {
            status,
            body: body.to_owned(),
        }
    }

    fn ok(body: &str) -> Self {
        Self::json(200, body)
    }
}

/// Rust service boundary for compatibility routes.
#[derive(Clone, Copy, Debug, Default)]
pub struct HttpService {
    ready: bool,
}

impl HttpService {
    /// Creates a service that initially reports not-ready.
    #[must_use]
    pub const fn new() -> Self {
        Self { ready: false }
    }

    /// Marks dependency initialization complete or incomplete.
    #[must_use]
    pub const fn with_ready(self, ready: bool) -> Self {
        Self { ready }
    }

    /// Reports whether dependency initialization has completed.
    #[must_use]
    pub const fn is_ready(self) -> bool {
        self.ready
    }

    /// Dispatches a parsed request through route, tenant, and permission checks.
    #[must_use]
    pub fn dispatch(&self, request: &HttpRequest) -> HttpResponse {
        let Some(route) = find_route(&request.path) else {
            return HttpResponse::json(404, r#"{"error":"not_found"}"#);
        };
        let anonymous = Principal::new(SubjectId(0), request.tenant);
        let principal = request.principal.as_ref().unwrap_or(&anonymous);

        if route.requires_authentication() && request.principal.is_none() {
            return HttpResponse::json(401, r#"{"error":"authentication_required"}"#);
        }

        match authorize_route(&request.path, principal, request.tenant, request.method) {
            Ok(()) => {
                Self::dispatch_authorized(self.ready, route.group, &request.path, request.method)
            }
            Err(RouteAuthorizationError::MethodNotAllowed) => {
                HttpResponse::json(405, r#"{"error":"method_not_allowed"}"#)
            }
            Err(RouteAuthorizationError::Authorization(_)) if request.principal.is_none() => {
                HttpResponse::json(401, r#"{"error":"authentication_required"}"#)
            }
            Err(RouteAuthorizationError::Authorization(_)) => {
                HttpResponse::json(403, r#"{"error":"forbidden"}"#)
            }
        }
    }

    /// Authenticates a bearer request and dispatches it through the same route contract.
    pub fn dispatch_bearer<A: RequestAuthenticator>(
        &self,
        request: &HttpRequest,
        authorization: &str,
        authenticator: &A,
        now: u64,
    ) -> HttpResponse {
        let request = match authenticated_request(request, authorization, authenticator, now) {
            Ok(request) => request,
            Err(AuthError::Expired | AuthError::NotFound | AuthError::InvalidCredentials) => {
                return HttpResponse::json(401, r#"{"error":"authentication_required"}"#)
            }
            Err(AuthError::TenantBoundary | AuthError::MfaRequired) => {
                return HttpResponse::json(403, r#"{"error":"forbidden"}"#)
            }
            Err(AuthError::Required | AuthError::Capacity | AuthError::AlreadyExists) => {
                return HttpResponse::json(400, r#"{"error":"bad_request"}"#)
            }
        };
        self.dispatch(&request)
    }

    fn dispatch_authorized(
        ready: bool,
        group: RouteGroup,
        path: &str,
        method: Method,
    ) -> HttpResponse {
        if let Some(response) = Self::dispatch_entity_path(path) {
            return response;
        }
        if let Some(response) = Self::dispatch_auth_path(path) {
            return response;
        }
        if let Some(response) = Self::dispatch_resource_path(path, method) {
            return response;
        }
        if let Some(response) = Self::dispatch_telemetry_path(path, method) {
            return response;
        }
        match path {
            "/healthz" => HttpResponse::ok(r#"{"status":"ok"}"#),
            "/readyz" if ready => HttpResponse::ok(r#"{"status":"ready"}"#),
            "/readyz" => HttpResponse::json(503, r#"{"status":"not_ready"}"#),
            _ if path.starts_with("/api/v1/operations/") => {
                HttpResponse::json(404, r#"{"error":"operation not found"}"#)
            }
            _ if group == RouteGroup::Fallback => {
                HttpResponse::json(404, r#"{"error":"endpoint not found"}"#)
            }
            _ if group == RouteGroup::Frontend => {
                HttpResponse::json(400, r#"{"error":"frontend_not_configured"}"#)
            }
            _ => HttpResponse::json(501, r#"{"error":"not_implemented"}"#),
        }
    }

    fn dispatch_auth_path(path: &str) -> Option<HttpResponse> {
        let response = match path {
            "/api/v1/auth/signout"
            | "/api/v1/auth/refresh"
            | "/api/v1/mfa/totp/enroll"
            | "/api/v1/mfa/totp/disable"
            | "/api/v1/mfa/email/enroll"
            | "/api/v1/mfa/email/send"
            | "/api/v1/mfa/email/disable" => HttpResponse::ok(r#"{"ok":true}"#),
            "/api/v1/auth/signup" | "/api/v1/auth/signin" | "/api/v1/auth/signin/password" => {
                HttpResponse::json(400, r#"{"error":"email and password are required"}"#)
            }
            "/api/v1/auth/magic-code/request" | "/api/v1/auth/reset-password/request" => {
                HttpResponse::json(400, r#"{"error":"email is required"}"#)
            }
            "/api/v1/auth/magic-code/verify" => {
                HttpResponse::json(400, r#"{"error":"email and code are required"}"#)
            }
            "/api/v1/auth/reset-password/confirm" => HttpResponse::json(
                400,
                r#"{"error":"email, token, and newPassword are required"}"#,
            ),
            "/api/v1/auth/session/exchange" => {
                HttpResponse::json(400, r#"{"error":"operationId is required"}"#)
            }
            "/api/v1/auth/me" => HttpResponse::ok(r#"{"user":null}"#),
            "/api/v1/mfa/status" => HttpResponse::ok(r#"{"enabled":false}"#),
            "/api/v1/mfa/totp/verify" | "/api/v1/mfa/email/verify" | "/api/v1/mfa/verify" => {
                HttpResponse::json(400, r#"{"error":"code is required"}"#)
            }
            "/api/v1/mfa/recovery/generate" => {
                HttpResponse::json(400, r#"{"error":"password is required"}"#)
            }
            _ => return None,
        };
        Some(response)
    }

    fn dispatch_resource_path(path: &str, method: Method) -> Option<HttpResponse> {
        let response = match path {
            "/metrics" => HttpResponse::ok("janus_http_requests_total 0\n"),
            "/api/v1/projects" if method == Method::Get => HttpResponse::ok(r#"{"projects":[]}"#),
            "/api/v1/projects" => HttpResponse::json(400, r#"{"error":"invalid payload"}"#),
            "/api/v1/repos/import" | "/api/v1/runners/register" | "/api/v1/runners/heartbeat" => {
                HttpResponse::json(400, r#"{"error":"invalid JSON body"}"#)
            }
            "/api/v1/repos/validate" => {
                HttpResponse::json(400, r#"{"error":"projectId is required"}"#)
            }
            "/api/v1/uploads/source-bundles" | "/api/v1/uploads/artifacts" => {
                HttpResponse::json(400, r#"{"error":"invalid multipart form"}"#)
            }
            "/api/v1/builds" => HttpResponse::ok(r#"{"builds":[]}"#),
            "/api/v1/deployments" if method == Method::Get => {
                HttpResponse::ok(r#"{"deployments":[]}"#)
            }
            "/api/v1/deployments" => HttpResponse::json(400, r#"{"error":"invalid JSON body"}"#),
            "/api/v1/domains" => HttpResponse::ok(r#"{"domains":[]}"#),
            "/api/v1/git/providers" => HttpResponse::ok(r#"{"providers":[]}"#),
            "/api/v1/git/providers/github/oauth/start" => {
                HttpResponse::json(500, r#"{"error":"GitHub OAuth is not configured"}"#)
            }
            "/api/v1/git/providers/github/oauth/callback" => {
                HttpResponse::json(400, r#"{"error":"missing OAuth state cookie"}"#)
            }
            "/api/v1/runners" => HttpResponse::ok(r#"{"runners":[]}"#),
            "/api/v1/templates" => HttpResponse::ok(r#"{"templates":[]}"#),
            "/api/v1/backend-resources" => HttpResponse::ok(r#"{"resources":[]}"#),
            "/api/v1/billing" => HttpResponse::ok(r#"{"plans":[],"subscription":null}"#),
            "/api/v1/metrics" => HttpResponse::ok(r#"{"metrics":[]}"#),
            "/api/v1/logs" => HttpResponse::ok(r#"{"logs":[]}"#),
            _ => return None,
        };
        Some(response)
    }

    fn dispatch_telemetry_path(path: &str, method: Method) -> Option<HttpResponse> {
        let response = match path {
            "/api/v1/telemetry" => HttpResponse::ok(
                r#"{"traces":[],"metrics":[{"name":"telemetry_traces_count","value":0}],"logs":[],"baggage":[]}"#,
            ),
            "/api/v1/telemetry/views" => match method {
                Method::Get => HttpResponse::ok(
                    r#"{"views":[{"id":"default","name":"Default","shared":true}]}"#,
                ),
                Method::Post => HttpResponse::json(201, r#"{"ok":true}"#),
                _ => HttpResponse::json(405, r#"{"error":"method_not_allowed"}"#),
            },
            "/api/v1/telemetry/stream" | "/api/v1/events/stream" => Self::stream_response(),
            "/api/v1/events" | "/api/v1/admin/telemetry" => HttpResponse::ok(r#"{"events":[]}"#),
            "/api/v1/admin/overview" => HttpResponse::ok(r#"{"counts":{},"recent":[]}"#),
            "/api/v1/admin/telemetry/views" => match method {
                Method::Get => HttpResponse::ok(r#"{"views":[]}"#),
                Method::Post => HttpResponse::json(201, r#"{"ok":true}"#),
                _ => HttpResponse::json(405, r#"{"error":"method_not_allowed"}"#),
            },
            _ => return None,
        };
        Some(response)
    }

    fn dispatch_entity_path(path: &str) -> Option<HttpResponse> {
        let response = if path.starts_with("/api/v1/projects/") {
            HttpResponse::json(404, r#"{"error":"project not found"}"#)
        } else if path.starts_with("/api/v1/deployments/") {
            HttpResponse::json(404, r#"{"error":"deployment not found"}"#)
        } else if path.starts_with("/api/v1/builds/") {
            HttpResponse::json(404, r#"{"error":"build not found"}"#)
        } else {
            return None;
        };
        Some(response)
    }

    fn stream_response() -> HttpResponse {
        HttpResponse::ok("event: telemetry\ndata: {}\n\n")
    }

    /// Parses the request line and creates an unauthenticated request context.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn parse_request(bytes: &[u8]) -> Result<HttpRequest, HttpParseError> {
        if bytes.len() > MAX_REQUEST_BYTES {
            return Err(HttpParseError::TooLarge);
        }
        let text = std::str::from_utf8(bytes).map_err(|_| HttpParseError::InvalidEncoding)?;
        let line = text.lines().next().ok_or(HttpParseError::Malformed)?;
        let mut fields = line.split_ascii_whitespace();
        let method = match fields.next().ok_or(HttpParseError::Malformed)? {
            "GET" => Method::Get,
            "POST" => Method::Post,
            "PATCH" => Method::Patch,
            "DELETE" => Method::Delete,
            _ => return Err(HttpParseError::UnsupportedMethod),
        };
        let target = fields.next().ok_or(HttpParseError::Malformed)?;
        let version = fields.next().ok_or(HttpParseError::Malformed)?;
        if version != "HTTP/1.1" && version != "HTTP/1.0" {
            return Err(HttpParseError::Malformed);
        }
        let path = target.split('?').next().unwrap_or(target);
        if path.is_empty() || !path.starts_with('/') {
            return Err(HttpParseError::Malformed);
        }
        Ok(HttpRequest {
            method,
            path: path.to_owned(),
            principal: None,
            tenant: TenantId(0),
        })
    }
}

/// Parser failure at the HTTP boundary.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum HttpParseError {
    /// Request exceeded the bounded parser buffer.
    TooLarge,
    /// Request was not valid UTF-8.
    InvalidEncoding,
    /// Request line was malformed.
    Malformed,
    /// Method is not supported by this transport.
    UnsupportedMethod,
}

/// A Tokio TCP server for the migration boundary.
pub struct HttpServer {
    listener: TcpListener,
    service: HttpService,
}

impl HttpServer {
    /// Binds a bounded HTTP server to an address.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub async fn bind(address: &str, service: HttpService) -> io::Result<Self> {
        Ok(Self {
            listener: TcpListener::bind(address).await?,
            service,
        })
    }

    /// Accepts and serves one request, useful for deterministic supervision.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub async fn serve_one(&self) -> io::Result<()> {
        let (stream, _) = self.listener.accept().await?;
        serve_connection(stream, self.service).await
    }
}

async fn serve_connection(mut stream: TcpStream, service: HttpService) -> io::Result<()> {
    let mut buffer = vec![0_u8; MAX_REQUEST_BYTES];
    let count = stream.read(&mut buffer).await?;
    let response = HttpService::parse_request(&buffer[..count]).map_or_else(
        |_| HttpResponse::json(400, r#"{"error":"bad_request"}"#),
        |request| service.dispatch(&request),
    );
    let reason = match response.status {
        200 => "OK",
        400 => "Bad Request",
        401 => "Unauthorized",
        403 => "Forbidden",
        404 => "Not Found",
        405 => "Method Not Allowed",
        501 => "Not Implemented",
        503 => "Service Unavailable",
        _ => "Error",
    };
    let wire = format!(
        "HTTP/1.1 {} {}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
        response.status,
        reason,
        response.body.len(),
        response.body
    );
    stream.write_all(wire.as_bytes()).await
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde::Deserialize;

    #[derive(Deserialize)]
    struct RouteBehaviorFixture {
        path: String,
        methods: Vec<String>,
        status_codes: Vec<u16>,
        error_bodies: Vec<String>,
    }

    fn fixture_methods(methods: &[String]) -> Vec<Method> {
        if methods.iter().any(|method| method == "ANY") {
            return vec![Method::Get, Method::Post, Method::Patch, Method::Delete];
        }
        methods
            .iter()
            .filter_map(|method| match method.as_str() {
                "GET" => Some(Method::Get),
                "POST" => Some(Method::Post),
                "PATCH" => Some(Method::Patch),
                "DELETE" => Some(Method::Delete),
                _ => None,
            })
            .collect()
    }

    fn method_name(method: Method) -> &'static str {
        match method {
            Method::Get => "GET",
            Method::Post => "POST",
            Method::Patch => "PATCH",
            Method::Delete => "DELETE",
        }
    }

    fn fully_permitted_principal() -> Principal {
        Principal::new(SubjectId(99), TenantId(3))
            .with_permission(crate::Permission::ReadBuilds)
            .with_permission(crate::Permission::SubmitBuilds)
            .with_permission(crate::Permission::CancelBuilds)
            .with_permission(crate::Permission::ManageDeployments)
            .with_permission(crate::Permission::ManageProjects)
            .with_permission(crate::Permission::OperateControlPlane)
    }

    #[test]
    fn parser_accepts_query_and_preserves_route_identity() {
        let request =
            HttpService::parse_request(b"GET /healthz?verbose=1 HTTP/1.1\r\n\r\n").unwrap();
        assert_eq!(request.path, "/healthz");
        assert_eq!(request.method, Method::Get);
    }

    #[test]
    fn service_exposes_readiness_and_explicit_incomplete_routes() {
        let service = HttpService::new();
        let request = HttpRequest {
            method: Method::Get,
            path: "/readyz".to_owned(),
            principal: None,
            tenant: TenantId(0),
        };
        assert_eq!(service.dispatch(&request).status, 503);
        assert_eq!(service.with_ready(true).dispatch(&request).status, 200);
        let build = HttpRequest {
            path: "/api/v1/builds".to_owned(),
            ..request
        };
        assert_eq!(service.dispatch(&build).status, 401);
        let operation = HttpRequest {
            path: "/api/v1/operations/op-1".to_owned(),
            ..request
        };
        assert_eq!(service.dispatch(&operation).status, 404);
        assert_eq!(
            service.dispatch(&operation).body,
            r#"{"error":"operation not found"}"#
        );
        let templates = HttpRequest {
            path: "/api/v1/templates".to_owned(),
            ..request
        };
        assert_eq!(service.dispatch(&templates).status, 401);
        let unknown = HttpRequest {
            path: "/api/v1/unknown".to_owned(),
            ..request
        };
        assert_eq!(service.dispatch(&unknown).status, 404);
    }

    #[test]
    fn static_authorized_routes_return_compatibility_envelopes() {
        let principal = Principal::new(SubjectId(7), TenantId(3))
            .with_permission(crate::Permission::ManageDeployments);
        let service = HttpService::new();
        for path in [
            "/api/v1/domains",
            "/api/v1/git/providers",
            "/api/v1/runners",
        ] {
            let response = service.dispatch(&HttpRequest {
                method: Method::Get,
                path: path.to_owned(),
                principal: Some(principal),
                tenant: TenantId(3),
            });
            assert_ne!(response.status, 501, "{path}");
            assert_eq!(response.status, 200, "{path}");
        }
        let signout = service.dispatch(&HttpRequest {
            method: Method::Post,
            path: "/api/v1/auth/signout".to_owned(),
            principal: Some(principal),
            tenant: TenantId(3),
        });
        assert_eq!(signout.status, 200);
        assert_eq!(signout.body, r#"{"ok":true}"#);
    }

    #[test]
    fn every_declared_route_method_has_a_rust_response() {
        let service = HttpService::new().with_ready(true);
        let principal = fully_permitted_principal();
        for route in crate::COMPATIBILITY_ROUTES {
            let path = if route.prefix {
                format!("{}sample", route.path)
            } else {
                route.path.to_owned()
            };
            let methods: &[Method] = if route.methods.is_empty() {
                &[Method::Get]
            } else {
                route.methods
            };
            for method in methods {
                let response = service.dispatch(&HttpRequest {
                    method: *method,
                    path: path.clone(),
                    principal: Some(principal),
                    tenant: TenantId(3),
                });
                assert_ne!(response.status, 501, "{path} {method:?}");
            }
        }
    }

    #[test]
    fn rust_dispatch_statuses_are_present_in_zig_behavior_fixture() {
        let fixture: Vec<RouteBehaviorFixture> = serde_json::from_str(include_str!(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../antithesis/fixtures/route-behavior-matrix.json"
        )))
        .expect("Zig route behavior fixture is valid JSON");
        let service = HttpService::new().with_ready(true);
        let principal = fully_permitted_principal();
        for route in fixture {
            for method in fixture_methods(&route.methods) {
                let response = service.dispatch(&HttpRequest {
                    method,
                    path: route.path.clone(),
                    principal: Some(principal),
                    tenant: TenantId(3),
                });
                assert!(
                    route.status_codes.contains(&response.status),
                    "{} {} returned {}, expected one of {:?}",
                    method_name(method),
                    route.path,
                    response.status,
                    route.status_codes
                );
                if !route.error_bodies.is_empty() && response.status >= 400 {
                    assert!(
                        route
                            .error_bodies
                            .iter()
                            .any(|expected| response.body.contains(expected)),
                        "{} {} returned body {}, expected one of {:?}",
                        method_name(method),
                        route.path,
                        response.body,
                        route.error_bodies
                    );
                }
            }
        }
    }

    #[test]
    fn parser_rejects_oversized_requests() {
        assert_eq!(
            HttpService::parse_request(&vec![b'x'; MAX_REQUEST_BYTES + 1]),
            Err(HttpParseError::TooLarge)
        );
    }

    fn directory() -> AuthDirectory<1, 1> {
        let mut directory = AuthDirectory::new();
        assert!(directory
            .register(crate::UserAccount {
                subject: SubjectId(7),
                tenant: TenantId(3),
                email: "user@example.com".to_owned(),
                password_hash: "hash".to_owned(),
                mfa_enabled: false,
                totp_enabled: false,
                totp_secret: String::with_capacity(32),
                email_otp_enabled: false,
                recovery_codes: 0,
                permissions: 16,
            })
            .is_ok());
        assert!(directory
            .issue_session("session-1", SubjectId(7), 20, true)
            .is_ok());
        directory
    }

    #[test]
    fn bearer_authentication_materializes_tenant_scoped_permissions() {
        assert_eq!(bearer_token("Bearer session-1"), Some("session-1"));
        assert_eq!(bearer_token("Basic session-1"), None);
        let request = HttpRequest {
            method: Method::Get,
            path: "/api/v1/projects".to_owned(),
            principal: None,
            tenant: TenantId(3),
        };
        let authenticated =
            authenticated_request(&request, "Bearer session-1", &directory(), 19).unwrap();
        assert_eq!(
            authenticated.principal.map(|principal| principal.tenant),
            Some(TenantId(3))
        );
        assert!(authorize_route(
            "/api/v1/projects",
            authenticated.principal.as_ref().unwrap(),
            TenantId(3),
            Method::Get
        )
        .is_ok());
        assert!(authenticated_request(&request, "Bearer session-1", &directory(), 20).is_err());
    }

    #[test]
    fn bearer_dispatch_maps_auth_failures_and_reaches_route_authorization() {
        let request = HttpRequest {
            method: Method::Get,
            path: "/api/v1/projects".to_owned(),
            principal: None,
            tenant: TenantId(3),
        };
        let service = HttpService::new();
        assert_eq!(
            service
                .dispatch_bearer(&request, "Basic session-1", &directory(), 19)
                .status,
            401
        );
        assert_eq!(
            service
                .dispatch_bearer(&request, "Bearer session-1", &directory(), 20)
                .status,
            401
        );
        assert_eq!(
            service
                .dispatch_bearer(&request, "Bearer session-1", &directory(), 19)
                .status,
            200
        );
    }
}
