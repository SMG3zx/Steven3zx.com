//! Opt-in verification against a separately published, disposable test database.
//! Never publishes, drops a database, or reads/writes the gateway's saved token.
use crate::module_bindings::*;
use spacetimedb_sdk::{DbContext, Identity, Table, Uuid};
use std::{
    sync::{mpsc, Arc},
    thread,
    time::{Duration, Instant},
};

const TIMEOUT: Duration = Duration::from_secs(5);

struct Client {
    connection: Arc<DbConnection>,
    identity: Identity,
    token: String,
}

impl Client {
    fn connect(host: &str, database: &str, token: Option<String>) -> Self {
        let (sender, receiver) = mpsc::channel();
        let error_sender = sender.clone();
        let connection = Arc::new(
            DbConnection::builder()
                .with_uri(host)
                .with_database_name(database)
                .with_token(token)
                .on_connect(move |_, identity, token| {
                    let _ = sender.send(Ok((identity, token.to_owned())));
                })
                .on_connect_error(move |_, _| {
                    let _ = error_sender.send(Err("Test database connection failed"));
                })
                .build()
                .expect("Cannot initialize test database connection"),
        );
        connection.run_threaded();
        let (identity, token) = receiver
            .recv_timeout(TIMEOUT)
            .expect("Connect timed out")
            .expect("Connect failed");
        let (sender, receiver) = mpsc::channel();
        let error_sender = sender.clone();
        connection
            .subscription_builder()
            .on_applied(move |_| {
                let _ = sender.send(true);
            })
            .on_error(move |_, _| {
                let _ = error_sender.send(false);
            })
            .subscribe([
                "SELECT * FROM my_gateway",
                "SELECT * FROM gateway_sessions",
                "SELECT * FROM gateway_profiles",
                "SELECT * FROM admin_session_diagnostics",
                "SELECT * FROM admin_lease_diagnostic",
                "SELECT * FROM gateway_inventory_snapshots",
                "SELECT * FROM gateway_world_players",
                "SELECT * FROM gateway_chat_messages",
            ]);
        assert!(
            receiver.recv_timeout(TIMEOUT).expect("Subscribe timed out"),
            "Publish the matching foundation module first"
        );
        Self {
            connection,
            identity,
            token,
        }
    }
}

impl Drop for Client {
    fn drop(&mut self) {
        let _ = self.connection.disconnect();
    }
}

macro_rules! call {
    ($conn:expr, $method:ident($($arg:expr),* $(,)?)) => {{
        let (sender, receiver) = mpsc::channel();
        $conn.reducers.$method($($arg,)* move |_, result| {
            let _ = sender.send(result.map_err(|_| "Internal reducer error".to_owned()).and_then(|value| value));
        }).expect("Cannot send test reducer");
        receiver.recv_timeout(TIMEOUT).expect("Test reducer timed out")
    }};
}

fn until(confirmed: impl Fn() -> bool) {
    let deadline = Instant::now() + TIMEOUT;
    while !confirmed() {
        assert!(
            Instant::now() < deadline,
            "Subscription confirmation timed out"
        );
        thread::sleep(Duration::from_millis(10));
    }
}

struct AuthorizationGuard {
    admin: Arc<DbConnection>,
    identities: Vec<Identity>,
}

impl Drop for AuthorizationGuard {
    fn drop(&mut self) {
        for identity in &self.identities {
            let (sender, receiver) = mpsc::channel();
            if self
                .admin
                .reducers
                .revoke_gateway_then(*identity, move |_, _| {
                    let _ = sender.send(());
                })
                .is_ok()
            {
                let _ = receiver.recv_timeout(TIMEOUT);
            }
        }
    }
}

