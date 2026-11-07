"""Rust frontend: tree-sitter-rust -> neutral AST.

Supported: `fn` over `i32` and `f64`, with `&[T; N]` / `&mut [T; N]` array parameters; `let`
(with `mut`, optional type), assignment and compound assignment, `if/else if/else`, `while`,
`for i in a..b` and `a..=b`, early `return`, a tail expression as the return value (including a
tail `if/else`), calls, `as i32` / `as f64` / `as usize` casts, `&& || !`. Arithmetic wraps like
the shared layer (release-mode Rust; debug builds panic on overflow). Refused with a line
number: structs, traits, generics, tuples, slices, `loop`, `break`/`continue`, `match`,
macros, references to scalars, `bool` values, other integer widths.
"""

import tree_sitter_rust

from polyjuice import nast
from polyjuice.frontends import _ts
from polyjuice.frontends._ts import field, line, named, operator, text
from polyjuice.frontends.clike import ASSIGN, BINARY, COMPARE
from polyjuice.lower import lower_module
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.nast import FLOAT, INT, ArrayType, UnsupportedSyntax

ensure_mlir_importable()

from mlir import ir  # noqa: E402

_PARSER = None
_STATEMENTS = {"expression_statement", "let_declaration", "empty_statement"}


def _parser():
    global _PARSER
    if _PARSER is None:
        _PARSER = _ts.make_parser(tree_sitter_rust.language())
    return _PARSER


