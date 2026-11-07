import pytest

from polyjuice.nast import UnsupportedSyntax
from polyjuice.pipeline import run


def ps(body: str, sig: str = "[int]$a", out: str = "[OutputType([int])]") -> str:
    return f"function f {{\n    {out}\n    param({sig})\n{body}\n}}\n"


def test_typed_and_untyped_locals_and_case_insensitive_spelling():
    out = run(ps("    [int]$s = $a * 2\n    $d = 2.5\n    $s += $a\n    return $s * $a"), "python", source_language="powershell").output
    assert "$" not in out and "def f(a: int) -> int:" in out and "return (a * 2 + a) * a" in out
    with pytest.raises(UnsupportedSyntax, match="same name in PowerShell"):
        run(ps("    $s = 0\n    $S = 1\n    return $s"), "c", source_language="powershell")


def test_percent_truncates_like_c_and_comparison_operators_map():
    ns: dict = {}
    exec(run(ps("    if ($a -lt 0 -and $a -ne -9) { return $a % 3 }\n    return 0"), "python", source_language="powershell").output, ns)
    assert ns["f"](-7) == -1  # PowerShell: -7 % 3 == -1


def test_calls_are_commands_with_positional_arguments():
    src = "function g {\n    [OutputType([int])]\n    param([int]$x, [int]$y)\n    return $x + $y\n}\n\n" + ps("    $r = g $a 2\n    return $r + (g 1 $a)")
    out = run(src, "python", source_language="powershell").output
    assert "g(a, 2)" in out and "g(1, a)" in out


def test_result_type_must_be_declared_and_division_of_ints_is_refused():
    with pytest.raises(UnsupportedSyntax, match="needs `\\[OutputType"):
        run(ps("    return 1", out=""), "c", source_language="powershell")
    with pytest.raises(UnsupportedSyntax, match="int or a float depending|int or a double|depending on the values"):
        run(ps("    return $a / 2"), "c", source_language="powershell")


def test_double_to_int_cast_is_refused_because_powershell_rounds():
    src = ps("    return [int]($a / 2.0)")
    with pytest.raises(UnsupportedSyntax, match="rounds"):
        run(src, "c", source_language="powershell")


@pytest.mark.parametrize(
    "body,message",
    [
        ("    foreach ($x in 1..3) { }\n    return 1", "unsupported statement foreach_statement"),
        ("    $x = 'text'\n    return 1", "unsupported expression"),
        ("    $x = @(1, 2)\n    return 1", "unsupported expression"),
        ("    return $true", "unsupported variable \\$true"),
        ("    while ($a) { break }\n    return 1", "unsupported statement"),
        ("    $x = 1 | Out-Null\n    return 1", "unsupported"),
    ],
)
def test_unsupported_powershell_is_refused_with_a_line_number(body, message):
    with pytest.raises(UnsupportedSyntax, match=rf"line \d+: .*{message}"):
        run(ps(body), "c", source_language="powershell")
