# PolyJuice design

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

## 2. Phase 0 decisions

Owner accepted the recommendations on 2026-11-07. Distribution name and packaging are explicitly not a concern.

**Accepted summary.** Targets: C first (EmitC), Python second (own emitter). Sources: Python subset first, C via ClangIR second. Detection: Python walkers. Dialect selection: static Python table. Runtime: none for v0; later shipped as source in the target language, written in C11. License: Apache-2.0. LLVM pinned to `llvmorg-23.1.5`. First work item: the build spike (section 6).

**Output for v0.** Source-to-source only (owner decision, 2026-11-07). Native executables are out of scope. EmitC gives C/C++ output; other target languages need our own emitters.
LLVM IR is used only as a verification stage; source is emitted at the highest level each target can express. See section 3.

**Dialect architecture.** One dialect per source language (`py`, and for C the upstream `cir`), lowering into a shared layer made of `func`, `arith`, `scf`, `memref`. A single universal high-level dialect stays an open research question: languages disagree on integer semantics (wrap vs trap vs UB), memory, and exceptions.
Trade-off: per-language dialects cost lowering code per language, but keep each frontend honest.

**Runtime.** Dynamic languages need an object model, GC, exceptions and dispatch linked into the output. Proposal: a small runtime in C11, built with CMake into a static library, shared across frontends, versioned with the compiler. Rust is the alternative (the monorepo already uses it) at the cost of a second toolchain in the build. Do not write it until the Python frontend needs it. v0 Python subset can avoid it by restricting to statically typed ints/floats/arrays.

**Definition of done for v0.** Translate a statically-typed Python subset program to C, and a C program to Python, such that the translated program passes the same tests as the original under differential testing.

**Non-goals for v0.** Binary decompilation. Full Python dynamism (`eval`, metaclasses, monkey-patching). Idiomatic-output guarantee for source-to-source. More than two frontends. Windows.

**Build and dependency policy.** Pinned to LLVM `llvmorg-23.1.5` (latest 23.1.x tag, verified 2026-11-07). Source build is likely required because CIR is off by default; prebuilt wheels cover only the Python bindings and may lack CIR. Target platform: Linux only (owner decision; x86_64 assumed). CMake + Ninja for C++, `uv` for Python.

**License.** Apache-2.0 (matches LLVM's Apache-2.0 with LLVM Exceptions and Pylir). No LICENSE file yet.

## 3. Pipeline (owner's direction, with the open tension marked)

```
source -> frontend -> language dialect
       -> dialect selection        (which dialects does this program need?)
       -> detection pipeline       (which domain patterns are present?)
       -> domain optimization      (passes enabled by what was detected)
       -> progressive lowering     (high-level -> mid-level -> ... -> LLVM dialect / LLVM IR)
       -> target-language emission
```

**Dialect selection.** The MLIR [dialect list](https://mlir.llvm.org/docs/Dialects/) (verified 2026-11-07) has 44 named dialects plus Builtin, SPIR-V, TOSA and Transform. Proposed buckets:

| Bucket | Dialects | Role |
|---|---|---|
| Core, always loaded | builtin, func, arith, math, scf, cf, memref, index, ub, complex | The shared layer |
| Data and domain | tensor, linalg, vector, affine, sparse_tensor, bufferization, shape, quant, tosa, ml_program | Targets of detection and domain optimization |
| Concurrency | async, omp, acc, gpu, mpi, shard | Only if the source or target needs parallelism |
| Hardware-specific | amdgpu, arm_neon, arm_sve, ArmSME, nvgpu, nvvm, rocdl, x86, xegpu, xevm, vcix, spirv, wasmssa | Out of scope for source-to-source v0 |
| Emission and lowering | emitc, llvm, ptr | Terminal stages |
| Infrastructure | irdl, pdl, pdl_interp, transform, dlti, smt | Defining dialects, rewriting, driving passes |

Selection is a function from the frontend output (ops used, types used, detected patterns) to a set of dialects to load and a pass pipeline to run. First version: a static table keyed on op and type usage, with the core bucket always loaded. Decided: a static Python table; a Transform-dialect script may replace it later.

**Detection pipeline.** Analyses that tag regions of IR with a recognised domain (for example a loop nest that is a matmul, a reduction, a stencil, or an elementwise map). Detection only annotates; it never rewrites. Optimization passes read the annotations. This keeps detectors independently testable. Candidate mechanisms: `linalg` generalization and raising, PDL/PDLL pattern matching, Transform-dialect matchers, or Python walkers. Decided: Python walkers first; move to PDL/PDLL or Transform matchers once patterns are stable or profiling demands it.

**Domain optimization.** Passes keyed on detections, for example raising a loop nest to `linalg.matmul`, then fusing or tiling. Each pass needs a verifier-clean before/after and a differential test.

**Lowering depth: per-target legality (decided 2026-11-07).** Source emission needs structured control flow, named types and loops. These exist at the structured shared layer (`scf`, `arith`, `memref`, `math`, `func`) and are gone by `cf` and the LLVM dialect, where emitting source is decompilation. So there is no single fixed emission depth. Each target declares which ops it can express directly (MLIR partial dialect conversion: legal vs illegal ops), and the pipeline lowers only the illegal ones, stopping at the first level the target understands.

| Level | Example dialects | Emitting source from here |
|---|---|---|
| 0. Language dialect | `py`, `cir` | Needs an emitter per source-and-target pair; defeats the shared layer. |
| 1. Domain / high-level | `linalg`, `tensor`, `vector`, `affine` | Most idiomatic, but only for targets that have the idiom. |
| 2. Structured shared layer | `scf`, `arith`, `memref`, `func` | Default emission level; maps to C, Rust, Python, Go. |
| 3. Unstructured | `cf` | Loops become blocks and branches; needs a relooper. Avoid. |
| 4. LLVM dialect / IR | `llvm` | Loops, structs and types are gone. Avoid. |

Examples: a C target accepts `scf` and `arith` but not `linalg.matmul`, so only `linalg` is lowered, to `scf` loops. A Python target with NumPy could keep `linalg.matmul` as `a @ b` and lower nothing.

LLVM IR is a verification stage, not an output: lower the program to it, run it, and compare with the original under differential testing to show each lowering step preserved behavior.

Unverified: how complete the `memref`/`scf`/`arith`/`func` to EmitC conversions are for C. Check in the spike.

## 4. Performance note

Most heavy compute already runs in C++ (MLIR and LLVM). Python-side hotspots will likely be parsing and AST walking. The 10x + CI gate in `CLAUDE.md` may rarely trigger.

## 5. Open questions

All Phase 0 questions are answered. Remaining unknowns are the unverified items in section 1 and the EmitC coverage in section 3, to be settled by the spike.

## 6. Spike: build and verify

Goal: settle the unverified items before writing compiler code. Scripts live in `scripts/`; build output lives outside the repo in `~/llvm-polyjuice/`.

1. Build LLVM `llvmorg-23.1.5` with clang (CIR enabled) and MLIR with Python bindings.
2. Does `mlir.dialects.ext` exist in this release?
3. Does CIR lower to `scf`/`memref`/`arith`, or only to the LLVM dialect?
4. How far do the EmitC conversions get a C-shaped `scf`/`arith`/`memref`/`func` program to C source?

Results are recorded in `docs/spike.md`.
