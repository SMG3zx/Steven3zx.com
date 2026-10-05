//! Protocol 777 Play fields and official packet layouts, without session policy.
use crate::{
    configuration,
    login::{int, string as put_wire_string, CodecResult, Reader},
};
use rusty_mines_inventory::{
    model::{self, HashedStack},
    wire::{self, Stack},
};
use std::io::{self, Write};

#[derive(Debug, PartialEq)]
pub struct InventoryClick {
    pub menu: i32,
    pub revision: i32,
    pub slot: i16,
    pub button: i8,
    pub mode: i32,
    pub changed: Vec<(i16, Option<HashedStack>)>,
    pub cursor: Option<HashedStack>,
}

pub const LOGIN: u8 = 0x32;
pub const LOADING: u8 = 0x27;
pub const SPAWN: u8 = 0x63;
pub const POSITION: u8 = 0x49;
pub const ABILITIES: u8 = 0x41;
pub const CENTER: u8 = 0x60;
pub const BATCH_START: u8 = 0x0c;
pub const BATCH_END: u8 = 0x0b;
pub const CHUNK: u8 = 0x2e;
pub const UNLOAD_CHUNK: u8 = 0x26;
pub const RENDER_DISTANCE: u8 = 0x61;
pub const SIMULATION_DISTANCE: u8 = 0x71;
pub const SPAWN_ENTITY: u8 = 0x01;
pub const MOVE_ENTITY: u8 = 0x37;
pub const REMOVE_ENTITY: u8 = 0x4e;
pub const PLAYER_INFO_REMOVE: u8 = 0x46;
pub const PLAYER_INFO_UPDATE: u8 = 0x47;

#[derive(Clone, Debug, PartialEq)]
pub struct PlayerEntity {
    pub entity_id: i32,
    pub player_uuid: uuid::Uuid,
    pub username: String,
    pub position: [f64; 3],
    pub rotation: [f32; 2],
    pub on_ground: bool,
}

pub fn player_spawn_packets(player: &PlayerEntity) -> CodecResult<(Vec<u8>, Vec<u8>)> {
    if player.entity_id <= 0
        || player.username.is_empty()
        || player.username.len() > 16
        || !player
            .username
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'_')
        || !player.position.iter().all(|v| v.is_finite())
        || !player.rotation.iter().all(|v| v.is_finite())
    {
        return Err("invalid player entity state");
    }
    let mut info = vec![PLAYER_INFO_UPDATE, 0x00, 0x01];
    info.extend(player.player_uuid.as_u128().to_be_bytes());
    put_wire_string(&mut info, &player.username)?;
    int(&mut info, 0); // no profile properties
    info.extend([0x02, 0x01, 0x03, 0x01, 0x04, 0x00, 0x07, 0x01, 0x06, 0x00]);
    let mut spawn = vec![SPAWN_ENTITY];
    int(&mut spawn, player.entity_id);
    spawn.extend(player.player_uuid.as_u128().to_be_bytes());
    int(&mut spawn, 159); // official 26.3 registry ID for minecraft:player
    for coordinate in player.position {
        spawn.extend(coordinate.to_be_bytes());
    }
    // LpVec3 zero velocity: presence flags, then no components.
    spawn.push(0);
    spawn.push(angle(player.rotation[1]));
    spawn.push(angle(player.rotation[0]));
    spawn.push(angle(player.rotation[0]));
    int(&mut spawn, 0);
    Ok((info, spawn))
}

pub fn player_move_packet(
    player: &PlayerEntity,
    delta: [f64; 3],
    rotation: bool,
) -> CodecResult<Vec<u8>> {
    if player.entity_id <= 0
        || !delta.iter().all(|v| v.is_finite())
        || !player.rotation.iter().all(|v| v.is_finite())
    {
        return Err("invalid player movement");
    }
    let scaled: Option<Vec<i16>> = delta
        .iter()
        .map(|v| {
            let n = (v * 4096.0).round();
            (n >= f64::from(i16::MIN) && n <= f64::from(i16::MAX)).then_some(n as i16)
        })
        .collect();
    if delta == [0.0; 3] {
        let mut packet = vec![0x39];
        int(&mut packet, player.entity_id);
        packet.push(u8::from(player.on_ground));
        packet.push(angle(player.rotation[0]));
        packet.push(angle(player.rotation[1]));
        return Ok(packet);
    }
    let mut packet = vec![if rotation { 0x37 } else { 0x36 }];
    int(&mut packet, player.entity_id);
    packet.push(u8::from(player.on_ground)); // on-ground bit; linear VecDelta step count 0.
    let values = scaled.ok_or("entity delta requires absolute-position fallback")?;
    for value in values {
        packet.extend(value.to_be_bytes());
    }
    if rotation {
        packet.push(angle(player.rotation[0]));
        packet.push(angle(player.rotation[1]));
    }

    Ok(packet)
}

pub fn player_remove_packets(
    player_id: uuid::Uuid,
    entity_id: i32,
) -> CodecResult<(Vec<u8>, Vec<u8>)> {
    if entity_id <= 0 {
        return Err("invalid entity id");
    }
    let mut profile = vec![PLAYER_INFO_REMOVE];
    int(&mut profile, 1);
    profile.extend(player_id.as_u128().to_be_bytes());
    let mut entity = vec![REMOVE_ENTITY];
    int(&mut entity, 1);
    int(&mut entity, entity_id);
    Ok((entity, profile))
}

fn angle(degrees: f32) -> u8 {
    ((degrees.rem_euclid(360.0) * (256.0 / 360.0)).round() as i32 & 0xff) as u8
}

