use spacetimedb::{
    ConnectionId, Identity, ReducerContext, ScheduleAt, SpacetimeType, Table, TimeDuration,
    Timestamp, Uuid, ViewContext,
};
use std::collections::HashSet;
use std::time::Duration;

use crate::policy::{
    IdentityKind, SessionPhase, accept_offline_profile, admin_allowed, advance_phase,
    enter_play_phase, offline_player_uuid, owns_connection, require_live_lease, wire_entity_id,
};
use crate::world::{player_game_mode, player_vitals};

const HEARTBEAT_BATCH_LIMIT: usize = 64;
const EXPIRY_BATCH_LIMIT: usize = 128;
const SESSION_LEASE_MICROS: i64 = 30_000_000;
const PLAYER_POSE_BATCH_LIMIT: usize = 64;
const GATEWAY_SESSION_LIMIT: usize = 128;
const PLAYER_CHAT_WINDOW_MICROS: i64 = 10_000_000;
const PLAYER_CHAT_WINDOW_LIMIT: u8 = 5;
const PLAYER_CHAT_TEXT_UTF16_LIMIT: usize = 256;
const GATEWAY_CHAT_HISTORY_LIMIT: usize = 128;

fn session_lease() -> TimeDuration {
    TimeDuration::from_micros(SESSION_LEASE_MICROS)
}

// Retain the original template's schema and reducers for existing databases.
#[spacetimedb::table(accessor = person, public)]
pub struct Person {
    name: String,
}

#[spacetimedb::table(accessor = administrator)]
pub struct Administrator {
    #[primary_key]
    identity: Identity,
}

#[spacetimedb::table(accessor = gateway)]
pub struct Gateway {
    #[primary_key]
    pub identity: Identity,
}

#[spacetimedb::table(accessor = player_profile)]
pub struct PlayerProfile {
    #[primary_key]
    pub player_uuid: Uuid,
    pub username: String,
    pub identity_kind: IdentityKind,
    pub first_seen: Timestamp,
    pub last_seen: Timestamp,
}

#[spacetimedb::table(
    accessor = player_session,
    index(accessor = by_gateway_connection, btree(columns = [gateway_identity, owner_connection]))
)]
pub struct PlayerSession {
    #[primary_key]
    pub session_uuid: Uuid,
    #[index(btree)]
    pub player_uuid: Uuid,
    #[index(btree)]
    pub gateway_identity: Identity,
    pub owner_connection: ConnectionId,
    pub phase: SessionPhase,
    pub connected_at: Timestamp,
    #[index(btree)]
    pub last_activity: Timestamp,
    #[default(0)]
    pub entity_id: i32,
}

#[spacetimedb::table(accessor = player_pose)]
pub struct PlayerPose {
    #[primary_key]
    pub session_uuid: Uuid,
    pub x: f64,
    pub y: f64,
    pub z: f64,
    pub yaw: f32,
    pub pitch: f32,
    pub on_ground: bool,
    pub revision: u64,
}

#[derive(SpacetimeType)]
pub struct PlayerPoseUpdate {
    pub session_uuid: Uuid,
    pub x: f64,
    pub y: f64,
    pub z: f64,
    pub yaw: f32,
    pub pitch: f32,
    pub on_ground: bool,
}

#[derive(SpacetimeType)]
pub struct GatewayWorldPlayer {
    pub session_uuid: Uuid,
    pub player_uuid: Uuid,
    pub username: String,
    pub entity_id: i32,
    pub x: f64,
    pub y: f64,
    pub z: f64,
    pub yaw: f32,
    pub pitch: f32,
    pub on_ground: bool,
    pub revision: u64,
}

#[spacetimedb::table(accessor = player_chat_rate)]
pub struct PlayerChatRate {
    #[primary_key]
    pub session_uuid: Uuid,
    pub window_started: Timestamp,
    pub sent_count: u8,
}

#[spacetimedb::table(accessor = player_chat_message)]
pub struct PlayerChatMessage {
    #[primary_key]
    #[auto_inc]
    pub message_id: u64,
    #[index(btree)]
    pub gateway_identity: Identity,
    pub gateway_sequence: u64,
    pub session_uuid: Uuid,
    pub player_uuid: Uuid,
    pub username: String,
    pub text: String,
    pub sent_at: Timestamp,
}

#[spacetimedb::table(accessor = gateway_chat_sequence)]
pub struct GatewayChatSequence {
    #[primary_key]
    pub gateway_identity: Identity,
    pub next_sequence: u64,
}

