#!/usr/bin/env python3
"""
Install vinci CLI.

This script installs the vinci CLI to your system PATH.
"""

import sys
import os
import shutil
from pathlib import Path


def get_install_paths():
    """Get installation paths based on user home."""
    home = Path.home()
    return {
        "script": home / "vinci",
        "bin": home / ".local" / "bin",
        "lib": home / ".local" / "lib" / "vinci",
    }


def ensure_bin_dir(paths):
    """Ensure the bin directory exists."""
    bin_dir = paths["bin"]
    bin_dir.mkdir(parents=True, exist_ok=True)
    return bin_dir


def create_symlink(bin_dir, target):
    """Create a symbolic link."""
    link_path = bin_dir / "vinci"
    
    if link_path.exists() or link_path.is_symlink():
        print(f"Removing existing {link_path}")
        link_path.unlink()
    
    link_path.symlink_to(target)
    print(f"Created {link_path} -> {target}")


def main():
    """Main installation."""
    paths = get_install_paths()
    bin_dir = ensure_bin_dir(paths)
    
    # Create symlink to the script
    create_symlink(bin_dir, paths["script"])
    
    print(f"Installation complete!")
    print(f"  Script: {paths['script']}")
    print(f"  Binary: {bin_dir / 'vinci'}")
    print()
    print("To use vinci, add ~/.local/bin to your PATH.")
    print("Example: echo 'export PATH=\"$HOME/.local/bin:$PATH\"' >> ~/.bashrc")
    
    return 0


if __name__ == "__main__":
    sys.exit(main())
