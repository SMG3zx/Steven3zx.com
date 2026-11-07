"""Python-subset frontend: parse with `ast`, build MLIR (func/arith/scf/memref).

Supported: functions with `int` and `Array[int, N]` parameters; assignment,
augmented assignment, array element store; `for i in range(...)`; `if/else`;
a single trailing `return`; + - * and comparisons.

Mutable variables are converted to SSA on the fly: names assigned inside a
loop or branch become `iter_args` or `scf.if` results.
"""

import ast

from polyjuice.mlir_env import ensure_mlir_importable

ensure_mlir_importable()

from mlir import ir  # noqa: E402
from mlir.dialects import arith, func, memref, scf  # noqa: E402


class UnsupportedSyntax(Exception):
    def __init__(self, node: ast.AST, message: str):
        super().__init__(f"line {getattr(node, 'lineno', '?')}: {message}")


_BINOPS = {ast.Add: arith.addi, ast.Sub: arith.subi, ast.Mult: arith.muli}
_CMPS = {
    ast.Lt: arith.CmpIPredicate.slt,
    ast.LtE: arith.CmpIPredicate.sle,
    ast.Gt: arith.CmpIPredicate.sgt,
    ast.GtE: arith.CmpIPredicate.sge,
    ast.Eq: arith.CmpIPredicate.eq,
    ast.NotEq: arith.CmpIPredicate.ne,
}


def _assigned_names(nodes: list[ast.stmt]) -> set[str]:
    names: set[str] = set()
    for node in nodes:
        for sub in ast.walk(node):
            if isinstance(sub, (ast.Assign, ast.AugAssign, ast.AnnAssign)):
                targets = sub.targets if isinstance(sub, ast.Assign) else [sub.target]
                for target in targets:
                    if isinstance(target, ast.Name):
                        names.add(target.id)
    return names


