"""Shared emitter engine for the structured layer (func/arith/scf/memref).

Works on structured IR, never on LLVM IR: `scf.for` becomes a counted loop,
`scf.if` becomes `if`/`else if`/`else`, loop-carried values and `scf.if`
results become ordinary mutable variables, and single-use temporaries are
inlined so `s += a[i]` stays one statement.

Language differences live in a `Syntax` table (see emit_c.py, emit_python.py).
Names come from the `polyjuice.*` attributes written by the frontend (see
frontend.py); anything without a name gets `t0`, `t1`, ...
"""

import re
from collections.abc import Callable
from dataclasses import dataclass, field, replace

from polyjuice.mlir_env import ensure_mlir_importable

ensure_mlir_importable()

from mlir import ir  # noqa: E402

PRIMARY, UNARY, MUL, ADD, REL, EQ, AND, OR, TERNARY = 100, 90, 80, 70, 60, 50, 40, 30, 10
INDENT = "    "

# Ops between a definition and its single use that cannot change what an
# inlined expression reads. Anything else (stores, loops, ifs) blocks inlining.
PURE = frozenset({
    "arith.constant", "arith.addi", "arith.subi", "arith.muli", "arith.cmpi",
    "arith.select", "arith.index_cast", "memref.load",
    "arith.addf", "arith.subf", "arith.mulf", "arith.divf", "arith.negf", "arith.cmpf",
    "arith.sitofp", "arith.fptosi", "arith.divsi", "arith.remsi", "arith.xori", "arith.remf",
    "arith.floordivsi",
})  # fmt: skip
_BINARY = {
    "arith.addi": ("+", ADD), "arith.subi": ("-", ADD), "arith.muli": ("*", MUL),
    "arith.addf": ("+", ADD), "arith.subf": ("-", ADD), "arith.mulf": ("*", MUL),
    "arith.divf": ("/", MUL),
}  # fmt: skip
_FLOAT_PREDICATES = {1: ("==", EQ), 2: (">", REL), 3: (">=", REL), 4: ("<", REL), 5: ("<=", REL), 13: ("!=", EQ)}
_PREDICATES = {0: ("==", EQ), 1: ("!=", EQ), 2: ("<", REL), 3: ("<=", REL), 4: (">", REL), 5: (">=", REL)}

LEGAL_OPS = frozenset(
    PURE | {"builtin.module", "func.func", "func.return", "func.call", "scf.for", "scf.if", "scf.while",
            "scf.condition", "scf.yield", "memref.store"}
)


class UnsupportedForTarget(Exception):
    pass


@dataclass(frozen=True)
class Syntax:
    language: str
    reserved: frozenset
    type_name: Callable[[ir.Type], str]
    param: Callable[[ir.Type, str], str]  # (type, name) -> parameter text
    function_header: Callable[[str | None, str, list[str]], str]  # (return type, name, params)
    declare: Callable[[str, str, str | None], str | None]  # (type, name, init) -> statement or None
    for_header: Callable[[str, str, str, str], str]  # (iv, lower, upper, step) texts
    if_header: Callable[[str, str], str]  # (keyword, cond)
    while_header: Callable[[str], str]  # (cond)
    if_keyword: str
    elif_keyword: str
    else_header: str
    close: str | None  # closing line of a block ("}"), None for indentation languages
    terminator: str  # statement terminator (";" or "")
    ternary: Callable[[str, str, str], str]  # (cond, a, b)
    range_evaluated_once: bool  # Python evaluates range() once, C re-evaluates the bound
    tuple_assign: bool  # `a, b = b, a + b` instead of temporaries
    increment: bool  # `x++` / `x--` for +-1
    empty_block: str | None  # statement for an empty block ("pass")
    function_separator: str
    prelude: Callable[[str], str]  # body text -> header (includes) text
    return_stmt: Callable[[str | None], str]
    cast: Callable[[str, str], str]  # (type, operand text)
    not_expr: Callable  # (Expr) -> Expr
    logic_symbols: tuple[str, str]  # ("&&", "||")
    bool_text: Callable[[bool], str]
    float_rem: Callable  # (Expr, Expr) -> Expr; C fmod
    floor_div: Callable  # (Expr, Expr) -> Expr; rounds toward negative infinity
    floor_rem: Callable  # (Expr, Expr) -> Expr; takes the divisor's sign
    int_div: Callable  # (Expr, Expr) -> Expr; truncates toward zero
    int_rem: Callable  # (Expr, Expr) -> Expr; sign of the dividend


