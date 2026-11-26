"""
Vinci TUI - Full Text User Interface

A rich terminal-based interface for interacting with Vinci's knowledge system.

Usage:
    python -m vinci_tui          # Start TUI
    python -m vinci_tui --demo   # Run demo mode
    python -m vinci_tui --help   # Show help

Examples:
    > create a new project to build a website
    > set goal for my-website to have 1000 visitors per month
    > move my-website to prepare stage
"""

__version__ = "0.1.0"
__author__ = "Vinci Team"

from .app import VinciTUI, main as tui_main
from .parser import IntentParser, ParsedIntent, parse_intent
from .cli_integration import CLIBridge, check_vinci_installation
from .demo import DemoMode

__all__ = [
    "VinciTUI",
    "IntentParser", 
    "ParsedIntent",
    "parse_intent",
    "CLIBridge",
    "check_vinci_installation",
    "DemoMode",
]
