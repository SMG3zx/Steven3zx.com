"""Ask Jev whether backend Go files are suitable candidates for Zig migration."""

from __future__ import annotations

import concurrent.futures
import json
import os
import re
import sys
from pathlib import Path
from urllib.request import Request, urlopen


ROOT = Path(__file__).resolve().parents[1]
BACKEND = ROOT / "backend" / "janus-api"
API_URL = os.environ.get("TYPESAFE_API_URL", "https://api.typesafe.ai/v1/systemone")
MODEL = os.environ.get("TYPESAFE_MODEL", "jev-latest")

QUESTIONS = {
    "zigSuitability": {
        "type": "choice",
        "instructions": (
            "Should this Go file be migrated to Zig based on its source, role, imports, "
            "runtime characteristics, ecosystem dependencies, and boundary responsibilities? "
            "Judge migration suitability, not technical-debt severity."
        ),
        "criteria": {
            "strong_candidate": "Zig is clearly a better fit and the file has a stable, narrow boundary.",
            "conditional_candidate": "Zig may help, but only after a benchmark or a small prototype.",
            "keep_go": "Go is the better fit because this is integration, business, HTTP, database, or security code.",
            "insufficient_evidence": "The source does not provide enough evidence for a language decision.",
        },
    },
    "migrationRisk": {
        "type": "score",
        "instructions": "How risky would migrating this file to Zig be, considering API compatibility, dependencies, concurrency, and operational behavior?",
        "criteria": ["Minimal", "Low", "Moderate", "High", "Extreme"],
    },
    "zigBenefit": {
        "type": "score",
        "instructions": "How much concrete benefit would Zig provide here through native performance, predictable memory, binary size, portability, or low-level control?",
        "criteria": ["None", "Small", "Moderate", "High", "Very high"],
    },
    "reason": {
        "type": "choice",
        "instructions": "What is the primary reason for the recommendation?",
        "criteria": {
            "native_runtime": "Low-level runtime, process, WASM, or resource-control work.",
            "performance": "A measured hot path where Zig could materially improve performance.",
            "portability": "A standalone tool or component benefits from a small portable binary.",
            "ecosystem": "Go's ecosystem is materially better for this file.",
            "integration": "The file is primarily an HTTP, database, auth, telemetry, or service integration boundary.",
            "unknown": "No clear language-specific reason is visible.",
        },
    },
}


def load_env() -> None:
    for name in (".env", ".env.local"):
        path = ROOT / name
        if not path.exists():
            continue
        for line in path.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, value = line.split("=", 1)
            value = value.strip().strip('"').strip("'")
            os.environ.setdefault(key.strip(), value)


def state_for(path: Path) -> dict:
    source = path.read_text(encoding="utf-8")
    rel = path.relative_to(ROOT).as_posix()
    imports = re.findall(r'"([^"\n]+)"', source[source.find("import"):]) if "import" in source else []
    return {
        "path": rel,
        "package": re.search(r"^package\s+([\w]+)", source, re.MULTILINE).group(1) if re.search(r"^package\s+([\w]+)", source, re.MULTILINE) else "unknown",
        "lineCount": len(source.splitlines()),
        "testFile": path.name.endswith("_test.go"),
        "imports": imports[:80],
        "source": source,
    }


def ask(state: dict) -> dict:
    body = json.dumps({"model": MODEL, "state": state, "questions": QUESTIONS}).encode()
    request = Request(API_URL, data=body, method="POST", headers={
        "Authorization": f"Bearer {os.environ['TYPESAFE_API_KEY']}",
        "Content-Type": "application/json",
    })
    with urlopen(request, timeout=60) as response:
        payload = json.loads(response.read().decode())
    return payload.get("answers", payload)


def main() -> int:
    if len(sys.argv) >= 3 and sys.argv[1] == "--summarize":
        report = json.loads(Path(sys.argv[2]).read_text(encoding="utf-8"))
        scored = [item for item in report["results"] if "answers" in item]
        probabilities = [item["answers"]["zigSuitability"].get("probabilities", {}) for item in scored]
        averages = {
            key: round(100 * sum(prob.get(key, 0) for prob in probabilities) / len(probabilities), 2)
            for key in ("strong_candidate", "conditional_candidate", "keep_go", "insufficient_evidence")
        }
        ranked = sorted(
            (
                {
                    "file": item["file"],
                    "strong": round(100 * item["answers"]["zigSuitability"].get("probabilities", {}).get("strong_candidate", 0), 2),
                    "conditional": round(100 * item["answers"]["zigSuitability"].get("probabilities", {}).get("conditional_candidate", 0), 2),
                    "choice": item["answers"]["zigSuitability"].get("choice"),
                }
                for item in scored
            ),
            key=lambda item: item["strong"] + item["conditional"],
            reverse=True,
        )
        print(json.dumps({"averages": averages, "top_candidates": ranked[:15]}, indent=2))
        return 0
    load_env()
    if not os.environ.get("TYPESAFE_API_KEY"):
        print("TYPESAFE_API_KEY is not set", file=sys.stderr)
        return 2
    files = sorted(BACKEND.rglob("*.go"))
    results = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=int(os.environ.get("TYPESAFE_CONCURRENCY", "4"))) as pool:
        futures = {pool.submit(ask, state_for(path)): path for path in files}
        for future in concurrent.futures.as_completed(futures):
            path = futures[future]
            try:
                results.append({"file": path.relative_to(ROOT).as_posix(), "answers": future.result()})
            except Exception as error:  # noqa: BLE001 - preserve per-file failures
                results.append({"file": path.relative_to(ROOT).as_posix(), "error": str(error)})

    scored = [item for item in results if "answers" in item]
    counts = {key: sum(1 for item in scored if item["answers"].get("zigSuitability", {}).get("choice") == key) for key in ("strong_candidate", "conditional_candidate", "keep_go", "insufficient_evidence")}
    output = {"analyzed": len(files), "scored": len(scored), "failed": len(results) - len(scored), "counts": counts, "results": sorted(results, key=lambda item: item["file"])}
    output_path = Path(sys.argv[1]) if len(sys.argv) > 1 else None
    rendered = json.dumps(output, indent=2)
    if output_path:
        output_path.write_text(rendered + "\n", encoding="utf-8")
        print(json.dumps({key: output[key] for key in ("analyzed", "scored", "failed", "counts")}))
    else:
        print(rendered)
    return 0 if not output["failed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
