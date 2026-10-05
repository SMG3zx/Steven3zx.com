//! Bounded identity, tenancy, session, and MFA state.

use crate::{Principal, SubjectId, TenantId};
use base32::Alphabet;
use hmac::{Hmac, Mac};
use rand::Rng;
use sha1::Sha1;
use sha2::{Digest, Sha256};

/// Durable identity and session snapshot.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AuthSnapshot {
    /// Registered tenant-owned accounts.
    pub users: Vec<UserAccount>,
    /// Opaque sessions issued for those accounts.
    pub sessions: Vec<SessionRecord>,
}

/// Failure while restoring an authentication snapshot.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AuthSnapshotError {
    /// The snapshot exceeds the configured directory capacity.
    Capacity,
    /// The snapshot contains duplicate, missing, or mismatched identities.
    Invalid,
}

/// Authentication failure at the identity boundary.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AuthError {
    /// Required identity input was empty.
    Required,
    /// The bounded user or session store has no free slot.
    Capacity,
    /// The email or session token is already registered.
    AlreadyExists,
    /// The requested user or session does not exist.
    NotFound,
    /// The supplied session has expired.
    Expired,
    /// The session belongs to a different tenant than requested.
    TenantBoundary,
    /// The identity must complete MFA before accessing the protected operation.
    MfaRequired,
    /// The supplied email or password was not accepted.
    InvalidCredentials,
}

/// Password verification boundary implemented by the credential adapter.
pub trait PasswordVerifier {
    /// Compares a plaintext login secret with an opaque stored hash.
    fn verify(&self, password: &str, password_hash: &str) -> bool;
}

/// Production bcrypt adapter for Go- and Zig-compatible password hashes.
#[derive(Clone, Copy, Debug, Default)]
pub struct BcryptPasswordVerifier;

impl PasswordVerifier for BcryptPasswordVerifier {
    fn verify(&self, password: &str, password_hash: &str) -> bool {
        bcrypt::verify(password, password_hash).unwrap_or(false)
    }
}

/// User identity and MFA configuration owned by one tenant.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct UserAccount {
    /// Stable subject identity.
    pub subject: SubjectId,
    /// Tenant owning the account.
    pub tenant: TenantId,
    /// Normalized email address.
    pub email: String,
    /// Opaque password-hash representation owned by the password adapter.
    pub password_hash: String,
    /// Whether MFA is required for protected access.
    pub mfa_enabled: bool,
    /// Whether TOTP is configured.
    pub totp_enabled: bool,
    /// Base32 RFC 6238 secret used by the TOTP adapter.
    pub totp_secret: String,
    /// Whether email OTP is configured.
    pub email_otp_enabled: bool,
    /// Number of unused recovery codes; hashes stay outside this boundary.
    pub recovery_codes: u16,
    /// Permission bits assigned by the tenant authorization adapter.
    pub permissions: u32,
}

/// An authenticated opaque session.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct SessionRecord {
    /// Opaque token supplied by the session adapter.
    pub token: String,
    /// Authenticated subject.
    pub subject: SubjectId,
    /// Tenant selected by the authenticated identity.
    pub tenant: TenantId,
    /// Session expiration timestamp in Unix seconds.
    pub expires_at: u64,
    /// Whether the session completed the required MFA challenge.
    pub mfa_verified: bool,
}

#[derive(Clone, Debug, Eq, PartialEq)]
struct EmailOtpChallenge {
    subject: SubjectId,
    code_hash: [u8; 32],
    expires_at: u64,
}

/// Bounded one-time email OTP challenge state.
pub struct EmailOtpStore<const MAX: usize> {
    challenges: Vec<Option<EmailOtpChallenge>>,
}

impl<const MAX: usize> EmailOtpStore<MAX> {
    /// Creates an empty bounded challenge store.
    #[must_use]
    pub fn new() -> Self {
        Self {
            challenges: vec![None; MAX],
        }
    }

