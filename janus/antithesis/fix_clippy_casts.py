#!/usr/bin/env python3
"""Replace the known truncating casts reported by strict Clippy.

The rewrite is intentionally exact and idempotent. Values crossing the
bounded persistence/runtime boundary use a checked conversion with a bounded
sentinel instead of silently truncating.
"""

from __future__ import annotations

from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent / "janus-rust" / "src"
FILES = [ROOT / "auth_persistence.rs", ROOT / "queue_persistence.rs", ROOT / "persistence.rs", ROOT / "lib.rs", ROOT / "object_store.rs", ROOT / "actor.rs"]

REPLACEMENTS = {
    "snapshot.users.len() as u32": "u32::try_from(snapshot.users.len()).unwrap_or(u32::MAX)",
    "snapshot.sessions.len() as u32": "u32::try_from(snapshot.sessions.len()).unwrap_or(u32::MAX)",
    "self.u32(value.len() as u32)": "self.u32(u32::try_from(value.len()).unwrap_or(u32::MAX))",
    "jobs.len() as u32": "u32::try_from(jobs.len()).unwrap_or(u32::MAX)",
    "self.u32(value.len() as u32)": "self.u32(u32::try_from(value.len()).unwrap_or(u32::MAX))",
    "snapshot.seen_commands.len() as u32": "u32::try_from(snapshot.seen_commands.len()).unwrap_or(u32::MAX)",
    "values.len() as u32": "u32::try_from(values.len()).unwrap_or(u32::MAX)",
    "result.stages.len() as u32": "u32::try_from(result.stages.len()).unwrap_or(u32::MAX)",
    "MAX_BUILDS.min(u32::MAX as usize) as u32": "u32::try_from(MAX_BUILDS.min(u32::MAX as usize)).unwrap_or(u32::MAX)",
    "count as u32": "u32::try_from(count).unwrap_or(u32::MAX)",
    "self.events.len() as u32": "u32::try_from(self.events.len()).unwrap_or(u32::MAX)",
    "self.effects.len() as u32": "u32::try_from(self.effects.len()).unwrap_or(u32::MAX)",
    "events.len() as u32": "u32::try_from(events.len()).unwrap_or(u32::MAX)",
    "effects.len() as u32": "u32::try_from(effects.len()).unwrap_or(u32::MAX)",
    "metadata.len() as usize": "usize::try_from(metadata.len()).unwrap_or(usize::MAX)",
    ".len() as usize": ".len() as usize",
}

ID_FIELDS = ("spec.id.0", "actor.0", "entity.0", "project.id.0", "project.0", "build.0", "deployment.0")


def main() -> None:
    changed = 0
    for path in FILES:
        source = path.read_text(encoding="utf-8")
        updated = source
        for old, new in REPLACEMENTS.items():
            updated = updated.replace(old, new)
        for field in ID_FIELDS:
            updated = updated.replace(
                f"{field} as usize",
                f"usize::try_from({field}).unwrap_or(usize::MAX)",
            )
        if updated != source:
            path.write_text(updated, encoding="utf-8")
            changed += 1
    print(f"checked-cast files changed: {changed}")


if __name__ == "__main__":
    main()