#[derive(SpacetimeType)]
pub struct GatewayChatMessage {
    pub message_id: u64,
    pub gateway_sequence: u64,
    pub session_uuid: Uuid,
    pub player_uuid: Uuid,
    pub username: String,
    pub text: String,
    pub verified: bool,
    pub sent_at: Timestamp,
}

#[derive(SpacetimeType, Copy, Clone, Debug, PartialEq, Eq)]
pub enum GatewayChatMode {
    Open,
    CommandsOnly,
    Hidden,
}

#[spacetimedb::table(accessor = gateway_chat_policy)]
pub struct GatewayChatPolicy {
    #[primary_key]
    pub gateway_identity: Identity,
    pub mode: GatewayChatMode,
}

#[spacetimedb::table(accessor = session_lease_cleanup, scheduled(cleanup_expired_sessions))]
pub struct SessionLeaseCleanup {
    #[primary_key]
    #[auto_inc]
    pub scheduled_id: u64,
    pub scheduled_at: ScheduleAt,
}

#[spacetimedb::table(accessor = session_lease_metrics)]
pub struct SessionLeaseMetrics {
    #[primary_key]
    pub key: u8,
    pub expired_total: u64,
    pub last_expired_at: Option<Timestamp>,
    /// ViewContext in SpacetimeDB 2.10 has no clock; this scheduled sample
    /// makes time-derived administrator diagnostics available with ≤5s lag.
    pub observed_at: Timestamp,
}

#[spacetimedb::table(accessor = session_entity)]
pub struct SessionEntity {
    #[primary_key]
    #[auto_inc]
    pub id: u64,
    #[unique]
    pub session_uuid: Uuid,
}

#[derive(SpacetimeType)]
pub struct AdminSessionDiagnostic {
    pub session_uuid: Uuid,
    pub player_uuid: Uuid,
    pub gateway_identity: Identity,
    pub phase: SessionPhase,
    pub age_micros: u64,
    /// Negative values indicate the scheduled cleanup has not removed an expired lease yet.
    pub lease_remaining_micros: i64,
}

#[derive(SpacetimeType)]
pub struct AdminLeaseDiagnostic {
    pub id: u8,
    pub session_rows: u64,
    pub expired_awaiting_cleanup: u64,
    pub expired_total: u64,
    pub last_expired_at: Option<Timestamp>,
}

#[spacetimedb::reducer(init)]
pub fn init(ctx: &ReducerContext) {
    // In 2.10.2 the host invokes init with the database owner's identity.
    ctx.db.administrator().insert(Administrator {
        identity: ctx.sender(),
    });
    ensure_lease_cleanup(ctx);
}

fn ensure_lease_cleanup(ctx: &ReducerContext) {
    if ctx.db.session_lease_cleanup().count() == 0 {
        ctx.db.session_lease_cleanup().insert(SessionLeaseCleanup {
            scheduled_id: 0,
            scheduled_at: ScheduleAt::Interval(Duration::from_secs(5).into()),
        });
    }
}

pub(crate) fn require_admin(ctx: &ReducerContext) -> Result<(), String> {
    let allowed = ctx
        .db
        .administrator()
        .identity()
        .find(ctx.sender())
        .is_some();
    // An existing template database does not rerun init on additive republish.
    // Only a publisher-selected identity can bootstrap its empty admin table.
    let bootstrap = if ctx.db.administrator().count() == 0 {
        option_env!("RUSTY_MINES_BOOTSTRAP_ADMIN").and_then(|value| value.parse::<Identity>().ok())
    } else {
        None
    };
    if !admin_allowed(ctx.sender(), allowed, bootstrap) {
        return Err("Administrator authorization required".into());
    }
    if !allowed {
        ctx.db.administrator().insert(Administrator {
            identity: ctx.sender(),
        });
    }
    Ok(())
}

#[spacetimedb::reducer]
pub fn register_gateway(ctx: &ReducerContext, identity: Identity) -> Result<(), String> {
    require_admin(ctx)?;
    // Additive republish does not rerun init. Authorized enrollment also
    // establishes the cleanup schedule for databases predating lease support.
    ensure_lease_cleanup(ctx);
    if ctx.db.gateway().identity().find(identity).is_none() {
        ctx.db.gateway().insert(Gateway { identity });
    }
    Ok(())
}

