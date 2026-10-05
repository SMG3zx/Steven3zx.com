"""Archive verified Go and Zig sources before their active directories are removed."""

from __future__ import annotations

import hashlib
from pathlib import Path
import zipfile


ROOT = Path(__file__).resolve().parents[1]
ARCHIVE = ROOT / "artifacts" / "legacy-implementations-20261001.zip"
SOURCES = (ROOT / "backend" / "janus-api", ROOT / "backend" / "janus-sloc", ROOT / "Janus-Zig")
EXCLUDED_PARTS = {".zig-cache", ".zig-global-cache", "zig-out", ".janus"}


def files_to_archive() -> list[tuple[Path, str]]:
    files: list[tuple[Path, str]] = []
    for source in SOURCES:
        if not source.is_dir():
            raise FileNotFoundError(source)
        for path in source.rglob("*"):
            if not path.is_file() or any(part in EXCLUDED_PARTS for part in path.relative_to(source).parts):
                continue
            archive_name = Path("legacy") / path.relative_to(ROOT)
            files.append((path, archive_name.as_posix()))
    return sorted(files, key=lambda item: item[1])


def main() -> int:
    files = files_to_archive()
    ARCHIVE.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(ARCHIVE, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        manifest: list[str] = []
        for path, archive_name in files:
            digest = hashlib.sha256(path.read_bytes()).hexdigest()
            archive.write(path, archive_name)
            manifest.append(f"{digest}  {archive_name}")
        archive.writestr("legacy/MANIFEST.sha256", "\n".join(manifest) + "\n")
    print(f"archived {len(files)} files to {ARCHIVE}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