    /// Issues a six-digit challenge and returns the code to the delivery adapter.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn issue(&mut self, subject: SubjectId, now: u64, ttl: u64) -> Result<String, AuthError> {
        let mut raw = [0_u8; 4];
        rand::thread_rng() // tigerstyle: allow-direct-randomness — email OTP adapter
            .fill(&mut raw);
        let code = format!("{:06}", u32::from_le_bytes(raw) % 1_000_000);
        let mut hash = [0_u8; 32];
        hash.copy_from_slice(Sha256::digest(code.as_bytes()).as_slice());
        let slot = self
            .challenges
            .iter_mut()
            .find(|entry| entry.is_none())
            .ok_or(AuthError::Capacity)?;
        *slot = Some(EmailOtpChallenge {
            subject,
            code_hash: hash,
            expires_at: now.saturating_add(ttl),
        });
        Ok(code)
    }

    /// Verifies and consumes one unexpired challenge.
    pub fn verify(&mut self, subject: SubjectId, code: &str, now: u64) -> bool {
        let mut hash = [0_u8; 32];
        hash.copy_from_slice(Sha256::digest(code.trim().as_bytes()).as_slice());
        let Some(slot) = self.challenges.iter_mut().find(|entry| {
            entry
                .as_ref()
                .is_some_and(|challenge| challenge.subject == subject && now < challenge.expires_at)
        }) else {
            return false;
        };
        let Some(challenge) = slot.as_ref() else {
            return false;
        };
        let valid = challenge.code_hash == hash;
        if valid {
            *slot = None;
        }
        valid
    }
}

impl<const MAX: usize> Default for EmailOtpStore<MAX> {
    fn default() -> Self {
        Self::new()
    }
}

/// Authenticated identity returned after session and tenant checks.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct AuthenticatedIdentity {
    /// Authenticated subject.
    pub subject: SubjectId,
    /// Authenticated tenant.
    pub tenant: TenantId,
    /// Permission bits copied from the authenticated account.
    pub permissions: u32,
}

impl AuthenticatedIdentity {
    /// Converts the authenticated session into the command-boundary principal.
    #[must_use]
    pub const fn into_principal(self) -> Principal {
        Principal::with_permissions(self.subject, self.tenant, self.permissions)
    }
}

/// Bounded in-memory identity/session directory used by the control-plane core.
pub struct AuthDirectory<const MAX_USERS: usize, const MAX_SESSIONS: usize> {
    users: Vec<Option<UserAccount>>,
    sessions: Vec<Option<SessionRecord>>,
}

impl<const MAX_USERS: usize, const MAX_SESSIONS: usize> AuthDirectory<MAX_USERS, MAX_SESSIONS> {
    /// Creates an empty bounded directory.
    #[must_use]
    pub fn new() -> Self {
        Self {
            users: vec![None; MAX_USERS],
            sessions: vec![None; MAX_SESSIONS],
        }
    }

