//! Uncompressed Java Edition 26.3 (protocol 777) login codecs.
//! Decode takes fields without framing; encode returns (packet ID, fields).
use std::io::{self, Write};
use uuid::Uuid;

pub const PROTOCOL_VERSION: i32 = 777;
const MAX_BYTES: usize = 0x1f_ffff;
pub type CodecResult<T> = Result<T, &'static str>;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LoginStart {
    pub name: String,
    pub player_uuid: Uuid,
}
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Property {
    pub name: String,
    pub value: String,
    pub signature: Option<String>,
}
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GameProfile {
    pub uuid: Uuid,
    pub username: String,
    pub properties: Vec<Property>,
}
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LoginSuccess {
    pub profile: GameProfile,
    pub session_id: Uuid,
}
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Serverbound {
    Start(LoginStart),
    EncryptionResponse {
        shared_secret: Vec<u8>,
        verify_token: Vec<u8>,
    },
    PluginResponse {
        message_id: i32,
        data: Option<Vec<u8>>,
    },
    Acknowledged,
    CookieResponse {
        key: String,
        payload: Option<Vec<u8>>,
    },
}
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Clientbound {
    Disconnect {
        reason: String,
    },
    EncryptionRequest {
        server_id: String,
        public_key: Vec<u8>,
        verify_token: Vec<u8>,
        should_authenticate: bool,
    },
    Success(LoginSuccess),
    SetCompression {
        threshold: i32,
    },
    PluginRequest {
        message_id: i32,
        channel: String,
        data: Vec<u8>,
    },
    CookieRequest {
        key: String,
    },
}

pub(crate) struct Reader<'a>(pub(crate) &'a [u8]);
impl<'a> Reader<'a> {
    pub(crate) fn take(&mut self, n: usize) -> CodecResult<&'a [u8]> {
        if n > self.0.len() {
            return Err("truncated protocol field");
        }
        let (v, rest) = self.0.split_at(n);
        self.0 = rest;
        Ok(v)
    }
    pub(crate) fn int(&mut self) -> CodecResult<i32> {
        let mut v = 0u32;
        for i in 0..5 {
            let b = self.take(1)?[0];
            if i == 4 && b & 0xf0 != 0 {
                return Err("invalid VarInt");
            }
            v |= u32::from(b & 127) << (i * 7);
            if b & 128 == 0 {
                return Ok(v as i32);
            }
        }
        Err("invalid VarInt")
    }
    pub(crate) fn length(&mut self, max: usize) -> CodecResult<usize> {
        let n = self.int()?;
        if n < 0 || n as usize > max {
            return Err("field length exceeds bound");
        }
        Ok(n as usize)
    }
    pub(crate) fn boolean(&mut self) -> CodecResult<bool> {
        match self.take(1)?[0] {
            0 => Ok(false),
            1 => Ok(true),
            _ => Err("invalid boolean"),
        }
    }
    pub(crate) fn string(&mut self, max: usize) -> CodecResult<String> {
        let n = self.length(max * 3)?;
        let s = std::str::from_utf8(self.take(n)?).map_err(|_| "invalid UTF-8")?;
        if s.encode_utf16().count() > max {
            return Err("string exceeds character bound");
        }
        Ok(s.to_owned())
    }
    pub(crate) fn identifier(&mut self) -> CodecResult<String> {
        let s = self.string(32767)?;
        validate_identifier(&s)?;
        Ok(s)
    }
    fn bytes(&mut self, max: usize) -> CodecResult<Vec<u8>> {
        let n = self.length(max)?;
        Ok(self.take(n)?.to_vec())
    }
    pub(crate) fn rest(&mut self, max: usize) -> CodecResult<Vec<u8>> {
        if self.0.len() > max {
            return Err("remaining data exceeds bound");
        }
        self.take(self.0.len()).map(<[u8]>::to_vec)
    }
    fn uuid(&mut self) -> CodecResult<Uuid> {
        Uuid::from_slice(self.take(16)?).map_err(|_| "invalid UUID")
    }
    pub(crate) fn end(self) -> CodecResult<()> {
        if self.0.is_empty() {
            Ok(())
        } else {
            Err("trailing protocol bytes")
        }
    }
}
fn validate_identifier(s: &str) -> CodecResult<()> {
    let (namespace, path) = s.split_once(':').unwrap_or(("minecraft", s));
    let valid = |b: u8| b.is_ascii_lowercase() || b.is_ascii_digit() || b"_.-".contains(&b);
    if namespace.is_empty()
        || path.is_empty()
        || !namespace.bytes().all(valid)
        || !path.bytes().all(|b| valid(b) || b == b'/')
    {
        return Err("invalid identifier");
    }
    Ok(())
}
pub(crate) fn int(out: &mut Vec<u8>, n: i32) {
    let mut n = n as u32;
    loop {
        let b = (n & 127) as u8;
        n >>= 7;
        out.push(b | if n == 0 { 0 } else { 128 });
        if n == 0 {
            break;
        }
    }
}
fn bytes(out: &mut Vec<u8>, b: &[u8]) -> CodecResult<()> {
    let n = i32::try_from(b.len()).map_err(|_| "field too large")?;
    int(out, n);
    out.extend_from_slice(b);
    Ok(())
}
pub(crate) fn string(out: &mut Vec<u8>, s: &str) -> CodecResult<()> {
    bytes(out, s.as_bytes())
}
fn optional<T>(
    out: &mut Vec<u8>,
    value: &Option<T>,
    f: impl FnOnce(&mut Vec<u8>, &T) -> CodecResult<()>,
) -> CodecResult<()> {
    out.push(u8::from(value.is_some()));
    if let Some(v) = value {
        f(out, v)?;
    }
    Ok(())
}

