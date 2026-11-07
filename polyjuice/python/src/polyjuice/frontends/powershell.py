"""PowerShell frontend: tree-sitter-powershell -> neutral AST.

PowerShell is dynamically typed and function results are untyped, so types come from what the
language offers for it: `[int]` / `[double]` attributes on parameters, `[int]$x = ...` typed
locals, and `[OutputType([int])]` for the result. An untyped local takes the type of its first
assignment. Names are case-insensitive in PowerShell; one spelling per name is required here.
`int` is treated as 32-bit (PowerShell promotes to double on overflow). `/` is refused on ints
(PowerShell returns an int or a double depending on the values), `%` truncates like C, and
casting a double to `[int]` is refused because PowerShell rounds where the shared layer truncates.
Supported: functions, assignment and compound assignment, `++`/`--`, `if/elseif/else`,
`while`, `for`, `return`, calls written as commands (`Gcd $a $b`), `-lt -le -gt -ge -eq -ne
-and -or -not`. Refused with a line number: pipelines, strings, arrays, hashtables, `foreach`,
`switch`, `break`/`continue`, `$true`/`$false`/`$null`, switches and named command parameters.
"""

import tree_sitter_powershell

from polyjuice import nast
from polyjuice.frontends import _ts
from polyjuice.frontends.javascript import returns_a_value
from polyjuice.frontends._ts import field, line, named, text
from polyjuice.lower import lower_module
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.nast import FLOAT, INT, UnsupportedSyntax

ensure_mlir_importable()

from mlir import ir  # noqa: E402

_PARSER = None
_ARITH = {"+": "+", "-": "-", "*": "*", "%": "mod"}
_COMPARE = {"-lt": "<", "-le": "<=", "-gt": ">", "-ge": ">=", "-eq": "==", "-ne": "!=",
            "-ilt": "<", "-ile": "<=", "-igt": ">", "-ige": ">=", "-ieq": "==", "-ine": "!="}  # fmt: skip
_ASSIGN = {"+=": "+", "-=": "-", "*=": "*", "%=": "mod"}
_RESERVED_VARIABLES = {"true", "false", "null", "_", "args", "input", "error", "pid", "home", "pwd"}


def _parser():
    global _PARSER
    if _PARSER is None:
        _PARSER = _ts.make_parser(tree_sitter_powershell.language())
    return _PARSER


def core(n):
    """Descend through the single-child wrapper chain PowerShell's grammar puts around everything."""
    while len(n.children) == 1 and n.children[0].is_named:
        n = n.children[0]
    return n


def tokens(n) -> list[str]:
    return [text(c) for c in n.children if not c.is_named]


