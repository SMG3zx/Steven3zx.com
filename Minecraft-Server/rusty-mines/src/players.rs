use crate::module_bindings::GatewayWorldPlayer;
use crate::module_bindings::SessionPhase;
use std::{
    collections::BTreeMap,
    io,
    sync::atomic::{AtomicBool, AtomicU64, Ordering},
    sync::{mpsc, Arc, Mutex},
    time::{Duration, Instant},
};
use uuid::Uuid;

#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Player {
    pub id: Uuid,
    pub username: String,
    pub phase: SessionPhase,
    pub connected_secs: u64,
    pub latency: Option<Duration>,
}
#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct Snapshot {
    pub rows: Vec<Player>,
    pub excluded: usize,
}
impl Snapshot {
    pub fn plaintext(&self) -> String {
        let play = self
            .rows
            .iter()
            .filter(|p| p.phase == SessionPhase::Play)
            .count();
        let mut lines = vec![format!(
            "LOCAL PLAYERS: {play} Play, {} joining",
            self.rows.len() - play
        )];
        for (i, p) in self.rows.iter().enumerate() {
            lines.push(format!(
                "{}  {}  {:?}  {}s  {}",
                i + 1,
                p.username,
                p.phase,
                p.connected_secs,
                latency(p.latency)
            ));
        }
        lines.push(format!("{} identity-scoped nonlocal/unavailable sessions excluded; kick is local-only. RTT = measured keepalive acknowledgement; — = no sample.", self.excluded));
        lines.join("\n")
    }
}
fn latency(value: Option<Duration>) -> String {
    value.map_or_else(
        || "—".into(),
        |v| format!("{:.1} ms", v.as_secs_f64() * 1000.0),
    )
}
pub(crate) fn render(frame: &mut ratatui::Frame, area: ratatui::layout::Rect, snapshot: &Snapshot) {
    use ratatui::{
        layout::{Constraint, Layout},
        widgets::{Block, Paragraph, Row, Table},
    };
    let areas = Layout::vertical([
        Constraint::Length(2),
        Constraint::Min(0),
        Constraint::Length(3),
    ])
    .split(area);
    let play = snapshot
        .rows
        .iter()
        .filter(|p| p.phase == SessionPhase::Play)
        .count();
    frame.render_widget(
        Paragraph::new(format!(
            "LOCAL PLAYERS: {play} Play, {} joining",
            snapshot.rows.len() - play
        )),
        areas[0],
    );
    frame.render_widget(
        Table::new(
            snapshot.rows.iter().enumerate().map(|(i, p)| {
                Row::new(vec![
                    (i + 1).to_string(),
                    p.username.clone(),
                    format!("{:?}", p.phase),
                    format!("{}s", p.connected_secs),
                    latency(p.latency),
                ])
            }),
            [
                Constraint::Length(4),
                Constraint::Min(8),
                Constraint::Length(13),
                Constraint::Length(10),
                Constraint::Length(12),
            ],
        )
        .header(Row::new(["#", "Username", "Phase", "Connected", "RTT"]))
        .block(Block::bordered().title(" Players ")),
        areas[1],
    );
    frame.render_widget(Paragraph::new(format!("{} identity-scoped nonlocal/unavailable sessions excluded; local-only kick.\nRTT = measured keepalive acknowledgement; — = no sample.", snapshot.excluded)), areas[2]);
}
pub(crate) type Delivery = (Uuid, mpsc::Receiver<Result<(), String>>);

