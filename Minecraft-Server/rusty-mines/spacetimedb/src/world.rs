//! Persistent, bounded block overrides. Mutations are session-owned and
//! transactional; gateways consume only the sender-scoped public view.
use crate::foundation::{gateway__view, player_pose, player_session, player_session__view};
use crate::inventory::player_inventory;
use crate::{
    foundation,
    policy::{self, SessionPhase, WorldGameMode},
};
use rusty_mines_inventory::{model, wire::Stack};
use spacetimedb::{ReducerContext, SpacetimeType, Table, Uuid, ViewContext};

const WORLD_ID: &str = "rusty-mines-flat-26.3-v1";
const ACTION_WINDOW: i64 = 10_000_000;
const MAX_ACTIONS_PER_WINDOW: u8 = 40;
const MAX_CHUNK_OVERRIDES: usize = 128;
const MAX_WORLD_OVERRIDES: u64 = 65_536;

#[derive(SpacetimeType, Copy, Clone, Debug, PartialEq, Eq)]
pub enum GameMode {
    Survival,
    Creative,
    Adventure,
    Spectator,
}

#[spacetimedb::table(accessor = player_game_mode)]
pub struct PlayerGameMode {
    #[primary_key]
    pub player_uuid: Uuid,
    pub mode: GameMode,
}

#[spacetimedb::table(accessor = player_vitals)]
#[derive(Clone)]
pub struct PlayerVitals {
    #[primary_key]
    pub player_uuid: Uuid,
    pub health: u8,
    pub food: u8,
    pub dead: bool,
    pub revision: u64,
    pub respawn_revision: Option<u64>,
    pub connected_gateway: Option<spacetimedb::Identity>,
    pub connected_session: Option<Uuid>,
}

#[derive(SpacetimeType)]
pub struct GatewayPlayerState {
    pub session_uuid: Uuid,
    pub mode: GameMode,
    pub health: u8,
    pub food: u8,
    pub dead: bool,
    pub revision: u64,
    pub respawn_revision: Option<u64>,
    pub state_owner: Option<spacetimedb::Identity>,
}

#[spacetimedb::view(accessor = gateway_player_state, public, primary_key = session_uuid)]
pub fn gateway_player_state(ctx: &ViewContext) -> Vec<GatewayPlayerState> {
    if ctx.db.gateway().identity().find(ctx.sender()).is_none() {
        return Vec::new();
    }
    ctx.db
        .player_session()
        .gateway_identity()
        .filter(ctx.sender())
        .filter_map(|session| {
            if session.phase != SessionPhase::Play {
                return None;
            }
            let mode = ctx
                .db
                .player_game_mode()
                .player_uuid()
                .find(session.player_uuid)?
                .mode;
            let vitals = ctx
                .db
                .player_vitals()
                .player_uuid()
                .find(session.player_uuid)?;
            if vitals.connected_gateway != Some(ctx.sender())
                || vitals.connected_session != Some(session.session_uuid)
            {
                return None;
            }
            Some(GatewayPlayerState {
                session_uuid: session.session_uuid,
                mode,
                health: vitals.health,
                food: vitals.food,
                dead: vitals.dead,
                revision: vitals.revision,
                respawn_revision: vitals.respawn_revision,
                state_owner: vitals.connected_gateway,
            })
        })
        .take(128)
        .collect()
}

pub(crate) fn claim_player_state(ctx: &ReducerContext, session_uuid: Uuid) -> Result<(), String> {
    let session = ctx
        .db
        .player_session()
        .session_uuid()
        .find(session_uuid)
        .ok_or("Session not found")?;
    let mut row = ctx
        .db
        .player_vitals()
        .player_uuid()
        .find(session.player_uuid)
        .ok_or("Player vitals are unavailable")?;
    if row.connected_session.is_some() || row.connected_gateway.is_some() {
        return Err("Player already has an active state owner".into());
    }
    row.connected_gateway = Some(ctx.sender());
    row.connected_session = Some(session_uuid);
    ctx.db.player_vitals().player_uuid().update(row);
    Ok(())
}

