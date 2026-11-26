# Vinci TUI - Full Text User Interface

A comprehensive terminal-based interface for interacting with Vinci's knowledge management system using natural language.

## 🎯 What is Vinci TUI?

Vinci TUI transforms your knowledge management into a rich, visual, natural-language experience. Instead of remembering commands like `vinci idea`, `vinci goal`, etc., you simply type in plain English:

```
> create a new project to build a website
> set goal for my-website to have 1000 visitors per month
> move my-website to prepare stage
> show me what I said about Rust programming
```

## ✨ Features

### Natural Language Understanding
- **Intent Detection**: Automatically detects what you want to do
- **Entity Extraction**: Pulls out idea titles, metrics, and other details
- **Smart Suggestions**: Recommends next steps based on context
- **Fallback to Chat**: Complex queries go to the chat engine

### Visual Interface
- **Pipeline Visualization**: See your ideas across all stages at a glance
- **Idea Cards**: Beautiful cards showing idea details and status
- **Real-time Updates**: Status updates as you work
- **Rich Formatting**: Colors, icons, and structured layout

### Keyboard Navigation
- Full keyboard control (arrow keys, j/k navigation)
- Command menu with hotkeys
- Multi-line input support
- Accessible design

### CLI Integration
- Bridges to existing Vinci CLI commands
- Works with installed Vinci binary
- Falls back to demo mode if not installed

## 🚀 Quick Start

### Installation

```bash
cd vinci/mojo/py/vinci_tui
pip install -r requirements.txt
python -m vinci_tui
```

### Demo Mode (No Installation Required)

```bash
python -m vinci_tui --demo
```

## 📖 Usage Examples

### Creating Ideas

```bash
> create a new project to build a website

[✓] Would you like me to:
    [ ] Create as Seed idea?
    [ ] Set goal metric?
    [ ] Add initial requirements?
```

### Setting Goals

```bash
> set goal for my-website-project to have 1000 visitors per month

[✓] Goal set for my-website-project
```

### Advancing Pipeline Stages

```bash
> move my-website-project to prepare stage

[✓] Advanced my-website-project to Prepare
```

### Searching/Recalling

```bash
> show me what I said about Rust programming

[Recalling from index...]

[i12] The person started learning Rust on 2026-11-20
[i13] Rust's ownership model is confusing but powerful
[d5/1] Read: "The Rust Programming Language" (read 2026-11-21)
```

### Recording Facts

```bash
> I remember that the best time for deep work is morning hours

[✓] Fact recorded
```

## 🎨 Visual Interface

### Pipeline Status

```
╔════════════════════════════════════════╗
║     Vinci Knowledge Partner TUI        ║
╠════════════════════════════════════════╣
║  📊 Pipeline Status                    ║
║                                       ║
║  🌱 Seed          [████░░] 5         ║
║  📝 Prepare       [██████░] 8         ║
║  🔬 Incubate      [███████] 12        ║
║  🔨 Build         [██░░░░] 3          ║
║  🌿 Tend          [░░░░░░] 2          ║
║  🏳️ Retired       [████░░] 4          ║
╚════════════════════════════════════════╝
```

### Idea Cards

```
┌───────────────────────────────────────┐
│ Idea i1                               │
├───────────────────────────────────────┤
│ 🌱 Build a website for my portfolio   │
│                                       │
│ Stage: 🌱 Seed                        │
│ Created: 2026-11-23                   │
│ For:                                  │
│ Goal:                                 │
└───────────────────────────────────────┘
```

## ⌨️ Keyboard Shortcuts

### Navigation
- `↑` / `k` - Move up
- `↓` / `j` - Move down
- `Enter` - Select/confirm
- `Esc` - Cancel/back
- `Tab` - Next field
- `Shift+Tab` - Previous field

### Commands
- `/` - Open command menu
- `h` - Show help
- `q` - Quit
- `p` - Pipeline status
- `n` - New idea
- `r` - Recall/search

### Editing
- `Ctrl+c` - Cancel input
- `Ctrl+d` - Exit
- `Backspace` - Delete character
- `Delete` - Delete forward

## 🏗️ Architecture

```
vinci_tui/
├── app.py              # Main application logic
├── parser.py           # Natural language intent parsing
├── widgets.py          # Rich UI components
├── cli_integration.py  # Bridge to Vinci CLI
├── demo.py             # Demo mode simulator
├── requirements.txt    # Dependencies
├── setup.py            # Installation script
└── README.md           # This file
```

### Components

1. **IntentParser** (`parser.py`)
   - Parses natural language into structured intent
   - Extracts entities (idea IDs, metrics, etc.)
   - Generates suggestions

2. **Widgets** (`widgets.py`)
   - Rich UI components
   - Pipeline visualization
   - Idea cards
   - Input fields

3. **CLIBridge** (`cli_integration.py`)
   - Executes Vinci CLI commands
   - Handles errors and timeouts
   - Checks installation status

4. **VinciTUI** (`app.py`)
   - Main application loop
   - State management
   - Layout rendering

## 🔧 Development

### Requirements

- Python 3.10+
- Rich library (>=13.0.0)
- Click library (>=8.0.0)

### Running Tests

```bash
pytest tests/
```

### Adding New Features

1. **Add parser patterns** in `parser.py`:

```python
PATTERNS = {
    "new_action": [
        r"pattern to match",
    ],
}
```

2. **Add new widget** in `widgets.py`:

```python
from rich.panel import Panel
from rich.text import Text

class NewWidget:
    def render(self, data):
        content = Text(str(data))
        return Panel(content)
```

3. **Update app** in `app.py` to use new widget

## 📊 Planned Features

- [x] Natural language parsing
- [x] Pipeline visualization
- [x] Idea cards
- [x] Keyboard navigation
- [ ] Real-time journal/index updates
- [ ] Interactive idea editing
- [ ] Chart visualization for metrics
- [ ] Multi-panel layout (split view)
- [ ] Export/import configurations
- [ ] Theme customization
- [ ] Mouse support
- [ ] Drag and drop
- [ ] Command history navigation
- [ ] Multi-select operations

## 🐛 Known Limitations

1. **CLI Integration**: Requires Vinci to be installed in PATH
2. **Demo Mode**: Simulated responses, not real Vinci data
3. **Keyboard Navigation**: Basic implementation, needs enhancement
4. **Multi-line Input**: Not fully implemented yet

## 📝 Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Submit a pull request

## 📄 License

MIT License - See LICENSE file

## 🤝 Support

For issues and questions:
- Open an issue on GitHub
- Check existing documentation
- Review the code comments

## 🙏 Acknowledgments

- Built with [Rich](https://github.com/Textualize/rich) for terminal UI
- Inspired by Vinci's philosophy of natural language interaction
- Designed for knowledge workers who prefer text interfaces
