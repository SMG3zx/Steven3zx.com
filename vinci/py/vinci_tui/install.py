#!/usr/bin/env python3
"""
Installation script for Vinci TUI.
"""

import os
import sys
import shutil


def install():
    """Install Vinci TUI."""
    print("Installing Vinci TUI...")
    
    # Check if requirements are met
    try:
        import rich
        print("✓ Rich library found")
    except ImportError:
        print("✗ Rich library not found")
        print("Installing dependencies...")
        os.system("pip install rich click")
        print("✓ Dependencies installed")
    
    # Add to path
    tui_path = os.path.dirname(os.path.abspath(__file__))
    sys.path.insert(0, tui_path)
    
    print(f"✓ Vinci TUI ready at: {tui_path}")
    print("\nRun with: python -m vinci_tui")
    return True


def uninstall():
    """Uninstall Vinci TUI."""
    print("Removing Vinci TUI...")
    # TODO: Implement actual uninstallation
    return True


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--uninstall":
        uninstall()
    else:
        install()