#[spacetimedb::reducer]
pub fn revoke_gateway(ctx: &ReducerContext, identity: Identity) -> Result<(), String> {
    require_admin(ctx)?;
    ctx.db.gateway().identity().delete(identity);
    let sessions: Vec<_> = ctx
        .db
        .player_session()
        .gateway_identity()
        .filter(identity)
        .collect();
    for session in sessions {
        remove_session(ctx, session);
    }
    Ok(())
}

pub(crate) fn require_gateway(ctx: &ReducerContext) -> Result<ConnectionId, String> {
    if ctx.db.gateway().identity().find(ctx.sender()).is_none() {
        return Err("Gateway is not authorized; ask the administrator to register_gateway".into());
    }
    ctx.connection_id()
        .ok_or_else(|| "Gateway connection required".into())
}

#[spacetimedb::reducer]
pub fn begin_offline_session(
    ctx: &ReducerContext,
    session_uuid: Uuid,
    username: String,
) -> Result<(), String> {
    let connection = require_gateway(ctx)?;
    let player_uuid = Uuid::from_u128(offline_player_uuid(&username)?);
    if session_uuid == Uuid::from_u128(0) {
        return Err("Session UUID must not be nil".into());
    }
    if let Some(existing) = ctx.db.player_session().session_uuid().find(session_uuid) {
        // A retry is safe only for the exact same immutable owner/player request.
        let same_profile = ctx
            .db
            .player_profile()
            .player_uuid()
            .find(existing.player_uuid)
            .is_some_and(|profile| profile.username == username);
        if existing.player_uuid == player_uuid
            && same_profile
            && owns_session(&existing, ctx.sender(), connection)
        {
            require_session_lease(ctx, &existing)?;
            return Ok(());
        }
        return Err("Session UUID already belongs to another request".into());
    }
    if ctx
        .db
        .player_session()
        .player_uuid()
        .filter(player_uuid)
        .next()
        .is_some()
    {
        return Err("Player already has an active session".into());
    }
    if ctx
        .db
        .player_session()
        .gateway_identity()
        .filter(ctx.sender())
        .count()
        >= GATEWAY_SESSION_LIMIT
    {
        return Err("Gateway session capacity reached".into());
    }
    match ctx.db.player_profile().player_uuid().find(player_uuid) {
        Some(mut profile) => {
            accept_offline_profile(&profile.identity_kind)?;
            profile.last_seen = ctx.timestamp;
            ctx.db.player_profile().player_uuid().update(profile);
        }
        None => {
            ctx.db.player_profile().insert(PlayerProfile {
                player_uuid,
                username,
                identity_kind: IdentityKind::Offline,
                first_seen: ctx.timestamp,
                last_seen: ctx.timestamp,
            });
        }
    }
    let entity = ctx
        .db
        .session_entity()
        .try_insert(SessionEntity {
            id: 0,
            session_uuid,
        })
        .map_err(|e| e.to_string())?;
    let entity_id = wire_entity_id(entity.id)?;
    ctx.db.player_session().insert(PlayerSession {
        session_uuid,
        player_uuid,
        gateway_identity: ctx.sender(),
        owner_connection: connection,
        phase: SessionPhase::Login,
        connected_at: ctx.timestamp,
        last_activity: ctx.timestamp,
        entity_id,
    });
    Ok(())
}

pub(crate) fn owns_session(
    session: &PlayerSession,
    sender: Identity,
    connection: ConnectionId,
) -> bool {
    owns_connection(
        session.gateway_identity,
        session.owner_connection,
        sender,
        connection,
    )
}

pub(crate) fn require_session_lease(
    ctx: &ReducerContext,
    session: &PlayerSession,
) -> Result<(), String> {
    require_live_lease(
        ctx.timestamp.to_micros_since_unix_epoch(),
        session.last_activity.to_micros_since_unix_epoch(),
        SESSION_LEASE_MICROS,
    )
}

#[spacetimedb::reducer]
pub fn advance_login_session(ctx: &ReducerContext, session_uuid: Uuid) -> Result<(), String> {
    let connection = require_gateway(ctx)?;
    let mut session = ctx
        .db
        .player_session()
        .session_uuid()
        .find(session_uuid)
        .ok_or_else(|| "Session not found".to_string())?;
    if !owns_session(&session, ctx.sender(), connection) {
        return Err("Session belongs to another gateway connection".into());
    }
    require_session_lease(ctx, &session)?;
    session.phase = advance_phase(&session.phase)?;
    session.last_activity = ctx.timestamp;
    touch_profile(ctx, session.player_uuid);
    ctx.db.player_session().session_uuid().update(session);
    Ok(())
}

