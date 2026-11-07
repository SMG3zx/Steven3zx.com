"""Idiomatic C emitter for the structured shared layer (func/arith/scf/memref).

Works on structured IR, never on LLVM IR: `scf.for` becomes a `for` loop,
`scf.if` becomes `if`/`else if`/`else`, loop-carried values and `scf.if`
results become ordinary mutable variables, and single-use temporaries are
inlined so `s += a[i]` stays one statement.

Names come from the `polyjuice.*` attributes written by the frontend (see
frontend.py); anything without a name gets `t0`, `t1`, ...

Known gap: C signed overflow is undefined, while the source subset defines
`int` as wrapping 32-bit. Build the output with `-fwrapv` if that matters.
"""

from dataclasses import dataclass, field

from polyjuice.mlir_env import ensure_mlir_importable

ensure_mlir_importable()

from mlir import ir  # noqa: E402

PRIMARY, UNARY, MUL, ADD, REL, EQ, TERNARY = 100, 90, 80, 70, 60, 50, 10
INDENT = "    "

# Ops between a definition and its single use that cannot change what an
# inlined expression reads. Anything else (stores, loops, ifs) blocks inlining.
_PURE = {
    "arith.constant", "arith.addi", "arith.subi", "arith.muli", "arith.cmpi",
    "arith.select", "arith.index_cast", "memref.load",
}  # fmt: skip
_BINARY = {"arith.addi": ("+", ADD), "arith.subi": ("-", ADD), "arith.muli": ("*", MUL)}
_PREDICATES = {0: ("==", EQ), 1: ("!=", EQ), 2: ("<", REL), 3: ("<=", REL), 4: (">", REL), 5: (">=", REL)}

LEGAL_OPS = frozenset(
    _PURE
    | {"builtin.module", "func.func", "func.return", "scf.for", "scf.if", "scf.yield", "memref.store"}
)


class UnsupportedForC(Exception):
    pass


@dataclass(frozen=True)
class Expr:
    text: str
    prec: int = PRIMARY
    reads: frozenset = frozenset()  # names of variables the text reads
    op: str | None = None  # set for binary expressions, enables `x += e`
    lhs: "Expr | None" = None
    rhs: "Expr | None" = None


def _wrap(child: Expr, parent_prec: int, right: bool = False) -> str:
    if child.prec < parent_prec or (right and child.prec == parent_prec):
        return f"({child.text})"
    return child.text


def _binary(symbol: str, prec: int, lhs: Expr, rhs: Expr) -> Expr:
    return Expr(
        f"{_wrap(lhs, prec)} {symbol} {_wrap(rhs, prec, right=True)}",
        prec,
        lhs.reads | rhs.reads,
        symbol,
        lhs,
        rhs,
    )


def _c_type(t: ir.Type) -> str:
    text = str(t)
    if text == "i32":
        return "int32_t"
    if text == "i1":
        return "bool"
    if text == "index":
        return "ptrdiff_t"
    raise UnsupportedForC(f"no C type for {text}")


def _attr_str(op, key: str) -> str | None:
    return ir.StringAttr(op.attributes[key]).value if key in op.attributes else None


def _attr_strs(op, key: str) -> list[str]:
    if key not in op.attributes:
        return []
    return [ir.StringAttr(a).value for a in ir.ArrayAttr(op.attributes[key])]


def _ops(block) -> list:
    return list(block.operations)


@dataclass
class _Scope:
    names: set[str] = field(default_factory=set)