class _Rust:
    def __init__(self, source: str):
        self.root = _ts.parse(_parser(), source)

    # -- types ----------------------------------------------------------
    def scalar(self, node):
        name = text(node)
        if name == "i32":
            return INT
        if name == "f64":
            return FLOAT
        raise UnsupportedSyntax(line(node), f"unsupported type {name!r}")

    def type_of(self, node):
        if node.type == "reference_type":
            inner = field(node, "type")
            if inner.type == "array_type":
                return self.array(inner)
            raise UnsupportedSyntax(line(node), "references to scalars are not supported")
        if node.type == "array_type":
            raise UnsupportedSyntax(line(node), "pass arrays by reference (&[T; N]); a by-value array is copied")
        return self.scalar(node)

    def array(self, node):
        length = field(node, "length")
        if length.type != "integer_literal":
            raise UnsupportedSyntax(line(node), "array types need a constant length")
        return ArrayType(self.scalar(field(node, "element")), int(text(length).replace("_", ""), 0))

    # -- module ---------------------------------------------------------
    def module(self) -> nast.Module:
        functions = []
        for node in named(self.root):
            if node.type == "function_item":
                functions.append(self.function(node))
            elif node.type in ("attribute_item", "use_declaration"):
                continue
            else:
                raise UnsupportedSyntax(line(node), f"unsupported item {node.type}")
        return nast.Module(functions)

    def function(self, node) -> nast.Function:
        if field(node, "type_parameters") is not None:
            raise UnsupportedSyntax(line(node), "generic functions are not supported")
        params = []
        for p in named(field(node, "parameters")):
            if p.type != "parameter":
                raise UnsupportedSyntax(line(p), f"unsupported parameter {p.type}")
            pattern = field(p, "pattern")
            if pattern.type == "mut_pattern":
                pattern = next(c for c in named(pattern) if c.type == "identifier")
            if pattern.type != "identifier":
                raise UnsupportedSyntax(line(p), "unsupported parameter pattern")
            params.append(nast.Param(text(pattern), self.type_of(field(p, "type"))))
        ret_node = field(node, "return_type")
        ret = self.scalar(ret_node) if ret_node is not None else None
        body = self.block(field(node, "body"), value=ret is not None)
        return nast.Function(text(field(node, "name")), params, ret, body, line(node), static=True)

    # -- blocks and statements -------------------------------------------
    def parts(self, block):
        kids = named(block)
        if not kids:
            return kids, None
        last = kids[-1]
        if last.type not in _STATEMENTS:
            return kids[:-1], last
        # a trailing `if { } else { }` without a semicolon is wrapped as a statement but is the value
        if last.type == "expression_statement" and not text(last).rstrip().endswith(";"):
            return kids[:-1], named(last)[0]
        return kids, None

    def block(self, block, value: bool = False) -> list:
        """Statements of `block`; with `value`, the tail expression becomes a return."""
        statements, tail = self.parts(block)
        out = []
        for child in statements:
            out.extend(self.stmt(child))
        if tail is not None:
            out.extend(self.tail_return(tail) if value else self.stmt(tail))
        elif value:
            pass  # every path must return explicitly; lowering reports a missing return
        return out

    def tail_return(self, e) -> list:
        where = line(e)
        if e.type == "if_expression":
            alt = field(e, "alternative")
            if alt is None:
                raise UnsupportedSyntax(where, "an `if` used as a value needs an `else`")
            inner = named(alt)[0]
            orelse = self.tail_return(inner) if inner.type == "if_expression" else self.block(inner, value=True)
            return [nast.If(self.expr(field(e, "condition")), self.block(field(e, "consequence"), value=True), orelse, where)]
        if e.type == "return_expression":
            return self.stmt(e)
        return [nast.Return(self.expr(e), where)]

    def stmt(self, n) -> list:
        t, where = n.type, line(n)
        if t == "empty_statement":
            return []
        if t == "expression_statement":
            return self.stmt(named(n)[0])
        if t == "let_declaration":
            pattern = field(n, "pattern")
            if pattern.type != "identifier":
                raise UnsupportedSyntax(where, "only simple `let name = ...` patterns are supported")
            kind_node, value = field(n, "type"), field(n, "value")
            if value is None and kind_node is None:
                raise UnsupportedSyntax(where, "`let` needs a type or a value")
            kind = self.scalar(kind_node) if kind_node is not None else None
            return [nast.Declare(text(pattern), kind, self.expr(value) if value is not None else None, where)]
        if t == "assignment_expression":
            return [nast.Assign(self.target(field(n, "left")), self.expr(field(n, "right")), where)]
        if t == "compound_assignment_expr":
            op = operator(n)
            if op not in ASSIGN:
                raise UnsupportedSyntax(where, f"unsupported assignment operator {op}")
            left = self.expr(field(n, "left"))
            return [nast.Assign(self.target(field(n, "left")), nast.Binary(ASSIGN[op], left, self.expr(field(n, "right")), where), where)]
        if t == "call_expression":
            return [nast.ExprStmt(self.expr(n), where)]
        if t == "return_expression":
            kids = named(n)
            return [nast.Return(self.expr(kids[0]) if kids else None, where)]
        if t == "if_expression":
            alt = field(n, "alternative")
            orelse = []
            if alt is not None:
                inner = named(alt)[0]
                orelse = self.stmt(inner) if inner.type == "if_expression" else self.block(inner)
            return [nast.If(self.expr(field(n, "condition")), self.block(field(n, "consequence")), orelse, where)]
        if t == "while_expression":
            return [nast.While(self.expr(field(n, "condition")), self.block(field(n, "body")), where)]
        if t == "for_expression":
            return [self.for_loop(n)]
        raise UnsupportedSyntax(where, f"unsupported statement {t}")

    def for_loop(self, n):
        where = line(n)
        pattern, rng = field(n, "pattern"), field(n, "value")
        if pattern.type != "identifier" or rng.type != "range_expression":
            raise UnsupportedSyntax(where, "only `for i in a..b` and `a..=b` are supported")
        bounds = named(rng)
        if len(bounds) != 2:
            raise UnsupportedSyntax(where, "open-ended ranges are not supported")
        inclusive = any(text(c) == "..=" for c in rng.children if not c.is_named)
        stop = self.expr(bounds[1])
        if inclusive:
            stop = nast.Binary("+", stop, nast.Const(1, where), where)
        return nast.For(text(pattern), self.expr(bounds[0]), stop, self.block(field(n, "body")), where)

    def target(self, n):
        if n.type == "identifier":
            return nast.Name(text(n), line(n))
        if n.type == "index_expression":
            array, index = named(n)
            if array.type == "identifier":
                return nast.Index(text(array), self.expr(index), line(n))
        raise UnsupportedSyntax(line(n), "unsupported assignment target")

    # -- expressions ----------------------------------------------------
    def expr(self, n):
        t, where = n.type, line(n)
        if t in ("integer_literal", "float_literal"):
            return _ts.number(n)
        if t == "identifier":
            return nast.Name(text(n), where)
        if t == "parenthesized_expression":
            return self.expr(named(n)[0])
        if t == "binary_expression":
            op = operator(n)
            left, right = self.expr(field(n, "left")), self.expr(field(n, "right"))
            if op in BINARY:
                return nast.Binary(BINARY[op], left, right, where)
            if op in COMPARE:
                return nast.Compare(op, left, right, where)
            if op in ("&&", "||"):
                return nast.Logical("and" if op == "&&" else "or", left, right, where)
            raise UnsupportedSyntax(where, f"unsupported operator {op}")
        if t == "unary_expression":
            op = next(text(c) for c in n.children if not c.is_named)
            operand = self.expr(named(n)[0])
            if op == "-":
                return _ts.negate(operand)
            if op == "!":
                return nast.Not(operand, where)
            raise UnsupportedSyntax(where, f"unsupported operator {op}")
        if t == "type_cast_expression":
            target = text(field(n, "type"))
            if target in ("i32", "usize"):
                return nast.Cast(INT, self.expr(field(n, "value")), where)
            if target == "f64":
                return nast.Cast(FLOAT, self.expr(field(n, "value")), where)
            raise UnsupportedSyntax(where, f"unsupported cast to {target}")
        if t == "call_expression":
            fn = field(n, "function")
            if fn.type != "identifier":
                raise UnsupportedSyntax(where, "only calls to plain functions are supported")
            return nast.Call(text(fn), [self.expr(a) for a in named(field(n, "arguments"))], where)
        if t == "index_expression":
            array, index = named(n)
            if array.type == "identifier":
                return nast.Index(text(array), self.expr(index), where)
        raise UnsupportedSyntax(where, f"unsupported expression {t}")


def parse(source: str) -> nast.Module:
    return _Rust(source).module()


def build_module(source: str, context: ir.Context) -> ir.Module:
    return lower_module(parse(source), context)