#[spacetimedb::reducer]
pub fn enter_play_session(ctx: &ReducerContext, session_uuid: Uuid) -> Result<(), String> {
    let connection = require_gateway(ctx)?;
    let mut session = ctx
        .db
        .player_session()
        .session_uuid()
        .find(session_uuid)
        .ok_or_else(|| "Session not found".to_string())?;
    if !owns_session(&session, ctx.sender(), connection) {
        return Err("Session belongs to another gateway connection".into());
    }
    require_session_lease(ctx, &session)?;
    if session.entity_id <= 0 {
        return Err("Session predates world initialization; reconnect".into());
    }
    if ctx
        .db
        .player_game_mode()
        .player_uuid()
        .find(session.player_uuid)
        .is_none()
    {
        ctx.db
            .player_game_mode()
            .insert(crate::world::PlayerGameMode {
                player_uuid: session.player_uuid,
                mode: crate::world::GameMode::Survival,
            });
    }
    if ctx
        .db
        .player_vitals()
        .player_uuid()
        .find(session.player_uuid)
        .is_none()
    {
        ctx.db.player_vitals().insert(crate::world::PlayerVitals {
            player_uuid: session.player_uuid,
            health: 20,
            food: 20,
            dead: false,
            revision: 1,
            respawn_revision: None,
            connected_gateway: None,
            connected_session: None,
        });
    }
    crate::world::claim_player_state(ctx, session_uuid)?;
    session.phase = enter_play_phase(&session.phase)?;
    session.last_activity = ctx.timestamp;
    touch_profile(ctx, session.player_uuid);
    ctx.db.player_session().session_uuid().update(session);
    if ctx
        .db
        .player_pose()
        .session_uuid()
        .find(session_uuid)
        .is_none()
    {
        ctx.db.player_pose().insert(PlayerPose {
            session_uuid,
            x: 8.5,
            y: 65.0,
            z: 8.5,
            yaw: 0.0,
            pitch: 0.0,
            on_ground: false,
            revision: 1,
        });
    }
    let pose = ctx
        .db
        .player_pose()
        .session_uuid()
        .find(session_uuid)
        .ok_or("Player position is not initialized")?;
    crate::world::replace_world_interest(
        ctx,
        session_uuid,
        ctx.sender(),
        (pose.x.floor() as i32).div_euclid(16),
        (pose.z.floor() as i32).div_euclid(16),
        2,
    )?;
    Ok(())
}

#[spacetimedb::reducer]
pub fn update_player_states(
    ctx: &ReducerContext,
    updates: Vec<PlayerPoseUpdate>,
) -> Result<(), String> {
    let connection = require_gateway(ctx)?;
    if updates.is_empty() || updates.len() > PLAYER_POSE_BATCH_LIMIT {
        return Err("Player pose batch must contain 1-64 rows".into());
    }
    let mut seen = HashSet::with_capacity(updates.len());
    let mut poses = Vec::with_capacity(updates.len());
    for update in &updates {
        if !seen.insert(update.session_uuid) {
            return Err("Player pose batch contains duplicate sessions".into());
        }
        if !crate::policy::valid_world_pose(update.x, update.y, update.z, update.yaw, update.pitch)
        {
            return Err("Player pose is nonfinite or outside the current world bounds".into());
        }
        let session = ctx
            .db
            .player_session()
            .session_uuid()
            .find(update.session_uuid)
            .ok_or_else(|| "Player session not found".to_string())?;
        if !owns_session(&session, ctx.sender(), connection) {
            return Err("Player session belongs to another gateway connection".into());
        }
        require_session_lease(ctx, &session)?;
        if session.phase != SessionPhase::Play || session.entity_id <= 0 {
            return Err("Player pose requires an active Play session".into());
        }
        let pose = ctx
            .db
            .player_pose()
            .session_uuid()
            .find(update.session_uuid)
            .ok_or_else(|| "Player pose has not been initialized".to_string())?;
        pose.revision
            .checked_add(1)
            .ok_or_else(|| "Player pose revision exhausted".to_string())?;
        poses.push(pose);
    }
    // Interest rebuilding is bounded by the 5x5 window and validates every
    // target center before any pose mutation, so a border failure rejects the
    // whole batch atomically.
    for update in &updates {
        let next_x = (update.x.floor() as i32).div_euclid(16);
        let next_z = (update.z.floor() as i32).div_euclid(16);
        let old = ctx
            .db
            .player_pose()
            .session_uuid()
            .find(update.session_uuid)
            .ok_or("Player pose has not been initialized")?;
        if (
            (old.x.floor() as i32).div_euclid(16),
            (old.z.floor() as i32).div_euclid(16),
        ) != (next_x, next_z)
            && (i64::from(next_x).abs() + 2 > i64::from(crate::policy::WORLD_BORDER_BLOCK / 16)
                || i64::from(next_z).abs() + 2 > i64::from(crate::policy::WORLD_BORDER_BLOCK / 16))
        {
            return Err("Chunk interest would exceed the supported world border".into());
        }
    }
    // Validate the whole batch before the first write, so any rejected row
    // leaves every pose unchanged.
    let mut moved_chunks = Vec::new();
    for (update, mut pose) in updates.into_iter().zip(poses) {
        let previous_chunk = (
            (pose.x.floor() as i32).div_euclid(16),
            (pose.z.floor() as i32).div_euclid(16),
        );
        pose.x = update.x;
        pose.y = update.y;
        pose.z = update.z;
        pose.yaw = update.yaw;
        pose.pitch = update.pitch;
        pose.on_ground = update.on_ground;
        pose.revision += 1;
        let next_chunk = (
            (pose.x.floor() as i32).div_euclid(16),
            (pose.z.floor() as i32).div_euclid(16),
        );
        ctx.db.player_pose().session_uuid().update(pose);
        if previous_chunk != next_chunk {
            moved_chunks.push((update.session_uuid, next_chunk.0, next_chunk.1));
        }
    }
    for (session_uuid, chunk_x, chunk_z) in moved_chunks {
        crate::world::replace_world_interest(ctx, session_uuid, ctx.sender(), chunk_x, chunk_z, 2)?;
    }
    Ok(())
}

