# Installing Vinci TUI

## Prerequisites

- Python 3.10 or higher
- pip (Python package manager)
- Optional: Vinci CLI installed (`./install.sh`)

## Quick Install

```bash
cd vinci/mojo/py/vinci_tui
pip install -r requirements.txt
python -m vinci_tui
```

## Installation Steps

### 1. Clone or Navigate to Vinci Repository

```bash
cd /home/steven3zx/Steven3zx.com/vinci/mojo/py/vinci_tui
```

### 2. Install Dependencies

```bash
pip install rich>=13.0.0 click>=8.0.0
```

### 3. Run the TUI

```bash
python -m vinci_tui
```

## Demo Mode (No Installation Required)

If you want to see how it looks without installing dependencies:

```bash
python vinci_tui/demo_visual.py
```

This shows a visual representation of the interface.

## Running with Vinci CLI

For full functionality with actual Vinci integration:

### Ensure Vinci is Installed

```bash
cd /home/steven3zx/Steven3zx.com/vinci/mojo
./install.sh
```

This installs the `vinci` command to your PATH (usually `~/.local/bin`).

### Run TUI

```bash
python -m vinci_tui
```

## Custom Journal Path

To use a specific journal file:

```bash
python -m vinci_tui --journal ~/.vinci/journal.jsonl
```

Or set the environment variable:

```bash
export VINCI_HOME=~/my-knowledge
python -m vinci_tui
```

## Uninstall

```bash
pip uninstall vinci-tui
```

## Troubleshooting

### "ModuleNotFoundError: No module named 'rich'"

Install Rich library:
```bash
pip install rich click
```

### "Vinci CLI not found"

Make sure Vinci is installed and in your PATH:
```bash
which vinci
# Should show path to vinci executable

# If not, install it:
cd /home/steven3zx/Steven3zx.com/vinci/mojo
./install.sh
```

### Permission Denied

Make sure you're running with the right permissions:
```bash
chmod +x vinci_tui/*.py
```

## Development Mode

For developers working on the TUI:

```bash
pip install -e .
pytest tests/
```

## File Structure

```
vinci_tui/
├── __init__.py          # Package initialization
├── __main__.py          # Entry point for `python -m vinci_tui`
├── app.py               # Main application logic
├── parser.py            # Natural language intent parsing
├── cli_integration.py   # Bridge to Vinci CLI
├── widgets.py           # Rich UI components
├── demo_visual.py       # Visual demo (no deps)
├── demo_standalone.py   # Interactive demo
├── install.py           # Installation script
├── setup.py             # Setup script
├── requirements.txt     # Python dependencies
├── INSTALL.md           # This file
└── README.md            # Quick reference
```

## Next Steps

After installation:

1. Run `python -m vinci_tui` to start the TUI
2. Try natural language commands like:
   - "create a new project to build a website"
   - "show me what I said about Rust"
3. Explore keyboard shortcuts (press `/` for command menu)

## Support

For issues or questions:
- Check `FULL_README.md` for detailed documentation
- Review code comments
- Open an issue in the repository
