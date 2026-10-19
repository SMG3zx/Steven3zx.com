macro_rules! console_event {
    ($($arg:tt)*) => { crate::console_log(format!($($arg)*)) };
}

use crate::data_type::Intent;
use crate::gateway::SessionStore;
use crate::metrics::{CountingIo, Traffic};
use crate::packets::{configuration, login, play, status};
use crate::packets::{parse_packet, HandshakePacket, Packet};
use login::{Clientbound, GameProfile, LoginSuccess, Serverbound};
use std::io::{self, BufReader, Read, Write};
use std::net::{Shutdown, SocketAddr, TcpStream};
use std::sync::Arc;
use std::time::{Duration, Instant};

const LOGIN_TIMEOUT: Duration = Duration::from_secs(5);
const CONFIGURATION_TIMEOUT: Duration = Duration::from_secs(30);
const KEEPALIVE_INTERVAL: Duration = Duration::from_secs(5);
const KEEPALIVE_TIMEOUT: Duration = Duration::from_secs(15);
const READ_POLL: Duration = Duration::from_millis(250);
const SERVER_TICK: Duration = Duration::from_millis(50);

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ConnectionState {
    Handshaking,
    Status,
    Login,
    Configuration,
    Play,
    Closed,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum LoginPhase {
    WaitingStart,
    WaitingAcknowledged,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ConfigurationPhase {
    WaitingKnownPacks,
    WaitingFinish,
}

#[derive(Clone, Copy, Debug, PartialEq)]
enum TeleportKind {
    Initial,
    Correction,
}

#[derive(Clone, Copy, Debug)]
struct PendingTeleport {
    kind: TeleportKind,
    pose: play::SynchronizePlayerPosition,
    deadline: Instant,
}

struct ClientSession {
    traffic: Arc<Traffic>,
    peer_addr: SocketAddr,
    pending: Vec<u8>,
    compression_threshold: Option<i32>,
    state: ConnectionState,
    protocol_version: Option<i32>,
    transferred: bool,
    status_requested: bool,
    login_phase: LoginPhase,
    backend: Arc<dyn SessionStore>,
    backend_session: Option<uuid::Uuid>,
    entity_id: Option<i32>,
    controls: Arc<crate::players::Registry>,
    control: Option<crate::players::Guard>,
    kick_ack: Option<std::sync::mpsc::Sender<Result<(), String>>>,
    configuration_phase: ConfigurationPhase,
    client_information: Option<configuration::ClientInformation>,
    client_brand: Option<String>,
    deadline: Instant,
    configuration_started: Option<Instant>,
    next_keepalive: Instant,
    keepalive: Option<(i64, Instant)>,
    keepalive_sent: Option<Instant>,
    teleport_confirmed: bool,
    teleport_id: i32,
    pending_teleport: Option<PendingTeleport>,
    loading_ticks: u8,
    loading_started: Option<Instant>,
    on_ground: bool,
    clock: Arc<dyn Fn() -> Instant + Send + Sync>,
    input_flags: u8,
    held_slot: i16,
    sprinting: bool,
    inventory_notice: bool,
    inventory_snapshot: Option<crate::module_bindings::InventorySnapshot>,
    inventory_pending: Option<PendingInventory>,
    inventory_resync: bool,
    inventory_deferred_close: Option<i32>,
    inventory_reopen_menu: bool,
    player_loaded: bool,
    play_confirmed: bool,
    player_position: [f64; 3],
    player_rotation: [f32; 2],
    vertical_velocity: f64,
    simulation_at: Instant,
    next_food_drain: Instant,
    flying: bool,
    chunk_radius: i32,
    streamed_radius: i32,
    chunk_center: (i32, i32),
    loaded_chunks: std::collections::BTreeSet<(i32, i32)>,
    pending_chunk_center: Option<(i32, i32)>,
    next_chunk_batch: Instant,
    replicated_players: std::collections::HashMap<uuid::Uuid, play::PlayerEntity>,
    chat_pending: Option<PendingChat>,
    block_pending: Option<PendingBlockAction>,
    block_sequence: u64,
    chunk_revisions: std::collections::HashMap<(i32, i32), u64>,
    health: f32,
    food: i32,
    dead: bool,
    game_mode: u8,
    pending_mode: Option<(std::sync::mpsc::Receiver<io::Result<()>>, u8, Instant, bool)>,
    pending_player_state: Option<PendingPlayerState>,
}

struct PendingInventory {
    receiver: std::sync::mpsc::Receiver<io::Result<()>>,
    sequence: u64,
    deadline: Instant,
    accepted: bool,
    evidence: Option<play::InventoryClick>,
}

struct PendingChat {
    receiver: std::sync::mpsc::Receiver<io::Result<()>>,
    deadline: Instant,
}

struct PendingPlayerState {
    receiver: std::sync::mpsc::Receiver<io::Result<()>>,
    expected: crate::gateway::GatewayPlayerState,
    deadline: Instant,
    accepted: bool,
    respawn: bool,
}

struct PendingBlockAction {
    receiver: std::sync::mpsc::Receiver<io::Result<()>>,
    sequence: i32,
    position: play::BlockPosition,
    expected_state: u32,
    place: bool,
    deadline: Instant,
    accepted: bool,
}

#[cfg(test)]
mod world_action_tests {
    use super::*;

    #[test]
    fn chunk_override_snapshot_decoder_enforces_bounds_and_coordinates() {
        let mut bytes = Vec::new();
        for value in [8_i32, 63, 8, 0] {
            bytes.extend(value.to_le_bytes());
        }
        let snapshot = crate::module_bindings::GatewayChunkSnapshot {
            snapshot_key: String::new(),
            world_id: String::new(),
            chunk_x: 0,
            chunk_z: 0,
            encoded_overrides: bytes,
            revision: 1,
        };
        assert_eq!(
            decode_block_overrides(&snapshot, 0, 0).unwrap()[&(8, 63, 8)],
            0
        );
        assert!(decode_block_overrides(&snapshot, 1, 0).is_err());
    }
}

fn chunk_window(center: (i32, i32), radius: i32) -> std::collections::BTreeSet<(i32, i32)> {
    let mut chunks = std::collections::BTreeSet::new();
    for z in center.1.saturating_sub(radius)..=center.1.saturating_add(radius) {
        for x in center.0.saturating_sub(radius)..=center.0.saturating_add(radius) {
            chunks.insert((x, z));
        }
    }
    chunks
}

fn decode_block_overrides(
    snapshot: &crate::module_bindings::GatewayChunkSnapshot,
    chunk_x: i32,
    chunk_z: i32,
) -> io::Result<std::collections::BTreeMap<(i32, i32, i32), i32>> {
    if snapshot.chunk_x != chunk_x
        || snapshot.chunk_z != chunk_z
        || snapshot.encoded_overrides.len() > 2048
        || !snapshot.encoded_overrides.len().is_multiple_of(16)
    {
        return Err(invalid_data("invalid bounded chunk override snapshot"));
    }
    let mut rows = std::collections::BTreeMap::new();
    let (chunks, _) = snapshot.encoded_overrides.as_chunks::<16>();
    for bytes in chunks {
        let x = i32::from_le_bytes(bytes[0..4].try_into().unwrap());
        let y = i32::from_le_bytes(bytes[4..8].try_into().unwrap());
        let z = i32::from_le_bytes(bytes[8..12].try_into().unwrap());
        let state = i32::from_le_bytes(bytes[12..16].try_into().unwrap());
        if x.div_euclid(16) != chunk_x
            || z.div_euclid(16) != chunk_z
            || !(-64..=319).contains(&y)
            || !(0..=u32::MAX as i64).contains(&i64::from(state))
            || rows.insert((x, y, z), state).is_some()
        {
            return Err(invalid_data(
                "invalid coordinate in chunk override snapshot",
            ));
        }
    }
    Ok(rows)
}

impl Drop for ClientSession {
    fn drop(&mut self) {
        self.control.take();
        if let Some(ack) = self.kick_ack.take() {
            let _ = ack.send(Err("Disconnect did not complete".into()));
        }
        if let Some(session) = self.backend_session.take() {
            self.backend.end(session);
        }
    }
}

impl ClientSession {
    fn new(peer_addr: SocketAddr, traffic: Arc<Traffic>, backend: Arc<dyn SessionStore>) -> Self {
        Self {
            traffic,
            peer_addr,
            pending: Vec::new(),
            compression_threshold: None,
            state: ConnectionState::Handshaking,
            protocol_version: None,
            transferred: false,
            status_requested: false,
            login_phase: LoginPhase::WaitingStart,
            backend,
            backend_session: None,
            entity_id: None,
            controls: Arc::new(crate::players::Registry::default()),
            control: None,
            kick_ack: None,
            configuration_phase: ConfigurationPhase::WaitingKnownPacks,
            client_information: None,
            client_brand: None,
            deadline: Instant::now() + LOGIN_TIMEOUT,
            configuration_started: None,
            next_keepalive: Instant::now() + KEEPALIVE_INTERVAL,
            keepalive: None,
            keepalive_sent: None,
            teleport_confirmed: false,
            teleport_id: 0,
            pending_teleport: None,
            loading_ticks: 0,
            loading_started: None,
            on_ground: false,
            clock: Arc::new(Instant::now),
            input_flags: 0,
            held_slot: 0,
            sprinting: false,
            inventory_notice: false,
            inventory_snapshot: None,
            inventory_pending: None,
            inventory_resync: false,
            inventory_deferred_close: None,
            inventory_reopen_menu: false,
            player_loaded: false,
            play_confirmed: false,
            player_position: [8.5, 65.0, 8.5],
            player_rotation: [0.0; 2],
            vertical_velocity: 0.0,
            simulation_at: Instant::now(),
            next_food_drain: Instant::now() + Duration::from_secs(5),
            flying: false,
            chunk_radius: rusty_mines::vanilla_world::MAX_RENDER_DISTANCE,
            streamed_radius: rusty_mines::vanilla_world::MAX_RENDER_DISTANCE,
            chunk_center: (0, 0),
            loaded_chunks: std::collections::BTreeSet::new(),
            pending_chunk_center: None,
            next_chunk_batch: Instant::now() + SERVER_TICK,
            replicated_players: std::collections::HashMap::new(),
            chat_pending: None,
            block_pending: None,
            block_sequence: 0,
            chunk_revisions: std::collections::HashMap::new(),
            health: 20.0,
            food: 20,
            dead: false,
            game_mode: 0,
            pending_mode: None,
            pending_player_state: None,
        }
    }

    fn receive(&mut self, bytes: &[u8], writer: &mut impl Write) -> io::Result<()> {
        if self.state == ConnectionState::Closed {
            return Ok(());
        }
        // A three-byte outer length bounds a frame; allow one TCP read of overlap.
        if self.pending.len().saturating_add(bytes.len()) > 0x1f_ffff + 3 + 1024 {
            if self.state == ConnectionState::Login {
                return self.disconnect_login(writer, "Login receive buffer limit exceeded");
            }
            if self.state == ConnectionState::Configuration {
                return self.disconnect_configuration(
                    writer,
                    "Configuration receive buffer limit exceeded",
                );
            }
            if self.state == ConnectionState::Play {
                return self.disconnect_play(writer, "Play receive buffer limit exceeded");
            }
            return Err(invalid_data("receive buffer limit exceeded"));
        }
        self.pending.extend_from_slice(bytes);
        let mut consumed = 0;

        loop {
            let parsed = parse_packet(&self.pending[consumed..], self.compression_threshold, true);
            let (packet, packet_bytes) = match parsed {
                Ok(Some(packet)) => packet,
                Ok(None) => break,
                Err(error) if self.state == ConnectionState::Login => {
                    return self
                        .disconnect_login(writer, &format!("Invalid login frame: {error:?}"));
                }
                Err(_) if self.state == ConnectionState::Configuration => {
                    return self.disconnect_configuration(writer, "Invalid Configuration frame");
                }
                Err(_) if self.state == ConnectionState::Play => {
                    return self.disconnect_play(writer, "Invalid Play frame");
                }
                Err(error) => {
                    return Err(invalid_data(format!("invalid Minecraft packet: {error:?}")))
                }
            };
            self.poll((self.clock)(), writer)?;
            if self.state == ConnectionState::Closed {
                self.pending.clear();
                return Ok(());
            }
            self.traffic.inbound_packet();
            self.handle_packet(&packet, writer)?;
            consumed += packet_bytes;
            if self.state == ConnectionState::Closed {
                self.pending.clear();
                return Ok(());
            }
        }

        // Retain the unfinished packet for the next TCP read.
        self.pending.drain(..consumed);
        Ok(())
    }

    fn handle_packet(&mut self, packet: &Packet, writer: &mut impl Write) -> io::Result<()> {
        console_event!(
            "[{}] Packet ID: 0x{:02X}, data: {} bytes",
            self.peer_addr,
            packet.id,
            packet.data.len(),
        );

        match (self.state, packet.id) {
            (ConnectionState::Handshaking, 0x00) => self.handle_handshake(&packet.data, writer),
            (ConnectionState::Handshaking, _) => Err(invalid_data("expected handshake packet")),
            (ConnectionState::Status, 0x00) => {
                status::validate_request(&packet.data)?;

                if self.status_requested {
                    return Err(invalid_data("duplicate status request"));
                }
                status::write_response(writer)?;
                self.status_requested = true;
                Ok(())
            }
            (ConnectionState::Status, 0x01) => {
                // Latency-only probes may ping without first requesting status.
                status::write_pong(writer, &packet.data)?;
                self.state = ConnectionState::Closed;
                Ok(())
            }
            (ConnectionState::Login, _) => self.handle_login(packet, writer),
            (ConnectionState::Configuration, _) => self.handle_configuration(packet, writer),
            (ConnectionState::Play, _) => self.handle_play(packet, writer),
            _ => Err(invalid_data(format!(
                "unhandled packet 0x{:02X} in state {:?}",
                packet.id, self.state,
            ))),
        }
    }

    fn disconnect_login(&mut self, writer: &mut impl Write, reason: &str) -> io::Result<()> {
        self.state = ConnectionState::Closed;
        self.control.take();
        self.pending.clear();
        Clientbound::Disconnect {
            reason: serde_json::json!({"text": reason}).to_string(),
        }
        .write(writer)
    }

    fn disconnect_configuration(
        &mut self,
        writer: &mut impl Write,
        reason: &str,
    ) -> io::Result<()> {
        self.state = ConnectionState::Closed;
        self.control.take();
        self.pending.clear();
        let nbt = configuration::NetworkNbt::text(reason).map_err(invalid_data)?;
        configuration::Clientbound::Disconnect(nbt).write(writer)
    }

    fn handle_login(&mut self, packet: &Packet, writer: &mut impl Write) -> io::Result<()> {
        let decoded = match Serverbound::decode(packet.id, &packet.data) {
            Ok(packet) => packet,
            Err(error) => return self.disconnect_login(writer, error),
        };
        match (self.login_phase, decoded) {
            (LoginPhase::WaitingStart, Serverbound::Start(start)) => {
                let uuid = match login::offline_uuid(&start.name) {
                    Ok(uuid) => uuid,
                    Err(error) => return self.disconnect_login(writer, error),
                };
                let session_id = uuid::Uuid::new_v4();
                self.control = Some(self.controls.register(session_id, start.name.clone()));
                if let Err(error) = self.backend.begin(session_id, &start.name) {
                    console_event!("Backend login rejected: {error}");
                    return self.disconnect_login(
                        writer,
                        "Login unavailable: backend rejected or did not confirm the session",
                    );
                }
                // From here, every exit (including a failed write) owns cleanup.
                self.backend_session = Some(session_id);
                self.poll((self.clock)(), writer)?;
                if self.state == ConnectionState::Closed {
                    return Ok(());
                }
                // Offline mode deliberately ignores the client's claimed UUID.
                Clientbound::Success(LoginSuccess {
                    profile: GameProfile {
                        uuid,
                        username: start.name,
                        properties: Vec::new(),
                    },
                    session_id,
                })
                .write(writer)?;
                self.login_phase = LoginPhase::WaitingAcknowledged;
                Ok(())
            }
            (LoginPhase::WaitingAcknowledged, Serverbound::Acknowledged) => {
                // The client switches wire state as soon as it sends this ACK.
                self.state = ConnectionState::Configuration;
                let now = (self.clock)();
                self.configuration_started = Some(now);
                self.deadline = now + CONFIGURATION_TIMEOUT;
                self.next_keepalive = now + KEEPALIVE_INTERVAL;
                match self
                    .backend
                    .configure(self.backend_session.expect("login session created"))
                {
                    Ok(entity_id) if entity_id > 0 => self.entity_id = Some(entity_id),
                    Ok(_) => {
                        return self.disconnect_configuration(
                            writer,
                            "Backend supplied an invalid entity ID",
                        )
                    }
                    Err(error) => {
                        console_event!("Backend configuration rejected: {error}");
                        return self.disconnect_configuration(
                            writer,
                            "Configuration unavailable: backend did not confirm the session",
                        );
                    }
                }
                self.poll((self.clock)(), writer)?;
                if self.state == ConnectionState::Closed {
                    return Ok(());
                }
                let vanilla =
                    rusty_mines::vanilla_configuration::vanilla().map_err(invalid_data)?;
                configuration::brand().write(writer)?;
                configuration::Clientbound::FeatureFlags(vec!["minecraft:vanilla".into()])
                    .write(writer)?;
                configuration::Clientbound::KnownPacks(vec![vanilla.core.clone()]).write(writer)
            }
            _ => self.disconnect_login(writer, "Unexpected or duplicate login packet"),
        }
    }

    fn handle_configuration(&mut self, packet: &Packet, writer: &mut impl Write) -> io::Result<()> {
        let decoded = match configuration::Serverbound::decode(packet.id, &packet.data) {
            Ok(packet) => packet,
            Err(error) => return self.disconnect_configuration(writer, error),
        };
        match decoded {
            configuration::Serverbound::ClientInformation(info) => {
                self.chunk_radius = i32::from(info.view_distance)
                    .clamp(1, rusty_mines::vanilla_world::MAX_RENDER_DISTANCE);
                self.streamed_radius = self.chunk_radius;
                self.client_information = Some(info);
                Ok(())
            }
            configuration::Serverbound::PluginMessage { channel, data } => {
                if channel == "minecraft:brand" || channel == "brand" {
                    match configuration::decode_brand(&data) {
                        Ok(brand) => self.client_brand = Some(brand),
                        Err(error) => return self.disconnect_configuration(writer, error),
                    }
                }
                Ok(())
            }
            configuration::Serverbound::KeepAlive(id) => {
                if self.keepalive.is_some_and(|(expected, _)| expected == id) {
                    self.record_keepalive_rtt();
                    self.keepalive = None;
                    Ok(())
                } else {
                    self.disconnect_configuration(
                        writer,
                        "Unexpected Configuration keepalive response",
                    )
                }
            }
            configuration::Serverbound::KnownPacks(packs)
                if self.configuration_phase == ConfigurationPhase::WaitingKnownPacks =>
            {
                let vanilla =
                    rusty_mines::vanilla_configuration::vanilla().map_err(invalid_data)?;
                if packs != [vanilla.core.clone()] {
                    return self.disconnect_configuration(
                        writer,
                        "Unsupported known packs: minecraft/core/26.3 is required",
                    );
                }
                for registry in &vanilla.registries {
                    self.poll((self.clock)(), writer)?;
                    if self.state == ConnectionState::Closed {
                        return Ok(());
                    }
                    registry.write(writer)?;
                }
                vanilla.tags.write(writer)?;
                self.poll((self.clock)(), writer)?;
                if self.state == ConnectionState::Closed {
                    return Ok(());
                }
                configuration::Clientbound::Finish.write(writer)?;
                self.configuration_phase = ConfigurationPhase::WaitingFinish;
                Ok(())
            }
            configuration::Serverbound::FinishAcknowledged
                if self.configuration_phase == ConfigurationPhase::WaitingFinish =>
            {
                let world = rusty_mines::vanilla_world::world().map_err(invalid_data)?;
                let entity = self
                    .entity_id
                    .filter(|id| *id > 0)
                    .ok_or_else(|| io::Error::other("Missing confirmed backend entity ID"))?;
                self.state = ConnectionState::Play;
                self.reset_loading();
                self.deadline = (self.clock)() + CONFIGURATION_TIMEOUT;
                self.keepalive = None;
                self.next_keepalive = (self.clock)() + KEEPALIVE_INTERVAL;
                play::write_login(writer, &world.login, entity)?;
                self.chunk_center = (0, 0);
                self.pending_chunk_center = None;
                self.loaded_chunks = chunk_window(self.chunk_center, self.chunk_radius);
                self.streamed_radius = self.chunk_radius;
                self.next_chunk_batch = (self.clock)() + SERVER_TICK;
                let initialization = world
                    .initialization_for(self.chunk_center.0, self.chunk_center.1, self.chunk_radius)
                    .map_err(invalid_data)?;
                for packet in &initialization {
                    self.poll((self.clock)(), writer)?;
                    if self.state == ConnectionState::Closed {
                        return Ok(());
                    }
                    if packet.first() == Some(&play::POSITION) {
                        self.send_teleport(TeleportKind::Initial, writer)?;
                    } else {
                        play::write_official_packet(writer, packet)?;
                    }
                }
                Ok(())
            }
            _ => self
                .disconnect_configuration(writer, "Unexpected or duplicate Configuration packet"),
        }
    }

    fn disconnect_play(&mut self, writer: &mut impl Write, reason: &str) -> io::Result<()> {
        self.pending_teleport = None;
        self.inventory_pending = None;
        self.inventory_deferred_close = None;
        self.pending_player_state = None;
        self.block_pending = None;
        self.pending_mode = None;
        self.state = ConnectionState::Closed;
        self.control.take();
        self.pending.clear();
        let nbt = configuration::NetworkNbt::text(reason).map_err(invalid_data)?;
        play::Clientbound::Disconnect(nbt).write(writer)
    }

    fn handle_play(&mut self, packet: &Packet, writer: &mut impl Write) -> io::Result<()> {
        let decoded = play::Serverbound::decode(packet.id, &packet.data);
        let valid = match decoded {
            Ok(play::Serverbound::TeleportAcknowledged(ack)) => {
                if let Some(pending) = self.pending_teleport {
                    if ack.id == pending.pose.id
                        && ack.position == pending.pose.position.map(f64::to_bits)
                        && ack.rotation == pending.pose.rotation.map(f32::to_bits)
                    {
                        if pending.kind == TeleportKind::Initial {
                            self.teleport_confirmed = true;
                        }
                        self.pending_teleport = None;
                        true
                    } else {
                        false
                    }
                } else {
                    false
                }
            }
            Ok(play::Serverbound::PlayerLoaded) => {
                if !self.player_loaded {
                    self.player_loaded = true;
                    true
                } else {
                    false
                }
            }
            Ok(play::Serverbound::ChatSession | play::Serverbound::ChunkBatchReceived) => true,
            // This packet describes client movement cadence, not server loading time.
            Ok(play::Serverbound::ClientTickEnd) => true,
            Ok(play::Serverbound::Abilities { flying }) => {
                self.flying = !self.dead && flying && matches!(self.game_mode, 1 | 3);
                self.send_mode_state(writer)?;
                true
            }
            Ok(play::Serverbound::KeepAlive(id)) => {
                if self.keepalive.is_some_and(|(expected, _)| id == expected) {
                    self.record_keepalive_rtt();
                    self.keepalive = None;
                    true
                } else {
                    false
                }
            }
            Ok(play::Serverbound::ClientInformation(info)) => {
                self.chunk_radius = i32::from(info.view_distance)
                    .clamp(1, rusty_mines::vanilla_world::MAX_RENDER_DISTANCE);
                self.pending_chunk_center = Some(self.chunk_center);
                self.client_information = Some(info);
                true
            }
            Ok(play::Serverbound::PluginMessage { channel, data }) => {
                if channel == "minecraft:brand" || channel == "brand" {
                    match configuration::decode_brand(&data) {
                        Ok(brand) => {
                            self.client_brand = Some(brand);
                            true
                        }
                        Err(_) => false,
                    }
                } else {
                    // Brand is the only supported Play plugin channel. Unknown
                    // channels are bounded by the codec and intentionally ignored.
                    true
                }
            }
            Ok(play::Serverbound::Movement(_)) if self.dead => true,
            Ok(play::Serverbound::Movement(movement)) if self.pending_teleport.is_some() => {
                let _ = movement;
                true
            }
            Ok(play::Serverbound::Movement(movement)) => {
                let old_position = self.player_position;
                let mut valid = true;
                let mut corrected = false;
                if let Some(position) = movement.position {
                    let border = f64::from(rusty_mines::vanilla_world::WORLD_BORDER)
                        - f64::from(rusty_mines::vanilla_world::MAX_RENDER_DISTANCE * 16);
                    valid = [0_usize, 2]
                        .into_iter()
                        .all(|axis| (position[axis] - old_position[axis]).abs() <= 16.0);
                    // The server owns vertical position. Only authorized flight may
                    // accept a client vertical coordinate; otherwise gravity advances it.
                    if self.flying && matches!(self.game_mode, 1 | 3) {
                        let delta = position[1] - old_position[1];
                        if delta.abs() > 16.0 {
                            valid = false;
                        }
                    }
                    if valid {
                        for axis in [0_usize, 2] {
                            self.player_position[axis] = position[axis].clamp(-border, border);
                            corrected |= self.player_position[axis] != position[axis];
                        }
                        if self.flying && matches!(self.game_mode, 1 | 3) {
                            self.player_position[1] = position[1].clamp(-64.0, 320.0);
                            corrected |= self.player_position[1] != position[1];
                            self.vertical_velocity = 0.0;
                        }
                        self.pending_chunk_center = Some((
                            (self.player_position[0].floor() as i32).div_euclid(16),
                            (self.player_position[2].floor() as i32).div_euclid(16),
                        ));
                    }
                }
                if valid {
                    // Client ground/collision bits are advisory only.
                    let _ = (movement.on_ground, movement.horizontal_collision);
                    if let Some(rotation) = movement.rotation {
                        self.player_rotation = rotation;
                    }
                    if let Err(error) = self.publish_authoritative_pose() {
                        if error.kind() != io::ErrorKind::Unsupported {
                            return self.disconnect_play(
                                writer,
                                "Backend player state unavailable; reconnect",
                            );
                        }
                    }
                    if !self.flying && self.game_mode != 3 {
                        self.simulate_vertical((self.clock)(), writer)?;
                    }
                    if corrected {
                        self.send_teleport(TeleportKind::Correction, writer)?;
                    }
                }
                if !valid {
                    self.send_teleport(TeleportKind::Correction, writer)?;
                }
                true
            }
            Ok(play::Serverbound::ChatMessage(message)) => {
                let Some(session) = self.backend_session.filter(|_| self.play_confirmed) else {
                    return self.disconnect_play(
                        writer,
                        "Player chat requires an active backend Play session",
                    );
                };
                if self.chat_pending.is_some() {
                    self.system_message(writer, "A player chat request is still pending.")?;
                } else {
                    match self.backend.chat_send(session, message) {
                        Ok(receiver) => {
                            self.chat_pending = Some(PendingChat {
                                receiver,
                                deadline: (self.clock)() + Duration::from_secs(3),
                            })
                        }
                        Err(error) => {
                            self.system_message(writer, &format!("Chat unavailable: {error}"))?
                        }
                    }
                }
                true
            }
            Ok(play::Serverbound::ChatCommand(command)) => {
                let parts: Vec<_> = command.split_whitespace().collect();
                if let ["storage", uuid] = parts.as_slice() {
                    if let Ok(uuid) = uuid::Uuid::parse_str(uuid) {
                        self.queue_inventory(3, uuid.as_bytes().to_vec(), None, writer)?;
                    } else {
                        self.system_message(writer, "Usage: /storage <authorized storage UUID>")?;
                    }
                } else if parts == ["inventory"] {
                    let menu = self
                        .inventory_snapshot
                        .as_ref()
                        .map_or(0, |snapshot| snapshot.menu_id);
                    let mut fields = Vec::new();
                    rusty_mines_inventory::wire::put_int(&mut fields, menu);
                    self.queue_inventory(4, fields, None, writer)?;
                } else if parts == ["help"] {
                    self.system_message(writer, "Commands: /help, /inventory, /storage <UUID>. Player chat is unsigned and may be disabled by the server.")?;
                } else {
                    self.system_message(writer, "Unknown command. Use /help.")?;
                }
                true
            }
            Ok(play::Serverbound::SignedChatMessage(_))
            | Ok(play::Serverbound::SignedChatCommand(_)) => {
                self.system_message(
                    writer,
                    "Signed chat is not verified or accepted; use ordinary unsigned chat.",
                )?;
                true
            }
            Ok(play::Serverbound::ChatAcknowledgement) => true,
            Ok(play::Serverbound::ClientStatus(0)) if self.dead => {
                if self.pending_player_state.is_none() {
                    let Some(session) = self.backend_session else {
                        if !self.backend.requires_persistent_player_state() {
                            self.respawn(
                                crate::gateway::GatewayPlayerState {
                                    mode: if self.game_mode == 2 {
                                        crate::module_bindings::GameMode::Adventure
                                    } else {
                                        crate::module_bindings::GameMode::Survival
                                    },
                                    health: 20,
                                    food: self.food.clamp(0, 20) as u8,
                                    dead: false,
                                    revision: 0,
                                    respawn_revision: Some(0),
                                    state_owner: None,
                                },
                                writer,
                            )?;
                            return Ok(());
                        }
                        return Err(io::Error::other("Respawn requires active backend Play"));
                    };
                    let request = match self.backend_session {
                        Some(session) => self.backend.player_respawn_request(session),
                        None => unreachable!("session was checked above"),
                    };
                    match request {
                        Ok(receiver) => {
                            let mut expected =
                                self.backend.player_state(session)?.ok_or_else(|| {
                                    io::Error::other(
                                        "Persistent player state unavailable; reconnect",
                                    )
                                })?;
                            expected.health = 20;
                            expected.dead = false;
                            expected.revision = expected.revision.saturating_add(1);
                            expected.respawn_revision = Some(expected.revision);
                            if expected.state_owner.is_none() {
                                self.system_message(writer, "Persistent player state ownership is unavailable; respawn refused.")?;
                                return Ok(());
                            }
                            if self.pending_player_state.is_some() {
                                return Ok(());
                            }
                            self.pending_player_state = Some(PendingPlayerState {
                                receiver,
                                expected,
                                deadline: (self.clock)() + Duration::from_secs(3),
                                accepted: false,
                                respawn: true,
                            });
                        }
                        Err(error) => {
                            self.system_message(writer, &format!("Respawn unavailable: {error}"))?;
                        }
                    }
                }
                true
            }
            Ok(play::Serverbound::ClientStatus(_)) => true,
            Ok(play::Serverbound::ChangeGameMode(value)) => {
                if self.dead {
                    self.send_mode_state(writer)?;
                    return Ok(());
                }
                if self.pending_mode.is_some() {
                    self.send_mode_state(writer)?;
                    return Ok(());
                }
                let Some(session) = self.backend_session else {
                    return self.disconnect_play(writer, "Mode changes require backend Play");
                };
                let mode = match value {
                    0 => crate::module_bindings::GameMode::Survival,
                    2 => crate::module_bindings::GameMode::Adventure,
                    _ => {
                        self.send_mode_state(writer)?;
                        return Ok(());
                    }
                };
                match self.backend.change_game_mode(session, mode) {
                    Ok(receiver) => {
                        self.pending_mode = Some((
                            receiver,
                            value as u8,
                            (self.clock)() + Duration::from_secs(3),
                            false,
                        ))
                    }
                    Err(error) => {
                        self.system_message(writer, &format!("Mode change unavailable: {error}"))?;
                        self.send_mode_state(writer)?;
                    }
                }
                true
            }
            Ok(play::Serverbound::PlayerInput(flags)) => {
                self.input_flags = if self.dead { 0 } else { flags };
                true
            }
            Ok(play::Serverbound::HeldSlot(slot)) => {
                if self.dead {
                    self.inventory_resync = true;
                    self.refresh_inventory(writer)?;
                } else if self.inventory_snapshot.is_some() {
                    let mut fields = Vec::new();
                    rusty_mines_inventory::wire::put_int(&mut fields, i32::from(slot));
                    self.queue_inventory(0, fields, None, writer)?;
                } else {
                    self.held_slot = slot;
                }
                true
            }
            Ok(play::Serverbound::PlayerCommand { action, .. }) => {
                if !self.dead {
                    match action {
                        1 => self.sprinting = true,
                        2 => self.sprinting = false,
                        _ => {}
                    }
                }
                true
            }
            Ok(play::Serverbound::PlayerAction {
                status,
                position,
                sequence,
                ..
            }) => {
                if self.dead {
                    self.correct_block(writer, position)?;
                    play::Clientbound::BlockAcknowledgement(sequence).write(writer)?;
                } else if status == 2 {
                    self.queue_block_action(sequence, position, false, writer)?;
                } else if status == 0 || status == 1 {
                    self.correct_block(writer, position)?;
                    play::Clientbound::BlockAcknowledgement(sequence).write(writer)?;
                } else if status == 3 || status == 4 {
                    self.inventory_resync = true;
                    self.refresh_inventory(writer)?;
                } else if status == 6 {
                    self.queue_inventory(6, Vec::new(), None, writer)?;
                } else {
                    play::Clientbound::BlockAcknowledgement(sequence).write(writer)?;
                }
                true
            }
            Ok(play::Serverbound::UseItemOn {
                position,
                face,
                sequence,
                ..
            }) => {
                let target = position.adjacent(face);
                if self.dead {
                    self.correct_block(writer, position)?;
                    self.correct_block(writer, target)?;
                    play::Clientbound::BlockAcknowledgement(sequence).write(writer)?;
                    return self.confirm_play(writer);
                }
                let held = self
                    .inventory_snapshot
                    .as_ref()
                    .and_then(|snapshot| {
                        rusty_mines_inventory::model::decode_slots(
                            &snapshot.slots,
                            if snapshot.menu_id == 0 { 46 } else { 63 },
                        )
                        .ok()
                    })
                    .and_then(|slots| {
                        slots
                            .get(36 + usize::from(self.held_slot.max(0) as u8))
                            .cloned()
                    });
                let stone = rusty_mines_inventory::model::item_id("minecraft:stone").ok();
                if held
                    .as_ref()
                    .is_some_and(|stack| stack.count > 0 && Some(stack.item) == stone)
                {
                    self.queue_block_action(sequence, target, true, writer)?;
                } else {
                    self.correct_block(writer, position)?;
                    self.correct_block(writer, target)?;
                    play::Clientbound::BlockAcknowledgement(sequence).write(writer)?;
                }
                true
            }
            Ok(play::Serverbound::UseItem {
                sequence, rotation, ..
            }) => {
                if !self.dead {
                    self.player_rotation = rotation;
                }
                play::Clientbound::BlockAcknowledgement(sequence).write(writer)?;
                true
            }
            Ok(play::Serverbound::CreativeSlot { slot, stack }) => {
                if self.dead {
                    self.inventory_resync = true;
                    self.refresh_inventory(writer)?;
                } else {
                    let mut fields = Vec::new();
                    rusty_mines_inventory::wire::put_int(&mut fields, i32::from(slot));
                    fields.extend(stack.trusted_bytes());
                    self.queue_inventory(1, fields, None, writer)?;
                }
                true
            }
            Ok(play::Serverbound::InventoryClick(click)) => {
                if self.dead {
                    self.inventory_resync = true;
                    self.refresh_inventory(writer)?;
                    return self.confirm_play(writer);
                }
                let stale = self.inventory_snapshot.as_ref().is_none_or(|snapshot| {
                    snapshot.revision != click.revision || snapshot.menu_id != click.menu
                });
                if stale {
                    self.inventory_resync = true;
                    self.refresh_inventory(writer)?;
                } else {
                    let mut fields = Vec::new();
                    for value in [
                        click.menu,
                        i32::from(click.slot),
                        i32::from(click.button),
                        click.mode,
                    ] {
                        rusty_mines_inventory::wire::put_int(&mut fields, value);
                    }
                    self.queue_inventory(2, fields, Some(click), writer)?;
                }
                true
            }
            Ok(play::Serverbound::CloseContainer(menu)) => {
                self.inventory_reopen_menu = true;
                if self.inventory_pending.is_some() {
                    self.inventory_deferred_close = Some(menu);
                } else {
                    let mut fields = Vec::new();
                    rusty_mines_inventory::wire::put_int(&mut fields, menu);
                    self.queue_inventory(4, fields, None, writer)?;
                }
                true
            }
            Ok(
                play::Serverbound::Punch
                | play::Serverbound::RecipeSettings
                | play::Serverbound::PickBlock
                | play::Serverbound::PickEntity
                | play::Serverbound::Attack(_),
            ) => true,
            Err(_) => false,
        };
        if !valid {
            return self.disconnect_play(
                writer,
                "Unsupported or invalid Play packet; static world only",
            );
        }
        self.confirm_play(writer)
    }

    fn confirm_play(&mut self, writer: &mut impl Write) -> io::Result<()> {
        if self.state == ConnectionState::Play
            && self.teleport_confirmed
            && (self.player_loaded || self.loading_ticks >= 60)
            && !self.play_confirmed
        {
            let session = self
                .backend_session
                .ok_or_else(|| io::Error::other("Missing backend session"))?;
            if let Err(error) = self.backend.play(session) {
                return self.disconnect_play(writer, &format!("Backend refused Play: {error}"));
            }
            if (self.clock)() >= self.deadline {
                return self.disconnect_play(writer, "World loading timed out");
            }
            // Prevent another transition while polling after a potentially slow backend call.
            self.play_confirmed = true;
            self.controls.mark_playing(session);
            self.controls
                .publish_snapshot_to(session, self.backend.world_players());
            self.poll((self.clock)(), writer)?;
            if self.state == ConnectionState::Closed {
                return Ok(());
            }
            self.send_mode_state(writer)?;
            self.system_message(writer, "Welcome to Rusty Mines. Mode-restricted block edits require a synchronized inventory backend.")?;
        }
        Ok(())
    }

    fn queue_inventory(
        &mut self,
        action: i32,
        fields: Vec<u8>,
        evidence: Option<play::InventoryClick>,
        writer: &mut impl Write,
    ) -> io::Result<()> {
        if !self.play_confirmed {
            return Ok(());
        }
        let Some(snapshot) = self.inventory_snapshot.as_ref() else {
            let state = rusty_mines_inventory::model::State::default();
            play::write_inventory(
                writer,
                0,
                0,
                &rusty_mines_inventory::model::encode_slots(&state.slots),
                &[0],
                self.held_slot as u8,
            )?;
            if !self.inventory_notice {
                self.system_message(
                    writer,
                    "Inventory backend upgrade is unavailable; this session remains read-only.",
                )?;
                self.inventory_notice = true;
            }
            return Ok(());
        };
        if self.inventory_pending.is_some() {
            self.inventory_resync = true;
            return self.refresh_inventory(writer);
        }
        let mut payload = Vec::new();
        rusty_mines_inventory::wire::put_int(&mut payload, action);
        rusty_mines_inventory::wire::put_int(&mut payload, snapshot.revision);
        payload.extend(fields);
        let sequence = snapshot
            .sequence
            .checked_add(1)
            .ok_or_else(|| io::Error::other("Inventory sequence exhausted"))?;
        let session = self
            .backend_session
            .ok_or_else(|| io::Error::other("Missing inventory session"))?;
        match self.backend.inventory_submit(session, sequence, payload) {
            Ok(receiver) => {
                self.inventory_pending = Some(PendingInventory {
                    receiver,
                    sequence,
                    deadline: (self.clock)() + Duration::from_secs(3),
                    accepted: false,
                    evidence,
                });
            }
            Err(error) => {
                self.system_message(writer, &format!("Inventory request unavailable: {error}"))?;
                self.inventory_resync = true;
            }
        }
        Ok(())
    }

    fn refresh_inventory(&mut self, writer: &mut impl Write) -> io::Result<()> {
        if !self.play_confirmed || self.state == ConnectionState::Closed {
            return Ok(());
        }
        let Some(session) = self.backend_session else {
            return Ok(());
        };
        if let Some(pending) = self.inventory_pending.as_mut() {
            if !pending.accepted {
                match pending.receiver.try_recv() {
                    Ok(Ok(())) => pending.accepted = true,
                    Ok(Err(error)) => {
                        self.inventory_pending = None;
                        self.inventory_resync = true;
                        self.system_message(
                            writer,
                            &format!("Inventory action rejected: {error}"),
                        )?;
                    }
                    Err(std::sync::mpsc::TryRecvError::Disconnected) => {
                        return self
                            .disconnect_play(writer, "Inventory result unavailable; reconnect");
                    }
                    Err(std::sync::mpsc::TryRecvError::Empty) => {}
                }
            }
        }
        if self
            .inventory_pending
            .as_ref()
            .is_some_and(|pending| (self.clock)() >= pending.deadline)
        {
            return self.disconnect_play(
                writer,
                "Inventory transaction confirmation timed out; reconnect",
            );
        }
        let Some(snapshot) = self.backend.inventory_snapshot(session)? else {
            if self.inventory_snapshot.is_some() {
                return self.disconnect_play(
                    writer,
                    "Authoritative inventory became unavailable; reconnect",
                );
            }
            return Ok(());
        };
        if snapshot.session_uuid != spacetimedb_sdk::Uuid::from_u128(session.as_u128()) {
            return self.disconnect_play(
                writer,
                "Authoritative inventory session mismatch; reconnect",
            );
        }
        let previous = self
            .inventory_snapshot
            .as_ref()
            .map_or(0, |row| row.menu_id);
        let mut prediction_mismatch = false;
        if self
            .inventory_pending
            .as_ref()
            .is_some_and(|pending| pending.accepted && snapshot.sequence == pending.sequence)
        {
            let pending = self
                .inventory_pending
                .take()
                .ok_or_else(|| io::Error::other("Inventory request disappeared"))?;
            if let Some(evidence) = pending.evidence {
                let slots = rusty_mines_inventory::model::decode_slots(
                    &snapshot.slots,
                    if snapshot.menu_id == 0 { 46 } else { 63 },
                )
                .map_err(io::Error::other)?;
                let cursor = rusty_mines_inventory::wire::decode_stack(&snapshot.cursor, false)
                    .map_err(io::Error::other)?;
                prediction_mismatch =
                    !rusty_mines_inventory::model::hash_matches_default(&evidence.cursor, &cursor)
                        || evidence.changed.iter().any(|(index, hash)| {
                            slots.get(*index as usize).is_none_or(|slot| {
                                !rusty_mines_inventory::model::hash_matches_default(hash, slot)
                            })
                        });
            }
            self.inventory_resync = true;
        }
        if self.inventory_resync || self.inventory_snapshot.as_ref() != Some(&snapshot) {
            play::write_inventory_menu(
                writer,
                if self.inventory_reopen_menu && self.inventory_pending.is_none() {
                    0
                } else {
                    previous
                },
                snapshot.menu_id,
                &snapshot.menu_title,
            )?;
            play::write_inventory(
                writer,
                snapshot.menu_id,
                snapshot.revision,
                &snapshot.slots,
                &snapshot.cursor,
                snapshot.selected,
            )?;
            self.held_slot = i16::from(snapshot.selected);
            self.inventory_snapshot = Some(snapshot);
            self.inventory_resync = false;
            if self.inventory_pending.is_none() {
                self.inventory_reopen_menu = false;
            }
        }
        if prediction_mismatch {
            self.system_message(
                writer,
                "Inventory prediction differed; authoritative snapshot restored.",
            )?;
        }
        if self.inventory_pending.is_none() {
            if let Some(menu) = self.inventory_deferred_close.take() {
                if self
                    .inventory_snapshot
                    .as_ref()
                    .is_some_and(|snapshot| snapshot.menu_id == menu)
                {
                    let mut fields = Vec::new();
                    rusty_mines_inventory::wire::put_int(&mut fields, menu);
                    self.queue_inventory(4, fields, None, writer)?;
                }
            }
        }
        if self
            .inventory_pending
            .as_ref()
            .is_some_and(|pending| (self.clock)() >= pending.deadline)
        {
            return self.disconnect_play(
                writer,
                "Inventory transaction confirmation timed out; reconnect",
            );
        }
        Ok(())
    }

    fn reset_loading(&mut self) {
        self.loading_ticks = 0;
        self.loading_started = Some((self.clock)());
        self.player_loaded = false;
        self.teleport_confirmed = false;
        self.play_confirmed = false;
        self.pending_teleport = None;
    }

    fn send_teleport(&mut self, kind: TeleportKind, writer: &mut impl Write) -> io::Result<()> {
        if self.pending_teleport.is_some() {
            return Ok(());
        }
        let Some(id) = self.teleport_id.checked_add(1).filter(|id| *id > 0) else {
            return self.disconnect_play(writer, "Teleport ID space exhausted");
        };
        let pose = play::SynchronizePlayerPosition {
            id,
            // Absolute client application adds +0 and clamps pitch before the ACK.
            position: self.player_position.map(|v| v + 0.0),
            velocity: [0.0; 3],
            rotation: [
                self.player_rotation[0] + 0.0,
                (self.player_rotation[1] + 0.0).clamp(-90.0, 90.0),
            ],
            flags: 0,
        };
        play::write_official_packet(writer, &pose.packet().map_err(invalid_data)?)?;
        self.teleport_id = id;
        self.pending_teleport = Some(PendingTeleport {
            kind,
            pose,
            deadline: (self.clock)() + Duration::from_secs(10),
        });
        Ok(())
    }

    fn system_message(&self, writer: &mut impl Write, text: &str) -> io::Result<()> {
        play::Clientbound::SystemMessage(
            configuration::NetworkNbt::text(text).map_err(invalid_data)?,
        )
        .write(writer)
    }
    fn send_mode_state(&self, writer: &mut impl Write) -> io::Result<()> {
        let flags = match self.game_mode {
            1 => 0x0f, // Creative: invulnerable, flying, allowed to fly, instant build.
            3 => 0x07, // Spectator: invulnerable, flying, and allowed to fly.
            _ => 0,
        };
        play::Clientbound::Abilities {
            flags,
            flying_speed: 0.05,
            walking_speed: 0.1,
        }
        .write(writer)?;
        play::Clientbound::Health {
            health: self.health,
            food: self.food,
            saturation: 5.0,
        }
        .write(writer)?;
        play::Clientbound::GameEvent {
            event: 3,
            value: f32::from(self.game_mode),
        }
        .write(writer)?;
        Ok(())
    }

    fn respawn(
        &mut self,
        expected: crate::gateway::GatewayPlayerState,
        writer: &mut impl Write,
    ) -> io::Result<()> {
        if !self.dead || !matches!(self.game_mode, 0 | 2) {
            return Ok(());
        }
        self.player_position = [8.5, 65.0, 8.5];
        self.vertical_velocity = 0.0;
        self.on_ground = true;
        self.health = f32::from(expected.health);
        self.food = i32::from(expected.food);
        self.dead = false;
        self.simulation_at = (self.clock)();
        self.loaded_chunks.clear();
        self.chunk_revisions.clear();
        self.pending_chunk_center = Some((0, 0));
        self.next_chunk_batch = self.simulation_at + SERVER_TICK;
        self.inventory_snapshot = None;
        self.inventory_resync = true;
        self.inventory_pending = None;
        self.inventory_deferred_close = None;
        self.inventory_reopen_menu = false;
        self.block_pending = None;
        self.pending_mode = None;
        self.pending_player_state = None;
        self.dead = false;
        self.sprinting = false;
        self.flying = matches!(self.game_mode, 1 | 3);
        self.next_food_drain = self.simulation_at + Duration::from_secs(5);
        self.player_loaded = false;
        self.loading_ticks = 0;
        self.loading_started = Some(self.simulation_at);
        play::Clientbound::Respawn {
            adventure: self.game_mode == 2,
        }
        .write(writer)?;
        self.send_mode_state(writer)?;
        self.publish_authoritative_pose()?;
        self.send_teleport(TeleportKind::Correction, writer)
    }

    fn simulate_vertical(&mut self, now: Instant, writer: &mut impl Write) -> io::Result<()> {
        if self.game_mode == 3 || self.dead {
            return Ok(());
        }
        let initial_y = self.player_position[1];
        let elapsed = now.saturating_duration_since(self.simulation_at);
        let ticks = (elapsed.as_nanos() / SERVER_TICK.as_nanos()).min(8) as u32;
        if ticks == 0 {
            return Ok(());
        }
        self.simulation_at += SERVER_TICK * ticks;
        for _ in 0..ticks {
            if self.flying && self.game_mode == 1 {
                self.vertical_velocity = 0.0;
            } else {
                let was_above_platform = self.player_position[1] >= 65.0;
                self.vertical_velocity = (self.vertical_velocity - 0.08).max(-3.92);
                self.player_position[1] += self.vertical_velocity;
                if was_above_platform && self.player_position[1] <= 65.0 {
                    self.player_position[1] = 65.0;
                    self.vertical_velocity = 0.0;
                    self.on_ground = true;
                } else {
                    self.on_ground = false;
                }
            }
            if self.game_mode == 0 && self.sprinting && self.food > 0 && now >= self.next_food_drain
            {
                self.food -= 1;
                self.next_food_drain = now + Duration::from_secs(5);
                play::Clientbound::Health {
                    health: self.health,
                    food: self.food,
                    saturation: 5.0_f32.min(self.food as f32),
                }
                .write(writer)?;
            }
            if self.player_position[1] < -64.0 {
                if matches!(self.game_mode, 0 | 2) {
                    if let Some(session) = self.backend_session {
                        if self
                            .pending_player_state
                            .as_ref()
                            .is_none_or(|pending| pending.expected.dead)
                        {
                            self.persist_player_state(now, false, writer)?;
                        }
                        match self.backend.player_state(session) {
                            Ok(Some(current)) if current.health == 0 && current.dead => {}
                            Ok(Some(_)) => return Ok(()),
                            Ok(None) if !self.backend.requires_persistent_player_state() => {}
                            Ok(None) => return Ok(()),
                            Err(error)
                                if !self.backend.requires_persistent_player_state()
                                    && error.kind() == io::ErrorKind::Unsupported => {}
                            Err(error) => return Err(error),
                        }
                    }
                    self.player_position[1] = -64.0;
                    self.vertical_velocity = 0.0;
                    self.on_ground = false;
                    self.health = 0.0;
                    self.dead = true;
                    play::Clientbound::Health {
                        health: self.health,
                        food: self.food,
                        saturation: 0.0,
                    }
                    .write(writer)?;
                    play::Clientbound::CombatDeath {
                        entity_id: self.entity_id.ok_or_else(|| {
                            io::Error::other("death without a confirmed entity ID")
                        })?,
                        message: configuration::NetworkNbt::text("You died in the void")
                            .map_err(invalid_data)?,
                    }
                    .write(writer)?;
                    self.on_ground = false;
                } else {
                    self.player_position = [8.5, 65.0, 8.5];
                    self.vertical_velocity = 0.0;
                    self.health = 20.0;
                    self.on_ground = true;
                    play::Clientbound::Health {
                        health: self.health,
                        food: self.food,
                        saturation: 5.0_f32.min(self.food as f32),
                    }
                    .write(writer)?;
                    self.send_teleport(TeleportKind::Correction, writer)?;
                }
                break;
            }
        }
        if self.player_position[1] != initial_y {
            self.publish_authoritative_pose()?;
        }
        Ok(())
    }

    #[cfg(test)]
    fn test_vertical_tick(&mut self, count: u32, writer: &mut impl Write) -> io::Result<()> {
        self.simulate_vertical(self.simulation_at + SERVER_TICK * count.min(8), writer)
    }
    fn correct_block(
        &self,
        writer: &mut impl Write,
        position: play::BlockPosition,
    ) -> io::Result<()> {
        if let Some(state) = self.authoritative_block_state(position)? {
            play::Clientbound::BlockUpdate(position, state).write(writer)?;
        }
        Ok(())
    }
    fn authoritative_block_state(&self, position: play::BlockPosition) -> io::Result<Option<i32>> {
        let Some(base) = rusty_mines::vanilla_world::block_state(position) else {
            return Ok(None);
        };
        let Some(session) = self.backend_session else {
            return Ok(Some(base));
        };
        let snapshot = match self.backend.chunk_snapshot(
            session,
            position.x.div_euclid(16),
            position.z.div_euclid(16),
        ) {
            Ok(snapshot) => snapshot,
            Err(error)
                if !self.backend.requires_persistent_world_snapshots()
                    && error.kind() == io::ErrorKind::Unsupported =>
            {
                None
            }
            Err(error) => return Err(error),
        };
        let Some(snapshot) = snapshot else {
            return Ok(Some(base));
        };
        decode_block_overrides(
            &snapshot,
            position.x.div_euclid(16),
            position.z.div_euclid(16),
        )
        .map(|rows| {
            rows.get(&(position.x, position.y, position.z))
                .copied()
                .unwrap_or(base)
        })
        .map(Some)
    }

    fn queue_block_action(
        &mut self,
        sequence: i32,
        position: play::BlockPosition,
        place: bool,
        writer: &mut impl Write,
    ) -> io::Result<()> {
        if !self.play_confirmed {
            return Ok(());
        }
        if self.block_pending.is_some() {
            self.correct_block(writer, position)?;
            play::Clientbound::BlockAcknowledgement(sequence).write(writer)?;
            return Ok(());
        }
        let Some(session) = self.backend_session else {
            self.correct_block(writer, position)?;
            play::Clientbound::BlockAcknowledgement(sequence).write(writer)?;
            return Ok(());
        };
        let Some(state) = self.authoritative_block_state(position)? else {
            play::Clientbound::BlockAcknowledgement(sequence).write(writer)?;
            return Ok(());
        };
        let Ok(expected) = u32::try_from(state) else {
            return Err(invalid_data("invalid block action coordinate/state"));
        };
        let (x, y, z) = (position.x, position.y, position.z);
        let action_sequence = self
            .block_sequence
            .checked_add(1)
            .ok_or_else(|| io::Error::other("block action sequence exhausted"))?;
        match self
            .backend
            .block_action_submit(session, action_sequence, x, y, z, expected, place)
        {
            Ok(receiver) => {
                self.block_pending = Some(PendingBlockAction {
                    receiver,
                    sequence,
                    position,
                    expected_state: expected,
                    place,
                    deadline: (self.clock)() + Duration::from_secs(3),
                    accepted: false,
                });
            }
            Err(error) if error.kind() == io::ErrorKind::Unsupported => {
                self.correct_block(writer, position)?;
                play::Clientbound::BlockAcknowledgement(sequence).write(writer)?;
            }
            Err(error) => return Err(error),
        }
        Ok(())
    }

    fn poll_block_action(&mut self, now: Instant, writer: &mut impl Write) -> io::Result<()> {
        let Some(pending) = self.block_pending.as_mut() else {
            return Ok(());
        };
        if now >= pending.deadline {
            return self.disconnect_play(writer, "Block action confirmation timed out; reconnect");
        }
        if !pending.accepted {
            match pending.receiver.try_recv() {
                Ok(Ok(())) => pending.accepted = true,
                Ok(Err(_)) => {
                    let position = pending.position;
                    let sequence = pending.sequence;
                    self.block_pending = None;
                    self.correct_block(writer, position)?;
                    play::Clientbound::BlockAcknowledgement(sequence).write(writer)?;
                    return Ok(());
                }
                Err(std::sync::mpsc::TryRecvError::Disconnected) => {
                    return self
                        .disconnect_play(writer, "Block action result unavailable; reconnect")
                }
                Err(std::sync::mpsc::TryRecvError::Empty) => return Ok(()),
            }
        }
        let pending = self.block_pending.as_ref().unwrap();
        let Some(session) = self.backend_session else {
            return self.disconnect_play(writer, "Block action session missing; reconnect");
        };
        let Some(action) = self.backend.block_action_snapshot(session)? else {
            return Ok(());
        };
        if action.sequence != self.block_sequence.saturating_add(1) {
            return Ok(());
        }
        let position = pending.position;
        let sequence = pending.sequence;
        if (action.x, action.y, action.z) != (position.x, position.y, position.z)
            || action.expected_state != pending.expected_state
            || action.resulting_state != if pending.place { 1 } else { 0 }
        {
            return self.disconnect_play(writer, "Block action confirmation mismatch; reconnect");
        }
        self.block_pending = None;
        self.block_sequence = action.sequence;
        self.inventory_resync = true;
        self.refresh_inventory(writer)?;
        self.correct_block(writer, position)?;
        play::Clientbound::BlockAcknowledgement(sequence).write(writer)
    }

    fn poll_chunk_overrides(&mut self, writer: &mut impl Write) -> io::Result<()> {
        let Some(session) = self.backend_session else {
            return Err(io::Error::new(
                io::ErrorKind::Unsupported,
                "Chunk snapshot requires backend session",
            ));
        };
        for (chunk_x, chunk_z) in self.loaded_chunks.iter().copied().collect::<Vec<_>>() {
            let snapshot = match self.backend.chunk_snapshot(session, chunk_x, chunk_z) {
                Ok(Some(snapshot)) => snapshot,
                Ok(None) if !self.backend.requires_persistent_world_snapshots() => continue,
                Err(error)
                    if !self.backend.requires_persistent_world_snapshots()
                        && error.kind() == io::ErrorKind::Unsupported =>
                {
                    continue
                }
                Ok(None) => return Err(io::Error::other("Chunk snapshot view is unavailable")),
                Err(error) => return Err(error),
            };
            if self.chunk_revisions.get(&(chunk_x, chunk_z)) == Some(&snapshot.revision) {
                continue;
            }
            let overrides = decode_block_overrides(&snapshot, chunk_x, chunk_z)?;
            for ((x, y, z), state) in &overrides {
                play::Clientbound::BlockUpdate(
                    play::BlockPosition {
                        x: *x,
                        y: *y,
                        z: *z,
                    },
                    *state,
                )
                .write(writer)?;
            }
            self.chunk_revisions
                .insert((chunk_x, chunk_z), snapshot.revision);
        }
        Ok(())
    }

    fn persist_player_state(
        &mut self,
        now: Instant,
        respawn: bool,
        writer: &mut impl Write,
    ) -> io::Result<()> {
        if let Some(pending) = self.pending_player_state.as_mut() {
            if now >= pending.deadline {
                return self
                    .disconnect_play(writer, "Player state confirmation timed out; reconnect");
            }
            if !pending.accepted {
                match pending.receiver.try_recv() {
                    Ok(Ok(())) => pending.accepted = true,
                    Ok(Err(_)) | Err(std::sync::mpsc::TryRecvError::Disconnected) => {
                        self.pending_player_state = None;
                        self.send_mode_state(writer)?;
                        return Ok(());
                    }
                    Err(std::sync::mpsc::TryRecvError::Empty) => return Ok(()),
                }
            }
            let pending = self.pending_player_state.as_ref().unwrap();
            let session = self
                .backend_session
                .ok_or_else(|| io::Error::other("Missing player state session"))?;
            let Some(actual) = self.backend.player_state(session)? else {
                return Ok(());
            };
            if actual != pending.expected {
                return Ok(());
            }
            let pending = self.pending_player_state.take().unwrap();
            if pending.respawn
                || (pending.expected.respawn_revision == Some(pending.expected.revision)
                    && pending.expected.health == 20
                    && !pending.expected.dead
                    && self.dead)
            {
                self.respawn(pending.expected, writer)?;
            }
            return Ok(());
        }
        let session = self
            .backend_session
            .ok_or_else(|| io::Error::other("Missing player state session"))?;
        let Some(current) = self.backend.player_state(session)? else {
            return Ok(());
        };
        if current.health == self.health as u8
            && current.food == self.food as u8
            && current.dead == self.dead
        {
            return Ok(());
        }
        let expected = crate::gateway::GatewayPlayerState {
            mode: current.mode,
            health: self.health.clamp(0.0, 20.0) as u8,
            food: self.food.clamp(0, 20) as u8,
            dead: self.dead,
            revision: current.revision.saturating_add(1),
            respawn_revision: None,
            state_owner: current.state_owner,
        };
        let receiver = match self.backend.player_state_update(
            session,
            expected.health,
            expected.food,
            expected.dead,
            current.revision,
        ) {
            Ok(receiver) => receiver,
            Err(error) => {
                return self
                    .disconnect_play(writer, &format!("Player state update failed: {error}"))
            }
        };
        self.pending_player_state = Some(PendingPlayerState {
            receiver,
            expected,
            deadline: now + Duration::from_secs(3),
            accepted: false,
            respawn,
        });
        Ok(())
    }
    fn record_keepalive_rtt(&mut self) {
        if let (Some(id), Some(sent)) = (self.backend_session, self.keepalive_sent.take()) {
            self.controls
                .sample(id, (self.clock)().saturating_duration_since(sent));
        }
    }

    fn publish_authoritative_pose(&self) -> io::Result<()> {
        let Some(session) = self.backend_session.filter(|_| self.play_confirmed) else {
            return Ok(());
        };
        self.backend
            .update_pose(crate::module_bindings::PlayerPoseUpdate {
                session_uuid: spacetimedb_sdk::Uuid::from_u128(session.as_u128()),
                x: self.player_position[0],
                y: self.player_position[1],
                z: self.player_position[2],
                yaw: self.player_rotation[0].rem_euclid(360.0),
                pitch: self.player_rotation[1].clamp(-90.0, 90.0),
                on_ground: self.on_ground,
            })
    }

    fn poll(&mut self, now: Instant, writer: &mut impl Write) -> io::Result<()> {
        if self.state == ConnectionState::Closed {
            return Ok(());
        }
        let kick = self
            .control
            .as_ref()
            .and_then(|guard| guard.receiver.try_recv().ok());
        if let Some(kick) = kick {
            let result = match self.state {
                ConnectionState::Login => self.disconnect_login(writer, &kick.reason),
                ConnectionState::Configuration => {
                    self.disconnect_configuration(writer, &kick.reason)
                }
                ConnectionState::Play => self.disconnect_play(writer, &kick.reason),
                _ => Err(io::Error::other(
                    "Connection is no longer in a kickable wire phase",
                )),
            };
            match &result {
                Ok(()) => self.kick_ack = Some(kick.ack),
                Err(error) => {
                    let _ = kick
                        .ack
                        .send(Err(format!("Disconnect send failed: {error}")));
                }
            }
            return result;
        }
        if let Some(session) = self.backend_session {
            if let Err(error) = self.backend.check_session(session) {
                console_event!("Backend session unavailable: {error}");
                return match self.state {
                    ConnectionState::Login => {
                        self.disconnect_login(writer, "Backend session unavailable; reconnect")
                    }
                    ConnectionState::Configuration => self
                        .disconnect_configuration(writer, "Backend session unavailable; reconnect"),
                    ConnectionState::Play => {
                        self.disconnect_play(writer, "Backend session unavailable; reconnect")
                    }
                    _ => Ok(()),
                };
            }
        }
        if self.state == ConnectionState::Play && self.play_confirmed {
            if let Some(session) = self.backend_session {
                let old_health = self.health;
                let old_food = self.food;
                let old_dead = self.dead;
                let old_mode = self.game_mode;
                let state = self.backend.player_state(session)?;
                if state.is_none() && self.backend.requires_persistent_player_state() {
                    return self.disconnect_play(
                        writer,
                        "Persistent player state is not synchronized; reconnect",
                    );
                }
                if let Some(state) = state {
                    if state.health > 20 || state.food > 20 {
                        return self.disconnect_play(writer, "Invalid persistent player state");
                    }
                    self.health = f32::from(state.health);
                    self.food = i32::from(state.food);
                    self.dead = state.dead && state.respawn_revision != Some(state.revision);
                }
                let mode = self.backend.game_mode(session)?;
                if mode.is_none() && self.backend.requires_persistent_player_state() {
                    return self.disconnect_play(
                        writer,
                        "Persistent game mode is not synchronized; reconnect",
                    );
                }
                if let Some(mode) = mode.map(|mode| match mode {
                    crate::module_bindings::GameMode::Survival => 0,
                    crate::module_bindings::GameMode::Creative => 1,
                    crate::module_bindings::GameMode::Adventure => 2,
                    crate::module_bindings::GameMode::Spectator => 3,
                }) {
                    self.game_mode = mode;
                }
                if state.is_some_and(|state| state.dead && !self.dead)
                    && self.pending_teleport.is_none()
                {
                    self.player_position = [8.5, 65.0, 8.5];
                    self.vertical_velocity = 0.0;
                    self.on_ground = true;
                    self.loaded_chunks.clear();
                    self.chunk_revisions.clear();
                    self.pending_chunk_center = Some((0, 0));
                    play::Clientbound::Respawn {
                        adventure: self.game_mode == 2,
                    }
                    .write(writer)?;
                    self.send_teleport(TeleportKind::Correction, writer)?;
                }
                self.flying = matches!(self.game_mode, 1 | 3);
                if (old_health, old_food, old_dead, old_mode)
                    != (self.health, self.food, self.dead, self.game_mode)
                {
                    self.send_mode_state(writer)?;
                }
            }
            self.simulate_vertical(now, writer)?;
            self.poll_block_action(now, writer)?;
            if self.state == ConnectionState::Closed {
                return Ok(());
            }
            self.persist_player_state(now, false, writer)?;
            if self.state == ConnectionState::Closed {
                return Ok(());
            }
            if let Some(session) = self.backend_session {
                if let Some(mode) = self.backend.game_mode(session)? {
                    let mode = match mode {
                        crate::module_bindings::GameMode::Survival => 0,
                        crate::module_bindings::GameMode::Creative => 1,
                        crate::module_bindings::GameMode::Adventure => 2,
                        crate::module_bindings::GameMode::Spectator => 3,
                    };
                    if mode != self.game_mode {
                        self.game_mode = mode;
                        self.flying = matches!(mode, 1 | 3);
                        self.send_mode_state(writer)?;
                    }
                }
            }
            let mut clear_mode = false;
            let mut restore_mode = false;
            if let Some((receiver, requested, deadline, accepted)) = self.pending_mode.as_mut() {
                if now >= *deadline {
                    return self
                        .disconnect_play(writer, "Mode change confirmation timed out; reconnect");
                }
                if !*accepted {
                    match receiver.try_recv() {
                        Ok(Ok(())) => *accepted = true,
                        Ok(Err(_)) | Err(std::sync::mpsc::TryRecvError::Disconnected) => {
                            clear_mode = true;
                            restore_mode = true;
                        }
                        Err(std::sync::mpsc::TryRecvError::Empty) => {}
                    }
                }
                if *accepted && self.game_mode == *requested {
                    clear_mode = true;
                }
            }
            if clear_mode {
                self.pending_mode = None;
            }
            if restore_mode {
                self.send_mode_state(writer)?;
            }
            self.poll_chunk_overrides(writer)?;
            let overflow = self
                .control
                .as_ref()
                .is_some_and(|guard| guard.take_replication_overflow());
            if overflow {
                return self.disconnect_play(writer, "Player replication fell behind; reconnect");
            }
            for _ in 0..64 {
                let event = self
                    .control
                    .as_ref()
                    .and_then(|guard| guard.replication.try_recv().ok());
                let Some(event) = event else { break };
                if let Err(error) = self.apply_replication_event(event, writer) {
                    return self
                        .disconnect_play(writer, &format!("Player replication failed: {error}"));
                }
            }
            if self
                .control
                .as_ref()
                .is_some_and(|guard| guard.take_chat_overflow())
            {
                return self.disconnect_play(writer, "Player chat delivery fell behind; reconnect");
            }
            for _ in 0..16 {
                let message = self
                    .control
                    .as_ref()
                    .and_then(|guard| guard.chat.try_recv().ok());
                let Some(message) = message else { break };
                if self.controls.chat_is_current(&message) {
                    self.system_message(
                        writer,
                        &format!("<{}> {}", message.username, message.text),
                    )?;
                }
            }
            if let Some(pending) = &self.chat_pending {
                if now >= pending.deadline {
                    self.chat_pending = None;
                    return self
                        .disconnect_play(writer, "Chat backend confirmation timed out; reconnect");
                }
                match pending.receiver.try_recv() {
                    Ok(Ok(())) => self.chat_pending = None,
                    Ok(Err(error)) => {
                        self.chat_pending = None;
                        self.system_message(writer, &format!("Chat rejected: {error}"))?;
                    }
                    Err(std::sync::mpsc::TryRecvError::Disconnected) => {
                        self.chat_pending = None;
                        return self.disconnect_play(writer, "Chat backend failed; reconnect");
                    }
                    Err(std::sync::mpsc::TryRecvError::Empty) => {}
                }
            }
            self.refresh_inventory(writer)?;
            if self.state == ConnectionState::Closed {
                return Ok(());
            }
            for _ in 0..16 {
                let message = self
                    .control
                    .as_ref()
                    .and_then(|g| g.messages.try_recv().ok());
                let Some(message) = message else { break };
                let result = self.system_message(writer, &message.reason);
                let _ = message.ack.send(
                    result
                        .as_ref()
                        .map(|_| ())
                        .map_err(|e| format!("Message send failed: {e}")),
                );
                result?;
            }
        }
        if self.state == ConnectionState::Play
            && self.pending_teleport.is_some_and(|p| now >= p.deadline)
        {
            return self.disconnect_play(writer, "Teleport acknowledgement timed out");
        }
        if now >= self.deadline && !(self.state == ConnectionState::Play && self.play_confirmed) {
            return match self.state {
                ConnectionState::Login => self.disconnect_login(writer, "Login timed out"),
                ConnectionState::Configuration => {
                    self.disconnect_configuration(writer, "Configuration timed out")
                }
                ConnectionState::Play => self.disconnect_play(writer, "World loading timed out"),
                _ => {
                    self.state = ConnectionState::Closed;
                    Ok(())
                }
            };
        }
        if !matches!(
            self.state,
            ConnectionState::Configuration | ConnectionState::Play
        ) {
            return Ok(());
        }
        if self
            .keepalive
            .is_some_and(|(_, sent)| now.duration_since(sent) >= KEEPALIVE_TIMEOUT)
        {
            return if self.state == ConnectionState::Play {
                self.disconnect_play(writer, "Play keepalive timed out")
            } else {
                self.disconnect_configuration(writer, "Configuration keepalive timed out")
            };
        }
        if self.state == ConnectionState::Play && !self.play_confirmed {
            if let Some(started) = self.loading_started {
                // The gateway has no simulation loop yet. Quantize monotonic elapsed
                // time into nominal 20 Hz server ticks, independent of client traffic.
                self.loading_ticks = self.loading_ticks.max(
                    (now.saturating_duration_since(started).as_nanos() / SERVER_TICK.as_nanos())
                        .min(60) as u8,
                );
            }
            self.confirm_play(writer)?;
            if self.state == ConnectionState::Closed {
                return Ok(());
            }
        }
        if self.keepalive.is_none() && now >= self.next_keepalive {
            let id = now
                .duration_since(self.configuration_started.expect("Configuration clock"))
                .as_millis() as i64;
            if self.state == ConnectionState::Play {
                play::Clientbound::KeepAlive(id).write(writer)?;
            } else {
                configuration::Clientbound::KeepAlive(id).write(writer)?;
            }
            // Measure after the successful write; retain the original deadline clock.
            self.keepalive_sent = Some((self.clock)());
            self.keepalive = Some((id, now));
            self.next_keepalive = now + KEEPALIVE_INTERVAL;
        }
        self.flush_chunk_interest(now, writer)?;
        Ok(())
    }

    fn flush_chunk_interest(&mut self, now: Instant, writer: &mut impl Write) -> io::Result<()> {
        if self.state != ConnectionState::Play || now < self.next_chunk_batch {
            return Ok(());
        }
        let Some(center) = self.pending_chunk_center.take() else {
            return Ok(());
        };
        let desired = chunk_window(center, self.chunk_radius);
        let unload: Vec<_> = self.loaded_chunks.difference(&desired).copied().collect();
        let load: Vec<_> = desired.difference(&self.loaded_chunks).copied().collect();
        let world = rusty_mines::vanilla_world::world().map_err(invalid_data)?;
        if center != self.chunk_center {
            play::write_official_packet(
                writer,
                &play::chunk_center_at(&world.initialization[3], center.0, center.1)
                    .map_err(invalid_data)?,
            )?;
        }
        if self.chunk_radius != self.streamed_radius {
            play::write_official_packet(
                writer,
                &play::chunk_distance(
                    &world.initialization[4],
                    play::RENDER_DISTANCE,
                    self.chunk_radius,
                )
                .map_err(invalid_data)?,
            )?;
            play::write_official_packet(
                writer,
                &play::chunk_distance(
                    &world.initialization[5],
                    play::SIMULATION_DISTANCE,
                    self.chunk_radius,
                )
                .map_err(invalid_data)?,
            )?;
        }
        for (x, z) in unload {
            play::write_official_packet(
                writer,
                &play::unload_chunk_at(&world.unload_template, x, z).map_err(invalid_data)?,
            )?;
        }
        if !load.is_empty() {
            play::write_official_packet(writer, &world.initialization[6])?;
            let mut bytes = 0usize;
            let mut loaded_chunks = Vec::new();
            for (x, z) in &load {
                let chunk = play::chunk_at(&world.chunk_template, *x, *z).map_err(invalid_data)?;
                bytes = bytes
                    .checked_add(chunk.len().saturating_add(5))
                    .filter(|total| *total <= 2 * 1024 * 1024)
                    .ok_or_else(|| invalid_data("chunk update exceeds byte budget"))?;
                play::write_official_packet(writer, &chunk)?;
                loaded_chunks.push((*x, *z));
            }
            let session = self
                .backend_session
                .ok_or_else(|| io::Error::other("Chunk load requires backend session"))?;
            if !self.backend.requires_persistent_world_snapshots() {
                let finished = play::chunk_batch_finished(
                    &world.initialization[world.initialization.len() - 2],
                    load.len(),
                )
                .map_err(invalid_data)?;
                play::write_official_packet(writer, &finished)?;
                self.loaded_chunks = desired;
                self.chunk_revisions
                    .retain(|key, _| self.loaded_chunks.contains(key));
                self.chunk_center = center;
                self.streamed_radius = self.chunk_radius;
                self.next_chunk_batch = now + SERVER_TICK;
                return Ok(());
            }
            if self.backend.requires_persistent_world_snapshots() {
                for (x, z) in &loaded_chunks {
                    if let Some(snapshot) = self.backend.chunk_snapshot(session, *x, *z)? {
                        decode_block_overrides(&snapshot, *x, *z)?;
                    }
                }
            }
            let mut loaded_revisions = Vec::new();
            for (x, z) in loaded_chunks {
                let Some(snapshot) = self.backend.chunk_snapshot(session, x, z)? else {
                    if self.backend.requires_persistent_world_snapshots() {
                        return Err(io::Error::other(
                            "Chunk override snapshot is unavailable; refusing stale batch",
                        ));
                    }
                    continue;
                };
                let overrides = decode_block_overrides(&snapshot, x, z)?;
                for ((block_x, block_y, block_z), state) in &overrides {
                    play::Clientbound::BlockUpdate(
                        play::BlockPosition {
                            x: *block_x,
                            y: *block_y,
                            z: *block_z,
                        },
                        *state,
                    )
                    .write(writer)?;
                }
                loaded_revisions.push(((x, z), snapshot.revision));
            }
            let finished = play::chunk_batch_finished(
                &world.initialization[world.initialization.len() - 2],
                load.len(),
            )
            .map_err(invalid_data)?;
            play::write_official_packet(writer, &finished)?;
            for (key, revision) in loaded_revisions {
                self.chunk_revisions.insert(key, revision);
            }
        }
        self.loaded_chunks = desired;
        self.chunk_revisions
            .retain(|key, _| self.loaded_chunks.contains(key));
        self.chunk_center = center;
        self.streamed_radius = self.chunk_radius;
        self.next_chunk_batch = now + SERVER_TICK;
        Ok(())
    }

    fn apply_replication_event(
        &mut self,
        event: crate::players::ReplicationEvent,
        writer: &mut impl Write,
    ) -> io::Result<()> {
        use crate::packets::write_frame;
        use crate::players::ReplicationEvent;
        fn write_spawn_bundle(
            writer: &mut impl Write,
            info: &[u8],
            spawn: &[u8],
        ) -> io::Result<()> {
            write_frame(writer, 0x00, &[])?;
            write_frame(writer, i32::from(info[0]), &info[1..])?;
            write_frame(writer, i32::from(spawn[0]), &spawn[1..])?;
            write_frame(writer, 0x00, &[])
        }
        match event {
            ReplicationEvent::Upsert(row) => {
                let id = uuid::Uuid::from_u128(row.session_uuid.as_u128());
                let player = play::PlayerEntity {
                    entity_id: row.entity_id,
                    player_uuid: uuid::Uuid::from_u128(row.player_uuid.as_u128()),
                    username: row.username,
                    position: [row.x, row.y, row.z],
                    rotation: [row.yaw, row.pitch],
                    on_ground: row.on_ground,
                };
                if let Some(previous) = self.replicated_players.get(&id) {
                    if previous.entity_id != player.entity_id
                        || previous.player_uuid != player.player_uuid
                    {
                        let (entity_remove, profile_remove) =
                            play::player_remove_packets(previous.player_uuid, previous.entity_id)
                                .map_err(invalid_data)?;
                        write_frame(writer, i32::from(entity_remove[0]), &entity_remove[1..])?;
                        write_frame(writer, i32::from(profile_remove[0]), &profile_remove[1..])?;
                        self.replicated_players.remove(&id);
                    } else {
                        let delta = [
                            player.position[0] - previous.position[0],
                            player.position[1] - previous.position[1],
                            player.position[2] - previous.position[2],
                        ];
                        let rotation_changed = player.rotation != previous.rotation;
                        let changed = delta != [0.0; 3]
                            || rotation_changed
                            || player.on_ground != previous.on_ground;
                        if changed {
                            let relative_overflow = delta.iter().any(|d| {
                                let scaled = (d * 4096.0).round();
                                scaled < i16::MIN as f64 || scaled > i16::MAX as f64
                            });
                            if relative_overflow {
                                let (entity_remove, profile_remove) = play::player_remove_packets(
                                    previous.player_uuid,
                                    previous.entity_id,
                                )
                                .map_err(invalid_data)?;
                                write_frame(
                                    writer,
                                    i32::from(entity_remove[0]),
                                    &entity_remove[1..],
                                )?;
                                write_frame(
                                    writer,
                                    i32::from(profile_remove[0]),
                                    &profile_remove[1..],
                                )?;
                                let (info, spawn) =
                                    play::player_spawn_packets(&player).map_err(invalid_data)?;
                                write_spawn_bundle(writer, &info, &spawn)?;
                            } else {
                                let movement =
                                    play::player_move_packet(&player, delta, rotation_changed)
                                        .map_err(invalid_data)?;
                                write_frame(writer, i32::from(movement[0]), &movement[1..])?;
                            }
                        }
                        self.replicated_players.insert(id, player);
                        return Ok(());
                    }
                }
                let (info, spawn) = play::player_spawn_packets(&player).map_err(invalid_data)?;
                write_spawn_bundle(writer, &info, &spawn)?;
                self.replicated_players.insert(id, player);
            }
            ReplicationEvent::Remove(id) => {
                if let Some(previous) = self.replicated_players.remove(&id) {
                    let (entity, profile) =
                        play::player_remove_packets(previous.player_uuid, previous.entity_id)
                            .map_err(invalid_data)?;
                    write_frame(writer, i32::from(entity[0]), &entity[1..])?;
                    write_frame(writer, i32::from(profile[0]), &profile[1..])?;
                }
            }
        }
        Ok(())
    }

    fn handle_handshake(&mut self, data: &[u8], writer: &mut impl Write) -> io::Result<()> {
        let handshake = HandshakePacket::decode(data).map_err(invalid_data)?;

        self.protocol_version = Some(handshake.protocol_version.value());
        self.transferred = handshake.intent == Intent::Transfer;
        self.deadline = (self.clock)() + LOGIN_TIMEOUT;
        self.state = match handshake.intent {
            Intent::Status => ConnectionState::Status,
            Intent::Login | Intent::Transfer => ConnectionState::Login,
        };

        if self.state == ConnectionState::Login {
            if self.transferred {
                return self.disconnect_login(writer, "Server transfers are not supported");
            }
            if self.protocol_version != Some(login::PROTOCOL_VERSION) {
                return self
                    .disconnect_login(writer, "Unsupported protocol: use Minecraft 26.3 (777)");
            }
        }

        console_event!(
            "[{}] Handshake: version={}, address={}:{}, intent={:?}",
            self.peer_addr,
            handshake.protocol_version.value(),
            handshake.server_address.as_str(),
            handshake.server_port,
            handshake.intent,
        );
        Ok(())
    }
}

#[cfg(test)]
pub(crate) fn handle_client(
    stream: TcpStream,
    traffic: Arc<Traffic>,
    backend: Arc<dyn SessionStore>,
) -> io::Result<()> {
    handle_controlled_client(
        stream,
        traffic,
        backend,
        Arc::new(crate::players::Registry::default()),
    )
}

pub(crate) fn handle_controlled_client(
    stream: TcpStream,
    traffic: Arc<Traffic>,
    backend: Arc<dyn SessionStore>,
    controls: Arc<crate::players::Registry>,
) -> io::Result<()> {
    let peer_addr = stream.peer_addr()?;
    // Windows accepted sockets can inherit the listener's nonblocking mode.
    // Client workers use blocking I/O with timeouts, unlike the accept loop.
    stream.set_nonblocking(false)?;
    stream.set_read_timeout(Some(READ_POLL))?;
    stream.set_write_timeout(Some(Duration::from_secs(5)))?;
    console_event!("New connection from {peer_addr}");

    let mut session = ClientSession::new(peer_addr, traffic.clone(), backend);
    session.controls = controls;
    let mut reader = BufReader::new(CountingIo::new(stream, traffic));
    let mut buffer = [0u8; 1024];

    loop {
        session.poll(Instant::now(), reader.get_mut())?;
        if session.state == ConnectionState::Closed {
            break;
        }
        let count = match reader.read(&mut buffer) {
            Ok(count) => count,
            Err(error) if error.kind() == io::ErrorKind::Interrupted => continue,
            Err(error)
                if matches!(
                    error.kind(),
                    io::ErrorKind::TimedOut | io::ErrorKind::WouldBlock
                ) =>
            {
                continue;
            }
            Err(error) => return Err(error),
        };

        if count == 0 {
            if !session.pending.is_empty() {
                return Err(io::Error::new(
                    io::ErrorKind::UnexpectedEof,
                    "connection closed during a packet",
                ));
            }

            console_event!("[{peer_addr}] Connection closed");
            break;
        }

        session.receive(&buffer[..count], reader.get_mut())?;
        if session.state == ConnectionState::Closed {
            break;
        }
    }

    session.control.take();
    if session.state == ConnectionState::Closed {
        graceful_close(&mut reader)?;
    }
    let ack = session.kick_ack.take();
    drop(session);
    if let Some(ack) = ack {
        let _ = ack.send(Ok(()));
    }
    console_event!("[{peer_addr}] Disconnected");
    Ok(())
}

/// Half-close first so the peer sees the reason and EOF before we drain pipelined input.
/// Raw reads still count bytes, but are deliberately not Minecraft packet events.
fn graceful_close(reader: &mut BufReader<CountingIo<TcpStream>>) -> io::Result<()> {
    reader.get_mut().inner.shutdown(Shutdown::Write)?;
    reader
        .get_mut()
        .inner
        .set_read_timeout(Some(Duration::from_millis(50)))?;
    let deadline = Instant::now() + Duration::from_secs(1);
    let mut remaining = 256 * 1024;
    let mut buffer = [0u8; 4096];
    while remaining > 0 && Instant::now() < deadline {
        let length = remaining.min(buffer.len());
        match reader.read(&mut buffer[..length]) {
            Ok(0) => break,
            Ok(count) => remaining -= count,
            Err(error) if error.kind() == io::ErrorKind::Interrupted => continue,
            Err(error)
                if matches!(
                    error.kind(),
                    io::ErrorKind::TimedOut | io::ErrorKind::WouldBlock
                ) =>
            {
                continue
            }
            Err(error)
                if matches!(
                    error.kind(),
                    io::ErrorKind::ConnectionReset | io::ErrorKind::ConnectionAborted
                ) =>
            {
                break
            }
            Err(error) => return Err(error),
        }
    }
    Ok(())
}

// Offline mode never trusts or stores this key. Only consume a bounded wire shape.
fn invalid_data(message: impl Into<String>) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, message.into())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::packets::write_frame;

    fn packet_ids(bytes: &[u8]) -> Vec<i32> {
        let mut rest = bytes;
        let mut ids = Vec::new();
        while let Some((packet, consumed)) = parse_packet(rest, None, false).unwrap() {
            ids.push(packet.id);
            rest = &rest[consumed..];
        }
        assert!(rest.is_empty());
        ids
    }

    #[test]
    fn multiplayer_replication_orders_spawn_move_fallback_and_remove() {
        let backend = Arc::new(RecordingBackend::default());
        let mut session = ClientSession::new(
            "127.0.0.1:25565".parse().unwrap(),
            Arc::new(Traffic::default()),
            backend,
        );
        let session_id = uuid::Uuid::new_v4();
        let player_uuid = uuid::Uuid::new_v4();
        let mut row = crate::module_bindings::GatewayWorldPlayer {
            session_uuid: spacetimedb_sdk::Uuid::from_u128(session_id.as_u128()),
            player_uuid: spacetimedb_sdk::Uuid::from_u128(player_uuid.as_u128()),
            username: "Alex".into(),
            entity_id: 123,
            x: 8.5,
            y: 65.0,
            z: 8.5,
            yaw: 0.0,
            pitch: 0.0,
            on_ground: false,
            revision: 1,
        };
        let mut output = Vec::new();
        session
            .apply_replication_event(
                crate::players::ReplicationEvent::Upsert(row.clone()),
                &mut output,
            )
            .unwrap();
        assert_eq!(packet_ids(&output), vec![0x00, 0x47, 0x01, 0x00]);
        output.clear();

        row.x += 0.25;
        row.yaw = 90.0;
        row.revision += 1;
        session
            .apply_replication_event(
                crate::players::ReplicationEvent::Upsert(row.clone()),
                &mut output,
            )
            .unwrap();
        assert_eq!(packet_ids(&output), vec![0x37]);
        output.clear();

        row.x += 20.0;
        row.revision += 1;
        session
            .apply_replication_event(
                crate::players::ReplicationEvent::Upsert(row.clone()),
                &mut output,
            )
            .unwrap();
        assert_eq!(
            packet_ids(&output),
            vec![0x4e, 0x46, 0x00, 0x47, 0x01, 0x00]
        );
        output.clear();

        session
            .apply_replication_event(
                crate::players::ReplicationEvent::Remove(session_id),
                &mut output,
            )
            .unwrap();
        assert_eq!(packet_ids(&output), vec![0x4e, 0x46]);
    }

    #[test]
    fn worker_disconnects_when_replication_or_chat_queues_overflow() {
        for overflow_chat in [false, true] {
            let controls = Arc::new(crate::players::Registry::default());
            let recipient_id = uuid::Uuid::new_v4();
            let guard = controls.register(recipient_id, "Recipient".into());
            controls.mark_playing(recipient_id);
            if overflow_chat {
                for _ in 0..=crate::players::REPLICATION_QUEUE_LIMIT {
                    controls.publish_chat(crate::players::ChatDelivery::new(
                        "Sender".into(),
                        "bounded message".into(),
                    ));
                }
            } else {
                let source_id = uuid::Uuid::new_v4();
                let row = crate::module_bindings::GatewayWorldPlayer {
                    session_uuid: spacetimedb_sdk::Uuid::from_u128(source_id.as_u128()),
                    player_uuid: spacetimedb_sdk::Uuid::from_u128(uuid::Uuid::new_v4().as_u128()),
                    username: "Source".into(),
                    entity_id: 42,
                    x: 8.5,
                    y: 65.0,
                    z: 8.5,
                    yaw: 0.0,
                    pitch: 0.0,
                    on_ground: true,
                    revision: 1,
                };
                for _ in 0..=crate::players::REPLICATION_QUEUE_LIMIT {
                    controls
                        .publish_replication(crate::players::ReplicationEvent::Upsert(row.clone()));
                }
            }

            let mut session = session();
            session.state = ConnectionState::Play;
            session.play_confirmed = true;
            session.backend_session = Some(uuid::Uuid::new_v4());
            session.controls = controls;
            session.control = Some(guard);
            let mut output = Vec::new();
            session.poll(Instant::now(), &mut output).unwrap();
            assert_eq!(session.state, ConnectionState::Closed);
            assert!(!output.is_empty());
        }
    }

    #[derive(Default)]
    struct InventoryBackend {
        recorder: RecordingBackend,
        snapshot: std::sync::Mutex<Option<crate::module_bindings::InventorySnapshot>>,
        calls: std::sync::Mutex<Vec<(u64, Vec<u8>)>>,
        results: std::sync::Mutex<Vec<std::sync::mpsc::Sender<io::Result<()>>>>,
    }

    impl SessionStore for InventoryBackend {
        fn begin(&self, session: uuid::Uuid, name: &str) -> io::Result<()> {
            self.recorder.begin(session, name)
        }
        fn configure(&self, session: uuid::Uuid) -> io::Result<i32> {
            self.recorder.configure(session)
        }
        fn play(&self, session: uuid::Uuid) -> io::Result<()> {
            self.recorder.play(session)
        }
        fn end(&self, session: uuid::Uuid) {
            self.recorder.end(session);
        }
        fn inventory_snapshot(
            &self,
            _: uuid::Uuid,
        ) -> io::Result<Option<crate::module_bindings::InventorySnapshot>> {
            Ok(self.snapshot.lock().unwrap().clone())
        }
        fn inventory_submit(
            &self,
            _: uuid::Uuid,
            sequence: u64,
            payload: Vec<u8>,
        ) -> io::Result<std::sync::mpsc::Receiver<io::Result<()>>> {
            self.calls.lock().unwrap().push((sequence, payload));
            let (sender, receiver) = std::sync::mpsc::channel();
            self.results.lock().unwrap().push(sender);
            Ok(receiver)
        }
    }

    fn inventory_session() -> (ClientSession, Arc<InventoryBackend>) {
        let backend = Arc::new(InventoryBackend::default());
        let mut session = initialized_m1(backend.clone());
        let state = rusty_mines_inventory::model::State::default();
        *backend.snapshot.lock().unwrap() = Some(crate::module_bindings::InventorySnapshot {
            session_uuid: spacetimedb_sdk::Uuid::from_u128(
                session.backend_session.unwrap().as_u128(),
            ),
            owner_connection: spacetimedb_sdk::ConnectionId::from_u128(1),
            revision: 0,
            selected: 0,
            slots: rusty_mines_inventory::model::encode_slots(&state.slots),
            cursor: vec![0],
            menu_id: 0,
            menu_title: String::new(),
            sequence: 0,
            creative_allowed: false,
        });
        session
            .receive(
                &[pending_ack(&session), play_frame(0x2c, &[])].concat(),
                &mut Vec::new(),
            )
            .unwrap();
        assert!(session.play_confirmed);
        assert_eq!(session.inventory_snapshot.as_ref().unwrap().sequence, 0);
        (session, backend)
    }

    #[test]
    fn inventory_confirmation_requires_callback_and_matching_subscribed_sequence() {
        for cache_first in [false, true] {
            let (mut session, backend) = inventory_session();
            session
                .queue_inventory(0, vec![1], None, &mut Vec::new())
                .unwrap();
            assert_eq!(session.inventory_snapshot.as_ref().unwrap().selected, 0);
            if cache_first {
                let mut row = backend.snapshot.lock().unwrap();
                let row = row.as_mut().unwrap();
                row.sequence = 1;
                row.revision = 1;
                row.selected = 1;
            } else {
                backend.results.lock().unwrap()[0].send(Ok(())).unwrap();
            }
            session.refresh_inventory(&mut Vec::new()).unwrap();
            assert!(session.inventory_pending.is_some());
            if cache_first {
                backend.results.lock().unwrap()[0].send(Ok(())).unwrap();
            } else {
                let mut row = backend.snapshot.lock().unwrap();
                let row = row.as_mut().unwrap();
                row.sequence = 1;
                row.revision = 1;
                row.selected = 1;
            }
            session.refresh_inventory(&mut Vec::new()).unwrap();
            assert!(session.inventory_pending.is_none());
            assert_eq!(session.held_slot, 1);
        }
    }

    #[test]
    fn inventory_flood_keeps_one_pending_request_and_rejection_resynchronizes() {
        let (mut session, backend) = inventory_session();
        for _ in 0..1000 {
            session
                .queue_inventory(0, vec![1], None, &mut Vec::new())
                .unwrap();
        }
        assert_eq!(backend.calls.lock().unwrap().len(), 1);
        assert_eq!(backend.results.lock().unwrap().len(), 1);
        assert_eq!(session.held_slot, 0);
        backend.results.lock().unwrap()[0]
            .send(Err(io::Error::other("denied")))
            .unwrap();
        let mut output = Vec::new();
        session.refresh_inventory(&mut output).unwrap();
        assert!(!output.is_empty());
        assert!(session.inventory_pending.is_none());
        assert_eq!(session.held_slot, 0);
    }

    #[test]
    fn inventory_timeout_applies_even_after_callback_without_matching_snapshot() {
        let (mut session, backend) = inventory_session();
        session
            .queue_inventory(0, vec![1], None, &mut Vec::new())
            .unwrap();
        backend.results.lock().unwrap()[0].send(Ok(())).unwrap();
        let deadline = session.inventory_pending.as_ref().unwrap().deadline;
        session.clock = Arc::new(move || deadline);
        session.refresh_inventory(&mut Vec::new()).unwrap();
        assert_eq!(session.state, ConnectionState::Closed);
        assert!(session.inventory_pending.is_none());
        drop(session);
        assert_eq!(
            backend
                .recorder
                .events
                .lock()
                .unwrap()
                .iter()
                .filter(|(event, _)| *event == "end")
                .count(),
            1
        );
    }

    #[test]
    fn inventory_close_discards_callback_and_late_result_cannot_update_closed_session() {
        let (mut session, backend) = inventory_session();
        session
            .queue_inventory(0, vec![1], None, &mut Vec::new())
            .unwrap();
        session.disconnect_play(&mut Vec::new(), "closed").unwrap();
        assert!(backend.results.lock().unwrap()[0].send(Ok(())).is_err());
        let mut output = Vec::new();
        session.refresh_inventory(&mut output).unwrap();
        assert!(output.is_empty());
        assert_eq!(session.held_slot, 0);
        drop(session);
        assert_eq!(
            backend
                .recorder
                .events
                .lock()
                .unwrap()
                .iter()
                .filter(|(event, _)| *event == "end")
                .count(),
            1
        );
    }

    #[test]
    fn inventory_foreign_session_snapshot_is_never_written() {
        let (mut session, backend) = inventory_session();
        backend
            .snapshot
            .lock()
            .unwrap()
            .as_mut()
            .unwrap()
            .session_uuid = spacetimedb_sdk::Uuid::from_u128(99);
        session.refresh_inventory(&mut Vec::new()).unwrap();
        assert_eq!(session.state, ConnectionState::Closed);
        assert_ne!(
            session.inventory_snapshot.as_ref().unwrap().session_uuid,
            spacetimedb_sdk::Uuid::from_u128(99)
        );
    }

    #[test]
    fn inventory_deferred_menu_close_waits_for_pending_action_and_reopens_on_rejection() {
        let (mut session, backend) = inventory_session();
        {
            let mut row = backend.snapshot.lock().unwrap();
            let row = row.as_mut().unwrap();
            row.menu_id = 7;
            row.menu_title = "Storage".into();
            row.slots = rusty_mines_inventory::model::encode_slots(
                &vec![rusty_mines_inventory::wire::Stack::empty(); 63],
            );
        }
        session.refresh_inventory(&mut Vec::new()).unwrap();
        session
            .queue_inventory(0, vec![1], None, &mut Vec::new())
            .unwrap();
        session
            .receive(&play_frame(0x13, &[7]), &mut Vec::new())
            .unwrap();
        assert_eq!(session.inventory_deferred_close, Some(7));
        assert_eq!(backend.calls.lock().unwrap().len(), 1);
        backend.results.lock().unwrap()[0].send(Ok(())).unwrap();
        backend.snapshot.lock().unwrap().as_mut().unwrap().sequence = 1;
        session.refresh_inventory(&mut Vec::new()).unwrap();
        assert_eq!(backend.calls.lock().unwrap().len(), 2);
        assert!(session.inventory_deferred_close.is_none());
        assert!(session.inventory_pending.is_some());
        backend.results.lock().unwrap()[1]
            .send(Err(io::Error::other("close rejected")))
            .unwrap();
        let mut output = Vec::new();
        session.refresh_inventory(&mut output).unwrap();
        assert!(!output.is_empty());
        assert!(session.inventory_pending.is_none());
        assert_eq!(session.inventory_snapshot.as_ref().unwrap().menu_id, 7);
        assert!(!session.inventory_reopen_menu);
    }

    #[test]
    fn inventory_stale_menu_or_revision_resyncs_without_reducer_call() {
        for (menu, revision) in [(1, 0), (0, 1)] {
            let (mut session, backend) = inventory_session();
            // Empty changed slots and empty hashed cursor are legal click evidence.
            let packet = [menu, revision, 0, 9, 0, 0, 0, 0];
            let mut output = Vec::new();
            session
                .receive(&play_frame(0x12, &packet), &mut output)
                .unwrap();
            assert_eq!(session.state, ConnectionState::Play);
            assert!(!output.is_empty());
            assert!(backend.calls.lock().unwrap().is_empty());
        }
    }

    #[test]
    fn inventory_missing_snapshot_and_callback_channel_loss_fail_closed() {
        for missing_snapshot in [true, false] {
            let (mut session, backend) = inventory_session();
            session
                .queue_inventory(0, vec![1], None, &mut Vec::new())
                .unwrap();
            if missing_snapshot {
                *backend.snapshot.lock().unwrap() = None;
            } else {
                backend.results.lock().unwrap().clear();
            }
            session.refresh_inventory(&mut Vec::new()).unwrap();
            assert_eq!(session.state, ConnectionState::Closed);
            assert!(session.inventory_pending.is_none());
        }
    }

    fn initialized_m1(backend: Arc<dyn SessionStore>) -> ClientSession {
        let mut s = backend_session(backend);
        s.receive(
            &[
                handshake(2),
                login_start("Notch"),
                vec![1, 3],
                configuration_responses(),
            ]
            .concat(),
            &mut Vec::new(),
        )
        .unwrap();
        s
    }

    fn pending_ack(s: &ClientSession) -> Vec<u8> {
        let p = s.pending_teleport.unwrap().pose;
        let mut data = crate::data_type::VarInt::new(p.id).encode();
        for v in p.position {
            data.extend(v.to_be_bytes());
        }
        for v in p.rotation {
            data.extend(v.to_be_bytes());
        }
        play_frame(0, &data)
    }

    #[test]
    fn unsigned_chat_uses_backend_confirmation_and_signed_chat_is_never_submitted() {
        struct ChatBackend {
            recorder: RecordingBackend,
            sent: std::sync::Mutex<Vec<String>>,
        }
        impl SessionStore for ChatBackend {
            fn begin(&self, id: uuid::Uuid, name: &str) -> io::Result<()> {
                self.recorder.begin(id, name)
            }
            fn configure(&self, id: uuid::Uuid) -> io::Result<i32> {
                self.recorder.configure(id)
            }
            fn play(&self, id: uuid::Uuid) -> io::Result<()> {
                self.recorder.play(id)
            }
            fn chat_send(
                &self,
                _: uuid::Uuid,
                text: String,
            ) -> io::Result<std::sync::mpsc::Receiver<io::Result<()>>> {
                self.sent.lock().unwrap().push(text);
                let (sender, receiver) = std::sync::mpsc::channel();
                sender.send(Ok(())).unwrap();
                Ok(receiver)
            }
            fn end(&self, id: uuid::Uuid) {
                self.recorder.end(id);
            }
        }
        fn chat_frame(text: &str, signed: bool) -> Vec<u8> {
            let mut data = crate::data_type::VarInt::new(text.len() as i32).encode();
            data.extend(text.as_bytes());
            data.extend([0; 16]);
            data.push(u8::from(signed));
            if signed {
                data.extend([0; 256]);
            }
            data.extend(crate::data_type::VarInt::new(0).encode()); // last-seen offset
            data.extend([0; 4]);
            play_frame(0x09, &data)
        }
        let backend = Arc::new(ChatBackend {
            recorder: RecordingBackend::default(),
            sent: std::sync::Mutex::new(Vec::new()),
        });
        let mut session = initialized_m1(backend.clone());
        session
            .receive(
                &[pending_ack(&session), play_frame(0x2c, &[])].concat(),
                &mut Vec::new(),
            )
            .unwrap();
        assert!(session.play_confirmed);
        let mut output = Vec::new();
        session
            .receive(&chat_frame("hello", false), &mut output)
            .unwrap();
        assert_eq!(session.state, ConnectionState::Play);
        assert_eq!(*backend.sent.lock().unwrap(), vec!["hello"]);

        session
            .receive(&chat_frame("unverified", true), &mut output)
            .unwrap();
        assert_eq!(*backend.sent.lock().unwrap(), vec!["hello"]);
        assert_eq!(session.state, ConnectionState::Play);
        assert!(output
            .windows(b"Signed chat is not verified or accepted".len())
            .any(|window| window == b"Signed chat is not verified or accepted"));
    }

    #[test]
    fn wire_login_uses_backend_entity_id_and_rejects_invalid_ids_before_world_output() {
        struct EntityBackend {
            id: i32,
            recorder: RecordingBackend,
        }
        impl SessionStore for EntityBackend {
            fn begin(&self, id: uuid::Uuid, name: &str) -> io::Result<()> {
                self.recorder.begin(id, name)
            }
            fn configure(&self, id: uuid::Uuid) -> io::Result<i32> {
                self.recorder.configure(id)?;
                Ok(self.id)
            }
            fn end(&self, id: uuid::Uuid) {
                self.recorder.end(id)
            }
        }
        for id in [1, 16384, i32::MAX, 0, -1, i32::MIN] {
            let backend = Arc::new(EntityBackend {
                id,
                recorder: RecordingBackend::default(),
            });
            let mut s = backend_session(backend.clone());
            let mut out = Vec::new();
            s.receive(
                &[
                    handshake(2),
                    login_start("Notch"),
                    vec![1, 3],
                    configuration_responses(),
                ]
                .concat(),
                &mut out,
            )
            .unwrap();
            let mut frames = out.as_slice();
            let mut wire_ids = Vec::new();
            while let Some((packet, consumed)) = parse_packet(frames, None, false).unwrap() {
                if packet.id == 0x32 {
                    wire_ids.push(i32::from_be_bytes(packet.data[..4].try_into().unwrap()));
                }
                frames = &frames[consumed..];
            }
            assert!(frames.is_empty());
            if id > 0 {
                assert_eq!(wire_ids, vec![id]);
                assert_eq!(s.state, ConnectionState::Play);
                assert_eq!(s.entity_id, Some(id));
            } else {
                assert!(wire_ids.is_empty());
                assert_eq!(s.state, ConnectionState::Closed);
                assert!(s.entity_id.is_none());
            }
            drop(s);
            assert_eq!(
                backend
                    .recorder
                    .events
                    .lock()
                    .unwrap()
                    .iter()
                    .filter(|(event, _)| *event == "end")
                    .count(),
                1
            );
        }
    }

    #[test]
    fn backend_session_removal_closes_idle_workers_in_each_wire_phase() {
        struct LiveBackend {
            live: std::sync::atomic::AtomicBool,
            recorder: RecordingBackend,
        }
        impl SessionStore for LiveBackend {
            fn begin(&self, id: uuid::Uuid, name: &str) -> io::Result<()> {
                self.recorder.begin(id, name)
            }
            fn configure(&self, id: uuid::Uuid) -> io::Result<i32> {
                self.recorder.configure(id)
            }
            fn play(&self, id: uuid::Uuid) -> io::Result<()> {
                self.recorder.play(id)
            }
            fn check_session(&self, _: uuid::Uuid) -> io::Result<()> {
                if self.live.load(std::sync::atomic::Ordering::Acquire) {
                    Ok(())
                } else {
                    Err(io::Error::new(
                        io::ErrorKind::PermissionDenied,
                        "Session removed",
                    ))
                }
            }
            fn end(&self, id: uuid::Uuid) {
                self.recorder.end(id)
            }
        }
        for phase in [
            ConnectionState::Login,
            ConnectionState::Configuration,
            ConnectionState::Play,
        ] {
            let backend = Arc::new(LiveBackend {
                live: std::sync::atomic::AtomicBool::new(true),
                recorder: RecordingBackend::default(),
            });
            let mut s = backend_session(backend.clone());
            let mut request = [handshake(2), login_start("Notch")].concat();
            if phase != ConnectionState::Login {
                request.extend([1, 3]);
            }
            if phase == ConnectionState::Play {
                request.extend(configuration_responses());
            }
            let mut out = Vec::new();
            s.receive(&request, &mut out).unwrap();
            assert_eq!(s.state, phase);
            if phase == ConnectionState::Play {
                s.receive(&[pending_ack(&s), play_frame(0x2c, &[])].concat(), &mut out)
                    .unwrap();
                assert!(s.play_confirmed);
            }
            backend
                .live
                .store(false, std::sync::atomic::Ordering::Release);
            out.clear();
            s.poll((s.clock)(), &mut out).unwrap();
            assert_eq!(s.state, ConnectionState::Closed);
            let (packet, consumed) = parse_packet(&out, None, false).unwrap().unwrap();
            assert_eq!(
                packet.id,
                match phase {
                    ConnectionState::Login => 0,
                    ConnectionState::Configuration => 2,
                    ConnectionState::Play => 0x20,
                    _ => unreachable!(),
                }
            );
            assert_eq!(consumed, out.len());
            drop(s);
            assert_eq!(
                backend
                    .recorder
                    .events
                    .lock()
                    .unwrap()
                    .iter()
                    .filter(|(event, _)| *event == "end")
                    .count(),
                1
            );
        }
    }

    #[test]
    fn m1_loading_ticks_orders_saturation_reset_and_replay() {
        for ack_first in [false, true] {
            let backend = Arc::new(RecordingBackend::default());
            let mut s = initialized_m1(backend.clone());
            let mut out = Vec::new();
            let started = s.loading_started.unwrap();
            let now = Arc::new(std::sync::Mutex::new(started));
            let clock = now.clone();
            s.clock = Arc::new(move || *clock.lock().unwrap());
            let ack = pending_ack(&s);
            if ack_first {
                s.receive(&ack, &mut out).unwrap();
            }
            // A malicious burst of client tick packets cannot expire the server timer.
            for _ in 0..100 {
                s.receive(&play_frame(0x0d, &[]), &mut out).unwrap();
            }
            assert_eq!(s.loading_ticks, 0);
            assert!(!s.play_confirmed);
            for n in 1..=61 {
                if n == 60 {
                    let before = started + SERVER_TICK * 60 - Duration::from_nanos(1);
                    *now.lock().unwrap() = before;
                    s.poll(before, &mut out).unwrap();
                    assert_eq!(s.loading_ticks, 59);
                    assert!(!s.play_confirmed);
                }
                *now.lock().unwrap() = started + SERVER_TICK * u32::from(n);
                // Idle sockets also reach the fallback, without client packets.
                let current = *now.lock().unwrap();
                s.poll(current, &mut out).unwrap();
                assert_eq!(s.loading_ticks, n.min(60));
                assert_eq!(s.play_confirmed, ack_first && n >= 60);
            }
            if !ack_first {
                s.receive(&ack, &mut out).unwrap();
            }
            assert!(s.play_confirmed);
            assert_eq!(
                backend
                    .events
                    .lock()
                    .unwrap()
                    .iter()
                    .filter(|(e, _)| *e == "play")
                    .count(),
                1
            );
            s.receive(&play_frame(0x2c, &[]), &mut out).unwrap();
            assert_eq!(s.state, ConnectionState::Play);
            s.receive(&play_frame(0x2c, &[]), &mut out).unwrap();
            assert_eq!(s.state, ConnectionState::Closed);
            s.reset_loading();
            assert_eq!(s.loading_ticks, 0);
            assert!(!s.teleport_confirmed && !s.player_loaded && !s.play_confirmed);
            assert_eq!(s.loading_started, Some(*now.lock().unwrap()));
        }
    }

    #[test]
    fn idle_loading_fallback_refusal_closes_and_cleans_session() {
        struct RefusesPlay(RecordingBackend);
        impl SessionStore for RefusesPlay {
            fn begin(&self, id: uuid::Uuid, name: &str) -> io::Result<()> {
                self.0.begin(id, name)
            }
            fn configure(&self, id: uuid::Uuid) -> io::Result<i32> {
                self.0.configure(id)
            }
            fn end(&self, id: uuid::Uuid) {
                self.0.end(id)
            }
        }
        let backend = Arc::new(RefusesPlay(RecordingBackend::default()));
        let mut s = initialized_m1(backend.clone());
        let id = s.backend_session.unwrap();
        let mut out = Vec::new();
        s.receive(&pending_ack(&s), &mut out).unwrap();
        s.poll(s.loading_started.unwrap() + SERVER_TICK * 60, &mut out)
            .unwrap();
        assert_eq!(s.state, ConnectionState::Closed);
        assert!(!s.play_confirmed);
        drop(s);
        assert_eq!(backend.0.events.lock().unwrap().last(), Some(&("end", id)));
    }

    #[test]
    fn m1_corrections_coalesce_ack_exactly_and_keep_readiness() {
        let backend = Arc::new(RecordingBackend::default());
        let mut s = initialized_m1(backend.clone());
        let mut out = Vec::new();
        s.receive(&[pending_ack(&s), play_frame(0x2c, &[])].concat(), &mut out)
            .unwrap();
        for id in 2..=4 {
            let mut data = Vec::new();
            for v in [
                f64::from(rusty_mines::vanilla_world::WORLD_BORDER) + 1.0,
                65.0,
                8.5,
            ] {
                data.extend(v.to_be_bytes());
            }
            data.push(3);
            out.clear();
            s.receive(&play_frame(0x1e, &data), &mut out).unwrap();
            assert_eq!(s.pending_teleport.unwrap().pose.id, id);
            assert_eq!(s.pending_teleport.unwrap().kind, TeleportKind::Correction);
            assert!(s.play_confirmed && s.teleport_confirmed);
            let length = out.len();
            let deadline = s.pending_teleport.unwrap().deadline;
            for _ in 0..20 {
                s.receive(&play_frame(0x1e, &data), &mut out).unwrap();
            }
            assert_eq!(out.len(), length);
            assert_eq!(s.pending_teleport.unwrap().deadline, deadline);
            let ack = pending_ack(&s);
            s.receive(&ack, &mut out).unwrap();
            assert!(s.pending_teleport.is_none());
        }
        assert_eq!(
            backend
                .events
                .lock()
                .unwrap()
                .iter()
                .filter(|(e, _)| *e == "play")
                .count(),
            1
        );
        s.receive(&teleport_ack(), &mut out).unwrap();
        assert_eq!(s.state, ConnectionState::Closed);
    }

    #[test]
    fn m1_virtual_clock_deadline_under_input_and_id_exhaustion() {
        for initial in [false, true] {
            let mut s = initialized_m1(Arc::new(RecordingBackend::default()));
            let now = Arc::new(std::sync::Mutex::new((s.clock)()));
            let clock = now.clone();
            s.clock = Arc::new(move || *clock.lock().unwrap());
            let mut out = Vec::new();
            if !initial {
                s.receive(&[pending_ack(&s), play_frame(0x2c, &[])].concat(), &mut out)
                    .unwrap();
                s.send_teleport(TeleportKind::Correction, &mut out).unwrap();
            }
            let deadline = s.pending_teleport.unwrap().deadline;
            *now.lock().unwrap() = deadline - Duration::from_nanos(1);
            s.receive(&play_frame(0x0d, &[]), &mut out).unwrap();
            assert_eq!(s.state, ConnectionState::Play);
            *now.lock().unwrap() = deadline;
            s.receive(&play_frame(0x0d, &[]), &mut out).unwrap();
            assert_eq!(s.state, ConnectionState::Closed);
            assert!(s.pending_teleport.is_none());
        }
        let mut s = initialized_m1(Arc::new(RecordingBackend::default()));
        s.receive(&pending_ack(&s), &mut Vec::new()).unwrap();
        s.teleport_id = i32::MAX - 1;
        s.send_teleport(TeleportKind::Correction, &mut Vec::new())
            .unwrap();
        assert_eq!(s.pending_teleport.unwrap().pose.id, i32::MAX);
        s.receive(&pending_ack(&s), &mut Vec::new()).unwrap();
        s.send_teleport(TeleportKind::Correction, &mut Vec::new())
            .unwrap();
        assert_eq!(s.state, ConnectionState::Closed);
    }

    #[test]
    fn m1_retains_flags_and_use_rotation_before_readonly_ack() {
        let mut s = initialized_m1(Arc::new(RecordingBackend::default()));
        s.receive(&pending_ack(&s), &mut Vec::new()).unwrap();
        for flags in 0..=3 {
            s.receive(&play_frame(0x21, &[flags]), &mut Vec::new())
                .unwrap();
            assert!(!s.on_ground);
            let _ = flags;
        }
        let mut data = vec![0, 1];
        data.extend(720.0_f32.to_be_bytes());
        data.extend(120.0_f32.to_be_bytes());
        let mut out = Vec::new();
        s.receive(&play_frame(0x43, &data), &mut out).unwrap();
        assert_eq!(s.player_rotation, [720.0, 120.0]);
        let (packet, _) = parse_packet(&out, None, false).unwrap().unwrap();
        assert_eq!(packet.id, 4);
        assert_eq!(packet.data, vec![1]);
    }

    fn session() -> ClientSession {
        ClientSession::new(
            "127.0.0.1:25565".parse().unwrap(),
            Arc::new(Traffic::default()),
            Arc::new(crate::gateway::TestSessions),
        )
    }

    fn handshake(intent: u8) -> Vec<u8> {
        // Frame length, packet ID, protocol 777, "localhost", port 25565, intent.
        vec![
            0x10, 0x00, 0x89, 0x06, 0x09, b'l', b'o', b'c', b'a', b'l', b'h', b'o', b's', b't',
            0x63, 0xdd, intent,
        ]
    }

    #[test]
    fn retains_partial_handshake_until_complete() {
        let mut session = session();
        let frame = handshake(2);

        session.receive(&frame[..5], &mut Vec::new()).unwrap();
        assert_eq!(session.state, ConnectionState::Handshaking);
        assert_eq!(session.pending, frame[..5]);
        assert_eq!(session.traffic.snapshot()[2], 0);

        session.receive(&frame[5..], &mut Vec::new()).unwrap();
        assert_eq!(session.state, ConnectionState::Login);
        assert_eq!(session.protocol_version, Some(777));
        assert_eq!(session.traffic.snapshot()[2], 1);
        assert!(!session.transferred);
        assert!(session.pending.is_empty());
    }

    #[test]
    fn disconnect_write_failure_counts_partial_bytes_but_not_packet() {
        struct FailAfter {
            remaining: usize,
        }
        impl Write for FailAfter {
            fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
                if self.remaining == 0 {
                    return Err(io::Error::new(io::ErrorKind::BrokenPipe, "closed"));
                }
                let count = bytes.len().min(self.remaining);
                self.remaining -= count;
                Ok(count)
            }
            fn flush(&mut self) -> io::Result<()> {
                Ok(())
            }
        }
        let mut session = session();
        let traffic = session.traffic.clone();
        let mut writer = CountingIo::new(FailAfter { remaining: 3 }, traffic.clone());
        // Transfer handshakes are valid frames but trigger a Login Disconnect.
        assert!(session.receive(&handshake(3), &mut writer).is_err());
        assert_eq!(traffic.snapshot(), [0, 3, 1, 0]);
        assert_eq!(session.state, ConnectionState::Closed);
        assert_eq!(self::session().traffic.snapshot(), [0; 4]);
    }

    #[test]
    fn updates_state_before_handling_next_packet_in_same_read() {
        let mut session = session();
        let mut frames = handshake(1);
        frames.extend_from_slice(&[0x01, 0x00]); // Status Request.

        session.receive(&frames, &mut Vec::new()).unwrap();
        assert_eq!(session.state, ConnectionState::Status);
        assert!(session.pending.is_empty());
        assert_eq!(session.traffic.snapshot()[2], 2);
    }

    #[test]
    fn fragmented_status_and_ping_stop_at_pong() {
        let mut session = session();
        let mut frames = handshake(1);
        frames.extend_from_slice(&[1, 0, 9, 1, 0xff, 0x80, 0, 1, 2, 3, 4, 5]);
        let mut output = Vec::new();
        for byte in frames {
            session.receive(&[byte], &mut output).unwrap();
        }
        let consumed = status::assert_response(&output);
        assert_eq!(&output[consumed..], &[9, 1, 0xff, 0x80, 0, 1, 2, 3, 4, 5]);
        assert_eq!(session.state, ConnectionState::Closed);
        session.receive(&[0xff; 6], &mut output).unwrap();
        assert!(session.pending.is_empty());
    }

    #[test]
    fn ping_only_ignores_trailing_malformed_frames() {
        let mut session = session();
        let mut frames = handshake(1);
        frames.extend_from_slice(&[9, 1, 0, 1, 2, 3, 4, 5, 6, 7]);
        frames.extend_from_slice(&[0xff; 6]);
        let mut output = Vec::new();
        session.receive(&frames, &mut output).unwrap();
        assert_eq!(output, [9, 1, 0, 1, 2, 3, 4, 5, 6, 7]);
        assert_eq!(session.state, ConnectionState::Closed);
    }

    #[test]
    fn rejects_duplicate_and_malformed_status_packets() {
        for packets in [
            vec![1, 0, 1, 0],
            vec![2, 0, 1],
            vec![8, 1, 0, 0, 0, 0, 0, 0, 0],
            vec![10, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0],
        ] {
            let mut session = session();
            let mut frames = handshake(1);
            frames.extend(packets);
            let error = session.receive(&frames, &mut Vec::new()).unwrap_err();
            assert_eq!(error.kind(), io::ErrorKind::InvalidData);
        }
    }

    #[test]
    fn propagates_write_failure() {
        struct FailedWriter;
        impl Write for FailedWriter {
            fn write(&mut self, _: &[u8]) -> io::Result<usize> {
                Err(io::ErrorKind::BrokenPipe.into())
            }
            fn flush(&mut self) -> io::Result<()> {
                Ok(())
            }
        }
        let mut frames = handshake(1);
        frames.extend_from_slice(&[1, 0]);
        assert_eq!(
            session()
                .receive(&frames, &mut FailedWriter)
                .unwrap_err()
                .kind(),
            io::ErrorKind::BrokenPipe
        );
    }

    #[test]
    fn loopback_fragmented_status_ping_and_eof() {
        use std::net::TcpListener;
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        listener.set_nonblocking(true).unwrap();
        let mut client = TcpStream::connect(listener.local_addr().unwrap()).unwrap();
        client
            .set_read_timeout(Some(Duration::from_secs(5)))
            .unwrap();
        client
            .set_write_timeout(Some(Duration::from_secs(5)))
            .unwrap();
        // Connect before accepting: accept cannot hang waiting for a test client.
        let (server, _) = listener.accept().unwrap();
        // Exercise inherited nonblocking mode on all platforms, not just Windows.
        server.set_nonblocking(true).unwrap();
        let traffic = Arc::new(Traffic::default());
        let shared = traffic.clone();
        let worker = std::thread::spawn(move || {
            handle_client(server, shared, Arc::new(crate::gateway::TestSessions))
        });
        // The blocking worker must accept fragmented input on an inherited socket.
        let mut frames = handshake(1);
        frames.extend_from_slice(&[1, 0, 9, 1, 0xff, 0x80, 0, 1, 2, 3, 4, 5]);
        let received = frames.len() as u64;
        for byte in frames {
            client.write_all(&[byte]).unwrap();
        }
        let mut output = Vec::new();
        client.read_to_end(&mut output).unwrap(); // Requires EOF after pong.
        let consumed = status::assert_response(&output);
        assert_eq!(&output[consumed..], &[9, 1, 0xff, 0x80, 0, 1, 2, 3, 4, 5]);
        worker.join().unwrap().unwrap();
        assert_eq!(traffic.snapshot(), [received, output.len() as u64, 3, 2]);
    }

    fn login_start(name: &str) -> Vec<u8> {
        let (id, data) = Serverbound::Start(login::LoginStart {
            name: name.into(),
            player_uuid: uuid::Uuid::from_bytes([99; 16]),
        })
        .encode()
        .unwrap();
        let mut frame = Vec::new();
        write_frame(&mut frame, id, &data).unwrap();
        frame
    }

    fn assert_success(output: &[u8]) -> usize {
        let (packet, n) = parse_packet(output, None, false).unwrap().unwrap();
        let Clientbound::Success(success) = Clientbound::decode(packet.id, &packet.data).unwrap()
        else {
            panic!("expected success")
        };
        assert_eq!(success.profile.uuid, login::offline_uuid("Notch").unwrap());
        assert_eq!(success.profile.username, "Notch");
        assert!(success.profile.properties.is_empty());
        assert_eq!(success.session_id.get_version_num(), 4);
        assert_eq!(success.session_id.get_variant(), uuid::Variant::RFC4122);
        n
    }

    fn config_frame(packet: configuration::Serverbound) -> Vec<u8> {
        let (id, data) = packet.encode().unwrap();
        let mut frame = Vec::new();
        write_frame(&mut frame, id, &data).unwrap();
        frame
    }

    fn configuration_responses() -> Vec<u8> {
        let core = rusty_mines::vanilla_configuration::vanilla()
            .unwrap()
            .core
            .clone();
        [
            config_frame(configuration::Serverbound::KnownPacks(vec![core])),
            config_frame(configuration::Serverbound::FinishAcknowledged),
        ]
        .concat()
    }

    fn assert_configuration_complete(mut output: &[u8]) {
        let vanilla = rusty_mines::vanilla_configuration::vanilla().unwrap();
        let mut expected = vec![
            configuration::brand(),
            configuration::Clientbound::FeatureFlags(vec!["minecraft:vanilla".into()]),
            configuration::Clientbound::KnownPacks(vec![vanilla.core.clone()]),
        ];
        expected.extend(vanilla.registries.clone());
        expected.push(vanilla.tags.clone());
        expected.push(configuration::Clientbound::Finish);
        for expected in expected {
            let (packet, n) = parse_packet(output, None, false).unwrap().unwrap();
            assert_eq!(
                configuration::Clientbound::decode(packet.id, &packet.data).unwrap(),
                expected
            );
            output = &output[n..];
        }
        let world = rusty_mines::vanilla_world::world().unwrap();
        let (packet, n) = parse_packet(output, None, false).unwrap().unwrap();
        assert_eq!(packet.id, 0x32);
        assert!(i32::from_be_bytes(packet.data[..4].try_into().unwrap()) > 0);
        assert_eq!(&packet.data[4..], &world.login[5..]);
        output = &output[n..];
        for expected in &world.initialization {
            let (packet, n) = parse_packet(output, None, false).unwrap().unwrap();
            assert_eq!(packet.id, i32::from(expected[0]));
            assert_eq!(packet.data, expected[1..]);
            output = &output[n..];
        }
        while !output.is_empty() {
            let (packet, n) = parse_packet(output, None, false).unwrap().unwrap();
            match packet.id {
                0x7c => assert_eq!(packet.data.last(), Some(&0)),
                0x41 => assert_eq!(packet.data.len(), 9),
                0x6a => assert_eq!(packet.data.len(), 9),
                0x27 => assert_eq!(packet.data.len(), 5),
                0x20 => assert!(!packet.data.is_empty()),
                other => panic!("unexpected post-initialization packet {other:#x}"),
            }
            output = &output[n..];
        }
    }

    #[test]
    fn fragmented_and_coalesced_login() {
        for fragmented in [false, true] {
            let mut s = session();
            let mut frames = handshake(2);
            frames.extend(login_start("Notch"));
            frames.extend([1, 3]);
            frames.extend(configuration_responses());
            let mut output = Vec::new();
            if fragmented {
                for b in frames {
                    s.receive(&[b], &mut output).unwrap();
                }
            } else {
                s.receive(&frames, &mut output).unwrap();
            }
            let n = assert_success(&output);
            assert_configuration_complete(&output[n..]);
            assert_eq!(s.state, ConnectionState::Play);
            assert!(s.pending.is_empty());
            assert_eq!(s.compression_threshold, None);
        }
    }

    #[test]
    fn login_errors_disconnect_and_close() {
        let mut old = handshake(2);
        old[2] = 0x88;
        let cases = [
            old,
            handshake(3),
            [handshake(2), vec![1, 3]].concat(),
            [handshake(2), login_start("Notch"), login_start("Notch")].concat(),
            [handshake(2), vec![2, 3, 0]].concat(),
            [handshake(2), vec![1, 1]].concat(),
            [handshake(2), vec![1, 2]].concat(),
            [handshake(2), vec![1, 4]].concat(),
            [handshake(2), vec![1, 5]].concat(),
            [handshake(2), login_start("bad name")].concat(),
            [handshake(2), vec![255; 6]].concat(),
        ];
        for frames in cases {
            let mut s = session();
            let mut out = Vec::new();
            s.receive(&frames, &mut out).unwrap();
            assert_eq!(s.state, ConnectionState::Closed);
            assert!(s.pending.is_empty());
            let (mut p, mut n) = parse_packet(&out, None, false).unwrap().unwrap();
            if p.id == 2 {
                let (next, count) = parse_packet(&out[n..], None, false).unwrap().unwrap();
                p = next;
                n += count;
            }
            assert!(matches!(
                Clientbound::decode(p.id, &p.data).unwrap(),
                Clientbound::Disconnect { .. }
            ));
            assert_eq!(n, out.len());
        }
    }

    #[test]
    fn loopback_login_acknowledgement_configuration_and_eof() {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let mut client = TcpStream::connect(listener.local_addr().unwrap()).unwrap();
        client
            .set_read_timeout(Some(Duration::from_secs(5)))
            .unwrap();
        client
            .set_write_timeout(Some(Duration::from_secs(5)))
            .unwrap();
        let (server, _) = listener.accept().unwrap();
        let traffic = Arc::new(Traffic::default());
        let shared = traffic.clone();
        let worker = std::thread::spawn(move || {
            handle_client(server, shared, Arc::new(crate::gateway::TestSessions))
        });
        let frames = [handshake(2), login_start("Notch")].concat();
        let responses = [
            vec![1, 3],
            configuration_responses(),
            teleport_ack(),
            play_frame(0x2c, &[]),
        ]
        .concat();
        let received = (frames.len() + responses.len()) as u64;
        for b in frames {
            client.write_all(&[b]).unwrap();
        }
        let mut output = Vec::new();
        loop {
            let mut b = [0; 1];
            client.read_exact(&mut b).unwrap();
            output.push(b[0]);
            if parse_packet(&output, None, false).unwrap().is_some() {
                break;
            }
        }
        assert_eq!(assert_success(&output), output.len());
        client.write_all(&responses).unwrap();
        client.shutdown(Shutdown::Write).unwrap();
        let mut remaining = Vec::new();
        client.read_to_end(&mut remaining).unwrap();
        assert_configuration_complete(&remaining);
        worker.join().unwrap().unwrap();
        assert_eq!(
            traffic.snapshot(),
            [received, (output.len() + remaining.len()) as u64, 7, 77]
        );
    }

    #[derive(Default)]
    struct RecordingBackend {
        events: std::sync::Mutex<Vec<(&'static str, uuid::Uuid)>>,
        reject_begin: bool,
        reject_configure: bool,
    }

    impl SessionStore for RecordingBackend {
        fn begin(&self, session: uuid::Uuid, username: &str) -> io::Result<()> {
            assert_eq!(username, "Notch");
            self.events.lock().unwrap().push(("begin", session));
            if self.reject_begin {
                Err(io::Error::other("Backend unavailable"))
            } else {
                Ok(())
            }
        }
        fn configure(&self, session: uuid::Uuid) -> io::Result<i32> {
            self.events.lock().unwrap().push(("configure", session));
            if self.reject_configure {
                Err(io::Error::other("Backend disconnected"))
            } else {
                Ok(16384)
            }
        }
        fn play(&self, session: uuid::Uuid) -> io::Result<()> {
            self.events.lock().unwrap().push(("play", session));
            Ok(())
        }
        fn end(&self, session: uuid::Uuid) {
            self.events.lock().unwrap().push(("end", session));
        }
    }

    fn play_frame(id: i32, data: &[u8]) -> Vec<u8> {
        let mut frame = Vec::new();
        write_frame(&mut frame, id, data).unwrap();
        frame
    }

    fn teleport_ack() -> Vec<u8> {
        let mut data = vec![1];
        for value in [8.5_f64, 65.0, 8.5] {
            data.extend(value.to_be_bytes());
        }
        data.extend([0; 8]);
        play_frame(0, &data)
    }

    #[test]
    fn loopback_kick_login_configuration_play_reason_eof_and_cleanup() {
        for phase in ["begin", "configure", "play"] {
            let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
            let mut client = TcpStream::connect(listener.local_addr().unwrap()).unwrap();
            client
                .set_read_timeout(Some(Duration::from_secs(5)))
                .unwrap();
            client
                .set_write_timeout(Some(Duration::from_secs(5)))
                .unwrap();
            let (server, _) = listener.accept().unwrap();
            let backend = Arc::new(RecordingBackend::default());
            let registry = Arc::new(crate::players::Registry::default());
            let traffic = Arc::new(Traffic::default());
            let worker_backend = backend.clone();
            let worker_registry = registry.clone();
            let worker_traffic = traffic.clone();
            let worker = std::thread::spawn(move || {
                handle_controlled_client(server, worker_traffic, worker_backend, worker_registry)
            });
            let mut input = [handshake(2), login_start("Notch")].concat();
            if phase != "begin" {
                input.extend([1, 3]);
            }
            if phase == "play" {
                input.extend(configuration_responses());
                input.extend(teleport_ack());
                input.extend(play_frame(0x2c, &[]));
            }
            client.write_all(&input).unwrap();
            // Consume initialization concurrently; world packets can fill the TCP send buffer.
            let mut receiving = client.try_clone().unwrap();
            let receive = std::thread::spawn(move || {
                let mut output = Vec::new();
                receiving.read_to_end(&mut output).unwrap();
                output
            });
            let deadline = Instant::now() + Duration::from_secs(4);
            let id = loop {
                if let Some((_, id)) = backend
                    .events
                    .lock()
                    .unwrap()
                    .iter()
                    .find(|(event, _)| *event == phase)
                {
                    break *id;
                }
                assert!(Instant::now() < deadline, "worker did not reach {phase}");
                std::thread::sleep(Duration::from_millis(5));
            };
            let reason = "Operator: quotes \" slash \\ Unicode 界 😀";
            let ack = registry.issue(id, reason.into()).unwrap();
            let requested = Instant::now();
            let output = receive.join().unwrap(); // Requires clean EOF, not ConnectionReset.
            assert!(requested.elapsed() < Duration::from_secs(4));
            client.shutdown(Shutdown::Write).unwrap();
            assert!(ack.recv_timeout(Duration::from_secs(5)).unwrap().is_ok());
            worker.join().unwrap().unwrap();
            let mut remaining = output.as_slice();
            let mut last = None;
            let mut frames = 0;
            while !remaining.is_empty() {
                let (packet, n) = parse_packet(remaining, None, false).unwrap().unwrap();
                last = Some(packet);
                remaining = &remaining[n..];
                frames += 1;
            }
            let packet = last.unwrap();
            if phase == "begin" {
                assert_eq!(packet.id, 0);
                let Clientbound::Disconnect { reason: json } =
                    Clientbound::decode(packet.id, &packet.data).unwrap()
                else {
                    panic!("expected Login Disconnect")
                };
                assert_eq!(
                    serde_json::from_str::<serde_json::Value>(&json).unwrap()["text"],
                    reason
                );
            } else {
                let expected = configuration::NetworkNbt::text(reason).unwrap();
                if phase == "configure" {
                    assert_eq!(
                        configuration::Clientbound::decode(packet.id, &packet.data).unwrap(),
                        configuration::Clientbound::Disconnect(expected)
                    );
                } else {
                    let mut expected_wire = Vec::new();
                    play::Clientbound::Disconnect(expected)
                        .write(&mut expected_wire)
                        .unwrap();
                    let (expected_packet, _) =
                        parse_packet(&expected_wire, None, false).unwrap().unwrap();
                    assert_eq!(packet, expected_packet);
                }
            }
            assert_eq!(
                backend
                    .events
                    .lock()
                    .unwrap()
                    .iter()
                    .filter(|(event, session)| *event == "end" && *session == id)
                    .count(),
                1
            );
            assert!(registry
                .player(id, crate::module_bindings::SessionPhase::Login)
                .is_none());
            assert!(registry.issue(id, "late".into()).is_err());
            let stats = traffic.snapshot();
            assert_eq!(stats[0], input.len() as u64);
            assert_eq!(stats[1], output.len() as u64);
            assert_eq!(stats[3], frames);
        }
    }

    #[test]
    fn loopback_kick_during_begin_wait_never_bypasses_backend_or_sends_success() {
        use std::sync::{
            atomic::{AtomicUsize, Ordering},
            mpsc, Mutex,
        };
        struct WaitingBegin {
            entered: mpsc::Sender<uuid::Uuid>,
            release: Mutex<mpsc::Receiver<()>>,
            ended: AtomicUsize,
        }
        impl SessionStore for WaitingBegin {
            fn begin(&self, id: uuid::Uuid, _: &str) -> io::Result<()> {
                self.entered.send(id).unwrap();
                self.release
                    .lock()
                    .unwrap()
                    .recv_timeout(Duration::from_secs(3))
                    .unwrap();
                Ok(())
            }
            fn configure(&self, _: uuid::Uuid) -> io::Result<i32> {
                panic!("kick must not configure")
            }
            fn end(&self, _: uuid::Uuid) {
                self.ended.fetch_add(1, Ordering::SeqCst);
            }
        }
        let (entered, waiting) = mpsc::channel();
        let (release, blocked) = mpsc::channel();
        let backend = Arc::new(WaitingBegin {
            entered,
            release: Mutex::new(blocked),
            ended: AtomicUsize::new(0),
        });
        let registry = Arc::new(crate::players::Registry::default());
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let mut client = TcpStream::connect(listener.local_addr().unwrap()).unwrap();
        client
            .set_read_timeout(Some(Duration::from_secs(5)))
            .unwrap();
        let (server, _) = listener.accept().unwrap();
        let worker_backend = backend.clone();
        let worker_registry = registry.clone();
        let worker = std::thread::spawn(move || {
            handle_controlled_client(
                server,
                Arc::new(Traffic::default()),
                worker_backend,
                worker_registry,
            )
        });
        client
            .write_all(&[handshake(2), login_start("Notch")].concat())
            .unwrap();
        let id = waiting.recv_timeout(Duration::from_secs(2)).unwrap();
        let ack = registry
            .issue(id, "Cancelled while joining".into())
            .unwrap();
        assert!(ack.try_recv().is_err());
        assert_eq!(backend.ended.load(Ordering::SeqCst), 0);
        release.send(()).unwrap();
        let mut output = Vec::new();
        client.read_to_end(&mut output).unwrap();
        client.shutdown(Shutdown::Write).unwrap();
        let (packet, bytes) = parse_packet(&output, None, false).unwrap().unwrap();
        assert_eq!(bytes, output.len());
        assert!(matches!(
            Clientbound::decode(packet.id, &packet.data).unwrap(),
            Clientbound::Disconnect { .. }
        ));
        assert!(ack.recv_timeout(Duration::from_secs(5)).unwrap().is_ok());
        worker.join().unwrap().unwrap();
        assert_eq!(backend.ended.load(Ordering::SeqCst), 1);
        assert!(registry
            .player(id, crate::module_bindings::SessionPhase::Login)
            .is_none());
    }

    #[test]
    fn kick_uses_actual_wire_phase_and_records_only_valid_rtt() {
        let backend = Arc::new(RecordingBackend::default());
        let mut s = backend_session(backend);
        let mut out = Vec::new();
        s.receive(
            &[handshake(2), login_start("Notch"), vec![1, 3]].concat(),
            &mut out,
        )
        .unwrap();
        let id = s.backend_session.unwrap();
        assert_eq!(
            s.controls
                .player(id, crate::module_bindings::SessionPhase::Configuration)
                .unwrap()
                .latency,
            None
        );
        let sent = Instant::now() - Duration::from_millis(20);
        s.keepalive = Some((123, sent));
        s.keepalive_sent = Some(sent);
        s.receive(
            &config_frame(configuration::Serverbound::KeepAlive(123)),
            &mut out,
        )
        .unwrap();
        assert!(
            s.controls
                .player(id, crate::module_bindings::SessionPhase::Configuration)
                .unwrap()
                .latency
                .unwrap()
                >= Duration::from_millis(20)
        );
        let ack = s.controls.issue(id, "bye".into()).unwrap();
        s.state = ConnectionState::Play; // Transition after request: worker must use Play, not selection phase.
        out.clear();
        s.poll(Instant::now(), &mut out).unwrap();
        let mut expected = Vec::new();
        play::Clientbound::Disconnect(configuration::NetworkNbt::text("bye").unwrap())
            .write(&mut expected)
            .unwrap();
        assert_eq!(out, expected);
        assert!(ack.try_recv().is_err()); // A written reason alone is not completed-disconnect feedback.
        drop(s);
        assert!(ack.recv().unwrap().is_err());
    }

    #[test]
    fn world_requires_loaded_and_exact_teleport_before_backend_play() {
        for loaded_first in [false, true] {
            let backend = Arc::new(RecordingBackend::default());
            let mut s = backend_session(backend.clone());
            let mut out = Vec::new();
            s.receive(
                &[
                    handshake(2),
                    login_start("Notch"),
                    vec![1, 3],
                    configuration_responses(),
                ]
                .concat(),
                &mut out,
            )
            .unwrap();
            assert_eq!(s.state, ConnectionState::Play);
            assert!(!s.play_confirmed);
            assert_eq!(backend.events.lock().unwrap().len(), 2);
            let loaded = play_frame(0x2c, &[]);
            let ack = teleport_ack();
            let (first, second) = if loaded_first {
                (&loaded, &ack)
            } else {
                (&ack, &loaded)
            };
            s.receive(first, &mut out).unwrap();
            assert!(!s.play_confirmed);
            assert_eq!(backend.events.lock().unwrap().len(), 2);
            for byte in second {
                s.receive(&[*byte], &mut out).unwrap();
            }
            assert!(s.play_confirmed);
            assert_eq!(backend.events.lock().unwrap()[2].0, "play");
            let now = s.deadline + Duration::from_secs(1);
            s.poll(now, &mut out).unwrap();
            assert_eq!(s.state, ConnectionState::Play);
            let id = s.keepalive.unwrap().0;
            s.receive(&play_frame(0x1c, &id.to_be_bytes()), &mut out)
                .unwrap();
            assert!(s.keepalive.is_none());
            s.receive(&play_frame(0x21, &[0]), &mut out).unwrap();
            s.receive(&play_frame(0x0d, &[]), &mut out).unwrap();
            assert_eq!(backend.events.lock().unwrap().len(), 3);
            drop(s);
            assert_eq!(backend.events.lock().unwrap()[3].0, "end");
        }
    }

    #[test]
    fn persistent_state_backend_missing_view_fails_closed_in_play() {
        struct PersistentStateUnavailable(RecordingBackend);
        impl SessionStore for PersistentStateUnavailable {
            fn begin(&self, session: uuid::Uuid, username: &str) -> io::Result<()> {
                self.0.begin(session, username)
            }
            fn configure(&self, session: uuid::Uuid) -> io::Result<i32> {
                self.0.configure(session)
            }
            fn play(&self, session: uuid::Uuid) -> io::Result<()> {
                self.0.play(session)
            }
            fn requires_persistent_player_state(&self) -> bool {
                true
            }
            fn end(&self, session: uuid::Uuid) {
                self.0.end(session)
            }
        }
        let mut s = initialized_m1(Arc::new(PersistentStateUnavailable(
            RecordingBackend::default(),
        )));
        s.receive(
            &[pending_ack(&s), play_frame(0x2c, &[])].concat(),
            &mut Vec::new(),
        )
        .unwrap();
        s.play_confirmed = true;
        let mut out = Vec::new();
        s.poll(Instant::now(), &mut out).unwrap();
        assert_eq!(s.state, ConnectionState::Closed);
    }

    #[test]
    fn malformed_play_does_not_promote_and_loading_has_deadline() {
        for (id, data) in [
            (0, vec![1]),
            (0x2c, vec![0]),
            (0x0b, f32::NAN.to_be_bytes().to_vec()),
            (0x1c, vec![0; 8]),
            (0x21, vec![4]),
            (0x7f, vec![]),
        ] {
            let mut s = session();
            let mut out = Vec::new();
            s.receive(
                &[
                    handshake(2),
                    login_start("Notch"),
                    vec![1, 3],
                    configuration_responses(),
                ]
                .concat(),
                &mut out,
            )
            .unwrap();
            s.receive(&play_frame(id, &data), &mut out).unwrap();
            assert_eq!(s.state, ConnectionState::Closed);
            assert!(!s.play_confirmed);
        }
        let mut s = session();
        let mut out = Vec::new();
        s.receive(
            &[
                handshake(2),
                login_start("Notch"),
                vec![1, 3],
                configuration_responses(),
            ]
            .concat(),
            &mut out,
        )
        .unwrap();
        s.poll(s.deadline, &mut out).unwrap();
        assert_eq!(s.state, ConnectionState::Closed);
    }

    #[test]
    fn next_play_interactions_correct_before_ack_and_broadcast_on_worker() {
        let mut s = session();
        s.teleport_id = 16383;
        let mut out = Vec::new();
        s.receive(
            &[
                handshake(2),
                login_start("Alex"),
                vec![1, 3],
                configuration_responses(),
            ]
            .concat(),
            &mut out,
        )
        .unwrap();
        let mut ack = crate::data_type::VarInt::new(16384).encode();
        for value in s.player_position {
            ack.extend(value.to_be_bytes());
        }
        for value in s.player_rotation {
            ack.extend(value.to_be_bytes());
        }
        s.receive(
            &[play_frame(0, &ack), play_frame(0x2c, &[])].concat(),
            &mut out,
        )
        .unwrap();
        assert!(s.play_confirmed);
        out.clear();
        let pos = play::BlockPosition { x: 8, y: 64, z: 8 };
        let mut action = vec![0];
        action.extend(pos.packed().to_be_bytes());
        action.push(1);
        action.extend(crate::data_type::VarInt::new(128).encode());
        s.receive(&play_frame(0x29, &action), &mut out).unwrap();
        let (block, n) = parse_packet(&out, None, false).unwrap().unwrap();
        assert_eq!(block.id, 8);
        assert_eq!(block.data.last(), Some(&9));
        let (ack, n2) = parse_packet(&out[n..], None, false).unwrap().unwrap();
        assert_eq!(ack.id, 4);
        assert_eq!(ack.data, vec![128, 1]);
        assert_eq!(n + n2, out.len());
        out.clear();
        let mut place = vec![0];
        place.extend(pos.packed().to_be_bytes());
        place.push(1);
        for v in [0.5_f32, 1.0, 0.5] {
            place.extend(v.to_be_bytes());
        }
        place.extend([0, 0, 129, 1]);
        s.receive(&play_frame(0x42, &place), &mut out).unwrap();
        let mut ids = Vec::new();
        let mut bytes = out.as_slice();
        while let Some((packet, n)) = parse_packet(bytes, None, false).unwrap() {
            ids.push(packet.id);
            bytes = &bytes[n..];
        }
        assert_eq!(ids, vec![8, 8, 4]);
        s.receive(
            &[
                play_frame(0x2e, &[]),
                play_frame(0x2b, &[0x7f]),
                play_frame(0x2a, &[1, 1, 0]),
                play_frame(0x36, &[0, 8]),
                play_frame(0x39, &[0, 36, 1, 1, 0, 0]),
                play_frame(0x13, &[0]),
            ]
            .concat(),
            &mut out,
        )
        .unwrap();
        assert_eq!(s.state, ConnectionState::Play);
        assert_eq!(s.input_flags, 127);
        assert_eq!(s.held_slot, 8);
        assert!(s.sprinting);
        let id = s.backend_session.unwrap();
        let snapshot =
            s.controls
                .snapshot([(id, crate::module_bindings::SessionPhase::Play, true)]);
        let deliveries = s
            .controls
            .broadcast(&snapshot, "Unicode 😀 quotes \"")
            .unwrap();
        assert_eq!(deliveries.len(), 1);
        out.clear();
        s.poll(Instant::now(), &mut out).unwrap();
        assert!(deliveries[0].1.try_recv().unwrap().is_ok());
        assert_eq!(parse_packet(&out, None, false).unwrap().unwrap().0.id, 0x7c);
    }
    #[test]
    fn movement_is_local_bounded_and_finite() {
        let mut s = session();
        let mut out = Vec::new();
        s.receive(
            &[
                handshake(2),
                login_start("Notch"),
                vec![1, 3],
                configuration_responses(),
                teleport_ack(),
                play_frame(0x2c, &[]),
            ]
            .concat(),
            &mut out,
        )
        .unwrap();
        let mut movement = Vec::new();
        for value in [9.0_f64, 66.0, 10.0] {
            movement.extend(value.to_be_bytes());
        }
        movement.extend(30_f32.to_be_bytes());
        movement.extend(10_f32.to_be_bytes());
        movement.push(0);
        s.receive(&play_frame(0x1f, &movement), &mut out).unwrap();
        assert_eq!(s.player_position, [9.0, 65.0, 10.0]);
        assert_eq!(s.player_rotation, [30.0, 10.0]);
        movement[..8].copy_from_slice(&f64::NAN.to_be_bytes());
        s.receive(&play_frame(0x1f, &movement), &mut out).unwrap();
        assert_eq!(s.state, ConnectionState::Closed);
        assert_eq!(s.player_position, [9.0, 65.0, 10.0]);
    }

    #[test]
    fn client_flight_request_cannot_grant_flight_outside_authorized_modes() {
        let mut s = initialized_m1(Arc::new(RecordingBackend::default()));
        s.receive(
            &[pending_ack(&s), play_frame(0x2c, &[])].concat(),
            &mut Vec::new(),
        )
        .unwrap();
        s.game_mode = 0;
        s.receive(&play_frame(0x28, &[2]), &mut Vec::new()).unwrap();
        assert!(!s.flying);
        s.game_mode = 1;
        s.receive(&play_frame(0x28, &[2]), &mut Vec::new()).unwrap();
        assert!(s.flying);
        s.receive(&play_frame(0x28, &[0]), &mut Vec::new()).unwrap();
        assert!(!s.flying);
    }

    #[test]
    fn unsupported_or_rejected_block_action_does_not_advance_commit_sequence() {
        let mut s = initialized_m1(Arc::new(RecordingBackend::default()));
        s.play_confirmed = true;
        let position = play::BlockPosition { x: 8, y: 63, z: 8 };
        let mut out = Vec::new();
        s.queue_block_action(1, position, false, &mut out).unwrap();
        assert_eq!(s.block_sequence, 0);
        s.queue_block_action(2, position, false, &mut out).unwrap();
        assert_eq!(s.block_sequence, 0);
    }

    #[test]
    fn block_actions_reconcile_rejections_confirm_commits_and_isolate_late_results() {
        struct BlockBackend {
            action: std::sync::Mutex<Option<crate::module_bindings::GatewayBlockAction>>,
            state: std::sync::Mutex<i32>,
            revision: std::sync::Mutex<u64>,
            results: std::sync::Mutex<Vec<std::sync::mpsc::Sender<io::Result<()>>>>,
            submits: std::sync::atomic::AtomicUsize,
        }
        impl BlockBackend {
            fn new() -> Self {
                Self {
                    action: std::sync::Mutex::new(None),
                    state: std::sync::Mutex::new(1),
                    revision: std::sync::Mutex::new(1),
                    results: std::sync::Mutex::new(Vec::new()),
                    submits: std::sync::atomic::AtomicUsize::new(0),
                }
            }

            fn publish_action(&self, session_uuid: uuid::Uuid, state: i32, sequence: u64) {
                *self.state.lock().unwrap() = state;
                *self.revision.lock().unwrap() = sequence + 1;
                *self.action.lock().unwrap() = Some(crate::module_bindings::GatewayBlockAction {
                    session_uuid: spacetimedb_sdk::Uuid::from_u128(session_uuid.as_u128()),
                    sequence,
                    x: 8,
                    y: 63,
                    z: 8,
                    expected_state: 1,
                    resulting_state: state as u32,
                    revision: sequence + 1,
                });
            }
        }
        impl SessionStore for BlockBackend {
            fn begin(&self, _: uuid::Uuid, _: &str) -> io::Result<()> {
                Ok(())
            }
            fn configure(&self, _: uuid::Uuid) -> io::Result<i32> {
                Ok(16384)
            }
            fn play(&self, _: uuid::Uuid) -> io::Result<()> {
                Ok(())
            }
            fn chunk_snapshot(
                &self,
                _: uuid::Uuid,
                chunk_x: i32,
                chunk_z: i32,
            ) -> io::Result<Option<crate::module_bindings::GatewayChunkSnapshot>> {
                let mut encoded_overrides = Vec::new();
                if (chunk_x, chunk_z) == (0, 0) {
                    for value in [8_i32, 63, 8, *self.state.lock().unwrap()] {
                        encoded_overrides.extend(value.to_le_bytes());
                    }
                }
                Ok(Some(crate::module_bindings::GatewayChunkSnapshot {
                    snapshot_key: format!("{chunk_x}:{chunk_z}"),
                    world_id: "test".into(),
                    chunk_x,
                    chunk_z,
                    encoded_overrides,
                    revision: *self.revision.lock().unwrap(),
                }))
            }
            fn requires_persistent_world_snapshots(&self) -> bool {
                true
            }
            fn block_action_submit(
                &self,
                _: uuid::Uuid,
                _: u64,
                _: i32,
                _: i32,
                _: i32,
                _: u32,
                _: bool,
            ) -> io::Result<std::sync::mpsc::Receiver<io::Result<()>>> {
                self.submits
                    .fetch_add(1, std::sync::atomic::Ordering::SeqCst);
                let (sender, receiver) = std::sync::mpsc::channel();
                self.results.lock().unwrap().push(sender);
                Ok(receiver)
            }
            fn block_action_snapshot(
                &self,
                session: uuid::Uuid,
            ) -> io::Result<Option<crate::module_bindings::GatewayBlockAction>> {
                Ok(self
                    .action
                    .lock()
                    .unwrap()
                    .clone()
                    .filter(|action| action.session_uuid.as_u128() == session.as_u128()))
            }
            fn end(&self, _: uuid::Uuid) {}
        }

        let backend = Arc::new(BlockBackend::new());
        let position = play::BlockPosition { x: 8, y: 63, z: 8 };
        let mut session = initialized_m1(backend.clone());
        session
            .receive(
                &[pending_ack(&session), play_frame(0x2c, &[])].concat(),
                &mut Vec::new(),
            )
            .unwrap();
        let session_id = session.backend_session.unwrap();
        let mut output = Vec::new();

        // One pending reducer call is permitted; a second prediction is corrected and ACKed.
        session
            .queue_block_action(1, position, false, &mut output)
            .unwrap();
        assert!(output.is_empty());
        session
            .queue_block_action(2, position, false, &mut output)
            .unwrap();
        assert_eq!(backend.submits.load(std::sync::atomic::Ordering::SeqCst), 1);
        assert_eq!(packet_ids(&output), vec![0x08, 0x04]);
        assert_eq!(session.block_sequence, 0);

        // A subscribed commit row alone is insufficient until reducer callback success arrives.
        backend.publish_action(session_id, 0, 1);
        output.clear();
        session
            .poll_block_action(Instant::now(), &mut output)
            .unwrap();
        assert!(output.is_empty());
        backend.results.lock().unwrap()[0].send(Ok(())).unwrap();
        session
            .poll_block_action(Instant::now(), &mut output)
            .unwrap();
        assert_eq!(packet_ids(&output), vec![0x08, 0x04]);
        assert_eq!(session.block_sequence, 1);

        // A rejected callback reconciles the authoritative old block before prediction ACK.
        backend.publish_action(session_id, 0, 1);
        output.clear();
        session
            .queue_block_action(3, position, false, &mut output)
            .unwrap();
        backend.results.lock().unwrap()[1]
            .send(Err(io::Error::other("rejected")))
            .unwrap();
        session
            .poll_block_action(Instant::now(), &mut output)
            .unwrap();
        assert_eq!(packet_ids(&output), vec![0x08, 0x04]);
        assert_eq!(session.block_sequence, 1);

        // Disconnect clears pending work; a late callback from that session cannot affect its replacement.
        output.clear();
        session
            .queue_block_action(4, position, false, &mut output)
            .unwrap();
        let late_sender = backend.results.lock().unwrap()[2].clone();
        let old_session = session.backend_session.unwrap();
        session
            .disconnect_play(&mut output, "test replacement")
            .unwrap();
        let mut replacement = initialized_m1(backend.clone());
        replacement
            .receive(
                &[pending_ack(&replacement), play_frame(0x2c, &[])].concat(),
                &mut Vec::new(),
            )
            .unwrap();
        assert_ne!(replacement.backend_session.unwrap(), old_session);
        assert!(late_sender.send(Ok(())).is_err());
        output.clear();
        replacement
            .poll_block_action(Instant::now(), &mut output)
            .unwrap();
        assert!(output.is_empty());
        assert_eq!(replacement.block_sequence, 0);
        assert!(replacement.block_pending.is_none());

        replacement
            .queue_block_action(5, position, false, &mut output)
            .unwrap();
        let deadline = replacement.block_pending.as_ref().unwrap().deadline;
        replacement
            .poll_block_action(deadline, &mut output)
            .unwrap();
        assert_eq!(replacement.state, ConnectionState::Closed);
        assert!(replacement.block_pending.is_none());
    }

    #[test]
    fn survival_gravity_lands_on_platform_and_tick_catchup_is_bounded() {
        let mut s = session();
        let mut out = Vec::new();
        s.game_mode = 0;
        s.player_position[1] = 80.0;
        for _ in 0..64 {
            s.test_vertical_tick(8, &mut out).unwrap();
        }
        assert_eq!(s.player_position[1], 65.0);
        assert!(s.on_ground);
        assert!(s.vertical_velocity.abs() < f64::EPSILON);

        s.player_position[1] = 80.0;
        s.on_ground = false;
        s.vertical_velocity = 0.0;
        s.simulation_at = Instant::now();
        s.test_vertical_tick(200, &mut out).unwrap();
        assert!(s.player_position[1] >= 76.0); // at most eight 50 ms steps
    }

    #[test]
    fn void_death_freezes_until_same_dimension_respawn_and_reloads_chunks() {
        let mut s = session();
        s.state = ConnectionState::Play;
        s.game_mode = 0;
        s.food = 17;
        s.entity_id = Some(7);
        s.player_position = [8.5, -63.5, 8.5];
        s.vertical_velocity = -1.0;
        let mut out = Vec::new();
        s.test_vertical_tick(1, &mut out).unwrap();
        assert!(s.dead);
        assert_eq!(s.health, 0.0);
        assert_eq!(s.player_position, [8.5, -64.0, 8.5]);
        let mut ids = Vec::new();
        let mut rest = out.as_slice();
        while let Some((packet, consumed)) = parse_packet(rest, None, false).unwrap() {
            ids.push(packet.id as u8);
            rest = &rest[consumed..];
        }
        assert_eq!(ids, [0x6a, 0x45]);

        out.clear();
        s.player_loaded = true;
        s.loading_ticks = 30;
        s.loading_started = Some(Instant::now() - Duration::from_secs(1));
        let (late_sender, late_receiver) = std::sync::mpsc::channel();
        s.block_pending = Some(PendingBlockAction {
            receiver: late_receiver,
            sequence: 88,
            position: play::BlockPosition { x: 8, y: 63, z: 8 },
            expected_state: 1,
            place: false,
            deadline: Instant::now() + Duration::from_secs(3),
            accepted: false,
        });
        let inventory_state = rusty_mines_inventory::model::State::default();
        s.inventory_snapshot = Some(crate::module_bindings::InventorySnapshot {
            session_uuid: spacetimedb_sdk::Uuid::from_u128(1),
            owner_connection: spacetimedb_sdk::ConnectionId::from_u128(1),
            revision: 4,
            selected: 0,
            slots: rusty_mines_inventory::model::encode_slots(&inventory_state.slots),
            cursor: vec![0],
            menu_id: 0,
            menu_title: String::new(),
            sequence: 4,
            creative_allowed: false,
        });
        let (_inventory_sender, inventory_receiver) = std::sync::mpsc::channel();
        s.inventory_pending = Some(PendingInventory {
            receiver: inventory_receiver,
            sequence: 5,
            deadline: Instant::now() + Duration::from_secs(3),
            accepted: false,
            evidence: None,
        });
        s.receive(&play_frame(0x1f, &[0; 33]), &mut out).unwrap();
        assert!(s.dead);
        assert!(out.is_empty());

        s.receive(&play_frame(0x0c, &[0]), &mut out).unwrap();
        assert!(!s.dead);
        assert_eq!(s.health, 20.0);
        assert_eq!(s.food, 17);
        assert_eq!(s.player_position, [8.5, 65.0, 8.5]);
        assert!(s.pending_teleport.is_some());
        assert_eq!(s.pending_teleport.unwrap().kind, TeleportKind::Correction);
        assert!(!s.player_loaded);
        assert_eq!(s.loading_ticks, 0);
        assert!(s.loading_started.is_some());
        assert!(s.block_pending.is_none());
        assert!(s.inventory_pending.is_none());
        assert!(s.inventory_snapshot.is_none());
        assert!(s.inventory_resync);
        assert!(s.loaded_chunks.is_empty());
        assert_eq!(s.pending_chunk_center, Some((0, 0)));
        let mut ids = Vec::new();
        let mut rest = out.as_slice();
        while let Some((packet, consumed)) = parse_packet(rest, None, false).unwrap() {
            ids.push(packet.id as u8);
            rest = &rest[consumed..];
        }
        assert_eq!(ids, [0x54, 0x41, 0x6a, 0x27, play::POSITION]);

        out.clear();
        let mut movement = Vec::new();
        for value in [16.5_f64, 65.0, 8.5] {
            movement.extend(value.to_be_bytes());
        }
        movement.extend([0.0_f32.to_be_bytes(), 0.0_f32.to_be_bytes()].concat());
        movement.push(1);
        s.receive(&play_frame(0x1f, &movement), &mut out).unwrap();
        assert_eq!(s.player_position, [8.5, 65.0, 8.5]);
        assert!(out.is_empty());
        assert!(late_sender.send(Ok(())).is_err());
        let ack = pending_ack(&s);
        s.receive(&ack, &mut out).unwrap();
        assert!(s.pending_teleport.is_none());
        s.receive(&play_frame(0x1f, &movement), &mut out).unwrap();
        assert_eq!(s.player_position, [16.5, 65.0, 8.5]);
        out.clear();
        s.receive(&play_frame(0x0c, &[0]), &mut out).unwrap();
        assert!(out.is_empty());

        s.dead = true;
        s.health = 0.0;
        s.game_mode = 2;
        out.clear();
        s.receive(&play_frame(0x0c, &[0]), &mut out).unwrap();
        let (packet, _) = parse_packet(&out, None, false).unwrap().unwrap();
        assert_eq!(packet.id as u8, 0x54);

        s.dead = false;
        s.game_mode = 1;
        s.player_position = [8.5, -63.5, 8.5];
        s.vertical_velocity = -1.0;
        out.clear();
        s.test_vertical_tick(1, &mut out).unwrap();
        assert!(!s.dead);
        assert_eq!(s.player_position, [8.5, 65.0, 8.5]);
    }

    #[test]
    fn persistent_respawn_waits_for_reducer_and_matching_subscribed_vitals() {
        struct RespawnBackend {
            state: std::sync::Mutex<crate::gateway::GatewayPlayerState>,
            result: std::sync::Mutex<Option<std::sync::mpsc::Sender<io::Result<()>>>>,
        }
        impl SessionStore for RespawnBackend {
            fn begin(&self, _: uuid::Uuid, _: &str) -> io::Result<()> {
                Ok(())
            }
            fn configure(&self, _: uuid::Uuid) -> io::Result<i32> {
                Ok(16384)
            }
            fn play(&self, _: uuid::Uuid) -> io::Result<()> {
                Ok(())
            }
            fn player_state(
                &self,
                _: uuid::Uuid,
            ) -> io::Result<Option<crate::gateway::GatewayPlayerState>> {
                Ok(Some(*self.state.lock().unwrap()))
            }
            fn game_mode(
                &self,
                _: uuid::Uuid,
            ) -> io::Result<Option<crate::module_bindings::GameMode>> {
                Ok(Some(crate::module_bindings::GameMode::Survival))
            }
            fn update_pose(&self, _: crate::module_bindings::PlayerPoseUpdate) -> io::Result<()> {
                Ok(())
            }
            fn requires_persistent_player_state(&self) -> bool {
                true
            }
            fn player_respawn_request(
                &self,
                _: uuid::Uuid,
            ) -> io::Result<std::sync::mpsc::Receiver<io::Result<()>>> {
                let (sender, receiver) = std::sync::mpsc::channel();
                *self.result.lock().unwrap() = Some(sender);
                Ok(receiver)
            }
            fn end(&self, _: uuid::Uuid) {}
        }

        let owner = spacetimedb_sdk::Identity::from_hex(
            "0000000000000000000000000000000000000000000000000000000000000001",
        )
        .unwrap();
        let backend = Arc::new(RespawnBackend {
            state: std::sync::Mutex::new(crate::gateway::GatewayPlayerState {
                mode: crate::module_bindings::GameMode::Survival,
                health: 0,
                food: 11,
                dead: true,
                revision: 3,
                respawn_revision: None,
                state_owner: Some(owner),
            }),
            result: std::sync::Mutex::new(None),
        });
        let mut session = initialized_m1(backend.clone());
        session
            .receive(
                &[pending_ack(&session), play_frame(0x2c, &[])].concat(),
                &mut Vec::new(),
            )
            .unwrap();
        assert!(session.play_confirmed);
        session.dead = true;
        session.health = 0.0;
        session.food = 11;
        session.player_loaded = true;
        session.loading_ticks = 30;
        let mut output = Vec::new();
        session
            .receive(&play_frame(0x0c, &[0]), &mut output)
            .unwrap();
        let pending = session.pending_player_state.as_ref().unwrap();
        assert_eq!(pending.expected.health, 20);
        assert_eq!(pending.expected.food, 11);
        assert_eq!(pending.expected.respawn_revision, Some(4));

        // A matching view row without the reducer callback is not confirmation.
        let expected = pending.expected;
        *backend.state.lock().unwrap() = expected;
        output.clear();
        session
            .persist_player_state(Instant::now(), false, &mut output)
            .unwrap();
        assert!(output.is_empty());
        assert!(session.dead);

        // Reducer success with a stale subscribed row also cannot respawn the client.
        backend
            .result
            .lock()
            .unwrap()
            .take()
            .unwrap()
            .send(Ok(()))
            .unwrap();
        *backend.state.lock().unwrap() = crate::gateway::GatewayPlayerState {
            health: 0,
            dead: true,
            revision: 3,
            respawn_revision: None,
            ..expected
        };
        session
            .persist_player_state(Instant::now(), false, &mut output)
            .unwrap();
        assert!(output.is_empty());
        assert!(session.dead);

        *backend.state.lock().unwrap() = expected;
        session
            .persist_player_state(Instant::now(), false, &mut output)
            .unwrap();
        assert!(!session.dead);
        assert_eq!(session.health, 20.0);
        assert_eq!(session.food, 11);
        assert!(!session.player_loaded);
        assert_eq!(session.loading_ticks, 0);
        assert_eq!(
            session.pending_teleport.unwrap().kind,
            TeleportKind::Correction
        );
        let mut ids = Vec::new();
        let mut remaining = output.as_slice();
        while let Some((packet, consumed)) = parse_packet(remaining, None, false).unwrap() {
            ids.push(packet.id as u8);
            remaining = &remaining[consumed..];
        }
        assert_eq!(ids, [0x54, 0x41, 0x6a, 0x27, play::POSITION]);
    }

    #[test]
    fn game_mode_state_synchronizes_all_four_vanilla_modes() {
        let mut s = session();
        let expected_abilities = [0x00, 0x0f, 0x00, 0x07];
        for mode in 0..=3 {
            s.game_mode = mode;
            let mut out = Vec::new();
            s.send_mode_state(&mut out).unwrap();
            let mut rest = out.as_slice();
            let mut packets = Vec::new();
            while let Some((packet, consumed)) = parse_packet(rest, None, false).unwrap() {
                packets.push(packet);
                rest = &rest[consumed..];
            }
            assert_eq!(packets.len(), 3);
            assert_eq!(packets[0].id as u8, 0x41);
            assert_eq!(packets[0].data[0], expected_abilities[usize::from(mode)]);
            assert_eq!(packets[1].id as u8, 0x6a);
            assert_eq!(packets[2].id as u8, 0x27);
            assert_eq!(packets[2].data[0], 3);
            assert_eq!(
                f32::from_be_bytes(packets[2].data[1..5].try_into().unwrap()),
                mode as f32
            );
        }
    }

    #[test]
    fn sprint_food_drain_is_survival_only_and_emits_health() {
        let mut s = session();
        let start = Instant::now();
        s.game_mode = 0;
        s.sprinting = true;
        s.next_food_drain = start;
        s.test_vertical_tick(1, &mut Vec::new()).unwrap();
        assert_eq!(s.food, 19);
        s.game_mode = 1;
        s.next_food_drain = start;
        s.test_vertical_tick(1, &mut Vec::new()).unwrap();
        assert_eq!(s.food, 19);
    }

    #[test]
    fn chunk_streaming_recenters_unloads_and_coalesces_bounded_windows() {
        let backend = Arc::new(RecordingBackend::default());
        let mut s = initialized_m1(backend);
        s.receive(
            &[pending_ack(&s), play_frame(0x2c, &[])].concat(),
            &mut Vec::new(),
        )
        .unwrap();
        assert_eq!(s.loaded_chunks.len(), 25);
        s.game_mode = 3;
        s.flying = true;

        let mut movement = Vec::new();
        for value in [16.0_f64, 66.0, 8.0] {
            movement.extend(value.to_be_bytes());
        }
        movement.extend(0.0_f32.to_be_bytes());
        movement.extend(0.0_f32.to_be_bytes());
        movement.push(3); // authorized Spectator flight plus on-ground client hint
        s.receive(&play_frame(0x1f, &movement), &mut Vec::new())
            .unwrap();
        assert_eq!(s.pending_chunk_center, Some((1, 0)));

        let mut out = Vec::new();
        s.next_chunk_batch = Instant::now();
        s.poll(Instant::now(), &mut out).unwrap();
        let mut ids = Vec::new();
        let mut rest = out.as_slice();
        while let Some((packet, consumed)) = parse_packet(rest, None, false).unwrap() {
            ids.push(packet.id as u8);
            rest = &rest[consumed..];
        }
        assert_eq!(
            ids,
            [
                vec![play::CENTER],
                vec![play::UNLOAD_CHUNK; 5],
                vec![play::BATCH_START],
                vec![play::CHUNK; 5],
                vec![play::BATCH_END],
            ]
            .concat()
        );
        assert_eq!(s.chunk_center, (1, 0));
        assert_eq!(s.loaded_chunks.len(), 25);
        assert!(s.loaded_chunks.contains(&(3, 0)));
        assert!(!s.loaded_chunks.contains(&(-2, 0)));

        for x in [8.0_f64, 0.0, -8.0, -16.0, -0.1] {
            movement[..8].copy_from_slice(&x.to_be_bytes());
            movement[8..16].copy_from_slice(&66.0_f64.to_be_bytes());
            movement[16..24].copy_from_slice(&8.0_f64.to_be_bytes());
            movement[32] = 3;
            s.receive(&play_frame(0x1f, &movement), &mut Vec::new())
                .unwrap();
        }
        assert_eq!(s.pending_chunk_center, Some((-1, 0)));
        out.clear();
        s.next_chunk_batch = Instant::now();
        s.poll(Instant::now(), &mut out).unwrap();
        assert_eq!(s.chunk_center, (-1, 0));
        assert_eq!(s.loaded_chunks, chunk_window((-1, 0), 2));
    }

    #[test]
    fn chunk_load_reconciles_snapshot_changes_during_and_after_batch_emission() {
        struct SnapshotRaceBackend {
            reads: std::sync::Mutex<std::collections::HashMap<(i32, i32), usize>>,
        }
        impl SessionStore for SnapshotRaceBackend {
            fn begin(&self, _: uuid::Uuid, _: &str) -> io::Result<()> {
                Ok(())
            }
            fn configure(&self, _: uuid::Uuid) -> io::Result<i32> {
                Ok(16384)
            }
            fn play(&self, _: uuid::Uuid) -> io::Result<()> {
                Ok(())
            }
            fn chunk_snapshot(
                &self,
                _: uuid::Uuid,
                chunk_x: i32,
                chunk_z: i32,
            ) -> io::Result<Option<crate::module_bindings::GatewayChunkSnapshot>> {
                let read = {
                    let mut reads = self.reads.lock().unwrap();
                    let read = reads.entry((chunk_x, chunk_z)).or_default();
                    *read += 1;
                    *read
                };
                let mut encoded_overrides = Vec::new();
                let revision = if (chunk_x, chunk_z) == (0, 0) && read > 1 {
                    let state = if read > 2 { 1 } else { 0 };
                    for value in [8_i32, 63, 8, state] {
                        encoded_overrides.extend(value.to_le_bytes());
                    }
                    if read > 2 {
                        3
                    } else {
                        2
                    }
                } else {
                    1
                };
                Ok(Some(crate::module_bindings::GatewayChunkSnapshot {
                    snapshot_key: format!("{chunk_x}:{chunk_z}"),
                    world_id: "test".into(),
                    chunk_x,
                    chunk_z,
                    encoded_overrides,
                    revision,
                }))
            }
            fn requires_persistent_world_snapshots(&self) -> bool {
                true
            }
            fn end(&self, _: uuid::Uuid) {}
        }

        let backend = Arc::new(SnapshotRaceBackend {
            reads: std::sync::Mutex::new(std::collections::HashMap::new()),
        });
        let mut session = initialized_m1(backend.clone());
        session
            .receive(
                &[pending_ack(&session), play_frame(0x2c, &[])].concat(),
                &mut Vec::new(),
            )
            .unwrap();
        backend.reads.lock().unwrap().clear();
        session.loaded_chunks.clear();
        session.chunk_revisions.clear();
        session.pending_chunk_center = Some((0, 0));
        session.next_chunk_batch = Instant::now();

        let mut output = Vec::new();
        session.poll(Instant::now(), &mut output).unwrap();
        let mut packets = Vec::new();
        let mut remaining = output.as_slice();
        while let Some((packet, consumed)) = parse_packet(remaining, None, false).unwrap() {
            packets.push(packet);
            remaining = &remaining[consumed..];
        }
        let last_chunk = packets
            .iter()
            .rposition(|packet| packet.id == i32::from(play::CHUNK))
            .unwrap();
        let block_update = packets.iter().position(|packet| packet.id == 0x08).unwrap();
        let batch_finished = packets
            .iter()
            .position(|packet| packet.id == i32::from(play::BATCH_END))
            .unwrap();
        let mut expected = Vec::new();
        play::Clientbound::BlockUpdate(play::BlockPosition { x: 8, y: 63, z: 8 }, 0)
            .write(&mut expected)
            .unwrap();
        let (expected, _) = parse_packet(&expected, None, false).unwrap().unwrap();
        assert!(last_chunk < block_update && block_update < batch_finished);
        assert_eq!(packets[block_update].data, expected.data);
        assert_eq!(session.chunk_revisions.get(&(0, 0)), Some(&2));

        // A later concurrent edit is picked up on the next Play poll, after
        // the original chunk batch has already completed.
        output.clear();
        session
            .poll(Instant::now() + SERVER_TICK, &mut output)
            .unwrap();
        let (later_update, _) = parse_packet(&output, None, false).unwrap().unwrap();
        let mut expected = Vec::new();
        play::Clientbound::BlockUpdate(play::BlockPosition { x: 8, y: 63, z: 8 }, 1)
            .write(&mut expected)
            .unwrap();
        let (expected, _) = parse_packet(&expected, None, false).unwrap().unwrap();
        assert_eq!(later_update.id, 0x08);
        assert_eq!(later_update.data, expected.data);
        assert_eq!(session.chunk_revisions.get(&(0, 0)), Some(&3));
    }

    #[test]
    fn chunk_unload_then_reload_reconciles_the_latest_override_revision() {
        struct SnapshotStore {
            state: std::sync::Mutex<(u64, i32)>,
        }
        impl SessionStore for SnapshotStore {
            fn begin(&self, _: uuid::Uuid, _: &str) -> io::Result<()> {
                Ok(())
            }
            fn configure(&self, _: uuid::Uuid) -> io::Result<i32> {
                Ok(16384)
            }
            fn play(&self, _: uuid::Uuid) -> io::Result<()> {
                Ok(())
            }
            fn chunk_snapshot(
                &self,
                _: uuid::Uuid,
                chunk_x: i32,
                chunk_z: i32,
            ) -> io::Result<Option<crate::module_bindings::GatewayChunkSnapshot>> {
                let (revision, state) = *self.state.lock().unwrap();
                let mut encoded_overrides = Vec::new();
                if (chunk_x, chunk_z) == (0, 0) {
                    for value in [8_i32, 63, 8, state] {
                        encoded_overrides.extend(value.to_le_bytes());
                    }
                }
                Ok(Some(crate::module_bindings::GatewayChunkSnapshot {
                    snapshot_key: format!("{chunk_x}:{chunk_z}"),
                    world_id: "test".into(),
                    chunk_x,
                    chunk_z,
                    encoded_overrides,
                    revision,
                }))
            }
            fn requires_persistent_world_snapshots(&self) -> bool {
                true
            }
            fn end(&self, _: uuid::Uuid) {}
        }

        let backend = Arc::new(SnapshotStore {
            state: std::sync::Mutex::new((10, 1)),
        });
        let mut session = initialized_m1(backend.clone());
        session
            .receive(
                &[pending_ack(&session), play_frame(0x2c, &[])].concat(),
                &mut Vec::new(),
            )
            .unwrap();
        session.loaded_chunks.clear();
        session.chunk_revisions.clear();
        session.pending_chunk_center = Some((0, 0));
        session.next_chunk_batch = Instant::now();

        let mut output = Vec::new();
        session.poll(Instant::now(), &mut output).unwrap();
        let mut packets = Vec::new();
        let mut remaining = output.as_slice();
        while let Some((packet, consumed)) = parse_packet(remaining, None, false).unwrap() {
            packets.push(packet);
            remaining = &remaining[consumed..];
        }
        let base_chunk = packets
            .iter()
            .rposition(|packet| packet.id == i32::from(play::CHUNK))
            .unwrap();
        let block_update = packets.iter().position(|packet| packet.id == 0x08).unwrap();
        let batch_finished = packets
            .iter()
            .position(|packet| packet.id == i32::from(play::BATCH_END))
            .unwrap();
        assert!(base_chunk < block_update && block_update < batch_finished);
        assert_eq!(session.chunk_revisions.get(&(0, 0)), Some(&10));

        session.pending_chunk_center = Some((3, 0));
        session.next_chunk_batch = Instant::now();
        output.clear();
        session.poll(Instant::now(), &mut output).unwrap();
        assert!(packet_ids(&output).contains(&i32::from(play::UNLOAD_CHUNK)));
        assert!(!session.loaded_chunks.contains(&(0, 0)));
        assert!(!session.chunk_revisions.contains_key(&(0, 0)));

        *backend.state.lock().unwrap() = (11, 0);
        session.pending_chunk_center = Some((0, 0));
        session.next_chunk_batch = Instant::now();
        output.clear();
        session.poll(Instant::now(), &mut output).unwrap();
        let mut packets = Vec::new();
        let mut remaining = output.as_slice();
        while let Some((packet, consumed)) = parse_packet(remaining, None, false).unwrap() {
            packets.push(packet);
            remaining = &remaining[consumed..];
        }
        let base_chunk = packets
            .iter()
            .rposition(|packet| packet.id == i32::from(play::CHUNK))
            .unwrap();
        let block_update = packets.iter().position(|packet| packet.id == 0x08).unwrap();
        let batch_finished = packets
            .iter()
            .position(|packet| packet.id == i32::from(play::BATCH_END))
            .unwrap();
        let mut expected = Vec::new();
        play::Clientbound::BlockUpdate(play::BlockPosition { x: 8, y: 63, z: 8 }, 0)
            .write(&mut expected)
            .unwrap();
        let (expected, _) = parse_packet(&expected, None, false).unwrap().unwrap();
        assert!(base_chunk < block_update && block_update < batch_finished);
        assert_eq!(packets[block_update].data, expected.data);
        assert_eq!(session.chunk_revisions.get(&(0, 0)), Some(&11));
    }

    #[test]
    fn backend_play_refusal_and_keepalive_timeout_disconnect_in_play() {
        struct Refusing;
        impl SessionStore for Refusing {
            fn begin(&self, _: uuid::Uuid, _: &str) -> io::Result<()> {
                Ok(())
            }
            fn configure(&self, _: uuid::Uuid) -> io::Result<i32> {
                Ok(1)
            }
            fn end(&self, _: uuid::Uuid) {}
        }
        let mut s = ClientSession::new(
            "127.0.0.1:25565".parse().unwrap(),
            Arc::new(Traffic::default()),
            Arc::new(Refusing),
        );
        let mut out = Vec::new();
        s.receive(
            &[
                handshake(2),
                login_start("Notch"),
                vec![1, 3],
                configuration_responses(),
                teleport_ack(),
                play_frame(0x2c, &[]),
            ]
            .concat(),
            &mut out,
        )
        .unwrap();
        assert_eq!(s.state, ConnectionState::Closed);
        assert!(!s.play_confirmed);
        let mut s = session();
        s.receive(
            &[
                handshake(2),
                login_start("Notch"),
                vec![1, 3],
                configuration_responses(),
                teleport_ack(),
                play_frame(0x2c, &[]),
            ]
            .concat(),
            &mut out,
        )
        .unwrap();
        let sent = s.next_keepalive;
        s.poll(sent, &mut out).unwrap();
        assert!(s.keepalive.is_some());
        s.poll(sent + KEEPALIVE_TIMEOUT, &mut out).unwrap();
        assert_eq!(s.state, ConnectionState::Closed);
    }

    fn backend_session(backend: Arc<dyn SessionStore>) -> ClientSession {
        ClientSession::new(
            "127.0.0.1:25565".parse().unwrap(),
            Arc::new(Traffic::default()),
            backend,
        )
    }

    #[test]
    fn backend_session_correlates_wire_uuid_and_cleans_after_configuration() {
        let backend = Arc::new(RecordingBackend::default());
        let mut session = backend_session(backend.clone());
        let mut out = Vec::new();
        session
            .receive(
                &[
                    handshake(2),
                    login_start("Notch"),
                    vec![1, 3],
                    configuration_responses(),
                ]
                .concat(),
                &mut out,
            )
            .unwrap();
        let (packet, n) = parse_packet(&out, None, false).unwrap().unwrap();
        let Clientbound::Success(success) = Clientbound::decode(packet.id, &packet.data).unwrap()
        else {
            panic!("expected success")
        };
        assert_configuration_complete(&out[n..]);
        assert_eq!(
            *backend.events.lock().unwrap(),
            vec![
                ("begin", success.session_id),
                ("configure", success.session_id)
            ]
        );
        drop(session);
        assert_eq!(
            *backend.events.lock().unwrap(),
            vec![
                ("begin", success.session_id),
                ("configure", success.session_id),
                ("end", success.session_id)
            ]
        );
    }

    #[test]
    fn backend_rejection_never_sends_login_success() {
        let backend = Arc::new(RecordingBackend {
            reject_begin: true,
            ..Default::default()
        });
        let mut session = backend_session(backend.clone());
        let mut out = Vec::new();
        session
            .receive(&[handshake(2), login_start("Notch")].concat(), &mut out)
            .unwrap();
        let (packet, n) = parse_packet(&out, None, false).unwrap().unwrap();
        assert_eq!(n, out.len());
        assert!(matches!(
            Clientbound::decode(packet.id, &packet.data).unwrap(),
            Clientbound::Disconnect { .. }
        ));
        assert_eq!(session.state, ConnectionState::Closed);
        drop(session);
        assert_eq!(backend.events.lock().unwrap().len(), 1);
    }

    #[test]
    fn phase_failure_and_write_failure_always_clean_confirmed_session() {
        let backend = Arc::new(RecordingBackend {
            reject_configure: true,
            ..Default::default()
        });
        let mut session = backend_session(backend.clone());
        let mut out = Vec::new();
        session
            .receive(
                &[handshake(2), login_start("Notch"), vec![1, 3]].concat(),
                &mut out,
            )
            .unwrap();
        let n = assert_success(&out);
        let (packet, count) = parse_packet(&out[n..], None, false).unwrap().unwrap();
        assert_eq!(n + count, out.len());
        assert_eq!(packet.id, 2);
        assert_eq!(packet.data[0], 8);
        assert_eq!(
            &packet.data[3..],
            b"Configuration unavailable: backend did not confirm the session"
        );
        drop(session);
        let events = backend.events.lock().unwrap();
        assert_eq!(
            events.iter().map(|e| e.0).collect::<Vec<_>>(),
            vec!["begin", "configure", "end"]
        );
        drop(events);

        let backend = Arc::new(RecordingBackend::default());
        let mut session = backend_session(backend.clone());
        assert!(session
            .receive(
                &[handshake(2), login_start("Notch")].concat(),
                &mut &mut [][..]
            )
            .is_err());
        drop(session);
        assert_eq!(
            backend
                .events
                .lock()
                .unwrap()
                .iter()
                .map(|e| e.0)
                .collect::<Vec<_>>(),
            vec!["begin", "end"]
        );
    }

    #[test]
    fn status_does_not_create_backend_sessions_and_eof_cleans_login() {
        let backend = Arc::new(RecordingBackend::default());
        let mut session = backend_session(backend.clone());
        session
            .receive(
                &[handshake(1), vec![9, 1, 0, 0, 0, 0, 0, 0, 0, 1]].concat(),
                &mut Vec::new(),
            )
            .unwrap();
        drop(session);
        assert!(backend.events.lock().unwrap().is_empty());
        let mut session = backend_session(backend.clone());
        session
            .receive(
                &[handshake(2), login_start("Notch")].concat(),
                &mut Vec::new(),
            )
            .unwrap();
        drop(session);
        assert_eq!(
            backend
                .events
                .lock()
                .unwrap()
                .iter()
                .map(|e| e.0)
                .collect::<Vec<_>>(),
            vec!["begin", "end"]
        );
    }

    fn information() -> configuration::Serverbound {
        configuration::Serverbound::ClientInformation(configuration::ClientInformation {
            locale: "en_us".into(),
            view_distance: 8,
            chat_mode: 0,
            chat_colors: true,
            skin_parts: 127,
            main_hand: 1,
            text_filtering: false,
            server_listings: true,
            particle_status: 0,
        })
    }

    fn enter_configuration() -> ClientSession {
        let mut s = session();
        s.receive(
            &[handshake(2), login_start("Notch"), vec![1, 3]].concat(),
            &mut Vec::new(),
        )
        .unwrap();
        assert_eq!(s.state, ConnectionState::Configuration);
        s
    }

    #[test]
    fn configuration_settings_brand_and_phase_validation() {
        let mut s = enter_configuration();
        let brand = config_frame(configuration::Serverbound::PluginMessage {
            channel: "minecraft:brand".into(),
            data: vec![7, b'v', b'a', b'n', b'i', b'l', b'l', b'a'],
        });
        s.receive(
            &[config_frame(information()), brand.clone()].concat(),
            &mut Vec::new(),
        )
        .unwrap();
        assert_eq!(s.client_brand.as_deref(), Some("vanilla"));
        assert_eq!(s.client_information.as_ref().unwrap().view_distance, 8);
        let core = rusty_mines::vanilla_configuration::vanilla()
            .unwrap()
            .core
            .clone();
        s.receive(
            &config_frame(configuration::Serverbound::KnownPacks(vec![core])),
            &mut Vec::new(),
        )
        .unwrap();
        assert_eq!(s.configuration_phase, ConfigurationPhase::WaitingFinish);
        s.receive(&brand, &mut Vec::new()).unwrap();
        assert_eq!(s.state, ConnectionState::Configuration);
        for frame in [
            config_frame(configuration::Serverbound::FinishAcknowledged),
            config_frame(configuration::Serverbound::KeepAlive(42)),
            config_frame(configuration::Serverbound::KnownPacks(vec![])),
            vec![2, 3, 0],
            vec![255; 6],
        ] {
            let mut s = enter_configuration();
            let mut out = Vec::new();
            s.receive(&frame, &mut out).unwrap();
            assert_eq!(s.state, ConnectionState::Closed);
            let (p, n) = parse_packet(&out, None, false).unwrap().unwrap();
            assert_eq!(n, out.len());
            assert!(matches!(
                configuration::Clientbound::decode(p.id, &p.data).unwrap(),
                configuration::Clientbound::Disconnect(_)
            ));
        }
        let duplicate = config_frame(configuration::Serverbound::KnownPacks(vec![
            rusty_mines::vanilla_configuration::vanilla()
                .unwrap()
                .core
                .clone(),
        ]));
        s.receive(&duplicate, &mut Vec::new()).unwrap();
        assert_eq!(s.state, ConnectionState::Closed);
    }

    #[test]
    fn absolute_deadlines_and_keepalive_milliseconds() {
        let mut s = enter_configuration();
        let started = s.configuration_started.unwrap();
        let mut out = Vec::new();
        s.poll(started + Duration::from_millis(4999), &mut out)
            .unwrap();
        assert!(out.is_empty());
        s.poll(started + KEEPALIVE_INTERVAL, &mut out).unwrap();
        let (p, _) = parse_packet(&out, None, false).unwrap().unwrap();
        assert_eq!(
            configuration::Clientbound::decode(p.id, &p.data).unwrap(),
            configuration::Clientbound::KeepAlive(5000)
        );
        s.poll(started + Duration::from_secs(19), &mut out).unwrap();
        assert_eq!(s.state, ConnectionState::Configuration);
        s.poll(started + Duration::from_secs(20), &mut out).unwrap();
        assert_eq!(s.state, ConnectionState::Closed);
        let mut s = enter_configuration();
        let start = s.configuration_started.unwrap();
        s.poll(start + KEEPALIVE_INTERVAL, &mut Vec::new()).unwrap();
        s.handle_configuration(
            &Packet {
                id: 4,
                data: 5000i64.to_be_bytes().to_vec(),
            },
            &mut Vec::new(),
        )
        .unwrap();
        assert!(s.keepalive.is_none());
        s.poll(start + CONFIGURATION_TIMEOUT, &mut Vec::new())
            .unwrap();
        assert_eq!(s.state, ConnectionState::Closed);
        let mut s = session();
        let deadline = s.deadline;
        s.receive(&handshake(2)[..5], &mut Vec::new()).unwrap();
        assert_eq!(s.deadline, deadline);
        s.poll(deadline, &mut Vec::new()).unwrap();
        assert_eq!(s.state, ConnectionState::Closed);
        let mut s = session();
        s.receive(&handshake(2), &mut Vec::new()).unwrap();
        let deadline = s.deadline;
        s.receive(&login_start("Notch"), &mut Vec::new()).unwrap();
        assert_eq!(s.deadline, deadline);
        s.poll(deadline, &mut Vec::new()).unwrap();
        assert_eq!(s.state, ConnectionState::Closed);
    }

    #[test]
    fn tcp_pipelined_settings_across_backend_wait_and_raw_drain() {
        use std::sync::{mpsc, Mutex};
        struct BlockingBackend {
            entered: mpsc::Sender<()>,
            release: Mutex<mpsc::Receiver<()>>,
            ended: mpsc::Sender<()>,
        }
        impl SessionStore for BlockingBackend {
            fn begin(&self, _: uuid::Uuid, _: &str) -> io::Result<()> {
                Ok(())
            }
            fn configure(&self, _: uuid::Uuid) -> io::Result<i32> {
                self.entered.send(()).unwrap();
                self.release
                    .lock()
                    .unwrap()
                    .recv_timeout(Duration::from_secs(3))
                    .map(|()| 1)
                    .map_err(io::Error::other)
            }
            fn end(&self, _: uuid::Uuid) {
                self.ended.send(()).unwrap();
            }
        }
        let (entered_tx, entered_rx) = mpsc::channel();
        let (release_tx, release_rx) = mpsc::channel();
        let (ended_tx, ended_rx) = mpsc::channel();
        let (done_tx, done_rx) = mpsc::channel();
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let mut client = TcpStream::connect(listener.local_addr().unwrap()).unwrap();
        client
            .set_read_timeout(Some(Duration::from_secs(5)))
            .unwrap();
        client
            .set_write_timeout(Some(Duration::from_secs(5)))
            .unwrap();
        let (server, _) = listener.accept().unwrap();
        let traffic = Arc::new(Traffic::default());
        let shared = traffic.clone();
        let worker = std::thread::spawn(move || {
            let result = handle_client(
                server,
                shared,
                Arc::new(BlockingBackend {
                    entered: entered_tx,
                    release: Mutex::new(release_rx),
                    ended: ended_tx,
                }),
            );
            done_tx.send(result).unwrap();
        });
        let login = [handshake(2), login_start("Notch"), vec![1, 3]].concat();
        client.write_all(&login).unwrap();
        entered_rx.recv_timeout(Duration::from_secs(3)).unwrap();
        let pipeline = [
            config_frame(information()),
            config_frame(configuration::Serverbound::PluginMessage {
                channel: "minecraft:brand".into(),
                data: vec![4, b't', b'e', b's', b't'],
            }),
            configuration_responses(),
            vec![255; 16384],
        ]
        .concat();
        client.write_all(&pipeline).unwrap();
        client.shutdown(Shutdown::Write).unwrap();
        release_tx.send(()).unwrap();
        let mut output = Vec::new();
        client.read_to_end(&mut output).unwrap();
        let n = assert_success(&output);
        assert_configuration_complete(&output[n..]);
        done_rx
            .recv_timeout(Duration::from_secs(5))
            .unwrap()
            .unwrap();
        worker.join().unwrap();
        ended_rx.recv_timeout(Duration::from_secs(1)).unwrap();
        assert_eq!(
            traffic.snapshot(),
            [
                (login.len() + pipeline.len()) as u64,
                output.len() as u64,
                7,
                74
            ]
        );
    }

    #[test]
    fn tcp_eof_and_partial_configuration_eof_clean_backend_sessions() {
        for partial_configuration in [false, true] {
            let backend = Arc::new(RecordingBackend::default());
            let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
            let mut client = TcpStream::connect(listener.local_addr().unwrap()).unwrap();
            client
                .set_read_timeout(Some(Duration::from_secs(5)))
                .unwrap();
            client
                .set_write_timeout(Some(Duration::from_secs(5)))
                .unwrap();
            let (server, _) = listener.accept().unwrap();
            let shared = backend.clone();
            let (done_tx, done_rx) = std::sync::mpsc::channel();
            let worker = std::thread::spawn(move || {
                done_tx
                    .send(handle_client(server, Arc::new(Traffic::default()), shared))
                    .unwrap();
            });
            let mut frames = [handshake(2), login_start("Notch")].concat();
            if partial_configuration {
                frames.extend([1, 3, 2, 4]);
            }
            client.write_all(&frames).unwrap();
            client.shutdown(Shutdown::Write).unwrap();
            let mut output = Vec::new();
            client.read_to_end(&mut output).unwrap();
            assert_success(&output);
            let result = done_rx.recv_timeout(Duration::from_secs(5)).unwrap();
            if partial_configuration {
                assert_eq!(result.unwrap_err().kind(), io::ErrorKind::UnexpectedEof);
            } else {
                result.unwrap();
            }
            worker.join().unwrap();
            let events = backend.events.lock().unwrap();
            assert_eq!(
                events.iter().map(|e| e.0).collect::<Vec<_>>(),
                if partial_configuration {
                    vec!["begin", "configure", "end"]
                } else {
                    vec!["begin", "end"]
                }
            );
            assert!(events.iter().all(|e| e.1 == events[0].1));
        }
    }

    #[test]
    fn preserves_transfer_intent_and_rejects_unknown_intent() {
        let mut transfer = session();
        transfer.receive(&handshake(3), &mut Vec::new()).unwrap();
        assert_eq!(transfer.state, ConnectionState::Closed);
        assert!(transfer.transferred);

        let mut invalid = session();
        let error = invalid.receive(&handshake(4), &mut Vec::new()).unwrap_err();
        assert_eq!(error.kind(), io::ErrorKind::InvalidData);
        assert_eq!(invalid.state, ConnectionState::Handshaking);
        assert_eq!(invalid.protocol_version, None);
    }
}
