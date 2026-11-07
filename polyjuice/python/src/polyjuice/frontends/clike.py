"""Shared mapping for the brace-and-semicolon languages (C, C++, Java, C#, JavaScript, ...).

Their grammars name most nodes alike (`if_statement`, `while_statement`, `binary_expression`,
`return_statement`) but differ in field names and in declarations, types, calls and casts.
A subclass fills in those differences through the small hooks below; everything else is
shared here: control flow, assignment operators, counted-loop recognition, operators.
"""

from polyjuice import nast
from polyjuice.frontends import _ts
from polyjuice.frontends._ts import field, line, named, operator, text
from polyjuice.nast import FLOAT, INT, UnsupportedSyntax

BINARY = {"+": "+", "-": "-", "*": "*", "/": "div", "%": "mod"}
COMPARE = {"<", "<=", ">", ">=", "==", "!="}
ASSIGN = {"+=": "+", "-=": "-", "*=": "*", "/=": "div", "%=": "mod"}


class CLike:
    # -- hooks a language overrides -------------------------------------
    number_types = {"number_literal"}
    identifier_types = {"identifier"}
    index_types = {"subscript_expression"}
    index_array_field, index_field = "argument", "index"
    unary_operand_field = "argument"
    init_fields, update_fields = ("initializer", "init"), ("update", "increment")
    strict_equality = {"===": "==", "!==": "!="}  # JavaScript only; harmless elsewhere

    def __init__(self, source: str, parser):
        self.root = _ts.parse(parser, source)

    def module(self) -> nast.Module:
        raise NotImplementedError

    def scalar(self, node, allow_void: bool = False):
        raise NotImplementedError

    def declaration(self, n) -> list:
        raise NotImplementedError

    def call_parts(self, n):
        """(callee name node, argument nodes) of a call expression."""
        fn = field(n, "function")
        if fn is None or fn.type not in self.identifier_types:
            raise UnsupportedSyntax(line(n), "only calls to plain functions are supported")
        return fn, named(field(n, "arguments"))

    def cast_parts(self, n):
        """(target scalar type, operand node) of a cast, or None if `n` is not a cast."""
        return None

    def update_parts(self, n):
        """(target node, '++' or '--') of an increment/decrement expression."""
        arg = field(n, "argument") or named(n)[0]
        return arg, operator(n) or ("++" if "++" in text(n) else "--")

    def literal(self, n):
        return None  # languages with true/false/null etc. may refuse them explicitly

    # -- statements -----------------------------------------------------
    def first(self, n, *names):
        for name in names:
            found = field(n, name)
            if found is not None:
                return found
        return None

    def block(self, node) -> list:
        if node.type in ("compound_statement", "block", "statement_block"):
            out = []
            for child in named(node):
                out.extend(self.stmt(child))
            return out
        return self.stmt(node)

    def stmt(self, n) -> list:
        t, where = n.type, line(n)
        if t in ("declaration", "local_variable_declaration", "lexical_declaration", "variable_declaration", "local_declaration_statement"):
            return self.declaration(n)
        if t == "expression_statement":
            return self.expression_statement(named(n)[0], n)
        if t == "if_statement":
            alt = field(n, "alternative")
            orelse = []
            if alt is not None:
                inner = named(alt)[0] if alt.type == "else_clause" else alt
                orelse = self.block(inner)
            return [nast.If(self.expr(field(n, "condition")), self.block(field(n, "consequence")), orelse, where)]
        if t == "while_statement":
            return [nast.While(self.expr(field(n, "condition")), self.block(field(n, "body")), where)]
        if t == "for_statement":
            return self.for_statement(n)
        if t == "return_statement":
            kids = named(n)
            return [nast.Return(self.expr(kids[0]) if kids else None, where)]
        if t in ("compound_statement", "block", "statement_block"):
            return [nast.Scope(self.block(n), where)]
        raise UnsupportedSyntax(where, f"unsupported statement {t}")

    def expression_statement(self, e, stmt) -> list:
        where = line(stmt)
        if e.type == "assignment_expression":
            left, op = self.target(field(e, "left")), operator(e)
            value = self.expr(field(e, "right"))
            if op == "=":
                return [nast.Assign(left, value, where)]
            if op in ASSIGN:
                return [nast.Assign(left, nast.Binary(ASSIGN[op], self.expr(field(e, "left")), value, where), where)]
            raise UnsupportedSyntax(where, f"unsupported assignment operator {op}")
        if e.type in ("update_expression", "postfix_unary_expression", "prefix_unary_expression"):
            arg, op = self.update_parts(e)
            step = "+" if op == "++" else "-"
            return [nast.Assign(self.target(arg), nast.Binary(step, self.expr(arg), nast.Const(1, where), where), where)]
        if e.type in ("call_expression", "method_invocation", "invocation_expression"):
            return [nast.ExprStmt(self.expr(e), where)]
        raise UnsupportedSyntax(where, f"unsupported expression statement {e.type}")

    def target(self, n):
        if n.type in self.identifier_types:
            return nast.Name(text(n), line(n))
        if n.type in self.index_types:
            array = field(n, self.index_array_field)
            if array is not None and array.type in self.identifier_types:
                return nast.Index(text(array), self.expr(field(n, self.index_field)), line(n))
        raise UnsupportedSyntax(line(n), "unsupported assignment target")

    def for_statement(self, n) -> list:
        where = line(n)
        init, cond, update = self.first(n, *self.init_fields), field(n, "condition"), self.first(n, *self.update_fields)
        if cond is None or update is None:
            raise UnsupportedSyntax(where, "for loops need a condition and an update")
        body = self.block(field(n, "body"))
        pre = []
        if init is not None:
            pre = self.declaration(init) if init.type != "expression_statement" and init.type != "assignment_expression" else self.expression_statement(init if init.type != "expression_statement" else named(init)[0], init)
        step = self.expression_statement(update, update)
        if (
            len(pre) == 1 and isinstance(pre[0], nast.Declare) and pre[0].value is not None
            and pre[0].type in (INT, None) and cond.type == "binary_expression"
            and operator(cond) in ("<", "<=") and field(cond, "left").type in self.identifier_types
            and text(field(cond, "left")) == pre[0].name and len(step) == 1 and is_increment(step[0], pre[0].name)
        ):  # fmt: skip
            counted = _ts.counted_loop(
                pre[0].name, pre[0].value, operator(cond), self.expr(field(cond, "right")), 1, body, where
            )
            if counted is not None:
                return [counted]
        return [nast.Scope(pre + [nast.While(self.expr(cond), body + step, where)], where)]

    # -- expressions ----------------------------------------------------
    def expr(self, n):
        t, where = n.type, line(n)
        if t in self.number_types:
            return _ts.number(n)
        if t in self.identifier_types:
            return nast.Name(text(n), where)
        if t == "parenthesized_expression":
            return self.expr(named(n)[0])
        if t == "binary_expression":
            op = self.strict_equality.get(operator(n), operator(n))
            left, right = self.expr(field(n, "left")), self.expr(field(n, "right"))
            if op in BINARY:
                return nast.Binary(BINARY[op], left, right, where)
            if op in COMPARE:
                return nast.Compare(op, left, right, where)
            if op in ("&&", "||"):
                return nast.Logical("and" if op == "&&" else "or", left, right, where)
            raise UnsupportedSyntax(where, f"unsupported operator {op}")
        if t in ("unary_expression", "prefix_unary_expression"):
            op, operand = operator(n), self.expr(field(n, self.unary_operand_field) or named(n)[-1])
            if op == "-":
                return _ts.negate(operand)
            if op == "+":
                return operand
            if op == "!":
                return nast.Not(operand, where)
            raise UnsupportedSyntax(where, f"unsupported operator {op}")
        cast = self.cast_parts(n)
        if cast is not None:
            return nast.Cast(cast[0], self.expr(cast[1]), where)
        if t in ("call_expression", "method_invocation", "invocation_expression"):
            fn, args = self.call_parts(n)
            return nast.Call(text(fn), [self.expr(a) for a in args], where)
        if t in self.index_types:
            array = field(n, self.index_array_field)
            if array is not None and array.type in self.identifier_types:
                return nast.Index(text(array), self.expr(field(n, self.index_field)), where)
        found = self.literal(n)
        if found is not None:
            return found
        raise UnsupportedSyntax(where, f"unsupported expression {t}")


def is_increment(stmt, var: str) -> bool:
    return (
        isinstance(stmt, nast.Assign) and isinstance(stmt.target, nast.Name) and stmt.target.id == var
        and isinstance(stmt.value, nast.Binary) and stmt.value.op == "+"
        and isinstance(stmt.value.left, nast.Name) and stmt.value.left.id == var
        and isinstance(stmt.value.right, nast.Const) and stmt.value.right.value == 1
    )  # fmt: skip