pub(crate) fn release_player_state(ctx: &ReducerContext, player_uuid: Uuid, session_uuid: Uuid) {
    if let Some(mut row) = ctx.db.player_vitals().player_uuid().find(player_uuid) {
        if row.connected_session == Some(session_uuid) {
            row.connected_gateway = None;
            row.connected_session = None;
            ctx.db.player_vitals().player_uuid().update(row);
        }
    }
}

#[spacetimedb::reducer]
pub fn update_player_vitals(
    ctx: &ReducerContext,
    session_uuid: Uuid,
    health: u8,
    food: u8,
    dead: bool,
    expected_revision: u64,
) -> Result<(), String> {
    let connection = foundation::require_gateway(ctx)?;
    let session = ctx
        .db
        .player_session()
        .session_uuid()
        .find(session_uuid)
        .ok_or("Session not found")?;
    if !foundation::owns_session(&session, ctx.sender(), connection) {
        return Err("Vitals session belongs to another connection".into());
    }
    foundation::require_session_lease(ctx, &session)?;
    if session.phase != SessionPhase::Play {
        return Err("Vitals require active Play".into());
    }
    if health > 20 || food > 20 {
        return Err("Vitals are out of range".into());
    }
    let current = ctx
        .db
        .player_vitals()
        .player_uuid()
        .find(session.player_uuid)
        .ok_or("Player vitals are unavailable")?;
    if current.revision != expected_revision {
        return Err("Player vitals changed; reconcile and retry".into());
    }
    if dead && (health != 0 || current.dead) {
        return Err("Invalid death transition".into());
    }
    if !dead && health == 0 {
        return Err("Zero health must mark the player dead".into());
    }
    if current.connected_gateway != Some(ctx.sender())
        || current.connected_session != Some(session_uuid)
    {
        return Err("Player state is owned by another active session".into());
    }
    let mut next = PlayerVitals {
        connected_gateway: current.connected_gateway,
        connected_session: current.connected_session,
        ..current.clone()
    };
    next.revision = next
        .revision
        .checked_add(1)
        .ok_or("Vitals revision exhausted")?;
    next.health = health;
    next.food = food;
    next.dead = dead;
    ctx.db.player_vitals().player_uuid().update(next);
    Ok(())
}

#[spacetimedb::reducer]
pub fn request_player_respawn(ctx: &ReducerContext, session_uuid: Uuid) -> Result<(), String> {
    let connection = foundation::require_gateway(ctx)?;
    let session = ctx
        .db
        .player_session()
        .session_uuid()
        .find(session_uuid)
        .ok_or("Session not found")?;
    if !foundation::owns_session(&session, ctx.sender(), connection) {
        return Err("Respawn session belongs to another connection".into());
    }
    foundation::require_session_lease(ctx, &session)?;
    if session.phase != SessionPhase::Play {
        return Err("Respawn requires active Play".into());
    }
    let mode = ctx
        .db
        .player_game_mode()
        .player_uuid()
        .find(session.player_uuid)
        .ok_or("Game mode is unavailable")?
        .mode;
    if !matches!(mode, GameMode::Survival | GameMode::Adventure) {
        return Err("Respawn is available only in Survival or Adventure".into());
    }
    let vitals = ctx
        .db
        .player_vitals()
        .player_uuid()
        .find(session.player_uuid)
        .ok_or("Player vitals are unavailable")?;
    if !vitals.dead {
        return Err("Player is not dead".into());
    }
    if vitals.connected_gateway != Some(ctx.sender())
        || vitals.connected_session != Some(session_uuid)
    {
        return Err("Player state is owned by another active session".into());
    }
    let mut next = vitals.clone();
    next.revision = next
        .revision
        .checked_add(1)
        .ok_or("Vitals revision exhausted")?;
    next.health = 20;
    next.dead = false;
    next.respawn_revision = Some(next.revision);
    ctx.db.player_vitals().player_uuid().update(next);
    Ok(())
}

#[derive(SpacetimeType)]
pub struct GatewayGameMode {
    pub session_uuid: Uuid,
    pub mode: GameMode,
}

