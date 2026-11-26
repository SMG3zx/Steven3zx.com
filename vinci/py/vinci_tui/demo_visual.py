#!/usr/bin/env python3
"""
Visual Demo for Vinci TUI.
Shows the interface layout without requiring user interaction.
"""

import os


class Color:
    """ANSI color codes."""
    RESET = "\033[0m"
    BOLD = "\033[1m"
    DIM = "\033[2m"
    BLUE = "\033[34m"
    GREEN = "\033[32m"
    RED = "\033[31m"
    YELLOW = "\033[33m"
    CYAN = "\033[36m"
    MAGENTA = "\033[35m"
    WHITE = "\033[37m"


def show_visual_demo():
    """Show visual demo of the TUI."""
    
    # Title screen
    print(f"\n{'='*80}")
    print(f"{Color.MAGENTA}{Color.BOLD}╔══════════════════════════════════════════════════════════════╗{Color.RESET}")
    print(f"║     Vinci Knowledge Partner TUI                              ║")
    print(f"╠══════════════════════════════════════════════════════════════╣")
    print(f"║  A natural language interface for your knowledge            ║")
    print(f"╚══════════════════════════════════════════════════════════════╝{Color.RESET}")
    
    # Pipeline section
    print(f"\n{Color.CYAN}{Color.BOLD}📊 Pipeline Status{Color.RESET}\n")
    
    data = {
        "seed": 5,
        "prepare": 8,
        "incubate": 12,
        "build": 3,
        "tend": 2,
        "retired": 4,
    }
    
    icons = {
        "seed": "🌱",
        "prepare": "📝",
        "incubate": "🔬",
        "build": "🔨",
        "tend": "🌿",
        "retired": "🏳️",
    }
    
    for stage, count in data.items():
        filled = "█" * min(count, 5)
        empty = "░" * max(0, 5 - count)
        bar = f"[{filled}{empty}]"
        print(f"  {icons[stage]} {stage.capitalize():12} [{bar}] {count}")
    
    # Ideas section
    print(f"\n{'='*80}")
    print(f"{Color.CYAN}{Color.BOLD}💡 Ideas in Progress{Color.RESET}\n")
    
    ideas = [
        {
            "id": "i1",
            "title": "Build a website for my portfolio",
            "stage": "seed",
            "created": "2026-11-23",
        },
        {
            "id": "i2",
            "title": "Learn Rust programming",
            "stage": "prepare",
            "created": "2026-11-20",
            "for": "Personal skill development",
        },
    ]
    
    for idea in ideas:
        print(f"{Color.GREEN}{Color.BOLD}┌───────────────────────────────────────────────{Color.RESET}")
        print(f"│ {idea['id']:5} - {idea['title']}")
        print(f"│                                               ")
        icon = icons[idea['stage']]
        print(f"│ {Color.DIM}Stage:{Color.RESET} {icon} {idea['stage'].capitalize()}")
        if 'for' in idea:
            print(f"│ {Color.DIM}For:{Color.RESET} {idea['for']}")
        if 'created' in idea:
            print(f"│ {Color.DIM}Created:{Color.RESET} {idea['created']}")
        print(f"{Color.GREEN}{Color.BOLD}└───────────────────────────────────────────────{Color.RESET}")
    
    # Suggestions example
    print(f"\n{'='*80}")
    print(f"{Color.CYAN}{Color.BOLD}💬 Example: 'create a new project to build a website'{Color.RESET}\n")
    print(f"  {Color.GREEN}✓ Success! Created seed idea[/]\n")
    print(f"  {Color.DIM}Suggestions:{Color.RESET}")
    print(f"    1. Set a goal for this idea")
    print(f"    2. Define who it's for")
    print(f"    3. Add initial requirements")
    
    # Command examples
    print(f"\n{'='*80}")
    print(f"{Color.CYAN}{Color.BOLD}📝 Try These Commands{Color.RESET}\n")
    
    examples = [
        "create a new project to build a website",
        "set goal for my-website to have 1000 visitors per month",
        "move my-website to prepare stage",
        "show me what I said about Rust programming",
        "I remember that mornings are best for coding",
    ]
    
    for i, example in enumerate(examples, 1):
        print(f"  {i}. {example}")
    
    # Keyboard shortcuts
    print(f"\n{'='*80}")
    print(f"{Color.CYAN}{Color.BOLD}⌨️  Keyboard Shortcuts{Color.RESET}\n")
    
    shortcuts = [
        ("↑/k", "Move up in list"),
        ("↓/j", "Move down in list"),
        ("Enter", "Select/confirm"),
        ("Esc", "Cancel/back"),
        ("/", "Open command menu"),
        ("h", "Show help"),
        ("q", "Quit application"),
    ]
    
    for key, action in shortcuts:
        print(f"  {key}: {action}")
    
    # Footer
    print(f"\n{'='*80}")
    print(f"{Color.GREEN}{Color.BOLD}Demo complete!{Color.RESET}\n")
    print("To run the full TUI with Rich library:")
    print("  pip install rich click")
    print("  python -m vinci_tui\n")


if __name__ == "__main__":
    show_visual_demo()