#[derive(Debug, PartialEq)]
pub struct TeleportAcknowledged {
    pub id: i32,
    // Preserve bit-exact acknowledgement matching, including signed zero.
    pub position: [u64; 3],
    pub rotation: [u32; 2],
}
#[derive(Debug, PartialEq)]
pub struct Movement {
    pub position: Option<[f64; 3]>,
    pub rotation: Option<[f32; 2]>,
    pub on_ground: bool,
    pub horizontal_collision: bool,
}
#[derive(Debug, PartialEq)]
pub enum Serverbound {
    TeleportAcknowledged(TeleportAcknowledged),
    PlayerLoaded,
    ChatSession,
    ClientTickEnd,
    Abilities {
        flying: bool,
    },
    ChunkBatchReceived,
    KeepAlive(i64),
    ClientInformation(configuration::ClientInformation),
    PluginMessage {
        channel: String,
        data: Vec<u8>,
    },
    Movement(Movement),
    Punch,
    ChatMessage(String),
    SignedChatMessage(String),
    ChatCommand(String),
    SignedChatCommand(String),
    ChatAcknowledgement,
    ClientStatus(i32),
    ChangeGameMode(i32),
    PlayerCommand {
        entity: i32,
        action: i32,
        jump_boost: i32,
    },
    PlayerInput(u8),
    HeldSlot(i16),
    PlayerAction {
        status: i32,
        position: BlockPosition,
        face: u8,
        sequence: i32,
    },
    UseItemOn {
        hand: i32,
        position: BlockPosition,
        face: i32,
        cursor: [f32; 3],
        inside: bool,
        border: bool,
        sequence: i32,
    },
    UseItem {
        hand: i32,
        sequence: i32,
        rotation: [f32; 2],
    },
    CreativeSlot {
        slot: i16,
        stack: Stack,
    },
    InventoryClick(InventoryClick),
    CloseContainer(i32),
    RecipeSettings,
    PickBlock,
    PickEntity,
    Attack(i32),
}
impl Serverbound {
    pub fn decode(id: i32, data: &[u8]) -> CodecResult<Self> {
        let mut r = Reader(data);
        let packet = match id {
            0x00 => {
                let id = nonnegative(&mut r)?;
                let mut position = [0; 3];
                for value in &mut position {
                    *value = u64::from_be_bytes(r.take(8)?.try_into().unwrap());
                }
                let mut rotation = [0; 2];
                for value in &mut rotation {
                    *value = u32::from_be_bytes(r.take(4)?.try_into().unwrap());
                }
                Self::TeleportAcknowledged(TeleportAcknowledged {
                    id,
                    position,
                    rotation,
                })
            }
            0x2c => Self::PlayerLoaded,
            0x0a => {
                r.take(24)?;
                for maximum in [512, 4096] {
                    let length =
                        usize::try_from(r.int()?).map_err(|_| "invalid chat session length")?;
                    if length > maximum {
                        return Err("invalid chat session length");
                    }
                    r.take(length)?;
                }
                Self::ChatSession
            }
            0x0d => Self::ClientTickEnd,
            0x28 => {
                let flags = r.take(1)?[0];
                if flags & !2 != 0 {
                    return Err("invalid abilities flags");
                }
                Self::Abilities {
                    flying: flags & 2 != 0,
                }
            }
            0x0b => {
                let rate = f32::from_be_bytes(r.take(4)?.try_into().unwrap());
                if !rate.is_finite() || rate <= 0.0 {
                    return Err("invalid chunk batch rate");
                }
                Self::ChunkBatchReceived
            }
            0x1c => Self::KeepAlive(i64::from_be_bytes(r.take(8)?.try_into().unwrap())),
            0x0e => {
                return match configuration::Serverbound::decode(0, data)? {
                    configuration::Serverbound::ClientInformation(info) => {
                        Ok(Self::ClientInformation(info))
                    }
                    _ => unreachable!(),
                }
            }
            0x16 => {
                return match configuration::Serverbound::decode(2, data)? {
                    configuration::Serverbound::PluginMessage { channel, data } => {
                        Ok(Self::PluginMessage { channel, data })
                    }
                    _ => unreachable!(),
                }
            }
            0x1e..=0x21 => {
                let position = if matches!(id, 0x1e | 0x1f) {
                    let mut values = [0.0; 3];
                    for value in &mut values {
                        *value = f64::from_be_bytes(r.take(8)?.try_into().unwrap());
                    }
                    if !values.iter().all(|v| v.is_finite()) {
                        return Err("nonfinite movement position");
                    }
                    Some(values)
                } else {
                    None
                };
                let rotation = if matches!(id, 0x1f | 0x20) {
                    let mut values = [0.0; 2];
                    for value in &mut values {
                        *value = f32::from_be_bytes(r.take(4)?.try_into().unwrap());
                    }
                    if !values.iter().all(|v| v.is_finite()) {
                        return Err("nonfinite movement rotation");
                    }
                    Some(values)
                } else {
                    None
                };
                let flags = r.take(1)?[0];
                if flags & !3 != 0 {
                    return Err("invalid movement flags");
                }
                Self::Movement(Movement {
                    position,
                    rotation,
                    on_ground: flags & 1 != 0,
                    horizontal_collision: flags & 2 != 0,
                })
            }
            0x06 => {
                nonnegative(&mut r)?;
                Self::ChatAcknowledgement
            }
            0x0c => Self::ClientStatus(ranged(&mut r, 2)?),
            0x07 => Self::ChatCommand(r.string(32767)?),
            0x08 => {
                let command = r.string(32767)?;
                r.take(16)?;
                let count = r.length(8)?;
                for _ in 0..count {
                    r.string(16)?;
                    r.take(256)?;
                }
                nonnegative(&mut r)?;
                r.take(4)?;
                Self::SignedChatCommand(command)
            }
            0x09 => {
                let message = r.string(256)?;
                r.take(16)?;
                let signed = r.boolean()?;
                if signed {
                    r.take(256)?;
                }
                nonnegative(&mut r)?;
                r.take(4)?;
                if signed {
                    Self::SignedChatMessage(message)
                } else {
                    Self::ChatMessage(message)
                }
            }
            0x05 => Self::ChangeGameMode(ranged(&mut r, 3)?),
            0x2e => Self::Punch,
            0x2a => Self::PlayerCommand {
                entity: nonnegative(&mut r)?,
                action: ranged(&mut r, 6)?,
                jump_boost: ranged(&mut r, 100)?,
            },
            0x2b => {
                let flags = r.take(1)?[0];
                if flags & !0x7f != 0 {
                    return Err("invalid player input flags");
                }
                Self::PlayerInput(flags)
            }
            0x36 => {
                let slot = i16::from_be_bytes(r.take(2)?.try_into().unwrap());
                if !(0..=8).contains(&slot) {
                    return Err("invalid held slot");
                }
                Self::HeldSlot(slot)
            }
            0x29 => Self::PlayerAction {
                status: ranged(&mut r, 7)?,
                position: BlockPosition::read(&mut r)?,
                face: {
                    let face = r.take(1)?[0];
                    if face > 5 {
                        return Err("invalid block face");
                    }
                    face
                },
                sequence: nonnegative(&mut r)?,
            },
            0x42 => {
                let hand = ranged(&mut r, 1)?;
                let position = BlockPosition::read(&mut r)?;
                let face = ranged(&mut r, 5)?;
                let cursor = [float(&mut r)?, float(&mut r)?, float(&mut r)?];
                if cursor.iter().any(|v| !(0.0..=1.0).contains(v)) {
                    return Err("invalid block cursor");
                }
                Self::UseItemOn {
                    hand,
                    position,
                    face,
                    cursor,
                    inside: r.boolean()?,
                    border: r.boolean()?,
                    sequence: nonnegative(&mut r)?,
                }
            }
            0x43 => Self::UseItem {
                hand: ranged(&mut r, 1)?,
                sequence: nonnegative(&mut r)?,
                rotation: [float(&mut r)?, float(&mut r)?],
            },
            0x39 => {
                if data.len() > 65536 {
                    return Err("creative slot payload too large");
                }
                let slot = i16::from_be_bytes(r.take(2)?.try_into().unwrap());
                if !(-1..=45).contains(&slot) || r.0.is_empty() {
                    return Err("invalid creative slot envelope");
                }
                let stack = wire::decode_stack(r.0, true)?;
                r.take(r.0.len())?;
                Self::CreativeSlot { slot, stack }
            }
            0x12 => {
                let menu = nonnegative(&mut r)?;
                let revision = nonnegative(&mut r)?;
                let slot = i16::from_be_bytes(r.take(2)?.try_into().unwrap());
                let button = r.take(1)?[0] as i8;
                let mode = ranged(&mut r, 6)?;
                let count = r.length(128)?;
                let mut changed = Vec::new();
                let mut ids = std::collections::BTreeSet::new();
                let mut inventory = wire::Reader::new(r.0)?;
                for _ in 0..count {
                    let index = i16::from_be_bytes(inventory.take(2)?.try_into().unwrap());
                    if !(0..63).contains(&index) || !ids.insert(index) {
                        return Err("invalid changed inventory slot");
                    }
                    changed.push((index, model::read_hashed(&mut inventory)?));
                }
                let cursor = model::read_hashed(&mut inventory)?;
                r.0 = inventory.bytes;
                Self::InventoryClick(InventoryClick {
                    menu,
                    revision,
                    slot,
                    button,
                    mode,
                    changed,
                    cursor,
                })
            }
            0x13 => Self::CloseContainer(nonnegative(&mut r)?),
            0x2f => {
                ranged(&mut r, 3)?;
                r.boolean()?;
                r.boolean()?;
                Self::RecipeSettings
            }
            0x24 => {
                BlockPosition::read(&mut r)?;
                r.boolean()?;
                Self::PickBlock
            }
            0x25 => {
                nonnegative(&mut r)?;
                r.boolean()?;
                Self::PickEntity
            }
            0x01 => Self::Attack(nonnegative(&mut r)?),
            _ => return Err("unsupported Play packet"),
        };
        r.end()?;
        Ok(packet)
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct BlockPosition {
    pub x: i32,
    pub y: i32,
    pub z: i32,
}
impl BlockPosition {
    fn read(r: &mut Reader<'_>) -> CodecResult<Self> {
        let n = i64::from_be_bytes(r.take(8)?.try_into().unwrap());
        Ok(Self {
            x: (n >> 38) as i32,
            y: (n << 52 >> 52) as i32,
            z: (n << 26 >> 38) as i32,
        })
    }
    pub fn packed(self) -> i64 {
        ((i64::from(self.x) & 0x3ffffff) << 38)
            | ((i64::from(self.z) & 0x3ffffff) << 12)
            | (i64::from(self.y) & 0xfff)
    }
    pub fn adjacent(self, face: i32) -> Self {
        let [dx, dy, dz] = [
            [0, -1, 0],
            [0, 1, 0],
            [0, 0, -1],
            [0, 0, 1],
            [-1, 0, 0],
            [1, 0, 0],
        ][face as usize];
        Self {
            x: self.x + dx,
            y: self.y + dy,
            z: self.z + dz,
        }
    }
}
fn nonnegative(r: &mut Reader<'_>) -> CodecResult<i32> {
    let n = r.int()?;
    if n < 0 {
        return Err("negative protocol field");
    }
    Ok(n)
}
fn ranged(r: &mut Reader<'_>, max: i32) -> CodecResult<i32> {
    let n = nonnegative(r)?;
    if n > max {
        return Err("invalid protocol enum/range");
    }
    Ok(n)
}
fn float(r: &mut Reader<'_>) -> CodecResult<f32> {
    let n = f32::from_be_bytes(r.take(4)?.try_into().unwrap());
    if !n.is_finite() {
        return Err("nonfinite protocol float");
    }
    Ok(n)
}

#[derive(Clone, Copy, Debug)]
pub struct SynchronizePlayerPosition {
    pub id: i32,
    pub position: [f64; 3],
    pub velocity: [f64; 3],
    pub rotation: [f32; 2],
    pub flags: i32,
}
impl SynchronizePlayerPosition {
    pub fn packet(&self) -> CodecResult<Vec<u8>> {
        if self.id <= 0
            || self.flags & !0x1ff != 0
            || !self
                .position
                .iter()
                .chain(self.velocity.iter())
                .all(|v| v.is_finite())
            || !self.rotation.iter().all(|v| v.is_finite())
        {
            return Err("invalid authoritative teleport");
        }
        let mut packet = vec![POSITION];
        int(&mut packet, self.id);
        for value in self.position.into_iter().chain(self.velocity) {
            packet.extend(value.to_be_bytes());
        }
        for value in self.rotation {
            packet.extend(value.to_be_bytes());
        }
        packet.extend(self.flags.to_be_bytes());
        Ok(packet)
    }
}

pub enum Clientbound {
    Disconnect(configuration::NetworkNbt),
    KeepAlive(i64),
    SystemMessage(configuration::NetworkNbt),
    BlockUpdate(BlockPosition, i32),
    BlockAcknowledgement(i32),
    Abilities {
        flags: u8,
        flying_speed: f32,
        walking_speed: f32,
    },
    Health {
        health: f32,
        food: i32,
        saturation: f32,
    },
    CombatDeath {
        entity_id: i32,
        message: configuration::NetworkNbt,
    },
    Respawn {
        adventure: bool,
    },
    GameEvent {
        event: u8,
        value: f32,
    },
}

pub fn write_inventory(
    writer: &mut impl Write,
    menu: i32,
    revision: i32,
    slots: &[u8],
    cursor: &[u8],
    selected: u8,
) -> io::Result<()> {
    if menu < 0 || revision < 0 || selected > 8 {
        return Err(io::Error::other("invalid authoritative inventory metadata"));
    }
    model::decode_slots(slots, if menu == 0 { 46 } else { 63 }).map_err(io::Error::other)?;
    wire::decode_stack(cursor, false).map_err(io::Error::other)?;
    let mut data = Vec::new();
    int(&mut data, menu);
    int(&mut data, revision);
    data.extend(slots);
    data.extend(cursor);
    write_frame(writer, 0x12, &data)?;
    let mut held = Vec::new();
    int(&mut held, i32::from(selected));
    write_frame(writer, 0x6b, &held)?;
    write_frame(writer, 0x62, cursor)
}
pub fn write_inventory_menu(
    writer: &mut impl Write,
    previous: i32,
    current: i32,
    title: &str,
) -> io::Result<()> {
    if previous == current {
        return Ok(());
    }
    if previous > 0 {
        let mut fields = Vec::new();
        int(&mut fields, previous);
        write_frame(writer, 0x11, &fields)?;
    }
    if current > 0 {
        let mut fields = Vec::new();
        int(&mut fields, current);
        int(&mut fields, 2);
        fields.extend(
            configuration::NetworkNbt::text(title)
                .map_err(io::Error::other)?
                .as_bytes(),
        );
        write_frame(writer, 0x3c, &fields)?;
    }
    Ok(())
}
impl Clientbound {
    pub fn write(&self, writer: &mut impl Write) -> io::Result<()> {
        match self {
            Self::Disconnect(nbt) => write_frame(writer, 0x20, nbt.as_bytes()),
            Self::KeepAlive(id) => write_frame(writer, 0x2d, &id.to_be_bytes()),
            Self::SystemMessage(nbt) => {
                let mut data = nbt.as_bytes().to_vec();
                data.push(0);
                write_frame(writer, 0x7c, &data)
            }
            Self::BlockUpdate(position, state) => {
                let mut data = position.packed().to_be_bytes().to_vec();
                int(&mut data, *state);
                write_frame(writer, 0x08, &data)
            }
            Self::BlockAcknowledgement(sequence) => {
                let mut data = Vec::new();
                int(&mut data, *sequence);
                write_frame(writer, 0x04, &data)
            }
            Self::Abilities {
                flags,
                flying_speed,
                walking_speed,
            } => {
                let mut data = vec![*flags];
                data.extend(flying_speed.to_be_bytes());
                data.extend(walking_speed.to_be_bytes());
                write_frame(writer, 0x41, &data)
            }
            Self::Health {
                health,
                food,
                saturation,
            } => {
                let mut data = health.to_be_bytes().to_vec();
                int(&mut data, *food);
                data.extend(saturation.to_be_bytes());
                write_frame(writer, 0x6a, &data)
            }
            Self::CombatDeath { entity_id, message } => {
                if *entity_id <= 0 {
                    return Err(io::Error::other("invalid death entity ID"));
                }
                let mut data = Vec::new();
                int(&mut data, *entity_id);
                data.extend_from_slice(message.as_bytes());
                write_frame(writer, 0x45, &data)
            }
            Self::Respawn { adventure } => {
                let packet = respawn_fixture(*adventure).map_err(io::Error::other)?;
                if packet.first() != Some(&0x54) {
                    return Err(io::Error::other("invalid official respawn fixture"));
                }
                write_frame(writer, 0x54, &packet[1..])
            }
            Self::GameEvent { event, value } => {
                let mut data = vec![*event];
                data.extend(value.to_be_bytes());
                write_frame(writer, 0x27, &data)
            }
        }
    }
}

fn respawn_fixture(adventure: bool) -> CodecResult<Vec<u8>> {
    let fixtures: serde_json::Value =
        serde_json::from_str(include_str!("../../assets/entity-26.3.json"))
            .map_err(|_| "invalid official Play fixtures")?;
    let name = if adventure {
        "respawn_adventure"
    } else {
        "respawn_survival"
    };
    let encoded = fixtures["packets"][name]["hex"]
        .as_str()
        .ok_or("missing official respawn fixture")?;
    if encoded.len() % 2 != 0 {
        return Err("invalid official respawn fixture length");
    }
    let mut bytes = Vec::with_capacity(encoded.len() / 2);
    for pair in encoded.as_bytes().as_chunks::<2>().0 {
        let pair = std::str::from_utf8(pair).map_err(|_| "invalid official respawn fixture")?;
        bytes.push(u8::from_str_radix(pair, 16).map_err(|_| "invalid official respawn fixture")?);
    }
    if bytes.len() < 2 || bytes[0] != 0x54 {
        return Err("invalid official respawn fixture packet");
    }
    Ok(bytes)
}

fn write_frame(writer: &mut impl Write, id: i32, data: &[u8]) -> io::Result<()> {
    let mut prefix = Vec::new();
    int(&mut prefix, id);
    let length = prefix
        .len()
        .checked_add(data.len())
        .filter(|n| *n <= 0x1f_ffff)
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "outbound frame too large"))?;
    let mut size = Vec::new();
    int(&mut size, length as i32);
    writer.write_all(&size)?;
    writer.write_all(&prefix)?;
    writer.write_all(data)
}
fn validate_position(bytes: &[u8]) -> CodecResult<(i32, &[u8])> {
    let mut r = Reader(bytes.get(1..).ok_or("missing position fields")?);
    let id = nonnegative(&mut r)?;
    let fields = r.take(60)?;
    r.end()?;
    Ok((id, fields))
}
/// Replace the VarInt field, preserving the official position/velocity/rotation body.
pub fn position_with_id(template: &[u8], id: i32) -> CodecResult<Vec<u8>> {
    if template.first() != Some(&POSITION) || id <= 0 {
        return Err("invalid teleport ID/packet");
    }
    let (_, fields) = validate_position(template)?;
    let mut packet = vec![POSITION];
    int(&mut packet, id);
    packet.extend_from_slice(fields);
    Ok(packet)
}
/// Validate the opaque official codec body and the layouts patched below.
pub fn validate_official_packet(bytes: &[u8], id: u8) -> CodecResult<()> {
    if bytes.first() != Some(&id) || bytes.len() > 0x1f_ffff {
        return Err("invalid world packet ID/size");
    }
    if (id == LOGIN && bytes.len() != 71)
        || (id == CHUNK && bytes.len() < 9)
        || (id == POSITION && validate_position(bytes).is_err())
    {
        return Err("unexpected official packet layout");
    }
    Ok(())
}
pub fn chunk_at(template: &[u8], x: i32, z: i32) -> CodecResult<Vec<u8>> {
    validate_official_packet(template, CHUNK)?;
    let mut packet = template.to_vec();
    packet[1..5].copy_from_slice(&x.to_be_bytes());
    packet[5..9].copy_from_slice(&z.to_be_bytes());
    Ok(packet)
}

