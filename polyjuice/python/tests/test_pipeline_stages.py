"""Selection, detection, optimization, legalization, and the LLVM verification path."""

import json
import textwrap

import pytest

from polyjuice import verify
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.pipeline import TargetLegalityError, run
from test_differential import (
    CLAMP, FIB, FILL, LIMIT_CHANGES, LOAD_BEFORE_STORE, MUTATE_PARAM, NESTED, RANGE_SUM,
    SECOND_LOOP, SUM, py_function,
)  # fmt: skip

ensure_mlir_importable()
from mlir import ir  # noqa: E402


def notes(result, stage):
    return next(s.notes for s in result.stages if s.name == stage)


def test_selection_skips_detectors_that_cannot_match():
    result = run("def f(x: int) -> int:\n    return x + 1\n", "c")
    assert notes(result, "selection")["detectors"] == []
    report = json.loads(result.artifacts["04-selection.json"])
    assert report["detectors"]["skipped"]["reduction"] == "needs scf.for"
    assert report["dialects_used"] == ["arith", "func"]


def test_reduction_is_detected_and_tagged_in_the_ir():
    result = run(SUM, "c")
    assert notes(result, "detect")["findings"] == [
        {"function": "total", "op": "scf.for", "pattern": "reduction", "ops": ["add"]}
    ]
    assert 'polyjuice.pattern = "reduction"' in result.artifacts["05-detected.mlir"]
    assert "polyjuice.pattern" not in result.artifacts["02-frontend.mlir"]


def test_multiple_reductions_and_ops():
    result = run(
        "def f(n: int) -> int:\n    s = 0\n    p = 1\n    for i in range(n):\n        s += i\n        p *= 2\n    return s + p\n",
        "c",
    )
    (finding,) = notes(result, "detect")["findings"]
    # carried variables are ordered by name: p, then s
    assert finding["pattern"] == "reduction" and finding["ops"] == ["mul", "add"]


def test_fibonacci_is_not_a_reduction():
    # `a` is updated from `b` and `b` from `a`: not an accumulate-into-self loop.
    assert notes(run(FIB, "c"), "detect")["findings"] == []


def test_a_loop_that_reads_its_accumulator_is_not_a_reduction():
    result = run(
        "def f(n: int) -> int:\n    s = 1\n    for i in range(n):\n        s = s + s\n    return s\n", "c"
    )
    assert notes(result, "detect")["findings"] == []


def test_elementwise_map_is_detected():
    (finding,) = notes(run(FILL, "c"), "detect")["findings"]
    assert finding["pattern"] == "elementwise_map"


def test_stages_with_nothing_to_do_say_so():
    result = run(SUM, "c")
    assert notes(result, "optimize") == {"applied": [], "unused_patterns": ["reduction"]}
    assert notes(result, "legalize") == {"lowerings_applied": []}


def test_a_registered_optimization_runs_on_its_pattern():
    from polyjuice import pipeline

    seen = []
    pipeline.DOMAIN_OPTIMIZATIONS["reduction"] = lambda module: seen.append("ran")
    try:
        assert notes(run(SUM, "c"), "optimize")["applied"] == ["reduction"]
    finally:
        del pipeline.DOMAIN_OPTIMIZATIONS["reduction"]
    assert seen == ["ran"]


def test_illegal_ops_are_reported_not_emitted(monkeypatch):
    from polyjuice import languages

    narrow = languages.Emitter("c", "c", frozenset({"builtin.module", "func.func", "func.return"}), lambda m: "")
    monkeypatch.setitem(languages.EMITTERS, "c", narrow)
    with pytest.raises(TargetLegalityError, match="arith.addi"):
        run("def f(x: int) -> int:\n    return x + 1\n", "c")


