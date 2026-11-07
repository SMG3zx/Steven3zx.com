"""Shared-layer step 2: floats, while loops, calls. Each program runs four ways:
original Python, emitted C, emitted Python, and the shared-layer IR executed by LLVM."""

import ctypes
import textwrap

import pytest

from polyjuice import verify
from polyjuice.frontend import UnsupportedSyntax, build_module
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.pipeline import run

ensure_mlir_importable()
from mlir import ir  # noqa: E402

MEAN = textwrap.dedent(
    """
    def mean(a: Array[float, 4]) -> float:
        s = 0.0
        for i in range(4):
            s += a[i]
        return s / 4.0
    """
)
CASTS = textwrap.dedent(
    """
    def avg(total: int, n: int) -> float:
        return float(total) / float(n)

    def trunc(x: float) -> int:
        return int(x)

    def hypot2(x: float, y: float) -> float:
        return -(x * x + y * y)
    """
)
GCD = textwrap.dedent(
    """
    def gcd(a: int, b: int) -> int:
        while a != b:
            if a > b:
                a = a - b
            else:
                b = b - a
        return a
    """
)
COUNT = textwrap.dedent(
    """
    def count_up(n: int) -> int:
        i = 0
        steps = 0
        while i < n:
            i += 1
            steps += 2
        return i * 100 + steps
    """
)
CALLS = textwrap.dedent(
    """
    def square(x: int) -> int:
        return x * x

    def sum_squares(n: int) -> int:
        s = 0
        for i in range(n):
            s += square(i)
        return s

    def fact(n: int) -> int:
        r = 1
        if n > 1:
            r = n * fact(n - 1)
        return r

    def tick(a: Array[int, 1]) -> int:
        v = a[0] + 1
        a[0] = v
        return v

    def order(a: Array[int, 1]) -> int:
        return tick(a) * 10 + tick(a)

    def bump(a: Array[int, 1], by: int):
        a[0] = a[0] + by

    def twice(a: Array[int, 1]) -> int:
        bump(a, 5)
        bump(a, 7)
        return a[0]
    """
)


def py_named(source: str, name: str):
    namespace: dict = {}
    exec(source.replace("Array[float, 4]", "list").replace("Array[int, 1]", "list"), namespace)
    return namespace[name]


def emitted_python(source: str, name: str):
    namespace: dict = {}
    exec(run(source, "python").output, namespace)
    return namespace[name]


def llvm_call(source: str, name: str, args: list, result=ctypes.c_int32):
    context = ir.Context()
    module = build_module(source, context)
    with context:
        return verify.execute(verify.lower_to_llvm_dialect(module), name, args, result)


# (source, function, argument tuples, ctypes result type)
CASES = [
    (MEAN, "mean", [([1.5, 2.5, -3.0, 10.25],), ([0.0] * 4,)], ctypes.c_double),
    (CASTS, "avg", [(7, 2), (10, 4), (-9, 2)], ctypes.c_double),
    (CASTS, "trunc", [(2.9,), (-2.9,), (0.0,), (1e9,)], ctypes.c_int32),
    (CASTS, "hypot2", [(3.0, 4.0), (0.5, -0.25)], ctypes.c_double),
    (GCD, "gcd", [(12, 18), (7, 7), (35, 14), (1, 9)], ctypes.c_int32),
    (COUNT, "count_up", [(0,), (1,), (9,)], ctypes.c_int32),
    (CALLS, "sum_squares", [(0,), (1,), (6,)], ctypes.c_int32),
    (CALLS, "fact", [(0,), (1,), (5,), (10,)], ctypes.c_int32),
    (CALLS, "order", [([0],)], ctypes.c_int32),
    (CALLS, "twice", [([100],)], ctypes.c_int32),
]
IDS = [f"{c[1]}" for c in CASES]


@pytest.mark.parametrize("source,name,calls,result", CASES, ids=IDS)
def test_llvm_matches_python(source, name, calls, result):
    for args in calls:
        expected = py_named(source, name)(*[list(a) if isinstance(a, list) else a for a in args])
        actual, _ = llvm_call(source, name, [list(a) if isinstance(a, list) else a for a in args], result)
        assert actual == expected, (name, args)


@pytest.mark.parametrize("source,name,calls,result", CASES, ids=IDS)
def test_emitted_python_matches_python(source, name, calls, result):
    for args in calls:
        expected = py_named(source, name)(*[list(a) if isinstance(a, list) else a for a in args])
        actual = emitted_python(source, name)(*[list(a) if isinstance(a, list) else a for a in args])
        assert actual == expected, (name, args)


