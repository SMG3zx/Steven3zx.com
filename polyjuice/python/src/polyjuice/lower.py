"""Lower the neutral AST (nast.py) to the shared layer: func/arith/scf/memref.

Mutable variables are converted to SSA on the fly: names assigned inside a loop
or branch become `iter_args`, `scf.while` operands or `scf.if` results. Source
names are kept as `polyjuice.*` discardable attributes so emitters can write
readable code:

    func.func   polyjuice.arg_names = ["a", "n"]
    scf.for     polyjuice.iv = "i", polyjuice.carried = ["s"]
    scf.while   polyjuice.carried = ["a", "b"]
    scf.if      polyjuice.carried = ["r"]   |   polyjuice.returns (its results are the return)
    any op      polyjuice.name = "y"        (value bound to a source name)

Early returns are rewritten so every path ends in a return, then lowered as an
`scf.if` whose results are the function's results.
"""

import math

from polyjuice import nast
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.nast import FLOAT, INT, ArrayType, UnsupportedSyntax

ensure_mlir_importable()

from mlir import ir  # noqa: E402
from mlir.dialects import arith, func, memref, scf  # noqa: E402

_INT_OPS = {"+": arith.addi, "-": arith.subi, "*": arith.muli}
_FLOAT_OPS = {"+": arith.addf, "-": arith.subf, "*": arith.mulf, "/": arith.divf}
_INT_CMPS = {
    "<": arith.CmpIPredicate.slt, "<=": arith.CmpIPredicate.sle, ">": arith.CmpIPredicate.sgt,
    ">=": arith.CmpIPredicate.sge, "==": arith.CmpIPredicate.eq, "!=": arith.CmpIPredicate.ne,
}  # fmt: skip
_FLOAT_CMPS = {
    "<": arith.CmpFPredicate.OLT, "<=": arith.CmpFPredicate.OLE, ">": arith.CmpFPredicate.OGT,
    ">=": arith.CmpFPredicate.OGE, "==": arith.CmpFPredicate.OEQ,
    "!=": arith.CmpFPredicate.UNE,  # nan != nan is true
}  # fmt: skip


def _names_attr(names: list[str]) -> ir.ArrayAttr:
    return ir.ArrayAttr.get([ir.StringAttr.get(n) for n in names])


def _is_array(value: ir.Value) -> bool:
    return str(value.type).startswith("memref<")


def _contains_return(stmts) -> bool:
    for st in stmts:
        if isinstance(st, nast.Return):
            return True
        if isinstance(st, nast.If) and (_contains_return(st.then) or _contains_return(st.orelse)):
            return True
        if isinstance(st, nast.Scope) and _contains_return(st.body):
            return True
        if isinstance(st, (nast.For, nast.While)) and _contains_return(st.body):
            return True
    return False


def _ends_with_return(stmts) -> bool:
    if not stmts:
        return False
    last = stmts[-1]
    if isinstance(last, nast.Return):
        return True
    return isinstance(last, nast.If) and _ends_with_return(last.then) and _ends_with_return(last.orelse)


class _Returns:
    """State for rewriting returns: the function's return type and a counter for hidden names."""

    def __init__(self, ret):
        self.ret = ret
        self.count = 0

    def next(self) -> int:
        self.count += 1
        return self.count


def _replace_returns(stmts: list, done: str, ret: str | None) -> list:
    """Inside a loop body: `return v` becomes `ret = v; done = 1` (the loop condition stops it)."""
    out = []
    for st in stmts:
        if isinstance(st, nast.Return):
            flag = nast.Assign(nast.Name(done, st.line), nast.Const(1, st.line), st.line)
            if st.value is None or ret is None:
                out.append(flag)
            else:
                out.append(nast.Assign(nast.Name(ret, st.line), st.value, st.line))
                out.append(flag)
        elif isinstance(st, nast.If):
            out.append(nast.If(st.cond, _replace_returns(st.then, done, ret), _replace_returns(st.orelse, done, ret), st.line))
        elif isinstance(st, nast.Scope):
            out.append(nast.Scope(_replace_returns(st.body, done, ret), st.line))
        else:
            out.append(st)
    return out