#[test]
#[ignore = "Requires explicit disposable database and administrator token; see README"]
fn live_gateway_authorization_visibility_ownership_and_cleanup() {
    let host =
        std::env::var("SPACETIMEDB_TEST_HOST").expect("Set SPACETIMEDB_TEST_HOST explicitly");
    let database = std::env::var("SPACETIMEDB_TEST_DATABASE")
        .expect("Set SPACETIMEDB_TEST_DATABASE explicitly");
    assert!(
        database.starts_with("rusty-mines-test-"),
        "Use a disposable rusty-mines-test-* database, never the production database"
    );
    let admin_token = std::env::var("SPACETIMEDB_TEST_ADMIN_TOKEN")
        .expect("Set the test database administrator token securely");
    let admin = Client::connect(&host, &database, Some(admin_token));
    let a = Client::connect(&host, &database, None);
    let b = Client::connect(&host, &database, None);
    let _authorization = AuthorizationGuard {
        admin: admin.connection.clone(),
        identities: vec![a.identity, b.identity],
    };
    let id = Uuid::from_u128(uuid::Uuid::new_v4().as_u128());
    let username = format!("T{}", &uuid::Uuid::new_v4().simple().to_string()[..12]);

    assert!(call!(a.connection, register_gateway_then(a.identity)).is_err());
    assert!(call!(
        a.connection,
        begin_offline_session_then(id, username.clone())
    )
    .is_err());
    assert_eq!(a.connection.db.gateway_sessions().count(), 0);
    assert_eq!(a.connection.db.gateway_profiles().count(), 0);

    // Private authoritative tables cannot be subscribed by an unprivileged caller.
    let (sender, receiver) = mpsc::channel();
    let applied = sender.clone();
    a.connection
        .subscription_builder()
        .on_error(move |_, _| {
            let _ = sender.send(true);
        })
        .on_applied(move |_| {
            let _ = applied.send(false);
        })
        .subscribe("SELECT * FROM player_chat_message");
    assert!(receiver
        .recv_timeout(TIMEOUT)
        .expect("Private table request timed out"));

    call!(admin.connection, register_gateway_then(a.identity)).unwrap();
    call!(admin.connection, register_gateway_then(b.identity)).unwrap();
    until(|| {
        a.connection.db.my_gateway().count() == 1 && b.connection.db.my_gateway().count() == 1
    });
    call!(
        a.connection,
        begin_offline_session_then(id, username.clone())
    )
    .unwrap();
    assert!(call!(a.connection, send_chat_then(id, "too early".into())).is_err());
    until(|| {
        a.connection.db.gateway_sessions().count() == 1
            && a.connection.db.gateway_profiles().count() == 1
    });
    assert_eq!(b.connection.db.gateway_sessions().count(), 0);
    assert_eq!(b.connection.db.gateway_profiles().count(), 0);
    let first = a
        .connection
        .db
        .gateway_sessions()
        .iter()
        .find(|row| row.session_uuid == id)
        .unwrap();
    assert!(first.entity_id > 0);
    call!(
        a.connection,
        begin_offline_session_then(id, username.clone())
    )
    .unwrap();
    assert_eq!(a.connection.db.gateway_sessions().count(), 1);
    let retried = a
        .connection
        .db
        .gateway_sessions()
        .iter()
        .find(|row| row.session_uuid == id)
        .unwrap();
    assert_eq!(retried.entity_id, first.entity_id);
    assert_eq!(retried.connected_at, first.connected_at);
    assert!(call!(
        a.connection,
        begin_offline_session_then(id, "Conflicting".to_owned())
    )
    .is_err());
    assert_eq!(a.connection.db.admin_session_diagnostics().count(), 0);
    assert_eq!(b.connection.db.admin_session_diagnostics().count(), 0);
    assert_eq!(a.connection.db.admin_lease_diagnostic().count(), 0);
    assert_eq!(b.connection.db.admin_lease_diagnostic().count(), 0);
    assert!(call!(
        b.connection,
        begin_offline_session_then(
            Uuid::from_u128(uuid::Uuid::new_v4().as_u128()),
            username.clone()
        )
    )
    .is_err());
    assert!(call!(b.connection, end_session_then(id)).is_err());
    assert!(call!(b.connection, advance_login_session_then(id)).is_err());

    // Same identity is still not enough to mutate another connection's session.
    let a2 = Client::connect(&host, &database, Some(a.token.clone()));
    assert_eq!(a2.identity, a.identity);
    assert_ne!(
        a2.connection.connection_id(),
        a.connection.connection_id(),
        "Ownership test requires distinct server-assigned connection IDs"
    );
    assert!(call!(a2.connection, end_session_then(id)).is_err());
    assert!(call!(a2.connection, advance_login_session_then(id)).is_err());
    assert!(call!(a2.connection, enter_play_session_then(id)).is_err());
    assert!(call!(a2.connection, initialize_player_inventory_then(id)).is_err());
    assert!(call!(
        a2.connection,
        begin_offline_session_then(id, username.clone())
    )
    .is_err());
    assert!(call!(a2.connection, heartbeat_sessions_then(vec![id])).is_err());
    assert!(call!(b.connection, heartbeat_sessions_then(vec![id])).is_err());
    assert!(call!(a.connection, heartbeat_sessions_then(Vec::new())).is_err());
    assert!(call!(a.connection, heartbeat_sessions_then(vec![id; 65])).is_err());
    let before = a
        .connection
        .db
        .gateway_sessions()
        .iter()
        .find(|row| row.session_uuid == id)
        .unwrap()
        .last_activity;
    assert!(call!(
        a.connection,
        heartbeat_sessions_then(vec![id, Uuid::from_u128(uuid::Uuid::new_v4().as_u128())])
    )
    .is_err());
    assert_eq!(
        a.connection
            .db
            .gateway_sessions()
            .iter()
            .find(|row| row.session_uuid == id)
            .unwrap()
            .last_activity,
        before
    );
    call!(a.connection, heartbeat_sessions_then(vec![id])).unwrap();
    until(|| {
        a.connection
            .db
            .gateway_sessions()
            .iter()
            .any(|row| row.session_uuid == id && row.last_activity > before)
    });
    let id2 = Uuid::from_u128(uuid::Uuid::new_v4().as_u128());
    let username2 = format!("T{}", &uuid::Uuid::new_v4().simple().to_string()[..12]);
    call!(a2.connection, begin_offline_session_then(id2, username2)).unwrap();
    until(|| a.connection.db.gateway_sessions().count() == 2);
    drop(a2);
    until(|| a.connection.db.gateway_sessions().count() == 1);
    assert_eq!(
        a.connection
            .db
            .gateway_sessions()
            .iter()
            .next()
            .unwrap()
            .session_uuid,
        id
    );

    call!(a.connection, advance_login_session_then(id)).unwrap();
    until(|| {
        a.connection
            .db
            .gateway_sessions()
            .iter()
            .any(|session| session.phase == SessionPhase::Configuration)
    });
    call!(a.connection, advance_login_session_then(id)).unwrap();
    assert_eq!(
        a.connection
            .db
            .gateway_sessions()
            .iter()
            .filter(|session| session.phase == SessionPhase::Play)
            .count(),
        0
    );
    call!(a.connection, enter_play_session_then(id)).unwrap();
    until(|| {
        a.connection
            .db
            .gateway_sessions()
            .iter()
            .any(|row| row.session_uuid == id && row.phase == SessionPhase::Play)
    });
    let same_identity = Client::connect(&host, &database, Some(a.token.clone()));
    assert_eq!(same_identity.identity, a.identity);
    until(|| {
        a.connection.db.gateway_world_players().count() == 1
            && same_identity.connection.db.gateway_world_players().count() == 1
    });
    assert_eq!(b.connection.db.gateway_world_players().count(), 0);
    assert!(call!(a.connection, send_chat_then(id, "\nnot allowed".into())).is_err());
    assert!(call!(a.connection, send_chat_then(id, "😀".repeat(129))).is_err());
    for index in 0..5 {
        call!(
            a.connection,
            send_chat_then(id, format!("unsigned test {index}"))
        )
        .unwrap();
    }
    assert!(call!(
        a.connection,
        send_chat_then(id, "sixth message is rate limited".into())
    )
    .is_err());
    until(|| a.connection.db.gateway_chat_messages().count() == 5);
    until(|| same_identity.connection.db.gateway_chat_messages().count() == 5);
    assert_eq!(b.connection.db.gateway_chat_messages().count(), 0);
    let mut chat_rows: Vec<_> = a.connection.db.gateway_chat_messages().iter().collect();
    chat_rows.sort_by_key(|row| row.gateway_sequence);
    assert_eq!(chat_rows.len(), 5);
    assert!(chat_rows.iter().all(|row| {
        row.session_uuid == id
            && row.username == username
            && row.text.starts_with("unsigned test ")
            && !row.verified
    }));
    assert!(chat_rows
        .windows(2)
        .all(|pair| pair[0].gateway_sequence < pair[1].gateway_sequence));
    call!(
        admin.connection,
        set_gateway_chat_mode_then(a.identity, GatewayChatMode::Hidden)
    )
    .unwrap();
    until(|| {
        a.connection.db.gateway_chat_messages().count() == 0
            && same_identity.connection.db.gateway_chat_messages().count() == 0
    });
    assert!(call!(a.connection, send_chat_then(id, "hidden".into())).is_err());
    call!(
        admin.connection,
        set_gateway_chat_mode_then(a.identity, GatewayChatMode::Open)
    )
    .unwrap();
    assert_eq!(a.connection.db.gateway_chat_messages().count(), 0);
    assert_eq!(
        a.connection
            .db
            .gateway_world_players()
            .iter()
            .next()
            .unwrap()
            .session_uuid,
        id
    );
    assert!(
        call!(
            same_identity.connection,
            update_player_states_then(vec![PlayerPoseUpdate {
                session_uuid: id,
                x: 9.0,
                y: 66.0,
                z: 10.0,
                yaw: 90.0,
                pitch: -45.0,
                on_ground: true,
            }])
        )
        .is_err(),
        "same gateway identity on another connection must not mutate the owned session"
    );
    assert!(
        call!(
            b.connection,
            update_player_states_then(vec![PlayerPoseUpdate {
                session_uuid: id,
                x: 9.0,
                y: 66.0,
                z: 10.0,
                yaw: 90.0,
                pitch: -45.0,
                on_ground: true,
            }])
        )
        .is_err(),
        "another gateway identity must not mutate the pose"
    );
    call!(
        a.connection,
        update_player_states_then(vec![PlayerPoseUpdate {
            session_uuid: id,
            x: 9.0,
            y: 65.0,
            z: 10.0,
            yaw: 90.0,
            pitch: -45.0,
            on_ground: true,
        }])
    )
    .unwrap();
    until(|| {
        a.connection.db.gateway_world_players().iter().any(|row| {
            row.session_uuid == id && row.x == 9.0 && row.yaw == 90.0 && row.revision == 2
        }) && same_identity
            .connection
            .db
            .gateway_world_players()
            .iter()
            .any(|row| {
                row.session_uuid == id && row.x == 9.0 && row.yaw == 90.0 && row.revision == 2
            })
    });
    drop(same_identity);
    until(|| a.connection.db.gateway_sessions().count() == 1);
    call!(a.connection, enter_play_session_then(id)).unwrap();
    assert!(call!(a.connection, advance_login_session_then(id)).is_err());
    assert_eq!(
        a.connection
            .db
            .gateway_sessions()
            .iter()
            .find(|row| row.session_uuid == id)
            .unwrap()
            .entity_id,
        first.entity_id
    );

    // Inventory operations are authorized by the gateway identity plus the
    // exact connection that owns the live session. Subscribed snapshots, not
    // reducer return values, are the observable result.
    call!(a.connection, initialize_player_inventory_then(id)).unwrap();
    until(|| {
        a.connection
            .db
            .gateway_inventory_snapshots()
            .iter()
            .any(|row| row.session_uuid == id)
    });
    assert_eq!(b.connection.db.gateway_inventory_snapshots().count(), 0);
    let player_uuid = a
        .connection
        .db
        .gateway_sessions()
        .iter()
        .find(|row| row.session_uuid == id)
        .unwrap()
        .player_uuid;
    let mut creative_request = Vec::new();
    rusty_mines_inventory::wire::put_int(&mut creative_request, 1); // Creative slot action
    rusty_mines_inventory::wire::put_int(&mut creative_request, 0); // Initial revision
    rusty_mines_inventory::wire::put_int(&mut creative_request, 36);
    creative_request.extend_from_slice(&[1, 1, 0, 0]); // One stone, no component patch
    assert!(call!(
        a.connection,
        apply_inventory_request_then(id, 1, creative_request.clone())
    )
    .is_err());
    assert!(call!(
        b.connection,
        apply_inventory_request_then(id, 1, creative_request.clone())
    )
    .is_err());

    call!(
        admin.connection,
        grant_inventory_permission_then(player_uuid, true)
    )
    .unwrap();
    let mut creative_request = Vec::new();
    rusty_mines_inventory::wire::put_int(&mut creative_request, 1);
    rusty_mines_inventory::wire::put_int(&mut creative_request, 1); // Permission grant advanced revision
    rusty_mines_inventory::wire::put_int(&mut creative_request, 36);
    creative_request.extend_from_slice(&[1, 1, 0, 0]);
    call!(
        a.connection,
        apply_inventory_request_then(id, 1, creative_request.clone())
    )
    .unwrap();
    call!(
        a.connection,
        apply_inventory_request_then(id, 1, creative_request.clone())
    )
    .unwrap();
    until(|| {
        a.connection
            .db
            .gateway_inventory_snapshots()
            .iter()
            .any(|row| row.session_uuid == id && row.sequence == 1 && row.revision == 2)
    });
    let snapshot = a
        .connection
        .db
        .gateway_inventory_snapshots()
        .iter()
        .find(|row| row.session_uuid == id)
        .unwrap();
    let slots = rusty_mines_inventory::model::decode_slots(&snapshot.slots, 46).unwrap();
    assert_eq!(slots[36].item, 1);
    assert_eq!(slots[36].count, 1);
    let mut conflicting_replay = creative_request.clone();
    *conflicting_replay.get_mut(2).unwrap() = 35;
    assert!(call!(
        a.connection,
        apply_inventory_request_then(id, 1, conflicting_replay)
    )
    .is_err());
    let mut stale_request = Vec::new();
    rusty_mines_inventory::wire::put_int(&mut stale_request, 0);
    rusty_mines_inventory::wire::put_int(&mut stale_request, 1);
    rusty_mines_inventory::wire::put_int(&mut stale_request, 3);
    assert!(call!(
        a.connection,
        apply_inventory_request_then(id, 2, stale_request)
    )
    .is_err());

    let storage_uuid = Uuid::from_u128(uuid::Uuid::new_v4().as_u128());
    let mut open_storage = Vec::new();
    rusty_mines_inventory::wire::put_int(&mut open_storage, 3);
    rusty_mines_inventory::wire::put_int(&mut open_storage, 2);
    open_storage.extend_from_slice(&storage_uuid.as_u128().to_be_bytes());
    assert!(call!(
        a.connection,
        apply_inventory_request_then(id, 2, open_storage.clone())
    )
    .is_err());
    call!(
        admin.connection,
        define_simple_storage_then(storage_uuid, "Live Test Storage".into())
    )
    .unwrap();
    call!(
        admin.connection,
        grant_storage_access_then(player_uuid, storage_uuid, true)
    )
    .unwrap();
    call!(
        a.connection,
        apply_inventory_request_then(id, 2, open_storage)
    )
    .unwrap();
    until(|| {
        a.connection
            .db
            .gateway_inventory_snapshots()
            .iter()
            .any(|row| {
                row.session_uuid == id && row.menu_id > 0 && row.menu_title == "Live Test Storage"
            })
    });

    // A second gateway has an independently authorized viewer. Committing a
    // storage click must update both subscription snapshots atomically.
    let id_b = Uuid::from_u128(uuid::Uuid::new_v4().as_u128());
    let username_b = format!("T{}", &uuid::Uuid::new_v4().simple().to_string()[..12]);
    call!(b.connection, begin_offline_session_then(id_b, username_b)).unwrap();
    until(|| b.connection.db.gateway_sessions().count() == 1);
    call!(b.connection, advance_login_session_then(id_b)).unwrap();
    call!(b.connection, enter_play_session_then(id_b)).unwrap();
    call!(b.connection, initialize_player_inventory_then(id_b)).unwrap();
    until(|| {
        b.connection
            .db
            .gateway_inventory_snapshots()
            .iter()
            .any(|row| row.session_uuid == id_b)
    });
    let player_uuid_b = b
        .connection
        .db
        .gateway_sessions()
        .iter()
        .find(|row| row.session_uuid == id_b)
        .unwrap()
        .player_uuid;
    call!(
        admin.connection,
        grant_storage_access_then(player_uuid_b, storage_uuid, true)
    )
    .unwrap();
    let mut open_storage_b = Vec::new();
    rusty_mines_inventory::wire::put_int(&mut open_storage_b, 3);
    rusty_mines_inventory::wire::put_int(&mut open_storage_b, 0);
    open_storage_b.extend_from_slice(&storage_uuid.as_u128().to_be_bytes());
    call!(
        b.connection,
        apply_inventory_request_then(id_b, 1, open_storage_b)
    )
    .unwrap();
    until(|| {
        b.connection
            .db
            .gateway_inventory_snapshots()
            .iter()
            .any(|row| row.session_uuid == id_b && row.menu_id > 0)
    });

    let mut shift_click = Vec::new();
    for value in [2, 3, 1, 54, 0, 1] {
        rusty_mines_inventory::wire::put_int(&mut shift_click, value);
    }
    call!(
        a.connection,
        apply_inventory_request_then(id, 3, shift_click)
    )
    .unwrap();
    until(|| {
        a.connection
            .db
            .gateway_inventory_snapshots()
            .iter()
            .any(|row| row.session_uuid == id && row.sequence == 3 && row.revision == 4)
            && b.connection
                .db
                .gateway_inventory_snapshots()
                .iter()
                .any(|row| row.session_uuid == id_b && row.revision > 1)
    });
    let shared = b
        .connection
        .db
        .gateway_inventory_snapshots()
        .iter()
        .find(|row| row.session_uuid == id_b)
        .unwrap();
    let storage_slots = rusty_mines_inventory::model::decode_slots(&shared.slots, 63).unwrap();
    assert_eq!(storage_slots[0].item, 1);
    assert_eq!(storage_slots[0].count, 1);

    call!(
        admin.connection,
        grant_storage_access_then(player_uuid, storage_uuid, false)
    )
    .unwrap();
    until(|| {
        a.connection
            .db
            .gateway_inventory_snapshots()
            .iter()
            .any(|row| row.session_uuid == id && row.menu_id == 0)
    });
    call!(
        admin.connection,
        grant_storage_access_then(player_uuid_b, storage_uuid, false)
    )
    .unwrap();
    until(|| {
        b.connection
            .db
            .gateway_inventory_snapshots()
            .iter()
            .any(|row| row.session_uuid == id_b && row.menu_id == 0)
    });

    call!(a.connection, end_session_then(id)).unwrap();
    call!(a.connection, end_session_then(id)).unwrap();
    call!(b.connection, end_session_then(id_b)).unwrap();
    until(|| {
        a.connection.db.gateway_sessions().count() == 0
            && a.connection.db.gateway_world_players().count() == 0
            && a.connection.db.gateway_profiles().count() == 0
            && b.connection.db.gateway_sessions().count() == 0
            && b.connection.db.gateway_profiles().count() == 0
    });

    // Revocation is atomic cleanup and blocks future mutations.
    call!(
        a.connection,
        begin_offline_session_then(id, username.clone())
    )
    .unwrap();
    until(|| a.connection.db.gateway_sessions().count() == 1);
    call!(admin.connection, revoke_gateway_then(a.identity)).unwrap();
    until(|| {
        a.connection.db.my_gateway().count() == 0 && a.connection.db.gateway_sessions().count() == 0
    });
    assert!(call!(a.connection, begin_offline_session_then(id, username)).is_err());
}