    /// Registers an account in a bounded user slot.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn register(&mut self, account: UserAccount) -> Result<(), AuthError> {
        let email = normalize_email(&account.email);
        if email.is_empty() || account.password_hash.trim().is_empty() {
            return Err(AuthError::Required);
        }
        if self
            .users
            .iter()
            .flatten()
            .any(|existing| normalize_email(&existing.email) == email)
        {
            return Err(AuthError::AlreadyExists);
        }
        let slot = self
            .users
            .iter_mut()
            .find(|entry| entry.is_none())
            .ok_or(AuthError::Capacity)?;
        let mut account = account;
        account.email = email;
        *slot = Some(account);
        Ok(())
    }

    /// Provisions the first bounded identity record for a newly signed-up account.
    ///
    /// Subject and tenant identifiers are allocated from the current directory
    /// contents so the result is deterministic after restoring a snapshot.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn register_new_account(
        &mut self,
        email: impl Into<String>,
        password_hash: impl Into<String>,
    ) -> Result<UserAccount, AuthError> {
        let subject = self
            .users
            .iter()
            .flatten()
            .map(|account| account.subject.0)
            .max()
            .unwrap_or(0)
            .checked_add(1)
            .map(SubjectId)
            .ok_or(AuthError::Capacity)?;
        let tenant = self
            .users
            .iter()
            .flatten()
            .map(|account| account.tenant.0)
            .max()
            .unwrap_or(0)
            .checked_add(1)
            .map(TenantId)
            .ok_or(AuthError::Capacity)?;
        let permissions = if self.users.iter().all(Option::is_none) {
            // Bootstrap the local control plane exactly once. Every later
            // account starts unprivileged and must be granted capabilities
            // through an already-authorized administration path.
            63
        } else {
            0
        };
        let account = UserAccount {
            subject,
            tenant,
            email: email.into(),
            password_hash: password_hash.into(),
            mfa_enabled: false,
            totp_enabled: false,
            totp_secret: String::default(),
            email_otp_enabled: false,
            recovery_codes: 0,
            permissions,
        };
        self.register(account)?;
        self.user(subject).cloned().ok_or(AuthError::NotFound)
    }

    /// Creates an opaque session for a registered user.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn issue_session(
        &mut self,
        token: impl Into<String>,
        subject: SubjectId,
        expires_at: u64,
        mfa_verified: bool,
    ) -> Result<(), AuthError> {
        let token = token.into().trim().to_owned();
        if token.is_empty() || self.user(subject).is_none() {
            return Err(AuthError::Required);
        }
        if self
            .sessions
            .iter()
            .flatten()
            .any(|session| session.token == token)
        {
            return Err(AuthError::AlreadyExists);
        }
        let tenant = self.user(subject).ok_or(AuthError::NotFound)?.tenant;
        let slot = self
            .sessions
            .iter_mut()
            .find(|entry| entry.is_none())
            .ok_or(AuthError::Capacity)?;
        *slot = Some(SessionRecord {
            token,
            subject,
            tenant,
            expires_at,
            mfa_verified,
        });
        Ok(())
    }

    /// Verifies credentials by email and issues an unauthenticated-MFA session.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn authenticate_password<V: PasswordVerifier>(
        &mut self,
        email: &str,
        password: &str,
        verifier: &V,
        token: impl Into<String>,
        expires_at: u64,
    ) -> Result<SubjectId, AuthError> {
        let email = normalize_email(email);
        let account = self
            .users
            .iter()
            .flatten()
            .find(|account| normalize_email(&account.email) == email)
            .ok_or(AuthError::InvalidCredentials)?;
        let subject = account.subject;
        if !verifier.verify(password, &account.password_hash) {
            return Err(AuthError::InvalidCredentials);
        }
        self.issue_session(token, subject, expires_at, false)?;
        Ok(subject)
    }

    /// Authenticates one session for its owner and requested tenant.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn authenticate(
        &self,
        token: &str,
        tenant: TenantId,
        now: u64,
    ) -> Result<AuthenticatedIdentity, AuthError> {
        let session = self
            .sessions
            .iter()
            .flatten()
            .find(|session| session.token == token.trim())
            .ok_or(AuthError::NotFound)?;
        if now >= session.expires_at {
            return Err(AuthError::Expired);
        }
        if session.tenant != tenant {
            return Err(AuthError::TenantBoundary);
        }
        let account = self.user(session.subject).ok_or(AuthError::NotFound)?;
        if account.mfa_enabled && !session.mfa_verified {
            return Err(AuthError::MfaRequired);
        }
        Ok(AuthenticatedIdentity {
            subject: session.subject,
            tenant: session.tenant,
            permissions: account.permissions,
        })
    }

    /// Authenticates a session and materializes the command-boundary principal.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn authenticate_principal(
        &self,
        token: &str,
        tenant: TenantId,
        now: u64,
    ) -> Result<Principal, AuthError> {
        Ok(self.authenticate(token, tenant, now)?.into_principal())
    }

    /// Revokes one opaque session.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn revoke_session(&mut self, token: &str) -> Result<(), AuthError> {
        let session = self
            .sessions
            .iter_mut()
            .find(|entry| {
                entry
                    .as_ref()
                    .is_some_and(|value| value.token == token.trim())
            })
            .ok_or(AuthError::NotFound)?;
        *session = None;
        Ok(())
    }

    /// Disables TOTP for one authenticated account and updates its MFA requirement.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn disable_totp(&mut self, subject: SubjectId) -> Result<(), AuthError> {
        let account = self.user_mut(subject).ok_or(AuthError::NotFound)?;
        account.totp_enabled = false;
        account.totp_secret.clear();
        account.mfa_enabled = account.email_otp_enabled;
        Ok(())
    }

    /// Enrolls a pending TOTP secret and returns its provisioning URI.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn enroll_totp(&mut self, subject: SubjectId) -> Result<String, AuthError> {
        let account = self.user_mut(subject).ok_or(AuthError::NotFound)?;
        let mut raw = [0_u8; 20];
        rand::thread_rng() // tigerstyle: allow-direct-randomness — TOTP secret adapter
            .fill(&mut raw);
        let secret = base32::encode(Alphabet::Rfc4648 { padding: false }, &raw);
        account.totp_secret.clone_from(&secret);
        Ok(totp_provisioning_uri(&secret, &account.email))
    }

    /// Verifies a current TOTP code and marks the subject's sessions MFA-ready.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn verify_totp(
        &mut self,
        subject: SubjectId,
        code: &str,
        now: u64,
    ) -> Result<(), AuthError> {
        let account = self.user(subject).ok_or(AuthError::NotFound)?;
        if !verify_totp_code(&account.totp_secret, code, now) {
            return Err(AuthError::InvalidCredentials);
        }
        let account = self.user_mut(subject).ok_or(AuthError::NotFound)?;
        account.totp_enabled = true;
        account.mfa_enabled = true;
        for session in self.sessions.iter_mut().flatten() {
            if session.subject == subject {
                session.mfa_verified = true;
            }
        }
        Ok(())
    }

    /// Disables email OTP for one authenticated account and updates its MFA requirement.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn disable_email_otp(&mut self, subject: SubjectId) -> Result<(), AuthError> {
        let account = self.user_mut(subject).ok_or(AuthError::NotFound)?;
        account.email_otp_enabled = false;
        account.mfa_enabled = account.totp_enabled;
        Ok(())
    }

    /// Enables email OTP and promotes current sessions after code verification.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn enable_email_otp(&mut self, subject: SubjectId) -> Result<(), AuthError> {
        let account = self.user_mut(subject).ok_or(AuthError::NotFound)?;
        account.email_otp_enabled = true;
        account.mfa_enabled = true;
        for session in self.sessions.iter_mut().flatten() {
            if session.subject == subject {
                session.mfa_verified = true;
            }
        }
        Ok(())
    }

    /// Reads a registered account by subject.
    #[must_use]
    pub fn user(&self, subject: SubjectId) -> Option<&UserAccount> {
        self.users
            .iter()
            .flatten()
            .find(|account| account.subject == subject)
    }

    /// Finds one normalized account by email for the external credential
    /// verifier boundary.
    #[must_use]
    pub fn users_by_email(&self, email: &str) -> Option<&UserAccount> {
        let email = normalize_email(email);
        self.users
            .iter()
            .flatten()
            .find(|account| normalize_email(&account.email) == email)
    }

    fn user_mut(&mut self, subject: SubjectId) -> Option<&mut UserAccount> {
        self.users
            .iter_mut()
            .flatten()
            .find(|account| account.subject == subject)
    }

    /// Captures bounded identity and session state for durable storage.
    #[must_use]
    pub fn snapshot(&self) -> AuthSnapshot {
        AuthSnapshot {
            users: self.users.iter().flatten().cloned().collect(),
            sessions: self.sessions.iter().flatten().cloned().collect(),
        }
    }

    /// Restores identity and session state after validating all ownership links.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn restore(&mut self, snapshot: &AuthSnapshot) -> Result<(), AuthSnapshotError> {
        if snapshot.users.len() > MAX_USERS || snapshot.sessions.len() > MAX_SESSIONS {
            return Err(AuthSnapshotError::Capacity);
        }
        let mut candidate = Self::new();
        for user in snapshot.users.iter().cloned() {
            candidate
                .register(user)
                .map_err(|_| AuthSnapshotError::Invalid)?;
        }
        for session in snapshot.sessions.iter().cloned() {
            if session.token.trim().is_empty()
                || candidate
                    .sessions
                    .iter()
                    .flatten()
                    .any(|existing| existing.token == session.token)
                || candidate.user(session.subject).map(|user| user.tenant) != Some(session.tenant)
            {
                return Err(AuthSnapshotError::Invalid);
            }
            let slot = candidate
                .sessions
                .iter_mut()
                .find(|entry| entry.is_none())
                .ok_or(AuthSnapshotError::Capacity)?;
            *slot = Some(session);
        }
        *self = candidate;
        Ok(())
    }

    /// Replaces the process projection from authoritative identity and session
    /// rows. Credential verification remains outside this projection when the
    /// `SpacetimeDB` module owns the private credential table.
    ///
    /// # Errors
    ///
    /// Returns an error when the subscribed rows violate auth invariants.
    pub fn hydrate_projection(
        &mut self,
        users: Vec<UserAccount>,
        sessions: Vec<SessionRecord>,
    ) -> Result<(), AuthSnapshotError> {
        self.restore(&AuthSnapshot { users, sessions })
    }
}