class _FunctionBuilder:
    def __init__(self):
        self.i32 = ir.IntegerType.get_signless(32)
        self.index = ir.IndexType.get()
        self.env: dict[str, ir.Value] = {}

    def type_of(self, annotation: ast.expr) -> ir.Type:
        if isinstance(annotation, ast.Name) and annotation.id == "int":
            return self.i32
        if (
            isinstance(annotation, ast.Subscript)
            and isinstance(annotation.value, ast.Name)
            and annotation.value.id == "Array"
        ):
            elt, size = annotation.slice.elts
            if isinstance(elt, ast.Name) and elt.id == "int" and isinstance(size, ast.Constant):
                return ir.MemRefType.get([size.value], self.i32)
        raise UnsupportedSyntax(annotation, "unsupported type annotation")

    def to_index(self, value: ir.Value) -> ir.Value:
        if value.type == self.index:
            return value
        return arith.index_cast(self.index, value)

    def to_i32(self, value: ir.Value) -> ir.Value:
        if value.type == self.i32:
            return value
        return arith.index_cast(self.i32, value)

    def expr(self, node: ast.expr) -> ir.Value:
        if isinstance(node, ast.Constant) and type(node.value) is int:
            return arith.constant(self.i32, node.value)
        if isinstance(node, ast.Name):
            if node.id not in self.env:
                raise UnsupportedSyntax(node, f"undefined name {node.id!r}")
            return self.env[node.id]
        if isinstance(node, ast.BinOp) and type(node.op) in _BINOPS:
            lhs = self.to_i32(self.expr(node.left))
            rhs = self.to_i32(self.expr(node.right))
            return _BINOPS[type(node.op)](lhs, rhs)
        if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.USub):
            zero = arith.constant(self.i32, 0)
            return arith.subi(zero, self.to_i32(self.expr(node.operand)))
        if isinstance(node, ast.Compare) and len(node.ops) == 1 and type(node.ops[0]) in _CMPS:
            lhs = self.to_i32(self.expr(node.left))
            rhs = self.to_i32(self.expr(node.comparators[0]))
            return arith.cmpi(_CMPS[type(node.ops[0])], lhs, rhs)
        if isinstance(node, ast.Subscript) and isinstance(node.value, ast.Name):
            array = self.env[node.value.id]
            return memref.load(array, [self.to_index(self.expr(node.slice))])
        raise UnsupportedSyntax(node, f"unsupported expression {type(node).__name__}")

    def stmts(self, body: list[ast.stmt]) -> None:
        for stmt in body:
            self.stmt(stmt)

    def stmt(self, node: ast.stmt) -> None:
        if isinstance(node, ast.Assign) and len(node.targets) == 1:
            target = node.targets[0]
            value = self.to_i32(self.expr(node.value))
            if isinstance(target, ast.Name):
                self.env[target.id] = value
                return
            if isinstance(target, ast.Subscript) and isinstance(target.value, ast.Name):
                array = self.env[target.value.id]
                memref.store(value, array, [self.to_index(self.expr(target.slice))])
                return
        elif isinstance(node, ast.AugAssign) and isinstance(node.target, ast.Name):
            if type(node.op) not in _BINOPS:
                raise UnsupportedSyntax(node, "unsupported augmented operator")
            lhs = self.env[node.target.id]
            rhs = self.to_i32(self.expr(node.value))
            self.env[node.target.id] = _BINOPS[type(node.op)](lhs, rhs)
            return
        elif isinstance(node, ast.For):
            return self.for_loop(node)
        elif isinstance(node, ast.If):
            return self.if_stmt(node)
        raise UnsupportedSyntax(node, f"unsupported statement {type(node).__name__}")

    def for_loop(self, node: ast.For) -> None:
        call = node.iter
        if not (
            isinstance(node.target, ast.Name)
            and isinstance(call, ast.Call)
            and isinstance(call.func, ast.Name)
            and call.func.id == "range"
            and 1 <= len(call.args) <= 3
            and not node.orelse
        ):
            raise UnsupportedSyntax(node, "only `for i in range(...)` is supported")
        args = [self.to_i32(self.expr(a)) for a in call.args]
        if len(args) == 3:
            raise UnsupportedSyntax(node, "range() with a step is not supported yet")
        lower = args[0] if len(args) == 2 else arith.constant(self.i32, 0)
        upper = args[-1]
        # Iterate k in [0, max(upper - lower, 0)) so the unsigned size_t loop in
        # the C output never sees a negative bound, then bind i = lower + k.
        diff = arith.subi(upper, lower)
        zero_i32 = arith.constant(self.i32, 0)
        count = arith.select(arith.cmpi(arith.CmpIPredicate.sgt, diff, zero_i32), diff, zero_i32)
        zero, one = arith.constant(self.index, 0), arith.constant(self.index, 1)
        upper_index = self.to_index(count)
        carried = sorted((_assigned_names(node.body) & self.env.keys()) - {node.target.id})
        carried = [n for n in carried if self.env[n].type == self.i32]
        loop = scf.ForOp(zero, upper_index, one, [self.env[n] for n in carried])
        outer = dict(self.env)
        with ir.InsertionPoint(loop.body):
            self.env[node.target.id] = arith.addi(lower, self.to_i32(loop.induction_variable))
            for name, arg in zip(carried, loop.inner_iter_args):
                self.env[name] = arg
            self.stmts(node.body)
            scf.YieldOp([self.env[n] for n in carried])
        self.env = outer
        for name, result in zip(carried, loop.results):
            self.env[name] = result

    def if_stmt(self, node: ast.If) -> None:
        cond = self.expr(node.test)
        carried = sorted(
            (_assigned_names(node.body + node.orelse) & self.env.keys())
        )
        carried = [n for n in carried if self.env[n].type == self.i32]
        op = scf.IfOp(cond, [self.i32] * len(carried), has_else=True)
        outer = dict(self.env)
        for block, body in ((op.then_block, node.body), (op.else_block, node.orelse)):
            self.env = dict(outer)
            with ir.InsertionPoint(block):
                self.stmts(body)
                scf.YieldOp([self.env[n] for n in carried])
        self.env = outer
        for name, result in zip(carried, op.results):
            self.env[name] = result

    def function(self, node: ast.FunctionDef) -> None:
        params = [self.type_of(a.annotation) for a in node.args.args]
        results = [] if node.returns is None else [self.type_of(node.returns)]
        fn = func.FuncOp(node.name, (params, results))
        entry = fn.add_entry_block()
        with ir.InsertionPoint(entry):
            for arg, value in zip(node.args.args, entry.arguments):
                self.env[arg.arg] = value
            body = node.body
            last = body[-1] if body else None
            if results and not isinstance(last, ast.Return):
                raise UnsupportedSyntax(node, "function must end with `return`")
            self.stmts(body[:-1] if isinstance(last, ast.Return) else body)
            if isinstance(last, ast.Return) and last.value is not None:
                func.ReturnOp([self.to_i32(self.expr(last.value))])
            else:
                func.ReturnOp([])


def build_module(source: str, context: ir.Context) -> ir.Module:
    tree = ast.parse(source)
    with context, ir.Location.unknown():
        module = ir.Module.create()
        with ir.InsertionPoint(module.body):
            for node in tree.body:
                if not isinstance(node, ast.FunctionDef):
                    raise UnsupportedSyntax(node, "only function definitions are supported")
                _FunctionBuilder().function(node)
        module.operation.verify()
        return module
