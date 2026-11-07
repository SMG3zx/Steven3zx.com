"""Verification path: lower the shared layer to LLVM and run it.

LLVM IR is not an output of PolyJuice. It is how we check the IR: the same
program is lowered to the LLVM dialect, translated to LLVM IR, and executed by
MLIR's ExecutionEngine, then compared against the original source. That
catches mistakes in the frontend and in any lowering step, independent of the
source emitters.
"""

import ctypes
import subprocess

from polyjuice.mlir_env import MLIR_TRANSLATE, ensure_mlir_importable

ensure_mlir_importable()

import numpy as np  # noqa: E402
from mlir import ir, passmanager  # noqa: E402
from mlir.execution_engine import ExecutionEngine  # noqa: E402
from mlir.runtime import get_ranked_memref_descriptor  # noqa: E402

LLVM_PIPELINE = (
    "builtin.module(convert-scf-to-cf,convert-cf-to-llvm,convert-arith-to-llvm,"
    "finalize-memref-to-llvm,convert-func-to-llvm,reconcile-unrealized-casts)"
)


def lower_to_llvm_dialect(module: ir.Module) -> ir.Module:
    """Return a lowered copy; the input module is left untouched."""
    copy = ir.Module.parse(str(module), context=module.context)
    for op in copy.body.operations:
        if op.operation.name == "func.func":
            op.operation.attributes["llvm.emit_c_interface"] = ir.UnitAttr.get(module.context)
    passmanager.PassManager.parse(LLVM_PIPELINE, module.context).run(copy.operation)
    return copy


def to_llvm_ir(llvm_module: ir.Module) -> str:
    result = subprocess.run(
        [str(MLIR_TRANSLATE), "--mlir-to-llvmir"],
        input=str(llvm_module),
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        raise RuntimeError(f"mlir-translate failed:\n{result.stderr}")
    return result.stdout


def execute(llvm_module: ir.Module, name: str, args: list, returns: bool = True):
    """Call `name` in the lowered module. Ints are passed by value, lists as i32 memrefs."""
    engine = ExecutionEngine(llvm_module)
    keep = []
    c_args = []
    for arg in args:
        if isinstance(arg, list):
            array = np.array(arg, dtype=np.int32)
            keep.append(array)
            c_args.append(ctypes.pointer(ctypes.pointer(get_ranked_memref_descriptor(array))))
        else:
            c_args.append(ctypes.pointer(ctypes.c_int32(arg)))
    if not returns:
        engine.invoke(name, *c_args)
        return None, keep
    result = ctypes.c_int32(0)
    engine.invoke(name, *c_args, ctypes.pointer(result))
    return result.value, keep
