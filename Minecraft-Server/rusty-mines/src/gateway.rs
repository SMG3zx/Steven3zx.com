use crate::{console_log, gateway_credentials, module_bindings::*};
use spacetimedb_sdk::{DbContext, Table, TableWithPrimaryKey, Uuid};
use std::{
    io,
    sync::{
        atomic::{AtomicBool, Ordering},
        mpsc, Arc, Mutex,
    },
    thread,
    time::{Duration, Instant},
};

const CONFIRMATION_TIMEOUT: Duration = Duration::from_secs(3);

#[derive(Default)]
struct RegistrationLog {
    identity: Option<String>,
    confirmed: Option<bool>,
    generation: u64,
    last_failure: Option<&'static str>,
}

impl RegistrationLog {
    fn failure(&mut self, message: &'static str) {
        self.last_failure = Some(message);
        self.generation = self.generation.wrapping_add(1);
    }
    fn confirm(&mut self, registered: bool) -> Option<crate::console::LogRecord> {
        let identity = self.identity.as_deref()?;
        if self.confirmed == Some(registered) {
            return None;
        }
        self.confirmed = Some(registered);
        self.generation = self.generation.wrapping_add(1);
        Some(crate::console::LogRecord::gateway(identity, registered))
    }
}

fn log_registration(state: &Mutex<RegistrationLog>, registered: bool) {
    if let Some(record) = state.lock().unwrap().confirm(registered) {
        crate::console_record(record);
    }
}

pub(crate) trait SessionStore: Send + Sync {
    // On failure, clean up any begin request that may still commit asynchronously.
    fn begin(&self, session: uuid::Uuid, username: &str) -> io::Result<()>;
    // Return the positive wire entity ID from the confirmed, connection-owned row.
    fn configure(&self, session: uuid::Uuid) -> io::Result<i32>;
    fn play(&self, _session: uuid::Uuid) -> io::Result<()> {
        Err(io::Error::other("Backend does not support Play"))
    }
    fn world_players(&self) -> Vec<GatewayWorldPlayer> {
        Vec::new()
    }
    fn update_pose(&self, _update: PlayerPoseUpdate) -> io::Result<()> {
        Err(io::Error::new(
            io::ErrorKind::Unsupported,
            "Backend player replication is unavailable",
        ))
    }
    fn chat_send(
        &self,
        _session: uuid::Uuid,
        _text: String,
    ) -> io::Result<mpsc::Receiver<io::Result<()>>> {
        Err(io::Error::new(
            io::ErrorKind::Unsupported,
            "Backend player chat is unavailable",
        ))
    }
    // Cache-only liveness check; workers never perform network waits per packet.
    fn check_session(&self, _session: uuid::Uuid) -> io::Result<()> {
        Ok(())
    }
    fn inventory_snapshot(&self, _session: uuid::Uuid) -> io::Result<Option<InventorySnapshot>> {
        Ok(None)
    }
    fn inventory_submit(
        &self,
        _session: uuid::Uuid,
        _sequence: u64,
        _payload: Vec<u8>,
    ) -> io::Result<mpsc::Receiver<io::Result<()>>> {
        Err(io::Error::new(
            io::ErrorKind::Unsupported,
            "Backend inventory module is unavailable",
        ))
    }
    fn chunk_snapshot(
        &self,
        _session: uuid::Uuid,
        _chunk_x: i32,
        _chunk_z: i32,
    ) -> io::Result<Option<GatewayChunkSnapshot>> {
        Ok(None)
    }
    #[allow(clippy::too_many_arguments)]
    fn block_action_submit(
        &self,
        _session: uuid::Uuid,
        _sequence: u64,
        _x: i32,
        _y: i32,
        _z: i32,
        _expected_state: u32,
        _place: bool,
    ) -> io::Result<mpsc::Receiver<io::Result<()>>> {
        Err(io::Error::new(
            io::ErrorKind::Unsupported,
            "Backend world actions are unavailable",
        ))
    }
    fn block_action_snapshot(
        &self,
        _session: uuid::Uuid,
    ) -> io::Result<Option<GatewayBlockAction>> {
        Ok(None)
    }
    fn game_mode(&self, _session: uuid::Uuid) -> io::Result<Option<GameMode>> {
        Ok(None)
    }
    fn player_state(&self, _session: uuid::Uuid) -> io::Result<Option<GatewayPlayerState>> {
        Ok(None)
    }
    fn requires_persistent_player_state(&self) -> bool {
        false
    }
    fn requires_persistent_world_snapshots(&self) -> bool {
        false
    }
    fn player_state_update(
        &self,
        _session: uuid::Uuid,
        _health: u8,
        _food: u8,
        _dead: bool,
        _expected_revision: u64,
    ) -> io::Result<mpsc::Receiver<io::Result<()>>> {
        Err(io::Error::new(
            io::ErrorKind::Unsupported,
            "Backend player state is unavailable",
        ))
    }
    fn player_respawn_request(
        &self,
        _session: uuid::Uuid,
    ) -> io::Result<mpsc::Receiver<io::Result<()>>> {
        Err(io::Error::new(
            io::ErrorKind::Unsupported,
            "Backend respawn is unavailable",
        ))
    }
    fn change_game_mode(
        &self,
        _session: uuid::Uuid,
        _mode: GameMode,
    ) -> io::Result<mpsc::Receiver<io::Result<()>>> {
        Err(io::Error::new(
            io::ErrorKind::Unsupported,
            "Backend mode transitions are unavailable",
        ))
    }
    fn end(&self, session: uuid::Uuid);
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) struct GatewayPlayerState {
    pub(crate) mode: GameMode,
    pub(crate) health: u8,
    pub(crate) food: u8,
    pub(crate) dead: bool,
    pub(crate) revision: u64,
    pub(crate) respawn_revision: Option<u64>,
    pub(crate) state_owner: Option<spacetimedb_sdk::Identity>,
}

pub(crate) struct Gateway {
    connection: Option<Arc<DbConnection>>,
    connected: Arc<AtomicBool>,
    subscribed: Arc<AtomicBool>,
    diagnostics: Arc<Mutex<RegistrationLog>>,
    inventory_supported: Arc<AtomicBool>,
    inventory_checked: Arc<AtomicBool>,
    world_actions_supported: Arc<AtomicBool>,
    modes_supported: Arc<AtomicBool>,
    player_state_supported: Arc<AtomicBool>,
    respawn_supported: Arc<AtomicBool>,
    world_players_supported: Arc<AtomicBool>,
    chat_supported: Arc<AtomicBool>,
    pose_updates: mpsc::SyncSender<PlayerPoseUpdate>,
}

struct SessionAttempt<'a> {
    gateway: &'a dyn SessionStore,
    session: uuid::Uuid,
    confirmed: bool,
}

impl Drop for SessionAttempt<'_> {
    fn drop(&mut self) {
        if !self.confirmed {
            self.gateway.end(self.session);
        }
    }
}

fn db_uuid(value: uuid::Uuid) -> Uuid {
    Uuid::from_u128(value.as_u128())
}