#[spacetimedb::view(accessor = gateway_game_modes, public, primary_key = session_uuid)]
pub fn gateway_game_modes(ctx: &ViewContext) -> Vec<GatewayGameMode> {
    if ctx.db.gateway().identity().find(ctx.sender()).is_none() {
        return Vec::new();
    }
    ctx.db
        .player_session()
        .gateway_identity()
        .filter(ctx.sender())
        .filter_map(|session| {
            if session.phase != SessionPhase::Play {
                return None;
            }
            let mode = ctx
                .db
                .player_game_mode()
                .player_uuid()
                .find(session.player_uuid)?
                .mode;
            Some(GatewayGameMode {
                session_uuid: session.session_uuid,
                mode,
            })
        })
        .take(128)
        .collect()
}

#[spacetimedb::reducer]
pub fn change_game_mode(
    ctx: &ReducerContext,
    session_uuid: Uuid,
    mode: GameMode,
) -> Result<(), String> {
    let connection = foundation::require_gateway(ctx)?;
    let session = ctx
        .db
        .player_session()
        .session_uuid()
        .find(session_uuid)
        .ok_or("Session not found")?;
    if !foundation::owns_session(&session, ctx.sender(), connection) {
        return Err("Mode session belongs to another connection".into());
    }
    foundation::require_session_lease(ctx, &session)?;
    if session.phase != SessionPhase::Play {
        return Err("Mode changes require active Play".into());
    }
    if matches!(mode, GameMode::Creative | GameMode::Spectator) {
        return Err("Creative and Spectator must be granted by an administrator".into());
    }
    let vitals = ctx
        .db
        .player_vitals()
        .player_uuid()
        .find(session.player_uuid)
        .ok_or("Player vitals are unavailable")?;
    if vitals.connected_gateway != Some(ctx.sender())
        || vitals.connected_session != Some(session_uuid)
    {
        return Err("Player state is owned by another active session".into());
    }
    let row = PlayerGameMode {
        player_uuid: session.player_uuid,
        mode,
    };
    if ctx
        .db
        .player_game_mode()
        .player_uuid()
        .find(session.player_uuid)
        .is_some()
    {
        ctx.db.player_game_mode().player_uuid().update(row);
    } else {
        ctx.db.player_game_mode().insert(row);
    }
    Ok(())
}

#[spacetimedb::reducer]
pub fn grant_player_game_mode(
    ctx: &ReducerContext,
    player_uuid: Uuid,
    mode: GameMode,
) -> Result<(), String> {
    foundation::require_admin(ctx)?;
    let row = PlayerGameMode { player_uuid, mode };
    if ctx
        .db
        .player_game_mode()
        .player_uuid()
        .find(player_uuid)
        .is_some()
    {
        ctx.db.player_game_mode().player_uuid().update(row);
    } else {
        ctx.db.player_game_mode().insert(row);
    }
    Ok(())
}

#[spacetimedb::table(accessor = block_override,
    index(accessor = by_chunk, btree(columns = [chunk_key])))]
pub struct BlockOverride {
    #[primary_key]
    pub block_key: String,
    pub chunk_key: String,
    pub world_id: String,
    pub chunk_x: i32,
    pub chunk_z: i32,
    pub x: i32,
    pub y: i32,
    pub z: i32,
    pub state_id: u32,
    pub revision: u64,
    pub updated_by: Uuid,
}

#[spacetimedb::table(accessor = block_action_rate)]
pub struct BlockActionRate {
    #[primary_key]
    pub session_uuid: Uuid,
    pub window_started_micros: i64,
    pub action_count: u8,
}

#[spacetimedb::table(accessor = block_action_state)]
pub struct BlockActionState {
    #[primary_key]
    pub session_uuid: Uuid,
    pub gateway_identity: spacetimedb::Identity,
    pub sequence: u64,
    pub x: i32,
    pub y: i32,
    pub z: i32,
    pub expected_state: u32,
    pub resulting_state: u32,
    pub revision: u64,
}

