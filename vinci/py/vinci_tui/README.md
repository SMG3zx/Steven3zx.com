# Vinci TUI - Full Text User Interface

A rich terminal-based interface for interacting with Vinci's knowledge system using natural language.

## Features

- **Natural Language Input**: Type in plain English instead of remembering commands
- **Visual Pipeline Status**: See your ideas organized by stage (Seed → Prepare → Incubate → Build → Tend)
- **Idea Cards**: Visual cards showing idea details and status
- **Smart Suggestions**: Get suggestions for next steps based on your input
- **Rich Terminal UI**: Colors, panels, and formatted output

## Installation

```bash
cd vinci/mojo/py/vinci_tui
pip install -r requirements.txt
```

## Usage

### Basic Usage

```bash
python -m vinci_tui
```

### With Custom Journal Path

```bash
python -m vinci_tui --journal ~/.vinci/journal.jsonl
```

### Demo Mode

```bash
python -m vinci_tui --demo
```

## Natural Language Examples

### Creating Ideas

```
> create a new project to build a website
[✓] Would you like me to:
    [ ] Create as Seed idea?
    [ ] Set goal metric?
    [ ] Add initial requirements?
```

### Setting Goals

```
> set goal for my-website-project to have 1000 visitors per month
```

### Advancing Stages

```
> move my-website-project to build stage
```

### Searching/Recalling

```
> show me what I said about Rust programming
> recall when I started learning Python
```

### Recording Facts

```
> I remember that the best time for deep work is morning hours
> note down: Website should use React framework
```

## Shortcuts

- `/help` - Show help menu
- `/exit` or `/q` - Quit application
- `/pipeline` - Show pipeline status

## Planned Features

- [ ] Integration with existing Vinci CLI commands
- [ ] Real-time journal/index updates
- [ ] Keyboard navigation (arrow keys, j/k for scrolling)
- [ ] Interactive idea editing
- [ ] Chart visualization for metrics
- [ ] Multi-panel layout (split view for ideas and chat)
- [ ] Export/import configurations
- [ ] Theme customization

## Architecture

```
vinci_tui/
├── app.py           # Main application logic
├── parser.py        # Natural language intent parsing
├── widgets.py       # Rich UI components
├── __main__.py      # Entry point
└── requirements.txt # Dependencies
```

## Development

### Running Tests

```bash
pytest tests/
```

### Adding New Widgets

Edit `widgets.py` to add new visual components.

### Extending Parser

Add new patterns to `parser.py`:

```python
PATTERNS = {
    "new_action": [
        r"pattern1",
        r"pattern2",
    ],
}
```

## License

MIT License - See LICENSE file
