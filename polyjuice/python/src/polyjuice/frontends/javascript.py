"""JavaScript frontend: tree-sitter-javascript -> neutral AST.

Every JavaScript number is an IEEE double, so every number here is `float`: `7 / 2` is 3.5,
`%` is `fmod`, and loop counters are floats (counted loops therefore become `while`).
Supported: function declarations, `let`/`const`/`var` with an initializer, assignment and
compound assignment, `++`/`--`, `if/else`, `while`, `for`, early `return`, calls by name,
`=== !== && || !`. A function that returns a value returns a number. Refused with a line
number: arrays, objects, strings, booleans, `null`/`undefined`, closures, classes,
`break`/`continue`, `switch`, ternary, `Math.*`, bitwise operators.
"""

import tree_sitter_javascript

from polyjuice import nast
from polyjuice.frontends import _ts
from polyjuice.frontends._ts import field, line, named, text
from polyjuice.frontends.clike import CLike
from polyjuice.lower import lower_module
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.nast import FLOAT, UnsupportedSyntax

ensure_mlir_importable()

from mlir import ir  # noqa: E402

_PARSER = None


def _parser():
    global _PARSER
    if _PARSER is None:
        _PARSER = _ts.make_parser(tree_sitter_javascript.language())
    return _PARSER


def returns_a_value(stmts) -> bool:
    for st in stmts:
        if isinstance(st, nast.Return) and st.value is not None:
            return True
        for attr in ("body", "then", "orelse"):
            inner = getattr(st, attr, None)
            if isinstance(inner, list) and returns_a_value(inner):
                return True
    return False


class JavaScriptFrontend(CLike):
    number_types = {"number"}
    index_types = set()
    init_fields, update_fields = ("initializer",), ("increment",)
    counted_loops = False

    def number(self, n):
        const = _ts.number(n)
        return nast.Const(float(const.value), const.line)

    def module(self) -> nast.Module:
        functions = []
        for node in named(self.root):
            if node.type == "function_declaration":
                functions.append(self.function(node))
            else:
                raise UnsupportedSyntax(line(node), f"unsupported top-level {node.type}")
        return nast.Module(functions)

    def parameter(self, p) -> nast.Param:
        if p.type != "identifier":
            raise UnsupportedSyntax(line(p), f"unsupported parameter {p.type}")
        return nast.Param(text(p), FLOAT)

    def function(self, node) -> nast.Function:
        if text(node).startswith(("async", "function*")):
            raise UnsupportedSyntax(line(node), "async and generator functions are not supported")
        params = [self.parameter(p) for p in named(field(node, "parameters"))]
        body = self.block(field(node, "body"))
        return nast.Function(
            text(field(node, "name")), params, self.return_type(node, body), body, line(node), static=True
        )

    def return_type(self, node, body):
        return FLOAT if returns_a_value(body) else None

    def declaration(self, n) -> list:
        out = []
        for d in named(n):
            if d.type != "variable_declarator":
                continue
            name, value = field(d, "name"), field(d, "value")
            if name.type != "identifier":
                raise UnsupportedSyntax(line(n), "destructuring is not supported")
            if value is None:
                raise UnsupportedSyntax(line(n), "a variable needs an initializer")
            self.check_declarator_type(d)
            out.append(nast.Declare(text(name), None, self.expr(value), line(n)))
        return out

    def check_declarator_type(self, d) -> None:
        pass


def parse(source: str) -> nast.Module:
    return JavaScriptFrontend(source, _parser()).module()


def build_module(source: str, context: ir.Context) -> ir.Module:
    return lower_module(parse(source), context)
