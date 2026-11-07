import pytest

from polyjuice.nast import UnsupportedSyntax
from polyjuice.pipeline import run


def go(body: str, sig: str = "a int") -> str:
    return f"package main\n\nfunc f({sig}) int {{\n{body}\n}}\n"


def test_go_reads_like_the_input():
    src = (
        "package main\n\nfunc sum(n int) int {\n\ts := 0\n\tfor i := 0; i < n; i++ {\n\t\ts += i\n\t}\n\treturn s\n}\n"
    )
    assert "for i in range(n):" in run(src, "python", source_language="go").output
    assert "for (int32_t i = 0; i < n; i++) {" in run(src, "c", source_language="go").output


def test_array_pointer_and_read_only_array_parameters():
    src = (
        "package main\n\nfunc total(a [4]int) int {\n\ts := 0\n\tfor i := range 4 {\n\t\ts += a[i]\n\t}\n\treturn s\n}\n\n"
        "func fill(a *[4]int) {\n\ta[0] = 7\n}\n"
    )
    out = run(src, "python", source_language="go").output
    assert "def total(a: list[int])" in out and "a[0] = 7" in out


def test_writing_to_an_array_value_is_refused_because_go_copies_it():
    src = "package main\n\nfunc f(a [4]int) int {\n\ta[0] = 1\n\treturn a[0]\n}\n"
    with pytest.raises(UnsupportedSyntax, match="copied in Go"):
        run(src, "c", source_language="go")


def test_multi_assignment_swaps_in_one_step():
    src = go("\tx, y := 0, 1\n\tfor i := 0; i < a; i++ {\n\t\tx, y = y, x+y\n\t}\n\treturn x")
    ns: dict = {}
    exec(run(src, "python", source_language="go").output, ns)
    assert [ns["f"](n) for n in range(8)] == [0, 1, 1, 2, 3, 5, 8, 13]


@pytest.mark.parametrize(
    "body,message",
    [
        ("\tx := []int{1}\n\treturn 0", "unsupported expression composite_literal"),
        ("\tfor {\n\t}\n\treturn 0", "infinite"),
        ("\tvar x int64 = 1\n\treturn 0", "unsupported type 'int64'"),
        ("\tfor i := 0; i < a; i++ {\n\t\tbreak\n\t}\n\treturn 0", "unsupported statement break_statement"),
        ("\tx := \"hi\"\n\treturn 0", "unsupported expression interpreted_string_literal"),
        ("\tdefer f(a)\n\treturn 0", "unsupported statement defer_statement"),
        ("\tgo f(a)\n\treturn 0", "unsupported statement go_statement"),
        ("\tif x := a; x > 1 {\n\t\treturn 1\n\t}\n\treturn 0", "initializer"),
    ],
)
def test_unsupported_go_is_refused_with_a_line_number(body, message):
    with pytest.raises(UnsupportedSyntax, match=rf"line \d+: .*{message}"):
        run(go(body), "c", source_language="go")


def test_shadowing_is_refused_not_miscompiled():
    src = go("\tx := 1\n\tif a > 0 {\n\t\tx := 2\n\t\treturn x\n\t}\n\treturn x")
    with pytest.raises(UnsupportedSyntax, match="already declared"):
        run(src, "c", source_language="go")
