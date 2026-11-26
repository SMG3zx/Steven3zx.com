"""
Rich widgets for Vinci TUI.
Provides visual components for the interface.
"""

from rich.console import Console
from rich.panel import Panel
from rich.table import Table
from rich.layout import Layout
from rich import box
from rich.text import Text
from rich.live import Live
from rich.progress import Progress, SpinnerColumn, TextColumn
from typing import List, Optional
import datetime


class PipelineWidget:
    """Visual representation of the pipeline stages."""
    
    STAGES = ["seed", "prepare", "incubate", "build", "tend", "retired"]
    STAGE_ICONS = {
        "seed": "🌱",
        "prepare": "📝",
        "incubate": "🔬",
        "build": "🔨",
        "tend": "🌿",
        "retired": "🏳️",
    }
    
    def __init__(self, console: Optional[Console] = None):
        self.console = console or Console()
    
    def render(self, ideas_data: dict) -> Panel:
        """Render pipeline visualization with idea counts per stage."""
        # Calculate counts for each stage
        stage_counts = {}
        for stage in self.STAGES:
            count = ideas_data.get(stage, 0)
            stage_counts[stage] = count
        
        # Create visual representation
        lines = []
        for i, stage in enumerate(self.STAGES):
            icon = self.STAGE_ICONS.get(stage, "•")
            count = stage_counts.get(stage, 0)
            
            # Progress bar segment
            filled = "█" * min(count, 5)
            empty = "░" * max(0, 5 - count)
            bar = f"[{filled}{empty}]"
            
            lines.append(f"{icon} {stage.capitalize():12} [{bar}] {count}")
        
        # Create panel
        content = Text("\n".join(lines))
        return Panel(
            content,
            title="📊 Pipeline Status",
            box=box.DOUBLE_HEAVY,
            border_style="blue",
        )


class IdeaCard:
    """Visual card for displaying an idea."""
    
    def __init__(self, console: Optional[Console] = None):
        self.console = console or Console()
    
    def render(self, idea_data: dict) -> Panel:
        """Render a single idea as a card."""
        idea_id = idea_data.get("id", "Unknown")
        title = idea_data.get("title", "Untitled")
        stage = idea_data.get("stage", "seed")
        
        # Get stage info
        stage_name = stage.capitalize()
        icon = self.STAGE_ICONS.get(stage, "•")
        
        # Build content
        lines = [
            f"[bold blue]{idea_id}[/] - {title}",
            "",
            f"[dim]Stage:[/dim] {icon} {stage_name}",
        ]
        
        # Add additional info if available
        for key, value in idea_data.items():
            if key not in ["id", "title", "stage"] and value:
                lines.append(f"[dim]{key.capitalize()}:[/dim] {value}")
        
        content = Text("\n".join(lines))
        
        return Panel(
            content,
            title=f"Idea {idea_id}",
            box=box.ROUNDED,
            border_style="green",
        )


class ChatInput:
    """Interactive input component for chat."""
    
    def __init__(self, console: Optional[Console] = None):
        self.console = console or Console()
        self.prompt = "you> "
    
    def render(self, text: str = "", suggestions: List[str] = []) -> Panel:
        """Render the input area with prompt and suggestions."""
        lines = []
        
        # Add prompt
        if text:
            lines.append(f"[bold]{self.prompt}[/]{text}")
        
        # Add suggestions if any
        if suggestions:
            lines.append("")
            suggestion_text = Text()
            for i, suggestion in enumerate(suggestions[:3], 1):
                suggestion_text.append(f"{i}. {suggestion}\n", style="dim")
            lines.append(suggestion_text)
        
        content = Text("\n".join(lines))
        
        return Panel(
            content,
            title="Ask anything or tell it what to do",
            box=box.SIMPLE,
            border_style="cyan",
            subtitle="/help for commands, /exit to leave",
        )


class StatsPanel:
    """Display statistics and metrics."""
    
    def __init__(self, console: Optional[Console] = None):
        self.console = console or Console()
    
    def render(self, stats: dict) -> Table:
        """Render statistics in a table format."""
        table = Table(title="📈 Statistics", show_header=True, header_style="bold magenta")
        
        # Add columns
        table.add_column("Metric", style="cyan")
        table.add_column("Value", style="green")
        table.add_column("Status", style="yellow")
        
        # Add rows based on available stats
        for key, value in stats.items():
            if isinstance(value, (int, float)):
                status = "✓" if value >= 0 else "⚠"
                table.add_row(key.capitalize(), str(value), status)
            else:
                table.add_row(key.capitalize(), str(value), "")
        
        return table


class LoadingIndicator:
    """Show loading spinner during operations."""
    
    def __init__(self, console: Optional[Console] = None):
        self.console = console or Console()
        self.spinner = SpinnerColumn()
        self.task = TextColumn("[{spinner}] {task.description}")
    
    def render(self, description: str) -> Progress:
        """Create a progress indicator."""
        with Progress(
            self.spinner,
            self.task,
            console=self.console,
        ) as progress:
            task = progress.add_task(description, total=None)
            return progress