pub(crate) struct Kick {
    pub reason: String,
    pub ack: mpsc::Sender<Result<(), String>>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct ChatDelivery {
    pub username: String,
    pub text: String,
    generation: u64,
}
impl ChatDelivery {
    pub fn new(username: String, text: String) -> Self {
        Self {
            username,
            text,
            generation: 0,
        }
    }
}

#[derive(Clone, Debug)]
pub(crate) enum ReplicationEvent {
    Upsert(GatewayWorldPlayer),
    Remove(uuid::Uuid),
}

pub(crate) const REPLICATION_QUEUE_LIMIT: usize = 128;
struct Entry {
    username: String,
    started: Instant,
    latency: Option<Duration>,
    sender: mpsc::SyncSender<Kick>,
    messages: mpsc::SyncSender<Kick>,
    replication: mpsc::SyncSender<ReplicationEvent>,
    replication_overflow: Arc<AtomicBool>,
    playing: bool,
    chat: mpsc::SyncSender<ChatDelivery>,
    chat_overflow: Arc<AtomicBool>,
    pending: bool,
}
#[derive(Default)]
pub(crate) struct Registry(Mutex<BTreeMap<Uuid, Entry>>, AtomicU64);
pub(crate) struct Guard {
    registry: Arc<Registry>,
    id: Uuid,
    pub receiver: mpsc::Receiver<Kick>,
    pub messages: mpsc::Receiver<Kick>,
    pub replication: mpsc::Receiver<ReplicationEvent>,
    replication_overflow: Arc<AtomicBool>,
    pub chat: mpsc::Receiver<ChatDelivery>,
    chat_overflow: Arc<AtomicBool>,
}
impl Guard {
    pub fn take_replication_overflow(&self) -> bool {
        self.replication_overflow.swap(false, Ordering::AcqRel)
    }
    pub fn take_chat_overflow(&self) -> bool {
        self.chat_overflow.swap(false, Ordering::AcqRel)
    }
}
impl Drop for Guard {
    fn drop(&mut self) {
        self.registry
            .0
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .remove(&self.id);
        while let Ok(message) = self.messages.try_recv() {
            let _ = message
                .ack
                .send(Err("Connection closed before message delivery".into()));
        }
        while let Ok(kick) = self.receiver.try_recv() {
            let _ = kick.ack.send(Err(
                "Connection closed before the disconnect could be sent".into()
            ));
        }
    }
}
impl Registry {
    pub fn snapshot(
        &self,
        sessions: impl IntoIterator<Item = (Uuid, SessionPhase, bool)>,
    ) -> Snapshot {
        let entries = self.0.lock().unwrap_or_else(|e| e.into_inner());
        let mut snapshot = Snapshot::default();
        for (id, phase, owned) in sessions {
            if let Some(entry) = entries.get(&id).filter(|_| owned) {
                snapshot.rows.push(Player {
                    id,
                    username: entry.username.clone(),
                    phase,
                    connected_secs: entry.started.elapsed().as_secs(),
                    latency: entry.latency,
                });
            } else {
                snapshot.excluded += 1;
            }
        }
        snapshot.rows.sort_by_key(|player| player.id);
        snapshot
    }

