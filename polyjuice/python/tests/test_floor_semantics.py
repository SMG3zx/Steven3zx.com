"""`//`, `%` and int `/` follow Python and Lua: floor division, divisor-signed remainder, true division."""

import ctypes

import pytest

from polyjuice import verify
from polyjuice.frontends.python import build_module
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.pipeline import run

ensure_mlir_importable()
from mlir import ir  # noqa: E402

SRC = (
    "def fdiv(a: int, b: int) -> int:\n    return a // b\n\n"
    "def floor_mod(a: int, b: int) -> int:\n    return a % b\n\n"
    "def tdiv(a: int, b: int) -> float:\n    return a / b\n"
)
PAIRS = [(7, 2), (-7, 2), (7, -2), (-7, -2), (6, 3), (-6, 3), (0, 5)]


@pytest.mark.parametrize("a,b", PAIRS)
def test_emitted_python_matches_python(a, b):
    ns: dict = {}
    out = run(SRC, "python").output
    exec(out, ns)
    assert (ns["fdiv"](a, b), ns["floor_mod"](a, b), ns["tdiv"](a, b)) == (a // b, a % b, a / b)


def test_emitted_python_uses_the_real_operators():
    out = run(SRC, "python").output
    assert "return a // b" in out and "return a % b" in out and "return a / b" not in out
    assert "float(a) / float(b)" in out


@pytest.mark.parametrize("a,b", PAIRS)
def test_emitted_c_matches_python(compile_c, a, b):
    lib = compile_c(SRC)
    lib.tdiv.restype = ctypes.c_double
    lib.fdiv.restype = lib.floor_mod.restype = ctypes.c_int32
    assert (lib.fdiv(a, b), lib.floor_mod(a, b), lib.tdiv(a, b)) == (a // b, a % b, a / b)


@pytest.mark.parametrize("a,b", PAIRS)
def test_llvm_matches_python(a, b):
    context = ir.Context()
    module = build_module(SRC, context)
    with context:
        lowered = verify.lower_to_llvm_dialect(module)
        assert verify.execute(lowered, "fdiv", [a, b])[0] == a // b
        assert verify.execute(lowered, "floor_mod", [a, b])[0] == a % b
        assert verify.execute(lowered, "tdiv", [a, b], ctypes.c_double)[0] == a / b


def test_a_call_in_a_floor_operand_is_evaluated_once():
    src = "def g(x: int) -> int:\n    return x\n\ndef f(a: int) -> int:\n    return g(a) % 3\n"
    out = run(src, "c").output
    assert out.count("g(a)") == 1
    ns: dict = {}
    exec(run(src, "python").output, ns)
    assert ns["f"](-7) == (-7) % 3