fn configuration_entity_id(
    row: &PlayerSession,
    session: Uuid,
    identity: spacetimedb_sdk::Identity,
    connection: spacetimedb_sdk::ConnectionId,
) -> io::Result<i32> {
    if row.session_uuid != session
        || row.gateway_identity != identity
        || row.owner_connection != connection
        || row.phase != SessionPhase::Configuration
    {
        return Err(io::Error::new(
            io::ErrorKind::PermissionDenied,
            "Session is not owned Configuration state",
        ));
    }
    if row.entity_id <= 0 {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "Session has no valid entity ID; reconnect",
        ));
    }
    Ok(row.entity_id)
}

impl Gateway {
    pub(crate) fn connect(
        host: &str,
        database: &str,
        controls: Arc<crate::players::Registry>,
    ) -> Arc<Self> {
        let connected = Arc::new(AtomicBool::new(false));
        let subscribed = Arc::new(AtomicBool::new(false));
        let registration = Arc::new(Mutex::new(RegistrationLog::default()));
        let inventory_supported = Arc::new(AtomicBool::new(false));
        let inventory_checked = Arc::new(AtomicBool::new(false));
        let world_actions_supported = Arc::new(AtomicBool::new(false));
        let modes_supported = Arc::new(AtomicBool::new(false));
        let player_state_supported = Arc::new(AtomicBool::new(false));
        let respawn_supported = Arc::new(AtomicBool::new(false));
        let world_players_supported = Arc::new(AtomicBool::new(false));
        let chat_supported = Arc::new(AtomicBool::new(false));
        let (pose_update_sender, pose_update_receiver) =
            mpsc::sync_channel::<PlayerPoseUpdate>(2048);
        let credentials = gateway_credentials::path(host, database).and_then(|path| {
            console_log(format!("Gateway credential file: {}", path.display()));
            gateway_credentials::load(&path).map(|token| (path, token))
        });
        let (token_path, token) = match credentials {
            Ok(credentials) => credentials,
            Err(_) => {
                console_log("Cannot load gateway credentials; login disabled, refusing anonymous fallback. Check credential file and permissions.".into());
                return Arc::new(Self {
                    connection: None,
                    inventory_supported,
                    inventory_checked,
                    world_actions_supported,
                    modes_supported,
                    player_state_supported,
                    respawn_supported,
                    connected,
                    subscribed,
                    diagnostics: {
                        registration
                            .lock()
                            .unwrap()
                            .failure("Credential loading failed; anonymous fallback refused");
                        registration
                    },
                    world_players_supported,
                    chat_supported,
                    pose_updates: pose_update_sender,
                });
            }
        };
        let connect_registration = registration.clone();
        let on_connect = connected.clone();
        let on_error = connected.clone();
        let on_disconnect = connected.clone();
        let disconnect_subscription = subscribed.clone();
        let disconnect_chat = chat_supported.clone();
        let error_diagnostics = registration.clone();
        let disconnect_diagnostics = registration.clone();
        let save_diagnostics = registration.clone();
        let connection = DbConnection::builder()
            .with_uri(host).with_database_name(database).with_token(token)
            .on_connect(move |conn, identity, token| {
                if gateway_credentials::save(&token_path, token).is_err() {
                    save_diagnostics.lock().unwrap().failure("Secure credential persistence failed");
                    console_log("Cannot persist gateway credentials securely; database connection disabled".into());
                    let _ = conn.disconnect();
                    return;
                }
                on_connect.store(true, Ordering::Release);
                let mut state = connect_registration.lock().unwrap();
                state.identity = Some(identity.to_string());
                state.confirmed = None;
                state.generation = state.generation.wrapping_add(1);
                console_log(format!("Gateway identity: {identity} [checking registration]"));
            })
            .on_connect_error(move |_, _| {
                on_error.store(false, Ordering::Release);
                error_diagnostics.lock().unwrap().failure("SDK connection failed");
                console_log("SpacetimeDB connection failed; login is unavailable (check host, database, and saved token)".into());
            })
            .on_disconnect(move |_, _| {
                on_disconnect.store(false, Ordering::Release);
                disconnect_subscription.store(false, Ordering::Release);
                disconnect_chat.store(false, Ordering::Release);
                disconnect_diagnostics.lock().unwrap().failure("SDK disconnected");
                console_log("Disconnected from SpacetimeDB; login is unavailable".into());
            })
            .build();
        let connection = match connection {
            Ok(connection) => {
                let replication_controls = controls.clone();
                connection
                    .db
                    .gateway_world_players()
                    .on_insert(move |_, row| {
                        replication_controls.publish_replication(
                            crate::players::ReplicationEvent::Upsert(row.clone()),
                        );
                    });
                let chat_controls = controls.clone();
                let chat_ready = chat_supported.clone();
                connection
                    .db
                    .gateway_chat_messages()
                    .on_insert(move |_, row| {
                        if chat_ready.load(Ordering::Acquire) {
                            chat_controls.publish_chat(crate::players::ChatDelivery::new(
                                row.username.clone(),
                                row.text.clone(),
                            ));
                        }
                    });
                let chat_clear_controls = controls.clone();
                connection
                    .db
                    .gateway_chat_messages()
                    .on_delete(move |_, _| {
                        chat_clear_controls.clear_chat();
                    });
                let replication_controls = controls.clone();
                connection
                    .db
                    .gateway_world_players()
                    .on_update(move |_, _, row| {
                        replication_controls.publish_replication(
                            crate::players::ReplicationEvent::Upsert(row.clone()),
                        );
                    });
                let replication_controls = controls.clone();
                connection
                    .db
                    .gateway_world_players()
                    .on_delete(move |_, row| {
                        replication_controls.publish_replication(
                            crate::players::ReplicationEvent::Remove(uuid::Uuid::from_u128(
                                row.session_uuid.as_u128(),
                            )),
                        );
                    });
                let applied = subscribed.clone();
                let subscription_diagnostics = registration.clone();
                let applied_registration = registration.clone();
                let failed = subscribed.clone();
                let inserted = registration.clone();
                let insert_applied = subscribed.clone();
                connection.db.my_gateway().on_insert(move |ctx, _| {
                    if insert_applied.load(Ordering::Acquire) {
                        log_registration(&inserted, ctx.db.my_gateway().count() > 0);
                    }
                });
                let deleted = registration.clone();
                let delete_applied = subscribed.clone();
                connection.db.my_gateway().on_delete(move |ctx, _| {
                    if delete_applied.load(Ordering::Acquire) {
                        log_registration(&deleted, ctx.db.my_gateway().count() > 0);
                    }
                });
                connection.subscription_builder()
                    .on_applied(move |ctx| {
                        applied.store(true, Ordering::Release);
                        log_registration(&applied_registration, ctx.db.my_gateway().count() > 0);
                        console_log("Subscribed to gateway authorization, sessions, and profiles".into());
                    })
                    .on_error(move |_, _| {
                        failed.store(false, Ordering::Release);
                        subscription_diagnostics.lock().unwrap().failure("Gateway subscription failed");
                        console_log("Gateway subscription failed; login is unavailable (publish matching module)".into());
                    })
                    .subscribe(["SELECT * FROM my_gateway", "SELECT * FROM gateway_sessions", "SELECT * FROM gateway_profiles"]);
                let chat_applied = chat_supported.clone();
                let chat_failed = chat_supported.clone();
                connection
                    .subscription_builder()
                    .on_applied(move |_| {
                        chat_applied.store(true, Ordering::Release);
                        console_log(
                            "Deployed module supports unsigned gateway-scoped player chat".into(),
                        );
                    })
                    .on_error(move |_, _| {
                        chat_failed.store(false, Ordering::Release);
                        console_log(
                            "Unsigned player chat is unavailable in the deployed module".into(),
                        );
                    })
                    .subscribe("SELECT * FROM gateway_chat_messages");
                let world_applied = world_players_supported.clone();
                let world_failed = world_players_supported.clone();
                let initial_replication_controls = controls.clone();
                connection.subscription_builder()
                    .on_applied(move |ctx| {
                        world_applied.store(true, Ordering::Release);
                        for row in ctx.db.gateway_world_players().iter().take(128) {
                            initial_replication_controls.publish_replication(crate::players::ReplicationEvent::Upsert(row));
                        }
                        console_log("Subscribed to identity-scoped Play player snapshots".into());
                    })
                    .on_error(move |_, _| {
                        world_failed.store(false, Ordering::Release);
                        console_log("M4 player replication is unavailable; publish the reviewed Play replication module".into());
                    })
                    .subscribe("SELECT * FROM gateway_world_players");
                // M2 is a separately published upgrade. Probe its public diagnostic
                // view without broadening the required foundation subscription.
                // Older deployments must not receive a reducer they do not have.
                let lease_supported = Arc::new(AtomicBool::new(false));
                subscribe_lease_capability(
                    &connection,
                    lease_supported.clone(),
                    registration.clone(),
                );
                let inventory_applied = inventory_supported.clone();
                let inventory_failed = inventory_supported.clone();
                let inventory_applied_checked = inventory_checked.clone();
                let inventory_failed_checked = inventory_checked.clone();
                connection.subscription_builder()
                    .on_applied(move |_| { inventory_applied.store(true,Ordering::Release); inventory_applied_checked.store(true,Ordering::Release); console_log("Deployed module supports authoritative inventory".into()); })
                    .on_error(move |_,_| { inventory_failed.store(false,Ordering::Release); inventory_failed_checked.store(true,Ordering::Release); console_log("M3 inventory is unavailable in the deployed module; publish the reviewed upgrade to enable it".into()); })
                    .subscribe("SELECT * FROM gateway_inventory_snapshots");
                let world_actions_applied = world_actions_supported.clone();
                let world_actions_failed = world_actions_supported.clone();
                connection.subscription_builder()
                    .on_applied(move |_| world_actions_applied.store(true, Ordering::Release))
                    .on_error(move |_, _| {
                        world_actions_failed.store(false, Ordering::Release);
                        console_log("M6 world action and chunk snapshot views are unavailable in the deployed module".into());
                    })
                    .subscribe(["SELECT * FROM gateway_block_actions", "SELECT * FROM gateway_chunk_snapshots"]);
                let mode_applied = modes_supported.clone();
                let mode_failed = modes_supported.clone();
                connection
                    .subscription_builder()
                    .on_applied(move |_| mode_applied.store(true, Ordering::Release))
                    .on_error(move |_, _| {
                        mode_failed.store(false, Ordering::Release);
                        console_log("M7 mode view is unavailable in the deployed module".into());
                    })
                    .subscribe("SELECT * FROM gateway_game_modes");
                let state_applied = player_state_supported.clone();
                let state_failed = player_state_supported.clone();
                let respawn_applied = respawn_supported.clone();
                let respawn_failed = respawn_supported.clone();
                connection
                    .subscription_builder()
                    .on_applied(move |_| {
                        state_applied.store(true, Ordering::Release);
                        respawn_applied.store(true, Ordering::Release);
                    })
                    .on_error(move |_, _| {
                        state_failed.store(false, Ordering::Release);
                        respawn_failed.store(false, Ordering::Release);
                        console_log("M7 persistent player state view is unavailable".into());
                    })
                    .subscribe("SELECT * FROM gateway_player_state");
                connection.run_threaded();
                let heartbeat_connection = Arc::new(connection);
                let heartbeat_source = heartbeat_connection.clone();
                let heartbeat_connected = connected.clone();
                let heartbeat_diagnostics = registration.clone();
                thread::spawn(move || {
                    // The on_connect callback may not have run when this thread starts.
                    // SDK transport liveness covers both connecting and connected states.
                    while heartbeat_source.is_active() {
                        thread::sleep(Duration::from_secs(5));
                        if !heartbeat_connected.load(Ordering::Acquire)
                            || !lease_supported.load(Ordering::Acquire)
                        {
                            continue;
                        }
                        let owner = heartbeat_source.connection_id();
                        let sessions: Vec<_> = heartbeat_source
                            .db
                            .gateway_sessions()
                            .iter()
                            .filter(|session| session.owner_connection == owner)
                            .map(|session| session.session_uuid)
                            .collect();
                        for batch in sessions.chunks(64) {
                            let diagnostics = heartbeat_diagnostics.clone();
                            if heartbeat_source
                                .reducers
                                .heartbeat_sessions_then(batch.to_vec(), move |ctx, result| {
                                    match result {
                                        Ok(Ok(())) => {}
                                        Ok(Err(_)) => {
                                            // A session can end between the cache snapshot and
                                            // this transaction. Retry the next cache snapshot;
                                            // subscribed deletion closes only affected workers.
                                            diagnostics.lock().unwrap_or_else(|e| e.into_inner()).failure("Session heartbeat batch rejected");
                                        }
                                        Err(_) => {
                                            diagnostics.lock().unwrap_or_else(|e| e.into_inner()).failure("Session heartbeat failed internally");
                                            console_log("Session heartbeat failed; disconnecting gateway so clients reconnect".into());
                                            let _ = ctx.disconnect();
                                        }
                                    }
                                })
                                .is_err()
                            {
                                heartbeat_diagnostics.lock().unwrap_or_else(|e| e.into_inner()).failure("Cannot enqueue session heartbeat");
                                console_log("Session heartbeat could not be queued; disconnecting gateway".into());
                                let _ = heartbeat_source.disconnect();
                                break;
                            }
                        }
                    }
                });
                let pose_connection = heartbeat_connection.clone();
                let pose_connected = connected.clone();
                let pose_supported = world_players_supported.clone();
                let pose_diagnostics = registration.clone();
                thread::spawn(move || {
                    use std::collections::HashMap;
                    while pose_connection.is_active() {
                        thread::sleep(Duration::from_millis(50));
                        if !pose_connected.load(Ordering::Acquire)
                            || !pose_supported.load(Ordering::Acquire)
                        {
                            continue;
                        }
                        let mut latest = HashMap::new();
                        while latest.len() < 256 {
                            match pose_update_receiver.try_recv() {
                                Ok(update) => {
                                    latest.insert(update.session_uuid, update);
                                }
                                Err(
                                    mpsc::TryRecvError::Empty | mpsc::TryRecvError::Disconnected,
                                ) => break,
                            }
                        }
                        let updates: Vec<_> = latest.into_values().collect();
                        for batch in updates.chunks(64) {
                            let failed = pose_connection.clone();
                            let diagnostics = pose_diagnostics.clone();
                            if pose_connection.reducers.update_player_states_then(batch.to_vec(), move |ctx, result| {
                                if !matches!(result, Ok(Ok(()))) {
                                    diagnostics.lock().unwrap_or_else(|e| e.into_inner()).failure("Authoritative player pose update failed");
                                    console_log("Player pose update failed; disconnecting gateway and active sessions".into());
                                    let _ = ctx.disconnect();
                                }
                            }).is_err() {
                                pose_diagnostics.lock().unwrap_or_else(|e| e.into_inner()).failure("Cannot enqueue authoritative player pose update");
                                let _ = failed.disconnect();
                                break;
                            }
                        }
                    }
                });
                Some(heartbeat_connection)
            }
            Err(_) => {
                registration
                    .lock()
                    .unwrap()
                    .failure("SDK initialization failed");
                console_log("Cannot initialize SpacetimeDB connection; status/console remain available, login disabled".into());
                None
            }
        };
        Arc::new(Self {
            connection,
            connected,
            subscribed,
            diagnostics: registration,
            inventory_supported,
            inventory_checked,
            world_actions_supported,
            modes_supported,
            player_state_supported,
            respawn_supported,
            world_players_supported,
            chat_supported,
            pose_updates: pose_update_sender,
        })
    }

