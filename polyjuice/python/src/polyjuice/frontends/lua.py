"""Lua frontend: tree-sitter-lua -> neutral AST.

Lua has no static types, so parameter and result types come from LuaLS annotations on the
line(s) before the function: `---@param a integer`, `---@param x number`, `---@return number`.
`integer` is treated as 32-bit; `number` as a double. Locals take the type of their initializer.
Operators follow Lua: `/` is true division, `//` and `%` floor, `~=` is not-equal.
Supported: global `function` declarations, `local`, assignment (including `a, b = b, a`), `if`
with `elseif`/`else`, `while`, numeric `for` (inclusive end), early `return`, calls, `and or not`.
Refused with a line number: tables, strings, booleans, `nil`, closures, `repeat`, generic `for`,
`break`/`goto`, `..`, `^`, bitwise operators, multiple return values.
"""

import re

import tree_sitter_lua

from polyjuice import nast
from polyjuice.frontends import _ts
from polyjuice.frontends._ts import field, line, named, text
from polyjuice.frontends.javascript import returns_a_value
from polyjuice.lower import lower_module
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.nast import FLOAT, INT, UnsupportedSyntax

ensure_mlir_importable()

from mlir import ir  # noqa: E402

_PARSER = None
_TYPES = {"integer": INT, "number": FLOAT}
_ARITH = {"+": "+", "-": "-", "*": "*", "/": "/", "//": "floordiv", "%": "floormod"}
_COMPARE = {"<", "<=", ">", ">=", "=="}
_PARAM = re.compile(r"@param\s+(\w+)\s+(\S+)")
_RETURN = re.compile(r"@return\s+(\S+)")


def _parser():
    global _PARSER
    if _PARSER is None:
        _PARSER = _ts.make_parser(tree_sitter_lua.language())
    return _PARSER