    pub fn register(self: &Arc<Self>, id: Uuid, username: String) -> Guard {
        let (sender, receiver) = mpsc::sync_channel(1);
        let (messages, message_receiver) = mpsc::sync_channel(16);
        let (replication, replication_receiver) = mpsc::sync_channel(REPLICATION_QUEUE_LIMIT);
        let replication_overflow = Arc::new(AtomicBool::new(false));
        let (chat, chat_receiver) = mpsc::sync_channel(REPLICATION_QUEUE_LIMIT);
        let chat_overflow = Arc::new(AtomicBool::new(false));
        self.0.lock().unwrap_or_else(|e| e.into_inner()).insert(
            id,
            Entry {
                username,
                started: Instant::now(),
                latency: None,
                sender,
                messages,
                replication,
                replication_overflow: replication_overflow.clone(),
                playing: false,
                chat,
                chat_overflow: chat_overflow.clone(),
                pending: false,
            },
        );
        Guard {
            registry: self.clone(),
            id,
            receiver,
            messages: message_receiver,
            replication: replication_receiver,
            replication_overflow,
            chat: chat_receiver,
            chat_overflow,
        }
    }
    pub fn mark_playing(&self, id: Uuid) {
        if let Some(entry) = self
            .0
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .get_mut(&id)
        {
            entry.playing = true;
        }
    }
    pub fn publish_replication(&self, event: ReplicationEvent) {
        let event_id = match &event {
            ReplicationEvent::Upsert(row) => uuid::Uuid::from_u128(row.session_uuid.as_u128()),
            ReplicationEvent::Remove(id) => *id,
        };
        let entries = self.0.lock().unwrap_or_else(|e| e.into_inner());
        for (&id, entry) in entries.iter() {
            if id == event_id || !entry.playing {
                continue;
            }
            if let Err(mpsc::TrySendError::Full(_)) = entry.replication.try_send(event.clone()) {
                entry.replication_overflow.store(true, Ordering::Release);
            }
        }
    }
    pub fn publish_chat(&self, message: ChatDelivery) {
        let message = ChatDelivery {
            generation: self.1.load(Ordering::Acquire),
            ..message
        };
        let entries = self.0.lock().unwrap_or_else(|e| e.into_inner());
        for entry in entries.values().filter(|entry| entry.playing) {
            if let Err(mpsc::TrySendError::Full(_)) = entry.chat.try_send(message.clone()) {
                entry.chat_overflow.store(true, Ordering::Release);
            }
        }
    }
    pub fn clear_chat(&self) {
        self.1.fetch_add(1, Ordering::AcqRel);
    }
    pub fn chat_is_current(&self, message: &ChatDelivery) -> bool {
        message.generation == self.1.load(Ordering::Acquire)
    }
    pub fn publish_snapshot_to(
        &self,
        recipient: Uuid,
        rows: impl IntoIterator<Item = GatewayWorldPlayer>,
    ) {
        let entries = self.0.lock().unwrap_or_else(|e| e.into_inner());
        let Some(entry) = entries.get(&recipient).filter(|entry| entry.playing) else {
            return;
        };
        for row in rows {
            if uuid::Uuid::from_u128(row.session_uuid.as_u128()) == recipient {
                continue;
            }
            if let Err(mpsc::TrySendError::Full(_)) =
                entry.replication.try_send(ReplicationEvent::Upsert(row))
            {
                entry.replication_overflow.store(true, Ordering::Release);
                break;
            }
        }
    }
    pub fn broadcast(&self, snapshot: &Snapshot, text: &str) -> io::Result<Vec<Delivery>> {
        validate_reason(text)?;
        let entries = self.0.lock().unwrap_or_else(|e| e.into_inner());
        let mut results = Vec::new();
        for player in snapshot
            .rows
            .iter()
            .filter(|p| p.phase == SessionPhase::Play)
        {
            let (ack, receiver) = mpsc::channel();
            if let Some(entry) = entries.get(&player.id).filter(|e| !e.pending) {
                if let Err(error) = entry.messages.try_send(Kick {
                    reason: text.into(),
                    ack,
                }) {
                    let message = match error {
                        mpsc::TrySendError::Full(m) | mpsc::TrySendError::Disconnected(m) => m,
                    };
                    let _ = message.ack.send(Err("Message queue full or closed".into()));
                }
            } else {
                let _ = ack.send(Err("Player left or kick pending".into()));
            }
            results.push((player.id, receiver));
        }
        Ok(results)
    }
    pub fn sample(&self, id: Uuid, value: Duration) {
        if let Some(entry) = self
            .0
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .get_mut(&id)
        {
            entry.latency = Some(value);
        }
    }
    #[cfg(test)]
    pub fn player(&self, id: Uuid, phase: SessionPhase) -> Option<Player> {
        self.0
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .get(&id)
            .map(|e| Player {
                id,
                username: e.username.clone(),
                phase,
                connected_secs: e.started.elapsed().as_secs(),
                latency: e.latency,
            })
    }
    pub fn issue(
        &self,
        id: Uuid,
        reason: String,
    ) -> io::Result<mpsc::Receiver<Result<(), String>>> {
        validate_reason(&reason)?;
        let (ack, receiver) = mpsc::channel();
        let mut entries = self.0.lock().unwrap_or_else(|e| e.into_inner());
        let entry = entries
            .get_mut(&id)
            .ok_or_else(|| io::Error::other("Player has left; select again"))?;
        if entry.pending {
            return Err(io::Error::other(
                "A kick is already pending for this session",
            ));
        }
        entry
            .sender
            .try_send(Kick { reason, ack })
            .map_err(|_| io::Error::other("Connection closed or kick queue unavailable"))?;
        entry.pending = true;
        Ok(receiver)
    }
}
pub(crate) fn validate_reason(reason: &str) -> io::Result<()> {
    if reason.trim().is_empty()
        || reason.chars().count() > 256
        || reason.chars().any(char::is_control)
    {
        return Err(io::Error::other(
            "Reason must contain 1–256 characters and no terminal controls",
        ));
    }
    Ok(())
}
fn select(snapshot: &Snapshot, text: &str) -> io::Result<Uuid> {
    let text = text.trim();
    if let Ok(index) = text.parse::<usize>() {
        return snapshot
            .rows
            .get(index.wrapping_sub(1))
            .map(|p| p.id)
            .ok_or_else(|| io::Error::other("Invalid player number"));
    }
    let matches: Vec<_> = snapshot
        .rows
        .iter()
        .filter(|p| p.username.eq_ignore_ascii_case(text))
        .collect();
    if matches.len() != 1 {
        return Err(io::Error::other(
            "Username missing or ambiguous; use the player number",
        ));
    }
    Ok(matches[0].id)
}
pub(crate) fn prompt_broadcast(editor: &mut reedline::Reedline) -> io::Result<String> {
    use reedline::{DefaultPrompt, DefaultPromptSegment, Signal};
    loop {
        match editor.read_line(&DefaultPrompt::new(
            DefaultPromptSegment::Basic("Broadcast message".into()),
            DefaultPromptSegment::Empty,
        ))? {
            Signal::Success(text) => match validate_reason(&text) {
                Ok(()) => return Ok(text),
                Err(error) => println!("{error}"),
            },
            Signal::CtrlC | Signal::CtrlD => {
                return Err(io::Error::new(
                    io::ErrorKind::Interrupted,
                    "Broadcast cancelled",
                ))
            }
        }
    }
}
pub(crate) fn prompt(
    editor: &mut reedline::Reedline,
    snapshot: &Snapshot,
) -> io::Result<(Uuid, String)> {
    use reedline::{DefaultPrompt, DefaultPromptSegment, Signal};
    fn ask(editor: &mut reedline::Reedline, question: &str) -> io::Result<String> {
        match editor.read_line(&DefaultPrompt::new(
            DefaultPromptSegment::Basic(question.into()),
            DefaultPromptSegment::Empty,
        ))? {
            Signal::Success(line) => Ok(line),
            Signal::CtrlC | Signal::CtrlD => {
                Err(io::Error::new(io::ErrorKind::Interrupted, "Kick cancelled"))
            }
        }
    }
    if snapshot.rows.is_empty() {
        return Err(io::Error::other("No local backend-confirmed players"));
    }
    println!("{}", snapshot.plaintext());
    let id = loop {
        match select(snapshot, &ask(editor, "Player number or username")?) {
            Ok(id) => break id,
            Err(error) => println!("{error}"),
        }
    };
    let reason = loop {
        let reason = ask(editor, "Reason [Kicked by server operator]")?;
        let reason = if reason.trim().is_empty() {
            "Kicked by server operator".into()
        } else {
            reason
        };
        match validate_reason(&reason) {
            Ok(()) => break reason,
            Err(error) => println!("{error}"),
        }
    };
    Ok((id, reason))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn broadcasts_are_play_only_bounded_and_do_not_block_kicks() {
        let registry = Arc::new(Registry::default());
        let id = Uuid::new_v4();
        let joining = Uuid::new_v4();
        let guard = registry.register(id, "Alex".into());
        let other = registry.register(joining, "Joining".into());
        let snapshot = registry.snapshot([
            (id, SessionPhase::Play, true),
            (joining, SessionPhase::Configuration, true),
        ]);
        for _ in 0..16 {
            assert_eq!(registry.broadcast(&snapshot, "Hello").unwrap().len(), 1);
        }
        let full = registry.broadcast(&snapshot, "Overflow").unwrap();
        assert!(full[0].1.try_recv().unwrap().is_err());
        assert!(other.messages.try_recv().is_err());
        let kick = registry.issue(id, "bye".into()).unwrap();
        assert!(guard.receiver.try_recv().is_ok());
        let pending = registry.broadcast(&snapshot, "Late").unwrap();
        assert!(pending[0].1.try_recv().unwrap().is_err());
        drop((guard, other, kick));
        assert!(registry.broadcast(&snapshot, "\u{1b}").is_err());
    }
    #[test]
    fn registry_delivery_cleanup_and_reconnect_isolation() {
        let registry = Arc::new(Registry::default());
        let old = Uuid::new_v4();
        let guard = registry.register(old, "Alex".into());
        let ack = registry.issue(old, "Unicode 😀 quotes \"".into()).unwrap();
        assert!(registry.issue(old, "again".into()).is_err());
        let kick = guard.receiver.try_recv().unwrap();
        drop(guard);
        let new = Uuid::new_v4();
        let new_guard = registry.register(new, "Alex".into());
        assert!(registry.issue(old, "late".into()).is_err());
        kick.ack.send(Ok(())).unwrap();
        assert!(ack.recv().unwrap().is_ok());
        assert!(new_guard.receiver.try_recv().is_err());
        assert!(registry.player(old, SessionPhase::Login).is_none());
        assert_eq!(
            registry.player(new, SessionPhase::Login).unwrap().latency,
            None
        );
    }
    #[test]
    fn snapshot_requires_local_control_and_exact_connection_ownership() {
        let registry = Arc::new(Registry::default());
        let id = Uuid::new_v4();
        let foreign = Uuid::new_v4();
        let guard = registry.register(id, "Alex".into());
        let foreign_guard = registry.register(foreign, "Other".into());
        let unconfirmed = registry.register(Uuid::new_v4(), "Pending".into());
        for phase in [
            SessionPhase::Login,
            SessionPhase::Configuration,
            SessionPhase::Play,
        ] {
            let snapshot = registry.snapshot([
                (id, phase, true),
                (foreign, SessionPhase::Play, false),
                (Uuid::new_v4(), SessionPhase::Play, true),
            ]);
            assert_eq!(snapshot.excluded, 2);
            assert_eq!(snapshot.rows.len(), 1);
            assert_eq!(snapshot.rows[0].phase, phase);
            assert_eq!(select(&snapshot, "1").unwrap(), id);
            assert_eq!(select(&snapshot, "alex").unwrap(), id);
            assert!(select(&snapshot, "Other").is_err());
            assert!(select(&snapshot, "0").is_err());
        }
        drop((guard, foreign_guard, unconfirmed));
        assert!(registry
            .snapshot([(id, SessionPhase::Play, true)])
            .rows
            .is_empty());
    }

    #[test]
    fn queued_close_reports_failure_and_reason_bounds() {
        let registry = Arc::new(Registry::default());
        let id = Uuid::new_v4();
        let guard = registry.register(id, "Alex".into());
        let ack = registry.issue(id, "bye".into()).unwrap();
        drop(guard);
        assert!(ack.recv().unwrap().is_err());
        for reason in ["".to_owned(), "\u{1b}[31m".into(), "😀".repeat(257)] {
            assert!(validate_reason(&reason).is_err());
        }
        assert!(validate_reason(&"😀".repeat(256)).is_ok());
    }

    #[test]
    fn replication_delivery_is_play_only_bounded_and_fails_closed_on_overflow() {
        let registry = Arc::new(Registry::default());
        let source = Uuid::new_v4();
        let recipient = Uuid::new_v4();
        let source_guard = registry.register(source, "Source".into());
        let recipient_guard = registry.register(recipient, "Recipient".into());
        let row = GatewayWorldPlayer {
            session_uuid: spacetimedb_sdk::Uuid::from_u128(source.as_u128()),
            player_uuid: spacetimedb_sdk::Uuid::from_u128(Uuid::new_v4().as_u128()),
            username: "Source".into(),
            entity_id: 42,
            x: 8.5,
            y: 65.0,
            z: 8.5,
            yaw: 0.0,
            pitch: 0.0,
            on_ground: false,
            revision: 1,
        };
        registry.publish_replication(ReplicationEvent::Upsert(row.clone()));
        assert!(recipient_guard.replication.try_recv().is_err());
        registry.mark_playing(recipient);
        for _ in 0..=REPLICATION_QUEUE_LIMIT {
            registry.publish_replication(ReplicationEvent::Upsert(row.clone()));
        }
        assert!(recipient_guard.take_replication_overflow());
        assert!(!recipient_guard.take_replication_overflow());
        assert_eq!(
            recipient_guard.replication.try_iter().count(),
            REPLICATION_QUEUE_LIMIT
        );
        drop(source_guard);
    }

    #[test]
    fn chat_delivery_is_play_only_bounded_and_reports_slow_recipients() {
        let registry = Arc::new(Registry::default());
        let id = Uuid::new_v4();
        let guard = registry.register(id, "Alex".into());
        registry.publish_chat(ChatDelivery::new("Sam".into(), "early".into()));
        assert!(guard.chat.try_recv().is_err());
        registry.mark_playing(id);
        for _ in 0..=REPLICATION_QUEUE_LIMIT {
            registry.publish_chat(ChatDelivery::new("Sam".into(), "hello".into()));
        }
        assert!(guard.take_chat_overflow());
        let queued: Vec<_> = guard.chat.try_iter().collect();
        assert_eq!(queued.len(), REPLICATION_QUEUE_LIMIT);
        assert!(queued
            .iter()
            .all(|message| registry.chat_is_current(message)));
        registry.clear_chat();
        assert!(queued
            .iter()
            .all(|message| !registry.chat_is_current(message)));
    }
}
