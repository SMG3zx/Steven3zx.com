#!/usr/bin/env bash
# Build LLVM/MLIR/Clang (with ClangIR) and the MLIR Python bindings for PolyJuice.
# Output lives outside the repo in $POLYJUICE_LLVM_ROOT (default ~/llvm-polyjuice).
set -euo pipefail

TAG=llvmorg-23.1.5
ROOT=${POLYJUICE_LLVM_ROOT:-$HOME/llvm-polyjuice}
JOBS=${JOBS:-$(nproc)}

mkdir -p "$ROOT"
cd "$ROOT"

if [ ! -d venv ]; then
  python3 -m venv venv
  venv/bin/pip install --quiet cmake ninja nanobind numpy pybind11 PyYAML lit filecheck
fi
export PATH="$ROOT/venv/bin:$PATH"

if [ ! -d llvm-project ]; then
  git clone --depth 1 --branch "$TAG" https://github.com/llvm/llvm-project.git
fi

cmake -G Ninja -S llvm-project/llvm -B build \
  -DCMAKE_BUILD_TYPE=Release \
  -DLLVM_ENABLE_ASSERTIONS=ON \
  -DLLVM_ENABLE_PROJECTS="clang;mlir" \
  -DLLVM_TARGETS_TO_BUILD="X86" \
  -DCLANG_ENABLE_CIR=ON \
  -DMLIR_ENABLE_BINDINGS_PYTHON=ON \
  -DPython3_EXECUTABLE="$ROOT/venv/bin/python" \
  -DLLVM_INSTALL_UTILS=ON \
  -DCMAKE_C_COMPILER=clang -DCMAKE_CXX_COMPILER=clang++ \
  -DLLVM_USE_LINKER=lld \
  -DLLVM_PARALLEL_LINK_JOBS=3 \
  -DCMAKE_INSTALL_PREFIX="$ROOT/install"

ninja -C build -j"$JOBS" clang mlir-opt mlir-translate mlir-python-bindings FileCheck not count