def _loop_with_exits(st, rest: list, ctx: _Returns) -> list:
    """Rewrite a loop containing returns as: loop guarded by a `done` flag, then `if done: return ret`."""
    k = ctx.next()
    done, ret = f"__done{k}", (f"__ret{k}" if ctx.ret else None)
    line = st.line
    zero = nast.Const(0.0 if ctx.ret == FLOAT else 0, line)
    body = list(st.body)
    if isinstance(st, nast.For):
        counter = f"__i{k}"
        body = [nast.Declare(st.var, INT, nast.Name(counter, line), line)] + body
        body.append(nast.Assign(nast.Name(counter, line), nast.Binary("+", nast.Name(counter, line), nast.Const(1, line), line), line))
        start = st.start if st.start is not None else nast.Const(0, line)
        init = [nast.Declare(counter, INT, start, line)]
        cond = nast.Compare("<", nast.Name(counter, line), st.stop, line)
    else:
        init, cond = [], st.cond
    body = _replace_returns(normalize_returns(body, ctx), done, ret)
    init.append(nast.Declare(done, INT, nast.Const(0, line), line))
    if ret:
        init.append(nast.Declare(ret, ctx.ret, zero, line))
    guarded = nast.Logical("and", nast.Compare("==", nast.Name(done, line), nast.Const(0, line), line), cond, line)
    after = nast.If(
        nast.Compare("!=", nast.Name(done, line), nast.Const(0, line), line),
        [nast.Return(nast.Name(ret, line) if ret else None, line)],
        rest,
        line,
    )
    return init + [nast.While(guarded, body, line)] + normalize_returns([after], ctx)


def normalize_returns(stmts: list, ctx: _Returns) -> list:
    """Rewrite so a return appears only at the tail of every path.

    `if c: return a` followed by `rest` becomes `if c: return a else: rest`. A loop with a
    return inside is rewritten with a done flag (see `_loop_with_exits`).
    """
    out = []
    for i, st in enumerate(stmts):
        if isinstance(st, nast.Return):
            out.append(st)
            return out  # anything after is dead
        if isinstance(st, (nast.For, nast.While)) and _contains_return(st.body):
            out.extend(_loop_with_exits(st, normalize_returns(stmts[i + 1 :], ctx), ctx))
            return out
        if isinstance(st, nast.Scope) and _contains_return(st.body):
            raise UnsupportedSyntax(st, "return inside a bare nested block is not supported yet")
        if isinstance(st, nast.If):
            then, orelse = normalize_returns(st.then, ctx), normalize_returns(st.orelse, ctx)
            rest = stmts[i + 1 :]
            then_returns, else_returns = _ends_with_return(then), _ends_with_return(orelse)
            if then_returns or else_returns:
                if not then_returns:
                    then = normalize_returns(then + rest, ctx)
                if not else_returns:
                    orelse = normalize_returns(orelse + rest, ctx)
                out.append(nast.If(st.cond, then, orelse, st.line))
                return out
            out.append(nast.If(st.cond, then, orelse, st.line))
            continue
        out.append(st)
    return out


