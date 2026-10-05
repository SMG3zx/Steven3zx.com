//! Bounded file persistence for authentication snapshots.

use std::fs::{create_dir_all, read, OpenOptions};
use std::io::{self, Write};
use std::path::Path;

use aes_gcm::{aead::Aead, Aes256Gcm, KeyInit, Nonce};
use base64::{engine::general_purpose::STANDARD_NO_PAD, Engine};
use rand::Rng;
use sha2::{Digest, Sha256};

use crate::{
    AuthDirectory, AuthSnapshot, AuthSnapshotError, SessionRecord, SubjectId, TenantId, UserAccount,
};

/// Maximum encoded authentication snapshot size.
pub const MAX_AUTH_SNAPSHOT_BYTES: usize = 4 * 1024 * 1024;

/// Failure while saving or loading authentication state.
#[derive(Debug)]
pub enum AuthStoreError {
    /// The underlying file operation failed.
    Io(io::Error),
    /// The file was malformed or exceeded bounds.
    Corrupt,
    /// The decoded state violated authentication invariants.
    Invalid(AuthSnapshotError),
    /// The encoded state exceeded the file adapter limit.
    Capacity,
}

/// Versioned file-backed authentication snapshot store.
pub struct FileAuthStore {
    path: String,
    encryption_key: [u8; 32],
}

impl FileAuthStore {
    /// Creates a store at the supplied path.
    pub fn new(path: impl Into<String>) -> Self {
        Self::with_secret(path, "janus-dev-insecure-mfa-key")
    }

    /// Creates a store using an explicit secret supplied by the application boundary.
    pub fn with_secret(path: impl Into<String>, secret: &str) -> Self {
        Self {
            path: path.into(),
            encryption_key: Sha256::digest(secret.as_bytes()).into(),
        }
    }

    /// Saves one bounded authentication snapshot and syncs it to disk.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn save<const MAX_USERS: usize, const MAX_SESSIONS: usize>(
        &self,
        directory: &AuthDirectory<MAX_USERS, MAX_SESSIONS>,
    ) -> Result<(), AuthStoreError> {
        let snapshot = directory.snapshot();
        let mut encoder = Encoder::new();
        encoder.bytes(b"JNS-AUT2");
        encoder.u32(u32::try_from(snapshot.users.len()).unwrap_or(u32::MAX));
        for user in &snapshot.users {
            encode_user(&mut encoder, user, &self.encryption_key)?;
        }
        encoder.u32(u32::try_from(snapshot.sessions.len()).unwrap_or(u32::MAX));
        for session in &snapshot.sessions {
            encode_session(&mut encoder, session);
        }
        if encoder.data.len() > MAX_AUTH_SNAPSHOT_BYTES {
            return Err(AuthStoreError::Capacity);
        }
        let path = Path::new(&self.path);
        if let Some(parent) = path.parent() {
            if !parent.as_os_str().is_empty() {
                create_dir_all(parent).map_err(AuthStoreError::Io)?;
            }
        }
        let mut file = OpenOptions::new()
            .create(true)
            .truncate(true)
            .write(true)
            .open(path)
            .map_err(AuthStoreError::Io)?;
        file.write_all(&encoder.data).map_err(AuthStoreError::Io)?;
        file.sync_all().map_err(AuthStoreError::Io)
    }

    /// Loads authentication state into a bounded directory, if the file exists.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn load<const MAX_USERS: usize, const MAX_SESSIONS: usize>(
        &self,
        directory: &mut AuthDirectory<MAX_USERS, MAX_SESSIONS>,
    ) -> Result<bool, AuthStoreError> {
        let data = match read(&self.path) {
            Ok(data) => data,
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(false),
            Err(error) => return Err(AuthStoreError::Io(error)),
        };
        if data.len() > MAX_AUTH_SNAPSHOT_BYTES {
            return Err(AuthStoreError::Capacity);
        }
        let mut decoder = Decoder::new(&data);
        let Some(version) = decoder.take(8) else {
            return Err(AuthStoreError::Corrupt);
        };
        let has_totp_secret = match version.as_slice() {
            b"JNS-AUTH" => false,
            b"JNS-AUT2" => true,
            _ => return Err(AuthStoreError::Corrupt),
        };
        let user_count = bounded_count(&mut decoder, MAX_USERS)?;
        let mut users = Vec::with_capacity(user_count.min(MAX_USERS));
        for _ in 0..user_count {
            users.push(
                decode_user(&mut decoder, has_totp_secret, &self.encryption_key)
                    .ok_or(AuthStoreError::Corrupt)?,
            );
        }
        let session_count = bounded_count(&mut decoder, MAX_SESSIONS)?;
        let mut sessions = Vec::with_capacity(session_count.min(MAX_SESSIONS));
        for _ in 0..session_count {
            sessions.push(decode_session(&mut decoder).ok_or(AuthStoreError::Corrupt)?);
        }
        if !decoder.finished() {
            return Err(AuthStoreError::Corrupt);
        }
        directory
            .restore(&AuthSnapshot { users, sessions })
            .map_err(AuthStoreError::Invalid)?;
        Ok(true)
    }
}

