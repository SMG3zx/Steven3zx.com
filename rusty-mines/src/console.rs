use std::{collections::VecDeque, io, sync::mpsc::Receiver, time::Duration};

use crossterm::{
    event::{
        self, DisableBracketedPaste, EnableBracketedPaste, Event, KeyCode, KeyEventKind,
        KeyModifiers,
    },
    execute,
    terminal::{disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen},
};
use ratatui::{
    backend::CrosstermBackend,
    layout::{Constraint, Layout},
    widgets::{Block, Paragraph},
    Terminal,
};
use tui_input::{backend::crossterm::EventHandler, Input};

pub const COMMANDS: [&str; 12] = [
    "help",
    "status",
    "players",
    "world",
    "gateway",
    "kick",
    "broadcast",
    "logs",
    "config",
    "clear",
    "stop",
    "exit",
];
const LIMIT: usize = 4096;

#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct LogRecord {
    pub(crate) text: String,
    registered: bool,
}

impl LogRecord {
    pub(crate) fn plain(text: String) -> Self {
        Self {
            text,
            registered: false,
        }
    }

    pub(crate) fn gateway(identity: &str, registered: bool) -> Self {
        Self {
            text: if registered {
                format!("Gateway identity: {identity} [registered]")
            } else {
                format!("Gateway identity: {identity} [unregistered]. Administrator must register_gateway this identity.")
            },
            registered,
        }
    }

    fn wrapped(&self, width: usize) -> Vec<ratatui::text::Line<'static>> {
        use ratatui::{
            style::{Color, Style},
            text::{Line, Span},
        };
        let tag_start = self
            .registered
            .then(|| self.text.len() - "[registered]".len());
        let mut offset = 0;
        wrap_line(&self.text, width)
            .into_iter()
            .map(|row| {
                let start = offset;
                offset += row.len();
                let split =
                    tag_start.map_or(row.len(), |tag| tag.saturating_sub(start).min(row.len()));
                if split == row.len() {
                    Line::from(row)
                } else {
                    Line::from(vec![
                        Span::raw(row[..split].to_owned()),
                        Span::styled(row[split..].to_owned(), Style::default().fg(Color::Green)),
                    ])
                }
            })
            .collect()
    }
}

// Drop runs on errors and unwinding; the panic hook restores before printing diagnostics.
struct Screen;
impl Screen {
    fn enter() -> io::Result<Self> {
        let guard = Self;
        enable_raw_mode()?;
        execute!(io::stdout(), EnterAlternateScreen, EnableBracketedPaste)?;
        Ok(guard)
    }
}
fn restore() {
    let _ = disable_raw_mode();
    let _ = execute!(
        io::stdout(),
        DisableBracketedPaste,
        LeaveAlternateScreen,
        crossterm::cursor::Show
    );
}
impl Drop for Screen {
    fn drop(&mut self) {
        restore();
    }
}

#[derive(Default, PartialEq, Eq)]
enum View {
    #[default]
    Logs,
    Output,
    Status,
    Players,
    World,
    Gateway,
}

