"""C frontend: the original (gcc) and every pipeline built from it agree with the reference."""

import pytest

import harness
from polyjuice.frontends.c import build_module
from polyjuice.nast import UnsupportedSyntax
from polyjuice.pipeline import run

SOURCE = harness.source_of("c")


def test_original_c_matches_the_reference():
    harness.check_results(harness.run_emitted_c(SOURCE), "original C")


@pytest.mark.parametrize("target", ["c", "python"])
def test_c_transpiles_and_agrees(target):
    output = run(SOURCE, target, source_language="c").output
    runner = harness.run_emitted_c if target == "c" else harness.run_emitted_python
    harness.check_results(runner(output), f"C -> {target}")


def test_c_through_llvm_agrees():
    harness.check_results(harness.run_llvm(lambda ctx: build_module(SOURCE, ctx)), "C -> LLVM")


def test_c_output_reads_like_the_input():
    out = run(SOURCE, "c", source_language="c").output
    assert "for (int32_t i = 0; i < n + 1; i++) {" in out
    assert "if (n <= 1) {" in out and "return n * fact(n - 1);" in out
    py = run(SOURCE, "python", source_language="c").output
    assert "for i in range(n + 1):" in py and "while b != 0:" in py


@pytest.mark.parametrize(
    "body,message",
    [
        ("long x = 1; return 0;", "unsupported type 'long'"),
        ("float x = 1; return 0;", "unsupported type 'float'"),
        ("int *p; return 0;", "unsupported declarator"),
        ("return a & 1;", "unsupported operator &"),
        ("while (a) { break; } return 0;", "unsupported statement break_statement"),
        ("int x[4]; return 0;", "unsupported declarator"),
        ("return a ? 1 : 2;", "unsupported expression conditional_expression"),
        ("char c = 'a'; return 0;", "unsupported type 'char'"),
        ("return 5L;", "unsupported numeric literal"),
    ],
)
def test_unsupported_c_is_refused_with_a_line_number(body, message):
    with pytest.raises(UnsupportedSyntax, match=rf"line \d+: .*{message}"):
        run(f"int f(int a) {{\n{body}\n}}\n", "c", source_language="c")


def test_syntax_errors_are_reported():
    with pytest.raises(UnsupportedSyntax, match="syntax error"):
        run("int f(int a) { return a + ; }\n", "c", source_language="c")


def test_for_loop_with_a_moving_bound_is_not_turned_into_a_range():
    src = "int f(int n) {\n int c = 0;\n for (int i = 0; i < n; i++) { n = n - 1; c++; }\n return c;\n}\n"
    out = run(src, "python", source_language="c").output
    assert "while i < n:" in out and "range(" not in out


def test_down_counting_and_stepped_loops_become_while():
    src = "int f(int n) {\n int s = 0;\n for (int i = n; i > 0; i--) { s += i; }\n return s;\n}\n"
    out = run(src, "python", source_language="c").output
    assert "while i > 0:" in out
    ns: dict = {}
    exec(out, ns)
    assert ns["f"](5) == 15


def test_integer_division_truncates_toward_zero_like_c():
    src = "int d(int a, int b) { return a / b; }\nint m(int a, int b) { return a % b; }\n"
    out = run(src, "python", source_language="c").output
    ns: dict = {}
    exec(out, ns)
    assert (ns["d"](-7, 2), ns["m"](-7, 2), ns["d"](7, -2), ns["m"](7, -2)) == (-3, -1, -3, 1)
    c = harness.run_emitted_c  # emitted C divides exactly as C does
    assert "return a / b;" in run(src, "c", source_language="c").output
