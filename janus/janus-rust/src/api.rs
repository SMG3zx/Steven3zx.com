//! Compatibility contract for the existing Janus HTTP surface.

use crate::{AuthorizationError, Permission, Principal, TenantId};

/// HTTP method accepted by a compatibility route.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Method {
    /// Read-only request.
    Get,
    /// Create or command request.
    Post,
    /// Partial update request.
    Patch,
    /// Delete request.
    Delete,
}

/// Compatibility route group.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RouteGroup {
    /// Health and metrics endpoints.
    System,
    /// Authentication and MFA endpoints.
    Auth,
    /// Existing build/deployment/operation endpoints.
    Core,
    /// Existing telemetry endpoints.
    Telemetry,
    /// Event stream endpoints.
    Events,
    /// API fallback route.
    Fallback,
    /// Frontend fallback route.
    Frontend,
}

/// One stable route contract.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Route {
    /// Existing URL path.
    pub path: &'static str,
    /// Whether the path matches descendants.
    pub prefix: bool,
    /// Compatibility grouping.
    pub group: RouteGroup,
    /// Allowed methods.
    pub methods: &'static [Method],
}

impl Route {
    /// Returns the stable Go/Zig handler identity for this route.
    #[must_use]
    pub fn handler_name(&self) -> &'static str {
        match self.path {
            "/api/v1/" => "APIFallback",
            "/" => "serveFrontend",
            "/healthz" => "Healthz",
            "/readyz" => "Readyz",
            "/metrics" => "PrometheusMetrics",
            "/api/v1/auth/signup" => "AuthSignup",
            "/api/v1/auth/signin" => "AuthSignin",
            "/api/v1/auth/signin/password" => "AuthSigninPassword",
            "/api/v1/auth/magic-code/request" => "AuthMagicCodeRequest",
            "/api/v1/auth/magic-code/verify" => "AuthMagicCodeVerify",
            "/api/v1/auth/reset-password/request" => "AuthResetPasswordRequest",
            "/api/v1/auth/reset-password/confirm" => "AuthResetPasswordConfirm",
            "/api/v1/auth/session/exchange" => "AuthSessionExchange",
            "/api/v1/auth/signout" => "AuthSignout",
            "/api/v1/auth/refresh" => "AuthRefresh",
            "/api/v1/auth/me" => "AuthMe",
            "/api/v1/mfa/status" => "MFAStatus",
            "/api/v1/mfa/totp/enroll" => "MFATOTPEnroll",
            "/api/v1/mfa/totp/verify" => "MFATOTPVerify",
            "/api/v1/mfa/totp/disable" => "MFATOTPDisable",
            "/api/v1/mfa/email/enroll" => "MFAEmailEnroll",
            "/api/v1/mfa/email/verify" => "MFAEmailVerify",
            "/api/v1/mfa/email/send" => "MFAEmailSend",
            "/api/v1/mfa/email/disable" => "MFAEmailDisable",
            "/api/v1/mfa/recovery/generate" => "MFARecoveryGenerate",
            "/api/v1/mfa/verify" => "MFAVerify",
            "/api/v1/projects" => "ProjectsRoute",
            "/api/v1/projects/" => "ProjectByIDRoute",
            "/api/v1/repos/import" => "RepoImport",
            "/api/v1/repos/validate" => "RepoValidate",
            "/api/v1/uploads/source-bundles" => "SourceBundleUploadRoute",
            "/api/v1/uploads/artifacts" => "ArtifactUploadRoute",
            "/api/v1/operations/" => "OperationByIDRoute",
            "/api/v1/builds" => "BuildsRoute",
            "/api/v1/builds/" => "BuildLogsRoute",
            "/api/v1/deployments" => "DeploymentsRoute",
            "/api/v1/deployments/" => "DeploymentByIDRoute",
            "/api/v1/runners" => "RunnersRoute",
            "/api/v1/runners/register" => "RunnerRegisterRoute",
            "/api/v1/runners/heartbeat" => "RunnerHeartbeatRoute",
            "/api/v1/domains" => "DomainsRoute",
            "/api/v1/templates" => "TemplatesRoute",
            "/api/v1/backend-resources" => "BackendResourcesRoute",
            "/api/v1/logs" => "LogsRoute",
            "/api/v1/git/providers" => "GitProvidersRoute",
            "/api/v1/git/providers/github/oauth/start" => "GitHubOAuthStartRoute",
            "/api/v1/git/providers/github/oauth/callback" => "GitHubOAuthCallbackRoute",
            "/api/v1/billing" => "BillingRoute",
            "/api/v1/metrics" => "MetricsAPIRoute",
            "/api/v1/telemetry" => "TelemetryRoute",
            "/api/v1/telemetry/views" => "TelemetryViewsRoute",
            "/api/v1/telemetry/stream" => "TelemetryStreamRoute",
            "/api/v1/admin/overview" => "AdminOverviewRoute",
            "/api/v1/admin/telemetry" => "AdminTelemetryRoute",
            "/api/v1/admin/telemetry/views" => "AdminTelemetryViewsRoute",
            "/api/v1/events" => "EventsRoute",
            "/api/v1/events/stream" => "EventsStreamRoute",
            _ => "UnknownRoute",
        }
    }

    /// Returns whether the Go/Zig route contract requires an authenticated user.
    #[must_use]
    pub fn requires_authentication(&self) -> bool {
        !matches!(
            self.path,
            "/api/v1/"
                | "/"
                | "/healthz"
                | "/readyz"
                | "/metrics"
                | "/api/v1/auth/signup"
                | "/api/v1/auth/signin"
                | "/api/v1/auth/signin/password"
                | "/api/v1/auth/magic-code/request"
                | "/api/v1/auth/magic-code/verify"
                | "/api/v1/auth/reset-password/request"
                | "/api/v1/auth/reset-password/confirm"
                | "/api/v1/auth/session/exchange"
                | "/api/v1/auth/signout"
                | "/api/v1/operations/"
        )
    }

    fn allows_method(&self, method: Method) -> bool {
        self.methods.is_empty() || self.methods.contains(&method)
    }
}