pub fn unload_chunk_at(template: &[u8], x: i32, z: i32) -> CodecResult<Vec<u8>> {
    if template.len() != 9 || template[0] != UNLOAD_CHUNK {
        return Err("invalid official unload-chunk template");
    }
    let mut packet = template.to_vec();
    // ChunkPos is a packed long: z occupies the high word and x the low word.
    packet[1..5].copy_from_slice(&z.to_be_bytes());
    packet[5..9].copy_from_slice(&x.to_be_bytes());
    Ok(packet)
}

pub fn chunk_center_at(template: &[u8], x: i32, z: i32) -> CodecResult<Vec<u8>> {
    if template.first() != Some(&CENTER) || template.len() != 3 {
        return Err("invalid official chunk-center template");
    }
    let mut packet = vec![CENTER];
    int(&mut packet, x);
    int(&mut packet, z);
    Ok(packet)
}

pub fn chunk_distance(template: &[u8], id: u8, distance: i32) -> CodecResult<Vec<u8>> {
    if !matches!(id, RENDER_DISTANCE | SIMULATION_DISTANCE)
        || template.first() != Some(&id)
        || template.len() != 2
        || !(0..=32).contains(&distance)
    {
        return Err("invalid official chunk-distance template/value");
    }
    let mut packet = vec![id];
    int(&mut packet, distance);
    Ok(packet)
}