fn touch_profile(ctx: &ReducerContext, player_uuid: Uuid) {
    if let Some(mut profile) = ctx.db.player_profile().player_uuid().find(player_uuid) {
        profile.last_seen = ctx.timestamp;
        ctx.db.player_profile().player_uuid().update(profile);
    }
}

fn remove_session(ctx: &ReducerContext, session: PlayerSession) {
    crate::world::release_player_state(ctx, session.player_uuid, session.session_uuid);
    crate::inventory::remove_session(ctx, &session);
    crate::world::remove_session_state(ctx, session.session_uuid);
    ctx.db
        .player_pose()
        .session_uuid()
        .delete(session.session_uuid);
    ctx.db
        .player_chat_rate()
        .session_uuid()
        .delete(session.session_uuid);
    ctx.db
        .session_entity()
        .session_uuid()
        .delete(session.session_uuid);
    touch_profile(ctx, session.player_uuid);
    ctx.db
        .player_session()
        .session_uuid()
        .delete(session.session_uuid);
}

/// Routes explicitly unsigned offline text to the caller's registered gateway feed.
/// The private message table is exposed to clients only through gateway_chat_messages.
#[spacetimedb::reducer]
pub fn send_chat(ctx: &ReducerContext, session_uuid: Uuid, text: String) -> Result<(), String> {
    let connection = require_gateway(ctx)?;
    if text.is_empty()
        || text.encode_utf16().count() > PLAYER_CHAT_TEXT_UTF16_LIMIT
        || text.chars().any(char::is_control)
    {
        return Err("Chat text must be 1-256 UTF-16 units without control characters".into());
    }

    let session = ctx
        .db
        .player_session()
        .session_uuid()
        .find(session_uuid)
        .ok_or_else(|| "Player session not found".to_string())?;
    if ctx
        .db
        .gateway_chat_policy()
        .gateway_identity()
        .find(ctx.sender())
        .is_some_and(|policy| policy.mode != GatewayChatMode::Open)
    {
        return Err("Player chat is disabled for this gateway".into());
    }
    if !owns_session(&session, ctx.sender(), connection) {
        return Err("Player session belongs to another gateway connection".into());
    }
    require_session_lease(ctx, &session)?;
    if session.phase != SessionPhase::Play || session.entity_id <= 0 {
        return Err("Chat requires an active Play session".into());
    }
    let profile = ctx
        .db
        .player_profile()
        .player_uuid()
        .find(session.player_uuid)
        .ok_or_else(|| "Player profile not found".to_string())?;

    let now_micros = ctx.timestamp.to_micros_since_unix_epoch();
    let mut rate = ctx
        .db
        .player_chat_rate()
        .session_uuid()
        .find(session_uuid)
        .unwrap_or(PlayerChatRate {
            session_uuid,
            window_started: ctx.timestamp,
            sent_count: 0,
        });
    let window_micros = rate.window_started.to_micros_since_unix_epoch();
    if now_micros < window_micros {
        return Err("Chat rate window is ahead of server time".into());
    }
    if now_micros - window_micros >= PLAYER_CHAT_WINDOW_MICROS {
        rate.window_started = ctx.timestamp;
        rate.sent_count = 0;
    }
    if rate.sent_count >= PLAYER_CHAT_WINDOW_LIMIT {
        return Err("Chat rate limit exceeded".into());
    }

    let mut sequence = ctx
        .db
        .gateway_chat_sequence()
        .gateway_identity()
        .find(ctx.sender())
        .unwrap_or(GatewayChatSequence {
            gateway_identity: ctx.sender(),
            next_sequence: 1,
        });
    let current_sequence = sequence.next_sequence;
    sequence.next_sequence = sequence
        .next_sequence
        .checked_add(1)
        .ok_or_else(|| "Gateway chat sequence exhausted".to_string())?;

    // Keep only the latest bounded feed for each gateway. Sequence numbers are
    // explicit because auto-increment message IDs are not ordering guarantees.
    let mut history: Vec<_> = ctx
        .db
        .player_chat_message()
        .gateway_identity()
        .filter(ctx.sender())
        .collect();
    if history.len() >= GATEWAY_CHAT_HISTORY_LIMIT {
        history.sort_by_key(|message| message.gateway_sequence);
        for message in history.into_iter().take(1) {
            ctx.db
                .player_chat_message()
                .message_id()
                .delete(message.message_id);
        }
    }

    rate.sent_count += 1;
    if ctx
        .db
        .player_chat_rate()
        .session_uuid()
        .find(session_uuid)
        .is_some()
    {
        ctx.db.player_chat_rate().session_uuid().update(rate);
    } else {
        ctx.db.player_chat_rate().insert(rate);
    }
    if ctx
        .db
        .gateway_chat_sequence()
        .gateway_identity()
        .find(ctx.sender())
        .is_some()
    {
        ctx.db
            .gateway_chat_sequence()
            .gateway_identity()
            .update(sequence);
    } else {
        ctx.db.gateway_chat_sequence().insert(sequence);
    }
    ctx.db.player_chat_message().insert(PlayerChatMessage {
        message_id: 0,
        gateway_identity: ctx.sender(),
        gateway_sequence: current_sequence,
        session_uuid,
        player_uuid: session.player_uuid,
        username: profile.username,
        text,
        sent_at: ctx.timestamp,
    });
    Ok(())
}

