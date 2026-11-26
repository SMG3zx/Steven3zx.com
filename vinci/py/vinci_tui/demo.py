#!/usr/bin/env python3
"""
Demo mode for Vinci TUI.
Shows example interactions without requiring actual Vinci installation.
"""

from rich.console import Console
from rich.panel import Panel
from rich.table import Table
from rich.text import Text
from rich.live import Live
from typing import List, Dict
import random


class DemoMode:
    """Demo mode simulator."""
    
    def __init__(self):
        self.console = Console()
        self.ideas = {
            "i1": {
                "id": "i1",
                "title": "Build a website for my portfolio",
                "stage": "seed",
                "created_at": "2026-11-23",
                "for": "",
                "goal": "",
            },
            "i2": {
                "id": "i2",
                "title": "Learn Rust programming",
                "stage": "prepare",
                "created_at": "2026-11-20",
                "for": "Personal skill development",
                "goal": "Complete The Rust Book in 30 days",
            },
        }
        
        self.pipeline_data = {
            "seed": 5,
            "prepare": 8,
            "incubate": 12,
            "build": 3,
            "tend": 2,
            "retired": 4,
        }
    
    def run(self):
        """Run the demo."""
        print("\n" + "="*60)
        print("Vinci TUI Demo Mode")
        print("="*60)
        print("\nThis is a demonstration of the TUI interface.")
        print("Commands will be simulated without actual Vinci integration.\n")
        
        self.show_welcome()
        
        # Simulate some demo interactions
        self.run_demo_scenarios()
        
        print("\n" + "="*60)
        print("Demo complete!")
        print("="*60 + "\n")
    
    def show_welcome(self):
        """Show welcome message."""
        welcome = Text()
        welcome.append("╔════════════════════════════════════════╗\n", style="bold blue")
        welcome.append("║     Vinci Knowledge Partner TUI        ║\n", style="bold blue")
        welcome.append("╠════════════════════════════════════════╣\n", style="bold blue")
        welcome.append("║  Demo Mode - Natural Language Demo    ║\n", style="white")
        welcome.append("╚════════════════════════════════════════╝\n", style="bold blue")
        
        self.console.print(welcome)
    
    def run_demo_scenarios(self):
        """Run a series of demo scenarios."""
        scenarios = [
            ("create a new project to build a website", "Creating idea..."),
            ("set goal for my-website to have 1000 visitors per month", "Setting goal..."),
            ("move my-website to prepare stage", "Advancing stage..."),
            ("show me what I said about Rust", "Recalling from index..."),
        ]
        
        for i, (input_text, action) in enumerate(scenarios):
            print(f"\n[{i+1}] Simulating: '{input_text}'")
            print(f"    → {action}")
            self.show_pipeline_preview()
            self.show_suggestions(input_text)
    
    def show_pipeline_preview(self):
        """Show a preview of pipeline status."""
        panel = Panel(
            Text("\n".join([
                "📊 Pipeline Status",
                "",
                "🌱 Seed          [████░░] 5",
                "📝 Prepare       [██████░] 8",
                "🔬 Incubate      [███████] 12",
                "🔨 Build         [██░░░░] 3",
                "🌿 Tend          [░░░░░░] 2",
                "🏳️ Retired       [████░░] 4",
            ])),
            title="Current Pipeline",
            box=box.DOUBLE,
            border_style="blue"
        )
        self.console.print(panel)
    
    def show_suggestions(self, input_text: str):
        """Show suggestions based on input."""
        suggestions = []
        
        if "create" in input_text.lower() or "new" in input_text.lower():
            suggestions = [
                "Set a goal for this idea",
                "Define who it's for",
                "Add initial requirements",
            ]
        
        panel = Panel(
            Text("\n".join([
                "[bold cyan]Suggestions:[/]",
                "",
            ] + [f"  {i+1}. {s}" for i, s in enumerate(suggestions)]),
            title="Next Steps",
            box=box.ROUNDED,
            border_style="green"
        )
        self.console.print(panel)


# Rich box import fix
from rich.box import DOUBLE as box


def main():
    """Entry point for demo mode."""
    demo = DemoMode()
    demo.run()


if __name__ == "__main__":
    main()