pub fn chunk_batch_finished(template: &[u8], count: usize) -> CodecResult<Vec<u8>> {
    if template.first() != Some(&BATCH_END) || template.len() != 2 || count > 1024 {
        return Err("invalid official chunk-batch template/count");
    }
    let mut packet = vec![BATCH_END];
    int(&mut packet, count as i32);
    Ok(packet)
}
pub fn write_login(writer: &mut impl Write, template: &[u8], entity: i32) -> io::Result<()> {
    validate_official_packet(template, LOGIN)
        .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
    let mut packet = template.to_vec();
    packet[1..5].copy_from_slice(&entity.to_be_bytes());
    write_official_packet(writer, &packet)
}
pub fn write_official_packet(writer: &mut impl Write, packet: &[u8]) -> io::Result<()> {
    let (&id, data) = packet
        .split_first()
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, "empty world packet"))?;
    write_frame(writer, i32::from(id), data)
}

#[cfg(test)]
mod tests {
    #[test]
    fn inventory_outputs_match_official_codecs_and_inputs_are_bounded() {
        let fixture: serde_json::Value =
            serde_json::from_str(include_str!("../../assets/inventory-26.3.json")).unwrap();
        let decode_hex = |value: &str| -> Vec<u8> {
            (0..value.len())
                .step_by(2)
                .map(|i| u8::from_str_radix(&value[i..i + 2], 16).unwrap())
                .collect()
        };
        let mut state = model::State::default();
        state.slots[9] = Stack {
            item: 1,
            count: 64,
            added: Vec::new(),
            removed: Vec::new(),
        };
        state.slots[36] = Stack {
            item: 1050,
            count: 1,
            added: Vec::new(),
            removed: Vec::new(),
        };
        let mut output = Vec::new();
        write_inventory(
            &mut output,
            0,
            128,
            &model::encode_slots(&state.slots),
            &[0],
            8,
        )
        .unwrap();
        let mut reader = Reader(&output);
        let length = reader.length(65536).unwrap();
        assert_eq!(
            reader.take(length).unwrap(),
            decode_hex(
                fixture["inventory_packets"]["inventory_mixed"]["hex"]
                    .as_str()
                    .unwrap()
            )
        );
        let length = reader.length(65536).unwrap();
        assert_eq!(
            reader.take(length).unwrap(),
            decode_hex(
                fixture["inventory_packets"]["inventory_held"]["hex"]
                    .as_str()
                    .unwrap()
            )
        );
        let mut menus = Vec::new();
        write_inventory_menu(&mut menus, 0, 128, "Storage 😀").unwrap();
        let mut reader = Reader(&menus);
        let length = reader.length(65536).unwrap();
        assert_eq!(
            reader.take(length).unwrap(),
            decode_hex(
                fixture["inventory_packets"]["inventory_open"]["hex"]
                    .as_str()
                    .unwrap()
            )
        );
        let mut menus = Vec::new();
        write_inventory_menu(&mut menus, 128, 0, "").unwrap();
        let mut reader = Reader(&menus);
        let length = reader.length(65536).unwrap();
        assert_eq!(
            reader.take(length).unwrap(),
            decode_hex(
                fixture["inventory_packets"]["inventory_close"]["hex"]
                    .as_str()
                    .unwrap()
            )
        );
        assert!(Serverbound::decode(0x12, &[0, 0, 0, 9, 0, 0, 0, 0]).is_ok());
        assert!(Serverbound::decode(0x12, &[0, 0, 0, 9, 0, 7, 0, 0]).is_err());
        assert!(Serverbound::decode(0x12, &[0, 0, 0, 9, 0, 0, 0, 2]).is_err());
        assert!(Serverbound::decode(0x39, &[0, 36, 1, 1, 1, 0, 3, 2, 128, 1]).is_ok());
        assert!(Serverbound::decode(0x39, &[0, 36, 1, 1, 1, 0, 3, 1, 128]).is_err());

        for (name, packet_id) in [
            ("creative_modified", 0x39),
            ("click_default", 0x12),
            ("click_modified", 0x12),
        ] {
            let packet = decode_hex(
                fixture["serverbound_inventory_packets"][name]["hex"]
                    .as_str()
                    .unwrap(),
            );
            assert_eq!(packet[0], packet_id as u8, "{name} packet ID");
            let payload = &packet[1..];
            let decoded = Serverbound::decode(packet_id, payload)
                .unwrap_or_else(|error| panic!("{name}: {error}"));
            match (name, decoded) {
                ("creative_modified", Serverbound::CreativeSlot { slot, stack }) => {
                    assert_eq!(slot, 36);
                    assert_eq!(stack.item, 1050);
                    assert_eq!(stack.count, 1);
                    assert!(!stack.added.is_empty());
                }
                ("click_default", Serverbound::InventoryClick(click)) => {
                    assert_eq!((click.menu, click.revision, click.slot), (0, 128, 36));
                    assert_eq!(click.changed.len(), 1);
                }
                ("click_modified", Serverbound::InventoryClick(click)) => {
                    assert_eq!((click.menu, click.revision, click.slot), (0, 128, 36));
                    assert_eq!(click.changed.len(), 1);
                    assert!(click.cursor.is_some());
                }
                _ => panic!("{name} decoded to an unexpected packet"),
            }
            for end in 0..payload.len() {
                assert!(
                    Serverbound::decode(packet_id, &payload[..end]).is_err(),
                    "{name} accepted truncated prefix {end}"
                );
            }
            let mut trailing = payload.to_vec();
            trailing.push(0);
            assert!(
                Serverbound::decode(packet_id, &trailing).is_err(),
                "{name} accepted trailing bytes"
            );
        }
    }
    #[test]
    fn inventory_registry_fixture_has_pinned_provenance_and_unique_ids() {
        let fixture: serde_json::Value =
            serde_json::from_str(include_str!("../../assets/inventory-26.3.json")).unwrap();
        assert_eq!(fixture["version"], "26.3");
        assert_eq!(fixture["protocol"], 777);
        assert_eq!(
            fixture["server_sha1"],
            "33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c"
        );
        for (key, count) in [("items", 1658), ("components", 122)] {
            let entries = fixture[key].as_array().unwrap();
            assert_eq!(entries.len(), count);
            let mut ids = std::collections::HashSet::new();
            let mut names = std::collections::HashSet::new();
            for entry in entries {
                assert!(ids.insert(entry["id"].as_u64().unwrap()));
                let name = entry["name"].as_str().unwrap();
                assert!(name.starts_with("minecraft:"));
                assert!(names.insert(name));
                if key == "items" {
                    assert!(entry["defaults"].is_object());
                }
            }
        }
        assert_eq!(fixture["slots"]["empty"]["hex"], "00");
        assert_eq!(fixture["slots"]["stone"]["hex"], "40010000");
        assert_eq!(fixture["slots"]["default_sword"]["hex"], "019a080000");
        assert_eq!(
            fixture["slots"]["removed_name_sword"]["hex"],
            "019a08010103800109"
        );
        let samples = fixture["default_component_samples"].as_object().unwrap();
        assert_eq!(samples.len(), 69);
        for (name, sample) in samples {
            assert!(fixture["components"]
                .as_array()
                .unwrap()
                .iter()
                .any(|entry| entry["name"] == *name));
            let hex = sample["hex"].as_str().unwrap();
            assert_eq!(hex.len() % 2, 0);
            assert!(hex.bytes().all(|byte| byte.is_ascii_hexdigit()));
            assert!(sample["value_class"].is_string());
            assert!(sample["codec_class"].is_string());
        }
    }
    use super::*;