    pub(crate) fn diagnostics(
        &self,
        host: &str,
        database: &str,
        players: crate::players::Snapshot,
    ) -> crate::gateway_dashboard::Snapshot {
        let state = self.diagnostics.lock().unwrap();
        let connected = self.connected.load(Ordering::Acquire);
        let subscribed = self.subscribed.load(Ordering::Acquire);
        crate::gateway_dashboard::Snapshot {
            host: host.into(),
            database: database.into(),
            identity: state.identity.clone(),
            owner_connection: self
                .connection
                .as_ref()
                .filter(|_| connected)
                .map(|conn| conn.connection_id().to_string()),
            connected,
            subscribed,
            registered: if connected && subscribed {
                state.confirmed
            } else {
                None
            },
            health: self.status(),
            generation: state.generation,
            last_failure: state.last_failure,
            players,
        }
    }

    pub(crate) fn status(&self) -> &'static str {
        if !self.connected.load(Ordering::Acquire) {
            "disconnected"
        } else if !self.subscribed.load(Ordering::Acquire) {
            "subscribing"
        } else if self
            .connection
            .as_ref()
            .is_some_and(|conn| conn.db.my_gateway().count() > 0)
        {
            "authorized"
        } else {
            "unauthorized"
        }
    }

    pub(crate) fn players(&self, registry: &crate::players::Registry) -> crate::players::Snapshot {
        if self.status() != "authorized" {
            return crate::players::Snapshot::default();
        }
        self.connection
            .as_ref()
            .map_or_else(crate::players::Snapshot::default, |conn| {
                // Collect SDK cache rows before taking the local control lock.
                // The subscribed view is identity-scoped, not connection-scoped.
                let sessions: Vec<_> = conn
                    .db
                    .gateway_sessions()
                    .iter()
                    .map(|row| {
                        (
                            uuid::Uuid::from_u128(row.session_uuid.as_u128()),
                            row.phase,
                            row.owner_connection == conn.connection_id(),
                        )
                    })
                    .collect();
                registry.snapshot(sessions)
            })
    }

    pub(crate) fn play_count(&self, registry: &crate::players::Registry) -> usize {
        self.players(registry)
            .rows
            .iter()
            .filter(|player| player.phase == SessionPhase::Play)
            .count()
    }

    fn ready(&self) -> io::Result<&Arc<DbConnection>> {
        if self.status() != "authorized" {
            return Err(io::Error::other(
                "Backend unavailable or gateway unauthorized",
            ));
        }
        self.connection
            .as_ref()
            .ok_or_else(|| io::Error::other("Backend unavailable"))
    }

    fn wait_for(
        &self,
        receiver: mpsc::Receiver<io::Result<()>>,
        deadline: Instant,
        confirmed: impl Fn() -> bool,
    ) -> io::Result<()> {
        let result = self.wait_for_inner(receiver, deadline, confirmed);
        if let Err(error) = &result {
            self.diagnostics
                .lock()
                .unwrap()
                .failure(if error.kind() == io::ErrorKind::TimedOut {
                    "Backend confirmation timed out"
                } else {
                    "Backend reducer rejected, failed or became unavailable"
                });
        }
        result
    }

    fn wait_for_inner(
        &self,
        receiver: mpsc::Receiver<io::Result<()>>,
        deadline: Instant,
        confirmed: impl Fn() -> bool,
    ) -> io::Result<()> {
        receiver
            .recv_timeout(deadline.saturating_duration_since(Instant::now()))
            .map_err(|_| {
                io::Error::new(
                    io::ErrorKind::TimedOut,
                    "Backend reducer confirmation timed out",
                )
            })??;
        loop {
            self.ready()?;
            if confirmed() {
                return Ok(());
            }
            if Instant::now() >= deadline {
                return Err(io::Error::new(
                    io::ErrorKind::TimedOut,
                    "Backend subscription confirmation timed out",
                ));
            }
            thread::sleep(Duration::from_millis(10));
        }
    }

    fn wait_until(&self, deadline: Instant, confirmed: impl Fn() -> bool) -> io::Result<()> {
        loop {
            self.ready()?;
            if confirmed() {
                return Ok(());
            }
            if Instant::now() >= deadline {
                return Err(io::Error::new(
                    io::ErrorKind::TimedOut,
                    "Backend subscription confirmation timed out",
                ));
            }
            thread::sleep(Duration::from_millis(10));
        }
    }

    pub(crate) fn disconnect(&self) {
        self.connected.store(false, Ordering::Release);
        self.subscribed.store(false, Ordering::Release);
        if let Some(conn) = &self.connection {
            let _ = conn.disconnect();
        }
    }
}

