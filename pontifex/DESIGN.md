# Pontifex design

Status: Phase 0 proposal. Nothing here is decided until the owner signs off. Survey date: 2026-11-07.

## 1. Prior-art survey

Latest LLVM release is 23.1.0 (2026-08-25), per [llvm.org schedule via Phoronix](https://www.phoronix.com/news/LLVM-23.1-Released). Flang docs already track "24 (in-progress)".

| Project | Covers | Status (verified) | Reuse |
|---|---|---|---|
| ClangIR (CIR) | C/C++ to MLIR | Upstreamed into llvm-project; incubator archived early 2026. Not self-hosting yet, off by default in builds, needs `-fclangir`. Gaps: C++ EH/cleanups, ABI lowering. [Apr 2026 report](https://discourse.llvm.org/t/clangir-upstream-progress-report-april-2026/90562), [May 2026 report](https://discourse.llvm.org/t/clangir-upstream-progress-report-may-2026/90760), [Phoronix](https://phoronix.com/news/ClangIR-Build-Default-Maybe) | High for C. Replaces writing a C frontend. Requires building LLVM from source with CIR enabled. |
| Flang FIR/HLFIR | Fortran to MLIR | In-tree, active. HLFIR is the only lowering path (legacy array-value ops removed). [Flang 24 notes](https://flang.llvm.org/docs/ReleaseNotes.html) | Frontend is reusable, but FIR is Fortran-shaped; lowering into our shared layer is real work. Not a first-three language. |
| Pylir | Python AOT compiler on MLIR | Last commit 2024-06-09 (GitHub API). README: "very much work in progress", Python 3.9 parsing, basic GC and EH. Apache-2.0. [repo](https://github.com/Pylir/Pylir) | Reference reading only (object model, runtime). Stale, tracks MLIR tip-of-tree. Do not depend on it. |
| Polygeist | C/C++ to MLIR (affine/scf) | Last commit 2024-07-31, last push 2025-06-19 (GitHub API). Pinned to an old llvm-project commit. [repo](https://github.com/llvm/Polygeist) | Superseded by ClangIR for our purposes. Useful as a design reference for emitting `scf`/`memref` from C. |
| torch-mlir | PyTorch to MLIR | Active (commits on 2026-11-06). LLVM incubator, not in any LLVM release. [repo](https://github.com/llvm/torch-mlir) | ML-specific. Not a general-language frontend. Useful as a model of an out-of-tree dialect plus Python packaging. |
| EmitC | MLIR to C/C++ source | In MLIR core. `mlir-translate --mlir-to-cpp`. Output is C99 unless multi-return or `call_opaque` needs C++11. [docs](https://mlir.llvm.org/docs/Dialects/EmitC/) | High for C/C++ as an output language. Other output languages need our own emitters. |
| MLIR Python bindings | Build/inspect/transform IR from Python | Python-defined dialects exist: IRDL bindings ([#158488](https://github.com/llvm/llvm-project/pull/158488)) and a DSL (`mlir.dialects.ext`, merged Jan 2026, [#169045](https://github.com/llvm/llvm-project/pull/169045), docs [#181372](https://github.com/llvm/llvm-project/pull/181372)). Wheels: `makslevental/mlir-wheels` archived 2025-11-29, moved to [llvm/eudsl](https://github.com/llvm/eudsl) (`pip install mlir-python-bindings -f https://llvm.github.io/eudsl`, "alpha"). | See below. |

### Unverified (do not rely on these yet)

- Whether `mlir.dialects.ext` is present in a given LLVM 23.1.x release or only in main.
- Whether eudsl wheels are pinned to release tags or only nightlies.
- Whether CIR has an upstream lowering to core dialects (scf/memref/arith), versus only to LLVM dialect. This decides whether C can feed the shared layer.
- Exact LLVM pins of torch-mlir and Polygeist, and Pylir's last working LLVM revision.
- What the Python bindings cannot do: expected gaps are custom passes and custom types/attributes with C++-level behavior. IRDL dialects are declarative; anything needing C++ verifiers or folders needs C++.

### Recommended first languages

1. **Python subset** (own frontend). Parse with the stdlib `ast` module, emit MLIR from Python through the bindings. Pylir is stale, so nothing replaces this. Python-only work, no C++ needed to start.
2. **C** via ClangIR. Avoids writing a C parser/type checker. Risk: depends on the unverified CIR lowering question above. Fallback: Polygeist as a reference or a much smaller C subset.
3. **Optional third, decide later:** Fortran via Flang (largest reuse, hardest to fit the shared layer) or a tiny toy language to stress the shared layer cheaply.

## 2. Phase 0 decisions (proposed)

**Output for v0.** Native via LLVM first. It is the shortest path and exercises the whole pipeline. Source-to-source is a separate backend, added second, emitting from the high-level stage (after the shared layer, before lowering to `cf`/LLVM). EmitC gives C/C++ output for free; other targets need our own emitters.
Trade-off: doing both at once doubles verification work before any frontend works.

**Dialect architecture.** One dialect per source language (`py`, and for C the upstream `cir`), lowering into a shared layer made of `func`, `arith`, `scf`, `memref`. A single universal high-level dialect stays an open research question: languages disagree on integer semantics (wrap vs trap vs UB), memory, and exceptions.
Trade-off: per-language dialects cost lowering code per language, but keep each frontend honest.

**Runtime.** Dynamic languages need an object model, GC, exceptions and dispatch linked into the output. Proposal: a small runtime in C11, built with CMake into a static library, shared across frontends, versioned with the compiler. Rust is the alternative (the monorepo already uses it) at the cost of a second toolchain in the build. Do not write it until the Python frontend needs it. v0 Python subset can avoid it by restricting to statically typed ints/floats/arrays.

**Definition of done for v0.** Compile a statically-typed Python subset program and a C program to native executables that pass the same tests as the originals under differential testing.

**Non-goals for v0.** Binary decompilation. Full Python dynamism (`eval`, metaclasses, monkey-patching). Idiomatic-output guarantee for source-to-source. More than two frontends. Windows.

**Build and dependency policy.** Pin one LLVM/MLIR release, proposed 23.1.x. Source build is likely required because CIR is off by default; prebuilt wheels cover only the Python bindings and may lack CIR. Target platform: Linux x86_64 only for v0 (macOS/arm64 later). CMake + Ninja for C++, `uv` for Python.

**License.** Proposal: Apache-2.0 (matches LLVM's Apache-2.0 with LLVM Exceptions and Pylir). Not committed; no LICENSE file yet.

## 3. Performance note

Most heavy compute already runs in C++ (MLIR and LLVM). Python-side hotspots will likely be parsing and AST walking. The 10x + CI gate in `CLAUDE.md` may rarely trigger.

## 4. Open questions

See the report that accompanied this commit; they are repeated here once answered.