    fn hex(text: &str) -> Vec<u8> {
        text.as_bytes()
            .as_chunks::<2>()
            .0
            .iter()
            .map(|p| u8::from_str_radix(std::str::from_utf8(p).unwrap(), 16).unwrap())
            .collect()
    }
    #[test]
    fn official_next_play_samples_are_exact_and_strict() {
        // Emitted and roundtripped by the pinned official 26.3 Play codec.
        for text in [
            "0502",
            "2e",
            "360008",
            "2b7f",
            "4300800142b40000c2340000",
            "42010000020000008040013f0000003f8000003f00000000008001",
            "29000000020000008040018001",
            "008080014021000000000000405040000000000040210000000000000000000000000000",
        ] {
            let bytes = hex(text);
            let (id, data) = (i32::from(bytes[0]), &bytes[1..]);
            assert!(Serverbound::decode(id, data).is_ok(), "{text}");
            for n in 0..data.len() {
                assert!(Serverbound::decode(id, &data[..n]).is_err());
            }
            let mut trailing = data.to_vec();
            trailing.push(0);
            assert!(Serverbound::decode(id, &trailing).is_err());
        }
        assert!(Serverbound::decode(0x2e, &[0]).is_err());
        assert!(Serverbound::decode(0x2b, &[128]).is_err());
        assert!(Serverbound::decode(0x36, &[0, 9]).is_err());
        assert!(Serverbound::decode(0x35, &[0, 1]).is_err());
        assert!(matches!(
            Serverbound::decode(0x05, &[2]),
            Ok(Serverbound::ChangeGameMode(2))
        ));
        assert!(Serverbound::decode(0x05, &[4]).is_err());
        for id in [1, 127, 128, 16384, i32::MAX] {
            let template = crate::vanilla_world::world()
                .unwrap()
                .initialization
                .last()
                .unwrap();
            let packet = position_with_id(template, id).unwrap();
            assert_eq!(validate_position(&packet).unwrap().0, id);
            let mut ack = Vec::new();
            int(&mut ack, id);
            ack.extend([0; 32]);
            assert!(
                matches!(Serverbound::decode(0,&ack), Ok(Serverbound::TeleportAcknowledged(a)) if a.id == id)
            );
        }
        let mut wire = Vec::new();
        Clientbound::SystemMessage(configuration::NetworkNbt::text("Hello 😀").unwrap())
            .write(&mut wire)
            .unwrap();
        assert_eq!(&wire[1..], hex("7c08000c48656c6c6f20eda0bdedb88000"));
        wire.clear();
        Clientbound::BlockAcknowledgement(128)
            .write(&mut wire)
            .unwrap();
        assert_eq!(wire, hex("03048001"));
        wire.clear();
        Clientbound::BlockUpdate(BlockPosition { x: 8, y: 64, z: 8 }, 9)
            .write(&mut wire)
            .unwrap();
        assert_eq!(&wire[1..], hex("08000002000000804009"));
        wire.clear();
        Clientbound::Abilities {
            flags: 0x0d,
            flying_speed: 0.05,
            walking_speed: 0.1,
        }
        .write(&mut wire)
        .unwrap();
        assert_eq!(wire[1], 0x41);
        wire.clear();
        Clientbound::Health {
            health: 20.0,
            food: 20,
            saturation: 5.0,
        }
        .write(&mut wire)
        .unwrap();
        assert_eq!(wire[1], 0x6a);
        wire.clear();
        Clientbound::GameEvent {
            event: 3,
            value: 1.0,
        }
        .write(&mut wire)
        .unwrap();
        assert_eq!(wire[1], 0x27);
    }
    #[test]
    fn authoritative_corrections_match_official_777_codec() {
        let fixtures: serde_json::Value =
            serde_json::from_str(include_str!("../../assets/teleport-26.3.json")).unwrap();
        assert_eq!(fixtures["version"], "26.3");
        assert_eq!(fixtures["protocol"], 777);
        assert_eq!(
            fixtures["server_sha1"],
            "33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c"
        );
        let samples = fixtures["corrections"].as_array().unwrap();
        assert_eq!(samples.len(), 10);
        for sample in samples {
            let pose = SynchronizePlayerPosition {
                id: i32::try_from(sample["id"].as_i64().unwrap()).unwrap(),
                position: [-30.25, 299.5, 45.75],
                velocity: [-0.5, 0.25, 1.5],
                rotation: [270.5, -89.5],
                flags: i32::try_from(sample["flags"].as_i64().unwrap()).unwrap(),
            };
            assert_eq!(pose.packet().unwrap(), hex(sample["hex"].as_str().unwrap()));
            for bad_id in [0, -1] {
                assert!(SynchronizePlayerPosition { id: bad_id, ..pose }
                    .packet()
                    .is_err());
            }
            assert!(SynchronizePlayerPosition {
                flags: 0x200,
                ..pose
            }
            .packet()
            .is_err());
            assert!(SynchronizePlayerPosition {
                velocity: [f64::NAN; 3],
                ..pose
            }
            .packet()
            .is_err());
            assert!(SynchronizePlayerPosition {
                position: [f64::INFINITY; 3],
                ..pose
            }
            .packet()
            .is_err());
            assert!(SynchronizePlayerPosition {
                rotation: [f32::NEG_INFINITY; 2],
                ..pose
            }
            .packet()
            .is_err());
        }
    }
    #[test]
    fn player_entity_packets_match_official_26_3_fixtures() {
        let fixtures: serde_json::Value =
            serde_json::from_str(include_str!("../../assets/entity-26.3.json")).unwrap();
        assert_eq!(fixtures["version"], "26.3");
        assert_eq!(fixtures["protocol"], 777);
        assert_eq!(
            fixtures["server_sha1"],
            "33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c"
        );
        let packets = &fixtures["packets"];
        let player = PlayerEntity {
            entity_id: 128,
            player_uuid: uuid::Uuid::from_u128(0x00112233445566778899aabbccddeeff),
            username: "FixturePlayer".into(),
            position: [8.5, 65.0, 8.5],
            rotation: [90.0, -45.0],
            on_ground: true,
        };
        let (info, spawn) = player_spawn_packets(&player).unwrap();
        assert_eq!(spawn, hex(packets["spawn_player"]["hex"].as_str().unwrap()));
        assert_eq!(
            info,
            [
                vec![0x47],
                hex(packets["player_info_entry"]["hex"].as_str().unwrap())
            ]
            .concat()
        );
        let (remove_entity, remove_profile) =
            player_remove_packets(player.player_uuid, player.entity_id).unwrap();
        assert_eq!(
            remove_entity,
            hex(packets["remove_entity"]["hex"].as_str().unwrap())
        );
        assert_eq!(
            remove_profile,
            hex(packets["remove_player_info"]["hex"].as_str().unwrap())
        );
        let moved = player.clone();
        assert_eq!(
            player_move_packet(&moved, [0.015625, -0.0078125, 0.00390625], true).unwrap(),
            hex(packets["move_player"]["hex"].as_str().unwrap())
        );
        let airborne = PlayerEntity {
            on_ground: false,
            ..moved.clone()
        };
        assert_eq!(
            player_move_packet(&airborne, [-0.015625, 0.0078125, -0.00390625], false).unwrap(),
            hex(packets["move_player_position"]["hex"].as_str().unwrap())
        );
        assert_eq!(
            player_move_packet(&moved, [0.0; 3], true).unwrap(),
            hex(packets["move_player_rotation"]["hex"].as_str().unwrap())
        );
        for invalid in [
            PlayerEntity {
                entity_id: 0,
                ..player.clone()
            },
            PlayerEntity {
                username: "bad name".into(),
                ..player.clone()
            },
            PlayerEntity {
                position: [f64::NAN; 3],
                ..player.clone()
            },
        ] {
            assert!(player_spawn_packets(&invalid).is_err());
        }
        assert!(player_move_packet(&player, [16.0; 3], true).is_err());
    }

