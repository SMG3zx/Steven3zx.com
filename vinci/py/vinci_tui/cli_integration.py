"""
CLI Integration for Vinci TUI
Bridges the TUI with the actual Vinci CLI commands.
"""

import subprocess
import sys
from typing import List, Optional


class CLIBridge:
    """Bridge between TUI and Vinci CLI."""
    
    def __init__(self, vinci_exe: Optional[str] = None):
        """
        Initialize the CLI bridge.
        
        Args:
            vinci_exe: Path to vinci executable (default: 'vinci' command)
        """
        self.vinci_exe = vinci_exe or "vinci"
    
    def run_command(self, args: List[str], capture_output: bool = True) -> tuple[int, str, str]:
        """
        Run a Vinci CLI command.
        
        Args:
            args: Command arguments (e.g., ["idea", "My Project"])
            capture_output: Whether to capture stdout/stderr
            
        Returns:
            Tuple of (return_code, stdout, stderr)
        """
        try:
            # Build full command
            cmd = [self.vinci_exe] + args
            
            # Run command
            result = subprocess.run(
                cmd,
                capture_output=capture_output,
                text=True,
                timeout=30  # 30 second timeout
            )
            
            return (
                result.returncode,
                result.stdout.strip(),
                result.stderr.strip()
            )
        except subprocess.TimeoutExpired:
            return (-1, "", "Command timed out after 30 seconds")
        except FileNotFoundError:
            return (-2, "", f"Vinci not found at '{self.vinci_exe}'. Make sure it's installed.")
        except Exception as e:
            return (-1, "", str(e))
    
    def execute_idea(self, title: str) -> tuple[int, str]:
        """Execute 'vinci idea <title>' command."""
        code, stdout, stderr = self.run_command(["idea", title])
        if code == 0:
            return (0, f"✓ Created idea: {title}")
        elif code == -2:
            return (-2, "Vinci not installed")
        else:
            return (code, stderr or stdout)
    
    def execute_for(self, idea_id: str, who: str) -> tuple[int, str]:
        """Execute 'vinci for <id> <who>' command."""
        code, stdout, stderr = self.run_command(["for", idea_id, who])
        if code == 0:
            return (0, f"✓ Set purpose for {idea_id}")
        else:
            return (code, stderr or stdout)
    
    def execute_goal(self, idea_id: str, goal: str) -> tuple[int, str]:
        """Execute 'vinci goal <id> <goal>' command."""
        code, stdout, stderr = self.run_command(["goal", idea_id, goal])
        if code == 0:
            return (0, f"✓ Set goal for {idea_id}")
        else:
            return (code, stderr or stdout)
    
    def execute_advance(self, idea_id: str, stage: str) -> tuple[int, str]:
        """Execute 'vinci advance <id> [days]' command."""
        code, stdout, stderr = self.run_command(["advance", idea_id])
        if code == 0:
            return (0, f"✓ Advanced {idea_id}")
        else:
            return (code, stderr or stdout)
    
    def execute_recall(self, query: str, sources: Optional[str] = None) -> tuple[int, str]:
        """Execute 'vinci recall <query>' command."""
        cmd = ["recall", query]
        if sources:
            cmd.extend(["sources", sources])
        
        code, stdout, stderr = self.run_command(cmd)
        if code == 0:
            return (0, stdout or "No results found")
        else:
            return (code, stderr or stdout)
    
    def execute_pipline(self) -> tuple[int, str]:
        """Execute 'vinci pipeline' command."""
        code, stdout, stderr = self.run_command(["pipeline"])
        if code == 0:
            return (0, stdout or "No pipeline data")
        else:
            return (code, stderr or stdout)
    
    def execute_show(self, idea_id: str) -> tuple[int, str]:
        """Execute 'vinci show <id>' command."""
        code, stdout, stderr = self.run_command(["show", idea_id])
        if code == 0:
            return (0, stdout or f"No data for {idea_id}")
        else:
            return (code, stderr or stdout)
    
    def is_installed(self) -> bool:
        """Check if Vinci CLI is installed."""
        code, _, _ = self.run_command(["--version"])
        return code == 0


def check_vinci_installation() -> tuple[bool, str]:
    """
    Check if Vinci is properly installed.
    
    Returns:
        Tuple of (is_installed, message)
    """
    bridge = CLIBridge()
    
    if not bridge.is_installed():
        return (False, 
            "Vinci CLI not found.\n"
            "Install it with: ./install.sh\n"
            "Or set PATH to include ~/.local/bin")
    
    return (True, "Vinci is ready!")
