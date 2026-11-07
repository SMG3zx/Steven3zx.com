"""Go frontend: tree-sitter-go -> neutral AST.

Supported: functions over `int`/`int32` and `float64` (Go's `int` is 64-bit; it is treated as
32-bit like Python's), `*[N]T` and read-only `[N]T` parameters; `:=`, `var`, `=`, `op=`, `++`,
`--`, multi-assignment; `if/else`, three-clause `for`, `for cond`, `for i := range n`, early
`return`; calls; `float64()`/`int()` conversions. Refused with a line number: slices, maps,
structs, goroutines, closures, multiple results, `break`/`continue`, `switch`, `defer`,
strings, `bool` values.
"""

import tree_sitter_go

from polyjuice import nast
from polyjuice.frontends import _ts
from polyjuice.frontends._ts import field, line, named, operator, text
from polyjuice.lower import lower_module
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.nast import FLOAT, INT, ArrayType, UnsupportedSyntax

ensure_mlir_importable()

from mlir import ir  # noqa: E402

_PARSER = None
_INT_TYPES = {"int", "int32"}
_FLOAT_TYPES = {"float64"}
_BINARY = {"+": "+", "-": "-", "*": "*", "/": "div", "%": "mod"}
_COMPARE = {"<", "<=", ">", ">=", "==", "!="}
_ASSIGN = {"+=": "+", "-=": "-", "*=": "*", "/=": "div", "%=": "mod"}


def _parser():
    global _PARSER
    if _PARSER is None:
        _PARSER = _ts.make_parser(tree_sitter_go.language())
    return _PARSER