#[derive(SpacetimeType)]
pub struct GatewayBlockAction {
    pub session_uuid: Uuid,
    pub sequence: u64,
    pub x: i32,
    pub y: i32,
    pub z: i32,
    pub expected_state: u32,
    pub resulting_state: u32,
    pub revision: u64,
}

#[spacetimedb::view(accessor = gateway_block_actions, public, primary_key = session_uuid)]
pub fn gateway_block_actions(ctx: &spacetimedb::ViewContext) -> Vec<GatewayBlockAction> {
    if ctx.db.gateway().identity().find(ctx.sender()).is_none() {
        return Vec::new();
    }
    ctx.db
        .player_session()
        .gateway_identity()
        .filter(ctx.sender())
        .filter_map(|session| {
            if session.phase != SessionPhase::Play {
                return None;
            }
            let row = ctx
                .db
                .block_action_state()
                .session_uuid()
                .find(session.session_uuid)?;
            Some(GatewayBlockAction {
                session_uuid: row.session_uuid,
                sequence: row.sequence,
                x: row.x,
                y: row.y,
                z: row.z,
                expected_state: row.expected_state,
                resulting_state: row.resulting_state,
                revision: row.revision,
            })
        })
        .take(128)
        .collect()
}

#[spacetimedb::table(accessor = world_interest,
    index(accessor = by_session, btree(columns = [session_uuid])),
    index(accessor = by_gateway, btree(columns = [gateway_identity])),
    index(accessor = by_chunk, btree(columns = [chunk_key])))]
pub struct WorldInterest {
    #[primary_key]
    pub interest_key: String,
    pub session_uuid: Uuid,
    pub gateway_identity: spacetimedb::Identity,
    pub world_id: String,
    pub chunk_key: String,
    pub chunk_x: i32,
    pub chunk_z: i32,
}

#[spacetimedb::table(accessor = gateway_chunk_state)]
pub struct GatewayChunkState {
    #[primary_key]
    pub snapshot_key: String,
    pub gateway_identity: spacetimedb::Identity,
    pub world_id: String,
    pub chunk_key: String,
    pub chunk_x: i32,
    pub chunk_z: i32,
    /// At most 128 entries, each `(x, y, z, block_state)` as little-endian i32.
    pub encoded_overrides: Vec<u8>,
    pub revision: u64,
}

#[derive(SpacetimeType)]
pub struct GatewayChunkSnapshot {
    pub snapshot_key: String,
    pub world_id: String,
    pub chunk_x: i32,
    pub chunk_z: i32,
    pub encoded_overrides: Vec<u8>,
    pub revision: u64,
}

#[spacetimedb::view(accessor = gateway_chunk_snapshots, public, primary_key = snapshot_key)]
pub fn gateway_chunk_snapshots(ctx: &ViewContext) -> Vec<GatewayChunkSnapshot> {
    if ctx.db.gateway().identity().find(ctx.sender()).is_none() {
        return Vec::new();
    }
    let mut seen = std::collections::BTreeSet::new();
    ctx.db
        .player_session()
        .gateway_identity()
        .filter(ctx.sender())
        .take(128)
        .flat_map(|session| {
            ctx.db
                .world_interest()
                .by_session()
                .filter(session.session_uuid)
                .take(25)
        })
        .filter_map(|interest| {
            let key = snapshot_key(ctx.sender(), &interest.chunk_key);
            if !seen.insert(key.clone()) {
                return None;
            }
            let row = ctx.db.gateway_chunk_state().snapshot_key().find(&key)?;
            Some(GatewayChunkSnapshot {
                snapshot_key: row.snapshot_key,
                world_id: row.world_id,
                chunk_x: row.chunk_x,
                chunk_z: row.chunk_z,
                encoded_overrides: row.encoded_overrides,
                revision: row.revision,
            })
        })
        .take(3_200)
        .collect()
}

fn block_key(x: i32, y: i32, z: i32) -> String {
    format!("{WORLD_ID}:{x}:{y}:{z}")
}

fn chunk_key(x: i32, z: i32) -> String {
    format!("{WORLD_ID}:{}:{}", x.div_euclid(16), z.div_euclid(16))
}

