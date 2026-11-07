"""python -> python round trip: emit idiomatic Python and run it against the original."""

import textwrap

import pytest

from polyjuice.pipeline import run
from test_differential import (
    CLAMP, FIB, FILL, LIMIT_CHANGES, LOAD_BEFORE_STORE, MUTATE_PARAM, NESTED, RANGE_SUM,
    SECOND_LOOP, SUM, py_function,
)  # fmt: skip


def emitted(source: str):
    namespace: dict = {}
    exec(run(source, "python").output, namespace)
    return next(v for k, v in namespace.items() if callable(v) and k != "__builtins__")


def test_round_trip_array_sum():
    data = [3, -1, 4, 1, -5, 9, 2, 6]
    for n in range(-2, 9):
        assert emitted(SUM)(list(data), n) == py_function(SUM)(list(data), n)


@pytest.mark.parametrize("lo,hi", [(0, 10), (-4, 4), (5, 5), (7, 2)])
def test_round_trip_range(lo, hi):
    assert emitted(RANGE_SUM)(lo, hi) == py_function(RANGE_SUM)(lo, hi)


@pytest.mark.parametrize("x", [-100, -3, 0, 5, 10, 11, 100])
def test_round_trip_if_chain(x):
    assert emitted(CLAMP)(x, -3, 10) == py_function(CLAMP)(x, -3, 10)


@pytest.mark.parametrize("n", range(0, 10))
def test_round_trip_parallel_assignment(n):
    assert emitted(FIB)(n) == py_function(FIB)(n)


def test_round_trip_store_and_load_order():
    a, b = [7, 0, 0, 0], [7, 0, 0, 0]
    assert emitted(LOAD_BEFORE_STORE)(a, 3) == py_function(LOAD_BEFORE_STORE)(b, 3)
    assert a == b
    c, d = [0] * 8, [0] * 8
    assert emitted(FILL)(c, 10) == py_function(FILL)(d, 10) and c == d


@pytest.mark.parametrize("source,args", [
    (MUTATE_PARAM, (5,)), (NESTED, (6,)), (LIMIT_CHANGES, (9,)), (SECOND_LOOP, (8,)),
])  # fmt: skip
def test_round_trip_hazard_programs(source, args):
    assert emitted(source)(*args) == py_function(source)(*args)


def test_output_reads_like_python():
    out = run(SUM, "python").output
    assert out == textwrap.dedent(
        """\
        def total(a: list[int], n: int) -> int:
            s = 0
            for i in range(n):
                s += a[i]
            return s
        """
    )
    assert "range(" in run(RANGE_SUM, "python").output
    assert "range(lo, hi)" in run(RANGE_SUM, "python").output


def test_empty_if_branch_gets_pass():
    out = run("def f(x: int) -> int:\n    r = x\n    if x > 0:\n        r = r\n    return r\n", "python").output
    compile(out, "<emitted>", "exec")


# Names that are legal Python but would break the emitted code in one target.
RESERVED = "def f(list: int, char: int) -> int:\n    s = 0\n    for i in range(list):\n        s += char\n    return s\n"


def test_reserved_names_are_renamed_in_python():
    out = run(RESERVED, "python").output
    assert "list_" in out and "char" in out
    namespace: dict = {}
    exec(out, namespace)
    assert namespace["f"](4, 3) == 12


def test_reserved_names_are_renamed_in_c(compile_c):
    import ctypes

    out = run(RESERVED, "c").output
    assert "char_" in out and "list" in out
    lib = compile_c(RESERVED)
    lib.f.restype = ctypes.c_int32
    assert lib.f(4, 3) == 12
