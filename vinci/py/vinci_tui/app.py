"""
Vinci TUI - Main Application
A full text-based interface for interacting with Vinci's knowledge system.
"""

import sys
import os
from typing import Optional, Dict, Any
from rich.console import Console
from rich.layout import Layout
from rich.panel import Panel
from rich.text import Text
from rich.table import Table
from rich.live import Live
from dataclasses import dataclass, field
from datetime import datetime

# Add parent directory to path for imports
sys.path.insert(0, str(__file__.rsplit("/vinci_tui", 1)[0]))

from .parser import IntentParser, ParsedIntent
from .widgets import (
    PipelineWidget,
    IdeaCard,
    ChatInput,
    StatsPanel,
)
from .cli_integration import CLIBridge, check_vinci_installation


@dataclass
class AppState:
    """Application state."""
    ideas: Dict[str, Dict[str, Any]] = field(default_factory=dict)
    chat_history: list = field(default_factory=list)
    current_stage: int = 0
    loading: bool = False
    suggestions: list = field(default_factory=list)
    pipeline_data: Dict[str, int] = field(default_factory=dict)
    
    def __post_init__(self):
        # Initialize pipeline data with zeros
        from .widgets import PipelineWidget
        for stage in PipelineWidget.STAGES:
            if stage not in self.pipeline_data:
                self.pipeline_data[stage] = 0