fn interest_key(
    gateway: spacetimedb::Identity,
    session_uuid: Uuid,
    chunk_x: i32,
    chunk_z: i32,
) -> String {
    format!("{gateway}:{session_uuid}:{WORLD_ID}:{chunk_x}:{chunk_z}")
}

fn snapshot_key(gateway: spacetimedb::Identity, chunk_key: &str) -> String {
    format!("{gateway}:{chunk_key}")
}

fn rebuild_chunk_snapshot(
    ctx: &ReducerContext,
    gateway: spacetimedb::Identity,
    chunk_x: i32,
    chunk_z: i32,
) -> Result<(), String> {
    let key = chunk_key(chunk_x * 16, chunk_z * 16);
    let mut encoded = Vec::new();
    for row in ctx
        .db
        .block_override()
        .by_chunk()
        .filter(&key)
        .take(MAX_CHUNK_OVERRIDES + 1)
    {
        if encoded.len() / 16 == MAX_CHUNK_OVERRIDES {
            return Err("Chunk override limit reached".into());
        }
        for value in [row.x, row.y, row.z] {
            encoded.extend(value.to_le_bytes());
        }
        encoded.extend(row.state_id.to_le_bytes());
    }
    let key_id = snapshot_key(gateway, &key);
    let current = ctx.db.gateway_chunk_state().snapshot_key().find(&key_id);
    let revision = current
        .as_ref()
        .map(|row| {
            row.revision
                .checked_add(1)
                .ok_or("Chunk snapshot revision exhausted")
        })
        .transpose()?
        .unwrap_or(1);
    let row = GatewayChunkState {
        snapshot_key: key_id.clone(),
        gateway_identity: gateway,
        world_id: WORLD_ID.into(),
        chunk_key: key,
        chunk_x,
        chunk_z,
        encoded_overrides: encoded,
        revision,
    };
    if current.is_some() {
        ctx.db.gateway_chunk_state().snapshot_key().update(row);
    } else {
        ctx.db.gateway_chunk_state().insert(row);
    }
    Ok(())
}

pub(crate) fn remove_session_state(ctx: &ReducerContext, session_uuid: Uuid) {
    for row in ctx
        .db
        .world_interest()
        .by_session()
        .filter(session_uuid)
        .collect::<Vec<_>>()
    {
        ctx.db
            .world_interest()
            .interest_key()
            .delete(&row.interest_key);
        if !ctx
            .db
            .world_interest()
            .by_gateway()
            .filter(row.gateway_identity)
            .take(3_200)
            .any(|other| other.chunk_key == row.chunk_key)
        {
            ctx.db
                .gateway_chunk_state()
                .snapshot_key()
                .delete(snapshot_key(row.gateway_identity, &row.chunk_key));
        }
    }
    ctx.db
        .block_action_rate()
        .session_uuid()
        .delete(session_uuid);
    ctx.db
        .block_action_state()
        .session_uuid()
        .delete(session_uuid);
}

#[allow(clippy::too_many_arguments)]
#[spacetimedb::reducer]
pub fn update_world_interest(
    ctx: &ReducerContext,
    session_uuid: Uuid,
    chunk_x: i32,
    chunk_z: i32,
    radius: u8,
) -> Result<(), String> {
    let _ = foundation::require_gateway(ctx)?;
    let connection = ctx.connection_id().ok_or("Connection is required")?;
    let session = ctx
        .db
        .player_session()
        .session_uuid()
        .find(session_uuid)
        .ok_or("Session not found")?;
    if !foundation::owns_session(&session, ctx.sender(), connection) {
        return Err("World interest session belongs to another connection".into());
    }
    foundation::require_session_lease(ctx, &session)?;
    if session.phase != SessionPhase::Play {
        return Err("World interest requires an active Play session".into());
    }
    if radius > 2
        || i64::from(chunk_x).abs() + i64::from(radius) > i64::from(policy::WORLD_BORDER_BLOCK / 16)
        || i64::from(chunk_z).abs() + i64::from(radius) > i64::from(policy::WORLD_BORDER_BLOCK / 16)
    {
        return Err("World interest exceeds bounds".into());
    }
    let pose = ctx
        .db
        .player_pose()
        .session_uuid()
        .find(session_uuid)
        .ok_or("Player position is not initialized")?;
    let pose_chunk = (
        (pose.x.floor() as i32).div_euclid(16),
        (pose.z.floor() as i32).div_euclid(16),
    );
    if pose_chunk != (chunk_x, chunk_z) {
        return Err("World interest center must match the authoritative gateway pose".into());
    }
    replace_world_interest(ctx, session_uuid, ctx.sender(), chunk_x, chunk_z, radius)
}

