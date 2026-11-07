"""PHP frontend: tree-sitter-php -> neutral AST.

PHP is function-scoped with no declarations, so the first assignment to a name declares it
and takes the type of its value. Parameters and the return value need type hints
(`int $a`, `float $b`, `: int`). `int` is treated as 32-bit (PHP's is 64-bit).
Supported: function definitions, assignment and compound assignment, `++`/`--`, `if`/`elseif`/
`else`, `while`, `for`, early `return`, calls, `(int)`/`(float)` casts, `intdiv()`, `&& || !`.
`/` is refused on ints because PHP returns an int or a float depending on the values; `%`
truncates toward zero like PHP's. Refused with a line number: arrays, strings, `echo`, classes,
references, `foreach`, `break`/`continue`, `switch`, ternary, `**`, `.`.
"""

import tree_sitter_php

from polyjuice import nast
from polyjuice.frontends import _ts
from polyjuice.frontends._ts import field, line, named, operator, text
from polyjuice.frontends.clike import BINARY, CLike
from polyjuice.lower import lower_module
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.nast import FLOAT, INT, UnsupportedSyntax

ensure_mlir_importable()

from mlir import ir  # noqa: E402

_PARSER = None


def _parser():
    global _PARSER
    if _PARSER is None:
        _PARSER = _ts.make_parser(tree_sitter_php.language_php())
    return _PARSER


class PhpFrontend(CLike):
    number_types = {"integer", "float"}
    identifier_types = {"variable_name"}
    index_types = set()
    init_fields, update_fields = ("initialize",), ("update",)

    def __init__(self, source: str, parser):
        super().__init__(source, parser)
        self.seen: set[str] = set()

    def ident(self, n) -> str:
        return text(n).lstrip("$")

    def scalar(self, node, allow_void: bool = False):
        name = text(node)
        if name == "int":
            return INT
        if name == "float":
            return FLOAT
        if name == "void" and allow_void:
            return None
        raise UnsupportedSyntax(line(node), f"unsupported type {name!r}")

    def module(self) -> nast.Module:
        functions = []
        for node in named(self.root):
            if node.type in ("php_tag", "text_interpolation"):
                continue
            if node.type != "function_definition":
                raise UnsupportedSyntax(line(node), f"unsupported top-level {node.type}")
            functions.append(self.function(node))
        return nast.Module(functions)

    def function(self, node) -> nast.Function:
        self.seen = set()
        params = []
        for p in named(field(node, "parameters")):
            if p.type != "simple_parameter" or field(p, "type") is None:
                raise UnsupportedSyntax(line(p), "parameters need a type hint like `int $a`")
            name = self.ident(field(p, "name"))
            self.seen.add(name)
            params.append(nast.Param(name, self.scalar(field(p, "type"))))
        ret_node = field(node, "return_type")
        if ret_node is None:
            raise UnsupportedSyntax(line(node), "functions need a return type like `: int` (or `: void`)")
        ret = self.scalar(ret_node, allow_void=True)
        return nast.Function(text(field(node, "name")), params, ret, self.block(field(node, "body")), line(node), static=True)

    def expression_statement(self, e, stmt) -> list:
        out = super().expression_statement(e, stmt)
        for i, st in enumerate(out):
            if isinstance(st, nast.Assign) and isinstance(st.target, nast.Name) and st.target.id not in self.seen:
                self.seen.add(st.target.id)
                out[i] = nast.Declare(st.target.id, None, st.value, st.line)
        return out

    def stmt(self, n) -> list:
        if n.type == "if_statement":
            return [self.if_chain(n, list(n.children_by_field_name("alternative")))]
        if n.type == "expression_statement" and named(n)[0].type == "function_call_expression":
            return [nast.ExprStmt(self.expr(named(n)[0]), line(n))]
        return super().stmt(n)

    def if_chain(self, n, alternatives) -> nast.If:
        orelse = []
        if alternatives:
            head, rest = alternatives[0], alternatives[1:]
            if head.type == "else_if_clause":
                orelse = [self.if_chain(head, rest)]
            else:
                orelse = self.block(field(head, "body"))
        return nast.If(self.expr(field(n, "condition")), self.block(field(n, "body")), orelse, line(n))

    def call_parts(self, n):
        fn = field(n, "function")
        if fn is None or fn.type != "name":
            raise UnsupportedSyntax(line(n), "only calls to plain functions are supported")
        return fn, [named(a)[0] for a in named(field(n, "arguments"))]

    def expr(self, n):
        t, where = n.type, line(n)
        if t == "binary_expression" and operator(n) == "/":
            return nast.Binary("fdiv", self.expr(field(n, "left")), self.expr(field(n, "right")), where)
        if t == "function_call_expression" and text(field(n, "function")) == "intdiv":
            args = [named(a)[0] for a in named(field(n, "arguments"))]
            if len(args) != 2:
                raise UnsupportedSyntax(where, "intdiv() takes two arguments")
            return nast.Binary("div", self.expr(args[0]), self.expr(args[1]), where)
        if t == "function_call_expression":
            fn, args = self.call_parts(n)
            return nast.Call(text(fn), [self.expr(a) for a in args], where)
        if t == "unary_op_expression":
            op, operand = self.unary_parts(n)
            value = self.expr(operand)
            if op == "-":
                return _ts.negate(value)
            if op == "!":
                return nast.Not(value, where)
            raise UnsupportedSyntax(where, f"unsupported operator {op}")
        return super().expr(n)

    def cast_parts(self, n):
        if n.type == "cast_expression":
            return self.scalar(field(n, "type")), field(n, "value")
        return None

    def unary_parts(self, n):
        op = next(text(c) for c in n.children if not c.is_named)
        return op, named(n)[-1]

    def update_parts(self, n):
        return field(n, "argument"), operator(n) or ("++" if "++" in text(n) else "--")


def parse(source: str) -> nast.Module:
    return PhpFrontend(source, _parser()).module()


def build_module(source: str, context: ir.Context) -> ir.Module:
    return lower_module(parse(source), context)
