import pytest

from polyjuice.nast import UnsupportedSyntax
from polyjuice.pipeline import run


def php(body: str, sig: str = "int $a") -> str:
    return f"<?php\nfunction f({sig}): int {{\n{body}\n}}\n"


def test_first_assignment_declares_and_variables_lose_their_sigil():
    body = "$s = $a * 2;\n$t = $s + $a;\nreturn $t * $s;"
    out = run(php(body), "python", source_language="php").output
    assert "$" not in out and "def f(a: int) -> int:" in out
    c = run(php(body), "c", source_language="php").output
    assert "$" not in c and "int32_t s = a * 2;" in c


def test_intdiv_truncates_and_modulo_takes_the_dividends_sign():
    ns: dict = {}
    exec(run(php("return intdiv($a, 2) * 10 + $a % 3;"), "python", source_language="php").output, ns)
    assert ns["f"](-7) == -3 * 10 + -1  # intdiv(-7, 2) == -3 and -7 % 3 == -1 in PHP


def test_slash_on_ints_is_refused_because_its_result_type_depends_on_the_values():
    with pytest.raises(UnsupportedSyntax, match="int or a float depending on the values"):
        run(php("return $a / 2;"), "c", source_language="php")


def test_slash_on_floats_is_true_division():
    src = "<?php\nfunction f(float $a): float {\n    return $a / 2.0;\n}\n"
    assert "return a / 2.0" in run(src, "python", source_language="php").output


@pytest.mark.parametrize(
    "src,message",
    [
        ("<?php\nfunction f($a): int {\n    return 1;\n}\n", "parameters need a type hint"),
        ("<?php\nfunction f(int $a) {\n    return 1;\n}\n", "functions need a return type"),
        ("<?php\nfunction f(string $a): int {\n    return 1;\n}\n", "unsupported type 'string'"),
        (php("echo $a;\nreturn 1;"), "unsupported statement echo_statement"),
        (php("$x = [1, 2];\nreturn 1;"), "unsupported expression array_creation_expression"),
        (php("return $a ** 2;"), "unsupported operator \\*\\*"),
        (php("return $a ? 1 : 2;"), "unsupported expression conditional_expression"),
        (php("foreach ($a as $x) {}\nreturn 1;"), "unsupported statement foreach_statement"),
    ],
)
def test_unsupported_php_is_refused_with_a_line_number(src, message):
    with pytest.raises(UnsupportedSyntax, match=rf"line \d+: .*{message}"):
        run(src, "c", source_language="php")
