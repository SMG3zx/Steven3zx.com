"""python -m vinci_cli: the same entry point as the installed `vinci` command."""

import sys

from .vinci import main

if __name__ == "__main__":
    sys.exit(main())
