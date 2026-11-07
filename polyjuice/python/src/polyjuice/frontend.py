"""Python-subset frontend: parse with `ast`, build MLIR (func/arith/scf/memref).

Supported: functions with `int`, `float` and `Array[int|float, N]` parameters;
assignment, tuple assignment, augmented assignment, array element store;
`for i in range(a[, b])`, `while`, `if/elif/else`; calls to other functions in the
same file (including recursion); a single trailing `return`; + - * (and / for
floats), unary -, comparisons, and explicit `float(x)` / `int(x)` casts.
`int` is 32-bit and wraps; `float` is a 64-bit IEEE double. Mixing the two
without a cast is an error.

Mutable variables are converted to SSA on the fly: names assigned inside a
loop or branch become `iter_args` or `scf.if` results. Python names are kept
as `polyjuice.*` discardable attributes so emitters can write readable code:

    func.func   polyjuice.arg_names = ["a", "n"]
    scf.for     polyjuice.iv = "i", polyjuice.carried = ["s"]
    scf.if      polyjuice.carried = ["r"]
    any op      polyjuice.name = "y"      (value bound to a Python name)
"""

import ast
import math

from polyjuice.mlir_env import ensure_mlir_importable

ensure_mlir_importable()

from mlir import ir  # noqa: E402
from mlir.dialects import arith, func, memref, scf  # noqa: E402


class UnsupportedSyntax(Exception):
    def __init__(self, node: ast.AST, message: str):
        super().__init__(f"line {getattr(node, 'lineno', '?')}: {message}")


_INT_OPS = {ast.Add: arith.addi, ast.Sub: arith.subi, ast.Mult: arith.muli}
_FLOAT_OPS = {
    ast.Add: arith.addf, ast.Sub: arith.subf, ast.Mult: arith.mulf, ast.Div: arith.divf,
}  # fmt: skip
_FLOAT_CMPS = {
    ast.Lt: arith.CmpFPredicate.OLT,
    ast.LtE: arith.CmpFPredicate.OLE,
    ast.Gt: arith.CmpFPredicate.OGT,
    ast.GtE: arith.CmpFPredicate.OGE,
    ast.Eq: arith.CmpFPredicate.OEQ,
    ast.NotEq: arith.CmpFPredicate.UNE,  # Python: nan != nan is True
}
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


def _is_array(value: ir.Value) -> bool:
    return str(value.type).startswith("memref<")


