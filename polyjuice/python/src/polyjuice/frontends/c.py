"""C frontend: tree-sitter-c -> neutral AST.

Supported: functions over `int`/`int32_t` and `double`, with `T name[N]` array parameters;
local scalars; `if/else`, `while`, `for`, early `return`; calls; arithmetic, comparisons,
`(int)`/`(double)` casts. Refused with a line number: pointers, structs, `long`/`float`/
`char`, `&& || !`, `break`/`continue`, `switch`, `do`, ternary, strings, local arrays.
"""

import tree_sitter_c

from polyjuice import nast
from polyjuice.frontends import _ts
from polyjuice.frontends._ts import field, line, named, operator, text
from polyjuice.lower import lower_module
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.nast import FLOAT, INT, ArrayType, UnsupportedSyntax

ensure_mlir_importable()

from mlir import ir  # noqa: E402

_PARSER = None
_BINARY = {"+": "+", "-": "-", "*": "*", "/": "div", "%": "mod"}
_COMPARE = {"<", "<=", ">", ">=", "==", "!="}
_ASSIGN = {"+=": "+", "-=": "-", "*=": "*", "/=": "div", "%=": "mod"}


def _parser():
    global _PARSER
    if _PARSER is None:
        _PARSER = _ts.make_parser(tree_sitter_c.language())
    return _PARSER


