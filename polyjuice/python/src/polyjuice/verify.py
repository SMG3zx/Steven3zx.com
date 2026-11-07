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


def execute(llvm_module: ir.Module, name: str, args: list, result=ctypes.c_int32):
    """Call `name` in the lowered module.

    Ints and floats are passed by value, lists as memrefs (float64 if any element is a
    float, else int32). Pass `result=None` for functions that return nothing.
    Returns (value, arrays) so callers can inspect in-place array writes.
    """
    engine = ExecutionEngine(llvm_module)
    arrays = []
    c_args = []
    for arg in args:
        if isinstance(arg, list):
            dtype = np.float64 if any(isinstance(x, float) for x in arg) else np.int32
            array = np.array(arg, dtype=dtype)
            arrays.append(array)
            c_args.append(ctypes.pointer(ctypes.pointer(get_ranked_memref_descriptor(array))))
        elif isinstance(arg, float):
            c_args.append(ctypes.pointer(ctypes.c_double(arg)))
        else:
            c_args.append(ctypes.pointer(ctypes.c_int32(arg)))
    if result is None:
        engine.invoke(name, *c_args)
        return None, arrays
    out = result(0)
    engine.invoke(name, *c_args, ctypes.pointer(out))
    return out.value, arrays