fn subscribe_lease_capability(
    connection: &DbConnection,
    supported: Arc<AtomicBool>,
    diagnostics: Arc<Mutex<RegistrationLog>>,
) {
    let applied = supported.clone();
    connection.subscription_builder()
        .on_applied(move |_| {
            applied.store(true, Ordering::Release);
            console_log("Deployed module supports session leases; heartbeats enabled".into());
        })
        .on_error(move |_, _| {
            supported.store(false, Ordering::Release);
            diagnostics.lock().unwrap_or_else(|e| e.into_inner())
                .failure("Session leases unavailable; deployed module upgrade required");
            console_log("Deployed module lacks M2 lease support; using the existing session lifecycle without lease heartbeats. Publish the reviewed module to enable leases.".into());
        })
        .subscribe("SELECT * FROM admin_lease_diagnostic");
}

impl SessionStore for Gateway {
    fn begin(&self, session: uuid::Uuid, username: &str) -> io::Result<()> {
        let conn = self.ready()?;
        // An unconfirmed call may commit after our deadline. Queue cleanup on
        // the same connection even on timeout, send failure, or unwinding.
        let mut attempt = SessionAttempt {
            gateway: self,
            session,
            confirmed: false,
        };
        let expected_player =
            db_uuid(crate::packets::login::offline_uuid(username).map_err(io::Error::other)?);
        let session = db_uuid(session);
        let username = username.to_owned();
        let (sender, receiver) = mpsc::channel();
        let deadline = Instant::now() + CONFIRMATION_TIMEOUT;
        conn.reducers
            .begin_offline_session_then(session, username.clone(), move |_, result| {
                let _ = sender.send(match result {
                    Ok(Ok(())) => Ok(()),
                    Ok(Err(error)) => Err(io::Error::other(error)),
                    Err(_) => Err(io::Error::other("Backend reducer failed internally")),
                });
            })
            .map_err(|_| {
                self.diagnostics
                    .lock()
                    .unwrap()
                    .failure("Cannot enqueue backend login");
                io::Error::other("Cannot enqueue backend login")
            })?;
        let result = self.wait_for(receiver, deadline, || {
            conn.db.gateway_sessions().iter().any(|row| {
                row.session_uuid == session
                    && row.player_uuid == expected_player
                    && row.phase == SessionPhase::Login
                    && row.owner_connection == conn.connection_id()
            }) && conn.db.gateway_profiles().iter().any(|row| {
                row.player_uuid == expected_player
                    && row.username == username
                    && row.identity_kind == IdentityKind::Offline
            })
        });
        attempt.confirmed = result.is_ok();
        result
    }

