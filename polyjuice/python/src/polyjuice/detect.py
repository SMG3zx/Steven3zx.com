"""Detection: tag regions of IR with the domain pattern they implement.

Detectors only annotate (`polyjuice.pattern` attributes); they never rewrite.
Optimization passes read the tags. Each detector declares the ops it needs so the
selection stage can skip detectors that cannot match.
"""

from collections.abc import Callable
from dataclasses import dataclass

from polyjuice.mlir_env import ensure_mlir_importable

ensure_mlir_importable()

from mlir import ir  # noqa: E402

_REDUCTION_OPS = {"arith.addi": "add", "arith.muli": "mul", "arith.subi": "sub"}


def walk_ops(block):
    for op in block.operations:
        yield op
        for region in op.regions:
            for inner in region.blocks:
                yield from walk_ops(inner)


def _depends_on(value: ir.Value, target: ir.Value, seen: set | None = None) -> bool:
    if value == target:
        return True
    if not isinstance(value, ir.OpResult):
        return False
    seen = seen if seen is not None else set()
    op = value.owner
    if op.operation in seen:
        return False
    seen.add(op.operation)
    return any(_depends_on(v, target, seen) for v in op.operands)


def _is_iv_index(value: ir.Value, iv: ir.Value) -> bool:
    return (
        isinstance(value, ir.OpResult)
        and value.owner.operation.name == "arith.index_cast"
        and value.owner.operands[0] == iv
    )


def detect_reduction(loop) -> dict | None:
    """A loop whose every carried value is updated as `acc = acc <op> f(other)`."""
    body = loop.regions[0].blocks[0]
    iv, *iter_args = list(body.arguments)
    if not iter_args:
        return None
    yielded = list(list(body.operations)[-1].operands)
    kinds = []
    for arg, value in zip(iter_args, yielded):
        if not isinstance(value, ir.OpResult):
            return None
        op = value.owner
        kind = _REDUCTION_OPS.get(op.operation.name)
        lhs, rhs = op.operands if kind else (None, None)
        if kind is None or lhs != arg or _depends_on(rhs, arg):
            return None
        kinds.append(kind)
    loop.operation.attributes["polyjuice.pattern"] = ir.StringAttr.get("reduction")
    loop.operation.attributes["polyjuice.reduction_ops"] = ir.ArrayAttr.get(
        [ir.StringAttr.get(k) for k in kinds]
    )
    return {"pattern": "reduction", "ops": kinds}


def detect_elementwise_map(loop) -> dict | None:
    """A loop with no carried values that only stores to `a[i]` at its own index."""
    body = loop.regions[0].blocks[0]
    iv, *iter_args = list(body.arguments)
    if iter_args:
        return None
    ops = list(walk_ops(body))
    stores = [op for op in ops if op.operation.name == "memref.store"]
    if not stores or any(o.operation.name in ("scf.for", "scf.if") for o in ops):
        return None
    if not all(len(s.operands) == 3 and _is_iv_index(s.operands[2], iv) for s in stores):
        return None
    loop.operation.attributes["polyjuice.pattern"] = ir.StringAttr.get("elementwise_map")
    return {"pattern": "elementwise_map", "stores": len(stores)}


@dataclass(frozen=True)
class Detector:
    name: str
    requires: frozenset  # op names that must be present for the detector to be worth running
    run: Callable


DETECTORS = [
    Detector("reduction", frozenset({"scf.for"}), detect_reduction),
    Detector("elementwise_map", frozenset({"scf.for", "memref.store"}), detect_elementwise_map),
]


def detect(module: ir.Module, detectors: list[Detector]) -> list[dict]:
    findings = []
    for fn in module.body.operations:
        if fn.operation.name != "func.func":
            continue
        name = ir.StringAttr(fn.operation.attributes["sym_name"]).value
        for op in walk_ops(fn.regions[0].blocks[0]):
            if op.operation.name != "scf.for" or "polyjuice.pattern" in op.attributes:
                continue
            for detector in detectors:
                found = detector.run(op)
                if found:
                    findings.append({"function": name, "op": "scf.for", **found})
                    break
    return findings