class CLike:
    """Mapping shared by C and C++ (their grammars name these nodes alike)."""

    int_types = {"int", "int32_t"}
    float_types = {"double"}

    def __init__(self, source: str, parser):
        self.root = _ts.parse(parser, source)

    # -- types ----------------------------------------------------------
    def scalar(self, node, allow_void: bool = False):
        name = text(node)
        if name in self.int_types:
            return INT
        if name in self.float_types:
            return FLOAT
        if name == "void" and allow_void:
            return None
        raise UnsupportedSyntax(line(node), f"unsupported type {name!r}")

    # -- module ---------------------------------------------------------
    def module(self) -> nast.Module:
        functions = []
        for node in named(self.root):
            if node.type == "function_definition":
                functions.append(self.function(node))
            elif node.type in ("preproc_include", "preproc_def") or self.is_prototype(node):
                continue
            else:
                raise UnsupportedSyntax(line(node), f"unsupported top-level {node.type}")
        return nast.Module(functions)

    def is_prototype(self, node) -> bool:
        return node.type == "declaration" and any(c.type == "function_declarator" for c in node.children)

    def function(self, node) -> nast.Function:
        declarator = field(node, "declarator")
        if declarator.type != "function_declarator" or field(declarator, "declarator").type != "identifier":
            raise UnsupportedSyntax(line(node), "unsupported function declarator (pointers?)")
        params = []
        for p in named(field(declarator, "parameters")):
            if p.type != "parameter_declaration":
                raise UnsupportedSyntax(line(p), f"unsupported parameter {p.type}")
            if text(field(p, "type")) == "void" and field(p, "declarator") is None:
                continue  # f(void)
            params.append(self.param(p))
        ret = self.scalar(field(node, "type"), allow_void=True)
        return nast.Function(
            text(field(declarator, "declarator")), params, ret, self.block(field(node, "body")),
            line(node), static=True,
        )  # fmt: skip

    def param(self, p) -> nast.Param:
        elem = self.scalar(field(p, "type"))
        d = field(p, "declarator")
        if d is not None and d.type == "identifier":
            return nast.Param(text(d), elem)
        if d is not None and d.type == "array_declarator" and field(d, "declarator").type == "identifier":
            size = field(d, "size")
            if size is None or size.type != "number_literal":
                raise UnsupportedSyntax(line(p), "array parameters need a constant size")
            return nast.Param(text(field(d, "declarator")), ArrayType(elem, int(text(size), 0)))
        raise UnsupportedSyntax(line(p), "unsupported parameter declarator (pointer?)")

    # -- statements -----------------------------------------------------
    def block(self, node) -> list:
        if node.type == "compound_statement":
            out = []
            for child in named(node):
                out.extend(self.stmt(child))
            return out
        return self.stmt(node)

    def stmt(self, n) -> list:
        t = n.type
        if t == "declaration":
            return self.declaration(n)
        if t == "expression_statement":
            return self.expression_statement(named(n)[0], n)
        if t == "if_statement":
            alt = field(n, "alternative")
            orelse = []
            if alt is not None:
                inner = named(alt)[0]
                orelse = self.block(inner)
            return [nast.If(self.expr(field(n, "condition")), self.block(field(n, "consequence")), orelse, line(n))]
        if t == "while_statement":
            return [nast.While(self.expr(field(n, "condition")), self.block(field(n, "body")), line(n))]
        if t == "for_statement":
            return self.for_statement(n)
        if t == "return_statement":
            kids = named(n)
            return [nast.Return(self.expr(kids[0]) if kids else None, line(n))]
        if t == "compound_statement":
            return [nast.Scope(self.block(n), line(n))]
        raise UnsupportedSyntax(line(n), f"unsupported statement {t}")

    def declaration(self, n) -> list:
        kind = self.scalar(field(n, "type"))
        out = []
        for d in n.children_by_field_name("declarator"):
            if d.type == "identifier":
                out.append(nast.Declare(text(d), kind, None, line(n)))
            elif d.type == "init_declarator" and field(d, "declarator").type == "identifier":
                out.append(nast.Declare(text(field(d, "declarator")), kind, self.expr(field(d, "value")), line(n)))
            else:
                raise UnsupportedSyntax(line(n), "unsupported declarator (local arrays and pointers are not supported)")
        return out

    def expression_statement(self, e, stmt) -> list:
        where = line(stmt)
        if e.type == "assignment_expression":
            left, op = self.target(field(e, "left")), operator(e)
            value = self.expr(field(e, "right"))
            if op == "=":
                return [nast.Assign(left, value, where)]
            if op in _ASSIGN:
                return [nast.Assign(left, nast.Binary(_ASSIGN[op], self.expr(field(e, "left")), value, where), where)]
            raise UnsupportedSyntax(where, f"unsupported assignment operator {op}")
        if e.type == "update_expression":
            target = self.target(field(e, "argument"))
            step = "+" if operator(e) == "++" else "-"
            return [nast.Assign(target, nast.Binary(step, self.expr(field(e, "argument")), nast.Const(1, where), where), where)]
        if e.type == "call_expression":
            call = self.expr(e)
            return [nast.ExprStmt(call, where)]
        raise UnsupportedSyntax(where, f"unsupported expression statement {e.type}")

    def target(self, n):
        if n.type == "identifier":
            return nast.Name(text(n), line(n))
        if n.type == "subscript_expression" and field(n, "argument").type == "identifier":
            return nast.Index(text(field(n, "argument")), self.expr(field(n, "index")), line(n))
        raise UnsupportedSyntax(line(n), "unsupported assignment target")

    def for_statement(self, n) -> list:
        where = line(n)
        init, cond, update = field(n, "initializer"), field(n, "condition"), field(n, "update")
        if cond is None or update is None:
            raise UnsupportedSyntax(where, "for loops need a condition and an update")
        body = self.block(field(n, "body"))
        counted = None
        if init is not None and init.type == "declaration" and cond.type == "binary_expression":
            decls = self.declaration(init)
            step = self.step_of(update)
            left = field(cond, "left")
            if (
                len(decls) == 1 and decls[0].type == INT and decls[0].value is not None
                and step is not None and step[0] == decls[0].name and step[1] == 1
                and left.type == "identifier" and text(left) == decls[0].name
            ):  # fmt: skip
                counted = _ts.counted_loop(
                    decls[0].name, decls[0].value, operator(cond), self.expr(field(cond, "right")), 1, body, where
                )
        if counted is not None:
            return [counted]
        pre = []
        if init is not None:
            pre = self.declaration(init) if init.type == "declaration" else self.expression_statement(init, init)
        step_stmts = self.expression_statement(update, update)
        return [nast.Scope(pre + [nast.While(self.expr(cond), body + step_stmts, where)], where)]

    def step_of(self, update):
        """(variable, +1) for `i++`, `++i`, `i += 1`; None otherwise."""
        if update.type == "update_expression" and operator(update) == "++":
            arg = field(update, "argument")
            return (text(arg), 1) if arg.type == "identifier" else None
        if update.type == "assignment_expression" and operator(update) == "+=":
            left, right = field(update, "left"), field(update, "right")
            if left.type == "identifier" and right.type == "number_literal" and text(right) == "1":
                return (text(left), 1)
        return None

    # -- expressions ----------------------------------------------------
    def expr(self, n):
        t, where = n.type, line(n)
        if t == "number_literal":
            return _ts.number(n)
        if t == "identifier":
            return nast.Name(text(n), where)
        if t == "parenthesized_expression":
            return self.expr(named(n)[0])
        if t == "binary_expression":
            op = operator(n)
            left, right = self.expr(field(n, "left")), self.expr(field(n, "right"))
            if op in _BINARY:
                return nast.Binary(_BINARY[op], left, right, where)
            if op in _COMPARE:
                return nast.Compare(op, left, right, where)
            if op in ("&&", "||"):
                return nast.Logical("and" if op == "&&" else "or", left, right, where)
            raise UnsupportedSyntax(where, f"unsupported operator {op}")
        if t == "unary_expression":
            op, operand = operator(n), self.expr(field(n, "argument"))
            if op == "-":
                return _ts.negate(operand)
            if op == "+":
                return operand
            if op == "!":
                return nast.Not(operand, where)
            raise UnsupportedSyntax(where, f"unsupported operator {op}")
        if t == "call_expression":
            fn = field(n, "function")
            if fn.type != "identifier":
                raise UnsupportedSyntax(where, "only calls to plain functions are supported")
            return nast.Call(text(fn), [self.expr(a) for a in named(field(n, "arguments"))], where)
        if t == "cast_expression":
            kind = self.scalar(field(field(n, "type"), "type"))
            return nast.Cast(kind, self.expr(field(n, "value")), where)
        if t == "subscript_expression" and field(n, "argument").type == "identifier":
            return nast.Index(text(field(n, "argument")), self.expr(field(n, "index")), where)
        raise UnsupportedSyntax(where, f"unsupported expression {t}")


def parse(source: str) -> nast.Module:
    return CLike(source, _parser()).module()


def build_module(source: str, context: ir.Context) -> ir.Module:
    return lower_module(parse(source), context)