impl Serverbound {
    pub fn decode(id: i32, data: &[u8]) -> CodecResult<Self> {
        let mut r = Reader(data);
        let packet = match id {
            0 => Self::Start(LoginStart {
                name: r.string(16)?,
                player_uuid: r.uuid()?,
            }),
            1 => Self::EncryptionResponse {
                shared_secret: r.bytes(MAX_BYTES)?,
                verify_token: r.bytes(MAX_BYTES)?,
            },
            2 => Self::PluginResponse {
                message_id: r.int()?,
                data: if r.boolean()? {
                    Some(r.rest(MAX_BYTES)?)
                } else {
                    None
                },
            },
            3 => Self::Acknowledged,
            4 => Self::CookieResponse {
                key: r.identifier()?,
                payload: if r.boolean()? {
                    Some(r.bytes(5120)?)
                } else {
                    None
                },
            },
            _ => return Err("unknown serverbound login packet"),
        };
        r.end()?;
        Ok(packet)
    }
    pub fn encode(&self) -> CodecResult<(i32, Vec<u8>)> {
        let mut out = Vec::new();
        let id = match self {
            Self::Start(s) => {
                string(&mut out, &s.name)?;
                out.extend_from_slice(s.player_uuid.as_bytes());
                0
            }
            Self::EncryptionResponse {
                shared_secret,
                verify_token,
            } => {
                bytes(&mut out, shared_secret)?;
                bytes(&mut out, verify_token)?;
                1
            }
            Self::PluginResponse { message_id, data } => {
                int(&mut out, *message_id);
                optional(&mut out, data, |o, d| {
                    o.extend_from_slice(d);
                    Ok(())
                })?;
                2
            }
            Self::Acknowledged => 3,
            Self::CookieResponse { key, payload } => {
                string(&mut out, key)?;
                optional(&mut out, payload, |o, p| bytes(o, p))?;
                4
            }
        };
        Self::decode(id, &out)?;
        Ok((id, out))
    }
}
impl Clientbound {
    pub fn decode(id: i32, data: &[u8]) -> CodecResult<Self> {
        let mut r = Reader(data);
        let packet = match id {
            0 => {
                let reason = r.string(32767)?;
                serde_json::from_str::<serde_json::Value>(&reason)
                    .map_err(|_| "invalid JSON component")?;
                Self::Disconnect { reason }
            }
            1 => Self::EncryptionRequest {
                server_id: r.string(20)?,
                public_key: r.bytes(MAX_BYTES)?,
                verify_token: r.bytes(MAX_BYTES)?,
                should_authenticate: r.boolean()?,
            },
            2 => {
                let uuid = r.uuid()?;
                let username = r.string(16)?;
                let n = r.length(16)?;
                let mut properties = Vec::with_capacity(n);
                for _ in 0..n {
                    properties.push(Property {
                        name: r.string(64)?,
                        value: r.string(32767)?,
                        signature: if r.boolean()? {
                            Some(r.string(1024)?)
                        } else {
                            None
                        },
                    });
                }
                Self::Success(LoginSuccess {
                    profile: GameProfile {
                        uuid,
                        username,
                        properties,
                    },
                    session_id: r.uuid()?,
                })
            }
            3 => Self::SetCompression {
                threshold: r.int()?,
            },
            4 => Self::PluginRequest {
                message_id: r.int()?,
                channel: r.identifier()?,
                data: r.rest(1048576)?,
            },
            5 => Self::CookieRequest {
                key: r.identifier()?,
            },
            _ => return Err("unknown clientbound login packet"),
        };
        r.end()?;
        Ok(packet)
    }
    pub fn encode(&self) -> CodecResult<(i32, Vec<u8>)> {
        let mut out = Vec::new();
        let id = match self {
            Self::Disconnect { reason } => {
                string(&mut out, reason)?;
                0
            }
            Self::EncryptionRequest {
                server_id,
                public_key,
                verify_token,
                should_authenticate,
            } => {
                string(&mut out, server_id)?;
                bytes(&mut out, public_key)?;
                bytes(&mut out, verify_token)?;
                out.push(u8::from(*should_authenticate));
                1
            }
            Self::Success(s) => {
                out.extend_from_slice(s.profile.uuid.as_bytes());
                string(&mut out, &s.profile.username)?;
                int(
                    &mut out,
                    i32::try_from(s.profile.properties.len()).map_err(|_| "too many properties")?,
                );
                for p in &s.profile.properties {
                    string(&mut out, &p.name)?;
                    string(&mut out, &p.value)?;
                    optional(&mut out, &p.signature, |o, s| string(o, s))?;
                }
                out.extend_from_slice(s.session_id.as_bytes());
                2
            }
            Self::SetCompression { threshold } => {
                int(&mut out, *threshold);
                3
            }
            Self::PluginRequest {
                message_id,
                channel,
                data,
            } => {
                int(&mut out, *message_id);
                string(&mut out, channel)?;
                out.extend_from_slice(data);
                4
            }
            Self::CookieRequest { key } => {
                string(&mut out, key)?;
                5
            }
        };
        Self::decode(id, &out)?;
        Ok((id, out))
    }
    pub fn write(&self, writer: &mut impl Write) -> io::Result<()> {
        let (id, data) = self
            .encode()
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidInput, e))?;
        let mut body = Vec::new();
        int(&mut body, id);
        body.extend(data);
        if body.len() > MAX_BYTES {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "frame too large",
            ));
        }
        let mut prefix = Vec::new();
        int(&mut prefix, body.len() as i32);
        writer.write_all(&prefix)?;
        writer.write_all(&body)
    }
}

