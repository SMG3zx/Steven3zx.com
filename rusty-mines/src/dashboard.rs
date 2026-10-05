use crate::metrics::{ProcessStats, TrafficStats};
use ratatui::{
    layout::{Constraint, Layout, Rect},
    style::{Color, Style},
    text::{Line, Span},
    widgets::{Block, BorderType, Paragraph, Row, Table},
    Frame,
};

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct StatusSnapshot {
    pub listen: String,
    pub database: String,
    pub host: String,
    pub gateway: &'static str,
    pub sockets: usize,
    pub players: usize,
    pub uptime_secs: u64,
    pub process: ProcessStats,
    pub traffic: TrafficStats,
}

fn bytes(value: u64) -> String {
    let mut value = value as f64;
    let mut unit = "B";
    for next in ["KiB", "MiB", "GiB", "TiB", "PiB", "EiB"] {
        if value < 1024.0 {
            break;
        }
        value /= 1024.0;
        unit = next;
    }
    if unit == "B" {
        format!("{value:.0} {unit}")
    } else {
        format!("{value:.2} {unit}")
    }
}

impl StatusSnapshot {
    fn server(&self) -> Vec<(&str, String)> {
        vec![
            ("Listening", self.listen.clone()),
            ("Database", self.database.clone()),
            ("Host", self.host.clone()),
            ("SpacetimeDB", self.gateway.into()),
            ("Players", format!("{} online (Play)", self.players)),
            ("Sockets", self.sockets.to_string()),
            (
                "Uptime",
                format!(
                    "{}d {:02}:{:02}:{:02}",
                    self.uptime_secs / 86400,
                    self.uptime_secs / 3600 % 24,
                    self.uptime_secs / 60 % 60,
                    self.uptime_secs % 60
                ),
            ),
        ]
    }
    fn process(&self) -> Vec<(&str, String)> {
        vec![
            (
                "CPU",
                self.process
                    .cpu_percent
                    .map_or_else(|| "—".into(), |v| format!("{v:.2}%")),
            ),
            (
                "Memory",
                self.process
                    .memory_mib
                    .map_or_else(|| "—".into(), |v| format!("{v:.2} MiB")),
            ),
        ]
    }
    fn network(&self) -> Vec<Row<'static>> {
        let t = &self.traffic;
        [
            ("RX", t.rx_bytes, t.rx_frames),
            ("TX", t.tx_bytes, t.tx_frames),
            (
                "Total",
                t.rx_bytes.saturating_add(t.tx_bytes),
                t.rx_frames.saturating_add(t.tx_frames),
            ),
        ]
        .into_iter()
        .map(|(label, size, frames)| {
            Row::new(vec![label.to_owned(), bytes(size), frames.to_string()])
        })
        .collect()
    }
    fn note(&self) -> &'static str {
        if self.gateway == "authorized" {
            "Players = Play sessions; gameplay unavailable."
        } else {
            "Login unavailable: check gateway state and logs; gameplay unavailable."
        }
    }
    pub fn plaintext(&self) -> String {
        let mut lines = vec!["SERVER".to_owned()];
        lines.extend(self.server().into_iter().map(|(k, v)| format!("{k}: {v}")));
        lines.push("PROCESS (60s rolling avg)".into());
        lines.extend(self.process().into_iter().map(|(k, v)| format!("{k}: {v}")));
        let t = &self.traffic;
        lines.push(format!("MINECRAFT TCP (since startup)\nRX: {} / {} frames\nTX: {} / {} frames\nTotal: {} / {} frames", bytes(t.rx_bytes), t.rx_frames, bytes(t.tx_bytes), t.tx_frames, bytes(t.rx_bytes.saturating_add(t.tx_bytes)), t.rx_frames.saturating_add(t.tx_frames)));
        lines.extend([SCOPE.into(), NETWORK_SCOPE.into(), self.note().into()]);
        lines.join("\n")
    }
}
const SCOPE: &str = "Process only; CPU normalized to machine capacity; no global traffic.";
const NETWORK_SCOPE: &str = "TCP payload / Minecraft frames; excludes SDK, DNS and TCP/IP headers.";