    fn configure(&self, session: uuid::Uuid) -> io::Result<i32> {
        let conn = self.ready()?;
        let session = db_uuid(session);
        let (sender, receiver) = mpsc::channel();
        let deadline = Instant::now() + CONFIRMATION_TIMEOUT;
        conn.reducers
            .advance_login_session_then(session, move |_, result| {
                let _ = sender.send(match result {
                    Ok(Ok(())) => Ok(()),
                    Ok(Err(error)) => Err(io::Error::other(error)),
                    Err(_) => Err(io::Error::other(
                        "Backend phase transition failed internally",
                    )),
                });
            })
            .map_err(|_| {
                self.diagnostics
                    .lock()
                    .unwrap()
                    .failure("Cannot enqueue backend phase transition");
                io::Error::other("Cannot enqueue backend phase transition")
            })?;
        self.wait_for(receiver, deadline, || {
            conn.db.gateway_sessions().iter().any(|row| {
                row.session_uuid == session
                    && row.phase == SessionPhase::Configuration
                    && row.gateway_identity == conn.identity()
                    && row.owner_connection == conn.connection_id()
            })
        })
        .map_err(|error| {
            io::Error::new(
                error.kind(),
                format!("Configuration phase confirmation failed: {error}"),
            )
        })?;
        let row = conn
            .db
            .gateway_sessions()
            .iter()
            .find(|row| {
                row.session_uuid == session
                    && row.phase == SessionPhase::Configuration
                    && row.owner_connection == conn.connection_id()
            })
            .ok_or_else(|| io::Error::other("Confirmed session is no longer available"))?;
        let entity = configuration_entity_id(&row, session, conn.identity(), conn.connection_id())?;
        let probe_deadline = Instant::now() + CONFIRMATION_TIMEOUT;
        while !self.inventory_checked.load(Ordering::Acquire) {
            self.ready()?;
            if Instant::now() >= probe_deadline {
                return Err(io::Error::new(
                    io::ErrorKind::TimedOut,
                    "Inventory capability probe timed out",
                ));
            }
            thread::sleep(Duration::from_millis(10));
        }
        if self.inventory_supported.load(Ordering::Acquire) {
            let (sender, receiver) = mpsc::channel();
            conn.reducers
                .initialize_player_inventory_then(session, move |_, result| {
                    let _ = sender.send(match result {
                        Ok(Ok(())) => Ok(()),
                        Ok(Err(error)) => Err(io::Error::other(error)),
                        Err(_) => Err(io::Error::other(
                            "Inventory initialization failed internally",
                        )),
                    });
                })
                .map_err(|_| io::Error::other("Cannot enqueue inventory initialization"))?;
            self.wait_for(receiver, Instant::now() + CONFIRMATION_TIMEOUT, || {
                conn.db.gateway_inventory_snapshots().iter().any(|row| {
                    row.session_uuid == session && row.owner_connection == conn.connection_id()
                })
            })
            .map_err(|error| {
                io::Error::new(
                    error.kind(),
                    format!("Inventory initialization confirmation failed: {error}"),
                )
            })?;
        }
        Ok(entity)
    }

    fn play(&self, session: uuid::Uuid) -> io::Result<()> {
        let conn = self.ready()?;
        let session = db_uuid(session);
        let (sender, receiver) = mpsc::channel();
        let deadline = Instant::now() + CONFIRMATION_TIMEOUT;
        conn.reducers
            .enter_play_session_then(session, move |_, result| {
                let _ = sender.send(match result {
                    Ok(Ok(())) => Ok(()),
                    Ok(Err(error)) => Err(io::Error::other(error)),
                    Err(_) => Err(io::Error::other(
                        "Backend Play transition failed internally",
                    )),
                });
            })
            .map_err(|_| {
                self.diagnostics
                    .lock()
                    .unwrap()
                    .failure("Cannot enqueue backend Play transition");
                io::Error::other("Cannot enqueue backend Play transition")
            })?;
        self.wait_for(receiver, deadline, || {
            conn.db.gateway_sessions().iter().any(|row| {
                row.session_uuid == session
                    && row.phase == SessionPhase::Play
                    && row.owner_connection == conn.connection_id()
            })
        })?;
        if self.inventory_supported.load(Ordering::Acquire) {
            let deadline = Instant::now() + CONFIRMATION_TIMEOUT;
            self.wait_until(deadline, || {
                conn.db.gateway_inventory_snapshots().iter().any(|row| {
                    row.session_uuid == session && row.owner_connection == conn.connection_id()
                })
            })?;
        }
        Ok(())
    }

    fn world_players(&self) -> Vec<GatewayWorldPlayer> {
        if !self.world_players_supported.load(Ordering::Acquire) || self.status() != "authorized" {
            return Vec::new();
        }
        self.connection.as_ref().map_or_else(Vec::new, |conn| {
            conn.db.gateway_world_players().iter().take(128).collect()
        })
    }