def test_a_registered_lowering_makes_an_op_legal(monkeypatch):
    """Target without addition: a lowering rewrites `a + b` as `a - (0 - b)`."""
    from mlir.dialects import arith

    from polyjuice import languages, pipeline

    legal = frozenset({"builtin.module", "func.func", "func.return", "arith.constant", "arith.subi"})
    monkeypatch.setitem(languages.EMITTERS, "c", languages.Emitter("c", "c", legal, lambda m: "ok"))

    def lower_addi(module):
        for op in list(pipeline.detection.walk_ops(module.body)):
            if op.operation.name == "arith.addi":
                lhs, rhs = op.operands
                with ir.InsertionPoint(op):
                    negated = arith.subi(arith.constant(rhs.type, 0), rhs)
                    replacement = arith.subi(lhs, negated)
                op.results[0].replace_all_uses_with(replacement)
                op.operation.erase()

    source = "def f(x: int, y: int) -> int:\n    return x + y\n"
    with pytest.raises(TargetLegalityError, match="arith.addi"):
        run(source, "c")
    monkeypatch.setitem(pipeline.LOWERINGS, "arith.addi", lower_addi)
    result = run(source, "c")
    assert notes(result, "legalize")["lowerings_applied"] == ["arith.addi"]
    assert "arith.addi" not in result.artifacts["07-legalized.mlir"]


def test_every_stage_is_listed_with_its_file(tmp_path):
    run(SUM, "c", tmp_path)
    manifest = json.loads((tmp_path / "manifest.json").read_text())
    assert [s["name"] for s in manifest["stages"]] == [
        "source", "ast", "frontend", "cleanup", "selection", "detect", "optimize", "legalize", "emit",
    ]
    assert all((tmp_path / s["file"]).exists() for s in manifest["stages"])


def test_verify_writes_llvm_artifacts(tmp_path):
    run(SUM, "c", tmp_path, verify=True)
    assert "llvm.func @total" in (tmp_path / "verify/llvm-dialect.mlir").read_text()
    assert "define i32 @total" in (tmp_path / "verify/module.ll").read_text()


# --- LLVM differential: the shared-layer IR, lowered and executed, against Python ---------------

CORPUS = [
    ("total", SUM, [([3, -1, 4, 1, -5, 9, 2, 6], n) for n in (-3, 0, 1, 8)]),
    ("range_sum", RANGE_SUM, [(lo, hi) for lo, hi in [(0, 10), (-4, 4), (5, 5), (7, 2), (-9, -3)]]),
    ("clamp", CLAMP, [(x, -3, 10) for x in (-100, -3, 0, 5, 10, 11, 100)]),
    ("fib", FIB, [(n,) for n in range(0, 12)]),
    ("countdown", MUTATE_PARAM, [(n,) for n in (0, 1, 5)]),
    ("tri", NESTED, [(n,) for n in (0, 1, 2, 7)]),
    ("shrink", LIMIT_CHANGES, [(n,) for n in (0, 1, 6, 9)]),
    ("twice", SECOND_LOOP, [(n,) for n in (0, 3, 8)]),
]


@pytest.mark.parametrize("name,source,cases", CORPUS, ids=[c[0] for c in CORPUS])
def test_llvm_execution_matches_python(name, source, cases):
    from polyjuice.frontend import build_module

    context = ir.Context()
    module = build_module(source, context)
    with context:
        lowered = verify.lower_to_llvm_dialect(module)
        for args in cases:
            expected = py_function(source)(*[list(a) if isinstance(a, list) else a for a in args])
            actual, _ = verify.execute(lowered, name, [list(a) if isinstance(a, list) else a for a in args])
            assert actual == expected, (name, args)


def test_llvm_execution_sees_array_writes():
    from polyjuice.frontend import build_module

    context = ir.Context()
    module = build_module(FILL, context)
    with context:
        lowered = verify.lower_to_llvm_dialect(module)
        result, (array,) = verify.execute(lowered, "fill", [[0] * 8, 10])
    assert result == 17 and list(array) == [10 + i for i in range(8)]


def test_llvm_execution_wraps_like_i32():
    """The shared layer's i32 wraps; this is what the emitted C and Python can disagree on."""
    from polyjuice.frontend import build_module

    context = ir.Context()
    module = build_module("def f(x: int) -> int:\n    return x + 1\n", context)
    with context:
        lowered = verify.lower_to_llvm_dialect(module)
        assert verify.execute(lowered, "f", [2**31 - 1])[0] == -(2**31)
