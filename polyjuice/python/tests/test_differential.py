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
    ["return x ** 2", "return x << 1", "return x / 2", "while x:\n        x -= 1\n    return x"],
)
def test_unsupported_syntax_is_rejected(body):
    with pytest.raises(UnsupportedSyntax):
        compile_to_c(f"def f(x: int) -> int:\n    {body}\n")


FIB = textwrap.dedent(
    """
    def fib(n: int) -> int:
        a = 0
        b = 1
        for i in range(n):
            a, b = b, a + b
        return a
    """
)

LOAD_BEFORE_STORE = textwrap.dedent(
    """
    def swap_first(a: Array[int, 4], v: int) -> int:
        old = a[0]
        a[0] = v
        return old + a[0] * 100
    """
)

MUTATE_PARAM = textwrap.dedent(
    """
    def countdown(n: int) -> int:
        start = n
        for i in range(start):
            n -= 1
        return start * 1000 + n
    """
)

NESTED = textwrap.dedent(
    """
    def tri(n: int) -> int:
        s = 0
        for i in range(n):
            for j in range(i):
                s += j
        return s
    """
)

LIMIT_CHANGES = textwrap.dedent(
    """
    def shrink(n: int) -> int:
        c = 0
        for i in range(n):
            n -= 1
            c += 1
        return c * 100 + n
    """
)

SECOND_LOOP = textwrap.dedent(
    """
    def twice(n: int) -> int:
        s = 0
        for i in range(n):
            s += i
        t = s
        s = 100
        for i in range(n):
            s += 2
        return t * 1000 + s
    """
)


@pytest.mark.parametrize("n", range(0, 12))
def test_fib_parallel_assignment(compile_c, n):
    lib = compile_c(FIB)
    lib.fib.restype = ctypes.c_int32
    assert lib.fib(n) == py_function(FIB)(n)


def test_load_is_not_moved_past_a_store(compile_c):
    lib = compile_c(LOAD_BEFORE_STORE)
    lib.swap_first.restype = ctypes.c_int32
    arr = (ctypes.c_int32 * 4)(7, 0, 0, 0)
    expected = py_function(LOAD_BEFORE_STORE)([7, 0, 0, 0], 3)
    assert lib.swap_first(arr, 3) == expected == 7 + 3 * 100


@pytest.mark.parametrize("n", [0, 1, 5])
def test_alias_of_a_variable_that_is_later_mutated(compile_c, n):
    lib = compile_c(MUTATE_PARAM)
    lib.countdown.restype = ctypes.c_int32
    assert lib.countdown(n) == py_function(MUTATE_PARAM)(n)


@pytest.mark.parametrize("n", [0, 1, 2, 7])
def test_nested_loops(compile_c, n):
    lib = compile_c(NESTED)
    lib.tri.restype = ctypes.c_int32
    assert lib.tri(n) == py_function(NESTED)(n)


@pytest.mark.parametrize("n", [0, 1, 6, 9])
def test_range_bound_is_evaluated_once(compile_c, n):
    lib = compile_c(LIMIT_CHANGES)
    lib.shrink.restype = ctypes.c_int32
    assert lib.shrink(n) == py_function(LIMIT_CHANGES)(n)


@pytest.mark.parametrize("n", [0, 3, 8])
def test_variable_reused_after_its_value_was_aliased(compile_c, n):
    lib = compile_c(SECOND_LOOP)
    lib.twice.restype = ctypes.c_int32
    assert lib.twice(n) == py_function(SECOND_LOOP)(n)