    fn update_pose(&self, update: PlayerPoseUpdate) -> io::Result<()> {
        if !self.world_players_supported.load(Ordering::Acquire) {
            return Err(io::Error::new(
                io::ErrorKind::Unsupported,
                "Backend player replication is unavailable",
            ));
        }
        self.pose_updates.try_send(update).map_err(|_| {
            self.diagnostics
                .lock()
                .unwrap_or_else(|e| e.into_inner())
                .failure("Authoritative player pose queue is full or closed");
            self.disconnect();
            io::Error::other("Cannot queue authoritative player state; reconnect")
        })
    }

    fn chat_send(
        &self,
        session: uuid::Uuid,
        text: String,
    ) -> io::Result<mpsc::Receiver<io::Result<()>>> {
        if !self.chat_supported.load(Ordering::Acquire) {
            return Err(io::Error::new(
                io::ErrorKind::Unsupported,
                "Unsigned player chat is unavailable",
            ));
        }
        self.check_session(session)?;
        let conn = self.ready()?;
        let (sender, receiver) = mpsc::channel();
        conn.reducers
            .send_chat_then(db_uuid(session), text, move |_, result| {
                let _ = sender.send(match result {
                    Ok(Ok(())) => Ok(()),
                    Ok(Err(error)) => Err(io::Error::other(error)),
                    Err(_) => Err(io::Error::other(
                        "Player chat transaction failed internally",
                    )),
                });
            })
            .map_err(|_| io::Error::other("Cannot enqueue player chat"))?;
        Ok(receiver)
    }

    fn check_session(&self, session: uuid::Uuid) -> io::Result<()> {
        let conn = self.ready()?;
        if conn.db.gateway_sessions().iter().any(|row| {
            row.session_uuid == db_uuid(session)
                && row.gateway_identity == conn.identity()
                && row.owner_connection == conn.connection_id()
        }) {
            Ok(())
        } else {
            Err(io::Error::new(
                io::ErrorKind::PermissionDenied,
                "Backend session expired or was removed",
            ))
        }
    }

    fn inventory_snapshot(&self, session: uuid::Uuid) -> io::Result<Option<InventorySnapshot>> {
        if !self.inventory_supported.load(Ordering::Acquire) {
            return Ok(None);
        }
        self.check_session(session)?;
        let conn = self.ready()?;
        Ok(conn.db.gateway_inventory_snapshots().iter().find(|row| {
            row.session_uuid == db_uuid(session) && row.owner_connection == conn.connection_id()
        }))
    }

    fn inventory_submit(
        &self,
        session: uuid::Uuid,
        sequence: u64,
        payload: Vec<u8>,
    ) -> io::Result<mpsc::Receiver<io::Result<()>>> {
        if !self.inventory_supported.load(Ordering::Acquire) {
            return Err(io::Error::new(
                io::ErrorKind::Unsupported,
                "Backend inventory upgrade required",
            ));
        }
        self.check_session(session)?;
        if payload.len() > rusty_mines_inventory::wire::MAX_BYTES {
            return Err(io::Error::other("Inventory request too large"));
        }
        let conn = self.ready()?;
        let (sender, receiver) = mpsc::channel();
        conn.reducers
            .apply_inventory_request_then(db_uuid(session), sequence, payload, move |_, result| {
                let _ = sender.send(match result {
                    Ok(Ok(())) => Ok(()),
                    Ok(Err(error)) => Err(io::Error::other(error)),
                    Err(_) => Err(io::Error::other("Inventory transaction failed internally")),
                });
            })
            .map_err(|_| io::Error::other("Cannot enqueue inventory transaction"))?;
        Ok(receiver)
    }

    fn chunk_snapshot(
        &self,
        session: uuid::Uuid,
        chunk_x: i32,
        chunk_z: i32,
    ) -> io::Result<Option<GatewayChunkSnapshot>> {
        if !self.world_actions_supported.load(Ordering::Acquire) {
            return Err(io::Error::new(
                io::ErrorKind::Unsupported,
                "Backend world snapshots are unavailable",
            ));
        }
        self.check_session(session)?;
        let conn = self.ready()?;
        Ok(conn
            .db
            .gateway_chunk_snapshots()
            .iter()
            .find(|row| row.chunk_x == chunk_x && row.chunk_z == chunk_z))
    }

    fn block_action_submit(
        &self,
        session: uuid::Uuid,
        sequence: u64,
        x: i32,
        y: i32,
        z: i32,
        expected_state: u32,
        place: bool,
    ) -> io::Result<mpsc::Receiver<io::Result<()>>> {
        if !self.world_actions_supported.load(Ordering::Acquire) {
            return Err(io::Error::new(
                io::ErrorKind::Unsupported,
                "Backend world actions are unavailable",
            ));
        }
        self.check_session(session)?;
        let conn = self.ready()?;
        let (sender, receiver) = mpsc::channel();
        conn.reducers
            .apply_block_action_then(
                db_uuid(session),
                sequence,
                x,
                y,
                z,
                expected_state,
                place,
                move |_, result| {
                    let _ = sender.send(match result {
                        Ok(Ok(())) => Ok(()),
                        Ok(Err(error)) => Err(io::Error::other(error)),
                        Err(_) => Err(io::Error::other(
                            "World action transaction failed internally",
                        )),
                    });
                },
            )
            .map_err(|_| io::Error::other("Cannot enqueue world action"))?;
        Ok(receiver)
    }

    fn block_action_snapshot(&self, session: uuid::Uuid) -> io::Result<Option<GatewayBlockAction>> {
        if !self.world_actions_supported.load(Ordering::Acquire) {
            return Ok(None);
        }
        self.check_session(session)?;
        let conn = self.ready()?;
        Ok(conn
            .db
            .gateway_block_actions()
            .iter()
            .find(|row| row.session_uuid == db_uuid(session)))
    }

    fn game_mode(&self, session: uuid::Uuid) -> io::Result<Option<GameMode>> {
        if !self.modes_supported.load(Ordering::Acquire) {
            return Ok(None);
        }
        self.check_session(session)?;
        let conn = self.ready()?;
        Ok(conn
            .db
            .gateway_player_state()
            .iter()
            .find(|row| row.session_uuid == db_uuid(session))
            .map(|row| row.mode))
    }

    fn player_state(&self, session: uuid::Uuid) -> io::Result<Option<GatewayPlayerState>> {
        if !self.player_state_supported.load(Ordering::Acquire) {
            return Err(io::Error::new(
                io::ErrorKind::Unsupported,
                "Persistent player state view is unavailable",
            ));
        }
        self.check_session(session)?;
        let conn = self.ready()?;
        Ok(conn
            .db
            .gateway_player_state()
            .iter()
            .find(|row| row.session_uuid == db_uuid(session))
            .map(|row| GatewayPlayerState {
                health: row.health,
                food: row.food,
                dead: row.dead,
                mode: row.mode,
                revision: row.revision,
                respawn_revision: row.respawn_revision,
                state_owner: row.state_owner,
            }))
    }

    fn requires_persistent_player_state(&self) -> bool {
        true
    }

    fn requires_persistent_world_snapshots(&self) -> bool {
        true
    }