class _FunctionEmitter:
    def __init__(self, fn):
        self.fn = fn
        self.lines: list[str] = []
        self.depth = 0
        self.env: dict[ir.Value, Expr] = {}
        self.scopes: list[_Scope] = [_Scope()]
        self.temp = 0

    # -- output helpers -------------------------------------------------
    def line(self, text: str) -> None:
        self.lines.append(f"{INDENT * self.depth}{text}")

    def visible(self, name: str) -> bool:
        return any(name in s.names for s in self.scopes)

    def fresh(self, hint: str | None) -> str:
        if hint is None:
            while True:
                name = f"t{self.temp}"
                self.temp += 1
                if not self.visible(name):
                    break
        else:
            name, n = hint, 1
            while self.visible(name):
                n += 1
                name = f"{hint}_{n}"
        self.scopes[-1].names.add(name)
        return name

    def var(self, name: str) -> Expr:
        return Expr(name, PRIMARY, frozenset({name}))

    # -- expressions ----------------------------------------------------
    def expr(self, value: ir.Value) -> Expr:
        return self.env[value]

    def build_expr(self, op) -> Expr:
        name = op.operation.name
        ops = [self.expr(v) for v in op.operands]
        if name in _BINARY:
            symbol, prec = _BINARY[name]
            if name == "arith.subi" and ops[0].text == "0":
                return Expr(f"-{_wrap(ops[1], UNARY)}", UNARY, ops[1].reads)
            return _binary(symbol, prec, ops[0], ops[1])
        if name == "arith.cmpi":
            predicate = ir.IntegerAttr(op.attributes["predicate"]).value
            if predicate not in _PREDICATES:
                raise UnsupportedForC(f"unsigned comparison predicate {predicate}")
            symbol, prec = _PREDICATES[predicate]
            return _binary(symbol, prec, ops[0], ops[1])
        if name == "arith.select":
            cond, a, b = ops
            return Expr(
                f"{_wrap(cond, TERNARY + 1)} ? {_wrap(a, TERNARY + 1)} : {_wrap(b, TERNARY + 1)}",
                TERNARY,
                cond.reads | a.reads | b.reads,
            )
        if name == "arith.index_cast":
            to = str(op.results[0].type)
            return ops[0] if to == "index" else Expr(f"(int32_t){_wrap(ops[0], UNARY)}", UNARY, ops[0].reads)
        if name == "memref.load":
            array, *indices = ops
            return Expr(array.text + "".join(f"[{i.text}]" for i in indices), PRIMARY, array.reads | frozenset().union(*(i.reads for i in indices)))
        raise UnsupportedForC(f"cannot build expression for {name}")

    def can_inline(self, op, use) -> bool:
        """A single-use value may be inlined at its use if nothing in between has effects."""
        user = use.owner
        if user.operation.block != op.operation.block:
            return False
        between = _ops(op.operation.block)
        start = next(i for i, o in enumerate(between) if o.operation == op.operation)
        for other in between[start + 1 :]:
            if other.operation == user.operation:
                return True
            if other.operation.name not in _PURE:
                return False
        return False

    def bind_value(self, op) -> None:
        result = op.results[0]
        expr = self.build_expr(op)
        uses = list(result.uses)
        if not uses:
            return
        if len(uses) == 1 and self.can_inline(op, uses[0]):
            self.env[result] = expr
            return
        name = self.fresh(_attr_str(op, "polyjuice.name"))
        self.line(f"{_c_type(result.type)} {name} = {expr.text};")
        self.env[result] = self.var(name)

    # -- statements -----------------------------------------------------
    def emit_block(self, block) -> None:
        for op in _ops(block):
            if op.operation.name not in ("scf.yield", "func.return"):
                self.emit_block_op(op)

    def emit_block_op(self, op) -> None:
        name = op.operation.name
        if name == "arith.constant":
            value = ir.IntegerAttr(op.attributes["value"]).value
            self.env[op.results[0]] = Expr(str(value), UNARY if value < 0 else PRIMARY)
        elif name in _PURE:
            self.bind_value(op)
        elif name == "memref.store":
            value, array, *indices = [self.expr(v) for v in op.operands]
            target = array.text + "".join(f"[{i.text}]" for i in indices)
            self.line(f"{target} = {value.text};")
        elif name == "scf.for":
            self.emit_for(op)
        elif name == "scf.if":
            self.emit_if(op)
        else:
            raise UnsupportedForC(f"cannot emit {name}")

    def assign(self, pairs: list[tuple[str, Expr]]) -> None:
        """Assign values to existing variables, keeping parallel-assignment semantics."""
        pairs = [(n, e) for n, e in pairs if e.text != n]
        names = [n for n, _ in pairs]
        conflict = any(
            names[i] in pairs[j][1].reads for i in range(len(pairs)) for j in range(i + 1, len(pairs))
        )
        if conflict:
            temps = []
            for name, expr in pairs:
                tmp = self.fresh(f"next_{name}")
                self.line(f"int32_t {tmp} = {expr.text};")
                temps.append((name, tmp))
            for name, tmp in temps:
                self.line(f"{name} = {tmp};")
            return
        for name, expr in pairs:
            if expr.op in ("+", "-") and expr.lhs.text == name and expr.rhs.text == "1":
                self.line(f"{name}{expr.op * 2};")
            elif expr.op in ("+", "-", "*") and expr.lhs.text == name:
                self.line(f"{name} {expr.op}= {expr.rhs.text};")
            else:
                self.line(f"{name} = {expr.text};")

    def yields(self, block) -> list[Expr]:
        return [self.expr(v) for v in _ops(block)[-1].operands]

    def emit_for(self, op) -> None:
        lower, upper, step, *inits = [self.expr(v) for v in op.operands]
        init_values = list(op.operands)[3:]
        body = op.regions[0].blocks[0]
        iv_arg, *iter_args = list(body.arguments)
        hints = _attr_strs(op, "polyjuice.carried")
        carried: list[str] = []
        for k, (value, init) in enumerate(zip(init_values, inits)):
            hint = hints[k] if k < len(hints) else None
            reuse = hint is not None and init.text == hint and len(list(value.uses)) == 1
            if reuse:
                carried.append(hint)
            else:
                name = self.fresh(hint)
                self.line(f"int32_t {name} = {init.text};")
                carried.append(name)
        # `i < stop` is re-evaluated every iteration in C, Python evaluates range() once.
        if upper.reads & set(carried) or "[" in upper.text:
            limit = self.fresh("limit")
            self.line(f"int32_t {limit} = {upper.text};")
            upper = self.var(limit)
        self.scopes.append(_Scope())
        iv = self.fresh(_attr_str(op, "polyjuice.iv"))
        increment = f"{iv}++" if step.text == "1" else f"{iv} += {step.text}"
        self.line(
            f"for (int32_t {iv} = {lower.text}; {iv} < {_wrap(upper, REL + 1)}; {increment}) {{"
        )
        self.env[iv_arg] = self.var(iv)
        for name, arg in zip(carried, iter_args):
            self.env[arg] = self.var(name)
        self.depth += 1
        self.emit_block(body)
        self.assign(list(zip(carried, self.yields(body))))
        self.depth -= 1
        self.line("}")
        self.scopes.pop()
        for name, result in zip(carried, op.results):
            self.env[result] = self.var(name)

    def emit_branch(self, block, dests: list[str]) -> None:
        self.depth += 1
        self.scopes.append(_Scope())
        self.emit_block(block)
        self.assign(list(zip(dests, self.yields(block))))
        self.scopes.pop()
        self.depth -= 1

    def else_if_target(self, block):
        """Return (prelude_ops, inner_if) when the else block is `[pure ops] scf.if yield-its-results`.

        The prelude may only compute the inner condition, so it must emit no statements.
        """
        ops = _ops(block)
        if len(ops) < 2 or ops[-2].operation.name != "scf.if":
            return None
        inner, terminator = ops[-2], ops[-1]
        if list(terminator.operands) != list(inner.results):
            return None
        prelude = ops[:-2]
        for op in prelude:
            if op.operation.name not in _PURE or op.operation.name == "memref.load":
                return None
            if op.operation.name != "arith.constant":
                uses = list(op.results[0].uses)
                if len(uses) != 1 or not self.can_inline(op, uses[0]):
                    return None
        return prelude, inner

    def emit_if(self, op, dests: list[str] | None = None, opener: str = "if") -> None:
        then_block, else_block = op.regions[0].blocks[0], op.regions[1].blocks[0]
        hints = _attr_strs(op, "polyjuice.carried")
        else_is_bare = len(_ops(else_block)) == 1  # only the yield
        initialized_from_else = False
        if dests is None:
            dests = []
            for k, result in enumerate(op.results):
                name = self.fresh(hints[k] if k < len(hints) else None)
                dests.append(name)
                if else_is_bare:
                    # `T r = else_value; if (c) { r = then_value; }`
                    self.line(f"{_c_type(result.type)} {name} = {self.yields(else_block)[k].text};")
                    initialized_from_else = True
                else:
                    self.line(f"{_c_type(result.type)} {name};")
        for result, name in zip(op.results, dests):
            self.env[result] = self.var(name)
        cond = self.expr(op.operands[0])
        self.line(f"{opener} ({cond.text}) {{")
        self.emit_branch(then_block, dests)
        if else_is_bare and (not op.results or initialized_from_else):
            self.line("}")
            return
        chain = self.else_if_target(else_block)
        if chain is not None:
            prelude, inner = chain
            for pre in prelude:
                self.emit_block_op(pre)
            self.emit_if(inner, dests, opener="} else if")
            return
        self.line("} else {")
        self.emit_branch(else_block, dests)
        self.line("}")

    # -- functions ------------------------------------------------------
    def emit(self) -> str:
        fn = self.fn
        body = fn.regions[0].blocks[0]
        names = _attr_strs(fn.operation, "polyjuice.arg_names")
        params = []
        for k, arg in enumerate(body.arguments):
            name = self.fresh(names[k] if k < len(names) else f"arg{k}")
            self.env[arg] = self.var(name)
            t = str(arg.type)
            if t.startswith("memref<"):
                memref_type = ir.MemRefType(arg.type)
                dims = "".join(f"[{d}]" for d in memref_type.shape)
                params.append(f"{_c_type(memref_type.element_type)} {name}{dims}")
            else:
                params.append(f"{_c_type(arg.type)} {name}")
        results = ir.FunctionType(ir.TypeAttr(fn.operation.attributes["function_type"]).value).results
        ret = _c_type(results[0]) if results else "void"
        sym = ir.StringAttr(fn.operation.attributes["sym_name"]).value
        self.lines.append(f"{ret} {sym}({', '.join(params) or 'void'}) {{")
        self.depth = 1
        self.emit_block(body)
        terminator = _ops(body)[-1]
        if terminator.operands:
            self.line(f"return {self.expr(terminator.operands[0]).text};")
        self.lines.append("}")
        return "\n".join(self.lines)


def emit_c(module: ir.Module) -> str:
    functions = [
        _FunctionEmitter(op).emit()
        for op in module.body.operations
        if op.operation.name == "func.func"
    ]
    text = "\n\n".join(functions) + "\n"
    includes = ["<stdint.h>"]
    if "bool " in text:
        includes.append("<stdbool.h>")
    if "ptrdiff_t" in text:
        includes.append("<stddef.h>")
    header = "".join(f"#include {inc}\n" for inc in sorted(includes))
    return f"{header}\n{text}"
