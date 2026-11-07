"""Shared tree-sitter helpers for the non-Python frontends."""

import re

from polyjuice import nast
from polyjuice.nast import UnsupportedSyntax


def make_parser(language_capsule):
    from tree_sitter import Language, Parser

    return Parser(Language(language_capsule))


def parse(parser, source: str):
    tree = parser.parse(source.encode())
    problem = first_problem(tree.root_node)
    if problem is not None:
        raise UnsupportedSyntax(line(problem), f"syntax error near {text(problem)[:30]!r}")
    return tree.root_node


def first_problem(node):
    if node.type == "ERROR" or node.is_missing:
        return node
    if not node.has_error:
        return None
    for child in node.children:
        found = first_problem(child)
        if found is not None:
            return found
    return node


def text(node) -> str:
    return node.text.decode()


def line(node) -> int:
    return node.start_point[0] + 1


def named(node):
    return [c for c in node.children if c.is_named and c.type != "comment"]


def field(node, name: str):
    return node.child_by_field_name(name)


def operator(node) -> str:
    op = field(node, "operator")
    return text(op) if op is not None else ""


_INT = re.compile(r"(0[xX][0-9a-fA-F]+|0[bB][01]+|0[0-7]*|[1-9][0-9]*)")
_FLOAT = re.compile(r"([0-9]+\.[0-9]*|\.[0-9]+|[0-9]+)([eE][+-]?[0-9]+)?")


def number(node) -> "nast.Const":
    """Parse a numeric literal without suffixes; anything else is refused."""
    raw = text(node).replace("_", "")
    where = line(node)
    if _INT.fullmatch(raw):
        value = int(raw, 8) if len(raw) > 1 and raw[0] == "0" and raw[1].isdigit() else int(raw, 0)
        return nast.Const(value, where)
    if _FLOAT.fullmatch(raw) and any(c in raw for c in ".eE"):
        return nast.Const(float(raw), where)
    raise UnsupportedSyntax(where, f"unsupported numeric literal {raw!r}")


def negate(operand):
    """`-5` is a constant, so `-2147483648` fits."""
    if isinstance(operand, nast.Const):
        return nast.Const(-operand.value, operand.line)
    return nast.Unary("-", operand, operand.line)


def counted_loop(var, start, op, stop, update_step, body, where):
    """Build a `For` when a C-style loop is exactly `for (var = start; var < stop; var++)`.

    Returns None when the shape differs or `stop` could change inside the body; callers then
    fall back to a `While`.
    """
    if update_step != 1 or op not in ("<", "<="):
        return None
    assigned = nast.assigned_names(body)
    if var in assigned or not _invariant(stop, assigned):
        return None
    if op == "<=":
        stop = nast.Binary("+", stop, nast.Const(1, where), where)
    return nast.For(var, start, stop, body, where)


def _invariant(expr, assigned: set[str]) -> bool:
    if isinstance(expr, nast.Const):
        return True
    if isinstance(expr, nast.Name):
        return expr.id not in assigned
    if isinstance(expr, nast.Binary):
        return _invariant(expr.left, assigned) and _invariant(expr.right, assigned)
    if isinstance(expr, nast.Unary):
        return _invariant(expr.operand, assigned)
    return False  # calls and array reads may change between iterations
