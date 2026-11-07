import pytest

from polyjuice.nast import UnsupportedSyntax
from polyjuice.pipeline import run

TS_HEADER = "function f(a: number): number {\n"


def js(body: str) -> str:
    return f"function f(a) {{\n{body}\n}}\n"


def test_every_number_is_a_double_so_division_is_not_integer_division():
    out = run(js("return a / 2;"), "python", source_language="javascript").output
    ns: dict = {}
    exec(out, ns)
    assert ns["f"](7.0) == 3.5  # JavaScript: 7 / 2 === 3.5
    assert "double f(double a)" in run(js("return a / 2;"), "c", source_language="javascript").output


def test_remainder_is_fmod():
    out = run(js("return a % 2;"), "c", source_language="javascript").output
    assert "fmod(a, 2.0)" in out and "#include <math.h>" in out


def test_loop_counters_are_floats_so_counted_loops_are_while_loops():
    out = run(js("let s = 0;\nfor (let i = 0; i < a; i++) { s += i; }\nreturn s;"), "python", source_language="javascript").output
    assert "while i < a:" in out and "range(" not in out


def test_sequential_updates_stay_in_source_order():
    out = run(js("let s = 0;\nfor (let i = 0; i < a; i++) { s += i; }\nreturn s;"), "python", source_language="javascript").output
    assert "s += i\n" in out and "i += 1.0" in out and "i, s" not in out


def test_typescript_accepts_number_and_void_only():
    out = run("function g(x: number): void {\n}\nfunction f(a: number): number {\n  let s: number = 1;\n  return s + a;\n}\n", "python", source_language="typescript").output
    assert "def g(x: float)" in out and "def f(a: float) -> float" in out
    with pytest.raises(UnsupportedSyntax, match="unsupported type 'string'"):
        run("function f(a: string): number {\n  return 1;\n}\n", "c", source_language="typescript")
    with pytest.raises(UnsupportedSyntax, match="type annotation"):
        run("function f(a): number {\n  return 1;\n}\n", "c", source_language="typescript")


@pytest.mark.parametrize(
    "body,message",
    [
        ("let x;\nreturn 1;", "needs an initializer"),
        ("return a ? 1 : 2;", "unsupported expression ternary_expression"),
        ("return true;", "unsupported expression true"),
        ("return \"s\";", "unsupported expression string"),
        ("return [1][0];", "unsupported expression subscript_expression"),
        ("return Math.floor(a);", "only calls to plain functions"),
        ("return a | 0;", "unsupported operator \\|"),
        ("while (a) { break; }\nreturn 1;", "unsupported statement break_statement"),
        ("const g = (x) => x;\nreturn 1;", "unsupported expression arrow_function"),
    ],
)
def test_unsupported_javascript_is_refused_with_a_line_number(body, message):
    with pytest.raises(UnsupportedSyntax, match=rf"line \d+: .*{message}"):
        run(js(body), "c", source_language="javascript")