#[spacetimedb::reducer]
pub fn set_gateway_chat_mode(
    ctx: &ReducerContext,
    gateway_identity: Identity,
    mode: GatewayChatMode,
) -> Result<(), String> {
    require_admin(ctx)?;
    if ctx.db.gateway().identity().find(gateway_identity).is_none() {
        return Err("Gateway is not registered".into());
    }
    let policy = GatewayChatPolicy {
        gateway_identity,
        mode,
    };
    if ctx
        .db
        .gateway_chat_policy()
        .gateway_identity()
        .find(gateway_identity)
        .is_some()
    {
        ctx.db
            .gateway_chat_policy()
            .gateway_identity()
            .update(policy);
    } else {
        ctx.db.gateway_chat_policy().insert(policy);
    }
    // Purge retained rows on policy changes so hidden history cannot reappear.
    let messages: Vec<_> = ctx
        .db
        .player_chat_message()
        .gateway_identity()
        .filter(gateway_identity)
        .collect();
    for message in messages {
        ctx.db
            .player_chat_message()
            .message_id()
            .delete(message.message_id);
    }
    Ok(())
}

#[spacetimedb::reducer]
pub fn heartbeat_sessions(ctx: &ReducerContext, session_uuids: Vec<Uuid>) -> Result<(), String> {
    let connection = require_gateway(ctx)?;
    if session_uuids.is_empty() {
        return Err("At least one session is required".into());
    }
    if session_uuids.len() > HEARTBEAT_BATCH_LIMIT {
        return Err("Heartbeat batch is too large".into());
    }

    let mut sessions = Vec::with_capacity(session_uuids.len());
    for session_uuid in session_uuids {
        let session = ctx
            .db
            .player_session()
            .session_uuid()
            .find(session_uuid)
            .ok_or_else(|| "Session not found".to_string())?;
        if !owns_session(&session, ctx.sender(), connection) {
            return Err("Session belongs to another gateway connection".into());
        }
        require_session_lease(ctx, &session)?;
        sessions.push(session);
    }

    for mut session in sessions {
        session.last_activity = ctx.timestamp;
        ctx.db.player_session().session_uuid().update(session);
    }
    Ok(())
}

