//! Java 26.3 (777) Configuration fields, independent of transport/backend.
use crate::login::{int, string, CodecResult, Reader};
use std::io::{self, Write};

const MAX_FRAME: usize = 0x1f_ffff;
const MAX_COLLECTION: usize = 65536;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ClientInformation {
    pub locale: String,
    pub view_distance: i8,
    pub chat_mode: i32,
    pub chat_colors: bool,
    pub skin_parts: u8,
    pub main_hand: i32,
    pub text_filtering: bool,
    pub server_listings: bool,
    pub particle_status: i32,
}
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct KnownPack {
    pub namespace: String,
    pub name: String,
    pub version: String,
}
/// Validated, anonymous network NBT (root type followed by payload, no root name).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct NetworkNbt(Vec<u8>);
impl NetworkNbt {
    pub fn from_bytes(bytes: Vec<u8>) -> CodecResult<Self> {
        if bytes.len() > MAX_FRAME {
            return Err("NBT exceeds byte bound");
        }
        let mut r = Reader(&bytes);
        read_nbt(&mut r)?;
        r.end()?;
        Ok(Self(bytes))
    }
    pub fn as_bytes(&self) -> &[u8] {
        &self.0
    }
    /// Java modified UTF-8, including NUL and surrogate pairs.
    pub fn text(text: &str) -> CodecResult<Self> {
        let mut bytes = Vec::new();
        for c in text.encode_utf16() {
            match c {
                1..=127 => bytes.push(c as u8),
                0..=2047 => {
                    bytes.push((0xc0 | (c >> 6)) as u8);
                    bytes.push((0x80 | (c & 63)) as u8);
                }
                _ => {
                    bytes.push((0xe0 | (c >> 12)) as u8);
                    bytes.push((0x80 | ((c >> 6) & 63)) as u8);
                    bytes.push((0x80 | (c & 63)) as u8);
                }
            }
        }
        let n = u16::try_from(bytes.len()).map_err(|_| "NBT text too long")?;
        let mut out = vec![8];
        out.extend_from_slice(&n.to_be_bytes());
        out.extend(bytes);
        Ok(Self(out))
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RegistryEntry {
    pub id: String,
    pub data: Option<NetworkNbt>,
}
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Tag {
    pub id: String,
    pub entries: Vec<i32>,
}
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RegistryTags {
    pub registry: String,
    pub tags: Vec<Tag>,
}
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Serverbound {
    ClientInformation(ClientInformation),
    PluginMessage { channel: String, data: Vec<u8> },
    FinishAcknowledged,
    KeepAlive(i64),
    KnownPacks(Vec<KnownPack>),
}
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Clientbound {
    PluginMessage {
        channel: String,
        data: Vec<u8>,
    },
    Disconnect(NetworkNbt),
    Finish,
    KeepAlive(i64),
    RegistryData {
        registry: String,
        entries: Vec<RegistryEntry>,
    },
    FeatureFlags(Vec<String>),
    Tags(Vec<RegistryTags>),
    KnownPacks(Vec<KnownPack>),
}
fn range(r: &mut Reader<'_>, max: i32) -> CodecResult<i32> {
    let value = r.int()?;
    if !(0..=max).contains(&value) {
        return Err("invalid enum value");
    }
    Ok(value)
}
fn packs(r: &mut Reader<'_>) -> CodecResult<Vec<KnownPack>> {
    let count = r.length(64)?;
    let mut result = Vec::new();
    for _ in 0..count {
        result.push(KnownPack {
            namespace: r.string(32767)?,
            name: r.string(32767)?,
            version: r.string(32767)?,
        });
    }
    Ok(result)
}
fn put_count(out: &mut Vec<u8>, n: usize) -> CodecResult<()> {
    int(out, i32::try_from(n).map_err(|_| "collection too large")?);
    Ok(())
}
fn put_packs(out: &mut Vec<u8>, packs: &[KnownPack]) -> CodecResult<()> {
    put_count(out, packs.len())?;
    for p in packs {
        string(out, &p.namespace)?;
        string(out, &p.name)?;
        string(out, &p.version)?;
    }
    Ok(())
}
impl Serverbound {
    pub fn decode(id: i32, data: &[u8]) -> CodecResult<Self> {
        if data.len() > MAX_FRAME {
            return Err("packet too large");
        }
        let mut r = Reader(data);
        let packet = match id {
            0 => Self::ClientInformation(ClientInformation {
                locale: r.string(16)?,
                view_distance: r.take(1)?[0] as i8,
                chat_mode: range(&mut r, 2)?,
                chat_colors: r.boolean()?,
                skin_parts: r.take(1)?[0],
                main_hand: range(&mut r, 1)?,
                text_filtering: r.boolean()?,
                server_listings: r.boolean()?,
                particle_status: range(&mut r, 2)?,
            }),
            2 => {
                let channel = r.identifier()?;
                let data = if channel == "minecraft:brand" || channel == "brand" {
                    let data = r.rest(32767 * 3 + 3)?;
                    decode_brand(&data)?;
                    data
                } else {
                    r.rest(32767)?
                };
                Self::PluginMessage { channel, data }
            }
            3 => Self::FinishAcknowledged,
            4 => Self::KeepAlive(i64::from_be_bytes(r.take(8)?.try_into().unwrap())),
            7 => Self::KnownPacks(packs(&mut r)?),
            _ => return Err("unknown serverbound Configuration packet"),
        };
        r.end()?;
        Ok(packet)
    }
    pub fn encode(&self) -> CodecResult<(i32, Vec<u8>)> {
        let mut out = Vec::new();
        let id = match self {
            Self::ClientInformation(c) => {
                string(&mut out, &c.locale)?;
                out.push(c.view_distance as u8);
                int(&mut out, c.chat_mode);
                out.push(c.chat_colors.into());
                out.push(c.skin_parts);
                int(&mut out, c.main_hand);
                out.push(c.text_filtering.into());
                out.push(c.server_listings.into());
                int(&mut out, c.particle_status);
                0
            }
            Self::PluginMessage { channel, data } => {
                string(&mut out, channel)?;
                out.extend(data);
                2
            }
            Self::FinishAcknowledged => 3,
            Self::KeepAlive(value) => {
                out.extend(value.to_be_bytes());
                4
            }
            Self::KnownPacks(packs) => {
                put_packs(&mut out, packs)?;
                7
            }
        };
        Self::decode(id, &out)?;
        Ok((id, out))
    }
}
impl Clientbound {
    pub fn decode(id: i32, data: &[u8]) -> CodecResult<Self> {
        if data.len() > MAX_FRAME {
            return Err("packet too large");
        }
        let mut r = Reader(data);
        let packet = match id {
            1 => Self::PluginMessage {
                channel: r.identifier()?,
                data: r.rest(MAX_FRAME)?,
            },
            2 => Self::Disconnect(read_nbt(&mut r)?),
            3 => Self::Finish,
            4 => Self::KeepAlive(i64::from_be_bytes(r.take(8)?.try_into().unwrap())),
            7 => {
                let registry = r.identifier()?;
                let count = r.length(MAX_COLLECTION)?;
                let mut entries = Vec::new();
                for _ in 0..count {
                    entries.push(RegistryEntry {
                        id: r.identifier()?,
                        data: if r.boolean()? {
                            Some(read_nbt(&mut r)?)
                        } else {
                            None
                        },
                    });
                }
                Self::RegistryData { registry, entries }
            }
            13 => {
                let count = r.length(64)?;
                let mut flags = Vec::new();
                for _ in 0..count {
                    flags.push(r.identifier()?);
                }
                Self::FeatureFlags(flags)
            }
            14 => {
                let count = r.length(1024)?;
                let mut registries = Vec::new();
                for _ in 0..count {
                    let registry = r.identifier()?;
                    let count = r.length(MAX_COLLECTION)?;
                    let mut tags = Vec::new();
                    for _ in 0..count {
                        let id = r.identifier()?;
                        let count = r.length(MAX_COLLECTION)?;
                        let mut entries = Vec::new();
                        for _ in 0..count {
                            let n = r.int()?;
                            if n < 0 {
                                return Err("negative tag entry ID");
                            }
                            entries.push(n);
                        }
                        tags.push(Tag { id, entries });
                    }
                    registries.push(RegistryTags { registry, tags });
                }
                Self::Tags(registries)
            }
            15 => Self::KnownPacks(packs(&mut r)?),
            _ => return Err("unknown clientbound Configuration packet"),
        };
        r.end()?;
        Ok(packet)
    }
    pub fn encode(&self) -> CodecResult<(i32, Vec<u8>)> {
        let mut out = Vec::new();
        let id = match self {
            Self::PluginMessage { channel, data } => {
                string(&mut out, channel)?;
                out.extend(data);
                1
            }
            Self::Disconnect(nbt) => {
                out.extend(nbt.as_bytes());
                2
            }
            Self::Finish => 3,
            Self::KeepAlive(value) => {
                out.extend(value.to_be_bytes());
                4
            }
            Self::RegistryData { registry, entries } => {
                string(&mut out, registry)?;
                put_count(&mut out, entries.len())?;
                for e in entries {
                    string(&mut out, &e.id)?;
                    out.push(e.data.is_some().into());
                    if let Some(nbt) = &e.data {
                        out.extend(nbt.as_bytes());
                    }
                }
                7
            }
            Self::FeatureFlags(flags) => {
                put_count(&mut out, flags.len())?;
                for f in flags {
                    string(&mut out, f)?;
                }
                13
            }
            Self::Tags(registries) => {
                put_count(&mut out, registries.len())?;
                for r in registries {
                    string(&mut out, &r.registry)?;
                    put_count(&mut out, r.tags.len())?;
                    for t in &r.tags {
                        string(&mut out, &t.id)?;
                        put_count(&mut out, t.entries.len())?;
                        for e in &t.entries {
                            int(&mut out, *e);
                        }
                    }
                }
                14
            }
            Self::KnownPacks(packs) => {
                put_packs(&mut out, packs)?;
                15
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
        if body.len() > MAX_FRAME {
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
/// Decode a brand's nested String, rejecting trailing plugin payload bytes.
pub fn decode_brand(data: &[u8]) -> CodecResult<String> {
    let mut r = Reader(data);
    let brand = r.string(32767)?;
    r.end()?;
    Ok(brand)
}
pub fn brand() -> Clientbound {
    let mut data = Vec::new();
    string(&mut data, "Rusty Mines").expect("static brand");
    Clientbound::PluginMessage {
        channel: "minecraft:brand".into(),
        data,
    }
}
fn nbt_string(r: &mut Reader<'_>) -> CodecResult<()> {
    let n = u16::from_be_bytes(r.take(2)?.try_into().unwrap()) as usize;
    let bytes = r.take(n)?;
    // Validate Java modified UTF-8 code units without requiring paired surrogates.
    let mut i = 0;
    while i < bytes.len() {
        let b = bytes[i];
        let n = match b {
            1..=127 => 1,
            0xc0..=0xdf => 2,
            0xe0..=0xef => 3,
            _ => return Err("invalid modified UTF-8"),
        };
        if i + n > bytes.len() || bytes[i + 1..i + n].iter().any(|b| b & 0xc0 != 0x80) {
            return Err("invalid modified UTF-8");
        }
        if n == 2 {
            let v = (u16::from(b & 31) << 6) | u16::from(bytes[i + 1] & 63);
            if v < 128 && v != 0 {
                return Err("overlong modified UTF-8");
            }
        }
        if n == 3 && b == 0xe0 && bytes[i + 1] < 0xa0 {
            return Err("overlong modified UTF-8");
        }
        i += n;
    }
    Ok(())
}
fn nbt_payload(r: &mut Reader<'_>, tag: u8, depth: usize, budget: &mut usize) -> CodecResult<()> {
    if depth > 64 || *budget == 0 {
        return Err("NBT complexity limit");
    }
    *budget -= 1;
    match tag {
        1..=6 => {
            r.take([0, 1, 2, 4, 8, 4, 8][tag as usize])?;
        }
        7 | 11 | 12 => {
            let n = i32::from_be_bytes(r.take(4)?.try_into().unwrap());
            let width = match tag {
                7 => 1,
                11 => 4,
                _ => 8,
            };
            if n < 0 {
                return Err("negative NBT array length");
            }
            let bytes = (n as usize)
                .checked_mul(width)
                .filter(|n| *n <= MAX_FRAME)
                .ok_or("NBT array too large")?;
            r.take(bytes)?;
        }
        8 => nbt_string(r)?,
        9 => {
            let kind = r.take(1)?[0];
            let n = i32::from_be_bytes(r.take(4)?.try_into().unwrap());
            if n < 0 || n as usize > *budget || kind > 12 || (kind == 0 && n != 0) {
                return Err("invalid NBT list");
            }
            for _ in 0..n {
                nbt_payload(r, kind, depth + 1, budget)?;
            }
        }
        10 => loop {
            let kind = r.take(1)?[0];
            if kind == 0 {
                break;
            }
            nbt_string(r)?;
            nbt_payload(r, kind, depth + 1, budget)?;
        },
        _ => return Err("invalid NBT tag type"),
    }
    Ok(())
}
fn read_nbt(r: &mut Reader<'_>) -> CodecResult<NetworkNbt> {
    let start = r.0;
    let tag = r.take(1)?[0];
    let mut budget = MAX_COLLECTION;
    nbt_payload(r, tag, 0, &mut budget)?;
    Ok(NetworkNbt(start[..start.len() - r.0.len()].to_vec()))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn roundtrip_clientbound() {
        for packet in [
            brand(),
            Clientbound::Disconnect(NetworkNbt::text("bye\0😀").unwrap()),
            Clientbound::Finish,
            Clientbound::KeepAlive(i64::MIN),
            Clientbound::RegistryData {
                registry: "minecraft:test".into(),
                entries: vec![
                    RegistryEntry {
                        id: "minecraft:a".into(),
                        data: None,
                    },
                    RegistryEntry {
                        id: "minecraft:b".into(),
                        data: Some(NetworkNbt::from_bytes(vec![10, 0]).unwrap()),
                    },
                ],
            },
            Clientbound::FeatureFlags(vec!["minecraft:vanilla".into()]),
            Clientbound::Tags(vec![RegistryTags {
                registry: "minecraft:test".into(),
                tags: vec![Tag {
                    id: "minecraft:a".into(),
                    entries: vec![0, 128],
                }],
            }]),
            Clientbound::KnownPacks(vec![KnownPack {
                namespace: "minecraft".into(),
                name: "core".into(),
                version: "26.3".into(),
            }]),
        ] {
            let (id, bytes) = packet.encode().unwrap();
            assert_eq!(Clientbound::decode(id, &bytes).unwrap(), packet);
            if id != 1 {
                let mut trailing = bytes.clone();
                trailing.push(0);
                assert!(Clientbound::decode(id, &trailing).is_err());
                for n in 0..bytes.len() {
                    assert!(Clientbound::decode(id, &bytes[..n]).is_err());
                }
            }
        }
    }
    #[test]
    fn client_information_bounds() {
        let packet = Serverbound::ClientInformation(ClientInformation {
            locale: "en_us".into(),
            view_distance: -1,
            chat_mode: 2,
            chat_colors: true,
            skin_parts: 255,
            main_hand: 1,
            text_filtering: false,
            server_listings: true,
            particle_status: 2,
        });
        let (id, bytes) = packet.encode().unwrap();
        assert_eq!(Serverbound::decode(id, &bytes).unwrap(), packet);
        for (index, value) in [(7, 3), (8, 2), (10, 2), (11, 2), (12, 2), (13, 3)] {
            let mut bad = bytes.clone();
            bad[index] = value;
            assert!(Serverbound::decode(0, &bad).is_err());
        }
        for n in 0..bytes.len() {
            assert!(Serverbound::decode(id, &bytes[..n]).is_err());
        }
        let mut bad = bytes;
        bad.push(0);
        assert!(Serverbound::decode(id, &bad).is_err());
    }
    #[test]
    fn serverbound_roundtrips_and_utf16_bounds() {
        for packet in [
            Serverbound::FinishAcknowledged,
            Serverbound::KeepAlive(i64::MAX),
            Serverbound::KnownPacks(vec![KnownPack {
                namespace: "Not an identifier".into(),
                name: "Core Pack".into(),
                version: "26.3".into(),
            }]),
            Serverbound::PluginMessage {
                channel: "minecraft:unknown".into(),
                data: vec![255; 32767],
            },
        ] {
            let (id, bytes) = packet.encode().unwrap();
            assert_eq!(Serverbound::decode(id, &bytes).unwrap(), packet);
            if id != 2 {
                let mut trailing = bytes.clone();
                trailing.push(0);
                assert!(Serverbound::decode(id, &trailing).is_err());
            }
        }
        let mut info = ClientInformation {
            locale: "😀".repeat(8),
            view_distance: i8::MIN,
            chat_mode: 0,
            chat_colors: true,
            skin_parts: 0,
            main_hand: 0,
            text_filtering: false,
            server_listings: true,
            particle_status: 0,
        };
        assert!(Serverbound::ClientInformation(info.clone())
            .encode()
            .is_ok());
        info.locale.push('a');
        assert!(Serverbound::ClientInformation(info).encode().is_err());
        let mut data = Vec::new();
        string(&mut data, &"界".repeat(32767)).unwrap();
        assert!(Serverbound::PluginMessage {
            channel: "minecraft:brand".into(),
            data
        }
        .encode()
        .is_ok());
        assert!(Serverbound::PluginMessage {
            channel: "minecraft:brand".into(),
            data: vec![1, b'a', 0]
        }
        .encode()
        .is_err());
        assert!(Clientbound::decode(7, &[1, b'r', 1, 1, b'e', 2]).is_err());
        assert!(Clientbound::Tags(vec![RegistryTags {
            registry: "r".into(),
            tags: vec![Tag {
                id: "t".into(),
                entries: vec![-1]
            }]
        }])
        .encode()
        .is_err());
        assert!(Serverbound::decode(7, &[128, 128, 128, 128, 16]).is_err());
    }

    #[test]
    fn anonymous_nbt_all_payload_types_and_no_root_name() {
        for (tag, width) in [
            (1, 1),
            (2, 2),
            (3, 4),
            (4, 8),
            (5, 4),
            (6, 8),
            (7, 4),
            (11, 4),
            (12, 4),
        ] {
            let mut bytes = vec![tag];
            bytes.extend(vec![0; width]);
            assert!(NetworkNbt::from_bytes(bytes).is_ok());
        }
        assert!(NetworkNbt::from_bytes(vec![9, 3, 0, 0, 0, 1, 0, 0, 0, 42]).is_ok());
        assert!(NetworkNbt::from_bytes(vec![10, 8, 0, 1, b'x', 0, 1, b'y', 0]).is_ok());
        assert!(NetworkNbt::from_bytes(vec![10, 0, 0, 0]).is_err());
        assert!(NetworkNbt::text(&"界".repeat(21846)).is_err());
    }

    #[test]
    fn strict_collections_plugins_and_nbt() {
        assert!(Serverbound::decode(7, &[255, 255, 255, 255, 15]).is_err());
        assert!(Serverbound::decode(7, &[65]).is_err());
        assert!(Serverbound::decode(3, &[0]).is_err());
        assert!(Serverbound::decode(4, &[0; 9]).is_err());
        assert!(Serverbound::PluginMessage {
            channel: "minecraft:unknown".into(),
            data: vec![0; 32768]
        }
        .encode()
        .is_err());
        assert!(Serverbound::PluginMessage {
            channel: "Invalid".into(),
            data: vec![]
        }
        .encode()
        .is_err());
        assert!(decode_brand(&[1, b'a', 0]).is_err());
        for bytes in [
            vec![0],
            vec![10, 13, 0, 0],
            vec![9, 0, 0, 0, 0, 1],
            vec![7, 255, 255, 255, 255],
            vec![8, 0, 1, 0],
            vec![8, 0, 2, 0xc1, 0x81],
        ] {
            assert!(NetworkNbt::from_bytes(bytes).is_err());
        }
        let mut nested = vec![];
        for _ in 0..66 {
            nested.extend([10, 0, 0]);
        }
        nested.extend([0; 66]);
        assert!(NetworkNbt::from_bytes(nested).is_err());
    }
}