class _FunctionBuilder:
    def __init__(self, signatures: dict[str, tuple[list[ir.Type], list[ir.Type]]]):
        self.i32 = ir.IntegerType.get_signless(32)
        self.f64 = ir.F64Type.get()
        self.i1 = ir.IntegerType.get_signless(1)
        self.index = ir.IndexType.get()
        self.signatures = signatures
        self.env: dict[str, ir.Value] = {}

    # -- types ----------------------------------------------------------
    def scalar_type(self, name: str) -> ir.Type | None:
        return {"int": self.i32, "float": self.f64}.get(name)

    def type_of(self, annotation: ast.expr) -> ir.Type:
        if isinstance(annotation, ast.Name) and self.scalar_type(annotation.id):
            return self.scalar_type(annotation.id)
        if (
            isinstance(annotation, ast.Subscript)
            and isinstance(annotation.value, ast.Name)
            and annotation.value.id == "Array"
        ):
            elt, size = annotation.slice.elts
            if isinstance(elt, ast.Name) and self.scalar_type(elt.id) and isinstance(size, ast.Constant):
                return ir.MemRefType.get([size.value], self.scalar_type(elt.id))
        raise UnsupportedSyntax(annotation, "unsupported type annotation")

    def to_index(self, node, value: ir.Value) -> ir.Value:
        if value.type != self.i32:
            raise UnsupportedSyntax(node, "array index must be an int")
        return arith.index_cast(self.index, value)

    def same_type(self, node, a: ir.Value, b: ir.Value) -> None:
        if a.type != b.type:
            raise UnsupportedSyntax(
                node, f"mixed types {a.type} and {b.type}; convert with float(x) or int(x)"
            )

    def carried_names(self, node, names: set[str], outer: dict) -> list[str]:
        return sorted(n for n in names & outer.keys() if not _is_array(outer[n]))

    def check_yield_types(self, node, names: list[str], outer: dict) -> None:
        for name in names:
            if self.env[name].type != outer[name].type:
                raise UnsupportedSyntax(node, f"{name!r} changes type inside this block")

    def bind(self, name: str, value: ir.Value) -> None:
        """Bind a Python name to an SSA value, remembering the name on the defining op."""
        self.env[name] = value
        if isinstance(value, ir.OpResult):
            op = value.owner
            if "polyjuice.name" not in op.attributes:
                op.attributes["polyjuice.name"] = ir.StringAttr.get(name)

    # -- expressions ----------------------------------------------------
    def condition(self, node: ast.expr) -> ir.Value:
        value = self.expr(node)
        if value.type != self.i1:
            raise UnsupportedSyntax(node, "a condition must be a comparison")
        return value

    def arithmetic(self, node, op, lhs: ir.Value, rhs: ir.Value) -> ir.Value:
        self.same_type(node, lhs, rhs)
        table = _INT_OPS if lhs.type == self.i32 else _FLOAT_OPS if lhs.type == self.f64 else {}
        if type(op) not in table:
            raise UnsupportedSyntax(
                node, f"unsupported operator {type(op).__name__} on {lhs.type}"
            )
        return table[type(op)](lhs, rhs)

    def expr(self, node: ast.expr) -> ir.Value:
        if isinstance(node, ast.Constant):
            if type(node.value) is int:
                return arith.constant(self.i32, node.value)
            if type(node.value) is float and math.isfinite(node.value):
                return arith.constant(self.f64, node.value)
        if isinstance(node, ast.Name):
            if node.id not in self.env:
                raise UnsupportedSyntax(node, f"undefined name {node.id!r}")
            return self.env[node.id]
        if isinstance(node, ast.BinOp):
            return self.arithmetic(node, node.op, self.expr(node.left), self.expr(node.right))
        if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.USub):
            operand = self.expr(node.operand)
            if operand.type == self.f64:
                return arith.negf(operand)
            return arith.subi(arith.constant(self.i32, 0), operand)
        if isinstance(node, ast.Compare) and len(node.ops) == 1 and type(node.ops[0]) in _CMPS:
            lhs, rhs = self.expr(node.left), self.expr(node.comparators[0])
            self.same_type(node, lhs, rhs)
            if lhs.type == self.f64:
                return arith.cmpf(_FLOAT_CMPS[type(node.ops[0])], lhs, rhs)
            return arith.cmpi(_CMPS[type(node.ops[0])], lhs, rhs)
        if isinstance(node, ast.Subscript) and isinstance(node.value, ast.Name):
            array = self.env[node.value.id]
            return memref.load(array, [self.to_index(node, self.expr(node.slice))])
        if isinstance(node, ast.Call) and isinstance(node.func, ast.Name):
            return self.call(node)
        raise UnsupportedSyntax(node, f"unsupported expression {type(node).__name__}")

    def call(self, node: ast.Call) -> ir.Value | None:
        name = node.func.id
        if name in ("float", "int") and len(node.args) == 1 and not node.keywords:
            arg = self.expr(node.args[0])
            if name == "float" and arg.type == self.i32:
                return arith.sitofp(self.f64, arg)
            if name == "int" and arg.type == self.f64:
                return arith.fptosi(self.i32, arg)
            if arg.type == (self.f64 if name == "float" else self.i32):
                return arg
            raise UnsupportedSyntax(node, f"cannot convert {arg.type} with {name}()")
        if name not in self.signatures:
            raise UnsupportedSyntax(node, f"call to unknown function {name!r}")
        params, results = self.signatures[name]
        args = [self.expr(a) for a in node.args]
        if node.keywords or len(args) != len(params):
            raise UnsupportedSyntax(node, f"{name}() takes {len(params)} positional arguments")
        for arg, param in zip(args, params):
            if arg.type != param:
                raise UnsupportedSyntax(node, f"{name}() expects {param}, got {arg.type}")
        op = func.CallOp(results, ir.FlatSymbolRefAttr.get(name), args)
        return op.results[0] if results else None

    # -- statements -----------------------------------------------------
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
            if value is None:
                raise UnsupportedSyntax(node, "function returns no value")
            if isinstance(target, ast.Name):
                self.bind(target.id, value)
                return
            if isinstance(target, ast.Subscript) and isinstance(target.value, ast.Name):
                array = self.env[target.value.id]
                if ir.MemRefType(array.type).element_type != value.type:
                    raise UnsupportedSyntax(node, "stored value does not match the array type")
                memref.store(value, array, [self.to_index(node, self.expr(target.slice))])
                return
        elif isinstance(node, ast.AugAssign) and isinstance(node.target, ast.Name):
            self.bind(
                node.target.id,
                self.arithmetic(node, node.op, self.env[node.target.id], self.expr(node.value)),
            )
            return
        elif isinstance(node, ast.Expr) and isinstance(node.value, ast.Call):
            self.expr(node.value)
            return
        elif isinstance(node, ast.For):
            return self.for_loop(node)
        elif isinstance(node, ast.While):
            return self.while_loop(node)
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
        if any(a.type != self.i32 for a in args):
            raise UnsupportedSyntax(node, "range() bounds must be ints")
        lower = args[0] if len(args) == 2 else arith.constant(self.i32, 0)
        upper, step = args[-1], arith.constant(self.i32, 1)
        outer = dict(self.env)
        carried = self.carried_names(node, _assigned_names(node.body) - {node.target.id}, outer)
        loop = scf.ForOp(lower, upper, step, [self.env[n] for n in carried])
        loop.operation.attributes["polyjuice.iv"] = ir.StringAttr.get(node.target.id)
        loop.operation.attributes["polyjuice.carried"] = _names_attr(carried)
        with ir.InsertionPoint(loop.body):
            self.env[node.target.id] = loop.induction_variable
            for name, arg in zip(carried, loop.inner_iter_args):
                self.env[name] = arg
            self.stmts(node.body)
            _check_no_lost_updates(node, outer, self.env, set(carried) | {node.target.id})
            self.check_yield_types(node, carried, outer)
            scf.YieldOp([self.env[n] for n in carried])
        self.env = outer
        for name, result in zip(carried, loop.results):
            self.env[name] = result

    def while_loop(self, node: ast.While) -> None:
        if node.orelse:
            raise UnsupportedSyntax(node, "while/else is not supported")
        outer = dict(self.env)
        carried = self.carried_names(node, _assigned_names(node.body), outer)
        inits = [outer[n] for n in carried]
        types = [v.type for v in inits]
        loop = scf.WhileOp(types, inits)
        loop.operation.attributes["polyjuice.carried"] = _names_attr(carried)
        before = loop.before.blocks.append(*types)
        after = loop.after.blocks.append(*types)
        with ir.InsertionPoint(before):
            for name, arg in zip(carried, before.arguments):
                self.env[name] = arg
            scf.ConditionOp(self.condition(node.test), list(before.arguments))
        self.env = dict(outer)
        with ir.InsertionPoint(after):
            for name, arg in zip(carried, after.arguments):
                self.env[name] = arg
            self.stmts(node.body)
            _check_no_lost_updates(node, outer, self.env, set(carried))
            self.check_yield_types(node, carried, outer)
            scf.YieldOp([self.env[n] for n in carried])
        self.env = outer
        for name, result in zip(carried, loop.results):
            self.env[name] = result

    def if_stmt(self, node: ast.If) -> None:
        cond = self.condition(node.test)
        outer = dict(self.env)
        carried = self.carried_names(node, _assigned_names(node.body + node.orelse), outer)
        op = scf.IfOp(cond, [outer[n].type for n in carried], has_else=True)
        op.operation.attributes["polyjuice.carried"] = _names_attr(carried)
        for block, body in ((op.then_block, node.body), (op.else_block, node.orelse)):
            self.env = dict(outer)
            with ir.InsertionPoint(block):
                self.stmts(body)
                _check_no_lost_updates(node, outer, self.env, set(carried))
                self.check_yield_types(node, carried, outer)
                scf.YieldOp([self.env[n] for n in carried])
        self.env = outer
        for name, result in zip(carried, op.results):
            self.env[name] = result

    def function(self, node: ast.FunctionDef) -> None:
        params, results = self.signatures[node.name]
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
                value = self.expr(last.value)
                if not results or value is None or value.type != results[0]:
                    raise UnsupportedSyntax(last, "returned value does not match the declared type")
                func.ReturnOp([value])
            else:
                func.ReturnOp([])


def build_module(source: str, context: ir.Context) -> ir.Module:
    tree = ast.parse(source)
    with context, ir.Location.unknown():
        module = ir.Module.create()
        probe = _FunctionBuilder({})
        signatures = {}
        for node in tree.body:
            if not isinstance(node, ast.FunctionDef):
                raise UnsupportedSyntax(node, "only function definitions are supported")
            signatures[node.name] = (
                [probe.type_of(a.annotation) for a in node.args.args],
                [] if node.returns is None else [probe.type_of(node.returns)],
            )
        with ir.InsertionPoint(module.body):
            for node in tree.body:
                _FunctionBuilder(signatures).function(node)
        module.operation.verify()
        return module