@pytest.mark.parametrize("source,name,calls,result", CASES, ids=IDS)
def test_emitted_c_matches_python(compile_c, source, name, calls, result):
    lib = compile_c(source)
    # `trunc` collides with libm, so the C emitter renames it to `trunc_`.
    function = getattr(lib, name + "_" if name == "trunc" else name)
    function.restype = result
    for args in calls:
        expected = py_named(source, name)(*[list(a) if isinstance(a, list) else a for a in args])
        c_args, keep = [], []
        for a in args:
            if isinstance(a, list):
                element = ctypes.c_double if any(isinstance(x, float) for x in a) else ctypes.c_int32
                arr = (element * len(a))(*a)
                keep.append(arr)
                c_args.append(arr)
            elif isinstance(a, float):
                c_args.append(ctypes.c_double(a))
            else:
                c_args.append(ctypes.c_int32(a))
        assert function(*c_args) == expected, (name, args)


# --- output shape ----------------------------------------------------------------------------


def test_while_loop_reads_like_c_and_python():
    c = run(GCD, "c").output
    assert "while (a != b) {" in c and "} else {" in c
    py = run(GCD, "python").output
    assert "while a != b:" in py and "if a > b:" in py


def test_calls_are_plain_calls_and_float_loops_use_double():
    c = run(CALLS, "c").output
    assert "s += square(i);" in c
    assert "r = n * fact(n - 1);" in c
    assert "double mean(double a[4])" in run(MEAN, "c").output
    assert "(double)total / (double)n" in run(CASTS, "c").output
    assert "float(total) / float(n)" in run(CASTS, "python").output


def test_two_calls_in_one_expression_are_sequenced_in_c():
    c = run(CALLS, "c").output
    order = c[c.index("int32_t order"):]
    # tick(a) * 10 + tick(a) would leave the order to the C compiler: each call gets its own
    # statement, first call first.
    assert order.count("tick(a)") == 2
    assert order.index("t0 = tick(a) * 10;") < order.index("t1 = tick(a);") < order.index("return t0 + t1;")


def test_a_call_used_only_for_its_effect_is_a_statement():
    assert "bump(a, 5);" in run(CALLS, "c").output
    assert "bump(a, 5)\n" in run(CALLS, "python").output


def test_gcd_is_not_mistaken_for_a_reduction():
    from polyjuice.pipeline import run as pipeline_run

    result = pipeline_run(GCD, "c")
    assert next(s.notes for s in result.stages if s.name == "detect")["findings"] == []


# --- things that must be refused, not miscompiled ---------------------------------------------


@pytest.mark.parametrize(
    "body,message",
    [
        ("return x + 1.0", "mixed types"),
        ("return x / 2", "unsupported operator"),
        ("return x // 2", "unsupported operator"),
        ("while x:\n        x = x - 1\n    return x", "condition must be a comparison"),
        ("if x:\n        x = 1\n    return x", "condition must be a comparison"),
        ("return missing(x)", "unknown function"),
        ("return float(1.5)", "int, got|cannot convert|returned value"),
    ],
)
def test_type_errors_are_unsupported_syntax(body, message):
    with pytest.raises(UnsupportedSyntax, match=message):
        run(f"def f(x: int) -> int:\n    {body}\n", "c")


def test_wrong_argument_type_is_refused():
    with pytest.raises(UnsupportedSyntax, match="expects"):
        run("def g(x: float) -> float:\n    return x\n\ndef f(n: int) -> float:\n    return g(n)\n", "c")


def test_variable_cannot_change_type_inside_a_loop():
    with pytest.raises(UnsupportedSyntax, match="changes type"):
        run("def f(n: int) -> int:\n    s = 0\n    for i in range(n):\n        s = 1.5\n    return 0\n", "c")


def test_function_names_that_collide_with_libc_are_renamed():
    out = run("def trunc(x: float) -> int:\n    return int(x)\n", "c").output
    assert "int32_t trunc_(double x)" in out
    assert "def trunc(" in run("def trunc(x: float) -> int:\n    return int(x)\n", "python").output


def test_gcd_updates_loop_variables_in_place():
    assert run(GCD, "c").output == textwrap.dedent(
        """\
        #include <stdint.h>

        int32_t gcd(int32_t a, int32_t b) {
            while (a != b) {
                if (a > b) {
                    a -= b;
                } else {
                    b -= a;
                }
            }
            return a;
        }
        """
    )