class _Lowerer:
    def __init__(self, signatures):
        self.i32 = ir.IntegerType.get_signless(32)
        self.f64 = ir.F64Type.get()
        self.i1 = ir.IntegerType.get_signless(1)
        self.index = ir.IndexType.get()
        self.signatures = signatures
        self.env: dict[str, ir.Value] = {}
        self.static = False  # statically typed source: assignments coerce to the declared type
        self.declared: dict[str, ir.Type] = {}  # declared but not yet assigned names

    # -- types ----------------------------------------------------------
    def mlir_type(self, t) -> ir.Type:
        if t == INT:
            return self.i32
        if t == FLOAT:
            return self.f64
        if isinstance(t, ArrayType):
            return ir.MemRefType.get([t.size], self.mlir_type(t.elem))
        raise UnsupportedSyntax(0, f"unsupported type {t!r}")

    def to_index(self, node, value: ir.Value) -> ir.Value:
        if value.type != self.i32:
            raise UnsupportedSyntax(node, "array index must be an int")
        return arith.index_cast(self.index, value)

    def coerce(self, node, value: ir.Value, want: ir.Type) -> ir.Value:
        """Implicit int -> float widening, which every supported language does."""
        if value is None:
            raise UnsupportedSyntax(node, "function returns no value")
        if value.type == want:
            return value
        if want == self.f64 and value.type == self.i32:
            return arith.sitofp(self.f64, value)
        raise UnsupportedSyntax(node, f"cannot convert {value.type} to {want} implicitly; add a cast")

    def promote(self, node, a: ir.Value, b: ir.Value):
        """Usual arithmetic conversion: an int next to a float becomes a float."""
        if a.type != b.type and {a.type, b.type} == {self.i32, self.f64}:
            return self.coerce(node, a, self.f64), self.coerce(node, b, self.f64)
        if a.type != b.type:
            raise UnsupportedSyntax(node, f"mixed types {a.type} and {b.type}")
        return a, b

    def carried_names(self, names: set[str], outer: dict) -> list[str]:
        return sorted(n for n in names & outer.keys() if not _is_array(outer[n]))

    def check_yield_types(self, node, names: list[str], outer: dict) -> None:
        for name in names:
            if self.env[name].type != outer[name].type:
                raise UnsupportedSyntax(node, f"{name!r} changes type inside this block")

    def check_no_lost_updates(self, node, outer: dict, carried: set[str]) -> None:
        """A name from the enclosing scope that changed but is not carried would be silently dropped."""
        for name, value in outer.items():
            if name not in carried and self.env.get(name) != value:
                raise UnsupportedSyntax(node, f"cannot carry {name!r} out of this block")

    def bind(self, name: str, value: ir.Value) -> None:
        """Bind a source name to an SSA value, remembering the name on the defining op."""
        self.env[name] = value
        if isinstance(value, ir.OpResult):
            op = value.owner
            if "polyjuice.name" not in op.attributes:
                op.attributes["polyjuice.name"] = ir.StringAttr.get(name)

    # -- expressions ----------------------------------------------------
    def condition(self, node) -> ir.Value:
        value = self.expr(node)
        if value.type != self.i1:
            raise UnsupportedSyntax(node, "a condition must be a comparison")
        return value

    def expr(self, node) -> ir.Value:
        if isinstance(node, nast.Const):
            if isinstance(node.value, bool):
                raise UnsupportedSyntax(node, "boolean values are not supported yet")
            if isinstance(node.value, int):
                if not -(2**31) <= node.value < 2**31:
                    raise UnsupportedSyntax(node, f"{node.value} does not fit in 32 bits")
                return arith.constant(self.i32, node.value)
            if math.isfinite(node.value):
                return arith.constant(self.f64, node.value)
            raise UnsupportedSyntax(node, "non-finite float literal")
        if isinstance(node, nast.Name):
            if node.id not in self.env:
                raise UnsupportedSyntax(node, f"undefined name {node.id!r}")
            return self.env[node.id]
        if isinstance(node, nast.Binary):
            lhs, rhs = self.promote(node, self.expr(node.left), self.expr(node.right))
            op = node.op
            if op == "div":
                op = "idiv" if lhs.type == self.i32 else "/"
            if op == "mod" and lhs.type == self.i32:
                return arith.remsi(lhs, rhs)
            if op == "idiv":
                return arith.divsi(lhs, rhs)
            table = _INT_OPS if lhs.type == self.i32 else _FLOAT_OPS if lhs.type == self.f64 else {}
            if op not in table:
                raise UnsupportedSyntax(node, f"unsupported operator {node.op} on {lhs.type}")
            return table[op](lhs, rhs)
        if isinstance(node, nast.Unary):
            operand = self.expr(node.operand)
            if operand.type == self.f64:
                return arith.negf(operand)
            if operand.type == self.i32:
                return arith.subi(arith.constant(self.i32, 0), operand)
            raise UnsupportedSyntax(node, f"cannot negate {operand.type}")
        if isinstance(node, nast.Compare):
            lhs, rhs = self.promote(node, self.expr(node.left), self.expr(node.right))
            if lhs.type == self.f64:
                return arith.cmpf(_FLOAT_CMPS[node.op], lhs, rhs)
            if lhs.type == self.i32:
                return arith.cmpi(_INT_CMPS[node.op], lhs, rhs)
            raise UnsupportedSyntax(node, f"cannot compare {lhs.type}")
        if isinstance(node, nast.Not):
            return arith.xori(self.condition(node.operand), arith.constant(self.i1, 1))
        if isinstance(node, nast.Logical):
            left = self.condition(node.left)
            op = scf.IfOp(left, [self.i1], has_else=True)
            short = op.then_block if node.op == "or" else op.else_block  # decided by the left side
            other = op.else_block if node.op == "or" else op.then_block
            with ir.InsertionPoint(short):
                scf.YieldOp([arith.constant(self.i1, 1 if node.op == "or" else 0)])
            with ir.InsertionPoint(other):
                scf.YieldOp([self.condition(node.right)])
            return op.results[0]
        if isinstance(node, nast.Index):
            array = self.env.get(node.array)
            if array is None or not _is_array(array):
                raise UnsupportedSyntax(node, f"{node.array!r} is not an array")
            return memref.load(array, [self.to_index(node, self.expr(node.index))])
        if isinstance(node, nast.Cast):
            arg = self.expr(node.operand)
            if node.to == FLOAT and arg.type == self.i32:
                return arith.sitofp(self.f64, arg)
            if node.to == INT and arg.type == self.f64:
                return arith.fptosi(self.i32, arg)
            if arg.type == self.mlir_type(node.to):
                return arg
            raise UnsupportedSyntax(node, f"cannot convert {arg.type} to {node.to}")
        if isinstance(node, nast.Call):
            return self.call(node)
        raise UnsupportedSyntax(node, f"unsupported expression {type(node).__name__}")

    def call(self, node: nast.Call) -> ir.Value | None:
        if node.func not in self.signatures:
            raise UnsupportedSyntax(node, f"call to unknown function {node.func!r}")
        params, results = self.signatures[node.func]
        args = [self.expr(a) for a in node.args]
        if len(args) != len(params):
            raise UnsupportedSyntax(node, f"{node.func}() takes {len(params)} arguments")
        args = [self.coerce(node, arg, param) if param == self.f64 else arg for arg, param in zip(args, params)]
        for arg, param in zip(args, params):
            if arg.type != param:
                raise UnsupportedSyntax(node, f"{node.func}() expects {param}, got {arg.type}")
        op = func.CallOp(results, ir.FlatSymbolRefAttr.get(node.func), args)
        return op.results[0] if results else None

    # -- statements -----------------------------------------------------
    def stmts(self, body: list) -> None:
        for stmt in body:
            self.stmt(stmt)

    def stmt(self, node) -> None:
        if isinstance(node, nast.Declare):
            if node.name in self.env or node.name in self.declared:
                raise UnsupportedSyntax(node, f"{node.name!r} is already declared (shadowing is not supported)")
            want = self.mlir_type(node.type)
            if node.value is None:
                self.declared[node.name] = want
            else:
                self.bind(node.name, self.coerce(node, self.expr(node.value), want))
        elif isinstance(node, nast.Assign):
            value = self.expr(node.value)
            if value is None:
                raise UnsupportedSyntax(node, "function returns no value")
            if isinstance(node.target, nast.Name):
                name = node.target.id
                if name in self.declared:
                    value = self.coerce(node, value, self.declared.pop(name))
                elif self.static and name in self.env:
                    value = self.coerce(node, value, self.env[name].type)
                self.bind(name, value)
                return
            array = self.env.get(node.target.array)
            if array is None or not _is_array(array):
                raise UnsupportedSyntax(node, f"{node.target.array!r} is not an array")
            value = self.coerce(node, value, ir.MemRefType(array.type).element_type)
            memref.store(value, array, [self.to_index(node, self.expr(node.target.index))])
        elif isinstance(node, nast.ParallelAssign):
            values = [self.expr(v) for v in node.values]  # all reads before any bind
            for target, value in zip(node.targets, values):
                if self.static and target.id in self.env:
                    value = self.coerce(node, value, self.env[target.id].type)
                self.bind(target.id, value)
        elif isinstance(node, nast.Scope):
            outer, outer_declared = set(self.env), set(self.declared)
            self.stmts(node.body)
            self.env = {n: v for n, v in self.env.items() if n in outer}
            self.declared = {n: t for n, t in self.declared.items() if n in outer_declared}
        elif isinstance(node, nast.ExprStmt):
            self.call(node.call)
        elif isinstance(node, nast.For):
            self.for_loop(node)
        elif isinstance(node, nast.While):
            self.while_loop(node)
        elif isinstance(node, nast.If):
            self.if_stmt(node)
        elif isinstance(node, nast.Return):
            raise UnsupportedSyntax(node, "return in an unsupported position")
        else:
            raise UnsupportedSyntax(node, f"unsupported statement {type(node).__name__}")

    def for_loop(self, node: nast.For) -> None:
        lower = self.expr(node.start) if node.start is not None else arith.constant(self.i32, 0)
        upper, step = self.expr(node.stop), arith.constant(self.i32, 1)
        if lower.type != self.i32 or upper.type != self.i32:
            raise UnsupportedSyntax(node, "loop bounds must be ints")
        outer, outer_declared = dict(self.env), dict(self.declared)
        carried = self.carried_names(nast.assigned_names(node.body) - {node.var}, outer)
        loop = scf.ForOp(lower, upper, step, [self.env[n] for n in carried])
        loop.operation.attributes["polyjuice.iv"] = ir.StringAttr.get(node.var)
        loop.operation.attributes["polyjuice.carried"] = _names_attr(carried)
        with ir.InsertionPoint(loop.body):
            self.env[node.var] = loop.induction_variable
            for name, arg in zip(carried, loop.inner_iter_args):
                self.env[name] = arg
            self.stmts(node.body)
            self.check_no_lost_updates(node, outer, set(carried) | {node.var})
            self.check_yield_types(node, carried, outer)
            scf.YieldOp([self.env[n] for n in carried])
        self.env, self.declared = outer, outer_declared
        for name, result in zip(carried, loop.results):
            self.env[name] = result

    def while_loop(self, node: nast.While) -> None:
        outer, outer_declared = dict(self.env), dict(self.declared)
        carried = self.carried_names(nast.assigned_names(node.body), outer)
        inits = [outer[n] for n in carried]
        types = [v.type for v in inits]
        loop = scf.WhileOp(types, inits)
        loop.operation.attributes["polyjuice.carried"] = _names_attr(carried)
        before = loop.before.blocks.append(*types)
        after = loop.after.blocks.append(*types)
        with ir.InsertionPoint(before):
            for name, arg in zip(carried, before.arguments):
                self.env[name] = arg
            scf.ConditionOp(self.condition(node.cond), list(before.arguments))
        self.env, self.declared = dict(outer), dict(outer_declared)
        with ir.InsertionPoint(after):
            for name, arg in zip(carried, after.arguments):
                self.env[name] = arg
            self.stmts(node.body)
            self.check_no_lost_updates(node, outer, set(carried))
            self.check_yield_types(node, carried, outer)
            scf.YieldOp([self.env[n] for n in carried])
        self.env, self.declared = outer, outer_declared
        for name, result in zip(carried, loop.results):
            self.env[name] = result

    def if_stmt(self, node: nast.If) -> None:
        cond = self.condition(node.cond)
        outer, outer_declared = dict(self.env), dict(self.declared)
        carried = self.carried_names(nast.assigned_names(node.then + node.orelse), outer)
        # `int x; if (c) x = 1; else x = 2;` -- declared earlier, assigned on both paths
        defined = sorted(
            (nast.assigned_names(node.then) & nast.assigned_names(node.orelse)) & outer_declared.keys()
        )
        names = carried + defined
        types = [outer[n].type for n in carried] + [outer_declared[n] for n in defined]
        op = scf.IfOp(cond, types, has_else=True)
        op.operation.attributes["polyjuice.carried"] = _names_attr(names)
        for block, body in ((op.then_block, node.then), (op.else_block, node.orelse)):
            self.env, self.declared = dict(outer), dict(outer_declared)
            with ir.InsertionPoint(block):
                self.stmts(body)
                self.check_no_lost_updates(node, outer, set(carried))
                missing = [n for n in defined if n not in self.env]
                if missing:
                    raise UnsupportedSyntax(node, f"{missing[0]!r} is not assigned on every path")
                self.check_yield_types(node, carried, outer)
                scf.YieldOp([self.env[n] for n in names])
        self.env, self.declared = outer, outer_declared
        for name, result in zip(names, op.results):
            self.env[name] = result
            self.declared.pop(name, None)

    # -- returns --------------------------------------------------------
    def lower_tail(self, stmts: list, results: list[ir.Type]) -> list[ir.Value]:
        """Lower a block whose every path ends in a return; yield the returned values."""
        if not stmts:
            raise UnsupportedSyntax(0, "a path through the function does not return")
        *head, last = stmts
        self.stmts(head)
        if isinstance(last, nast.Return):
            if last.value is None:
                if results:
                    raise UnsupportedSyntax(last, "missing return value")
                return []
            value = self.expr(last.value)
            if not results or value is None:
                raise UnsupportedSyntax(last, "returned value does not match the declared type")
            try:
                return [self.coerce(last, value, results[0])]
            except UnsupportedSyntax:
                raise UnsupportedSyntax(last, "returned value does not match the declared type") from None
        if not isinstance(last, nast.If):
            raise UnsupportedSyntax(last, "a path through the function does not return")
        cond = self.condition(last.cond)
        op = scf.IfOp(cond, results, has_else=True)
        op.operation.attributes["polyjuice.returns"] = ir.UnitAttr.get()
        outer, outer_declared = dict(self.env), dict(self.declared)
        for block, body in ((op.then_block, last.then), (op.else_block, last.orelse)):
            self.env, self.declared = dict(outer), dict(outer_declared)
            with ir.InsertionPoint(block):
                scf.YieldOp(self.lower_tail(body, results))
        self.env, self.declared = outer, outer_declared
        return list(op.results)

    def function(self, node: nast.Function) -> None:
        params, results = self.signatures[node.name]
        fn = func.FuncOp(node.name, (params, results))
        fn.operation.attributes["polyjuice.arg_names"] = _names_attr([p.name for p in node.params])
        entry = fn.add_entry_block()
        body = list(node.body)
        if not results and not _ends_with_return(body):
            body.append(nast.Return(None, node.line))
        body = normalize_returns(body, _Returns(node.ret))
        if not _ends_with_return(body):
            raise UnsupportedSyntax(node, f"not every path of {node.name}() returns a value")
        self.static = node.static
        with ir.InsertionPoint(entry):
            for param, value in zip(node.params, entry.arguments):
                self.env[param.name] = value
            func.ReturnOp(self.lower_tail(body, results))


def lower_module(module: nast.Module, context: ir.Context) -> ir.Module:
    with context, ir.Location.unknown():
        probe = _Lowerer({})
        signatures = {}
        for fn in module.functions:
            if fn.name in signatures:
                raise UnsupportedSyntax(fn, f"function {fn.name!r} is defined twice")
            signatures[fn.name] = (
                [probe.mlir_type(p.type) for p in fn.params],
                [] if fn.ret is None else [probe.mlir_type(fn.ret)],
            )
        out = ir.Module.create()
        with ir.InsertionPoint(out.body):
            for fn in module.functions:
                _Lowerer(signatures).function(fn)
        out.operation.verify()
        return out
