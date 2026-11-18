"""Where the C# oracle lives. Every differential test reads this one setting.

VINCI_ORACLE names the archived C# binary (default ~/.local/share/vinci-csharp-oracle/vinci).
The binary is a framework-dependent .NET 10 publish, so it needs the dotnet 10 runtime. Its
source is the source/ directory beside it: src/ and tests/ as they were at the commit in
source/HEAD.txt, and portal_truth/Truth.cs, the program behind the portal's tables. The
generators and the coverage report read the C# files from there.
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Final

DEFAULT_ORACLE: Final = Path.home() / ".local" / "share" / "vinci-csharp-oracle" / "vinci"
ORACLE: Final[Path] = Path(os.environ.get("VINCI_ORACLE") or DEFAULT_ORACLE).expanduser()
ORACLE_SOURCE: Final[Path] = ORACLE.parent / "source"