#[derive(Default)]
struct Model {
    lines: VecDeque<LogRecord>,
    output: VecDeque<LogRecord>,
    view: View,
    generation: u64,
    cached: Option<(usize, u64, Vec<ratatui::text::Line<'static>>)>,
    input: Input,
    completions: String,
    history: VecDeque<String>,
    history_index: Option<usize>,
    draft: String,
    scroll: usize,
    snapshot: crate::dashboard::StatusSnapshot,
    players: crate::players::Snapshot,
    world: crate::world_dashboard::Snapshot,
    gateway: crate::gateway_dashboard::Snapshot,
}
impl Model {
    fn log(&mut self, message: &str) {
        self.record(LogRecord::plain(message.to_owned()));
    }
    fn record(&mut self, message: LogRecord) {
        // Bound individual lines as well as retained line count; strip terminal controls.
        for line in message.text.lines().take(LIMIT) {
            let text: String = line
                .chars()
                .filter(|c| !c.is_control())
                .take(8192)
                .collect();
            let registered = message.registered && text.ends_with("[registered]");
            self.lines.push_back(LogRecord { text, registered });
            if self.lines.len() > LIMIT {
                self.lines.pop_front();
            }
        }
        self.generation = self.generation.wrapping_add(1);
        if self.view == View::Logs {
            self.scroll = 0;
        }
    }
    fn clear(&mut self) {
        self.view = View::Output;
        self.output.clear();
        self.scroll = 0;
        self.cached = None;
    }
    fn output(&mut self, message: &str) {
        self.log(message);
        self.output.extend(
            message
                .lines()
                .take(LIMIT)
                .map(|line| {
                    line.chars()
                        .filter(|c| !c.is_control())
                        .take(8192)
                        .collect::<String>()
                })
                .map(LogRecord::plain),
        );
        while self.output.len() > LIMIT {
            self.output.pop_front();
        }
        self.cached = None;
    }
    fn refresh(&mut self, snapshot: crate::dashboard::StatusSnapshot) {
        self.snapshot = snapshot;
    }
    fn rows(&mut self, width: usize) -> &[ratatui::text::Line<'static>] {
        let generation = if self.view == View::Logs {
            self.generation
        } else {
            0
        };
        if !self
            .cached
            .as_ref()
            .is_some_and(|(w, g, _)| *w == width && *g == generation)
        {
            let lines = if self.view == View::Logs {
                &self.lines
            } else {
                &self.output
            };
            self.cached = Some((
                width,
                generation,
                lines.iter().flat_map(|line| line.wrapped(width)).collect(),
            ));
        }
        &self.cached.as_ref().unwrap().2
    }
    fn submit(&mut self) -> String {
        let line = self.input.value().trim().to_owned();
        if !line.is_empty() {
            if self.history.back() != Some(&line) {
                self.history.push_back(line.clone());
                if self.history.len() > LIMIT {
                    self.history.pop_front();
                }
            }
            self.log(&format!("rusty-mines> {line}"));
        }
        self.input.reset();
        self.completions.clear();
        self.history_index = None;
        self.draft.clear();
        line
    }
    fn history(&mut self, older: bool) {
        if !older && self.history_index.is_none() {
            return;
        }
        if older {
            if self.history.is_empty() {
                return;
            }
            self.history_index = Some(match self.history_index {
                None => {
                    self.draft = self.input.value().to_owned();
                    self.history.len() - 1
                }
                Some(index) => index.saturating_sub(1),
            });
        } else {
            self.history_index = self
                .history_index
                .and_then(|index| (index + 1 < self.history.len()).then_some(index + 1));
        }
        self.input = Input::new(
            self.history_index
                .map_or_else(|| self.draft.clone(), |index| self.history[index].clone()),
        );
    }
    fn complete(&mut self) {
        let matches: Vec<_> = COMMANDS
            .iter()
            .filter(|command| command.starts_with(self.input.value()))
            .collect();
        self.completions.clear();
        if matches.len() == 1 {
            self.input = Input::new(matches[0].to_string());
        } else if !matches.is_empty() {
            self.completions = matches.into_iter().copied().collect::<Vec<_>>().join("  ");
            self.log(&self.completions.clone());
        }
    }
}

pub struct Console {
    terminal: Terminal<CrosstermBackend<io::Stdout>>,
    screen: Option<Screen>,
    model: Model,
    receiver: Receiver<LogRecord>,
}
impl Console {
    pub fn new(receiver: Receiver<LogRecord>) -> io::Result<Self> {
        let previous = std::panic::take_hook();
        std::panic::set_hook(Box::new(move |info| {
            restore();
            previous(info);
        }));
        let screen = Screen::enter()?;
        let terminal = Terminal::new(CrosstermBackend::new(io::stdout()))?;
        Ok(Self {
            terminal,
            screen: Some(screen),
            model: Model::default(),
            receiver,
        })
    }
    pub fn log(&mut self, message: &str) {
        self.model.output(message);
    }
    pub fn clear(&mut self) {
        self.model.clear();
    }
    pub fn status(&mut self, snapshot: crate::dashboard::StatusSnapshot) {
        self.model.view = View::Status;
        self.model.log(&snapshot.plaintext());
        self.model.refresh(snapshot);
    }
    pub fn players(&mut self, snapshot: crate::players::Snapshot) {
        self.model.view = View::Players;
        self.model.log(&snapshot.plaintext());
        self.model.players = snapshot;
    }
    pub fn world(&mut self, snapshot: crate::world_dashboard::Snapshot) {
        self.model.view = View::World;
        self.model.log(&snapshot.plaintext());
        self.model.world = snapshot;
    }
    pub fn gateway(&mut self, snapshot: crate::gateway_dashboard::Snapshot) {
        self.model.view = View::Gateway;
        self.model.log(&snapshot.plaintext());
        self.model.gateway = snapshot;
    }
    pub fn logs(&mut self) {
        self.model.view = View::Logs;
        self.model.scroll = 0;
        self.model.cached = None;
    }
    pub fn suspend<T>(&mut self, action: impl FnOnce() -> T) -> io::Result<T> {
        self.screen.take();
        execute!(
            io::stdout(),
            crossterm::terminal::Clear(crossterm::terminal::ClearType::All),
            crossterm::cursor::MoveTo(0, 0)
        )?;
        let result = action();
        self.screen = Some(Screen::enter()?);
        self.terminal.clear()?;
        Ok(result)
    }
    pub fn draw(&mut self) -> io::Result<()> {
        let model = &mut self.model;
        self.terminal.draw(|frame| render(frame, model))?;
        Ok(())
    }
    pub fn read_command(
        &mut self,
        mut snapshot: impl FnMut() -> crate::dashboard::StatusSnapshot,
        mut players: impl FnMut() -> crate::players::Snapshot,
        mut gateway: impl FnMut(crate::players::Snapshot) -> crate::gateway_dashboard::Snapshot,
    ) -> io::Result<Option<String>> {
        loop {
            // Limit work per frame so a busy producer cannot starve keyboard/rendering.
            for _ in 0..1024 {
                match self.receiver.try_recv() {
                    Ok(line) => self.model.record(line),
                    Err(_) => break,
                }
            }
            let dropped = crate::take_dropped_logs();
            if dropped > 0 {
                self.model.log(&format!(
                    "[Console queue full: dropped {dropped} background messages]"
                ));
            }
            if self.model.view == View::Status {
                self.model.refresh(snapshot());
            }
            match self.model.view {
                View::Players => self.model.players = players(),
                View::World => self.model.world.players = players(),
                View::Gateway => self.model.gateway = gateway(players()),
                _ => {}
            }
            self.draw()?;
            if !event::poll(Duration::from_millis(50))? {
                continue;
            }
            let event = event::read()?;
            if matches!(&event, Event::Key(key) if key.code != KeyCode::Tab && key.kind != KeyEventKind::Release)
                || matches!(&event, Event::Paste(_))
            {
                self.model.completions.clear();
            }
            match &event {
                Event::Key(key) if key.kind == KeyEventKind::Release => {}
                Event::Key(key) => match key.code {
                    KeyCode::Char('c' | 'd') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                        return Ok(None)
                    }
                    KeyCode::Enter => return Ok(Some(self.model.submit())),
                    KeyCode::Up => self.model.history(true),
                    KeyCode::Down => self.model.history(false),
                    KeyCode::Tab => self.model.complete(),
                    KeyCode::PageUp => self.model.scroll = self.model.scroll.saturating_add(10),
                    KeyCode::PageDown => self.model.scroll = self.model.scroll.saturating_sub(10),
                    KeyCode::End if key.modifiers.contains(KeyModifiers::CONTROL) => {
                        self.model.scroll = 0
                    }
                    _ => {
                        self.model.input.handle_event(&event);
                    }
                },
                Event::Paste(text) => {
                    for c in text.chars().filter(|c| !c.is_control()).take(8192) {
                        self.model
                            .input
                            .handle(tui_input::InputRequest::InsertChar(c));
                    }
                }
                _ => {}
            }
        }
    }
}

