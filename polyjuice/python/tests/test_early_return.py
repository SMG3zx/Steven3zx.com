"""Early returns: lowered to a returning scf.if, emitted as guard clauses."""

import ctypes
import textwrap

import pytest

from polyjuice.nast import UnsupportedSyntax
from polyjuice.pipeline import run

FACT = textwrap.dedent(
    """
    def fact(n: int) -> int:
        if n <= 1:
            return 1
        return n * fact(n - 1)
    """
)
SIGN = textwrap.dedent(
    """
    def sign(x: int) -> int:
        if x < 0:
            return -1
        elif x == 0:
            return 0
        return 1
    """
)
FIND = textwrap.dedent(
    """
    def clamp_sum(a: int, b: int, limit: int) -> int:
        s = a + b
        if s > limit:
            return limit
        t = s * 2
        if t > limit:
            return t - limit
        return t
    """
)
SHOW = textwrap.dedent(
    """
    def bump(a: Array[int, 2], i: int):
        if i < 0:
            return
        a[i] = a[i] + 1
    """
)


def py_named(source, name):
    namespace: dict = {}
    exec(source.replace("Array[int, 2]", "list"), namespace)
    return namespace[name]


def test_guard_clause_shape_in_c_and_python():
    assert run(FACT, "c").output == textwrap.dedent(
        """\
        #include <stdint.h>

        int32_t fact(int32_t n) {
            if (n <= 1) {
                return 1;
            }
            return n * fact(n - 1);
        }
        """
    )
    assert run(FACT, "python").output == textwrap.dedent(
        """\
        def fact(n: int) -> int:
            if n <= 1:
                return 1
            return n * fact(n - 1)
        """
    )


def test_elif_chain_of_returns_is_a_flat_sequence_of_guards():
    assert run(SIGN, "python").output == textwrap.dedent(
        """\
        def sign(x: int) -> int:
            if x < 0:
                return -1
            if x == 0:
                return 0
            return 1
        """
    )


def test_code_between_guards_is_kept_in_order():
    out = run(FIND, "c").output
    assert out.index("int32_t s = a + b;") < out.index("return limit;") < out.index("int32_t t = s * 2;")


def test_void_function_early_return():
    c = run(SHOW, "c").output
    assert "void bump" in c and "return;" in c and "a[i] = a[i] + 1;" in c
    assert not c.rstrip().endswith("return;\n}")


@pytest.mark.parametrize("n", range(0, 8))
def test_fact_agrees_everywhere(compile_c, n):
    lib = compile_c(FACT)
    lib.fact.restype = ctypes.c_int32
    assert lib.fact(n) == py_named(FACT, "fact")(n)


@pytest.mark.parametrize("x", [-5, 0, 7])
def test_sign_and_clamp_agree(compile_c, x):
    lib = compile_c(SIGN)
    lib.sign.restype = ctypes.c_int32
    assert lib.sign(x) == py_named(SIGN, "sign")(x)
    lib = compile_c(FIND)
    lib.clamp_sum.restype = ctypes.c_int32
    for limit in (3, 10, 40):
        assert lib.clamp_sum(x, 4, limit) == py_named(FIND, "clamp_sum")(x, 4, limit)


def test_emitted_python_agrees():
    ns: dict = {}
    exec(run(FIND, "python").output, ns)
    for a, b, limit in [(1, 2, 100), (30, 30, 10), (3, 4, 10)]:
        assert ns["clamp_sum"](a, b, limit) == py_named(FIND, "clamp_sum")(a, b, limit)


def test_missing_return_is_refused():
    with pytest.raises(UnsupportedSyntax, match="does not return|returns a value"):
        run("def f(x: int) -> int:\n    if x > 0:\n        return 1\n", "c")


LOOP_RETURN = textwrap.dedent(
    """
    def first_multiple(n: int, k: int) -> int:
        for i in range(1, n):
            if i * k > 50:
                return i
        return -1

    def nested(n: int) -> int:
        for i in range(n):
            for j in range(n):
                if i * j == 12:
                    return i * 100 + j
        return 0

    def stop_early(n: int):
        total = 0
        for i in range(n):
            if i > 3:
                return
            total += i
    """
)


@pytest.mark.parametrize("args", [(100, 3), (5, 3), (100, 100), (2, 60)])
def test_return_inside_a_loop(compile_c, args):
    expected = py_named(LOOP_RETURN, "first_multiple")(*args)
    lib = compile_c(LOOP_RETURN)
    lib.first_multiple.restype = ctypes.c_int32
    assert lib.first_multiple(*args) == expected
    ns: dict = {}
    exec(run(LOOP_RETURN, "python").output, ns)
    assert ns["first_multiple"](*args) == expected


@pytest.mark.parametrize("n", [0, 3, 4, 8])
def test_return_inside_nested_loops(compile_c, n):
    expected = py_named(LOOP_RETURN, "nested")(n)
    lib = compile_c(LOOP_RETURN)
    lib.nested.restype = ctypes.c_int32
    assert lib.nested(n) == expected
    ns: dict = {}
    exec(run(LOOP_RETURN, "python").output, ns)
    assert ns["nested"](n) == expected


def test_return_inside_a_loop_through_llvm():
    from polyjuice import verify
    from polyjuice.frontend import build_module
    from polyjuice.mlir_env import ensure_mlir_importable

    ensure_mlir_importable()
    from mlir import ir

    context = ir.Context()
    module = build_module(LOOP_RETURN, context)
    with context:
        lowered = verify.lower_to_llvm_dialect(module)
        for args in [(100, 3), (5, 3), (2, 60)]:
            assert verify.execute(lowered, "first_multiple", list(args))[0] == py_named(LOOP_RETURN, "first_multiple")(*args)
        for n in (0, 4, 8):
            assert verify.execute(lowered, "nested", [n])[0] == py_named(LOOP_RETURN, "nested")(n)