    #[test]
    fn death_and_same_dimension_respawn_match_official_26_3_codecs() {
        let fixtures: serde_json::Value =
            serde_json::from_str(include_str!("../../assets/entity-26.3.json")).unwrap();
        assert_eq!(fixtures["version"], "26.3");
        assert_eq!(fixtures["protocol"], 777);
        assert_eq!(
            fixtures["server_sha1"],
            "33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c"
        );
        let packets = &fixtures["packets"];
        let death = Clientbound::CombatDeath {
            entity_id: 1,
            message: configuration::NetworkNbt::text("You died").unwrap(),
        };
        let mut wire = Vec::new();
        death.write(&mut wire).unwrap();
        assert_eq!(usize::from(wire[0]), wire.len() - 1);
        assert_eq!(
            &wire[1..],
            hex(packets["combat_death"]["hex"].as_str().unwrap())
        );
        for adventure in [false, true] {
            let mut wire = Vec::new();
            Clientbound::Respawn { adventure }.write(&mut wire).unwrap();
            assert_eq!(usize::from(wire[0]), wire.len() - 1);
            let name = if adventure {
                "respawn_adventure"
            } else {
                "respawn_survival"
            };
            assert_eq!(&wire[1..], hex(packets[name]["hex"].as_str().unwrap()));
        }
        let client_respawn = hex(packets["client_status_respawn"]["hex"].as_str().unwrap());
        assert_eq!(client_respawn, [vec![0x0c], vec![0x00]].concat());
        assert_eq!(
            Serverbound::decode(0x0c, &[0x00]),
            Ok(Serverbound::ClientStatus(0))
        );
        assert_eq!(
            Serverbound::decode(0x0c, &[0x01]),
            Ok(Serverbound::ClientStatus(1))
        );
        assert_eq!(
            Serverbound::decode(0x0c, &[0x02]),
            Ok(Serverbound::ClientStatus(2))
        );
        assert!(Serverbound::decode(0x0c, &[0x03]).is_err());
        assert!(Serverbound::decode(0x0c, &[0x00, 0x00]).is_err());
    }