fn render(frame: &mut ratatui::Frame, model: &mut Model) {
    let areas = Layout::vertical([
        Constraint::Min(0),
        Constraint::Length(1),
        Constraint::Length(1),
    ])
    .split(frame.area());
    let dashboard = matches!(
        model.view,
        View::Status | View::Players | View::World | View::Gateway
    );
    let panels = Layout::vertical([Constraint::Min(0), Constraint::Length(u16::from(dashboard))])
        .split(areas[0]);
    let content = panels[0];
    if model.view == View::Status {
        crate::dashboard::render(frame, content, &model.snapshot);
    } else if model.view == View::Players {
        crate::players::render(frame, content, &model.players);
    } else if model.view == View::World {
        crate::world_dashboard::render(frame, content, &model.world);
    } else if model.view == View::Gateway {
        crate::gateway_dashboard::render(frame, content, &model.gateway);
    } else {
        let width = areas[0].width.max(1) as usize;
        let height = areas[0].height as usize;
        let count = model.rows(width).len();
        model.scroll = model.scroll.min(count.saturating_sub(height));
        let end = count.saturating_sub(model.scroll);
        let start = end.saturating_sub(height);
        frame.render_widget(
            Paragraph::new(model.rows(width)[start..end].to_vec()),
            areas[0],
        );
    }
    if dashboard {
        frame.render_widget(
            Paragraph::new("help · status · players · world · gateway · logs")
                .style(ratatui::style::Style::default().fg(ratatui::style::Color::DarkGray)),
            panels[1],
        );
    }
    frame.render_widget(Paragraph::new(model.completions.as_str()), areas[1]);
    let prefix = if areas[2].width > 14 {
        "rusty-mines> "
    } else {
        "> "
    };
    let available = areas[2].width.saturating_sub(prefix.len() as u16).max(1) as usize;
    let scroll = model.input.visual_scroll(available);
    let input_area = ratatui::layout::Rect {
        x: areas[2].x + (prefix.len() as u16).min(areas[2].width),
        width: areas[2].width.saturating_sub(prefix.len() as u16),
        ..areas[2]
    };
    frame.render_widget(Paragraph::new(prefix).block(Block::default()), areas[2]);
    frame.render_widget(
        Paragraph::new(model.input.value()).scroll((0, scroll.min(u16::MAX as usize) as u16)),
        input_area,
    );
    if input_area.width > 0 && input_area.height > 0 {
        frame.set_cursor_position((
            input_area.x
                + model
                    .input
                    .visual_cursor()
                    .saturating_sub(scroll)
                    .min(input_area.width.saturating_sub(1) as usize) as u16,
            input_area.y,
        ));
    }
}