    fn player_state_update(
        &self,
        session: uuid::Uuid,
        health: u8,
        food: u8,
        dead: bool,
        expected_revision: u64,
    ) -> io::Result<mpsc::Receiver<io::Result<()>>> {
        if !self.player_state_supported.load(Ordering::Acquire) {
            return Err(io::Error::new(
                io::ErrorKind::Unsupported,
                "Persistent player state update is unavailable",
            ));
        }
        self.check_session(session)?;
        let conn = self.ready()?;
        let (sender, receiver) = mpsc::channel();
        conn.reducers
            .update_player_vitals_then(
                db_uuid(session),
                health,
                food,
                dead,
                expected_revision,
                move |_, result| {
                    let _ = sender.send(match result {
                        Ok(Ok(())) => Ok(()),
                        Ok(Err(error)) => Err(io::Error::other(error)),
                        Err(_) => Err(io::Error::other("Player state update failed internally")),
                    });
                },
            )
            .map_err(|_| io::Error::other("Cannot enqueue player state update"))?;
        Ok(receiver)
    }

    fn player_respawn_request(
        &self,
        session: uuid::Uuid,
    ) -> io::Result<mpsc::Receiver<io::Result<()>>> {
        if !self.respawn_supported.load(Ordering::Acquire) {
            return Err(io::Error::new(
                io::ErrorKind::Unsupported,
                "Persistent player state update is unavailable",
            ));
        }
        self.check_session(session)?;
        let conn = self.ready()?;
        let (sender, receiver) = mpsc::channel();
        conn.reducers
            .request_player_respawn_then(db_uuid(session), move |_, result| {
                let _ = sender.send(match result {
                    Ok(Ok(())) => Ok(()),
                    Ok(Err(error)) => Err(io::Error::other(error)),
                    Err(_) => Err(io::Error::other("Player respawn failed internally")),
                });
            })
            .map_err(|_| io::Error::other("Cannot enqueue respawn request"))?;
        Ok(receiver)
    }

    fn change_game_mode(
        &self,
        session: uuid::Uuid,
        mode: GameMode,
    ) -> io::Result<mpsc::Receiver<io::Result<()>>> {
        if !self.modes_supported.load(Ordering::Acquire) {
            return Err(io::Error::new(
                io::ErrorKind::Unsupported,
                "Backend mode transitions are unavailable",
            ));
        }
        self.check_session(session)?;
        let conn = self.ready()?;
        let (sender, receiver) = mpsc::channel();
        conn.reducers
            .change_game_mode_then(db_uuid(session), mode, move |_, result| {
                let _ = sender.send(match result {
                    Ok(Ok(())) => Ok(()),
                    Ok(Err(error)) => Err(io::Error::other(error)),
                    Err(_) => Err(io::Error::other("Mode transition failed internally")),
                });
            })
            .map_err(|_| io::Error::other("Cannot enqueue mode transition"))?;
        Ok(receiver)
    }

    fn end(&self, session: uuid::Uuid) {
        // Unauthorized begin calls cannot have created a session; revocation
        // atomically deletes existing sessions. Do not disconnect enrollment.
        if self.status() == "unauthorized" {
            return;
        }
        if let Some(conn) = &self.connection {
            let cleanup_connection = conn.clone();
            let cleanup_diagnostics = self.diagnostics.clone();
            if conn.reducers.end_session_then(db_uuid(session), move |_, result| {
                if !matches!(result, Ok(Ok(()))) {
                    cleanup_diagnostics.lock().unwrap().failure("Backend session cleanup failed");
                    console_log("Backend session cleanup failed; disconnecting gateway for lifecycle cleanup".into());
                    let _ = cleanup_connection.disconnect();
                }
            }).is_err() {
                self.diagnostics.lock().unwrap().failure("Cannot enqueue backend cleanup");
                self.disconnect();
            }
        }
    }
}

#[cfg(test)]
pub(crate) struct TestSessions;
#[cfg(test)]
impl SessionStore for TestSessions {
    fn begin(&self, _: uuid::Uuid, _: &str) -> io::Result<()> {
        Ok(())
    }
    fn configure(&self, _: uuid::Uuid) -> io::Result<i32> {
        Ok(1)
    }
    fn play(&self, _: uuid::Uuid) -> io::Result<()> {
        Ok(())
    }
    fn end(&self, _: uuid::Uuid) {}
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    #[ignore = "Read-only SDK probe; set RUSTY_MINES_PROBE_HOST/DATABASE and expected lease support explicitly"]
    fn read_only_lease_probe_preserves_foundation_connection() {
        let host = std::env::var("RUSTY_MINES_PROBE_HOST").expect("Explicit probe host required");
        let database =
            std::env::var("RUSTY_MINES_PROBE_DATABASE").expect("Explicit probe database required");
        let expected: bool = std::env::var("RUSTY_MINES_PROBE_LEASES")
            .expect("Explicit expected capability required")
            .parse()
            .unwrap();
        let (sender, receiver) = mpsc::channel();
        let failed = sender.clone();
        let connection = DbConnection::builder()
            .with_uri(host)
            .with_database_name(database)
            .on_connect(move |_, _, _| {
                sender.send(true).unwrap();
            })
            .on_connect_error(move |_, _| {
                let _ = failed.send(false);
            })
            .build()
            .expect("Read-only anonymous connection failed");
        connection.run_threaded();
        assert!(receiver.recv_timeout(Duration::from_secs(10)).unwrap());
        let supported = Arc::new(AtomicBool::new(false));
        let diagnostics = Arc::new(Mutex::new(RegistrationLog::default()));
        subscribe_lease_capability(&connection, supported.clone(), diagnostics.clone());
        // Queue a barrier after the probe. Foundation views must still apply,
        // even when the optional lease view is absent.
        let (sender, receiver) = mpsc::channel();
        let failed = sender.clone();
        connection
            .subscription_builder()
            .on_applied(move |_| {
                sender.send(true).unwrap();
            })
            .on_error(move |_, _| {
                let _ = failed.send(false);
            })
            .subscribe([
                "SELECT * FROM my_gateway",
                "SELECT * FROM gateway_sessions",
                "SELECT * FROM gateway_profiles",
            ]);
        assert!(receiver.recv_timeout(Duration::from_secs(10)).unwrap());
        assert!(connection.is_active());
        assert_eq!(supported.load(Ordering::Acquire), expected);
        if !expected {
            assert_eq!(
                diagnostics.lock().unwrap().last_failure,
                Some("Session leases unavailable; deployed module upgrade required")
            );
        }
        connection.disconnect().unwrap();
    }