pub fn validate_username(name: &str) -> CodecResult<()> {
    if name.is_empty()
        || name.len() > 16
        || !name.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'_')
    {
        return Err("username must be 1..16 ASCII letters, digits or underscores");
    }
    Ok(())
}
/// Java UUID.nameUUIDFromBytes, with no namespace prepended. Names are case-sensitive.
pub fn offline_uuid(name: &str) -> CodecResult<Uuid> {
    validate_username(name)?;
    let mut bytes = md5::compute(format!("OfflinePlayer:{name}").as_bytes()).0;
    bytes[6] = (bytes[6] & 0x0f) | 0x30;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    Ok(Uuid::from_bytes(bytes))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn java_uuid_and_names() {
        assert_eq!(
            offline_uuid("Notch").unwrap().simple().to_string(),
            "b50ad385829d3141a2167e7d7539ba7f"
        );
        for s in ["", "a b", "é", "abcdefghijklmnopq", "a\0"] {
            assert!(offline_uuid(s).is_err());
        }
        assert_ne!(offline_uuid("Notch"), offline_uuid("notch"));
    }
    #[test]
    fn roundtrips_all_packets() {
        let server = [
            Serverbound::Start(LoginStart {
                name: "Notch".into(),
                player_uuid: Uuid::nil(),
            }),
            Serverbound::EncryptionResponse {
                shared_secret: vec![1, 2],
                verify_token: vec![3],
            },
            Serverbound::PluginResponse {
                message_id: -1,
                data: Some(vec![0, 255]),
            },
            Serverbound::PluginResponse {
                message_id: 0,
                data: None,
            },
            Serverbound::Acknowledged,
            Serverbound::CookieResponse {
                key: "minecraft:key".into(),
                payload: Some(vec![1; 5120]),
            },
            Serverbound::CookieResponse {
                key: "key".into(),
                payload: None,
            },
        ];
        for p in server {
            let (id, d) = p.encode().unwrap();
            assert_eq!(Serverbound::decode(id, &d).unwrap(), p);
            if !matches!(p, Serverbound::PluginResponse { data: Some(_), .. }) {
                let mut trailing = d.clone();
                trailing.push(0);
                assert!(Serverbound::decode(id, &trailing).is_err());
            }
            for n in 0..d.len() {
                if !matches!(p, Serverbound::PluginResponse { data: Some(_), .. }) {
                    assert!(Serverbound::decode(id, &d[..n]).is_err());
                }
            }
        }
        let client = [
            Clientbound::Disconnect {
                reason: "{\"text\":\"bye\"}".into(),
            },
            Clientbound::EncryptionRequest {
                server_id: "".into(),
                public_key: vec![1],
                verify_token: vec![2],
                should_authenticate: false,
            },
            Clientbound::Success(LoginSuccess {
                profile: GameProfile {
                    uuid: Uuid::nil(),
                    username: "Notch".into(),
                    properties: vec![Property {
                        name: "textures".into(),
                        value: "abc".into(),
                        signature: Some("sig".into()),
                    }],
                },
                session_id: Uuid::from_bytes([42; 16]),
            }),
            Clientbound::SetCompression { threshold: -1 },
            Clientbound::PluginRequest {
                message_id: 42,
                channel: "minecraft:test".into(),
                data: vec![255],
            },
            Clientbound::CookieRequest { key: "test".into() },
        ];
        for p in client {
            let (id, d) = p.encode().unwrap();
            assert_eq!(Clientbound::decode(id, &d).unwrap(), p);
            if !matches!(p, Clientbound::PluginRequest { .. }) {
                for n in 0..d.len() {
                    assert!(Clientbound::decode(id, &d[..n]).is_err());
                }
                let mut trailing = d.clone();
                trailing.push(0);
                assert!(Clientbound::decode(id, &trailing).is_err());
            }
            if let Clientbound::Success(_) = p {
                assert_eq!(&d[d.len() - 16..], &[42; 16]);
                assert!(Clientbound::decode(id, &d[..d.len() - 16]).is_err());
            }
        }
    }
    #[test]
    fn success_exact_wire_and_strict_booleans() {
        let p = Clientbound::Success(LoginSuccess {
            profile: GameProfile {
                uuid: Uuid::from_bytes([1; 16]),
                username: "A".into(),
                properties: vec![],
            },
            session_id: Uuid::from_bytes([2; 16]),
        });
        let (id, data) = p.encode().unwrap();
        assert_eq!(id, 2);
        assert_eq!(data, [vec![1; 16], vec![1, b'A', 0], vec![2; 16]].concat());
        assert!(Clientbound::decode(1, &[0, 0, 0, 2]).is_err());
        assert!(Serverbound::decode(4, &[1, b'x', 2]).is_err());
        let bad_signature = [
            vec![0; 16],
            vec![1, b'A', 1, 1, b'n', 1, b'v', 2],
            vec![0; 16],
        ]
        .concat();
        assert!(Clientbound::decode(2, &bad_signature).is_err());

        // UTF-16 bounds count a supplementary Unicode character twice.
        assert!(Serverbound::Start(LoginStart {
            name: "😀".repeat(8),
            player_uuid: Uuid::nil()
        })
        .encode()
        .is_ok());
        assert!(Serverbound::Start(LoginStart {
            name: "😀".repeat(9),
            player_uuid: Uuid::nil()
        })
        .encode()
        .is_err());
        assert!(Clientbound::decode(5, &[255, 255, 255, 255, 16]).is_err());
        assert!(Clientbound::decode(99, &[]).is_err());
        assert!(Serverbound::decode(99, &[]).is_err());
    }

    #[test]
    fn malformed_and_bounds() {
        for d in [vec![0], vec![2], vec![0, 0]] {
            assert!(Serverbound::decode(3, &d).is_err());
        }
        assert!(Serverbound::decode(2, &[0, 2]).is_err());
        assert!(Serverbound::decode(2, &[0, 0, 1]).is_err());
        assert!(Serverbound::decode(1, &[255, 255, 255, 255, 15]).is_err());
        for key in ["", ":key", "x:", "X:key", "x:a:b", "x:a b"] {
            assert!(Clientbound::CookieRequest { key: key.into() }
                .encode()
                .is_err());
        }
        assert!(Serverbound::CookieResponse {
            key: "x".into(),
            payload: Some(vec![0; 5121])
        }
        .encode()
        .is_err());
        assert!(Clientbound::PluginRequest {
            message_id: 0,
            channel: "x".into(),
            data: vec![0; 1048577]
        }
        .encode()
        .is_err());
        assert!(Serverbound::Start(LoginStart {
            name: "a".repeat(17),
            player_uuid: Uuid::nil()
        })
        .encode()
        .is_err());
        assert!(Clientbound::EncryptionRequest {
            server_id: "a".repeat(21),
            public_key: vec![],
            verify_token: vec![],
            should_authenticate: true
        }
        .encode()
        .is_err());
        let mut s = LoginSuccess {
            profile: GameProfile {
                uuid: Uuid::nil(),
                username: "a".into(),
                properties: vec![
                    Property {
                        name: "n".repeat(64),
                        value: "v".repeat(32767),
                        signature: Some("s".repeat(1024))
                    };
                    16
                ],
            },
            session_id: Uuid::nil(),
        };
        assert!(Clientbound::Success(s.clone()).encode().is_ok());
        s.profile.properties.push(s.profile.properties[0].clone());
        assert!(Clientbound::Success(s.clone()).encode().is_err());
        s.profile.properties.truncate(1);
        s.profile.properties[0].name.push('n');
        assert!(Clientbound::Success(s.clone()).encode().is_err());
        s.profile.properties[0].name = "n".into();
        s.profile.properties[0].value.push('v');
        assert!(Clientbound::Success(s.clone()).encode().is_err());
        s.profile.properties[0].value = "v".into();
        s.profile.properties[0]
            .signature
            .as_mut()
            .unwrap()
            .push('s');
        assert!(Clientbound::Success(s).encode().is_err());
        assert!(Clientbound::Disconnect {
            reason: "not JSON".into()
        }
        .encode()
        .is_err());
        assert!(Serverbound::decode(0, &[1, 255]).is_err());
    }
}
