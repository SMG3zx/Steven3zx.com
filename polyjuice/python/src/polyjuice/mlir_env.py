"""Locate the locally built MLIR Python bindings and tools (see scripts/setup-llvm.sh)."""

import os
import sys
from pathlib import Path

ROOT = Path(os.environ.get("POLYJUICE_LLVM_ROOT", Path.home() / "llvm-polyjuice"))
PYTHON_PACKAGES = Path(
    os.environ.get(
        "POLYJUICE_MLIR_PYTHON",
        ROOT / "build/tools/mlir/python_packages/mlir_core",
    )
)
MLIR_TRANSLATE = Path(
    os.environ.get("POLYJUICE_MLIR_TRANSLATE", ROOT / "build/bin/mlir-translate")
)


def ensure_mlir_importable() -> None:
    if str(PYTHON_PACKAGES) not in sys.path:
        sys.path.insert(0, str(PYTHON_PACKAGES))