class _Lua:
    def __init__(self, source: str):
        self.root = _ts.parse(_parser(), source)

    def scalar(self, name: str, where: int):
        if name not in _TYPES:
            raise UnsupportedSyntax(where, f"unsupported type {name!r} (use integer or number)")
        return _TYPES[name]

    def module(self) -> nast.Module:
        functions, comments = [], []
        for node in self.root.children:
            if node.type == "comment":
                comments.append(node)
            elif node.type == "function_declaration":
                functions.append(self.function(node, comments))
                comments = []
            else:
                raise UnsupportedSyntax(line(node), f"unsupported top-level {node.type}")
        return nast.Module(functions)

    def function(self, node, comments) -> nast.Function:
        name_node = field(node, "name")
        if name_node.type != "identifier":
            raise UnsupportedSyntax(line(node), "only plain `function name(...)` declarations are supported")
        declared, ret_name = {}, None
        for c in comments:
            body = text(c)
            if m := _PARAM.search(body):
                declared[m.group(1)] = (m.group(2), line(c))
            elif m := _RETURN.search(body):
                ret_name = (m.group(1), line(c))
        params = []
        for p in named(field(node, "parameters")):
            if p.type != "identifier":
                raise UnsupportedSyntax(line(p), "varargs are not supported")
            if text(p) not in declared:
                raise UnsupportedSyntax(line(p), f"parameter {text(p)!r} needs a `---@param {text(p)} integer|number` annotation")
            kind, where = declared[text(p)]
            params.append(nast.Param(text(p), self.scalar(kind, where)))
        body = self.block(field(node, "body"))
        ret = None
        if returns_a_value(body):
            if ret_name is None:
                raise UnsupportedSyntax(line(node), "a function that returns a value needs a `---@return integer|number` annotation")
            ret = self.scalar(*ret_name)
        return nast.Function(text(name_node), params, ret, body, line(node), static=True)

    # -- statements -----------------------------------------------------
    def block(self, node) -> list:
        out = []
        for child in named(node):
            if child.type != "comment":
                out.extend(self.stmt(child))
        return out

    def stmt(self, n) -> list:
        t, where = n.type, line(n)
        if t == "variable_declaration":
            inner = next((c for c in named(n) if c.type == "assignment_statement"), None)
            if inner is None:
                raise UnsupportedSyntax(where, "a local needs an initial value")
            names = [self.name(x) for x in named(field_list(inner, "variable_list"))]
            values = [self.expr(v) for v in named(field_list(inner, "expression_list"))]
            if len(names) != len(values):
                raise UnsupportedSyntax(where, "unsupported local declaration")
            return [nast.Declare(nm, None, v, where) for nm, v in zip(names, values)]
        if t == "assignment_statement":
            targets = [nast.Name(self.name(x), line(x)) for x in named(field_list(n, "variable_list"))]
            values = [self.expr(v) for v in named(field_list(n, "expression_list"))]
            if len(targets) != len(values):
                raise UnsupportedSyntax(where, "unsupported assignment")
            if len(targets) == 1:
                return [nast.Assign(targets[0], values[0], where)]
            return [nast.ParallelAssign(targets, values, where)]
        if t == "function_call":
            return [nast.ExprStmt(self.expr(n), where)]
        if t == "if_statement":
            return [self.if_chain(self_if := n, list(n.children_by_field_name("alternative")))]
        if t == "while_statement":
            return [nast.While(self.expr(field(n, "condition")), self.block(field(n, "body")), where)]
        if t == "for_statement":
            return self.for_statement(n)
        if t == "return_statement":
            values = named(named(n)[0]) if named(n) else []
            if len(values) > 1:
                raise UnsupportedSyntax(where, "multiple return values are not supported")
            return [nast.Return(self.expr(values[0]) if values else None, where)]
        raise UnsupportedSyntax(where, f"unsupported statement {t}")

    def name(self, n) -> str:
        if n.type != "identifier":
            raise UnsupportedSyntax(line(n), "tables and fields are not supported")
        return text(n)

    def if_chain(self, n, alternatives) -> nast.If:
        where = line(n)
        orelse = []
        if alternatives:
            head, rest = alternatives[0], alternatives[1:]
            if head.type == "elseif_statement":
                orelse = [self.if_chain(head, rest)]
            else:
                orelse = self.block(field(head, "body"))
        return nast.If(self.expr(field(n, "condition")), self.block(field(n, "consequence")), orelse, where)

    def for_statement(self, n) -> list:
        where = line(n)
        clause = field(n, "clause")
        if clause.type != "for_numeric_clause":
            raise UnsupportedSyntax(where, "only numeric `for` loops are supported")
        var = text(field(clause, "name"))
        start, end, step = self.expr(field(clause, "start")), self.expr(field(clause, "end")), field(clause, "step")
        body = self.block(field(n, "body"))
        if step is None or (self.expr(step) == nast.Const(1, line(step))):
            stop = nast.Binary("+", end, nast.Const(1, where), where)  # Lua's end is inclusive
            return [nast.For(var, start, stop, body, where)]
        stepped = self.expr(step)
        if not isinstance(stepped, nast.Const) or stepped.value == 0 or not isinstance(stepped.value, int):
            raise UnsupportedSyntax(where, "a `for` step must be a nonzero integer constant")
        limit = f"__limit_{where}"
        cond = nast.Compare("<=" if stepped.value > 0 else ">=", nast.Name(var, where), nast.Name(limit, where), where)
        bump = nast.Assign(nast.Name(var, where), nast.Binary("+", nast.Name(var, where), stepped, where), where)
        return [nast.Scope([nast.Declare(var, None, start, where), nast.Declare(limit, None, end, where),
                            nast.While(cond, body + [bump], where)], where)]  # fmt: skip

    # -- expressions ----------------------------------------------------
    def expr(self, n):
        t, where = n.type, line(n)
        if t == "number":
            return _ts.number(n)
        if t == "identifier":
            return nast.Name(text(n), where)
        if t == "parenthesized_expression":
            return self.expr(named(n)[0])
        if t == "binary_expression":
            op = text(field(n, "operator")) if field(n, "operator") is not None else _ts.operator(n)
            left, right = self.expr(field(n, "left")), self.expr(field(n, "right"))
            if op in _ARITH:
                return nast.Binary(_ARITH[op], left, right, where)
            if op in _COMPARE:
                return nast.Compare(op, left, right, where)
            if op == "~=":
                return nast.Compare("!=", left, right, where)
            if op in ("and", "or"):
                return nast.Logical(op, left, right, where)
            raise UnsupportedSyntax(where, f"unsupported operator {op}")
        if t == "unary_expression":
            op = next(text(c) for c in n.children if not c.is_named)
            operand = self.expr(field(n, "operand"))
            if op == "-":
                return _ts.negate(operand)
            if op == "not":
                return nast.Not(operand, where)
            raise UnsupportedSyntax(where, f"unsupported operator {op}")
        if t == "function_call":
            fn = field(n, "name")
            if fn.type != "identifier":
                raise UnsupportedSyntax(where, "only calls to plain functions are supported")
            return nast.Call(text(fn), [self.expr(a) for a in named(field(n, "arguments"))], where)
        raise UnsupportedSyntax(where, f"unsupported expression {t}")


def field_list(node, kind: str):
    return next(c for c in named(node) if c.type == kind)


def parse(source: str) -> nast.Module:
    return _Lua(source).module()


def build_module(source: str, context: ir.Context) -> ir.Module:
    return lower_module(parse(source), context)