    #[test]
    fn interaction_ranges_and_inventory_bounds() {
        for action in 0..=7 {
            let mut fields = vec![action];
            fields.extend([0; 8]);
            fields.extend([0, 0]);
            assert!(Serverbound::decode(0x29, &fields).is_ok());
            fields[9] = 6;
            assert!(Serverbound::decode(0x29, &fields).is_err());
        }
        for action in 0..=6 {
            assert!(Serverbound::decode(0x2a, &[1, action, 0]).is_ok());
        }
        assert!(Serverbound::decode(0x2a, &[1, 7, 0]).is_err());
        assert!(Serverbound::decode(0x2a, &[1, 3, 101]).is_err());
        assert!(Serverbound::decode(0x39, &[0, 36, 1, 1, 0, 0]).is_ok());
        assert!(Serverbound::decode(0x39, &[0, 36]).is_err());
        assert!(Serverbound::decode(0x39, &vec![0; 65537]).is_err());
        for value in [f32::NAN, f32::INFINITY] {
            let mut use_item = vec![0, 0];
            use_item.extend(value.to_be_bytes());
            use_item.extend([0; 4]);
            assert!(Serverbound::decode(0x43, &use_item).is_err());
        }
        let p = BlockPosition {
            x: -32,
            y: -64,
            z: -1,
        };
        assert_eq!(
            BlockPosition::read(&mut Reader(&p.packed().to_be_bytes())).unwrap(),
            p
        );
    }
    #[test]
    fn fixed_fields_reject_truncation_trailing_bytes_and_invalid_flags() {
        for (id, data) in [
            (0x2c, vec![]),
            (0x0d, vec![]),
            (0x28, vec![2]),
            (0x1c, vec![0; 8]),
            (0x00, vec![0; 33]),
            (0x0b, 1.0_f32.to_be_bytes().to_vec()),
        ] {
            assert!(Serverbound::decode(id, &data).is_ok());
            for length in 0..data.len() {
                assert!(Serverbound::decode(id, &data[..length]).is_err());
            }
            let mut trailing = data;
            trailing.push(0);
            assert!(Serverbound::decode(id, &trailing).is_err());
        }
        assert!(Serverbound::decode(0x28, &[1]).is_err());
        assert_eq!(
            Serverbound::decode(0x28, &[2]),
            Ok(Serverbound::Abilities { flying: true })
        );
        assert_eq!(
            Serverbound::decode(0x28, &[0]),
            Ok(Serverbound::Abilities { flying: false })
        );
        for rate in [0.0_f32, -1.0, f32::NAN, f32::INFINITY] {
            assert!(Serverbound::decode(0x0b, &rate.to_be_bytes()).is_err());
        }
    }

    #[test]
    fn deferred_connection_controls_are_not_accepted_in_play() {
        // These controls need an outstanding request or a complete state
        // transition. Until those flows are implemented, the connection
        // policy closes the session instead of accepting unsolicited replies.
        for id in [0x10, 0x15, 0x26, 0x2d] {
            assert_eq!(
                Serverbound::decode(id, &[]),
                Err("unsupported Play packet"),
                "Play packet 0x{id:02x} must stay unsupported"
            );
        }
    }