    #[test]
    fn entity_id_requires_matching_session_gateway_connection_and_phase() {
        use spacetimedb_sdk::{ConnectionId, Identity, Timestamp};
        let session = Uuid::from_u128(1);
        let identity =
            Identity::from_hex("0000000000000000000000000000000000000000000000000000000000000001")
                .unwrap();
        let connection = ConnectionId::from_u128(2);
        let mut row = PlayerSession {
            session_uuid: session,
            player_uuid: Uuid::from_u128(3),
            gateway_identity: identity,
            owner_connection: connection,
            phase: SessionPhase::Configuration,
            connected_at: Timestamp::from_micros_since_unix_epoch(0),
            last_activity: Timestamp::from_micros_since_unix_epoch(0),
            entity_id: i32::MAX,
        };
        assert_eq!(
            configuration_entity_id(&row, session, identity, connection).unwrap(),
            i32::MAX
        );
        assert!(configuration_entity_id(&row, Uuid::from_u128(99), identity, connection).is_err());
        let other =
            Identity::from_hex("0000000000000000000000000000000000000000000000000000000000000002")
                .unwrap();
        assert!(configuration_entity_id(&row, session, other, connection).is_err());
        assert!(
            configuration_entity_id(&row, session, identity, ConnectionId::from_u128(99)).is_err()
        );
        for phase in [SessionPhase::Login, SessionPhase::Play] {
            row.phase = phase;
            assert!(configuration_entity_id(&row, session, identity, connection).is_err());
        }
        row.phase = SessionPhase::Configuration;
        for id in [0, -1, i32::MIN] {
            row.entity_id = id;
            assert!(configuration_entity_id(&row, session, identity, connection).is_err());
        }
    }

    #[test]
    fn registration_initial_confirmation_and_transitions() {
        for initial in [false, true] {
            let mut state = RegistrationLog::default();
            assert!(state.confirm(initial).is_none());
            state.identity = Some("c200test".into());
            assert_eq!(state.confirmed, None);
            let record = state.confirm(initial).unwrap();
            assert!(record.text.contains(if initial {
                "[registered]"
            } else {
                "[unregistered]"
            }));
            assert_eq!(record.text.contains("Administrator must"), !initial);
            assert!(state.confirm(initial).is_none());
            let changed = state.confirm(!initial).unwrap();
            assert!(changed.text.contains(if initial {
                "[unregistered]"
            } else {
                "[registered]"
            }));
            assert!(state.confirm(!initial).is_none());
            assert!(state.confirm(initial).is_some());
        }
    }
    #[test]
    fn diagnostics_preserve_bounded_failure_and_unknown_registration() {
        let mut state = RegistrationLog {
            identity: Some("public-identity".into()),
            ..Default::default()
        };
        state.failure("Gateway subscription failed");
        let generation = state.generation;
        state.confirm(true);
        assert!(state.generation > generation);
        assert_eq!(state.last_failure, Some("Gateway subscription failed"));
        let gateway = Gateway {
            connection: None,
            connected: Arc::new(AtomicBool::new(false)),
            subscribed: Arc::new(AtomicBool::new(false)),
            diagnostics: Arc::new(Mutex::new(state)),
            inventory_supported: Arc::new(AtomicBool::new(false)),
            inventory_checked: Arc::new(AtomicBool::new(true)),
            world_players_supported: Arc::new(AtomicBool::new(false)),
            world_actions_supported: Arc::new(AtomicBool::new(false)),
            modes_supported: Arc::new(AtomicBool::new(false)),
            player_state_supported: Arc::new(AtomicBool::new(false)),
            respawn_supported: Arc::new(AtomicBool::new(false)),
            chat_supported: Arc::new(AtomicBool::new(false)),
            pose_updates: mpsc::sync_channel(1).0,
        };
        let snapshot = gateway.diagnostics("https://example.com", "mines", Default::default());
        assert_eq!(snapshot.registered, None);
        assert_eq!(snapshot.health, "disconnected");
        assert_eq!(snapshot.identity.as_deref(), Some("public-identity"));
        assert_eq!(snapshot.owner_connection, None);
        assert_eq!(snapshot.last_failure, Some("Gateway subscription failed"));
    }

    #[test]
    fn uuid_conversion_preserves_wire_identity() {
        let id = uuid::Uuid::parse_str("b50ad385-829d-3141-a216-7e7d7539ba7f").unwrap();
        assert_eq!(
            db_uuid(id),
            Uuid::from_u128(0xb50ad385829d3141a2167e7d7539ba7f)
        );
    }
    #[test]
    fn attempt_cleans_unconfirmed_calls_and_unwinding() {
        struct Recorder(std::sync::Mutex<Vec<uuid::Uuid>>);
        impl SessionStore for Recorder {
            fn begin(&self, _: uuid::Uuid, _: &str) -> io::Result<()> {
                Ok(())
            }
            fn configure(&self, _: uuid::Uuid) -> io::Result<i32> {
                Ok(1)
            }
            fn end(&self, session: uuid::Uuid) {
                self.0.lock().unwrap().push(session);
            }
        }
        let recorder = Recorder(std::sync::Mutex::new(Vec::new()));
        let id = uuid::Uuid::new_v4();
        drop(SessionAttempt {
            gateway: &recorder,
            session: id,
            confirmed: false,
        });
        assert_eq!(*recorder.0.lock().unwrap(), vec![id]);
        drop(SessionAttempt {
            gateway: &recorder,
            session: id,
            confirmed: true,
        });
        assert_eq!(recorder.0.lock().unwrap().len(), 1);
        let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            let _guard = SessionAttempt {
                gateway: &recorder,
                session: id,
                confirmed: false,
            };
            panic!("network worker failed");
        }));
        assert!(result.is_err());
        assert_eq!(*recorder.0.lock().unwrap(), vec![id, id]);
    }

    #[test]
    fn reducer_failure_never_counts_as_confirmation() {
        let gateway = Gateway {
            connection: None,
            connected: Arc::new(AtomicBool::new(false)),
            subscribed: Arc::new(AtomicBool::new(false)),
            diagnostics: Arc::new(Mutex::new(RegistrationLog::default())),
            inventory_supported: Arc::new(AtomicBool::new(false)),
            inventory_checked: Arc::new(AtomicBool::new(true)),
            world_players_supported: Arc::new(AtomicBool::new(false)),
            world_actions_supported: Arc::new(AtomicBool::new(false)),
            modes_supported: Arc::new(AtomicBool::new(false)),
            player_state_supported: Arc::new(AtomicBool::new(false)),
            respawn_supported: Arc::new(AtomicBool::new(false)),
            chat_supported: Arc::new(AtomicBool::new(false)),
            pose_updates: mpsc::sync_channel(1).0,
        };
        // An old deployment must avoid inventory reducers entirely. With no
        // connection, a regression past the capability guard would fail here.
        let session = uuid::Uuid::new_v4();
        assert!(gateway.inventory_snapshot(session).unwrap().is_none());
        assert_eq!(
            gateway
                .inventory_submit(session, 1, vec![0])
                .unwrap_err()
                .kind(),
            io::ErrorKind::Unsupported
        );
        let (sender, receiver) = mpsc::channel();
        sender.send(Err(io::Error::other("Unauthorized"))).unwrap();
        let error = gateway
            .wait_for(receiver, Instant::now() + Duration::from_millis(20), || {
                true
            })
            .unwrap_err();
        assert_eq!(error.to_string(), "Unauthorized");
        let (_sender, receiver) = mpsc::channel();
        assert_eq!(
            gateway
                .wait_for(receiver, Instant::now(), || true)
                .unwrap_err()
                .kind(),
            io::ErrorKind::TimedOut
        );
    }
}
