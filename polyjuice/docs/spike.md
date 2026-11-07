# Spike results: LLVM 23.1.5 build and verification

Date: 2026-11-07. Build: `scripts/setup-llvm.sh` (Release + assertions, clang+mlir, `CLANG_ENABLE_CIR=ON`, Python bindings, Python 3.14). Output in `~/llvm-polyjuice/build`.

## Results

| Question | Result |
|---|---|
| Build succeeds on this machine? | Yes. 6448/6448 steps, no failures. clang 23.1.5, `mlir-opt`, `mlir-translate`, `cir-opt`, `cir-translate`, `cir-lsp-server`, FileCheck. |
| Python dialect DSL in 23.1.5? | Present: `mlir/dialects/ext.py` and `irdl.py` ship in the built package. `ext` imports on Python 3.14, and `class D(ext.Dialect, name="pyd")` plus a `D.Operation` subclass is accepted. **Not yet exercised:** defining ops with operands/results, loading the dialect, building and verifying IR. |
| Does `clang -fclangir -emit-cir` work on C? | Yes. A C loop becomes structured `cir.for` / `cir.scope` ops, not branches. |
| Does CIR lower to `scf`/`memref`/`arith`/`func`? | **No, not in 23.1.5.** `clang/lib/CIR/Lowering` contains only `DirectToLLVM`; no CIR-to-MLIR-core lowering exists, and `clang` has no `-emit-mlir` flag. |
| Do the EmitC conversions produce C? | Yes, with caveats below. |

## EmitC findings

`mlir-opt --convert-to-emitc` followed by `mlir-translate --mlir-to-cpp` turned an `scf.for` + `arith` + `func` + static-`memref` program into readable C:

```c
int32_t sum(int32_t v1[8], size_t v2) {
  ...
  for (size_t i7 = v3; i7 < v2; i7 += v4) {
    int32_t v9 = v1[i7];
    uint32_t v10 = (uint32_t) v8;  // signed add goes through unsigned casts
    ...
```

Caveats:
- A **dynamic** `memref<?xi32>` argument made `func.func` fail to legalize when I ran the per-dialect passes (`--convert-scf-to-emitc --convert-arith-to-emitc --convert-func-to-emitc [--convert-memref-to-emitc]`). I did not test dynamic memrefs under `--convert-to-emitc`.
- Running the per-dialect passes one by one left `unrealized_conversion_cast` ops that `mlir-translate` could not print. `--convert-to-emitc` resolved these in the static case.
- `arith.addi` is emitted through `uint32_t` casts to get defined wraparound. Correct, but not idiomatic output.
- `index` becomes `size_t`.

## Consequences for the design

1. **The C frontend is not free.** ClangIR gives structured IR, but we must write the CIR-to-shared-layer lowering ourselves. That lowering is a C++ MLIR pass inside the clang tree's dialects, because the CIR dialect is not part of the MLIR Python modules (expected, not tested). CIR also keeps locals in `alloca`/`load`/`store`, so producing `scf.for` with `iter_args` needs promotion of locals to SSA values.
2. Alternatives for C: a restricted C subset frontend written in Python (for example over `pycparser` or libclang), or stale Polygeist as a reference. Decision needed.
3. **Python subset to C is viable now.** It needs only the MLIR Python bindings and EmitC. Do this path first.
4. Dynamic memrefs need investigation before they can be a source-language feature.

## First end-to-end slice (Python subset to C)

`polyjuice/python` compiles the Python subset to `func`/`arith`/`scf`/`memref`, lowers with `convert-to-emitc`, and prints C with `mlir-translate`. 20 differential tests pass (compiled C vs the original Python).

Findings while building it:
- **`range()` with a negative bound was a real bug.** EmitC loops use unsigned `size_t`, so `i32 -> index` turned `range(-3)` into about 2^64 iterations. Fixed by iterating `k` in `[0, max(hi - lo, 0))` and binding `i = lo + k`. `test_negative_bound_runs_zero_iterations` guards it.
- **EmitC has no conversion for `arith.maxsi`.** Full `canonicalize` rewrites `select(x > 0, x, 0)` into `maxsi`, which `mlir-translate` then cannot print. The pipeline uses `canonicalize{enable-patterns=false}`, which still removes dead ops and runs folds.
- Output needs `<stdint.h>`, `<stddef.h>` and `<stdbool.h>`; the pipeline prepends them.
- `int` is 32-bit with wraparound, not Python's unbounded integer. `//` and `%` are rejected because Python floors and C truncates. Step in `range()` is rejected for now.