@dataclass(frozen=True)
class Expr:
    text: str
    prec: int = PRIMARY
    reads: frozenset = frozenset()  # names of variables the text reads
    op: str | None = None  # set for binary expressions, enables `x += e`
    lhs: "Expr | None" = None
    rhs: "Expr | None" = None
    effects: bool = False  # contains a call, so evaluation order matters


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
        lhs.effects or rhs.effects,
    )


def binary_expr(symbol: str, prec: int, lhs: "Expr", rhs: "Expr") -> "Expr":
    return _binary(symbol, prec, lhs, rhs)


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


class FunctionEmitter:
    def __init__(self, fn, syntax: Syntax):
        self.fn = fn
        self.syn = syntax
        self.lines: list[str] = []
        self.depth = 0
        self.env: dict[ir.Value, Expr] = {}
        self.scopes: list[_Scope] = [_Scope()]
        self.temp = 0
        # loops whose `return`s the lowering turned into a done flag: flag name -> result name.
        # The flags never appear in the output; their assignments become `return`s.
        self.exit_flags: dict[str, str | None] = {}
        self.exit_stack: list[str] = []

    # -- output helpers -------------------------------------------------
    def line(self, text: str) -> None:
        self.lines.append(f"{INDENT * self.depth}{text}")

    def stmt(self, text: str | None) -> None:
        if text is not None:
            self.line(text + self.syn.terminator)

    def close(self) -> None:
        if self.syn.close is not None:
            self.line(self.syn.close)

    def visible(self, name: str) -> bool:
        return any(name in s.names for s in self.scopes)

    def safe(self, name: str) -> str:
        if name.startswith("__polyjuice_"):  # exit flags: never printed, so keep them collision-proof
            return name
        counter = re.fullmatch(r"__ctr_(.+)_\d+", name)  # a loop counter the lowering introduced
        if counter:
            name = counter.group(1)
        elif name.startswith("__"):  # `__x` is reserved in C
            name = name.lstrip("_") + "_"
        return name + "_" if name in self.syn.reserved else name

    def fresh(self, hint: str | None) -> str:
        if hint is None:
            while True:
                name = f"t{self.temp}"
                self.temp += 1
                if not self.visible(name):
                    break
        else:
            base = self.safe(hint)
            name, n = base, 1
            while self.visible(name):
                n += 1
                name = f"{base}_{n}"
        self.scopes[-1].names.add(name)
        return name

    def var(self, name: str) -> Expr:
        return Expr(name, PRIMARY, frozenset({name}))

    # -- expressions ----------------------------------------------------
    def expr(self, value: ir.Value) -> Expr:
        return self.env[value]

    def build_expr(self, op) -> Expr:
        expr = self._build_expr(op)
        if not expr.effects and any(self.expr(v).effects for v in op.operands):
            expr = replace(expr, effects=True)
        return expr

    def _build_expr(self, op) -> Expr:
        name = op.operation.name
        ops = [self.expr(v) for v in op.operands]
        if name == "func.call":
            callee = self.safe(ir.FlatSymbolRefAttr(op.attributes["callee"]).value)
            reads = frozenset().union(*(o.reads for o in ops))
            return Expr(f"{callee}({', '.join(o.text for o in ops)})", PRIMARY, reads, effects=True)
        if name == "arith.xori":
            if ops[1].text not in ("true", "True", "1"):
                raise UnsupportedForTarget("xor other than boolean not")
            return self.syn.not_expr(ops[0])
        if name == "arith.floordivsi":
            return self.syn.floor_div(ops[0], ops[1])
        if name == "arith.subi":
            rem = self.floor_remainder(op)
            if rem is not None:
                return rem
        if name == "arith.remf":
            return self.syn.float_rem(ops[0], ops[1])
        if name == "arith.divsi":
            return self.syn.int_div(ops[0], ops[1])
        if name == "arith.remsi":
            return self.syn.int_rem(ops[0], ops[1])
        if name == "arith.negf":
            return Expr(f"-{_wrap(ops[0], UNARY)}", UNARY, ops[0].reads)
        if name == "arith.cmpf":
            predicate = ir.IntegerAttr(op.attributes["predicate"]).value
            if predicate not in _FLOAT_PREDICATES:
                raise UnsupportedForTarget(f"float comparison predicate {predicate}")
            symbol, prec = _FLOAT_PREDICATES[predicate]
            return _binary(symbol, prec, ops[0], ops[1])
        if name in ("arith.sitofp", "arith.fptosi"):
            return Expr(self.syn.cast(self.syn.type_name(op.results[0].type), _wrap(ops[0], UNARY)), UNARY, ops[0].reads)
        if name in _BINARY:
            symbol, prec = _BINARY[name]
            if name == "arith.subi" and ops[0].text == "0":  # 0 - x is how the frontend writes -x
                return Expr(f"-{_wrap(ops[1], UNARY)}", UNARY, ops[1].reads)
            return _binary(symbol, prec, ops[0], ops[1])
        if name == "arith.cmpi":
            predicate = ir.IntegerAttr(op.attributes["predicate"]).value
            if predicate not in _PREDICATES:
                raise UnsupportedForTarget(f"unsigned comparison predicate {predicate}")
            symbol, prec = _PREDICATES[predicate]
            return _binary(symbol, prec, ops[0], ops[1])
        if name == "arith.select":
            cond, a, b = ops
            return Expr(
                self.syn.ternary(
                    _wrap(cond, TERNARY + 1), _wrap(a, TERNARY + 1), _wrap(b, TERNARY + 1)
                ),
                TERNARY,
                cond.reads | a.reads | b.reads,
            )
        if name == "arith.index_cast":
            to = str(op.results[0].type)
            if to == "index":
                return ops[0]
            return Expr(self.syn.cast(self.syn.type_name(op.results[0].type), _wrap(ops[0], UNARY)), UNARY, ops[0].reads)
        if name == "memref.load":
            array, *indices = ops
            reads = array.reads | frozenset().union(*(i.reads for i in indices))
            return Expr(array.text + "".join(f"[{i.text}]" for i in indices), PRIMARY, reads)
        raise UnsupportedForTarget(f"cannot build expression for {name}")

    def floor_remainder(self, op):
        """`a - floordiv(a, b) * b` is how the lowering writes floormod; print it as one operator."""
        a, product = op.operands
        if not isinstance(product, ir.OpResult) or product.owner.operation.name != "arith.muli":
            return None
        quotient, b = product.owner.operands
        if not isinstance(quotient, ir.OpResult) or quotient.owner.operation.name != "arith.floordivsi":
            return None
        if list(quotient.owner.operands) != [a, b]:
            return None
        return self.syn.floor_rem(self.expr(a), self.expr(b))

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
            if other.operation.name not in PURE:
                return False
        return False

    def contains_call(self, value: ir.Value) -> bool:
        if not isinstance(value, ir.OpResult):
            return False
        op = value.owner
        return op.operation.name == "func.call" or (
            op.operation.name in PURE and any(self.contains_call(v) for v in op.operands)
        )

    def competes_with_call(self, use) -> bool:
        """C leaves argument/operand evaluation order open; two calls in one expression must be sequenced."""
        return sum(self.contains_call(v) for v in use.owner.operands) > 1

    def bind_value(self, op) -> None:
        result = op.results[0]
        expr = self.build_expr(op)
        uses = list(result.uses)
        if not uses:
            return
        if len(uses) == 1 and self.can_inline(op, uses[0]) and not self.competes_with_call(uses[0]):
            self.env[result] = expr
            return
        name = self.fresh(_attr_str(op, "polyjuice.name"))
        self.stmt(self.syn.declare(self.syn.type_name(result.type), name, expr.text))
        self.env[result] = self.var(name)

    # -- statements -----------------------------------------------------
    def emit_block(self, block) -> None:
        for op in _ops(block):
            if op.operation.name not in ("scf.yield", "func.return"):
                self.emit_block_op(op)

    def emit_block_op(self, op) -> None:
        name = op.operation.name
        if name == "arith.constant":
            if str(op.results[0].type) == "i1":
                value = bool(ir.IntegerAttr(op.attributes["value"]).value)
                self.env[op.results[0]] = Expr(self.syn.bool_text(value))
                return
            if str(op.results[0].type) == "f64":
                value = ir.FloatAttr(op.attributes["value"]).value
                text = repr(value)
            else:
                value = ir.IntegerAttr(op.attributes["value"]).value
                text = str(value)
            self.env[op.results[0]] = Expr(text, UNARY if value < 0 else PRIMARY)
        elif name == "func.call":
            if list(op.results) and list(op.results[0].uses):
                self.bind_value(op)
            else:
                self.stmt(self.build_expr(op).text)
        elif name == "scf.while":
            self.emit_while(op)
        elif name in PURE:
            self.bind_value(op)
        elif name == "memref.store":
            value, array, *indices = [self.expr(v) for v in op.operands]
            target = array.text + "".join(f"[{i.text}]" for i in indices)
            self.stmt(f"{target} = {value.text}")
        elif name == "scf.for":
            self.emit_for(op)
        elif name == "scf.if":
            if not self.try_logical(op):
                self.emit_if(op)
        else:
            raise UnsupportedForTarget(f"cannot emit {name}")

    def try_logical(self, op) -> bool:
        """`if a { b } else { false }` is `a && b`; `if a { true } else { b }` is `a || b`."""
        if len(list(op.results)) != 1 or str(op.results[0].type) != "i1":
            return False
        then_block, else_block = op.regions[0].blocks[0], op.regions[1].blocks[0]

        def constant(block):
            """The boolean a block yields when it does nothing else (canonicalize hoists constants out)."""
            ops = _ops(block)
            if len(ops) > 2 or ops[-1].operation.name != "scf.yield" or len(list(ops[-1].operands)) != 1:
                return None
            if len(ops) == 2 and ops[0].operation.name != "arith.constant":
                return None
            value = ops[-1].operands[0]
            if isinstance(value, ir.OpResult) and value.owner.operation.name == "arith.constant":
                return bool(ir.IntegerAttr(value.owner.attributes["value"]).value)
            return None

        if constant(else_block) is False:
            symbol, prec, operand_block = self.syn.logic_symbols[0], AND, then_block
        elif constant(then_block) is True:
            symbol, prec, operand_block = self.syn.logic_symbols[1], OR, else_block
        else:
            return False
        mark = len(self.lines)
        for inner in _ops(operand_block)[:-1]:
            self.emit_block_op(inner)
        if len(self.lines) != mark:
            raise UnsupportedForTarget("a logical operand that needs statements")
        left = self.expr(op.operands[0])
        right = self.expr(_ops(operand_block)[-1].operands[0])
        expr = _binary(symbol, prec, left, right)
        result = op.results[0]
        uses = list(result.uses)
        if not uses:
            return True
        if len(uses) == 1 and self.can_inline(op, uses[0]) and not self.competes_with_call(uses[0]):
            self.env[result] = expr
        else:
            name = self.fresh(_attr_str(op, "polyjuice.name"))
            self.stmt(self.syn.declare(self.syn.type_name(result.type), name, expr.text))
            self.env[result] = self.var(name)
        return True

    def assign(self, pairs: list[tuple[str, Expr]]) -> None:
        """Assign values to existing variables, keeping parallel-assignment semantics."""
        if self.exit_stack:
            done = self.exit_stack[-1]
            if any(n == done and e.text in ("1", "true", "True") for n, e in pairs):
                result = self.exit_flags[done]
                value = next((e for n, e in pairs if n == result), None) if result else None
                self.stmt(self.syn.return_stmt(value.text if value is not None else None))
                return
        pairs = [(n, e) for n, e in pairs if e.text != n]
        names = [n for n, _ in pairs]
        conflict = any(
            names[i] in pairs[j][1].reads for i in range(len(pairs)) for j in range(i + 1, len(pairs))
        )
        if conflict:
            self.assign_in_order(pairs)
            return
        for name, expr in pairs:
            if self.syn.increment and expr.op in ("+", "-") and expr.lhs.text == name and expr.rhs.text == "1":
                self.stmt(f"{name}{expr.op * 2}")
            elif expr.op in ("+", "-", "*") and expr.lhs.text == name:
                self.stmt(f"{name} {expr.op}= {expr.rhs.text}")
            else:
                self.stmt(f"{name} = {expr.text}")

    def assign_in_order(self, pairs: list[tuple[str, Expr]]) -> None:
        """Parallel assignment without tuples: emit each assignment once nothing pending still
        reads its target, and break a cycle with one temporary (`next_x`)."""
        pending = list(pairs)
        while pending:
            ready = next(
                (p for p in pending if not any(p[0] in q[1].reads for q in pending if q is not p)), None
            )
            if ready is not None:
                pending.remove(ready)
                self.assign([ready])
                continue
            if self.syn.tuple_assign:  # a true cycle: `a, b = b, a + b`
                self.stmt(f"{', '.join(n for n, _ in pending)} = {', '.join(e.text for _, e in pending)}")
                return
            # a cycle: stash the most complex right-hand side, which then reads nothing pending
            victim = max(pending, key=lambda p: len(p[1].text) if p[1].op else 0)
            tmp = self.fresh(f"next_{victim[0]}")
            self.stmt(self.syn.declare(self.syn.type_name(self.type_of_name(victim[0])), tmp, victim[1].text))
            pending[pending.index(victim)] = (victim[0], self.var(tmp))

    def type_of_name(self, name: str):
        """The MLIR type of a variable: its declared one, found through the environment."""
        for value, expr in self.env.items():
            if expr.text == name:
                return value.type
        raise UnsupportedForTarget(f"cannot find the type of {name!r}")

    def yields(self, block) -> list[Expr]:
        return [self.expr(v) for v in _ops(block)[-1].operands]

    def in_place_ifs(self, block, dests: list[str], args) -> dict:
        """scf.if ops whose results only feed the block's yield can assign the destinations directly.

        `args` are the block arguments holding the destinations' old values (None when the
        destinations are fresh). The old value must be dead once the if has run.
        """
        ops = _ops(block)
        yielded = list(ops[-1].operands)
        found = {}
        for pos, op in enumerate(ops[:-1]):
            if op.operation.name != "scf.if" or "polyjuice.returns" in op.attributes or not list(op.results):
                continue
            slots = []
            for result in op.results:
                uses = list(result.uses)
                if len(uses) != 1 or uses[0].owner != ops[-1].operation or result not in yielded:
                    break
                slots.append(yielded.index(result))
            else:
                if args is not None and not all(self.dead_after(args[j], ops, pos) for j in slots):
                    continue
                found[op.operation] = [dests[j] for j in slots]
        return found

    def dead_after(self, value: ir.Value, ops: list, pos: int) -> bool:
        """No use of `value` happens after the op at `pos` (uses inside that op are fine)."""
        later = {o.operation for o in ops[pos + 1 :]}
        for use in value.uses:
            owner = use.owner
            while owner is not None:
                if owner in later:
                    return False
                owner = owner.parent
        return True

    def body(self, block, dests: list[str], args=None) -> None:
        """Emit a nested block's statements plus the assignments of its yielded values."""
        self.depth += 1
        self.scopes.append(_Scope())
        before = len(self.lines)
        in_place = self.in_place_ifs(block, dests, args)
        for op in _ops(block)[:-1]:
            if op.operation in in_place:
                self.emit_if(op, dests=in_place[op.operation])
            else:
                self.emit_block_op(op)
        self.assign(list(zip(dests, self.yields(block))))
        if len(self.lines) == before and self.syn.empty_block:
            self.line(self.syn.empty_block)
        self.scopes.pop()
        self.depth -= 1

    def declare_carried(self, values: list, inits: list[Expr], hints: list[str], hidden=()) -> list[str]:
        carried: list[str] = []
        for k, (value, init) in enumerate(zip(values, inits)):
            hint = hints[k] if k < len(hints) else None
            safe = self.safe(hint) if hint is not None else None
            if hint in hidden:
                carried.append(safe)  # an exit flag: used for bookkeeping only, never declared
            elif safe is not None and init.text == safe and len(list(value.uses)) == 1:
                carried.append(safe)  # the old value dies here: update it in place
            else:
                name = self.fresh(hint)
                self.stmt(self.syn.declare(self.syn.type_name(value.type), name, init.text))
                carried.append(name)
        return carried

    def emit_while(self, op) -> None:
        before, after = op.regions[0].blocks[0], op.regions[1].blocks[0]
        values = list(op.operands)
        exits = _attr_strs(op, "polyjuice.exits")
        done = self.safe(exits[0]) if exits else None
        if exits:
            self.exit_flags[done] = self.safe(exits[1]) if exits[1] else None
        carried = self.declare_carried(
            values, [self.expr(v) for v in values], _attr_strs(op, "polyjuice.carried"), hidden=set(exits)
        )
        for block in (before, after):
            for name, arg in zip(carried, block.arguments):
                self.env[arg] = self.var(name)
        *pre, condition = _ops(before)
        mark = len(self.lines)
        for pre_op in pre:
            self.emit_block_op(pre_op)
        if len(self.lines) != mark:
            raise UnsupportedForTarget(
                "a while condition that needs statements: " + "; ".join(l.strip() for l in self.lines[mark:])
            )
        if list(condition.operands)[1:] != list(before.arguments):
            raise UnsupportedForTarget("a while loop that forwards values other than its carried variables")
        cond = self.expr(condition.operands[0])
        if done is not None and cond.lhs is not None and cond.lhs.text == f"{done} == 0":
            cond = cond.rhs  # `done == 0 && c` guarded the loop; with real returns it is just `c`
        self.line(self.syn.while_header(cond.text))
        if done is not None:
            self.exit_stack.append(done)
        self.body(after, carried, list(after.arguments))
        if done is not None:
            self.exit_stack.pop()
        self.close()
        for name, result in zip(carried, op.results):
            self.env[result] = self.var(name)

    def emit_for(self, op) -> None:
        lower, upper, step, *inits = [self.expr(v) for v in op.operands]
        init_values = list(op.operands)[3:]
        block = op.regions[0].blocks[0]
        iv_arg, *iter_args = list(block.arguments)
        carried = self.declare_carried(init_values, inits, _attr_strs(op, "polyjuice.carried"))
        # C re-evaluates `i < stop` every iteration; Python evaluates range() once.
        if not self.syn.range_evaluated_once and (upper.reads & set(carried) or "[" in upper.text):
            limit = self.fresh("limit")
            self.stmt(self.syn.declare(self.syn.type_name(iv_arg.type), limit, upper.text))
            upper = self.var(limit)
        self.scopes.append(_Scope())
        iv = self.fresh(_attr_str(op, "polyjuice.iv"))
        self.line(
            self.syn.for_header(iv, lower.text, _wrap(upper, REL + 1), step.text)
        )
        self.env[iv_arg] = self.var(iv)
        for name, arg in zip(carried, iter_args):
            self.env[arg] = self.var(name)
        self.body(block, carried, iter_args)
        self.close()
        self.scopes.pop()
        for name, result in zip(carried, op.results):
            self.env[result] = self.var(name)

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
            if op.operation.name not in PURE or op.operation.name == "memref.load":
                return None
            if op.operation.name != "arith.constant":
                uses = list(op.results[0].uses)
                if len(uses) != 1 or not self.can_inline(op, uses[0]):
                    return None
        return prelude, inner

    def flag_check(self, op) -> bool:
        """Is this `if done != 0 { ... }` on an exit flag? Then its condition is false wherever
        it can be reached, because every path that set the flag already returned."""
        cond = self.expr(op.operands[0]).text
        return any(cond == f"{flag} != 0" for flag in self.exit_flags)

    def inline_else(self, op, dests) -> None:
        else_block = op.regions[1].blocks[0]
        for inner in _ops(else_block)[:-1]:
            self.emit_block_op(inner)
        yielded = self.yields(else_block)
        if dests is not None:
            self.assign(list(zip(dests, yielded)))
            for result, name in zip(op.results, dests):
                self.env[result] = self.var(name)
        else:
            for result, value in zip(op.results, yielded):
                self.env[result] = value

    def emit_if(self, op, dests: list[str] | None = None, keyword: str | None = None) -> None:
        if self.flag_check(op):
            self.inline_else(op, dests)
            return
        keyword = keyword or self.syn.if_keyword
        then_block, else_block = op.regions[0].blocks[0], op.regions[1].blocks[0]
        hints = _attr_strs(op, "polyjuice.carried")
        else_is_bare = len(_ops(else_block)) == 1  # only the yield
        initialized_from_else = False
        if dests is None:
            dests = []
            for k, result in enumerate(op.results):
                name = self.fresh(hints[k] if k < len(hints) else None)
                dests.append(name)
                type_text = self.syn.type_name(result.type)
                if else_is_bare:
                    # `T r = else_value; if (c) { r = then_value; }`
                    self.stmt(self.syn.declare(type_text, name, self.yields(else_block)[k].text))
                    initialized_from_else = True
                else:
                    self.stmt(self.syn.declare(type_text, name, None))
        for result, name in zip(op.results, dests):
            self.env[result] = self.var(name)
        cond = self.expr(op.operands[0])
        self.line(self.syn.if_header(keyword, cond.text))
        self.body(then_block, dests)
        else_changes_nothing = else_is_bare and all(
            e.text == d for e, d in zip(self.yields(else_block), dests)
        )
        if else_is_bare and (not op.results or initialized_from_else or else_changes_nothing):
            self.close()
            return
        if self.lines[-1].strip().startswith("return"):
            # the then-branch returned: the else path simply continues, no `else` needed
            self.close()
            self.inline_else(op, dests)
            return
        chain = self.else_if_target(else_block)
        if chain is not None:
            prelude, inner = chain
            for pre in prelude:
                self.emit_block_op(pre)
            self.emit_if(inner, dests, keyword=self.syn.elif_keyword)
            return
        self.line(self.syn.else_header)
        self.body(else_block, dests)
        self.close()

    # -- early returns --------------------------------------------------
    def returning_tail(self, block):
        """The scf.if that ends `block` with its results returned from the function, if any."""
        ops = _ops(block)
        if len(ops) >= 2 and ops[-2].operation.name == "scf.if" and "polyjuice.returns" in ops[-2].attributes:
            if list(ops[-1].operands) == list(ops[-2].results):
                return ops[-2]
        return None

    def emit_returning(self, block, final: bool = False) -> None:
        """Emit `block` whose every path returns; `final` skips a redundant trailing bare return."""
        ops = _ops(block)
        tail = self.returning_tail(block)
        for op in ops[:-1]:
            if tail is None or op.operation != tail.operation:
                self.emit_block_op(op)
        if tail is not None:
            self.emit_return_if(tail, final)
            return
        values = list(ops[-1].operands)
        if values:
            self.stmt(self.syn.return_stmt(self.expr(values[0]).text))
        elif not final:
            self.stmt(self.syn.return_stmt(None))

    def emit_return_if(self, op, final: bool) -> None:
        """Guard-clause style: `if (c) { return a; }` then the else path continues unindented."""
        then_block, else_block = op.regions[0].blocks[0], op.regions[1].blocks[0]
        if self.flag_check(op):
            self.emit_returning(else_block, final)
            return
        cond = self.expr(op.operands[0])
        self.line(self.syn.if_header(self.syn.if_keyword, cond.text))
        self.depth += 1
        self.scopes.append(_Scope())
        self.emit_returning(then_block)
        self.scopes.pop()
        self.depth -= 1
        self.close()
        self.emit_returning(else_block, final)

    # -- functions ------------------------------------------------------
    def emit(self) -> str:
        fn = self.fn
        block = fn.regions[0].blocks[0]
        names = _attr_strs(fn.operation, "polyjuice.arg_names")
        params = []
        for k, arg in enumerate(block.arguments):
            name = self.fresh(names[k] if k < len(names) else f"arg{k}")
            self.env[arg] = self.var(name)
            params.append(self.syn.param(arg.type, name))
        function_type = ir.FunctionType(ir.TypeAttr(fn.operation.attributes["function_type"]).value)
        results = function_type.results
        ret = self.syn.type_name(results[0]) if results else None
        sym = self.safe(ir.StringAttr(fn.operation.attributes["sym_name"]).value)
        self.line(self.syn.function_header(ret, sym, params))
        self.depth = 1
        before = len(self.lines)
        if self.returning_tail(block) is not None:
            self.emit_returning(block, final=not results)
        else:
            self.emit_block(block)
            terminator = _ops(block)[-1]
            if terminator.operands:
                self.stmt(self.syn.return_stmt(self.expr(terminator.operands[0]).text))
        if len(self.lines) == before and self.syn.empty_block:
            self.line(self.syn.empty_block)
        self.depth = 0
        self.close()
        return "\n".join(self.lines)


def emit_module(module: ir.Module, syntax: Syntax) -> str:
    functions = [
        FunctionEmitter(op, syntax).emit()
        for op in module.body.operations
        if op.operation.name == "func.func"
    ]
    text = syntax.function_separator.join(functions) + "\n"
    header = syntax.prelude(text)
    return f"{header}\n{text}" if header else text