struct Encoder {
    data: Vec<u8>,
}

impl Encoder {
    fn new() -> Self {
        Self {
            data: Vec::with_capacity(MAX_AUTH_SNAPSHOT_BYTES),
        }
    }

    fn bytes(&mut self, value: &[u8]) {
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
}

struct Decoder<'a> {
    data: &'a [u8],
    offset: usize,
}

impl<'a> Decoder<'a> {
    const fn new(data: &'a [u8]) -> Self {
        Self { data, offset: 0 }
    }

    fn take(&mut self, count: usize) -> Option<Vec<u8>> {
        let end = self.offset.checked_add(count)?;
        let value = self.data.get(self.offset..end)?.to_vec();
        self.offset = end;
        Some(value)
    }

    fn u8(&mut self) -> Option<u8> {
        self.take(1)?.first().copied()
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
        String::from_utf8(self.take(length)?).ok()
    }

    const fn finished(&self) -> bool {
        self.offset == self.data.len()
    }
}

fn bounded_count(decoder: &mut Decoder<'_>, maximum: usize) -> Result<usize, AuthStoreError> {
    let count = usize::try_from(decoder.u32().ok_or(AuthStoreError::Corrupt)?)
        .map_err(|_| AuthStoreError::Corrupt)?;
    if count > maximum {
        Err(AuthStoreError::Capacity)
    } else {
        Ok(count)
    }
}

fn encode_user(
    encoder: &mut Encoder,
    user: &UserAccount,
    encryption_key: &[u8; 32],
) -> Result<(), AuthStoreError> {
    encoder.u64(user.subject.0);
    encoder.u32(user.tenant.0);
    encoder.string(&user.email);
    encoder.string(&user.password_hash);
    encoder.u8(u8::from(user.mfa_enabled));
    encoder.u8(u8::from(user.totp_enabled));
    encoder.string(&protect_totp_secret(&user.totp_secret, encryption_key)?);
    encoder.u8(u8::from(user.email_otp_enabled));
    encoder.u16(user.recovery_codes);
    encoder.u32(user.permissions);
    Ok(())
}

fn decode_user(
    decoder: &mut Decoder<'_>,
    has_totp_secret: bool,
    encryption_key: &[u8; 32],
) -> Option<UserAccount> {
    let subject = SubjectId(decoder.u64()?);
    let tenant = TenantId(decoder.u32()?);
    let email = decoder.string()?;
    let password_hash = decoder.string()?;
    let mfa_enabled = decoder.u8()? == 1;
    let totp_enabled = decoder.u8()? == 1;
    let totp_secret = if has_totp_secret {
        reveal_totp_secret(&decoder.string()?, encryption_key)?
    } else {
        String::default()
    };
    Some(UserAccount {
        subject,
        tenant,
        email,
        password_hash,
        mfa_enabled,
        totp_enabled,
        totp_secret,
        email_otp_enabled: decoder.u8()? == 1,
        recovery_codes: decoder.u16()?,
        permissions: decoder.u32()?,
    })
}

