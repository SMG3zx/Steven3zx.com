//! Tenant-owned project and repository-check components.

use crate::{EntityId, TenantId};

/// Maximum repository validation stages retained in one result.
pub const MAX_REPO_CHECK_STAGES: usize = 8;

/// A single repository validation stage.
#[derive(Clone, Debug, Eq, PartialEq, serde::Deserialize, serde::Serialize)]
pub struct RepoCheckStage {
    /// Stable stage name.
    pub name: String,
    /// Stage result, such as `passed` or `failed`.
    pub status: String,
    /// Human-readable stage detail.
    pub message: String,
}

/// Result of validating a project's configured repository.
#[derive(Clone, Debug, Eq, PartialEq, serde::Deserialize, serde::Serialize)]
pub struct RepoCheckResult {
    /// Whether every required stage passed.
    pub ok: bool,
    /// Overall result status.
    pub status: String,
    /// Overall result detail.
    pub message: String,
    /// Repository URL that was checked.
    pub repo_url: String,
    /// Branch or source reference that was checked.
    pub branch: String,
    /// Ordered bounded validation stages.
    pub stages: Vec<RepoCheckStage>,
    /// Unix timestamp supplied by the adapter.
    pub checked_at: u64,
}

/// Tenant-owned project component.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Project {
    /// Entity identity.
    pub id: EntityId,
    /// Owning tenant.
    pub tenant: TenantId,
    /// Display name.
    pub name: String,
    /// Stable URL-safe slug.
    pub slug: String,
    /// Optional human-readable description.
    pub description: String,
    /// Project status.
    pub status: String,
    /// Repository provider, such as `github`.
    pub repo_provider: String,
    /// Repository URL.
    pub repo_url: String,
    /// Repository branch or source reference.
    pub repo_branch: String,
    /// Most recent repository validation result.
    pub repo_check: Option<RepoCheckResult>,
    /// Creation timestamp supplied by the adapter.
    pub created_at: u64,
    /// Last update timestamp supplied by the adapter.
    pub updated_at: u64,
}

/// Failure while inserting or validating a project component.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ProjectError {
    /// A required project field is empty.
    Required,
    /// A project slug contains unsupported characters.
    InvalidSlug,
    /// The configured repository URL is not an HTTPS URL.
    InvalidRepositoryUrl,
    /// The configured repository provider is unsupported.
    InvalidProvider,
    /// The validation result contains too many stages.
    TooManyStages,
    /// The project entity slot is outside the world capacity.
    Capacity,
    /// The project entity already has a component.
    AlreadyExists,
}

/// Repository validation adapter implemented by local or remote Git providers.
pub trait RepositoryValidator {
    /// Validates one repository URL and source branch at a caller timestamp.
    fn validate(&self, repo_url: &str, branch: &str, checked_at: u64) -> RepoCheckResult;
}

/// Deterministic local validator used before a provider adapter is configured.
#[derive(Clone, Copy, Debug, Default)]
pub struct LocalRepositoryValidator;

impl RepositoryValidator for LocalRepositoryValidator {
    fn validate(&self, repo_url: &str, branch: &str, checked_at: u64) -> RepoCheckResult {
        let repo_url = repo_url.trim();
        let branch = branch.trim();
        let url_ok = repo_url.starts_with("https://") && repo_url.len() > "https://".len();
        let branch_ok =
            !branch.is_empty() && branch.len() <= 256 && !branch.chars().any(char::is_whitespace);
        let stages = vec![
            RepoCheckStage {
                name: "repository_url".to_owned(),
                status: if url_ok { "passed" } else { "failed" }.to_owned(),
                message: if url_ok {
                    "repository URL is HTTPS"
                } else {
                    "repository URL must use HTTPS"
                }
                .to_owned(),
            },
            RepoCheckStage {
                name: "source_branch".to_owned(),
                status: if branch_ok { "passed" } else { "failed" }.to_owned(),
                message: if branch_ok {
                    "source branch is bounded"
                } else {
                    "source branch is empty, too long, or contains whitespace"
                }
                .to_owned(),
            },
        ];
        let ok = url_ok && branch_ok;
        RepoCheckResult {
            ok,
            status: if ok { "passed" } else { "failed" }.to_owned(),
            message: if ok {
                "repository configuration accepted"
            } else {
                "repository configuration rejected"
            }
            .to_owned(),
            repo_url: repo_url.to_owned(),
            branch: branch.to_owned(),
            stages,
            checked_at,
        }
    }
}

impl Project {
    /// Validates bounded project and repository configuration.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn validate(&self) -> Result<(), ProjectError> {
        if self.name.trim().is_empty()
            || self.slug.trim().is_empty()
            || self.repo_url.trim().is_empty()
            || self.repo_branch.trim().is_empty()
        {
            return Err(ProjectError::Required);
        }
        if !self.slug.chars().all(|character| {
            character.is_ascii_lowercase() || character.is_ascii_digit() || character == '-'
        }) {
            return Err(ProjectError::InvalidSlug);
        }
        if !self.repo_url.starts_with("https://") {
            return Err(ProjectError::InvalidRepositoryUrl);
        }
        if !matches!(
            self.repo_provider.as_str(),
            "github" | "gitlab" | "bitbucket"
        ) {
            return Err(ProjectError::InvalidProvider);
        }
        if self
            .repo_check
            .as_ref()
            .is_some_and(|result| result.stages.len() > MAX_REPO_CHECK_STAGES)
        {
            return Err(ProjectError::TooManyStages);
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn project() -> Project {
        Project {
            id: EntityId(2),
            tenant: TenantId(7),
            name: "Janus".to_owned(),
            slug: "janus".to_owned(),
            description: "control plane".to_owned(),
            status: "active".to_owned(),
            repo_provider: "github".to_owned(),
            repo_url: "https://github.com/example/janus".to_owned(),
            repo_branch: "main".to_owned(),
            repo_check: None,
            created_at: 1,
            updated_at: 1,
        }
    }

    #[test]
    fn project_validation_preserves_repository_boundaries() {
        assert!(project().validate().is_ok());
        let mut invalid = project();
        invalid.repo_url = "http://github.com/example/janus".to_owned();
        assert_eq!(invalid.validate(), Err(ProjectError::InvalidRepositoryUrl));
        invalid = project();
        invalid.slug = "Janus_Rust".to_owned();
        assert_eq!(invalid.validate(), Err(ProjectError::InvalidSlug));
    }

    #[test]
    fn repository_check_stage_count_is_bounded() {
        let mut value = project();
        value.repo_check = Some(RepoCheckResult {
            ok: true,
            status: "passed".to_owned(),
            message: "ok".to_owned(),
            repo_url: value.repo_url.clone(),
            branch: value.repo_branch.clone(),
            stages: (0..=MAX_REPO_CHECK_STAGES)
                .map(|index| RepoCheckStage {
                    name: format!("stage-{index}"),
                    status: "passed".to_owned(),
                    message: String::new(),
                })
                .collect(),
            checked_at: 2,
        });
        assert_eq!(value.validate(), Err(ProjectError::TooManyStages));
    }

    #[test]
    fn local_repository_validator_is_deterministic_and_bounded() {
        let validator = LocalRepositoryValidator;
        let valid = validator.validate(" https://github.com/example/janus ", "main", 9);
        assert!(valid.ok);
        assert_eq!(valid.stages.len(), 2);
        assert_eq!(valid.checked_at, 9);
        let invalid = validator.validate("http://github.com/example/janus", "bad branch", 10);
        assert!(!invalid.ok);
        assert_eq!(invalid.status, "failed");
    }
}
