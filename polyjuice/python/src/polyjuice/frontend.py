"""Python-subset frontend: parse with `ast`, build MLIR (func/arith/scf/memref).

Supported: functions with `int` and `Array[int, N]` parameters; assignment,
augmented assignment, array element store; `for i in range(a[, b])`; `if/else`;
a single trailing `return`; + - * unary - and comparisons.

Mutable variables are converted to SSA on the fly: names assigned inside a
loop or branch become `iter_args` or `scf.if` results. Python names are kept
as `polyjuice.*` discardable attributes so emitters can write readable code:

    func.func   polyjuice.arg_names = ["a", "n"]
    scf.for     polyjuice.iv = "i", polyjuice.carried = ["s"]
    scf.if      polyjuice.carried = ["r"]
    any op      polyjuice.name = "y"      (value bound to a Python name)
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
                    names.update(n.id for n in ast.walk(target) if isinstance(n, ast.Name))
    return names


def _check_no_lost_updates(node, outer, inner, carried) -> None:
    """A name from the enclosing scope that changed but is not carried would be silently dropped."""
    for name, value in outer.items():
        if name not in carried and inner.get(name) != value:
            raise UnsupportedSyntax(node, f"cannot carry {name!r} out of this block")


def _names_attr(names: list[str]) -> ir.ArrayAttr:
    return ir.ArrayAttr.get([ir.StringAttr.get(n) for n in names])


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
        return arith.index_cast(self.index, value)

    def bind(self, name: str, value: ir.Value) -> None:
        """Bind a Python name to an SSA value, remembering the name on the defining op."""
        self.env[name] = value
        if isinstance(value, ir.OpResult):
            op = value.owner
            if "polyjuice.name" not in op.attributes:
                op.attributes["polyjuice.name"] = ir.StringAttr.get(name)

    def expr(self, node: ast.expr) -> ir.Value:
        if isinstance(node, ast.Constant) and type(node.value) is int:
            return arith.constant(self.i32, node.value)
        if isinstance(node, ast.Name):
            if node.id not in self.env:
                raise UnsupportedSyntax(node, f"undefined name {node.id!r}")
            return self.env[node.id]
        if isinstance(node, ast.BinOp) and type(node.op) in _BINOPS:
            return _BINOPS[type(node.op)](self.expr(node.left), self.expr(node.right))
        if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.USub):
            return arith.subi(arith.constant(self.i32, 0), self.expr(node.operand))
        if isinstance(node, ast.Compare) and len(node.ops) == 1 and type(node.ops[0]) in _CMPS:
            return arith.cmpi(
                _CMPS[type(node.ops[0])], self.expr(node.left), self.expr(node.comparators[0])
            )
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
            if isinstance(target, ast.Tuple) and isinstance(node.value, ast.Tuple):
                if len(target.elts) != len(node.value.elts) or not all(
                    isinstance(t, ast.Name) for t in target.elts
                ):
                    raise UnsupportedSyntax(node, "only `a, b = x, y` tuple assignment")
                values = [self.expr(v) for v in node.value.elts]  # all reads before any bind
                for t, v in zip(target.elts, values):
                    self.bind(t.id, v)
                return
            value = self.expr(node.value)
            if isinstance(target, ast.Name):
                self.bind(target.id, value)
                return
            if isinstance(target, ast.Subscript) and isinstance(target.value, ast.Name):
                array = self.env[target.value.id]
                memref.store(value, array, [self.to_index(self.expr(target.slice))])
                return
        elif isinstance(node, ast.AugAssign) and isinstance(node.target, ast.Name):
            if type(node.op) not in _BINOPS:
                raise UnsupportedSyntax(node, "unsupported augmented operator")
            self.bind(
                node.target.id,
                _BINOPS[type(node.op)](self.env[node.target.id], self.expr(node.value)),
            )
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
            and 1 <= len(call.args) <= 2
            and not node.orelse
        ):
            raise UnsupportedSyntax(node, "only `for i in range(stop)` / `range(start, stop)`")
        args = [self.expr(a) for a in call.args]
        lower = args[0] if len(args) == 2 else arith.constant(self.i32, 0)
        upper, step = args[-1], arith.constant(self.i32, 1)
        carried = sorted((_assigned_names(node.body) & self.env.keys()) - {node.target.id})
        carried = [n for n in carried if self.env[n].type == self.i32]
        loop = scf.ForOp(lower, upper, step, [self.env[n] for n in carried])
        loop.operation.attributes["polyjuice.iv"] = ir.StringAttr.get(node.target.id)
        loop.operation.attributes["polyjuice.carried"] = _names_attr(carried)
        outer = dict(self.env)
        with ir.InsertionPoint(loop.body):
            self.env[node.target.id] = loop.induction_variable
            for name, arg in zip(carried, loop.inner_iter_args):
                self.env[name] = arg
            self.stmts(node.body)
            _check_no_lost_updates(node, outer, self.env, set(carried) | {node.target.id})
            scf.YieldOp([self.env[n] for n in carried])
        self.env = outer
        for name, result in zip(carried, loop.results):
            self.env[name] = result

    def if_stmt(self, node: ast.If) -> None:
        cond = self.expr(node.test)
        carried = sorted(_assigned_names(node.body + node.orelse) & self.env.keys())
        carried = [n for n in carried if self.env[n].type == self.i32]
        op = scf.IfOp(cond, [self.i32] * len(carried), has_else=True)
        op.operation.attributes["polyjuice.carried"] = _names_attr(carried)
        outer = dict(self.env)
        for block, body in ((op.then_block, node.body), (op.else_block, node.orelse)):
            self.env = dict(outer)
            with ir.InsertionPoint(block):
                self.stmts(body)
                _check_no_lost_updates(node, outer, self.env, set(carried))
                scf.YieldOp([self.env[n] for n in carried])
        self.env = outer
        for name, result in zip(carried, op.results):
            self.env[name] = result

    def function(self, node: ast.FunctionDef) -> None:
        params = [self.type_of(a.annotation) for a in node.args.args]
        results = [] if node.returns is None else [self.type_of(node.returns)]
        fn = func.FuncOp(node.name, (params, results))
        fn.operation.attributes["polyjuice.arg_names"] = _names_attr(
            [a.arg for a in node.args.args]
        )
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
                func.ReturnOp([self.expr(last.value)])
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
