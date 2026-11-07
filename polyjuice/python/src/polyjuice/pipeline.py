"""Source -> shared layer -> target source, writing one artifact per stage.

    00-source.<ext>       the input, verbatim
    01-ast.txt            Python AST dump (Python sources only)
    02-frontend.mlir      verified IR straight from the frontend
    03-cleaned.mlir       dead code and folds removed, structure kept
    04-selection.json     dialects used; which detectors and passes apply
    05-detected.mlir      IR tagged with `polyjuice.pattern` by the detectors
    06-optimized.mlir     after domain optimizations keyed on the tags
    07-legalized.mlir     after lowering whatever the target cannot express
    08-output.<ext>       emitted target source
    verify/               with verify=True: LLVM dialect and LLVM IR (a check, not an output)
    manifest.json         stage list with timings, sizes and per-stage notes

Domain optimizations and target lowerings are registries that are empty today:
the stages run, record that nothing applied, and pass the IR through.
"""

import ast
import json
import time
from collections import Counter
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path

from polyjuice import detect as detection
from polyjuice import verify as verification
from polyjuice.languages import resolve
from polyjuice.mlir_env import ensure_mlir_importable

ensure_mlir_importable()

from mlir import ir, passmanager  # noqa: E402

# Dialects every program may rely on (DESIGN.md, "core, always loaded").
CORE_DIALECTS = ["arith", "builtin", "cf", "func", "index", "math", "memref", "scf", "ub"]

# pattern -> pass over the module. Empty until a domain optimization exists.
DOMAIN_OPTIMIZATIONS: dict[str, Callable[[ir.Module], None]] = {}

# op name -> rewrite that lowers it into ops a target can express. Empty for now.
LOWERINGS: dict[str, Callable[[ir.Module], None]] = {}


class TargetLegalityError(Exception):
    pass


@dataclass
class StageRecord:
    name: str
    file: str
    seconds: float
    bytes: int
    notes: dict = field(default_factory=dict)


@dataclass
class PipelineResult:
    output: str
    stages: list[StageRecord] = field(default_factory=list)
    artifacts: dict[str, str] = field(default_factory=dict)  # relative path -> text


def op_counts(module: ir.Module) -> Counter:
    return Counter(op.operation.name for op in detection.walk_ops(module.body))


def run(
    source: str,
    target: str = "c",
    out_dir: str | Path | None = None,
    source_language: str = "python",
    verify: bool = False,
) -> PipelineResult:
    frontend_spec, emitter_spec = resolve(source_language, target)
    result = PipelineResult(output="")
    context = ir.Context()
    state: dict = {}

    def stage(name: str, file: str, fn: Callable[[], str | tuple[str, dict]]) -> None:
        start = time.perf_counter()
        produced = fn()
        elapsed = time.perf_counter() - start
        text, notes = produced if isinstance(produced, tuple) else (produced, {})
        result.artifacts[file] = text
        result.stages.append(StageRecord(name, file, elapsed, len(text.encode()), notes))

    def module() -> ir.Module:
        return state["module"]

    def frontend() -> str:
        state["module"] = frontend_spec.build_module(source, context)
        return str(module())

    def cleanup() -> str:
        # Patterns stay off: full canonicalization rewrites scf.if into arith.select
        # and would erase the structure the emitters rely on.
        pm = passmanager.PassManager.parse(
            "builtin.module(canonicalize{enable-patterns=false})", context
        )
        pm.run(module().operation)
        module().operation.verify()
        return str(module())

    def selection() -> tuple[str, dict]:
        counts = op_counts(module())
        used = sorted({name.split(".")[0] for name in counts})
        chosen, skipped = [], {}
        for detector in detection.DETECTORS:
            missing = sorted(detector.requires - counts.keys())
            if missing:
                skipped[detector.name] = f"needs {', '.join(missing)}"
            else:
                chosen.append(detector)
        state["detectors"] = chosen
        report = {
            "source": source_language,
            "target": target,
            "dialects_used": used,
            "dialects_loaded": sorted(set(CORE_DIALECTS) | set(used)),
            "ops": dict(sorted(counts.items())),
            "detectors": {"selected": [d.name for d in chosen], "skipped": skipped},
            "domain_optimizations": {"available": sorted(DOMAIN_OPTIMIZATIONS)},
        }
        return json.dumps(report, indent=2) + "\n", {"detectors": report["detectors"]["selected"]}

    def detected() -> tuple[str, dict]:
        findings = detection.detect(module(), state["detectors"])
        module().operation.verify()
        state["findings"] = findings
        return str(module()), {"findings": findings}

    def optimized() -> tuple[str, dict]:
        patterns = {f["pattern"] for f in state["findings"]}
        applied = []
        for pattern in sorted(patterns & DOMAIN_OPTIMIZATIONS.keys()):
            DOMAIN_OPTIMIZATIONS[pattern](module())
            applied.append(pattern)
        module().operation.verify()
        return str(module()), {"applied": applied, "unused_patterns": sorted(patterns - set(applied))}

    def legalized() -> tuple[str, dict]:
        applied = []
        for op_name in sorted(set(op_counts(module())) - emitter_spec.legal_ops):
            if op_name in LOWERINGS:
                LOWERINGS[op_name](module())
                applied.append(op_name)
        illegal = sorted(set(op_counts(module())) - emitter_spec.legal_ops)
        if illegal:
            raise TargetLegalityError(f"ops not expressible in {target}: {', '.join(illegal)}")
        module().operation.verify()
        return str(module()), {"lowerings_applied": applied}

    def emit() -> str:
        result.output = emitter_spec.emit(module())
        return result.output

    with context, ir.Location.unknown():
        stage("source", f"00-source.{frontend_spec.extension}", lambda: source)
        if source_language == "python":
            stage("ast", "01-ast.txt", lambda: ast.dump(ast.parse(source), indent=2) + "\n")
        stage("frontend", "02-frontend.mlir", frontend)
        stage("cleanup", "03-cleaned.mlir", cleanup)
        stage("selection", "04-selection.json", selection)
        stage("detect", "05-detected.mlir", detected)
        stage("optimize", "06-optimized.mlir", optimized)
        stage("legalize", "07-legalized.mlir", legalized)
        stage("emit", f"08-output.{emitter_spec.extension}", emit)
        if verify:
            lowered = verification.lower_to_llvm_dialect(module())
            stage("verify-llvm-dialect", "verify/llvm-dialect.mlir", lambda: str(lowered))
            stage("verify-llvm-ir", "verify/module.ll", lambda: verification.to_llvm_ir(lowered))

    manifest = {
        "source": source_language,
        "target": target,
        "stages": [vars(s) for s in result.stages],
    }
    result.artifacts["manifest.json"] = json.dumps(manifest, indent=2) + "\n"
    if out_dir is not None:
        directory = Path(out_dir)
        for file, text in result.artifacts.items():
            (directory / file).parent.mkdir(parents=True, exist_ok=True)
            (directory / file).write_text(text)
    return result


def compile_to_c(source: str, out_dir: str | Path | None = None) -> str:
    return run(source, "c", out_dir).output
