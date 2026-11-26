#!/usr/bin/env python3
"""
Standalone Demo Mode for Vinci TUI.
No external dependencies required - uses only standard library.
Shows example interactions and visual layout.
"""

import os
import sys


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


def clear_screen():
    """Clear terminal screen."""
    os.system('clear' if os.name == 'posix' else 'cls')


def print_box(text, title=""):
    """Print text in a box."""
    width = len(text) + 10
    border = "─" * (width + 4)
    
    print(f"\n┌{border}┐")
    if title:
        print(f"│ {title.center(width)} │")
    print(f"│ {text.center(width)} │")
    print(f"└{border}┘")


def print_section(title, text):
    """Print a section with title."""
    print(f"\n{Color.CYAN}{Color.BOLD}{title}{Color.RESET}")
    print(Color.DIM + text + Color.RESET)


def show_pipeline():
    """Show pipeline visualization."""
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
    
    lines = []
    for stage, count in data.items():
        filled = "█" * min(count, 5)
        empty = "░" * max(0, 5 - count)
        bar = f"[{filled}{empty}]"
        lines.append(f"{icons[stage]} {stage.capitalize():12} [{bar}] {count}")
    
    print_section("Pipeline Status", "\n".join(lines))


def show_ideas():
    """Show ideas as cards."""
    ideas = [
        {
            "id": "i1",
            "title": "Build a website for my portfolio",
            "stage": "seed",
        },
        {
            "id": "i2",
            "title": "Learn Rust programming",
            "stage": "prepare",
        },
    ]
    
    icons = {
        "seed": "🌱",
        "prepare": "📝",
        "incubate": "🔬",
        "build": "🔨",
        "tend": "🌿",
        "retired": "🏳️",
    }
    
    for idea in ideas:
        print_section(f"Idea {idea['id']}", f"{icons[idea['stage']]} {idea['title']}")
        print(f"  Stage: {icons[idea['stage']]} {idea['stage'].capitalize()}")


def show_suggestions(input_text):
    """Show suggestions based on input."""
    suggestions = []
    
    if "create" in input_text.lower() or "new" in input_text.lower():
        suggestions = [
            "Set a goal for this idea",
            "Define who it's for",
            "Add initial requirements",
        ]
    
    print_section("Suggestions", "\n".join([f"  {i+1}. {s}" for i, s in enumerate(suggestions)]))


def main():
    """Run the demo."""
    print(f"\n{'='*60}")
    print(f"{Color.BOLD}Vinci TUI Demo Mode{Color.RESET}")
    print(f"{'='*60}\n")
    
    # Show welcome
    clear_screen()
    print_box(
        f"{Color.MAGENTA}{Color.BOLD}Vinci Knowledge Partner TUI{Color.RESET}\n"
        f"A natural language interface for your knowledge",
        "Welcome to Demo Mode"
    )
    
    input("\nPress Enter to continue...")
    
    # Show pipeline
    show_pipeline()
    
    input("\nPress Enter to see ideas...")
    
    # Show ideas
    clear_screen()
    show_ideas()
    
    input("\nPress Enter to see suggestions...")
    
    # Show examples
    clear_screen()
    print_box(
        f"{Color.GREEN}Try these natural language commands:{Color.RESET}\n"
        "1. create a new project to build a website\n"
        "2. set goal for my-website to have 1000 visitors per month\n"
        "3. move my-website to prepare stage\n"
        "4. show me what I said about Rust programming\n"
        "5. I remember that mornings are best for coding",
        "Example Commands"
    )
    
    input("\nPress Enter to continue...")
    
    # Show suggestions example
    clear_screen()
    print_box("Simulating: 'create a new project to build a website'", "")
    show_suggestions("create a new project to build a website")
    
    input("\nPress Enter to continue...")
    
    # Show full interface preview
    clear_screen()
    print(f"{Color.BOLD}Full Interface Preview{Color.RESET}\n")
    
    print_box(
        f"{Color.MAGENTA}{Color.BOLD}Vinci Knowledge Partner{Color.RESET} | Stage: Seed",
        ""
    )
    
    show_pipeline()
    
    input("\nPress Enter to see ideas...")
    
    show_ideas()
    
    input("\n" + "="*60)
    print(f"\n{Color.BOLD}Demo complete!{Color.RESET}\n")
    print("To run the full TUI:")
    print("  pip install rich click")
    print("  python -m vinci_tui\n")


if __name__ == "__main__":
    main()
