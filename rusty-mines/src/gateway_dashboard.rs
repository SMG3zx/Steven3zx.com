use crate::{dashboard, module_bindings::SessionPhase, players};
use ratatui::{
    layout::{Constraint, Layout, Rect},
    style::{Color, Style},
    text::{Line, Span},
    widgets::Paragraph,
    Frame,
};

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct Snapshot {
    pub host: String,
    pub database: String,
    pub identity: Option<String>,
    pub owner_connection: Option<String>,
    pub connected: bool,
    pub subscribed: bool,
    pub registered: Option<bool>,
    pub health: &'static str,
    pub generation: u64,
    pub last_failure: Option<&'static str>,
    pub players: players::Snapshot,
}
impl Snapshot {
    fn fields(&self) -> Vec<(&'static str, String)> {
        vec![
            ("Endpoint", self.host.clone()),
            ("Database", self.database.clone()),
            (
                "Identity",
                self.identity
                    .clone()
                    .unwrap_or_else(|| "unavailable".into()),
            ),
            (
                "Owner SDK",
                self.owner_connection
                    .clone()
                    .unwrap_or_else(|| "unavailable".into()),
            ),
            ("Connected", self.connected.to_string()),
            ("Subscribed", self.subscribed.to_string()),
            ("SpacetimeDB", self.health.into()),
            (
                "Generation",
                format!("{} diagnostic changes", self.generation),
            ),
            (
                "Last failure",
                self.last_failure.unwrap_or("none recorded").into(),
            ),
        ]
    }
    fn counts(&self) -> String {
        let count = |phase| {
            self.players
                .rows
                .iter()
                .filter(|p| p.phase == phase)
                .count()
        };
        format!("Local sessions: Login {} · Configuration {} · Play {}\n{} identity-scoped nonlocal/unavailable rows excluded; other SDK connections are not counted.", count(SessionPhase::Login), count(SessionPhase::Configuration), count(SessionPhase::Play), self.players.excluded)
    }
    pub fn plaintext(&self) -> String {
        let mut lines = vec!["GATEWAY".into()];
        lines.extend(self.fields().into_iter().map(|(k, v)| format!("{k}: {v}")));
        lines.push(format!("Registration: {}", self.registration()));
        lines.push(self.counts());
        lines.join("\n")
    }
    fn registration(&self) -> &'static str {
        match self.registered {
            Some(true) => "[registered]",
            Some(false) => "[unregistered]",
            None => "unavailable (awaiting connected subscription)",
        }
    }
}
pub(crate) fn render(frame: &mut Frame, area: Rect, snapshot: &Snapshot) {
    let sections = Layout::vertical([
        Constraint::Length(11),
        Constraint::Length(1),
        Constraint::Min(0),
    ])
    .split(area);
    dashboard::fields(
        frame,
        sections[0],
        " GATEWAY ",
        snapshot.fields(),
        snapshot.health,
    );
    frame.render_widget(
        Paragraph::new(Line::from(vec![
            Span::raw("Registration: "),
            Span::styled(
                snapshot.registration(),
                Style::default().fg(match snapshot.registered {
                    Some(true) => Color::Green,
                    Some(false) => Color::Red,
                    None => Color::Yellow,
                }),
            ),
        ])),
        sections[1],
    );
    frame.render_widget(
        Paragraph::new(snapshot.counts())
            .wrap(ratatui::widgets::Wrap { trim: true })
            .block(dashboard::block(" LOCAL SESSIONS ")),
        sections[2],
    );
}
