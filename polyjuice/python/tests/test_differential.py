"""Run each program as Python and as compiled C, and compare the results."""

import ctypes
import textwrap

import pytest

from polyjuice import compile_to_c
from polyjuice.frontend import UnsupportedSyntax

SUM = textwrap.dedent(
    """
    def total(a: Array[int, 8], n: int) -> int:
        s = 0
        for i in range(n):
            s += a[i]
        return s
    """
)

RANGE_SUM = textwrap.dedent(
    """
    def range_sum(lo: int, hi: int) -> int:
        s = 0
        for i in range(lo, hi):
            s += i * i
        return s
    """
)

CLAMP = textwrap.dedent(
    """
    def clamp(x: int, lo: int, hi: int) -> int:
        r = x
        if x < lo:
            r = lo
        else:
            if x > hi:
                r = hi
        return r
    """
)

FILL = textwrap.dedent(
    """
    def fill(a: Array[int, 8], v: int) -> int:
        for i in range(8):
            a[i] = v + i
        return a[7]
    """
)


def py_function(source: str):
    namespace: dict = {}
    exec(source.replace("Array[int, 8]", "list"), namespace)
    return next(v for k, v in namespace.items() if callable(v) and k != "__builtins__")


def test_array_sum_matches_python(compile_c):
    lib = compile_c(SUM)
    lib.total.restype = ctypes.c_int32
    data = [3, -1, 4, 1, -5, 9, 2, 6]
    arr = (ctypes.c_int32 * 8)(*data)
    expected = py_function(SUM)
    for n in range(0, 9):
        assert lib.total(arr, n) == expected(data, n)


@pytest.mark.parametrize("n", [-5, -1])
def test_negative_bound_runs_zero_iterations(compile_c, n):
    lib = compile_c(SUM)
    lib.total.restype = ctypes.c_int32
    arr = (ctypes.c_int32 * 8)(*range(8))
    assert lib.total(arr, n) == 0 == py_function(SUM)(list(range(8)), n)


@pytest.mark.parametrize("lo,hi", [(0, 10), (-4, 4), (5, 5), (7, 2), (-9, -3)])
def test_range_with_lower_bound(compile_c, lo, hi):
    lib = compile_c(RANGE_SUM)
    lib.range_sum.restype = ctypes.c_int32
    assert lib.range_sum(lo, hi) == py_function(RANGE_SUM)(lo, hi)


@pytest.mark.parametrize("x", [-100, -3, 0, 5, 10, 11, 100])
def test_if_else_with_carried_value(compile_c, x):
    lib = compile_c(CLAMP)
    lib.clamp.restype = ctypes.c_int32
    assert lib.clamp(x, -3, 10) == py_function(CLAMP)(x, -3, 10)


def test_array_store(compile_c):
    lib = compile_c(FILL)
    lib.fill.restype = ctypes.c_int32
    arr = (ctypes.c_int32 * 8)()
    assert lib.fill(arr, 10) == 17
    assert list(arr) == [10 + i for i in range(8)]


@pytest.mark.parametrize(
    "body",
    ["return x // 2", "return x % 2", "return x / 2", "while x:\n        x -= 1\n    return x"],
)
def test_unsupported_syntax_is_rejected(body):
    with pytest.raises(UnsupportedSyntax):
        compile_to_c(f"def f(x: int) -> int:\n    {body}\n")
