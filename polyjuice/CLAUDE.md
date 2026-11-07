# PolyJuice working rules

Source-to-source compiler on MLIR and LLVM. Read `DESIGN.md` before changing structure. Part of the Steven3zx.com monorepo; root `AGENTS.md` rules (commits, remotes) apply.

Linux only. Source-to-source only; no native-executable backend for now.

## Scope discipline

- "Any language" is a vision, not a design driver. Do not design for it until two or three frontends work end to end.
- Ask before any decision that is expensive to reverse (dialect design, LLVM pin, runtime language, license).
- Cite sources for survey findings and flag anything you could not verify.

## Language and FFI policy

- Python for orchestration, CLI, test harness, and frontend prototyping.
- MLIR's core API is C++, so C++ in the compiler core is expected. First evaluate whether the MLIR Python bindings suffice before writing any C++ wrapper.
- Extra C++ beyond what MLIR requires is allowed only for hotspots proven by profiling.
- Use an explicit FFI layer (pybind11 or nanobind) and measure its overhead.

## Performance rule

A C++ rewrite of a Python function must pass BOTH gates:

- Practical gate: at least 10x median speedup on the function, AND a meaningful end-to-end gain. Account for Amdahl's law: a 10x speedup on 5% of runtime is not enough.
- Statistical gate: at least 30 runs, confidence intervals that do not overlap with the baseline, FFI and marshalling cost included.

Commit the benchmark harness before the optimized implementation. Report mean, standard deviation, and confidence interval, never a single run.

## Correctness rules

- Differential testing: run each test program in its original language and in the compiled output, then compare results.
- Every IR stage has a verifier and text round-trip tests (parse, print, reparse) checked with FileCheck.
- Each new frontend lowers into the shared layer, so adding a language never requires a new backend.
- Source-to-source output is emitted from a high-level MLIR stage (structured control flow), never from LLVM IR.

## Pipeline rules

- Every pipeline stage writes a numbered artifact file (and appears in `manifest.json`) so each stage can be studied and improved on its own. A new stage is not done until it emits its file.
- Output must read like hand-written code in the target language. Emitters use the user's names, mutable variables, and inline temporaries only when that cannot change behavior.
- A construct the frontend cannot carry correctly must raise `UnsupportedSyntax`, never be dropped silently.