#[spacetimedb::reducer]
pub fn cleanup_expired_sessions(ctx: &ReducerContext, _timer: SessionLeaseCleanup) {
    let cutoff = ctx.timestamp - session_lease();
    let expired: Vec<_> = ctx
        .db
        .player_session()
        .last_activity()
        .filter(..=cutoff)
        .take(EXPIRY_BATCH_LIMIT)
        .collect();
    let mut removed = 0u64;
    for session in expired {
        if session.last_activity + session_lease() <= ctx.timestamp {
            remove_session(ctx, session);
            removed = removed.saturating_add(1);
        }
    }
    if let Some(mut metrics) = ctx.db.session_lease_metrics().key().find(0) {
        metrics.expired_total = metrics.expired_total.saturating_add(removed);
        if removed > 0 {
            metrics.last_expired_at = Some(ctx.timestamp);
        }
        metrics.observed_at = ctx.timestamp;
        ctx.db.session_lease_metrics().key().update(metrics);
    } else {
        ctx.db.session_lease_metrics().insert(SessionLeaseMetrics {
            key: 0,
            expired_total: removed,
            last_expired_at: (removed > 0).then_some(ctx.timestamp),
            observed_at: ctx.timestamp,
        });
    }
}

#[spacetimedb::reducer]
pub fn end_session(ctx: &ReducerContext, session_uuid: Uuid) -> Result<(), String> {
    let connection = require_gateway(ctx)?;
    if let Some(session) = ctx.db.player_session().session_uuid().find(session_uuid) {
        if !owns_session(&session, ctx.sender(), connection) {
            return Err("Session belongs to another gateway connection".into());
        }
        remove_session(ctx, session);
    }
    Ok(())
}

#[spacetimedb::reducer(client_connected)]
pub fn identity_connected(_ctx: &ReducerContext) {}

#[spacetimedb::reducer(client_disconnected)]
pub fn identity_disconnected(ctx: &ReducerContext) {
    if let Some(connection) = ctx.connection_id() {
        let sessions: Vec<_> = ctx
            .db
            .player_session()
            .by_gateway_connection()
            .filter((ctx.sender(), connection))
            .collect();
        for session in sessions {
            remove_session(ctx, session);
        }
    }
}

#[spacetimedb::view(accessor = my_gateway, public, primary_key = identity)]
pub fn my_gateway(ctx: &ViewContext) -> Option<Gateway> {
    ctx.db.gateway().identity().find(ctx.sender())
}

#[spacetimedb::view(accessor = gateway_sessions, public, primary_key = session_uuid)]
pub fn gateway_sessions(ctx: &ViewContext) -> Vec<PlayerSession> {
    if ctx.db.gateway().identity().find(ctx.sender()).is_none() {
        return Vec::new();
    }
    ctx.db
        .player_session()
        .gateway_identity()
        .filter(ctx.sender())
        .collect()
}

#[spacetimedb::view(accessor = gateway_profiles, public, primary_key = player_uuid)]
pub fn gateway_profiles(ctx: &ViewContext) -> Vec<PlayerProfile> {
    if ctx.db.gateway().identity().find(ctx.sender()).is_none() {
        return Vec::new();
    }
    ctx.db
        .player_session()
        .gateway_identity()
        .filter(ctx.sender())
        .filter_map(|session| {
            ctx.db
                .player_profile()
                .player_uuid()
                .find(session.player_uuid)
        })
        .collect()
}

