#!/usr/bin/env python3
"""
Vinci TUI - Entry point
Run as: python -m vinci_tui
"""

import sys
import argparse
from .app import VinciTUI


def main():
    parser = argparse.ArgumentParser(
        description="Vinci TUI - Natural language interface for your knowledge"
    )
    parser.add_argument(
        "--journal", "-j",
        type=str,
        default=None,
        help="Path to journal file (default: from VINCI_HOME)"
    )
    parser.add_argument(
        "--demo", "-d",
        action="store_true",
        help="Run demo mode with sample data"
    )
    
    args = parser.parse_args()
    
    # Create app and run
    app = VinciTUI(journal_path=args.journal)
    
    if args.demo:
        print("[Demo mode - this will be implemented]")
        print("Running demo...")
    
    try:
        app.run()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