#[test]
#[ignore = "Requires an explicitly published disposable module with M6 world reducers"]
fn live_world_actions_are_owned_sequenced_scoped_and_persistent() {
    let host =
        std::env::var("SPACETIMEDB_TEST_HOST").expect("Set SPACETIMEDB_TEST_HOST explicitly");
    let database = std::env::var("SPACETIMEDB_TEST_DATABASE")
        .expect("Set SPACETIMEDB_TEST_DATABASE explicitly");
    assert!(
        database.starts_with("rusty-mines-test-"),
        "Use a disposable local database"
    );
    let admin_token = std::env::var("SPACETIMEDB_TEST_ADMIN_TOKEN")
        .expect("Set the disposable database administrator token");
    let admin = Client::connect(&host, &database, Some(admin_token));
    let a = Client::connect(&host, &database, None);
    // Reuse only this test's fresh token to model a second connection under
    // the same authorized gateway identity.
    let same_gateway = Client::connect(&host, &database, Some(a.token.clone()));
    let b = Client::connect(&host, &database, None);
    let _authorization = AuthorizationGuard {
        admin: admin.connection.clone(),
        identities: vec![a.identity, b.identity],
    };
    call!(admin.connection, register_gateway_then(a.identity)).unwrap();
    call!(admin.connection, register_gateway_then(b.identity)).unwrap();
    let subscribe_actions = |client: &Client| {
        let (sender, receiver) = mpsc::channel();
        let failed = sender.clone();
        client
            .connection
            .subscription_builder()
            .on_applied(move |_| {
                let _ = sender.send(true);
            })
            .on_error(move |_, _| {
                let _ = failed.send(false);
            })
            .subscribe([
                "SELECT * FROM gateway_block_actions",
                "SELECT * FROM gateway_game_modes",
                "SELECT * FROM gateway_chunk_snapshots",
                "SELECT * FROM gateway_player_state",
            ]);
        assert!(receiver
            .recv_timeout(TIMEOUT)
            .expect("Block action view subscription timed out"));
    };
    subscribe_actions(&a);
    subscribe_actions(&same_gateway);
    subscribe_actions(&b);

    let id = Uuid::from_u128(uuid::Uuid::new_v4().as_u128());
    let username = format!("W{}", &uuid::Uuid::new_v4().simple().to_string()[..10]);
    call!(
        a.connection,
        begin_offline_session_then(id, username.clone())
    )
    .unwrap();
    call!(a.connection, advance_login_session_then(id)).unwrap();
    call!(a.connection, enter_play_session_then(id)).unwrap();
    call!(a.connection, initialize_player_inventory_then(id)).unwrap();
    until(|| {
        a.connection
            .db
            .gateway_player_state()
            .iter()
            .any(|row| row.session_uuid == id)
    });
    assert!(call!(
        a.connection,
        update_player_vitals_then(id, 14, 12, false, 1)
    )
    .is_ok());
    until(|| {
        a.connection.db.gateway_player_state().iter().any(|row| {
            row.session_uuid == id
                && row.health == 14
                && row.food == 12
                && !row.dead
                && row.revision == 2
        })
    });
    assert!(call!(a.connection, update_player_vitals_then(id, 0, 12, true, 2)).is_ok());
    until(|| {
        a.connection
            .db
            .gateway_player_state()
            .iter()
            .any(|row| row.session_uuid == id && row.health == 0 && row.dead && row.revision == 3)
    });
    assert!(call!(a.connection, request_player_respawn_then(id)).is_ok());
    until(|| {
        a.connection.db.gateway_player_state().iter().any(|row| {
            row.session_uuid == id
                && row.health == 20
                && row.food == 12
                && !row.dead
                && row.revision == 4
                && row.respawn_revision == Some(4)
        })
    });
    call!(
        a.connection,
        update_player_states_then(vec![PlayerPoseUpdate {
            session_uuid: id,
            x: 8.5,
            y: 65.0,
            z: 8.5,
            yaw: 0.0,
            pitch: 0.0,
            on_ground: true,
        }])
    )
    .unwrap();
    call!(a.connection, update_world_interest_then(id, 0, 0, 0)).unwrap();
    let peer_id = Uuid::from_u128(uuid::Uuid::new_v4().as_u128());
    let peer_username = format!("W{}", &uuid::Uuid::new_v4().simple().to_string()[..10]);
    call!(
        same_gateway.connection,
        begin_offline_session_then(peer_id, peer_username)
    )
    .unwrap();
    call!(same_gateway.connection, advance_login_session_then(peer_id)).unwrap();
    call!(same_gateway.connection, enter_play_session_then(peer_id)).unwrap();
    call!(
        same_gateway.connection,
        initialize_player_inventory_then(peer_id)
    )
    .unwrap();
    call!(
        same_gateway.connection,
        update_player_states_then(vec![PlayerPoseUpdate {
            session_uuid: peer_id,
            x: 8.5,
            y: 65.0,
            z: 8.5,
            yaw: 0.0,
            pitch: 0.0,
            on_ground: true,
        }])
    )
    .unwrap();
    call!(
        same_gateway.connection,
        update_world_interest_then(peer_id, 0, 0, 0)
    )
    .unwrap();
    let snapshot_contains = |client: &Client, state: u32| {
        client
            .connection
            .db
            .gateway_chunk_snapshots()
            .iter()
            .any(|row| {
                row.chunk_x == 0
                    && row.chunk_z == 0
                    && row
                        .encoded_overrides
                        .as_chunks::<16>()
                        .0
                        .iter()
                        .any(|entry| {
                            let x = i32::from_le_bytes(entry[0..4].try_into().unwrap());
                            let y = i32::from_le_bytes(entry[4..8].try_into().unwrap());
                            let z = i32::from_le_bytes(entry[8..12].try_into().unwrap());
                            let block = u32::from_le_bytes(entry[12..16].try_into().unwrap());
                            (x, y, z, block) == (8, 63, 8, state)
                        })
            })
    };
    call!(a.connection, change_game_mode_then(id, GameMode::Adventure)).unwrap();
    until(|| {
        a.connection
            .db
            .gateway_game_modes()
            .iter()
            .any(|row| row.session_uuid == id && row.mode == GameMode::Adventure)
    });
    assert!(call!(
        a.connection,
        apply_block_action_then(id, 1, 8, 63, 8, 1, false)
    )
    .is_err());
    assert!(call!(a.connection, change_game_mode_then(id, GameMode::Creative)).is_err());
    let player_uuid = Uuid::from_u128(
        crate::packets::login::offline_uuid(&username)
            .unwrap()
            .as_u128(),
    );
    assert!(call!(
        a.connection,
        grant_player_game_mode_then(player_uuid, GameMode::Creative)
    )
    .is_err());
    call!(a.connection, change_game_mode_then(id, GameMode::Survival)).unwrap();
    until(|| {
        a.connection
            .db
            .gateway_game_modes()
            .iter()
            .any(|row| row.session_uuid == id && row.mode == GameMode::Survival)
    });
    call!(
        a.connection,
        apply_block_action_then(id, 1, 8, 63, 8, 1, false)
    )
    .unwrap();
    until(|| {
        a.connection
            .db
            .gateway_block_actions()
            .iter()
            .any(|row| row.session_uuid == id && row.sequence == 1 && row.resulting_state == 0)
    });
    until(|| snapshot_contains(&same_gateway, 0));
    let committed_break = a
        .connection
        .db
        .gateway_block_actions()
        .iter()
        .find(|row| row.session_uuid == id && row.sequence == 1)
        .expect("Committed break snapshot");
    let inventory_after_break = a
        .connection
        .db
        .gateway_inventory_snapshots()
        .iter()
        .find(|row| row.session_uuid == id)
        .expect("Inventory snapshot after supported Survival drop");
    // Replaying the exact committed request is idempotent and cannot award a
    // second drop or advance the inventory/block revisions.
    call!(
        a.connection,
        apply_block_action_then(id, 1, 8, 63, 8, 1, false)
    )
    .unwrap();
    assert_eq!(
        a.connection
            .db
            .gateway_block_actions()
            .iter()
            .find(|row| row.session_uuid == id && row.sequence == 1),
        Some(committed_break.clone())
    );
    assert_eq!(
        a.connection
            .db
            .gateway_inventory_snapshots()
            .iter()
            .find(|row| row.session_uuid == id),
        Some(inventory_after_break.clone())
    );
    assert!(call!(
        a.connection,
        apply_block_action_then(id, 1, 8, 63, 8, 0, true)
    )
    .is_err());
    assert_eq!(
        a.connection
            .db
            .gateway_block_actions()
            .iter()
            .find(|row| row.session_uuid == id && row.sequence == 1),
        Some(committed_break)
    );
    assert_eq!(
        a.connection
            .db
            .gateway_inventory_snapshots()
            .iter()
            .find(|row| row.session_uuid == id),
        Some(inventory_after_break)
    );
    call!(
        admin.connection,
        grant_player_game_mode_then(player_uuid, GameMode::Creative)
    )
    .unwrap();
    until(|| {
        a.connection
            .db
            .gateway_game_modes()
            .iter()
            .any(|row| row.session_uuid == id && row.mode == GameMode::Creative)
    });
    until(|| {
        a.connection.db.gateway_player_state().iter().any(|row| {
            row.session_uuid == id
                && row.mode == GameMode::Creative
                && row.state_owner == Some(a.identity)
        })
    });
    call!(
        a.connection,
        apply_block_action_then(id, 2, 8, 63, 8, 0, true)
    )
    .unwrap();
    until(|| {
        a.connection
            .db
            .gateway_block_actions()
            .iter()
            .any(|row| row.session_uuid == id && row.sequence == 2 && row.resulting_state == 1)
    });
    until(|| snapshot_contains(&same_gateway, 1));
    call!(
        admin.connection,
        grant_player_game_mode_then(player_uuid, GameMode::Spectator)
    )
    .unwrap();
    until(|| {
        a.connection
            .db
            .gateway_game_modes()
            .iter()
            .any(|row| row.session_uuid == id && row.mode == GameMode::Spectator)
    });
    assert!(call!(
        a.connection,
        apply_block_action_then(id, 3, 8, 63, 8, 1, false)
    )
    .is_err());
    call!(
        admin.connection,
        grant_player_game_mode_then(player_uuid, GameMode::Creative)
    )
    .unwrap();
    until(|| {
        a.connection
            .db
            .gateway_game_modes()
            .iter()
            .any(|row| row.session_uuid == id && row.mode == GameMode::Creative)
    });
    call!(
        a.connection,
        apply_block_action_then(id, 3, 8, 63, 8, 1, false)
    )
    .unwrap();
    until(|| {
        a.connection
            .db
            .gateway_block_actions()
            .iter()
            .any(|row| row.session_uuid == id && row.sequence == 3 && row.resulting_state == 0)
    });
    until(|| snapshot_contains(&same_gateway, 0));
    call!(
        admin.connection,
        grant_player_game_mode_then(player_uuid, GameMode::Survival)
    )
    .unwrap();
    until(|| {
        a.connection
            .db
            .gateway_game_modes()
            .iter()
            .any(|row| row.session_uuid == id && row.mode == GameMode::Survival)
    });
    assert_eq!(b.connection.db.gateway_block_actions().count(), 0);
    assert!(call!(
        b.connection,
        apply_block_action_then(id, 3, 8, 63, 8, 0, true)
    )
    .is_err());

    // Race two authorized sessions against the same untouched block. Exactly
    // one reducer may commit the drop and override; the loser sees stale state.
    let inventory_revisions_before: i32 = [(&a, id), (&same_gateway, peer_id)]
        .into_iter()
        .map(|(client, session_id)| {
            client
                .connection
                .db
                .gateway_inventory_snapshots()
                .iter()
                .find(|row| row.session_uuid == session_id)
                .unwrap()
                .revision
        })
        .sum();
    let barrier = Arc::new(std::sync::Barrier::new(3));
    let first_connection = a.connection.clone();
    let first_barrier = barrier.clone();
    let first = thread::spawn(move || {
        first_barrier.wait();
        call!(
            first_connection,
            apply_block_action_then(id, 4, 9, 63, 8, 1, false)
        )
    });
    let second_connection = same_gateway.connection.clone();
    let second_barrier = barrier.clone();
    let second = thread::spawn(move || {
        second_barrier.wait();
        call!(
            second_connection,
            apply_block_action_then(peer_id, 1, 9, 63, 8, 1, false)
        )
    });
    barrier.wait();
    let first_result = first.join().expect("First concurrent edit panicked");
    let second_result = second.join().expect("Second concurrent edit panicked");
    assert_ne!(first_result.is_ok(), second_result.is_ok());
    until(|| {
        same_gateway
            .connection
            .db
            .gateway_chunk_snapshots()
            .iter()
            .any(|row| {
                row.chunk_x == 0
                    && row.chunk_z == 0
                    && row
                        .encoded_overrides
                        .as_chunks::<16>()
                        .0
                        .iter()
                        .any(|entry| {
                            i32::from_le_bytes(entry[0..4].try_into().unwrap()) == 9
                                && i32::from_le_bytes(entry[4..8].try_into().unwrap()) == 63
                                && i32::from_le_bytes(entry[8..12].try_into().unwrap()) == 8
                                && u32::from_le_bytes(entry[12..16].try_into().unwrap()) == 0
                        })
            })
    });
    assert!(call!(a.connection, update_player_vitals_then(id, 13, 7, false, 4)).is_ok());
    until(|| {
        a.connection.db.gateway_player_state().iter().any(|row| {
            row.session_uuid == id
                && row.health == 13
                && row.food == 7
                && !row.dead
                && row.revision == 5
        })
    });
    let inventory_revisions_after: i32 = [(&a, id), (&same_gateway, peer_id)]
        .into_iter()
        .map(|(client, session_id)| {
            client
                .connection
                .db
                .gateway_inventory_snapshots()
                .iter()
                .find(|row| row.session_uuid == session_id)
                .unwrap()
                .revision
        })
        .sum();
    assert_eq!(inventory_revisions_after, inventory_revisions_before + 1);

    // Fill the chunk override cap across fresh Creative sessions, then force a
    // Survival break to fail only after its inventory drop has been staged in
    // the same reducer transaction. The failed call must roll back both writes.
    let current_snapshot = same_gateway
        .connection
        .db
        .gateway_chunk_snapshots()
        .iter()
        .find(|row| row.chunk_x == 0 && row.chunk_z == 0)
        .expect("Current shared chunk snapshot");
    let mut occupied: std::collections::HashSet<(i32, i32, i32)> = current_snapshot
        .encoded_overrides
        .as_chunks::<16>()
        .0
        .iter()
        .map(|entry| {
            (
                i32::from_le_bytes(entry[0..4].try_into().unwrap()),
                i32::from_le_bytes(entry[4..8].try_into().unwrap()),
                i32::from_le_bytes(entry[8..12].try_into().unwrap()),
            )
        })
        .collect();
    let mut override_count = occupied.len();
    let anchors = [(3, 3), (11, 3), (3, 11), (11, 11)];
    for (anchor_x, anchor_z) in anchors {
        if override_count == 128 {
            break;
        }
        let fill_id = Uuid::from_u128(uuid::Uuid::new_v4().as_u128());
        let fill_username = format!("W{}", &uuid::Uuid::new_v4().simple().to_string()[..10]);
        call!(
            a.connection,
            begin_offline_session_then(fill_id, fill_username.clone())
        )
        .unwrap();
        call!(a.connection, advance_login_session_then(fill_id)).unwrap();
        call!(a.connection, enter_play_session_then(fill_id)).unwrap();
        call!(a.connection, initialize_player_inventory_then(fill_id)).unwrap();
        call!(
            admin.connection,
            grant_player_game_mode_then(
                Uuid::from_u128(
                    crate::packets::login::offline_uuid(&fill_username)
                        .unwrap()
                        .as_u128()
                ),
                GameMode::Creative
            )
        )
        .unwrap();
        call!(
            a.connection,
            update_player_states_then(vec![PlayerPoseUpdate {
                session_uuid: fill_id,
                x: anchor_x as f64 + 0.5,
                y: 65.0,
                z: anchor_z as f64 + 0.5,
                yaw: 0.0,
                pitch: 0.0,
                on_ground: true,
            }])
        )
        .unwrap();
        call!(a.connection, update_world_interest_then(fill_id, 0, 0, 0)).unwrap();
        let mut sequence = 0_u64;
        for z in 0_i32..16 {
            for x in 0_i32..16 {
                if override_count == 128 || sequence == 32 {
                    break;
                }
                let (world_x, world_z) = (x, z);
                if (world_x - anchor_x).pow(2) + (world_z - anchor_z).pow(2) > 15
                    || occupied.contains(&(world_x, 63, world_z))
                {
                    continue;
                }
                sequence += 1;
                call!(
                    a.connection,
                    apply_block_action_then(fill_id, sequence, world_x, 63, world_z, 1, false)
                )
                .unwrap();
                occupied.insert((world_x, 63, world_z));
                override_count += 1;
            }
        }
        call!(a.connection, end_session_then(fill_id)).unwrap();
    }
    assert_eq!(override_count, 128, "The test must fill the chunk cap");
    let rollback_id = Uuid::from_u128(uuid::Uuid::new_v4().as_u128());
    let rollback_username = format!("W{}", &uuid::Uuid::new_v4().simple().to_string()[..10]);
    call!(
        a.connection,
        begin_offline_session_then(rollback_id, rollback_username.clone())
    )
    .unwrap();
    call!(a.connection, advance_login_session_then(rollback_id)).unwrap();
    call!(a.connection, enter_play_session_then(rollback_id)).unwrap();
    call!(a.connection, initialize_player_inventory_then(rollback_id)).unwrap();
    until(|| {
        a.connection
            .db
            .gateway_inventory_snapshots()
            .iter()
            .any(|row| row.session_uuid == rollback_id)
    });
    let rollback_player = Uuid::from_u128(
        crate::packets::login::offline_uuid(&rollback_username)
            .unwrap()
            .as_u128(),
    );
    let rollback_inventory_before = a
        .connection
        .db
        .gateway_inventory_snapshots()
        .iter()
        .find(|row| row.session_uuid == rollback_id)
        .expect("Rollback test inventory snapshot")
        .clone();
    call!(
        a.connection,
        update_player_states_then(vec![PlayerPoseUpdate {
            session_uuid: rollback_id,
            x: 15.5,
            y: 65.0,
            z: 15.5,
            yaw: 0.0,
            pitch: 0.0,
            on_ground: true,
        }])
    )
    .unwrap();
    call!(
        a.connection,
        update_world_interest_then(rollback_id, 0, 0, 0)
    )
    .unwrap();
    call!(
        admin.connection,
        grant_player_game_mode_then(rollback_player, GameMode::Survival)
    )
    .unwrap();
    let cap_error = call!(
        a.connection,
        apply_block_action_then(rollback_id, 1, 15, 63, 15, 1, false)
    );
    assert!(cap_error.is_err(), "The full chunk must reject the edit");
    assert_eq!(
        a.connection
            .db
            .gateway_inventory_snapshots()
            .iter()
            .find(|row| row.session_uuid == rollback_id),
        Some(rollback_inventory_before.clone()),
        "A rejected transaction must roll back its staged block drop"
    );
    assert!(!a
        .connection
        .db
        .gateway_block_actions()
        .iter()
        .any(|row| row.session_uuid == rollback_id));
    let snapshot_after_rejection = a
        .connection
        .db
        .gateway_chunk_snapshots()
        .iter()
        .find(|row| row.chunk_x == 0 && row.chunk_z == 0)
        .expect("Chunk snapshot after rejected edit");
    assert_eq!(snapshot_after_rejection.encoded_overrides.len() / 16, 128);
    assert!(!snapshot_after_rejection
        .encoded_overrides
        .as_chunks::<16>()
        .0
        .iter()
        .any(|entry| {
            i32::from_le_bytes(entry[0..4].try_into().unwrap()) == 15
                && i32::from_le_bytes(entry[4..8].try_into().unwrap()) == 63
                && i32::from_le_bytes(entry[8..12].try_into().unwrap()) == 15
        }));
    call!(a.connection, end_session_then(rollback_id)).unwrap();

    // End/recreate the offline session while retaining the same profile. The
    // world override survives and rejects the stale generator-base state.
    call!(a.connection, end_session_then(id)).unwrap();
    until(|| a.connection.db.gateway_sessions().count() == 0);
    let next = Uuid::from_u128(uuid::Uuid::new_v4().as_u128());
    call!(
        a.connection,
        begin_offline_session_then(next, username.clone())
    )
    .unwrap();
    call!(a.connection, advance_login_session_then(next)).unwrap();
    call!(a.connection, enter_play_session_then(next)).unwrap();
    call!(a.connection, initialize_player_inventory_then(next)).unwrap();
    until(|| {
        a.connection.db.gateway_player_state().iter().any(|row| {
            row.session_uuid == next
                && row.health == 13
                && row.food == 7
                && !row.dead
                && row.revision == 5
                && row.respawn_revision == Some(4)
        })
    });
    call!(
        a.connection,
        update_player_states_then(vec![PlayerPoseUpdate {
            session_uuid: next,
            x: 8.5,
            y: 65.0,
            z: 8.5,
            yaw: 0.0,
            pitch: 0.0,
            on_ground: true,
        }])
    )
    .unwrap();
    call!(a.connection, update_world_interest_then(next, 0, 0, 0)).unwrap();
    assert!(call!(
        a.connection,
        apply_block_action_then(next, 1, 8, 63, 8, 1, false)
    )
    .is_err());
    call!(
        a.connection,
        apply_block_action_then(next, 1, 8, 63, 8, 0, true)
    )
    .unwrap();
    call!(a.connection, end_session_then(next)).unwrap();
    call!(same_gateway.connection, end_session_then(peer_id)).unwrap();

    // Reconnect the same gateway identity with its fresh test token, then
    // reclaim the profile in a new session and confirm vitals still persist.
    a.connection.disconnect().unwrap();
    let restarted_gateway = Client::connect(&host, &database, Some(a.token.clone()));
    assert_eq!(restarted_gateway.identity, a.identity);
    subscribe_actions(&restarted_gateway);
    let resumed_id = Uuid::from_u128(uuid::Uuid::new_v4().as_u128());
    call!(
        restarted_gateway.connection,
        begin_offline_session_then(resumed_id, username.clone())
    )
    .unwrap();
    call!(
        restarted_gateway.connection,
        advance_login_session_then(resumed_id)
    )
    .unwrap();
    call!(
        restarted_gateway.connection,
        enter_play_session_then(resumed_id)
    )
    .unwrap();
    until(|| {
        restarted_gateway
            .connection
            .db
            .gateway_player_state()
            .iter()
            .any(|row| {
                row.session_uuid == resumed_id
                    && row.health == 13
                    && row.food == 7
                    && !row.dead
                    && row.revision == 5
                    && row.respawn_revision == Some(4)
            })
    });
    call!(
        restarted_gateway.connection,
        initialize_player_inventory_then(resumed_id)
    )
    .unwrap();
    call!(
        restarted_gateway.connection,
        update_player_states_then(vec![PlayerPoseUpdate {
            session_uuid: resumed_id,
            x: 8.5,
            y: 65.0,
            z: 8.5,
            yaw: 0.0,
            pitch: 0.0,
            on_ground: true,
        }])
    )
    .unwrap();
    call!(
        restarted_gateway.connection,
        update_world_interest_then(resumed_id, 0, 0, 0)
    )
    .unwrap();
    until(|| snapshot_contains(&restarted_gateway, 1));
    call!(restarted_gateway.connection, end_session_then(resumed_id)).unwrap();

    let revoked_session = Uuid::from_u128(uuid::Uuid::new_v4().as_u128());
    let revoked_username = format!("W{}", &uuid::Uuid::new_v4().simple().to_string()[..10]);
    call!(
        restarted_gateway.connection,
        begin_offline_session_then(revoked_session, revoked_username.clone())
    )
    .unwrap();
    call!(
        restarted_gateway.connection,
        advance_login_session_then(revoked_session)
    )
    .unwrap();
    call!(
        restarted_gateway.connection,
        enter_play_session_then(revoked_session)
    )
    .unwrap();
    call!(admin.connection, revoke_gateway_then(a.identity)).unwrap();
    until(|| restarted_gateway.connection.db.my_gateway().count() == 0);
    assert!(call!(
        restarted_gateway.connection,
        apply_block_action_then(revoked_session, 1, 8, 63, 8, 1, false)
    )
    .is_err());
    assert_eq!(b.connection.db.my_gateway().count(), 1);
}