fn protect_totp_secret(secret: &str, encryption_key: &[u8; 32]) -> Result<String, AuthStoreError> {
    if secret.is_empty() {
        return Ok(String::default());
    }
    let cipher = Aes256Gcm::new_from_slice(encryption_key).map_err(|_| AuthStoreError::Corrupt)?;
    let mut nonce_bytes = [0_u8; 12];
    rand::thread_rng() // tigerstyle: allow-direct-randomness — persistence nonce adapter
        .fill(&mut nonce_bytes);
    let nonce = Nonce::from_slice(&nonce_bytes);
    let ciphertext = cipher
        .encrypt(nonce, secret.as_bytes())
        .map_err(|_| AuthStoreError::Corrupt)?;
    let mut payload = nonce_bytes.to_vec();
    payload.extend_from_slice(&ciphertext);
    Ok(format!("JNSG1:{}", STANDARD_NO_PAD.encode(payload)))
}

fn reveal_totp_secret(stored: &str, encryption_key: &[u8; 32]) -> Option<String> {
    if stored.is_empty() {
        return Some(String::default());
    }
    let Some(encoded) = stored.strip_prefix("JNSG1:") else {
        return Some(stored.to_owned());
    };
    let payload = STANDARD_NO_PAD.decode(encoded).ok()?;
    if payload.len() < 12 {
        return None;
    }
    let (nonce_bytes, ciphertext) = payload.split_at(12);
    let cipher = Aes256Gcm::new_from_slice(encryption_key).ok()?;
    let nonce = Nonce::from_slice(nonce_bytes);
    String::from_utf8(cipher.decrypt(nonce, ciphertext).ok()?).ok()
}

fn encode_session(encoder: &mut Encoder, session: &SessionRecord) {
    encoder.string(&session.token);
    encoder.u64(session.subject.0);
    encoder.u32(session.tenant.0);
    encoder.u64(session.expires_at);
    encoder.u8(u8::from(session.mfa_verified));
}

fn decode_session(decoder: &mut Decoder<'_>) -> Option<SessionRecord> {
    Some(SessionRecord {
        token: decoder.string()?,
        subject: SubjectId(decoder.u64()?),
        tenant: TenantId(decoder.u32()?),
        expires_at: decoder.u64()?,
        mfa_verified: decoder.u8()? == 1,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn file_auth_store_round_trips_tenant_session_and_permissions() {
        let path = std::env::temp_dir().join(format!("janus-auth-{}.bin", std::process::id()));
        let _ = std::fs::remove_file(&path);
        let mut source = AuthDirectory::<2, 2>::new();
        source
            .register(UserAccount {
                subject: SubjectId(7),
                tenant: TenantId(3),
                email: "user@example.com".to_owned(),
                password_hash: "adapter-hash".to_owned(),
                mfa_enabled: true,
                totp_enabled: true,
                totp_secret: "JBSWY3DPEHPK3PXP".to_owned(),
                email_otp_enabled: false,
                recovery_codes: 2,
                permissions: 16,
            })
            .unwrap();
        source
            .issue_session("token-1", SubjectId(7), 20, true)
            .unwrap();
        let store = FileAuthStore::new(path.to_string_lossy().into_owned());
        store.save(&source).unwrap();
        let persisted = std::fs::read(&path).unwrap();
        assert!(!persisted
            .windows(b"JBSWY3DPEHPK3PXP".len())
            .any(|window| window == b"JBSWY3DPEHPK3PXP"));
        let mut restored = AuthDirectory::<2, 2>::new();
        assert!(store.load(&mut restored).unwrap());
        assert_eq!(restored.snapshot(), source.snapshot());
        let _ = std::fs::remove_file(path);
    }
}
