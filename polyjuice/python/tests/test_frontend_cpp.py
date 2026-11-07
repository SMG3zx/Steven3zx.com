import pytest

from polyjuice.nast import UnsupportedSyntax
from polyjuice.pipeline import run


def test_static_cast_and_using_directives():
    src = "#include <cstdint>\nusing namespace std;\ndouble half(int a) { return static_cast<double>(a) / 2; }\nint whole(double d) { return static_cast<int>(d); }\n"
    out = run(src, "python", source_language="cpp").output
    assert "float(a) / 2" in out and "int(d)" in out


@pytest.mark.parametrize(
    "body,message",
    [
        ("bool ok = 1; return 0;", "unsupported type 'bool'"),
        ("auto x = 1; return 0;", "unsupported type 'auto'"),
        ("return std::abs(a);", "unsupported expression|only calls"),
        ("int &r = a; return r;", "unsupported declarator"),
        ("return a ? 1 : 2;", "unsupported expression conditional_expression"),
    ],
)
def test_unsupported_cpp_is_refused_with_a_line_number(body, message):
    with pytest.raises(UnsupportedSyntax, match=rf"line \d+: .*({message})"):
        run(f"int f(int a) {{\n{body}\n}}\n", "c", source_language="cpp")