/// Failure while authorizing a compatible HTTP route.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RouteAuthorizationError {
    /// The route does not allow the requested method.
    MethodNotAllowed,
    /// The principal lacks the route's tenant or operation permission.
    Authorization(AuthorizationError),
}

impl Route {
    /// Returns the permission required by this route and method, if protected.
    #[must_use]
    pub fn required_permission(&self, method: Method) -> Option<Permission> {
        match self.group {
            RouteGroup::System | RouteGroup::Auth | RouteGroup::Fallback | RouteGroup::Frontend => {
                None
            }
            RouteGroup::Core if self.path == "/api/v1/operations/" => None,
            RouteGroup::Telemetry | RouteGroup::Events => Some(Permission::ReadBuilds),
            RouteGroup::Core
                if self.path == "/api/v1/projects" || self.path == "/api/v1/projects/" =>
            {
                Some(Permission::ManageProjects)
            }
            RouteGroup::Core if self.path.contains("/builds") => match method {
                Method::Get => Some(Permission::ReadBuilds),
                Method::Post | Method::Patch => Some(Permission::SubmitBuilds),
                Method::Delete => Some(Permission::CancelBuilds),
            },
            RouteGroup::Core => Some(Permission::ManageDeployments),
        }
    }

    /// Authorizes one method and tenant against an authenticated principal.
    ///
    /// # Errors
    ///
    /// Returns [`RouteAuthorizationError::MethodNotAllowed`] when the method
    /// is not registered, or an authorization error when the principal lacks
    /// the route permission or tenant boundary.
    pub fn authorize(
        &self,
        principal: &Principal,
        tenant: TenantId,
        method: Method,
    ) -> Result<(), RouteAuthorizationError> {
        if !self.allows_method(method) {
            return Err(RouteAuthorizationError::MethodNotAllowed);
        }
        if let Some(permission) = self.required_permission(method) {
            principal
                .authorize(tenant, permission)
                .map_err(RouteAuthorizationError::Authorization)?;
        }
        Ok(())
    }
}

const GET: &[Method] = &[Method::Get];
const POST: &[Method] = &[Method::Post];
const GET_POST: &[Method] = &[Method::Get, Method::Post];
const GET_DELETE: &[Method] = &[Method::Get, Method::Delete];
const PATCH_DELETE: &[Method] = &[Method::Patch, Method::Delete];

