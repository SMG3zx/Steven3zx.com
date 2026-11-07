"""Kotlin frontend: tree-sitter-kotlin -> neutral AST.

Supported: top-level `fun` over `Int` and `Double` (block or `= expr` bodies); `val`/`var` with
inferred or declared types; assignment, compound assignment, `++`/`--`; `if`/`else if`/`else`
as statements, `while`, `for (i in a until b)` and `a..b`, early `return`; calls by name;
`.toInt()` / `.toDouble()`; `&& || !`. `Int` wraps like the shared layer. Refused with a line
number: classes, lambdas, arrays and lists, strings, `Boolean`/`Long`/`Float`, `when`,
`break`/`continue`, `if` used as a value, ranges with `step`/`downTo`, extension calls.
"""

import tree_sitter_kotlin

from polyjuice import nast
from polyjuice.frontends import _ts
from polyjuice.frontends._ts import field, line, named, text
from polyjuice.frontends.clike import ASSIGN, CLike
from polyjuice.lower import lower_module
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.nast import FLOAT, INT, UnsupportedSyntax

ensure_mlir_importable()

from mlir import ir  # noqa: E402

_PARSER = None


def _parser():
    global _PARSER
    if _PARSER is None:
        _PARSER = _ts.make_parser(tree_sitter_kotlin.language())
    return _PARSER


class KotlinFrontend(CLike):
    number_types = {"number_literal", "float_literal"}
    index_types = set()

    def scalar(self, node, allow_void: bool = False):
        name = text(node)
        if name == "Int":
            return INT
        if name == "Double":
            return FLOAT
        if name == "Unit" and allow_void:
            return None
        raise UnsupportedSyntax(line(node), f"unsupported type {name!r}")

    def module(self) -> nast.Module:
        functions = []
        for node in named(self.root):
            if node.type == "function_declaration":
                functions.append(self.function(node))
            elif node.type in ("package_header", "import_list", "import_header"):
                continue
            else:
                raise UnsupportedSyntax(line(node), f"unsupported top-level {node.type}")
        return nast.Module(functions)

    def function(self, node) -> nast.Function:
        params = []
        for p in named(next(c for c in named(node) if c.type == "function_value_parameters")):
            kids = named(p)
            if p.type != "parameter" or len(kids) != 2:
                raise UnsupportedSyntax(line(p), "unsupported parameter")
            params.append(nast.Param(text(kids[0]), self.scalar(kids[1])))
        types = [c for c in named(node) if c.type == "user_type"]
        ret = self.scalar(types[0], allow_void=True) if types else None
        body_node = next(c for c in named(node) if c.type == "function_body")
        inner = named(body_node)
        if inner and inner[0].type == "block":
            body = self.block(inner[0])
        else:
            if ret is None:
                raise UnsupportedSyntax(line(node), "an expression body needs a declared return type")
            body = [nast.Return(self.expr(inner[0]), line(node))]
        return nast.Function(text(field(node, "name")), params, ret, body, line(node), static=True)

    # -- statements -----------------------------------------------------
    def block(self, node) -> list:
        if node.type == "block":
            out = []
            for child in named(node):
                if child.type not in ("line_comment", "block_comment", "comment"):
                    out.extend(self.stmt(child))
            return out
        return self.stmt(node)

    def stmt(self, n) -> list:
        t, where = n.type, line(n)
        if t == "property_declaration":
            variable = next(c for c in named(n) if c.type == "variable_declaration")
            kids = named(variable)
            kind = self.scalar(kids[1]) if len(kids) > 1 else None
            values = [c for c in named(n) if c != variable]
            if not values:
                raise UnsupportedSyntax(where, "a variable needs an initial value")
            return [nast.Declare(text(kids[0]), kind, self.expr(values[0]), where)]
        if t == "assignment":
            left, op = field(n, "left"), _ts.operator(n)
            if left.type != "identifier":
                raise UnsupportedSyntax(where, "unsupported assignment target")
            target, value = nast.Name(text(left), line(left)), self.expr(field(n, "right"))
            if op == "=":
                return [nast.Assign(target, value, where)]
            if op in ASSIGN:
                return [nast.Assign(target, nast.Binary(ASSIGN[op], nast.Name(text(left), where), value, where), where)]
            raise UnsupportedSyntax(where, f"unsupported assignment operator {op}")
        if t == "unary_expression":
            op = next(text(c) for c in n.children if not c.is_named)
            arg = field(n, "argument")
            if op not in ("++", "--") or arg.type != "identifier":
                raise UnsupportedSyntax(where, "expression statement without effect")
            return [nast.Assign(nast.Name(text(arg), where), nast.Binary("+" if op == "++" else "-", nast.Name(text(arg), where), nast.Const(1, where), where), where)]
        if t == "call_expression":
            return [nast.ExprStmt(self.expr(n), where)]
        if t == "if_expression":
            rest = [c for c in named(n) if c != field(n, "condition")]
            orelse = self.block(rest[1]) if len(rest) > 1 else []
            return [nast.If(self.expr(field(n, "condition")), self.block(rest[0]), orelse, where)]
        if t == "while_statement":
            return [nast.While(self.expr(field(n, "condition")), self.block(next(c for c in named(n) if c.type == "block")), where)]
        if t == "for_statement":
            return [self.for_loop(n)]
        if t == "return_expression":
            kids = named(n)
            return [nast.Return(self.expr(kids[0]) if kids else None, where)]
        raise UnsupportedSyntax(where, f"unsupported statement {t}")

    def for_loop(self, n):
        where = line(n)
        kids = named(n)
        variable, iterable, body = kids[0], kids[1], kids[-1]
        if variable.type != "variable_declaration" or body.type != "block":
            raise UnsupportedSyntax(where, "only `for (i in a until b)` and `a..b` are supported")
        var = text(named(variable)[0])
        bounds = named(iterable)
        if iterable.type == "range_expression" and len(bounds) == 2:
            stop = nast.Binary("+", self.expr(bounds[1]), nast.Const(1, where), where)
            return nast.For(var, self.expr(bounds[0]), stop, self.block(body), where)
        if iterable.type == "infix_expression" and len(bounds) == 3 and text(bounds[1]) == "until":
            return nast.For(var, self.expr(bounds[0]), self.expr(bounds[2]), self.block(body), where)
        raise UnsupportedSyntax(where, "only `a until b` and `a..b` ranges are supported")

    # -- expressions ----------------------------------------------------
    def expr(self, n):
        t, where = n.type, line(n)
        if t == "call_expression":
            callee, args = named(n)[0], named(n)[1]
            if callee.type == "navigation_expression":
                operand, method = named(callee)[0], text(named(callee)[-1])
                if method in ("toInt", "toDouble") and not named(args):
                    return nast.Cast(INT if method == "toInt" else FLOAT, self.expr(operand), where)
                raise UnsupportedSyntax(where, f"unsupported method call .{method}()")
            if callee.type != "identifier":
                raise UnsupportedSyntax(where, "only calls to plain functions are supported")
            return nast.Call(text(callee), [self.expr(named(a)[0]) for a in named(args)], where)
        if t == "unary_expression":
            op = next(text(c) for c in n.children if not c.is_named)
            operand = self.expr(field(n, "argument"))
            if op == "-":
                return _ts.negate(operand)
            if op == "!":
                return nast.Not(operand, where)
            raise UnsupportedSyntax(where, f"unsupported operator {op}")
        return super().expr(n)


def parse(source: str) -> nast.Module:
    return KotlinFrontend(source, _parser()).module()


def build_module(source: str, context: ir.Context) -> ir.Module:
    return lower_module(parse(source), context)