class _PowerShell:
    def __init__(self, source: str):
        self.root = _ts.parse(_parser(), source)
        self.functions: dict[str, str] = {}  # lowercase -> first spelling
        self.names: dict[str, str] = {}
        self.seen: set[str] = set()

    # -- names and types --------------------------------------------------
    def spelled(self, table: dict, name: str, where: int) -> str:
        first = table.setdefault(name.lower(), name)
        if first != name:
            raise UnsupportedSyntax(where, f"{name!r} and {first!r} are the same name in PowerShell; use one spelling")
        return first

    def type_name(self, n) -> str:
        """The `int` in `[int]`, found inside whatever attribute wrappers surround it."""
        stack = [n]
        while stack:
            node = stack.pop()
            if node.type == "type_identifier":
                return text(node).lower()
            stack.extend(reversed(node.children))
        raise UnsupportedSyntax(line(n), "missing type")

    def find(self, n, kind: str):
        """First descendant of the given node type."""
        stack = [n]
        while stack:
            node = stack.pop(0)
            if node.type == kind:
                return node
            stack.extend(node.children)
        return None

    def scalar(self, name: str, where: int):
        if name in ("int", "int32"):
            return INT
        if name == "double":
            return FLOAT
        raise UnsupportedSyntax(where, f"unsupported type [{name}]")

    # -- module and functions ----------------------------------------------
    def module(self) -> nast.Module:
        functions = []
        for node in self.iter_statements(self.root):
            if node.type != "function_statement":
                raise UnsupportedSyntax(line(node), f"unsupported top-level {node.type}")
            functions.append(self.function(node))
        return nast.Module(functions)

    def iter_statements(self, container):
        """Statements under a program/statement_list/block, however deeply the grammar nests them."""
        for child in named(container):
            if child.type in ("statement_list", "program"):
                yield from self.iter_statements(child)
            elif child.type != "comment":
                yield child

    def function(self, node) -> nast.Function:
        where = line(node)
        name = text(next(c for c in named(node) if c.type == "function_name"))
        if not name.replace("_", "a").isalnum():
            raise UnsupportedSyntax(where, f"{name!r} is not a valid identifier in the target languages")
        name = self.spelled(self.functions, name, where)
        self.names, self.seen = {}, set()
        block = next(c for c in named(node) if c.type == "script_block")
        param_block = next((c for c in named(block) if c.type == "param_block"), None)
        params, ret_name = [], None
        if param_block is not None:
            for c in named(param_block):
                if c.type in ("attribute_list", "attribute") and "outputtype" in text(c).lower():
                    literal = self.find(c, "type_literal")
                    if literal is None:
                        raise UnsupportedSyntax(line(c), "[OutputType()] needs a type, like [OutputType([int])]")
                    ret_name = self.type_name(literal)
            for p in self.parameters(param_block):
                kind = self.scalar(self.type_name(next(x for x in named(p) if x.type in ("attribute_list", "type_literal"))), line(p))
                var = next(x for x in named(p) if x.type == "variable")
                pname = self.spelled(self.names, text(var).lstrip("$"), line(p))
                self.seen.add(pname)
                params.append(nast.Param(pname, kind))
        body_node = field(block, "script_block_body")
        body = self.block(body_node) if body_node is not None else []
        ret = None
        if returns_a_value(body):
            if ret_name is None:
                raise UnsupportedSyntax(where, "a function that returns a value needs `[OutputType([int])]` (or [double])")
            ret = self.scalar(ret_name, where)
        return nast.Function(name, params, ret, body, where, static=True)

    def parameters(self, param_block):
        found = []
        stack = list(named(param_block))
        while stack:
            n = stack.pop(0)
            if n.type == "script_parameter":
                found.append(n)
            elif n.type == "parameter_list":
                stack = list(named(n)) + stack
        return found

    # -- statements --------------------------------------------------------
    def block(self, container) -> list:
        out = []
        for stmt in self.iter_statements(container):
            out.extend(self.stmt(core(stmt)))
        return out

    def body_of(self, node) -> list:
        return self.block(next(c for c in named(node) if c.type == "statement_block"))

    def stmt(self, n) -> list:
        t, where = n.type, line(n)
        if t == "assignment_expression":
            return self.assignment(n)
        if t in ("post_increment_expression", "post_decrement_expression", "pre_increment_expression", "pre_decrement_expression"):
            var = self.variable(next(c for c in named(n) if c.type == "variable"))
            op = "+" if "increment" in t else "-"
            return [nast.Assign(var, nast.Binary(op, self.variable(next(c for c in named(n) if c.type == "variable")), nast.Const(1, where), where), where)]
        if t == "flow_control_statement":
            if tokens(n)[0] != "return":
                raise UnsupportedSyntax(where, f"unsupported statement {tokens(n)[0]}")
            value = named(n)
            return [nast.Return(self.expr(core(value[0])) if value else None, where)]
        if t == "command":
            return [nast.ExprStmt(self.expr(n), where)]
        if t == "if_statement":
            return [self.if_chain(n)]
        if t == "while_statement":
            return [nast.While(self.expr(core(field(n, "condition"))), self.body_of(n), where)]
        if t == "for_statement":
            return self.for_statement(n)
        raise UnsupportedSyntax(where, f"unsupported statement {t}")

    def variable(self, n):
        name = text(n).lstrip("$")
        if name.lower() in _RESERVED_VARIABLES or ":" in name:
            raise UnsupportedSyntax(line(n), f"unsupported variable ${name}")
        return nast.Name(self.spelled(self.names, name, line(n)), line(n))

    def assignment(self, n) -> list:
        where = line(n)
        target_node = core(named(n)[0])
        op = text(next(c for c in named(n) if c.type == "assignement_operator")).strip()
        value = self.expr(core(field(n, "value")))
        kind = None
        if target_node.type == "cast_expression":  # [int]$s = 0
            kind = self.scalar(self.type_name(target_node), where)
            target_node = core(next(c for c in named(target_node) if c.type != "type_literal"))
        if target_node.type != "variable":
            raise UnsupportedSyntax(where, "unsupported assignment target")
        target = self.variable(target_node)
        if op == "=":
            if target.id not in self.seen:
                self.seen.add(target.id)
                return [nast.Declare(target.id, kind, value, where)]
            if kind is not None:
                raise UnsupportedSyntax(where, f"{target.id!r} is already declared")
            return [nast.Assign(target, value, where)]
        if op in _ASSIGN and kind is None:
            return [nast.Assign(target, nast.Binary(_ASSIGN[op], self.variable(target_node), value, where), where)]
        raise UnsupportedSyntax(where, f"unsupported assignment operator {op}")

    def if_chain(self, n) -> nast.If:
        where = line(n)
        elifs = [c for c in named(n) if c.type == "elseif_clauses"]
        clauses = list(named(elifs[0])) if elifs else []
        else_clause = next((c for c in named(n) if c.type == "else_clause"), None)
        orelse = self.body_of(else_clause) if else_clause is not None else []
        for clause in reversed(clauses):
            orelse = [nast.If(self.expr(core(field(clause, "condition"))), self.body_of(clause), orelse, line(clause))]
        return nast.If(self.expr(core(field(n, "condition"))), self.body_of(n), orelse, where)

    def for_statement(self, n) -> list:
        where = line(n)
        init, cond, step = field(n, "for_initializer"), field(n, "for_condition"), field(n, "for_iterator")
        if init is None or cond is None or step is None:
            raise UnsupportedSyntax(where, "for loops need an initializer, condition and iterator")
        pre = self.stmt(core(init))
        body = self.body_of(n)
        update = self.stmt(core(step))
        if (
            len(pre) == 1 and isinstance(pre[0], nast.Declare) and pre[0].type in (INT, None)
            and cond_is_less_than(core(cond), pre[0].name) and len(update) == 1
            and _is_increment(update[0], pre[0].name)
        ):  # fmt: skip
            c = core(cond)
            counted = _ts.counted_loop(
                pre[0].name, pre[0].value, "<=" if tokens_of_comparison(c) in ("-le", "-ile") else "<",
                self.expr(core(named(c)[-1])), 1, body, where,
            )  # fmt: skip
            if counted is not None:
                return [counted]
        return [nast.Scope(pre + [nast.While(self.expr(core(cond)), body + update, where)], where)]

    # -- expressions ---------------------------------------------------------
    def expr(self, n):
        n = core(n)
        t, where = n.type, line(n)
        if t in ("decimal_integer_literal", "hexadecimal_integer_literal", "integer_literal"):
            return _ts.number(n)
        if t == "real_literal":
            return _ts.number(n)
        if t == "variable":
            return self.variable(n)
        if t == "parenthesized_expression":
            return self.expr(core(named(n)[0]))
        if t in ("additive_expression", "multiplicative_expression"):
            left, right = core(named(n)[0]), core(named(n)[-1])
            op = tokens(n)[0]
            if op == "/":
                return nast.Binary("fdiv", self.expr(left), self.expr(right), where)
            if op not in _ARITH:
                raise UnsupportedSyntax(where, f"unsupported operator {op}")
            return nast.Binary(_ARITH[op], self.expr(left), self.expr(right), where)
        if t == "comparison_expression":
            op = tokens_of_comparison(n).lower()
            if op not in _COMPARE:
                raise UnsupportedSyntax(where, f"unsupported operator {op}")
            return nast.Compare(_COMPARE[op], self.expr(core(named(n)[0])), self.expr(core(named(n)[-1])), where)
        if t == "logical_expression":
            op = tokens(n)[0].lower()
            if op not in ("-and", "-or"):
                raise UnsupportedSyntax(where, f"unsupported operator {op}")
            return nast.Logical(op[1:], self.expr(core(named(n)[0])), self.expr(core(named(n)[-1])), where)
        if t == "expression_with_unary_operator":
            op, operand = tokens(n)[0].lower(), self.expr(core(named(n)[0]))
            if op == "-":
                return _ts.negate(operand)
            if op in ("-not", "!"):
                return nast.Not(operand, where)
            raise UnsupportedSyntax(where, f"unsupported operator {op}")
        if t == "cast_expression":
            target = self.scalar(self.type_name(n), where)
            operand = self.expr(core(next(c for c in named(n) if c.type != "type_literal")))
            if target == INT and isinstance(operand, nast.Cast) is False and _is_float_expression(operand):
                raise UnsupportedSyntax(where, "PowerShell's [int] rounds a double; this is not supported yet")
            return nast.Cast(target, operand, where)
        if t == "command":
            name = text(next(c for c in named(n) if c.type == "command_name"))
            if name.lower() != name and name.lower() in self.functions and self.functions[name.lower()] != name:
                raise UnsupportedSyntax(where, f"{name!r} and {self.functions[name.lower()]!r} are the same function in PowerShell")
            elements = next((c for c in named(n) if c.type == "command_elements"), None)
            args = [] if elements is None else [c for c in named(elements) if c.type != "command_argument_sep"]
            for a in args:
                if text(a).startswith("-") and a.type not in ("decimal_integer_literal", "real_literal"):
                    raise UnsupportedSyntax(line(a), "named command parameters are not supported")
            return nast.Call(self.functions.get(name.lower(), name), [self.expr(core(a)) for a in args], where)
        raise UnsupportedSyntax(where, f"unsupported expression {t}")


