"""Python-subset frontend: `ast` -> neutral AST.

Supported: functions with `int`, `float` and `Array[int|float, N]` parameters;
assignment, tuple assignment, augmented assignment, array element store;
`for i in range(a[, b])`, `while`, `if/elif/else`, early `return`; calls to other
functions in the same file; + - * (and / for floats), unary -, comparisons, and
explicit `float(x)` / `int(x)` casts. `int` is 32-bit and wraps.
"""

import ast

from polyjuice import nast
from polyjuice.lower import lower_module
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.nast import FLOAT, INT, ArrayType, UnsupportedSyntax

ensure_mlir_importable()

from mlir import ir  # noqa: E402

_OPS = {ast.Add: "+", ast.Sub: "-", ast.Mult: "*", ast.Div: "/"}
_CMPS = {ast.Lt: "<", ast.LtE: "<=", ast.Gt: ">", ast.GtE: ">=", ast.Eq: "==", ast.NotEq: "!="}
_SCALARS = {"int": INT, "float": FLOAT}


def _type(annotation: ast.expr):
    if isinstance(annotation, ast.Name) and annotation.id in _SCALARS:
        return _SCALARS[annotation.id]
    if (
        isinstance(annotation, ast.Subscript)
        and isinstance(annotation.value, ast.Name)
        and annotation.value.id == "Array"
        and isinstance(annotation.slice, ast.Tuple)
        and len(annotation.slice.elts) == 2
    ):
        elt, size = annotation.slice.elts
        if isinstance(elt, ast.Name) and elt.id in _SCALARS and isinstance(size, ast.Constant):
            return ArrayType(_SCALARS[elt.id], size.value)
    raise UnsupportedSyntax(annotation.lineno, "unsupported type annotation")


def _expr(node: ast.expr):
    line = getattr(node, "lineno", 0)
    if isinstance(node, ast.Constant):
        return nast.Const(node.value, line)
    if isinstance(node, ast.Name):
        return nast.Name(node.id, line)
    if isinstance(node, ast.BinOp):
        if type(node.op) not in _OPS:
            raise UnsupportedSyntax(line, f"unsupported operator {type(node.op).__name__}")
        return nast.Binary(_OPS[type(node.op)], _expr(node.left), _expr(node.right), line)
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.USub):
        return nast.Unary("-", _expr(node.operand), line)
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.Not):
        return nast.Not(_expr(node.operand), line)
    if isinstance(node, ast.BoolOp):
        op = "and" if isinstance(node.op, ast.And) else "or"
        result = _expr(node.values[0])
        for value in node.values[1:]:
            result = nast.Logical(op, result, _expr(value), line)
        return result
    if isinstance(node, ast.Compare) and len(node.ops) == 1 and type(node.ops[0]) in _CMPS:
        return nast.Compare(_CMPS[type(node.ops[0])], _expr(node.left), _expr(node.comparators[0]), line)
    if isinstance(node, ast.Subscript) and isinstance(node.value, ast.Name):
        return nast.Index(node.value.id, _expr(node.slice), line)
    if isinstance(node, ast.Call) and isinstance(node.func, ast.Name):
        if node.keywords:
            raise UnsupportedSyntax(line, "keyword arguments are not supported")
        args = [_expr(a) for a in node.args]
        if node.func.id in ("float", "int") and len(args) == 1:
            return nast.Cast(_SCALARS[node.func.id], args[0], line)
        return nast.Call(node.func.id, args, line)
    raise UnsupportedSyntax(line, f"unsupported expression {type(node).__name__}")


def _target(node: ast.expr):
    if isinstance(node, ast.Name):
        return nast.Name(node.id, node.lineno)
    if isinstance(node, ast.Subscript) and isinstance(node.value, ast.Name):
        return nast.Index(node.value.id, _expr(node.slice), node.lineno)
    raise UnsupportedSyntax(node.lineno, "unsupported assignment target")


def _block(body: list[ast.stmt]) -> list:
    return [_stmt(s) for s in body]


def _stmt(node: ast.stmt):
    line = node.lineno
    if isinstance(node, ast.Assign) and len(node.targets) == 1:
        target = node.targets[0]
        if isinstance(target, ast.Tuple) and isinstance(node.value, ast.Tuple):
            if len(target.elts) != len(node.value.elts) or not all(
                isinstance(t, ast.Name) for t in target.elts
            ):
                raise UnsupportedSyntax(line, "only `a, b = x, y` tuple assignment")
            return nast.ParallelAssign(
                [_target(t) for t in target.elts], [_expr(v) for v in node.value.elts], line
            )
        return nast.Assign(_target(target), _expr(node.value), line)
    if isinstance(node, ast.AugAssign):
        if type(node.op) not in _OPS:
            raise UnsupportedSyntax(line, f"unsupported operator {type(node.op).__name__}")
        target = _target(node.target)
        return nast.Assign(target, nast.Binary(_OPS[type(node.op)], _expr(node.target), _expr(node.value), line), line)
    if isinstance(node, ast.Expr) and isinstance(node.value, ast.Call):
        call = _expr(node.value)
        if not isinstance(call, nast.Call):
            raise UnsupportedSyntax(line, "expression statement without effect")
        return nast.ExprStmt(call, line)
    if isinstance(node, ast.For):
        call = node.iter
        if not (
            isinstance(node.target, ast.Name)
            and isinstance(call, ast.Call)
            and isinstance(call.func, ast.Name)
            and call.func.id == "range"
            and 1 <= len(call.args) <= 2
            and not node.orelse
        ):
            raise UnsupportedSyntax(line, "only `for i in range(stop)` / `range(start, stop)`")
        args = [_expr(a) for a in call.args]
        start, stop = (None, args[0]) if len(args) == 1 else (args[0], args[1])
        return nast.For(node.target.id, start, stop, _block(node.body), line)
    if isinstance(node, ast.While):
        if node.orelse:
            raise UnsupportedSyntax(line, "while/else is not supported")
        return nast.While(_expr(node.test), _block(node.body), line)
    if isinstance(node, ast.If):
        return nast.If(_expr(node.test), _block(node.body), _block(node.orelse), line)
    if isinstance(node, ast.Return):
        return nast.Return(_expr(node.value) if node.value is not None else None, line)
    raise UnsupportedSyntax(line, f"unsupported statement {type(node).__name__}")


def parse(source: str) -> nast.Module:
    tree = ast.parse(source)
    functions = []
    for node in tree.body:
        if not isinstance(node, ast.FunctionDef):
            raise UnsupportedSyntax(node.lineno, "only function definitions are supported")
        params = [nast.Param(a.arg, _type(a.annotation)) for a in node.args.args]
        ret = _type(node.returns) if node.returns is not None else None
        functions.append(nast.Function(node.name, params, ret, _block(node.body), node.lineno))
    return nast.Module(functions)


def build_module(source: str, context: ir.Context) -> ir.Module:
    return lower_module(parse(source), context)