    #[test]
    fn movement_validates_fields_not_world_bounds() {
        for (id, position, rotation) in [
            (0x1e, true, false),
            (0x1f, true, true),
            (0x20, false, true),
            (0x21, false, false),
        ] {
            let mut data = Vec::new();
            if position {
                for value in [1000.0_f64, -500.0, 1000.0] {
                    data.extend(value.to_be_bytes());
                }
            }
            if rotation {
                for value in [90.0_f32, -45.0] {
                    data.extend(value.to_be_bytes());
                }
            }
            data.push(3);
            assert!(matches!(
                Serverbound::decode(id, &data),
                Ok(Serverbound::Movement(_))
            ));
            for length in 0..data.len() {
                assert!(Serverbound::decode(id, &data[..length]).is_err());
            }
            *data.last_mut().unwrap() = 4;
            assert!(Serverbound::decode(id, &data).is_err());
        }
        let mut nonfinite = f64::NAN.to_be_bytes().to_vec();
        nonfinite.extend([0; 17]);
        assert!(Serverbound::decode(0x1e, &nonfinite).is_err());
        let mut nonfinite = f32::INFINITY.to_be_bytes().to_vec();
        nonfinite.extend([0; 5]);
        assert!(Serverbound::decode(0x20, &nonfinite).is_err());
    }

    #[test]
    fn teleport_preserves_bits_and_leaves_expected_id_to_session() {
        let mut data = vec![2];
        for value in [-0.0_f64, 65.0, 8.5] {
            data.extend(value.to_be_bytes());
        }
        data.extend([0; 8]);
        let Serverbound::TeleportAcknowledged(ack) = Serverbound::decode(0, &data).unwrap() else {
            panic!()
        };
        assert_eq!(ack.id, 2);
        assert_eq!(ack.position[0], (-0.0_f64).to_bits());
    }

    #[test]
    fn chat_session_arrays_are_bounded_and_consumed_exactly() {
        let mut data = vec![0; 24];
        data.extend([1, 42, 0]);
        assert_eq!(
            Serverbound::decode(0x0a, &data),
            Ok(Serverbound::ChatSession)
        );
        data.push(0);
        assert!(Serverbound::decode(0x0a, &data).is_err());
        for length in [-1, 4097] {
            let mut data = vec![0; 24];
            int(&mut data, length);
            assert!(Serverbound::decode(0x0a, &data).is_err());
        }
    }

    #[test]
    fn chat_decoder_distinguishes_unsigned_from_unverified_signed_payloads() {
        let mut unsigned = Vec::new();
        put_wire_string(&mut unsigned, "hello 😀").unwrap();
        unsigned.extend([0; 16]); // timestamp and salt
        unsigned.push(0); // no signature
        int(&mut unsigned, 0); // no last-seen offset
        unsigned.extend([0; 4]); // acknowledgement bitset
        assert_eq!(
            Serverbound::decode(0x09, &unsigned),
            Ok(Serverbound::ChatMessage("hello 😀".into()))
        );

        let mut signed = Vec::new();
        put_wire_string(&mut signed, "hello 😀").unwrap();
        signed.extend([0; 16]);
        signed.push(1);
        signed.extend([0; 256]);
        int(&mut signed, 0);
        signed.extend([0; 4]);
        assert_eq!(
            Serverbound::decode(0x09, &signed),
            Ok(Serverbound::SignedChatMessage("hello 😀".into()))
        );

        let mut signed_command = Vec::new();
        put_wire_string(&mut signed_command, "help").unwrap();
        signed_command.extend([0; 16]);
        int(&mut signed_command, 0); // no argument signatures
        int(&mut signed_command, 0); // no last-seen offset
        signed_command.extend([0; 4]);
        assert_eq!(
            Serverbound::decode(0x08, &signed_command),
            Ok(Serverbound::SignedChatCommand("help".into()))
        );
    }

    #[test]
    fn official_patches_and_frames_preserve_untouched_bytes() {
        let mut login = vec![7; 71];
        login[0] = LOGIN;
        let original = login.clone();
        let mut output = Vec::new();
        write_login(&mut output, &login, 123).unwrap();
        assert_eq!(&output[..2], &[71, LOGIN]);
        assert_eq!(&output[2..6], &123_i32.to_be_bytes());
        assert_eq!(&output[6..], &login[5..]);
        assert_eq!(login, original);
        let template = [CHUNK, 0, 0, 0, 0, 0, 0, 0, 0, 42];
        let chunk = chunk_at(&template, -2, 2).unwrap();
        assert_eq!(&chunk[1..5], &(-2_i32).to_be_bytes());
        assert_eq!(&chunk[5..9], &2_i32.to_be_bytes());
        assert_eq!(chunk[9], 42);
        assert!(chunk_at(&template[..8], 0, 0).is_err());
        assert!(validate_official_packet(&login[..70], LOGIN).is_err());
        assert!(validate_official_packet(&[POSITION; 61], POSITION).is_err());
        output.clear();
        Clientbound::KeepAlive(-1).write(&mut output).unwrap();
        assert_eq!(output, [vec![9, 0x2d], vec![255; 8]].concat());
        output.clear();
        let nbt = configuration::NetworkNbt::text("Bye").unwrap();
        Clientbound::Disconnect(nbt.clone())
            .write(&mut output)
            .unwrap();
        assert_eq!(output, [vec![7, 0x20], nbt.as_bytes().to_vec()].concat());
    }

    #[test]
    fn chunk_interest_controls_match_official_26_3_fixtures() {
        let fixtures: serde_json::Value =
            serde_json::from_str(include_str!("../../assets/chunk-stream-26.3.json")).unwrap();
        assert_eq!(fixtures["version"], "26.3");
        assert_eq!(fixtures["protocol"], 777);
        assert_eq!(
            fixtures["server_sha1"],
            "33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c"
        );
        let packets = &fixtures["packets"];
        let unload = hex(packets["unload_chunk"]["hex"].as_str().unwrap());
        assert_eq!(unload_chunk_at(&unload, -2, 1).unwrap(), unload);
        assert_eq!(
            chunk_center_at(&hex("600000"), -2, 1).unwrap(),
            hex("60feffffff0f01")
        );
        assert_eq!(
            chunk_distance(
                &hex(packets["render_distance"]["hex"].as_str().unwrap()),
                RENDER_DISTANCE,
                2,
            )
            .unwrap(),
            hex("6102")
        );
        assert_eq!(
            chunk_distance(
                &hex(packets["simulation_distance"]["hex"].as_str().unwrap()),
                SIMULATION_DISTANCE,
                2,
            )
            .unwrap(),
            hex("7102")
        );
        assert_eq!(
            chunk_batch_finished(&hex(packets["batch_end"]["hex"].as_str().unwrap()), 25).unwrap(),
            hex("0b19")
        );
        assert!(chunk_batch_finished(&hex("0b19"), 1025).is_err());
    }
}
