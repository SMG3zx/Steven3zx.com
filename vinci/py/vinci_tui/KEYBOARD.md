# Vinci TUI Keyboard Navigation

## Overview

The Vinci TUI supports keyboard navigation for efficient interaction.

## Default Keybindings

### Navigation

| Key | Action |
|-----|--------|
| `↑` / `k` | Move up in list |
| `↓` / `j` | Move down in list |
| `Enter` | Select/confirm |
| `Esc` | Cancel/back |
| `Tab` | Next field/input |
| `Shift+Tab` | Previous field/input |

### Actions

| Key | Action |
|-----|--------|
| `/` | Open command menu |
| `h` | Show help (when in input) |
| `q` | Quit application |
| `p` | Show pipeline status |
| `n` | New idea |
| `r` | Recall/search |

### Editing

| Key | Action |
|-----|--------|
| `Ctrl+c` | Cancel current input |
| `Ctrl+d` | Exit (EOF) |
| `Backspace` | Delete character |
| `Delete` | Delete forward |

## Usage Examples

### Navigating Idea Lists

```
> show all ideas

┌─────────────────────┐
│ 1. i1 - Website     │
│ 2. i2 - Rust Learn  │
└─────────────────────┘

[Use ↑/↓ or k/j to navigate, Enter to select]
```

### Multi-line Input

```
> create new project

Project title: [cursor here...]
[Press Ctrl+Enter to submit]
```

### Command Menu (press `/`)

```
Commands:
  c - Create idea
  s - Show ideas
  r - Recall/search
  p - Pipeline status
  h - Help
  q - Quit

[Use arrow keys to select, Enter to execute]
```

## Advanced Features (Planned)

- **Mouse support**: Click on list items
- **Drag and drop**: Reorder ideas
- **Hotkeys for shortcuts**: `Ctrl+n` for new idea
- **Search while typing**: Filter ideas as you type
- **Multi-select**: Ctrl+click to select multiple

## Accessibility

- All functionality available via keyboard
- Screen reader compatible (ARIA labels)
- High contrast mode support
- Customizable keybindings (future)
