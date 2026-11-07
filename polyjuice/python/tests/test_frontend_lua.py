import pytest

from polyjuice.nast import UnsupportedSyntax
from polyjuice.pipeline import run

HEAD = "---@param a integer\n---@return integer\n"


def lua(body: str) -> str:
    return f"{HEAD}function f(a)\n{body}\nend\n"


def test_lua_operators_map_to_python_operators():
    out = run(lua("return a // 2 + a % 3"), "python", source_language="lua").output
    assert "return a // 2 + a % 3" in out


def test_floor_division_and_modulo_follow_lua_for_negative_numbers():
    ns: dict = {}
    exec(run(lua("return a // 2"), "python", source_language="lua").output, ns)
    assert (ns["f"](-7), ns["f"](7)) == (-4, 3)  # Lua: -7 // 2 == -4


def test_true_division_returns_a_float():
    src = "---@param a integer\n---@return number\nfunction f(a)\n  return a / 2\nend\n"
    out = run(src, "python", source_language="lua").output
    ns: dict = {}
    exec(out, ns)
    assert ns["f"](7) == 3.5


def test_numeric_for_is_inclusive_and_steps_work():
    src = lua("local s = 0\nfor i = a, 1, -1 do s = s + i end\nreturn s")
    ns: dict = {}
    exec(run(src, "python", source_language="lua").output, ns)
    assert ns["f"](4) == 10
    inclusive = lua("local s = 0\nfor i = 1, a do s = s + i end\nreturn s")
    out = run(inclusive, "python", source_language="lua").output
    assert "range(1, a + 1)" in out


def test_missing_annotations_are_errors_not_guesses():
    with pytest.raises(UnsupportedSyntax, match="needs a `---@param a"):
        run("function f(a)\n  return 1\nend\n", "c", source_language="lua")
    with pytest.raises(UnsupportedSyntax, match="needs a `---@return"):
        run("---@param a integer\nfunction f(a)\n  return 1\nend\n", "c", source_language="lua")
    with pytest.raises(UnsupportedSyntax, match="unsupported type 'string'"):
        run("---@param a string\nfunction f(a)\nend\n", "c", source_language="lua")


@pytest.mark.parametrize(
    "body,message",
    [
        ("local t = {1}\nreturn 1", "unsupported expression table_constructor"),
        ("return a .. a", "unsupported operator \\.\\."),
        ("return a ^ 2", "unsupported operator \\^"),
        ("repeat a = a + 1 until a > 3\nreturn a", "unsupported statement repeat_statement"),
        ("while true do break end\nreturn 1", "unsupported expression true"),
        ("return a, a", "multiple return values"),
        ("local x\nreturn 1", "needs an initial value"),
        ("for k, v in pairs(a) do end\nreturn 1", "only numeric"),
    ],
)
def test_unsupported_lua_is_refused_with_a_line_number(body, message):
    with pytest.raises(UnsupportedSyntax, match=rf"line \d+: .*{message}"):
        run(lua(body), "c", source_language="lua")
