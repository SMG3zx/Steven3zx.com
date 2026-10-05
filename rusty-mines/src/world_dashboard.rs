use crate::{dashboard, module_bindings::SessionPhase, players};
use ratatui::{
    layout::{Constraint, Layout, Rect},
    widgets::Paragraph,
    Frame,
};

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct Snapshot {
    pub players: players::Snapshot,
}
impl Snapshot {
    fn fields(&self) -> Vec<(&'static str, String)> {
        let m = &rusty_mines::vanilla_world::METADATA;
        vec![
            ("Dimension", m.dimension.into()),
            ("Spawn", m.spawn.into()),
            ("Spawn block", m.default_spawn.into()),
            (
                "Chunks",
                format!(
                    "{}; x/z -{}..={} (static delivered area)",
                    (2 * m.chunk_radius + 1).pow(2),
                    m.chunk_radius,
                    m.chunk_radius
                ),
            ),
            (
                "Platform",
                format!("grass y={}; stone 63; bedrock 62", m.platform_y),
            ),
            ("Height", m.height.into()),
            ("Mode", m.mode.into()),
            (
                "Local Play",
                self.players
                    .rows
                    .iter()
                    .filter(|p| p.phase == SessionPhase::Play)
                    .count()
                    .to_string(),
            ),
        ]
    }
    pub fn plaintext(&self) -> String {
        let mut lines = vec!["WORLD — static official 26.3 / protocol 777".into()];
        lines.extend(self.fields().into_iter().map(|(k, v)| format!("{k}: {v}")));
        lines.push(rusty_mines::vanilla_world::METADATA.limitations.into());
        lines.join("\n")
    }
}

pub(crate) fn render(frame: &mut Frame, area: Rect, snapshot: &Snapshot) {
    let sections = Layout::vertical([Constraint::Length(10), Constraint::Min(0)]).split(area);
    dashboard::fields(
        frame,
        sections[0],
        " WORLD — static 26.3 / 777 ",
        snapshot.fields(),
        "",
    );
    frame.render_widget(
        Paragraph::new(rusty_mines::vanilla_world::METADATA.limitations)
            .wrap(ratatui::widgets::Wrap { trim: true })
            .block(dashboard::block(" LIMITATIONS ")),
        sections[1],
    );
}