impl<const MAX_USERS: usize, const MAX_SESSIONS: usize> Default
    for AuthDirectory<MAX_USERS, MAX_SESSIONS>
{
    fn default() -> Self {
        Self::new()
    }
}

fn normalize_email(email: &str) -> String {
    email.trim().to_ascii_lowercase()
}

fn totp_provisioning_uri(secret: &str, email: &str) -> String {
    let label = format!("Janus:{}", email.trim());
    format!(
        "otpauth://totp/{}?secret={}&issuer=Janus",
        label.replace(' ', "%20"),
        secret
    )
}

fn verify_totp_code(secret: &str, code: &str, now: u64) -> bool {
    let Some(key) = base32::decode(Alphabet::Rfc4648 { padding: false }, secret.trim()) else {
        return false;
    };
    if code.trim().len() != 6 || !code.trim().bytes().all(|value| value.is_ascii_digit()) {
        return false;
    }
    [-1_i64, 0, 1].into_iter().any(|offset| {
        let counter = if offset.is_negative() {
            now.saturating_sub(offset.unsigned_abs().saturating_mul(30)) / 30
        } else {
            now.saturating_add(offset.unsigned_abs().saturating_mul(30)) / 30
        };
        let Ok(mut mac) = Hmac::<Sha1>::new_from_slice(&key) else {
            return false;
        };
        mac.update(&counter.to_be_bytes());
        let digest = mac.finalize().into_bytes();
        let index = usize::from(digest[19] & 0x0f);
        let value = (u32::from(digest[index]) << 24)
            | (u32::from(digest[index + 1]) << 16)
            | (u32::from(digest[index + 2]) << 8)
            | u32::from(digest[index + 3]);
        let expected = (value & 0x7fff_ffff) % 1_000_000;
        let mut difference = 0_u8;
        for (actual, wanted) in format!("{expected:06}").bytes().zip(code.trim().bytes()) {
            difference |= actual ^ wanted;
        }
        difference == 0
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn account(mfa_enabled: bool) -> UserAccount {
        UserAccount {
            subject: SubjectId(7),
            tenant: TenantId(3),
            email: " User@Example.com ".to_owned(),
            password_hash: "adapter-hash".to_owned(),
            mfa_enabled,
            totp_enabled: mfa_enabled,
            totp_secret: if mfa_enabled {
                "JBSWY3DPEHPK3PXP".to_owned()
            } else {
                String::default()
            },
            email_otp_enabled: false,
            recovery_codes: 0,
            permissions: 63,
        }
    }

    struct TestVerifier;

    impl PasswordVerifier for TestVerifier {
        fn verify(&self, password: &str, password_hash: &str) -> bool {
            password == "correct" && password_hash == "adapter-hash"
        }
    }

    #[test]
    fn password_verifier_controls_session_issuance() {
        let mut directory = AuthDirectory::<1, 1>::new();
        directory.register(account(false)).unwrap();
        assert_eq!(
            directory.authenticate_password(
                "user@example.com",
                "wrong",
                &TestVerifier,
                "token-1",
                20
            ),
            Err(AuthError::InvalidCredentials)
        );
        assert_eq!(
            directory.authenticate_password(
                "USER@example.com",
                "correct",
                &TestVerifier,
                "token-1",
                20
            ),
            Ok(SubjectId(7))
        );
        assert!(directory.authenticate("token-1", TenantId(3), 19).is_ok());
    }

    #[test]
    fn bcrypt_verifier_accepts_legacy_go_and_zig_hashes() {
        let verifier = BcryptPasswordVerifier;
        assert!(verifier.verify(
            "password",
            "$2a$10$raV7DRKl2iEmmd70abrFv.Z9cDK7jQM2TtNHKpuIxLLtkTVrZGIDG"
        ));
        assert!(verifier.verify(
            "password",
            "$2b$10$QvWDOsLZqARNQS.z6d/JGOw/dkTlKzV.SSBB4qtOl13o5v.BcVY7q"
        ));
        assert!(!verifier.verify(
            "wrong-password",
            "$2a$10$raV7DRKl2iEmmd70abrFv.Z9cDK7jQM2TtNHKpuIxLLtkTVrZGIDG"
        ));
    }

    #[test]
    fn new_account_allocates_distinct_subject_and_tenant() {
        let mut directory = AuthDirectory::<2, 2>::new();
        let account = directory
            .register_new_account("New@Example.com", "bcrypt-hash")
            .unwrap();
        assert_eq!(account.subject, SubjectId(1));
        assert_eq!(account.tenant, TenantId(1));
        assert_eq!(account.email, "new@example.com");
        assert_eq!(account.permissions, 63);
        assert_eq!(directory.user(SubjectId(1)), Some(&account));
    }

    #[test]
    fn only_the_first_new_account_is_bootstrap_admin() {
        let mut directory = AuthDirectory::<2, 2>::new();
        let first = directory
            .register_new_account("first@example.com", "first-hash")
            .unwrap();
        let second = directory
            .register_new_account("second@example.com", "second-hash")
            .unwrap();

        assert_eq!(first.permissions, 63);
        assert_eq!(second.permissions, 0);
    }

    #[test]
    fn totp_verification_matches_rfc6238_and_promotes_sessions() {
        let mut directory = AuthDirectory::<1, 1>::new();
        directory
            .register(UserAccount {
                subject: SubjectId(7),
                tenant: TenantId(3),
                email: "totp@example.com".to_owned(),
                password_hash: "adapter-hash".to_owned(),
                mfa_enabled: false,
                totp_enabled: false,
                totp_secret: "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ".to_owned(),
                email_otp_enabled: false,
                recovery_codes: 0,
                permissions: 0,
            })
            .unwrap();
        directory
            .issue_session("totp-session", SubjectId(7), 100, false)
            .unwrap();
        directory.verify_totp(SubjectId(7), "287082", 59).unwrap();
        assert!(directory.user(SubjectId(7)).unwrap().totp_enabled);
        assert!(directory
            .authenticate("totp-session", TenantId(3), 59)
            .is_ok());
    }

    #[test]
    fn email_otp_is_bounded_expiring_and_single_use() {
        let mut store = EmailOtpStore::<1>::new();
        let code = store.issue(SubjectId(7), 100, 10).unwrap();
        assert!(!store.verify(SubjectId(8), &code, 101));
        assert!(store.verify(SubjectId(7), &code, 101));
        assert!(!store.verify(SubjectId(7), &code, 101));
        let next = store.issue(SubjectId(7), 100, 10).unwrap();
        assert!(!store.verify(SubjectId(7), &next, 110));
    }

    #[test]
    fn sessions_enforce_tenant_expiration_and_revocation() {
        let mut directory = AuthDirectory::<2, 2>::new();
        directory.register(account(false)).unwrap();
        directory
            .issue_session("token-1", SubjectId(7), 20, false)
            .unwrap();
        assert_eq!(
            directory.authenticate("token-1", TenantId(3), 19),
            Ok(AuthenticatedIdentity {
                subject: SubjectId(7),
                tenant: TenantId(3),
                permissions: 63,
            })
        );
        assert_eq!(
            directory.authenticate("token-1", TenantId(4), 19),
            Err(AuthError::TenantBoundary)
        );
        assert_eq!(
            directory.authenticate("token-1", TenantId(3), 20),
            Err(AuthError::Expired)
        );
        directory.revoke_session("token-1").unwrap();
        assert_eq!(
            directory.authenticate("token-1", TenantId(3), 19),
            Err(AuthError::NotFound)
        );
    }

    #[test]
    fn mfa_required_sessions_are_rejected_until_verified() {
        let mut directory = AuthDirectory::<1, 1>::new();
        directory.register(account(true)).unwrap();
        directory
            .issue_session("token-1", SubjectId(7), 20, false)
            .unwrap();
        assert_eq!(
            directory.authenticate("token-1", TenantId(3), 19),
            Err(AuthError::MfaRequired)
        );
        directory.revoke_session("token-1").unwrap();
        directory
            .issue_session("token-2", SubjectId(7), 20, true)
            .unwrap();
        assert!(directory.authenticate("token-2", TenantId(3), 19).is_ok());
    }

    #[test]
    fn disabling_totp_updates_account_mfa_state() {
        let mut directory = AuthDirectory::<1, 1>::new();
        directory.register(account(true)).unwrap();
        directory.disable_totp(SubjectId(7)).unwrap();
        let value = directory.user(SubjectId(7)).unwrap();
        assert!(!value.totp_enabled);
        assert!(!value.mfa_enabled);
    }

    #[test]
    fn disabling_email_otp_updates_account_mfa_state() {
        let mut directory = AuthDirectory::<1, 1>::new();
        let mut value = account(false);
        value.email_otp_enabled = true;
        value.mfa_enabled = true;
        directory.register(value).unwrap();
        directory.disable_email_otp(SubjectId(7)).unwrap();
        let account = directory.user(SubjectId(7)).unwrap();
        assert!(!account.email_otp_enabled);
        assert!(!account.mfa_enabled);
    }

    #[test]
    fn auth_snapshot_round_trips_permissions_sessions_and_mfa_state() {
        let mut directory = AuthDirectory::<2, 2>::new();
        directory.register(account(true)).unwrap();
        directory
            .issue_session("token-1", SubjectId(7), 20, true)
            .unwrap();
        let snapshot = directory.snapshot();
        let mut restored = AuthDirectory::<2, 2>::new();
        restored.restore(&snapshot).unwrap();
        assert_eq!(restored.snapshot(), snapshot);
        assert_eq!(
            restored
                .authenticate("token-1", TenantId(3), 19)
                .unwrap()
                .permissions,
            63
        );
    }
}
