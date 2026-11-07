"""Source -> MLIR -> target source, writing one artifact per stage.

    00-source.py        the input, verbatim
    01-ast.txt          Python AST dump
    02-frontend.mlir    verified IR straight from the frontend
    03-optimized.mlir   after structure-preserving cleanup
    04-dialects.json    dialects/ops used, and whether the target can express them
    05-output.c         emitted target source
    manifest.json       stage list with timings and sizes

Detection and domain optimization are not wired in yet; when they are, they get
their own numbered stages between 03 and 04.
"""

import ast
import json
import time
from collections import Counter
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path

from polyjuice.emit_c import LEGAL_OPS as C_LEGAL_OPS
from polyjuice.emit_c import emit_c
from polyjuice.frontend import build_module
from polyjuice.mlir_env import ensure_mlir_importable

ensure_mlir_importable()

from mlir import ir, passmanager  # noqa: E402

TARGETS = {"c": {"extension": "c", "legal_ops": C_LEGAL_OPS, "emit": emit_c}}


class TargetLegalityError(Exception):
    pass


@dataclass
class StageRecord:
    name: str
    file: str
    seconds: float
    bytes: int


@dataclass
class PipelineResult:
    output: str
    stages: list[StageRecord] = field(default_factory=list)
    artifacts: dict[str, str] = field(default_factory=dict)  # file name -> text


def walk_ops(module: ir.Module):
    def visit(block):
        for op in block.operations:
            yield op
            for region in op.regions:
                for inner in region.blocks:
                    yield from visit(inner)

    yield from visit(module.body)


def analyze_dialects(module: ir.Module, target: str) -> dict:
    ops = Counter(op.operation.name for op in walk_ops(module))
    legal = TARGETS[target]["legal_ops"]
    return {
        "target": target,
        "dialects": sorted({name.split(".")[0] for name in ops}),
        "ops": dict(sorted(ops.items())),
        "illegal_for_target": sorted(name for name in ops if name not in legal),
    }


def run(source: str, target: str = "c", out_dir: str | Path | None = None) -> PipelineResult:
    spec = TARGETS[target]
    result = PipelineResult(output="")
    context = ir.Context()
    state: dict = {}

    def stage(name: str, file: str, fn: Callable[[], str]) -> None:
        start = time.perf_counter()
        text = fn()
        elapsed = time.perf_counter() - start
        result.artifacts[file] = text
        result.stages.append(StageRecord(name, file, elapsed, len(text.encode())))

    def frontend() -> str:
        state["module"] = build_module(source, context)
        return str(state["module"])

    def optimize() -> str:
        # Patterns stay off for now: full canonicalization rewrites scf.if into
        # arith.select and would erase the structure the emitters rely on.
        pm = passmanager.PassManager.parse(
            "builtin.module(canonicalize{enable-patterns=false})", context
        )
        pm.run(state["module"].operation)
        state["module"].operation.verify()
        return str(state["module"])

    def dialects() -> str:
        report = analyze_dialects(state["module"], target)
        if report["illegal_for_target"]:
            raise TargetLegalityError(
                f"ops not expressible in {target}: {', '.join(report['illegal_for_target'])}"
            )
        return json.dumps(report, indent=2) + "\n"

    def emit() -> str:
        result.output = spec["emit"](state["module"])
        return result.output

    with context, ir.Location.unknown():
        stage("source", "00-source.py", lambda: source)
        stage("ast", "01-ast.txt", lambda: ast.dump(ast.parse(source), indent=2) + "\n")
        stage("frontend", "02-frontend.mlir", frontend)
        stage("optimize", "03-optimized.mlir", optimize)
        stage("dialects", "04-dialects.json", dialects)
        stage("emit", f"05-output.{spec['extension']}", emit)

    manifest = {
        "target": target,
        "stages": [vars(s) for s in result.stages],
    }
    result.artifacts["manifest.json"] = json.dumps(manifest, indent=2) + "\n"
    if out_dir is not None:
        directory = Path(out_dir)
        directory.mkdir(parents=True, exist_ok=True)
        for file, text in result.artifacts.items():
            (directory / file).write_text(text)
    return result


def compile_to_c(source: str, out_dir: str | Path | None = None) -> str:
    return run(source, "c", out_dir).output
