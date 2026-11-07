import pytest

from polyjuice.nast import UnsupportedSyntax
from polyjuice.pipeline import run


def rs(body: str, sig: str = "a: i32") -> str:
    return f"fn f({sig}) -> i32 {{\n{body}\n}}\n"


def test_tail_if_is_the_return_value_and_reads_like_guard_clauses():
    out = run(rs("    if a > 0 { a } else { -a }"), "python", source_language="rust").output
    assert "return a" in out and "return -a" in out


def test_ranges_and_casts():
    src = "fn f(n: i32) -> f64 {\n    let mut s = 0.0;\n    for i in 0..n {\n        s += i as f64;\n    }\n    s / 2.0\n}\n"
    out = run(src, "python", source_language="rust").output
    assert "for i in range(n):" in out and "s += float(i)" in out


def test_array_references_are_arrays_but_by_value_arrays_are_refused():
    src = "fn first(a: &[i32; 4]) -> i32 {\n    a[0]\n}\n\nfn put(a: &mut [i32; 4]) {\n    a[1] = 5;\n}\n"
    out = run(src, "python", source_language="rust").output
    assert "def first(a: list[int])" in out and "a[1] = 5" in out
    with pytest.raises(UnsupportedSyntax, match="by reference"):
        run("fn f(a: [i32; 4]) -> i32 {\n    a[0]\n}\n", "c", source_language="rust")


@pytest.mark.parametrize(
    "body,message",
    [
        ("    let x: u8 = 1;\n    0", "unsupported type 'u8'"),
        ("    loop {\n    }\n    0", "unsupported statement loop_expression"),
        ("    match a { _ => 0 }", "unsupported expression match_expression"),
        ("    println!(\"hi\");\n    0", "unsupported statement macro_invocation"),
        ("    let (x, y) = (1, 2);\n    x", "simple `let name"),
        ("    let v = a as i64;\n    0", "unsupported cast to i64"),
        ("    if a > 0 { 1 }", "needs an `else`"),
    ],
)
def test_unsupported_rust_is_refused_with_a_line_number(body, message):
    with pytest.raises(UnsupportedSyntax, match=rf"line \d+: .*{message}"):
        run(rs(body), "c", source_language="rust")