pub(crate) fn replace_world_interest(
    ctx: &ReducerContext,
    session_uuid: Uuid,
    gateway: spacetimedb::Identity,
    chunk_x: i32,
    chunk_z: i32,
    radius: u8,
) -> Result<(), String> {
    if radius > 2
        || i64::from(chunk_x).abs() + i64::from(radius) > i64::from(policy::WORLD_BORDER_BLOCK / 16)
        || i64::from(chunk_z).abs() + i64::from(radius) > i64::from(policy::WORLD_BORDER_BLOCK / 16)
    {
        return Err("World interest radius exceeds bounds".into());
    }
    let wanted: std::collections::BTreeSet<_> = (-i32::from(radius)..=i32::from(radius))
        .flat_map(|dx| {
            (-i32::from(radius)..=i32::from(radius)).map(move |dz| (chunk_x + dx, chunk_z + dz))
        })
        .collect();
    let session_interests: Vec<_> = ctx
        .db
        .world_interest()
        .by_session()
        .filter(session_uuid)
        .collect();
    for row in session_interests {
        if row.world_id != WORLD_ID || !wanted.contains(&(row.chunk_x, row.chunk_z)) {
            ctx.db
                .world_interest()
                .interest_key()
                .delete(&row.interest_key);
            if !ctx
                .db
                .world_interest()
                .by_gateway()
                .filter(row.gateway_identity)
                .take(3_200)
                .any(|other| other.chunk_key == row.chunk_key)
            {
                ctx.db
                    .gateway_chunk_state()
                    .snapshot_key()
                    .delete(snapshot_key(row.gateway_identity, &row.chunk_key));
            }
        }
    }
    for (x, z) in wanted {
        let key = interest_key(gateway, session_uuid, x, z);
        if ctx.db.world_interest().interest_key().find(&key).is_none() {
            ctx.db.world_interest().insert(WorldInterest {
                interest_key: key,
                session_uuid,
                gateway_identity: gateway,
                world_id: WORLD_ID.into(),
                chunk_key: chunk_key(x * 16, z * 16),
                chunk_x: x,
                chunk_z: z,
            });
        }
        rebuild_chunk_snapshot(ctx, gateway, x, z)?;
    }
    Ok(())
}