class _Go:
    def __init__(self, source: str):
        self.root = _ts.parse(_parser(), source)
        self.scopes: list[set[str]] = []

    def scalar(self, node):
        name = text(node)
        if name in _INT_TYPES:
            return INT
        if name in _FLOAT_TYPES:
            return FLOAT
        raise UnsupportedSyntax(line(node), f"unsupported type {name!r}")

    def type_of(self, node):
        if node.type == "array_type":
            length = field(node, "length")
            if length.type != "int_literal":
                raise UnsupportedSyntax(line(node), "array types need a constant length")
            return ArrayType(self.scalar(field(node, "element")), int(text(length), 0))
        if node.type == "pointer_type" and named(node)[0].type == "array_type":
            return self.type_of(named(node)[0])
        return self.scalar(node)

    def module(self) -> nast.Module:
        functions = []
        for node in named(self.root):
            if node.type == "function_declaration":
                functions.append(self.function(node))
            elif node.type in ("package_clause", "import_declaration"):
                continue
            else:
                raise UnsupportedSyntax(line(node), f"unsupported top-level {node.type}")
        return nast.Module(functions)

    def function(self, node) -> nast.Function:
        params = []
        for p in named(field(node, "parameters")):
            if p.type != "parameter_declaration":
                raise UnsupportedSyntax(line(p), "variadic parameters are not supported")
            kind = field(p, "type")
            for name in p.children_by_field_name("name"):
                params.append(nast.Param(text(name), self.type_of(kind)))
        result = field(node, "result")
        ret = None
        if result is not None:
            if result.type == "parameter_list":
                raise UnsupportedSyntax(line(result), "named or multiple results are not supported")
            ret = self.scalar(result)
        body = self.block(field(node, "body"), [p.name for p in params])
        self.check_value_arrays(node, params, body)
        return nast.Function(text(field(node, "name")), params, ret, body, line(node), static=True)

    def check_value_arrays(self, node, params, body) -> None:
        value_arrays = {
            text(name)
            for p in named(field(node, "parameters"))
            if field(p, "type").type == "array_type"
            for name in p.children_by_field_name("name")
        }
        for st in _walk(body):
            if isinstance(st, nast.Assign) and isinstance(st.target, nast.Index) and st.target.array in value_arrays:
                raise UnsupportedSyntax(
                    st.line, f"{st.target.array!r} is an array value (copied in Go); take a *[N]T to mutate it"
                )

    # -- statements -----------------------------------------------------
    def block(self, node, declared=()) -> list:
        self.scopes.append(set(declared))
        out = []
        statements = [c for c in named(node)]
        if len(statements) == 1 and statements[0].type == "statement_list":
            statements = named(statements[0])
        for child in statements:
            out.extend(self.stmt(child))
        self.scopes.pop()
        return out

    def stmt(self, n) -> list:
        t, where = n.type, line(n)
        if t == "short_var_declaration":
            return self.short_decl(n)
        if t == "var_declaration":
            out = []
            for spec in named(n):
                out.extend(self.var_spec(spec))
            return out
        if t == "assignment_statement":
            return self.assignment(n)
        if t in ("inc_statement", "dec_statement"):
            target = self.expr(named(n)[0])
            if not isinstance(target, (nast.Name, nast.Index)):
                raise UnsupportedSyntax(where, "unsupported increment target")
            return [nast.Assign(target, nast.Binary("+" if t == "inc_statement" else "-", self.expr(named(n)[0]), nast.Const(1, where), where), where)]
        if t == "expression_statement":
            call = self.expr(named(n)[0])
            if not isinstance(call, nast.Call):
                raise UnsupportedSyntax(where, "expression statement without effect")
            return [nast.ExprStmt(call, where)]
        if t == "if_statement":
            if field(n, "initializer") is not None:
                raise UnsupportedSyntax(where, "if with an initializer is not supported")
            alt = field(n, "alternative")
            orelse = []
            if alt is not None:
                orelse = self.stmt(alt) if alt.type == "if_statement" else self.block(alt)
            return [nast.If(self.expr(field(n, "condition")), self.block(field(n, "consequence")), orelse, where)]
        if t == "for_statement":
            return self.for_statement(n)
        if t == "return_statement":
            values = named(n)
            if values and values[0].type == "expression_list":
                values = named(values[0])
            if len(values) > 1:
                raise UnsupportedSyntax(where, "multiple return values are not supported")
            return [nast.Return(self.expr(values[0]) if values else None, where)]
        if t == "block":
            return [nast.Scope(self.block(n), where)]
        raise UnsupportedSyntax(where, f"unsupported statement {t}")

    def declare_name(self, name: str) -> bool:
        """True if `name` is new in the current scope (a real declaration)."""
        if name in self.scopes[-1]:
            return False
        self.scopes[-1].add(name)
        return True

    def short_decl(self, n) -> list:
        names = named(field(n, "left"))
        values = [self.expr(v) for v in named(field(n, "right"))]
        if len(names) != len(values) or not all(x.type == "identifier" for x in names):
            raise UnsupportedSyntax(line(n), "unsupported short variable declaration")
        where = line(n)
        fresh = [self.declare_name(text(x)) for x in names]
        if len(names) == 1:
            name = text(names[0])
            return [nast.Declare(name, None, values[0], where) if fresh[0] else nast.Assign(nast.Name(name, where), values[0], where)]
        if all(fresh):
            if any(_mentions(v, {text(x) for x in names}) for v in values):
                raise UnsupportedSyntax(where, "unsupported multi-declaration that refers to itself")
            return [nast.Declare(text(x), None, v, where) for x, v in zip(names, values)]
        if not any(fresh):
            return [nast.ParallelAssign([nast.Name(text(x), where) for x in names], values, where)]
        raise UnsupportedSyntax(where, "mixed new and existing names in one := are not supported")

    def var_spec(self, spec) -> list:
        where = line(spec)
        kind = field(spec, "type")
        names = spec.children_by_field_name("name")
        value = field(spec, "value")
        values = [self.expr(v) for v in named(value)] if value is not None else []
        if values and len(values) != len(names):
            raise UnsupportedSyntax(where, "unsupported var declaration")
        out = []
        for i, name in enumerate(names):
            if not self.declare_name(text(name)):
                raise UnsupportedSyntax(where, f"{text(name)!r} is already declared")
            out.append(nast.Declare(text(name), self.scalar(kind) if kind is not None else None, values[i] if values else None, where))
        return out

    def assignment(self, n) -> list:
        where = line(n)
        left = [self.target(x) for x in named(field(n, "left"))]
        right = [self.expr(x) for x in named(field(n, "right"))]
        op = operator(n)
        if len(left) != len(right):
            raise UnsupportedSyntax(where, "unsupported assignment")
        if op == "=":
            if len(left) == 1:
                return [nast.Assign(left[0], right[0], where)]
            if all(isinstance(t, nast.Name) for t in left):
                return [nast.ParallelAssign(left, right, where)]
            raise UnsupportedSyntax(where, "multi-assignment to array elements is not supported")
        if op in _ASSIGN and len(left) == 1:
            return [nast.Assign(left[0], nast.Binary(_ASSIGN[op], self.expr(named(field(n, "left"))[0]), right[0], where), where)]
        raise UnsupportedSyntax(where, f"unsupported assignment operator {op}")

    def target(self, n):
        if n.type == "identifier":
            return nast.Name(text(n), line(n))
        if n.type == "index_expression" and field(n, "operand").type == "identifier":
            return nast.Index(text(field(n, "operand")), self.expr(field(n, "index")), line(n))
        raise UnsupportedSyntax(line(n), "unsupported assignment target")

    def for_statement(self, n) -> list:
        where = line(n)
        body_node = field(n, "body")
        children = [c for c in named(n) if c != body_node]
        head = children[0] if children else None
        if head is None or head.type == "block":
            raise UnsupportedSyntax(where, "infinite `for` loops are not supported")
        if head.type == "range_clause":
            left = named(field(head, "left"))
            if len(left) != 1 or left[0].type != "identifier":
                raise UnsupportedSyntax(where, "only `for i := range n` is supported")
            var = text(left[0])
            self.scopes.append({var})
            body = self.block(body_node)
            self.scopes.pop()
            return [nast.For(var, None, self.expr(field(head, "right")), body, where)]
        if head.type == "for_clause":
            init, cond, update = field(head, "initializer"), field(head, "condition"), field(head, "update")
            if cond is None or update is None or init is None:
                raise UnsupportedSyntax(where, "for loops need an initializer, condition and update")
            self.scopes.append(set())
            pre = self.stmt(init)
            body = self.block(body_node)
            step = self.stmt(update)
            self.scopes.pop()
            if (
                len(pre) == 1 and isinstance(pre[0], nast.Declare) and pre[0].value is not None
                and cond.type == "binary_expression" and operator(cond) in ("<", "<=")
                and field(cond, "left").type == "identifier" and text(field(cond, "left")) == pre[0].name
                and len(step) == 1 and _is_increment(step[0], pre[0].name)
            ):  # fmt: skip
                counted = _ts.counted_loop(
                    pre[0].name, pre[0].value, operator(cond), self.expr(field(cond, "right")), 1, body, where
                )
                if counted is not None:
                    return [counted]
            return [nast.Scope(pre + [nast.While(self.expr(cond), body + step, where)], where)]
        # `for cond { ... }`
        return [nast.While(self.expr(head), self.block(body_node), where)]

    # -- expressions ----------------------------------------------------
    def expr(self, n):
        t, where = n.type, line(n)
        if t == "int_literal":
            return _ts.number(n)
        if t == "float_literal":
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
            op, operand = operator(n), self.expr(field(n, "operand"))
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
            args = [self.expr(a) for a in named(field(n, "arguments"))]
            name = text(fn)
            if name in _INT_TYPES | _FLOAT_TYPES:
                if len(args) != 1:
                    raise UnsupportedSyntax(where, "a conversion takes one argument")
                return nast.Cast(INT if name in _INT_TYPES else FLOAT, args[0], where)
            return nast.Call(name, args, where)
        if t == "index_expression" and field(n, "operand").type == "identifier":
            return nast.Index(text(field(n, "operand")), self.expr(field(n, "index")), where)
        raise UnsupportedSyntax(where, f"unsupported expression {t}")


def _is_increment(stmt, var: str) -> bool:
    return (
        isinstance(stmt, nast.Assign) and isinstance(stmt.target, nast.Name) and stmt.target.id == var
        and isinstance(stmt.value, nast.Binary) and stmt.value.op == "+"
        and isinstance(stmt.value.left, nast.Name) and stmt.value.left.id == var
        and isinstance(stmt.value.right, nast.Const) and stmt.value.right.value == 1
    )  # fmt: skip


def _walk(stmts):
    for st in stmts:
        yield st
        for attr in ("body", "then", "orelse"):
            inner = getattr(st, attr, None)
            if isinstance(inner, list):
                yield from _walk(inner)


def _mentions(expr, names: set[str]) -> bool:
    if isinstance(expr, nast.Name):
        return expr.id in names
    return any(
        _mentions(getattr(expr, a), names)
        for a in ("left", "right", "operand")
        if hasattr(expr, a)
    ) or any(_mentions(a, names) for a in getattr(expr, "args", []))


def parse(source: str) -> nast.Module:
    return _Go(source).module()


def build_module(source: str, context: ir.Context) -> ir.Module:
    return lower_module(parse(source), context)