/// Stable routes needed by the first Rust vertical slice.
pub const COMPATIBILITY_ROUTES: &[Route] = &[
    Route {
        path: "/healthz",
        prefix: false,
        group: RouteGroup::System,
        methods: GET,
    },
    Route {
        path: "/readyz",
        prefix: false,
        group: RouteGroup::System,
        methods: GET,
    },
    Route {
        path: "/metrics",
        prefix: false,
        group: RouteGroup::System,
        methods: GET,
    },
    Route {
        path: "/api/v1/auth/signup",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/auth/signin",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/auth/signin/password",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/auth/magic-code/request",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/auth/magic-code/verify",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/auth/reset-password/request",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/auth/reset-password/confirm",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/auth/session/exchange",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/auth/signout",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/auth/refresh",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/auth/me",
        prefix: false,
        group: RouteGroup::Auth,
        methods: GET,
    },
    Route {
        path: "/api/v1/mfa/status",
        prefix: false,
        group: RouteGroup::Auth,
        methods: GET,
    },
    Route {
        path: "/api/v1/mfa/totp/enroll",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/mfa/totp/verify",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/mfa/totp/disable",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/mfa/email/enroll",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/mfa/email/verify",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/mfa/email/send",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/mfa/email/disable",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/mfa/recovery/generate",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/mfa/verify",
        prefix: false,
        group: RouteGroup::Auth,
        methods: POST,
    },
    Route {
        path: "/api/v1/projects",
        prefix: false,
        group: RouteGroup::Core,
        methods: GET_POST,
    },
    Route {
        path: "/api/v1/projects/",
        prefix: true,
        group: RouteGroup::Core,
        methods: PATCH_DELETE,
    },
    Route {
        path: "/api/v1/repos/import",
        prefix: false,
        group: RouteGroup::Core,
        methods: POST,
    },
    Route {
        path: "/api/v1/repos/validate",
        prefix: false,
        group: RouteGroup::Core,
        methods: POST,
    },
    Route {
        path: "/api/v1/uploads/source-bundles",
        prefix: false,
        group: RouteGroup::Core,
        methods: POST,
    },
    Route {
        path: "/api/v1/uploads/artifacts",
        prefix: false,
        group: RouteGroup::Core,
        methods: POST,
    },
    Route {
        path: "/api/v1/operations/",
        prefix: true,
        group: RouteGroup::Core,
        methods: GET,
    },
    Route {
        path: "/api/v1/builds",
        prefix: false,
        group: RouteGroup::Core,
        methods: GET,
    },
    Route {
        path: "/api/v1/builds/",
        prefix: true,
        group: RouteGroup::Core,
        methods: GET,
    },
    Route {
        path: "/api/v1/deployments",
        prefix: false,
        group: RouteGroup::Core,
        methods: GET_POST,
    },
    Route {
        path: "/api/v1/deployments/",
        prefix: true,
        group: RouteGroup::Core,
        methods: GET_DELETE,
    },
    Route {
        path: "/api/v1/runners",
        prefix: false,
        group: RouteGroup::Core,
        methods: GET,
    },
    Route {
        path: "/api/v1/runners/register",
        prefix: false,
        group: RouteGroup::Core,
        methods: POST,
    },
    Route {
        path: "/api/v1/runners/heartbeat",
        prefix: false,
        group: RouteGroup::Core,
        methods: POST,
    },
    Route {
        path: "/api/v1/domains",
        prefix: false,
        group: RouteGroup::Core,
        methods: GET,
    },
    Route {
        path: "/api/v1/templates",
        prefix: false,
        group: RouteGroup::Core,
        methods: GET,
    },
    Route {
        path: "/api/v1/backend-resources",
        prefix: false,
        group: RouteGroup::Core,
        methods: GET,
    },
    Route {
        path: "/api/v1/logs",
        prefix: false,
        group: RouteGroup::Core,
        methods: GET,
    },
    Route {
        path: "/api/v1/git/providers",
        prefix: false,
        group: RouteGroup::Core,
        methods: GET,
    },
    Route {
        path: "/api/v1/git/providers/github/oauth/start",
        prefix: false,
        group: RouteGroup::Core,
        methods: GET,
    },
    Route {
        path: "/api/v1/git/providers/github/oauth/callback",
        prefix: false,
        group: RouteGroup::Core,
        methods: GET,
    },
    Route {
        path: "/api/v1/billing",
        prefix: false,
        group: RouteGroup::Core,
        methods: GET,
    },
    Route {
        path: "/api/v1/metrics",
        prefix: false,
        group: RouteGroup::Core,
        methods: GET,
    },
    Route {
        path: "/api/v1/telemetry",
        prefix: false,
        group: RouteGroup::Telemetry,
        methods: GET,
    },
    Route {
        path: "/api/v1/telemetry/views",
        prefix: false,
        group: RouteGroup::Telemetry,
        methods: GET_POST,
    },
    Route {
        path: "/api/v1/telemetry/stream",
        prefix: false,
        group: RouteGroup::Telemetry,
        methods: GET,
    },
    Route {
        path: "/api/v1/admin/overview",
        prefix: false,
        group: RouteGroup::Telemetry,
        methods: GET,
    },
    Route {
        path: "/api/v1/admin/telemetry",
        prefix: false,
        group: RouteGroup::Telemetry,
        methods: GET,
    },
    Route {
        path: "/api/v1/admin/telemetry/views",
        prefix: false,
        group: RouteGroup::Telemetry,
        methods: GET_POST,
    },
    Route {
        path: "/api/v1/events",
        prefix: false,
        group: RouteGroup::Events,
        methods: GET,
    },
    Route {
        path: "/api/v1/events/stream",
        prefix: false,
        group: RouteGroup::Events,
        methods: GET,
    },
    Route {
        path: "/api/v1/",
        prefix: true,
        group: RouteGroup::Fallback,
        methods: &[],
    },
    Route {
        path: "/",
        prefix: true,
        group: RouteGroup::Frontend,
        methods: &[],
    },
];