#[allow(clippy::too_many_arguments)]
#[spacetimedb::reducer]
pub fn apply_block_action(
    ctx: &ReducerContext,
    session_uuid: Uuid,
    sequence: u64,
    x: i32,
    y: i32,
    z: i32,
    expected_state: u32,
    place: bool,
) -> Result<(), String> {
    if sequence == 0 {
        return Err("Block action sequence must be positive".into());
    }
    let _gateway = foundation::require_gateway(ctx)?;
    let connection = ctx.connection_id().ok_or("Connection is required")?;
    let session = ctx
        .db
        .player_session()
        .session_uuid()
        .find(session_uuid)
        .ok_or("Session not found")?;
    if !foundation::owns_session(&session, ctx.sender(), connection) {
        return Err("Block action session belongs to another connection".into());
    }
    foundation::require_session_lease(ctx, &session)?;
    if session.phase != SessionPhase::Play {
        return Err("Block actions require an active Play session".into());
    }
    if !ctx
        .db
        .world_interest()
        .by_session()
        .filter(session_uuid)
        .any(|interest| {
            interest.chunk_x == x.div_euclid(16) && interest.chunk_z == z.div_euclid(16)
        })
    {
        return Err("Block action is outside the session's subscribed world interest".into());
    }
    let pose = ctx
        .db
        .player_pose()
        .session_uuid()
        .find(session_uuid)
        .ok_or("Player position is unavailable")?;
    let eye = [pose.x, pose.y + 1.62, pose.z];
    let target = [f64::from(x) + 0.5, f64::from(y) + 0.5, f64::from(z) + 0.5];
    let reach_squared = match ctx
        .db
        .player_game_mode()
        .player_uuid()
        .find(session.player_uuid)
        .map(|r| r.mode)
        .unwrap_or(GameMode::Survival)
    {
        GameMode::Creative => 25.0,
        _ => 20.25,
    };
    let distance_squared =
        (eye[0] - target[0]).powi(2) + (eye[1] - target[1]).powi(2) + (eye[2] - target[2]).powi(2);
    if distance_squared > reach_squared {
        return Err("Block action exceeds server reach".into());
    }
    if !policy::valid_block_position(x, y, z) || !policy::supported_block_state(expected_state) {
        return Err("Block position or expected state is outside supported bounds".into());
    }
    let mode = ctx
        .db
        .player_game_mode()
        .player_uuid()
        .find(session.player_uuid)
        .map(|row| row.mode)
        .unwrap_or(GameMode::Survival);
    if mode == GameMode::Adventure {
        return Err(
            "Adventure block actions require supported CanDestroy/CanPlaceOn components".into(),
        );
    }
    let policy_mode = match mode {
        GameMode::Survival => WorldGameMode::Survival,
        GameMode::Creative => WorldGameMode::Creative,
        GameMode::Adventure => WorldGameMode::Adventure,
        GameMode::Spectator => WorldGameMode::Spectator,
    };
    let resulting_state = if place { 1 } else { 0 };
    policy::block_action_allowed(policy_mode, expected_state, resulting_state)
        .map_err(str::to_owned)?;
    if let Some(previous) = ctx
        .db
        .block_action_state()
        .session_uuid()
        .find(session_uuid)
    {
        if sequence == previous.sequence
            && (
                previous.x,
                previous.y,
                previous.z,
                previous.expected_state,
                previous.resulting_state,
            ) == (x, y, z, expected_state, resulting_state)
        {
            return Ok(());
        }
        if previous.sequence.checked_add(1) != Some(sequence) {
            return Err("Block action sequence mismatch".into());
        }
    } else if sequence != 1 {
        return Err("First block action sequence must be one".into());
    }
    let now = ctx.timestamp.to_micros_since_unix_epoch();
    if let Some(mut rate) = ctx.db.block_action_rate().session_uuid().find(session_uuid) {
        if now.saturating_sub(rate.window_started_micros) < ACTION_WINDOW {
            if rate.action_count >= MAX_ACTIONS_PER_WINDOW {
                return Err("Block action rate limit exceeded".into());
            }
            rate.action_count += 1;
        } else {
            rate.window_started_micros = now;
            rate.action_count = 1;
        }
        ctx.db.block_action_rate().session_uuid().update(rate);
    } else {
        ctx.db.block_action_rate().insert(BlockActionRate {
            session_uuid,
            window_started_micros: now,
            action_count: 1,
        });
    }
    let key = block_key(x, y, z);
    let existing = ctx.db.block_override().block_key().find(&key);
    let base_state = match y {
        62 => 88,
        63 => 1,
        64 => 9,
        _ => 0,
    };
    let current = existing
        .as_ref()
        .map(|row| row.state_id)
        .unwrap_or(base_state);
    if current != expected_state {
        return Err("Block state changed; resynchronize".into());
    }
    if place && current != 0 {
        return Err("Placement target is not empty".into());
    }
    if mode == GameMode::Survival && !place && !matches!(current, 1 | 9) {
        return Err("This block has no supported Survival drop".into());
    }
    let mut inventory = ctx
        .db
        .player_inventory()
        .player_uuid()
        .find(session.player_uuid)
        .ok_or("Player inventory is unavailable")?;
    let mut slots = model::decode_slots(&inventory.slots, 46).map_err(str::to_owned)?;
    if mode == GameMode::Survival {
        let held = 36 + usize::from(inventory.selected);
        if place {
            let stack = slots
                .get_mut(held)
                .ok_or("Selected inventory slot is invalid")?;
            if stack.item != model::item_id("minecraft:stone").map_err(str::to_owned)?
                || stack.count == 0
            {
                return Err("Survival placement requires held stone".into());
            }
            stack.count -= 1;
            if stack.count == 0 {
                *stack = Stack::empty();
            }
        } else {
            let dropped_item = match current {
                1 => model::item_id("minecraft:stone"),
                9 => model::item_id("minecraft:dirt"),
                _ => return Err("This block has no supported Survival drop".into()),
            }
            .map_err(str::to_owned)?;
            let target = if let Some(index) = slots
                .iter()
                .position(|stack| stack.item == dropped_item && stack.count < 64)
            {
                &mut slots[index]
            } else if let Some(index) = (9..45).find(|index| slots[*index].count == 0) {
                &mut slots[index]
            } else {
                return Err("Inventory is full; cannot collect block".into());
            };
            if target.count == 0 {
                target.item = dropped_item;
            }
            target.count += 1;
        }
    } else if mode == GameMode::Creative && place {
        let held = 36 + usize::from(inventory.selected);
        let stack = slots
            .get_mut(held)
            .ok_or("Selected inventory slot is invalid")?;
        if stack.item != model::item_id("minecraft:stone").map_err(str::to_owned)?
            || stack.count == 0
        {
            *stack = Stack {
                item: model::item_id("minecraft:stone").map_err(str::to_owned)?,
                count: 64,
                added: Vec::new(),
                removed: Vec::new(),
            };
        }
    }
    if matches!(mode, GameMode::Survival) || (mode == GameMode::Creative && place) {
        inventory.revision = inventory
            .revision
            .checked_add(1)
            .ok_or("Inventory revision exhausted")?;
        inventory.slots = model::encode_slots(&slots);
        ctx.db.player_inventory().player_uuid().update(inventory);
    }
    if let Some(row) = existing.as_ref()
        && row.revision == u64::MAX
    {
        return Err("Block revision exhausted".into());
    }
    let revision = existing.map(|row| row.revision + 1).unwrap_or(1);
    let row = BlockOverride {
        block_key: key,
        chunk_key: chunk_key(x, z),
        world_id: WORLD_ID.into(),
        chunk_x: x.div_euclid(16),
        chunk_z: z.div_euclid(16),
        x,
        y,
        z,
        state_id: resulting_state,
        revision,
        updated_by: session.player_uuid,
    };
    if ctx
        .db
        .block_override()
        .block_key()
        .find(&row.block_key)
        .is_some()
    {
        ctx.db.block_override().block_key().update(row);
    } else {
        let chunk_override_count = ctx
            .db
            .block_override()
            .by_chunk()
            .filter(&row.chunk_key)
            .count();
        if chunk_override_count >= MAX_CHUNK_OVERRIDES {
            return Err("Chunk override limit reached".into());
        }
        if ctx.db.block_override().count() >= MAX_WORLD_OVERRIDES {
            return Err("World override limit reached".into());
        }
        ctx.db.block_override().insert(row);
    }
    let chunk = chunk_key(x, z);
    let interested_chunks: Vec<_> = ctx
        .db
        .world_interest()
        .by_gateway()
        .filter(ctx.sender())
        .filter(|interest| interest.chunk_key == chunk)
        .map(|interest| (interest.chunk_x, interest.chunk_z))
        .collect();
    for (chunk_x, chunk_z) in interested_chunks {
        rebuild_chunk_snapshot(ctx, ctx.sender(), chunk_x, chunk_z)?;
    }
    let action = BlockActionState {
        session_uuid,
        gateway_identity: ctx.sender(),
        sequence,
        x,
        y,
        z,
        expected_state,
        resulting_state,
        revision,
    };
    if ctx
        .db
        .block_action_state()
        .session_uuid()
        .find(session_uuid)
        .is_some()
    {
        ctx.db.block_action_state().session_uuid().update(action);
    } else {
        ctx.db.block_action_state().insert(action);
    }
    Ok(())
}
