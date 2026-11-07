"""Source -> MLIR -> C. Dialect selection and detection are not wired in yet."""

import subprocess

from polyjuice.mlir_env import MLIR_TRANSLATE, ensure_mlir_importable

ensure_mlir_importable()

from mlir import ir, passmanager  # noqa: E402
from mlir.dialects import arith, emitc, func, memref, scf  # noqa: E402

from polyjuice.frontend import build_module  # noqa: E402

C_HEADER = "#include <stdint.h>\n#include <stddef.h>\n#include <stdbool.h>\n\n"


def _context() -> ir.Context:
    context = ir.Context()
    context.allow_unregistered_dialects = False
    return context


def lower_to_emitc(module: ir.Module) -> None:
    pm = passmanager.PassManager.parse(
        "builtin.module(canonicalize{enable-patterns=false},convert-to-emitc)", module.context
    )
    pm.run(module.operation)
    module.operation.verify()


def compile_to_c(source: str) -> str:
    context = _context()
    module = build_module(source, context)
    with context:
        lower_to_emitc(module)
        text = str(module)
    result = subprocess.run(
        [str(MLIR_TRANSLATE), "--mlir-to-cpp"],
        input=text,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        raise RuntimeError(f"mlir-translate failed:\n{result.stderr}\n--- IR ---\n{text}")
    return C_HEADER + result.stdout