/// Finds the exact or longest-prefix route contract.
#[must_use]
pub fn find_route(path: &str) -> Option<Route> {
    COMPATIBILITY_ROUTES
        .iter()
        .copied()
        .find(|route| !route.prefix && route.path == path)
        .or_else(|| {
            COMPATIBILITY_ROUTES
                .iter()
                .copied()
                .filter(|route| route.prefix && path.starts_with(route.path))
                .max_by_key(|route| route.path.len())
        })
}

/// Checks whether an existing route accepts a method.
#[must_use]
pub fn allows_method(path: &str, method: Method) -> bool {
    find_route(path).is_some_and(|route| route.allows_method(method))
}

/// Finds and authorizes one compatible route for an authenticated request.
///
/// # Errors
///
/// Returns a route authorization error when the path is known but the method,
/// tenant, or principal permissions are incompatible.
pub fn authorize_route(
    path: &str,
    principal: &Principal,
    tenant: TenantId,
    method: Method,
) -> Result<(), RouteAuthorizationError> {
    let route = find_route(path).ok_or(RouteAuthorizationError::MethodNotAllowed)?;
    route.authorize(principal, tenant, method)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde::Deserialize;

    #[derive(Deserialize)]
    struct ZigRouteFixture {
        path: String,
        handler: String,
        methods: Vec<String>,
        auth: bool,
    }

    fn method_name(method: Method) -> &'static str {
        match method {
            Method::Get => "GET",
            Method::Post => "POST",
            Method::Patch => "PATCH",
            Method::Delete => "DELETE",
        }
    }

    #[test]
    fn compatibility_preserves_core_routes() {
        assert!(allows_method("/healthz", Method::Get));
        assert_eq!(
            find_route("/api/v1/auth/me").map(|route| route.group),
            Some(RouteGroup::Auth)
        );
        assert!(allows_method("/api/v1/builds", Method::Get));
        assert!(allows_method("/api/v1/builds/build-1/logs", Method::Get));
        assert!(allows_method("/api/v1/projects/project-1", Method::Patch));
        assert!(allows_method(
            "/api/v1/deployments/deploy-1",
            Method::Delete
        ));
        assert!(!allows_method("/api/v1/telemetry", Method::Post));
        assert_eq!(
            find_route("/api/v1/unknown").map(|route| route.group),
            Some(RouteGroup::Fallback)
        );
        assert_eq!(
            find_route("/index.html").map(|route| route.group),
            Some(RouteGroup::Frontend)
        );
    }

    #[test]
    fn protected_routes_use_principal_permissions() {
        let principal = Principal::new(crate::SubjectId(1), TenantId(7))
            .with_permission(Permission::ReadBuilds);
        assert!(authorize_route("/api/v1/builds", &principal, TenantId(7), Method::Get).is_ok());
        assert_eq!(
            authorize_route(
                "/api/v1/deployments/deploy-1",
                &principal,
                TenantId(7),
                Method::Delete
            ),
            Err(RouteAuthorizationError::Authorization(
                AuthorizationError::PermissionDenied
            ))
        );
        assert_eq!(
            authorize_route("/api/v1/builds", &principal, TenantId(8), Method::Get),
            Err(RouteAuthorizationError::Authorization(
                AuthorizationError::TenantBoundary
            ))
        );
    }

    #[test]
    fn route_contract_preserves_reference_handler_and_authentication_policy() {
        let builds = find_route("/api/v1/builds").unwrap();
        assert_eq!(builds.handler_name(), "BuildsRoute");
        assert!(builds.requires_authentication());

        let operation = find_route("/api/v1/operations/op-1").unwrap();
        assert_eq!(operation.handler_name(), "OperationByIDRoute");
        assert!(!operation.requires_authentication());

        assert!(allows_method("/api/v1/unknown", Method::Get));
        assert!(allows_method("/unknown", Method::Post));
    }

    #[test]
    fn every_reference_route_has_a_stable_handler_identity() {
        assert_eq!(COMPATIBILITY_ROUTES.len(), 57);
        assert!(COMPATIBILITY_ROUTES
            .iter()
            .all(|route| route.handler_name() != "UnknownRoute"));
    }

    #[test]
    fn zig_behavior_fixture_matches_rust_route_identity_methods_and_auth() {
        let fixture: Vec<ZigRouteFixture> = serde_json::from_str(include_str!(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../antithesis/fixtures/route-behavior-matrix.json"
        )))
        .expect("Zig route behavior fixture is valid JSON");
        assert_eq!(fixture.len(), COMPATIBILITY_ROUTES.len());
        for expected in fixture {
            let route = find_route(&expected.path).expect("fixture route exists in Rust");
            assert_eq!(route.handler_name(), expected.handler, "{}", expected.path);
            assert_eq!(
                route.requires_authentication(),
                expected.auth,
                "{}",
                expected.path
            );
            let mut methods = if route.methods.is_empty() {
                vec!["ANY".to_owned()]
            } else {
                route
                    .methods
                    .iter()
                    .map(|method| method_name(*method).to_owned())
                    .collect::<Vec<_>>()
            };
            methods.sort_unstable();
            assert_eq!(methods, expected.methods, "{}", expected.path);
        }
    }

    #[test]
    fn go_router_registration_matches_rust_compatibility_routes() {
        let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../antithesis/fixtures/go-router-registration.txt");
        let source = std::fs::read_to_string(path).expect("Go router source is present");
        let mut go_routes = source
            .lines()
            .filter_map(|line| {
                let tail = line.split("mux.Handle").nth(1)?;
                let tail = tail.strip_prefix("Func").unwrap_or(tail);
                tail.strip_prefix("(\"")?.split('"').next()
            })
            .collect::<Vec<_>>();
        go_routes.sort_unstable();
        go_routes.dedup();
        let mut rust_routes = COMPATIBILITY_ROUTES
            .iter()
            .map(|route| route.path)
            .collect::<Vec<_>>();
        rust_routes.sort_unstable();
        rust_routes.dedup();
        assert_eq!(rust_routes, go_routes);

        let mut go_handlers = source
            .lines()
            .filter_map(|line| {
                let tail = line.split("mux.Handle").nth(1)?;
                let tail = tail.strip_prefix("Func").unwrap_or(tail);
                let registration = tail.strip_prefix("(\"")?;
                let mut fields = registration.splitn(2, "\",");
                let path = fields.next()?;
                let expression = fields.next()?.trim().trim_end_matches(')').trim();
                let handler = if expression.contains("promhttp.Handler") {
                    "PrometheusMetrics"
                } else {
                    expression.rsplit('.').next()?.trim()
                };
                Some(format!("{path}|{handler}"))
            })
            .collect::<Vec<_>>();
        go_handlers.sort_unstable();
        let mut rust_handlers = COMPATIBILITY_ROUTES
            .iter()
            .map(|route| format!("{}|{}", route.path, route.handler_name()))
            .collect::<Vec<_>>();
        rust_handlers.sort_unstable();
        assert_eq!(rust_handlers, go_handlers);
    }
}