class VinciTUI:
    """Main TUI application."""
    
    def __init__(self, journal_path: Optional[str] = None):
        self.console = Console()
        self.parser = IntentParser(verbose=False)
        self.state = AppState()
        
        # Initialize CLI bridge
        self.bridge = CLIBridge()
        
        # Set journal path if provided
        if journal_path:
            os.environ["VINCI_HOME"] = os.path.dirname(journal_path)
    
    def run(self):
        """Run the TUI application."""
        # Check installation
        installed, message = check_vinci_installation()
        if not installed:
            self.console.print(f"[red]{message}[/]", style="bold")
            self.console.print("[yellow]You can still use demo mode for testing.[/]\n")
        
        self.show_welcome()
        
        # Initial pipeline fetch
        self._fetch_pipeline_data()
        
        while True:
            try:
                user_input = self.get_user_input()
                
                if not user_input.strip():
                    continue
                
                # Handle special commands
                if user_input.lower().strip("/") in ["/exit", "/quit", "/q"]:
                    self.show_goodbye()
                    break
                
                if user_input.lower().strip("/") == "/help":
                    self.show_help()
                    continue
                
                if user_input.lower().strip("/") == "/pipeline":
                    self._fetch_pipeline_data()
                    self.render_main_layout()
                    continue
                
                # Parse intent
                intent = self.parser.parse(user_input)
                
                # Execute action
                result = self.execute_intent(intent, user_input)
                
                # Update suggestions
                self.state.suggestions = intent.suggestions
                
            except KeyboardInterrupt:
                self.show_goodbye()
                break
    
    def show_welcome(self):
        """Display welcome screen."""
        welcome_text = Text()
        welcome_text.append("╔════════════════════════════════════════╗\n", style="bold blue")
        welcome_text.append("║     Vinci Knowledge Partner TUI        ║\n", style="bold blue")
        welcome_text.append("╠════════════════════════════════════════╣\n", style="bold blue")
        welcome_text.append("║  Natural language interface for        ║\n", style="white")
        welcome_text.append("║  your ideas, notes, and knowledge.     ║\n", style="white")
        welcome_text.append("╚════════════════════════════════════════╝\n", style="bold blue")
        
        self.console.print(welcome_text)
        self.console.print("[dim]Type /help for commands[/dim]\n")
    
    def show_goodbye(self):
        """Display goodbye message."""
        text = Text("Goodbye! Your knowledge is safe. 👋\n", style="green bold")
        self.console.print(text)
    
    def get_user_input(self) -> str:
        """Get user input from console."""
        try:
            return self.console.input("> ")
        except EOFError:
            return ""
    
    def show_help(self):
        """Display help information."""
        help_text = Text()
        
        help_text.append("\n[bold]NATURAL LANGUAGE INPUT:[/]\n", style="bold cyan")
        help_text.append("  'create new project to build website'  → Creates seed idea\n")
        help_text.append("  'set goal for my-project to reach 1000 visitors' → Sets metric\n")
        help_text.append("  'move my-project to build stage' → Advances pipeline\n")
        help_text.append("  'show me what I said about Rust' → Recalls from index\n")
        help_text.append("  'I remember that mornings are best for coding' → Records fact\n")
        help_text.append("")
        
        help_text.append("[bold]COMMANDS:[/]\n", style="bold cyan")
        help_text.append("  [/help]      - Show this help\n")
        help_text.append("  [/exit]      - Exit the application\n")
        help_text.append("  [/pipeline]  - Show pipeline status\n")
        help_text.append("")
        
        self.console.print(help_text)
    
    def execute_intent(self, intent: ParsedIntent, raw_input: str) -> str:
        """Execute parsed intent and return result."""
        cli_args = intent.to_command()
        
        if not cli_args or cli_args[0] == "recall":
            # Use chat mode for recall/search
            return self._execute_chat_mode(raw_input)
        elif cli_args[0] in ["idea", "for", "goal", "advance"]:
            # Execute CLI command
            return self._execute_cli_command(cli_args, raw_input)
        else:
            return "[yellow]Unknown action. Try /help for available commands.[/]"
    
    def _execute_cli_command(self, args: list, raw_input: str) -> str:
        """Execute a CLI command via subprocess."""
        if not self.bridge.is_installed():
            return "[red]Vinci CLI not installed. Install with ./install.sh[/red]"
        
        code, stdout, stderr = self.bridge.run_command(args)
        
        if code == 0:
            return f"[green]✓ Success:[/]\n{stdout}"
        elif code == -2:
            return "[red]Vinci CLI not found in PATH[/red]"
        else:
            return f"[red]Error:[/]\n{stderr or stdout}"
    
    def _execute_chat_mode(self, input_text: str) -> str:
        """Execute in chat mode (passes to existing vinci chat)."""
        if not self.bridge.is_installed():
            return "[yellow]Vinci CLI not installed. Using demo mode.[/yellow]"
        
        # For now, just echo the command
        # TODO: Integrate with actual chat
        return f"[dim]Would execute: vinci {input_text}[/dim]"
    
    def _fetch_pipeline_data(self):
        """Fetch current pipeline data from Vinci."""
        if not self.bridge.is_installed():
            return
        
        code, stdout, stderr = self.bridge.run_command(["pipeline"])
        
        if code == 0:
            # Parse pipeline output
            # TODO: Implement actual parsing
            pass
    
    def render_main_layout(self):
        """Render the main application layout."""
        layout = Layout()
        
        # Split into panels
        layout.split_column(
            Layout(name="header", size=3),
            Layout(name="pipeline"),
            Layout(name="ideas", ratio=2),
            Layout(name="input"),
        )
        
        # Render each panel
        self._render_header(layout["header"])
        self._render_pipeline(layout["pipeline"])
        self._render_ideas(layout["ideas"])
        self._render_input(layout["input"])
        
        self.console.print(layout)
    
    def _render_header(self, panel):
        """Render header panel."""
        content = Text()
        content.append("Vinci Knowledge Partner", style="bold magenta")
        content.append(" | ")
        stage_names = ["Seed", "Prepare", "Incubate", "Build", "Tend", "Retired"]
        stage = stage_names[min(self.state.current_stage, 5)]
        content.append(f"Current Stage: {stage}")
        
        panel.update(Panel(content, box=box.DOUBLE))
    
    def _render_pipeline(self, panel):
        """Render pipeline status."""
        if self.state.loading:
            panel.update(Panel("Loading...", box=box.DOUBLE))
        else:
            widget = PipelineWidget()
            content = widget.render(self.state.pipeline_data)
            panel.update(content)
    
    def _render_ideas(self, panel):
        """Render ideas section."""
        if not self.state.ideas:
            content = Text("[dim]No ideas yet. Try 'create new project to build website'[/dim]")
            panel.update(Panel(content, box=box.ROUNDED))
        else:
            # Render each idea as a card
            cards = []
            for idea_id, data in self.state.ideas.items():
                widget = IdeaCard()
                card = widget.render(data)
                cards.append(card)
            
            if cards:
                # Stack cards vertically
                stacked = Panel(
                    Text("\n\n".join(str(c) for c in cards), justify="left"),
                    box=box.DOUBLE
                )
                panel.update(stacked)
            else:
                content = Text("[dim]No ideas loaded[/dim]")
                panel.update(Panel(content, box=box.ROUNDED))
    
    def _render_input(self, panel):
        """Render input section."""
        if self.state.loading:
            content = Text("Processing...")
        elif self.state.suggestions:
            widget = ChatInput()
            content = widget.render("", self.state.suggestions)
        else:
            content = Text("[dim]Type your request here...[/dim]")
        
        panel.update(Panel(content, box=box.SIMPLE))


def main():
    """Entry point for the TUI application."""
    app = VinciTUI()
    app.run()


if __name__ == "__main__":
    main()