fn wrap_line(line: &str, width: usize) -> Vec<String> {
    use ratatui::text::Line;
    use unicode_segmentation::UnicodeSegmentation;
    let mut rows = vec![String::new()];
    let mut used = 0;
    for grapheme in line.graphemes(true) {
        let size = Line::from(grapheme).width();
        if used + size > width && used > 0 {
            rows.push(String::new());
            used = 0;
        }
        rows.last_mut().unwrap().push_str(grapheme);
        used += size;
    }
    rows
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn registered_tag_survives_wrapping_cache_redraw_and_views() {
        use ratatui::{backend::TestBackend, style::Color};
        let mut model = Model::default();
        let record = LogRecord::gateway("c200界identity", true);
        model.record(record.clone());
        model.clear();
        model.output("command output");
        assert_eq!(model.rows(80)[0].to_string(), "command output");
        model.view = View::Logs;
        model.cached = None;
        for width in [80, 7, 1, 40] {
            let rows = model.rows(width).to_vec();
            assert_eq!(
                rows.iter().map(ToString::to_string).collect::<String>(),
                format!("{}command output", record.text)
            );
            assert_eq!(model.rows(width), rows.as_slice());
            let mut terminal = Terminal::new(TestBackend::new(width as u16, 100)).unwrap();
            for _ in 0..2 {
                terminal.draw(|frame| render(frame, &mut model)).unwrap();
                let green: String = terminal
                    .backend()
                    .buffer()
                    .content
                    .iter()
                    .filter(|cell| cell.fg == Color::Green)
                    .map(|cell| cell.symbol())
                    .collect();
                assert_eq!(green, "[registered]");
            }
        }
    }

    #[test]
    fn unregistered_and_plain_messages_do_not_gain_green_tags() {
        use ratatui::style::Color;
        for record in [
            LogRecord::gateway("c200", false),
            LogRecord::plain("[registered]\u{1b}".into()),
        ] {
            let mut model = Model::default();
            model.record(record);
            assert!(model
                .rows(5)
                .iter()
                .flat_map(|row| &row.spans)
                .all(|span| span.style.fg != Some(Color::Green)));
            assert!(!model.lines[0].text.contains('\u{1b}'));
        }
    }
    #[test]
    fn logs_preserve_input_and_are_bounded() {
        let mut model = Model {
            input: Input::new("sta界".into()),
            ..Model::default()
        };
        for _ in 0..LIMIT + 10 {
            model.log("hello\nworld\u{1b}");
        }
        assert_eq!(model.lines.len(), LIMIT);
        assert_eq!(model.lines.back().unwrap().text, "world");
        assert_eq!(model.input.value(), "sta界");
    }
    #[test]
    fn history_restores_draft_and_completion() {
        let mut model = Model {
            input: Input::new("help".into()),
            ..Model::default()
        };
        assert_eq!(model.submit(), "help");
        model.input = Input::new("sta".into());
        model.history(false);
        assert_eq!(model.input.value(), "sta");
        model.history(true);
        assert_eq!(model.input.value(), "help");
        model.history(false);
        assert_eq!(model.input.value(), "sta");
        model.complete();
        assert_eq!(model.input.value(), "status");
    }
    #[test]
    fn unicode_wrapping_and_empty_lines() {
        assert_eq!(wrap_line("a界b", 3), vec!["a界", "b"]);
        assert_eq!(wrap_line("", 1), vec![""]);
        assert_eq!(wrap_line("e\u{301}x", 1), vec!["e\u{301}", "x"]);
        assert_eq!(wrap_line("👩‍💻x", 2), vec!["👩‍💻", "x"]);
    }
    #[test]
    fn resize_reflows_logs_without_mutating_input() {
        let model = Model {
            input: Input::new("界draft".into()),
            ..Model::default()
        };
        let cursor = model.input.cursor();
        for width in [80, 4, 1, 120] {
            let rows = wrap_line("a界b", width);
            assert_eq!(rows.concat(), "a界b");
            let areas = Layout::vertical([Constraint::Min(0), Constraint::Length(1)])
                .split(ratatui::layout::Rect::new(0, 0, width as u16, 10));
            assert_eq!(areas[1].y, 9);
            assert_eq!(model.input.value(), "界draft");
            assert_eq!(model.input.cursor(), cursor);
        }
    }
    #[test]
    fn switching_views_retains_logs_and_follows_new_messages() {
        let mut model = Model::default();
        model.log("background");
        model.clear();
        model.output("help result");
        assert_eq!(model.rows(80), &[ratatui::text::Line::from("help result")]);
        model.clear();
        assert!(model.rows(80).is_empty());
        model.view = View::Logs;
        model.cached = None;
        assert_eq!(
            model
                .rows(80)
                .iter()
                .map(ToString::to_string)
                .collect::<Vec<_>>(),
            ["background", "help result"]
        );
        model.scroll = 10;
        model.log("new message");
        assert_eq!(model.scroll, 0);
        assert_eq!(model.rows(80).last().unwrap().to_string(), "new message");
    }
    #[test]
    fn dashboard_refresh_preserves_input_history_and_does_not_log_ticks() {
        let mut model = Model {
            input: Input::new("status".into()),
            ..Model::default()
        };
        model.submit();
        model.clear();
        model.view = View::Status;
        model.input = Input::new("sta界".into());
        let cursor = model.input.cursor();
        let count = model.lines.len();
        for n in 0..20 {
            model.refresh(crate::dashboard::StatusSnapshot {
                sockets: n,
                ..Default::default()
            });
            assert_eq!(model.snapshot.sockets, n);
        }
        model.log("background while status");
        assert_eq!(model.snapshot.sockets, 19);
        assert_eq!(model.input.value(), "sta界");
        assert_eq!(model.input.cursor(), cursor);
        assert_eq!(model.lines.len(), count + 1);
        model.history(true);
        assert_eq!(model.input.value(), "status");
        model.input = Input::new("st".into());
        model.complete();
        model.refresh(crate::dashboard::StatusSnapshot {
            sockets: 20,
            ..Default::default()
        });
        assert_eq!(model.input.value(), "st");
        assert_eq!(model.completions, "status  stop");
    }
    #[test]
    fn dashboard_diff_render_keeps_rows_and_prompt_stable() {
        use ratatui::backend::TestBackend;
        let mut terminal = Terminal::new(TestBackend::new(100, 26)).unwrap();
        let mut model = Model {
            view: View::Status,
            input: Input::new("sta界".into()),
            ..Model::default()
        };
        model.refresh(crate::dashboard::StatusSnapshot {
            sockets: 1,
            gateway: "unauthorized",
            host: "very-long-host.example/".repeat(20),
            ..Default::default()
        });
        terminal.draw(|frame| render(frame, &mut model)).unwrap();
        let before = terminal.backend().buffer().clone();
        model.snapshot.sockets = 999999;
        model.snapshot.traffic.rx_bytes = u64::MAX;
        model.snapshot.process.cpu_percent = Some(100.0);
        model.log("background event");
        terminal.draw(|frame| render(frame, &mut model)).unwrap();
        let after = terminal.backend().buffer();
        assert_ne!(before, *after);
        for y in 1..8 {
            for x in 1..13 {
                assert_eq!(before[(x, y)], after[(x, y)]);
            }
        }
        for x in 0..100 {
            assert_eq!(before[(x, 25)], after[(x, 25)]);
        }
        for (width, height) in [(40, 26), (1, 1), (20, 5), (100, 26)] {
            terminal.backend_mut().resize(width, height);
            terminal
                .resize(ratatui::layout::Rect::new(0, 0, width, height))
                .unwrap();
            terminal.draw(|frame| render(frame, &mut model)).unwrap();
            assert_eq!(model.input.value(), "sta界");
        }
        assert_eq!(model.input.value(), "sta界");
    }
    #[test]
    fn players_refresh_resize_and_view_switch_preserve_prompt_and_logs() {
        use crate::{
            module_bindings::SessionPhase,
            players::{Player, Snapshot},
        };
        let mut terminal = Terminal::new(ratatui::backend::TestBackend::new(100, 20)).unwrap();
        let mut model = Model {
            view: View::Players,
            input: Input::new("kick界".into()),
            ..Default::default()
        };
        model.log("retained before players");
        let cursor = model.input.cursor();
        let id = uuid::Uuid::new_v4();
        for phase in [
            SessionPhase::Login,
            SessionPhase::Configuration,
            SessionPhase::Play,
        ] {
            model.players = Snapshot {
                rows: vec![Player {
                    id,
                    username: "Alex".into(),
                    phase,
                    connected_secs: 42,
                    latency: None,
                }],
                excluded: 2,
            };
            terminal.draw(|frame| render(frame, &mut model)).unwrap();
            let buffer = terminal.backend().buffer();
            let text = buffer
                .content()
                .iter()
                .map(|c| c.symbol())
                .collect::<String>();
            assert!(text.contains("Alex"));
            assert!(text.contains(&format!("{phase:?}")));
            assert!(text.contains("42s"));
            assert!(text.contains("—"));
        }
        model.players.rows[0].latency = Some(Duration::from_millis(25));
        terminal.draw(|frame| render(frame, &mut model)).unwrap();
        assert!(terminal
            .backend()
            .buffer()
            .content()
            .iter()
            .map(|c| c.symbol())
            .collect::<String>()
            .contains("25.0 ms"));
        for (width, height) in [(55, 20), (20, 5), (1, 1), (100, 20)] {
            terminal.backend_mut().resize(width, height);
            terminal
                .resize(ratatui::layout::Rect::new(0, 0, width, height))
                .unwrap();
            terminal.draw(|frame| render(frame, &mut model)).unwrap();
            assert_eq!(model.input.value(), "kick界");
            assert_eq!(model.input.cursor(), cursor);
        }
        assert_eq!(model.lines.len(), 1);
        model.view = View::Logs;
        assert!(model.rows(100)[0]
            .to_string()
            .contains("retained before players"));
    }

    #[test]
    fn world_gateway_live_resize_preserves_prompt_and_history() {
        use ratatui::{backend::TestBackend, style::Color};
        let mut terminal = Terminal::new(TestBackend::new(110, 28)).unwrap();
        let mut model = Model {
            input: Input::new("draft界😀".into()),
            ..Default::default()
        };
        model.log("retained");
        let cursor = model.input.cursor();
        for view in [View::World, View::Gateway] {
            model.view = view;
            model.gateway.health = "authorized";
            model.gateway.registered = Some(true);
            for (width, height) in [(110, 28), (55, 28), (20, 5), (1, 1), (110, 28)] {
                terminal.backend_mut().resize(width, height);
                terminal
                    .resize(ratatui::layout::Rect::new(0, 0, width, height))
                    .unwrap();
                terminal.draw(|frame| render(frame, &mut model)).unwrap();
                assert_eq!(model.input.value(), "draft界😀");
                assert_eq!(model.input.cursor(), cursor);
                assert_eq!(model.lines.len(), 1);
                if width >= 55 {
                    let text = terminal
                        .backend()
                        .buffer()
                        .content()
                        .iter()
                        .map(|c| c.symbol())
                        .collect::<String>();
                    assert!(text.contains("help · status · players · world · gateway · logs"));
                    if model.view == View::World {
                        assert!(text.contains("minecraft:overworld"));
                        assert!(text.contains("25; x/z -2..=2"));
                    } else {
                        assert!(text.contains("[registered]"));
                        assert!(terminal
                            .backend()
                            .buffer()
                            .content()
                            .iter()
                            .any(|c| c.symbol() == "[" && c.fg == Color::Green));
                    }
                }
            }
            let before = terminal.backend().buffer().clone();
            model.gateway.registered = None;
            model.gateway.health = "disconnected";
            let player = crate::players::Player {
                id: uuid::Uuid::nil(),
                username: "Alex".into(),
                phase: crate::module_bindings::SessionPhase::Play,
                connected_secs: 1,
                latency: None,
            };
            model.world.players.rows.push(player.clone());
            model.gateway.players.rows.push(player);
            terminal.draw(|frame| render(frame, &mut model)).unwrap();
            assert_ne!(before, *terminal.backend().buffer());
            assert_eq!(model.lines.len(), 1);
        }
        for (prefix, command) in [("wo", "world"), ("ga", "gateway")] {
            model.input = Input::new(prefix.into());
            model.complete();
            assert_eq!(model.input.value(), command);
        }
        assert!(model
            .gateway
            .plaintext()
            .contains("unavailable (awaiting connected subscription)"));
        model.view = View::Logs;
        assert!(model.rows(110)[0].to_string().contains("retained"));
    }

    #[test]
    fn editing_unicode_does_not_split_utf8() {
        let mut input = Input::new("a界".into());
        input.handle(tui_input::InputRequest::DeletePrevChar);
        assert_eq!(input.value(), "a");
    }
}