#[spacetimedb::view(accessor = gateway_world_players, public, primary_key = session_uuid)]
pub fn gateway_world_players(ctx: &ViewContext) -> Vec<GatewayWorldPlayer> {
    if ctx.db.gateway().identity().find(ctx.sender()).is_none() {
        return Vec::new();
    }
    ctx.db
        .player_session()
        .gateway_identity()
        .filter(ctx.sender())
        .filter_map(|session| {
            if session.phase != SessionPhase::Play || session.entity_id <= 0 {
                return None;
            }
            let pose = ctx
                .db
                .player_pose()
                .session_uuid()
                .find(session.session_uuid)?;
            let profile = ctx
                .db
                .player_profile()
                .player_uuid()
                .find(session.player_uuid)?;
            Some(GatewayWorldPlayer {
                session_uuid: session.session_uuid,
                player_uuid: session.player_uuid,
                username: profile.username,
                entity_id: session.entity_id,
                x: pose.x,
                y: pose.y,
                z: pose.z,
                yaw: pose.yaw,
                pitch: pose.pitch,
                on_ground: pose.on_ground,
                revision: pose.revision,
            })
        })
        .collect()
}

#[spacetimedb::view(
    accessor = gateway_chat_messages,
    public,
    primary_key = message_id
)]
pub fn gateway_chat_messages(ctx: &ViewContext) -> Vec<GatewayChatMessage> {
    if ctx.db.gateway().identity().find(ctx.sender()).is_none() {
        return Vec::new();
    }
    if ctx
        .db
        .gateway_chat_policy()
        .gateway_identity()
        .find(ctx.sender())
        .is_some_and(|policy| policy.mode != GatewayChatMode::Open)
    {
        return Vec::new();
    }
    let mut messages: Vec<_> = ctx
        .db
        .player_chat_message()
        .gateway_identity()
        .filter(ctx.sender())
        .map(|message| GatewayChatMessage {
            message_id: message.message_id,
            gateway_sequence: message.gateway_sequence,
            session_uuid: message.session_uuid,
            player_uuid: message.player_uuid,
            username: message.username,
            text: message.text,
            verified: false,
            sent_at: message.sent_at,
        })
        .collect();
    messages.sort_by_key(|message| message.gateway_sequence);
    messages
}

#[spacetimedb::view(
    accessor = admin_session_diagnostics,
    public,
    primary_key = session_uuid
)]
pub fn admin_session_diagnostics(ctx: &ViewContext) -> Vec<AdminSessionDiagnostic> {
    if ctx
        .db
        .administrator()
        .identity()
        .find(ctx.sender())
        .is_none()
    {
        return Vec::new();
    }
    let Some(metrics) = ctx.db.session_lease_metrics().key().find(0) else {
        return Vec::new();
    };
    let now = metrics.observed_at.to_micros_since_unix_epoch();
    ctx.db
        .player_session()
        .last_activity()
        .filter(..=metrics.observed_at)
        .take(EXPIRY_BATCH_LIMIT)
        .map(|session| {
            let connected_at = session.connected_at.to_micros_since_unix_epoch();
            let last_activity = session.last_activity.to_micros_since_unix_epoch();
            let (age_micros, lease_remaining_micros) = crate::policy::session_durations_micros(
                now,
                connected_at,
                last_activity,
                SESSION_LEASE_MICROS,
            );
            AdminSessionDiagnostic {
                session_uuid: session.session_uuid,
                player_uuid: session.player_uuid,
                gateway_identity: session.gateway_identity,
                phase: session.phase,
                age_micros,
                lease_remaining_micros,
            }
        })
        .collect()
}

#[spacetimedb::view(accessor = admin_lease_diagnostic, public, primary_key = id)]
pub fn admin_lease_diagnostic(ctx: &ViewContext) -> Option<AdminLeaseDiagnostic> {
    ctx.db.administrator().identity().find(ctx.sender())?;
    let metrics = ctx.db.session_lease_metrics().key().find(0);
    Some(AdminLeaseDiagnostic {
        id: 0,
        session_rows: ctx.db.player_session().count(),
        expired_awaiting_cleanup: metrics.as_ref().map_or(0, |value| {
            let cutoff = value.observed_at - session_lease();
            ctx.db
                .player_session()
                .last_activity()
                .filter(..=cutoff)
                .take(EXPIRY_BATCH_LIMIT)
                .count() as u64
        }),
        expired_total: metrics.as_ref().map_or(0, |value| value.expired_total),
        last_expired_at: metrics.and_then(|value| value.last_expired_at),
    })
}

#[spacetimedb::reducer]
pub fn add(ctx: &ReducerContext, name: String) {
    ctx.db.person().insert(Person { name });
}

#[spacetimedb::reducer]
pub fn say_hello(ctx: &ReducerContext) {
    for person in ctx.db.person().iter() {
        log::info!("Hello, {}!", person.name);
    }
    log::info!("Hello, World!");
}