pub(crate) fn block(title: &str) -> Block<'_> {
    Block::bordered()
        .border_type(BorderType::Rounded)
        .title(title)
        .border_style(Style::default().fg(Color::DarkGray))
        .title_style(Style::default().fg(Color::Cyan))
}
pub(crate) fn fields(
    frame: &mut Frame,
    area: Rect,
    title: &str,
    rows: Vec<(&str, String)>,
    status: &str,
) {
    let inner = block(title).inner(area);
    frame.render_widget(block(title), area);
    let lines: Vec<Line> = rows
        .into_iter()
        .map(|(label, value)| {
            let color = if label == "SpacetimeDB" {
                match status {
                    "authorized" => Color::Green,
                    "subscribing" => Color::Yellow,
                    _ => Color::Red,
                }
            } else {
                Color::Reset
            };
            Line::from(vec![
                Span::styled(format!("{label:<12}"), Style::default().fg(Color::DarkGray)),
                Span::styled(
                    ellipsize(&value, inner.width.saturating_sub(12) as usize),
                    Style::default().fg(color),
                ),
            ])
        })
        .collect();
    frame.render_widget(Paragraph::new(lines), inner);
}
pub(crate) fn ellipsize(text: &str, width: usize) -> String {
    use unicode_segmentation::UnicodeSegmentation;
    let clean: String = text.chars().filter(|c| !c.is_control()).collect();
    if Line::from(clean.as_str()).width() <= width {
        return clean;
    }
    let mut result = String::new();
    for g in clean.graphemes(true) {
        if Line::from(result.as_str()).width() + Line::from(g).width() > width.saturating_sub(1) {
            break;
        }
        result.push_str(g);
    }
    if width > 0 {
        result.push('…');
    }
    result
}

pub(crate) fn render(frame: &mut Frame, area: Rect, snapshot: &StatusSnapshot) {
    let wide = area.width >= 90;
    let sections = Layout::vertical([
        Constraint::Length(if wide { 9 } else { 14 }),
        Constraint::Length(6),
        Constraint::Min(0),
    ])
    .split(area);
    let top = if wide {
        Layout::horizontal([Constraint::Percentage(60), Constraint::Percentage(40)])
            .split(sections[0])
    } else {
        Layout::vertical([Constraint::Length(9), Constraint::Length(5)]).split(sections[0])
    };
    fields(
        frame,
        top[0],
        " SERVER ",
        snapshot.server(),
        snapshot.gateway,
    );
    fields(
        frame,
        top[1],
        " PROCESS (60s rolling avg) ",
        snapshot.process(),
        snapshot.gateway,
    );
    let table = Table::new(
        snapshot.network(),
        [
            Constraint::Length(8),
            Constraint::Min(12),
            Constraint::Length(14),
        ],
    )
    .header(Row::new(["Direction", "Bytes", "Frames"]).style(Style::default().fg(Color::DarkGray)))
    .block(block(" MINECRAFT TCP (since startup) "));
    frame.render_widget(table, sections[1]);
    frame.render_widget(
        Paragraph::new(vec![
            Line::from(snapshot.note()).style(Style::default().fg(
                if snapshot.gateway == "authorized" {
                    Color::DarkGray
                } else {
                    Color::Yellow
                },
            )),
            Line::from(SCOPE),
            Line::from(NETWORK_SCOPE),
        ])
        .style(Style::default().fg(Color::DarkGray)),
        sections[2],
    );
}

#[cfg(test)]
mod tests {
    use super::*;
    fn screen(terminal: &ratatui::Terminal<ratatui::backend::TestBackend>) -> Vec<String> {
        let buffer = terminal.backend().buffer();
        (0..buffer.area.height)
            .map(|y| {
                (0..buffer.area.width)
                    .map(|x| buffer[(x, y)].symbol())
                    .collect()
            })
            .collect()
    }

    #[test]
    fn wide_and_stacked_sections_show_state_and_totals() {
        use ratatui::{backend::TestBackend, Terminal};
        let snapshot = StatusSnapshot {
            database: "mines".into(),
            host: "https://database.example".into(),
            gateway: "unauthorized",
            traffic: TrafficStats {
                rx_bytes: 1024,
                tx_bytes: 2048,
                rx_frames: 3,
                tx_frames: 4,
            },
            process: ProcessStats {
                samples: 38,
                ..Default::default()
            },
            ..Default::default()
        };
        for (width, height, process_y, network_y) in [(110, 25, 0, 9), (70, 30, 9, 14)] {
            let mut terminal = Terminal::new(TestBackend::new(width, height)).unwrap();
            terminal
                .draw(|frame| render(frame, frame.area(), &snapshot))
                .unwrap();
            let rows = screen(&terminal);
            assert!(rows[0].contains("SERVER"));
            assert!(rows[process_y].contains("PROCESS (60s rolling avg)"));
            assert!(rows[network_y].contains("MINECRAFT TCP (since startup)"));
            let text = rows.join("\n");
            for value in [
                "unauthorized",
                "3.00 KiB",
                "Login unavailable",
                "Database",
                "Host",
            ] {
                assert!(text.contains(value), "missing {value}: {text}");
            }
            assert!(!text.contains("Window"));
            assert!(!text.contains("/60 samples"));
            assert_eq!(terminal.backend().buffer()[(13, 4)].fg, Color::Red);
        }
        let plaintext = snapshot.plaintext();
        assert!(plaintext.contains("Total: 3.00 KiB / 7 frames"));
        assert!(plaintext.contains("Login unavailable"));
    }

    #[test]
    fn units_and_unicode_truncation() {
        assert_eq!(bytes(1024), "1.00 KiB");
        assert_eq!(bytes(0), "0 B");
        assert_eq!(ellipsize("界界界", 4), "界…");
    }
}