def tokens_of_comparison(n) -> str:
    op = next(c for c in named(n) if c.type == "comparison_operator")
    return text(op)


def cond_is_less_than(c, var: str) -> bool:
    if c.type != "comparison_expression":
        return False
    left = core(named(c)[0])
    return left.type == "variable" and text(left).lstrip("$").lower() == var.lower() and tokens_of_comparison(c).lower() in ("-lt", "-le", "-ilt", "-ile")


def _is_increment(stmt, var: str) -> bool:
    return (
        isinstance(stmt, nast.Assign) and isinstance(stmt.target, nast.Name) and stmt.target.id == var
        and isinstance(stmt.value, nast.Binary) and stmt.value.op == "+"
        and isinstance(stmt.value.right, nast.Const) and stmt.value.right.value == 1
    )  # fmt: skip


def _is_float_expression(e) -> bool:
    """Syntactic check: does the expression obviously produce a double?"""
    if isinstance(e, nast.Const):
        return isinstance(e.value, float)
    if isinstance(e, nast.Cast):
        return e.to == FLOAT
    if isinstance(e, nast.Binary):
        return e.op == "fdiv" or _is_float_expression(e.left) or _is_float_expression(e.right)
    return False


def parse(source: str) -> nast.Module:
    return _PowerShell(source).module()


def build_module(source: str, context: ir.Context) -> ir.Module:
    return lower_module(parse(source), context)
