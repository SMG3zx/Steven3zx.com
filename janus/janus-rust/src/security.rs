//! Principal, tenant, and capability checks at the command boundary.

use crate::TenantId;

/// Stable identity for an authenticated caller.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub struct SubjectId(pub u64);

/// Permission required by an inbound command or query.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Permission {
    /// Read build or operation state.
    ReadBuilds,
    /// Submit new builds.
    SubmitBuilds,
    /// Cancel an existing build.
    CancelBuilds,
    /// Create or stop deployments.
    ManageDeployments,
    /// Create, update, or delete projects.
    ManageProjects,
    /// Inspect or operate the control plane.
    OperateControlPlane,
}

/// Authenticated principal presented to the command adapter.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Principal {
    /// Caller identity.
    pub subject: SubjectId,
    /// Tenant selected by authentication and authorization.
    pub tenant: TenantId,
    permissions: u32,
}

/// Authorization failure.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AuthorizationError {
    /// The requested tenant differs from the principal's tenant.
    TenantBoundary,
    /// The principal lacks the required permission.
    PermissionDenied,
}

impl Principal {
    /// Creates a principal with no permissions.
    #[must_use]
    pub const fn new(subject: SubjectId, tenant: TenantId) -> Self {
        Self {
            subject,
            tenant,
            permissions: 0,
        }
    }

    /// Grants one permission while constructing an adapter principal.
    #[must_use]
    pub const fn with_permission(mut self, permission: Permission) -> Self {
        self.permissions |= permission_bit(permission);
        self
    }

    /// Materializes a principal from an authenticated identity and its adapter-owned permissions.
    pub(crate) const fn with_permissions(
        subject: SubjectId,
        tenant: TenantId,
        permissions: u32,
    ) -> Self {
        Self {
            subject,
            tenant,
            permissions,
        }
    }

    /// Verifies tenant ownership and permission for one operation.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn authorize(
        &self,
        tenant: TenantId,
        permission: Permission,
    ) -> Result<(), AuthorizationError> {
        if self.tenant != tenant {
            return Err(AuthorizationError::TenantBoundary);
        }
        if self.permissions & permission_bit(permission) == 0 {
            return Err(AuthorizationError::PermissionDenied);
        }
        Ok(())
    }
}

const fn permission_bit(permission: Permission) -> u32 {
    match permission {
        Permission::ReadBuilds => 1,
        Permission::SubmitBuilds => 2,
        Permission::CancelBuilds => 4,
        Permission::ManageDeployments => 8,
        Permission::ManageProjects => 16,
        Permission::OperateControlPlane => 32,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn principal_requires_permission_and_matching_tenant() {
        let principal =
            Principal::new(SubjectId(7), TenantId(3)).with_permission(Permission::SubmitBuilds);
        assert!(principal
            .authorize(TenantId(3), Permission::SubmitBuilds)
            .is_ok());
        assert_eq!(
            principal.authorize(TenantId(3), Permission::CancelBuilds),
            Err(AuthorizationError::PermissionDenied)
        );
        assert_eq!(
            principal.authorize(TenantId(4), Permission::SubmitBuilds),
            Err(AuthorizationError::TenantBoundary)
        );
    }
}
