"""Archive and remove the superseded Qwik frontend after Actix cutover."""

from __future__ import annotations

import hashlib
import shutil
import zipfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "frontend" / "web"
ARCHIVE = ROOT / "artifacts" / "qwik-frontend-legacy-20261001.zip"
EXCLUDED_PARTS = {"node_modules", "dist", "test-results"}


def main() -> None:
    if not SOURCE.is_dir():
        raise SystemExit(f"Qwik frontend directory is missing: {SOURCE}")
    ARCHIVE.parent.mkdir(parents=True, exist_ok=True)
    files = sorted(
        path
        for path in SOURCE.rglob("*")
        if path.is_file() and not EXCLUDED_PARTS.intersection(path.parts)
    )
    manifest = []
    with zipfile.ZipFile(ARCHIVE, "w", zipfile.ZIP_DEFLATED) as bundle:
        for path in files:
            relative = path.relative_to(ROOT).as_posix()
            digest = hashlib.sha256(path.read_bytes()).hexdigest()
            manifest.append(f"{digest}  {relative}")
            bundle.write(path, relative)
        bundle.writestr("legacy/MANIFEST.sha256", "\n".join(manifest) + "\n")
    shutil.rmtree(SOURCE)
    print(f"archived {len(files)} files to {ARCHIVE}")
    print(f"removed {SOURCE}")


if __name__ == "__main__":
    main()
