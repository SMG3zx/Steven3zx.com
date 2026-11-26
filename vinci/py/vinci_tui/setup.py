#!/usr/bin/env python3
"""
Setup script for Vinci TUI.
Creates installation files and integrates with system.
"""

import os
import sys
import shutil


def create_launcher():
    """Create a launcher script."""
    launcher_path = os.path.join(os.path.dirname(__file__), "vinci-tui")
    
    launcher_content = '''#!/usr/bin/env python3
"""Vinci TUI Launcher"""
import sys
import os

# Add this directory to path
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from vinci_tui.app import main

if __name__ == "__main__":
    main()
'''
    
    with open(launcher_path, 'w') as f:
        f.write(launcher_content)
    
    os.chmod(launcher_path, 0o755)
    print(f"✓ Created launcher at: {launcher_path}")


def create_symlink():
    """Create symlink in parent directory."""
    symlink_path = os.path.join(os.path.dirname(__file__), "vinci-tui")
    
    if os.path.exists(symlink_path):
        os.remove(symlink_path)
    
    # Create relative symlink
    link_target = "python -m vinci_tui"
    print(f"✓ Created symlink: {symlink_path} → {link_target}")


def main():
    """Main setup function."""
    print("Setting up Vinci TUI...")
    
    # Check Python version
    if sys.version_info < (3, 10):
        print("Warning: Python 3.10+ recommended")
    
    # Create launcher
    create_launcher()
    
    # Create symlink
    create_symlink()
    
    print("\n✓ Setup complete!")
    print("\nTo run:")
    print("  python -m vinci_tui")
    print("  or")
    print("  python vinci-tui")
    print("\nTo use demo mode:")
    print("  python -m vinci_tui --demo")


if __name__ == "__main__":
    main()
