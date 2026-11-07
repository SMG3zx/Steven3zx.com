import pytest

from polyjuice.nast import UnsupportedSyntax
from polyjuice.pipeline import run


def kt(body: str) -> str:
    return f"fun f(a: Int): Int {{\n{body}\n}}\n"


def test_expression_bodies_ranges_and_conversions():
    src = "fun sq(x: Int): Int = x * x\n\nfun mean(n: Int): Double {\n    var s = 0\n    for (i in 0..n) {\n        s += sq(i)\n    }\n    return s.toDouble() / 2.0\n}\n"
    out = run(src, "python", source_language="kotlin").output
    assert "def sq(x: int) -> int:\n    return x * x" in out
    assert "for i in range(n + 1):" in out and "float(s) / 2.0" in out


def test_int_division_truncates_like_kotlin():
    ns: dict = {}
    exec(run(kt("return a / 2 * 10 + a % 3"), "python", source_language="kotlin").output, ns)
    assert ns["f"](-7) == -3 * 10 + -1  # Kotlin: -7 / 2 == -3, -7 % 3 == -1


@pytest.mark.parametrize(
    "body,message",
    [
        ("val x: Long = 1\nreturn 0", "unsupported type 'Long'"),
        ("return when (a) { else -> 1 }", "unsupported expression when_expression"),
        ("val s = \"x\"\nreturn 0", "unsupported expression"),
        ("for (i in 0..a step 2) {}\nreturn 0", "ranges are supported|unsupported"),
        ("val l = listOf(1)\nreturn 0", "call to unknown function .listOf."),
        ("var x: Int\nreturn 0", "needs an initial value"),
        ("val f = { x: Int -> x }\nreturn 0", "unsupported expression lambda_literal"),
        ("while (a > 0) { break }\nreturn 0", "unsupported statement"),
    ],
)
def test_unsupported_kotlin_is_refused_with_a_line_number(body, message):
    with pytest.raises(UnsupportedSyntax, match=rf"line \d+: .*({message})"):
        run(kt(body), "c", source_language="kotlin")
