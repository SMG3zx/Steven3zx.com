# WASM Compilation Languages Research Report

**Date:** 2026-03-13  
**Context:** Janus uses wasmtime v42.0.1 for running WASM binaries. This report covers languages that compile to WASM/WASI, their toolchains, wasi-http support, and gaps in the current Janus build pipeline.

---

## 1. Languages That Compile to WASM with WASI Support

### Tier 1 — Mature, Production-Ready WASI Support

| Language | WASI Target | Toolchain | Binary Size | Notes |
|----------|-------------|-----------|-------------|-------|
| **Rust** | `wasm32-wasip1`, `wasm32-wasip2` | `rustc` + `cargo`, `cargo-component`, `wasm-pack` | Small (100KB–2MB) | Best-in-class WASI support. Primary language of the Bytecode Alliance ecosystem. |
| **C/C++** | `wasm32-wasi` | Emscripten (`emcc`), `clang --target=wasm32-wasi` with wasi-sdk | Small–Medium | Mature via LLVM/clang. wasi-sdk provides a sysroot. Emscripten adds POSIX emulation but larger output. |
| **Go (TinyGo)** | `wasip1`, `wasip2` | TinyGo v0.34+ | Small (500KB–3MB) | TinyGo supports `wasip2` target with WASI Preview 2 / Component Model. Produces much smaller binaries than standard Go. |
| **Go (std)** | `GOOS=wasip1 GOARCH=wasm` | Go 1.21+ | Large (5–20MB) | Standard Go compiler. Produces large binaries due to Go runtime. Only WASI Preview 1 (wasip1). No component model support. |

### Tier 2 — Functional WASI Support via Componentize Tools

| Language | WASI Target | Toolchain | Binary Size | Notes |
|----------|-------------|-----------|-------------|-------|
| **Python** | WASI P2 via `componentize-py` | `componentize-py` (Bytecode Alliance) | Large (10–30MB) | Embeds CPython interpreter in the component. Supports wasi-http. Suitable for non-performance-critical workloads. |
| **JavaScript/TypeScript** | WASI P2 via `jco` / `componentize-js` | `@bytecodealliance/jco`, `componentize-js` | Medium (5–15MB) | Embeds SpiderMonkey/StarlingMonkey JS engine. Supports wasi-http. Good for JS-native teams. |
| **C# / .NET** | WASI P2 via `componentize-dotnet` | `componentize-dotnet` NuGet package | Medium (5–15MB) | Bytecode Alliance project. Uses NativeAOT-LLVM to compile .NET to Wasm component. Supports wasi-http. |

### Tier 3 — Experimental / Emerging

| Language | WASI Target | Toolchain | Notes |
|----------|-------------|-----------|-------|
| **AssemblyScript** | `wasm32` (core spec) | `asc` compiler | TypeScript-like syntax. Compiles directly to Wasm. Limited WASI support — mainly wasi-preview-1 via shims. No native component model support. |
| **Zig** | `wasm32-wasi` | `zig build` | LLVM-based. Can target wasm32-wasi. Small binaries. Community-level WASI support, no official component model tooling yet. |
| **Swift** | `wasm32-wasi` | SwiftWasm | Experimental. SwiftWasm project provides wasi target. No component model tooling. |
| **Kotlin** | `wasm32-wasi` | Kotlin/Wasm (experimental) | Browser-focused primarily. WASI support is experimental. |
| **Ruby** | WASI P1 | `ruby.wasm` | Embeds CRuby interpreter. Very large binaries. Experimental. |

---

## 2. WASI-HTTP Support Matrix (for `wasmtime serve`)

`wasmtime serve` runs WASM components that implement the `wasi:http/proxy` world (specifically the `wasi:http/incoming-handler` interface). This is the key interface for serving HTTP directly via wasmtime.

| Language | wasi-http Support | Component Model | `wasmtime serve` Compatible | Toolchain |
|----------|-------------------|-----------------|----------------------------|-----------|
| **Rust** | ✅ Full (native) | ✅ Yes | ✅ Yes | `cargo-component` builds components targeting `wasi:http/proxy` world. Use `wit-bindgen` for bindings. |
| **Go (TinyGo)** | ✅ Yes (wasip2 target) | ✅ Yes (v0.34+) | ✅ Yes | TinyGo `wasip2` target. Can build components for `wasi:http/proxy` world. See tinygo-org/tinygo#4843 for non-cli worlds. |
| **Python** | ✅ Yes | ✅ Yes | ✅ Yes | `componentize-py` can target `wasi:http/proxy` world. |
| **JavaScript/TS** | ✅ Yes | ✅ Yes | ✅ Yes | `jco componentize` can target `wasi:http/proxy` world. |
| **C# / .NET** | ✅ Yes | ✅ Yes | ✅ Yes | `componentize-dotnet` supports wasi-http world. |
| **C/C++** | ⚠️ Manual | ⚠️ Via `wasm-tools compose` | ⚠️ Possible but complex | No native component model support. Must manually create component from core module using `wasm-tools component new` and compose with WASI adapters. |
| **AssemblyScript** | ❌ No | ❌ No | ❌ No | No component model support. Cannot implement `wasi:http/proxy`. |
| **Zig** | ⚠️ Manual | ⚠️ Via adapters | ⚠️ Possible but complex | Similar to C/C++ — requires manual component creation. |

---

## 3. Build Toolchain Requirements Per Language

### Rust (wasi-http component)
```dockerfile
FROM rust:1.82-bookworm
RUN rustup target add wasm32-wasip2
RUN cargo install cargo-component
WORKDIR /src
COPY . .
RUN cargo component build --release
# Output: target/wasm32-wasip2/release/*.wasm
```
- **Requires:** `rustc`, `cargo`, `cargo-component`, `wasm32-wasip2` target
- **Build command:** `cargo component build --target wasm32-wasip2 --release`
- **Output:** A WASM component with `wasi:http/proxy` exports

### Rust (wasip1 command — current Janus)
```dockerfile
FROM rust:1.82-bookworm
RUN rustup target add wasm32-wasip1
WORKDIR /src
COPY . .
RUN cargo build --target wasm32-wasip1 --release
# Output: target/wasm32-wasip1/release/*.wasm
```
- **Requires:** `rustc`, `cargo`, `wasm32-wasip1` target
- **Build command:** `cargo build --target wasm32-wasip1 --release`

### Go — Standard Compiler (wasip1 command — current Janus)
```dockerfile
FROM golang:1.24-bookworm
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -o app.wasm .
```
- **Requires:** Go 1.21+ 
- **Build command:** `GOOS=wasip1 GOARCH=wasm go build -o app.wasm .`

### Go — TinyGo (wasip2 component for wasi-http)
```dockerfile
FROM tinygo/tinygo:0.34.0
WORKDIR /src
COPY . .
RUN tinygo build -target=wasip2 -o app.wasm .
```
- **Requires:** TinyGo 0.34+
- **Build command:** `tinygo build -target=wasip2 -o app.wasm .`
- **Note:** Not all Go std library packages supported

### C/C++ (wasi-sdk, wasip1)
```dockerfile
FROM ghcr.io/aspect-build/wasi-sdk:wasi-sdk-24
WORKDIR /src
COPY . .
RUN /opt/wasi-sdk/bin/clang --sysroot=/opt/wasi-sdk/share/wasi-sysroot \
    -o app.wasm main.c
```
- **Requires:** wasi-sdk (clang + wasi sysroot) or Emscripten
- **Build command:** `clang --target=wasm32-wasi -o app.wasm main.c`

### Python (componentize-py, wasi-http)
```dockerfile
FROM python:3.12-slim
RUN pip install componentize-py
WORKDIR /src
COPY . .
RUN componentize-py -d wit -w wasi:http/proxy componentize app -o app.wasm
```
- **Requires:** `componentize-py` Python package
- **Build command:** `componentize-py -d wit -w wasi:http/proxy componentize app -o app.wasm`

### JavaScript/TypeScript (jco, wasi-http)
```dockerfile
FROM node:20-alpine
RUN npm install -g @bytecodealliance/jco @bytecodealliance/componentize-js
WORKDIR /src
COPY . .
RUN jco componentize app.js -w wasi:http/proxy -o app.wasm
```
- **Requires:** Node.js, `@bytecodealliance/jco`, `@bytecodealliance/componentize-js`
- **Build command:** `jco componentize app.js -w wasi:http/proxy -o app.wasm`

### C# / .NET (componentize-dotnet, wasi-http)
```dockerfile
FROM mcr.microsoft.com/dotnet/sdk:9.0
WORKDIR /src
COPY . .
RUN dotnet build -c Release
# componentize-dotnet NuGet package handles Wasm compilation in MSBuild
```
- **Requires:** .NET SDK 9.0+, `BytecodeAlliance.ComponentizeSharp` NuGet package
- **Build command:** `dotnet build` (MSBuild integration via NuGet)

---

## 4. How Janus Currently Handles WASM Builds

### Current Build Strategy Detection (`wasm_build_strategy.go`)
Janus detects the build strategy by checking files in the repository root:

1. **`prebuilt_wasm`** — If a `.wasm` file exists at `app.wasm`, `main.wasm`, `dist/app.wasm`, `build/app.wasm`, or any `.wasm` in root
2. **`go_wasip1`** — If `go.mod` exists → builds with `GOOS=wasip1 GOARCH=wasm go build`
3. **`rust_wasip1`** — If `Cargo.toml` exists → builds with `cargo build --target wasm32-wasip1 --release`
4. **`unknown`** — Falls through to error

### Current Build Execution (`build_executor.go`)
The `buildWasmArtifact()` function:
- Checks for prebuilt `.wasm` files first
- For Go: runs `go build` with `GOOS=wasip1 GOARCH=wasm` env vars directly (not containerized)
- For Rust: runs `cargo build --target wasm32-wasip1 --release` directly (not containerized)
- All builds produce **wasip1 command modules** (not components)
- Build happens directly on the host, not in Docker containers

### Current Runtime Detection (`wasm_runtime_detection.go`)
Janus detects runtime mode by scanning the WASM binary bytes for strings:
- If binary contains `wasi:http/proxy`, `wasi:http/incoming-handler`, or `wasi:http/types` → `wasm/wasi-http-component`
- Otherwise → `wasm/wasi-command`

### Current Runtime Launching (`runtime_launcher.go`)
Two execution modes:
1. **`wasm/wasi-http-component`** → `wasmtime serve --addr=<addr> -Shttp -Scli -Sinherit-env <artifact>` (native HTTP via wasi-http)
2. **`wasm/wasi-command`** → HTTP shim that wraps `wasmtime run` per-request, passing request info via env vars (`JANUS_REQUEST_METHOD`, `JANUS_REQUEST_PATH`, etc.) and reading stdout as response

### Current Dockerfile Resolution (`docker_file.go`)
The Dockerfile resolver is used for **container deployments** (not WASM builds):
- Checks for existing `Dockerfile` in repo root
- Generates fallback Dockerfiles for: Node.js, Python, Go, and generic
- Does NOT generate WASM build Dockerfiles

---

## 5. Gaps in Current Janus Build Pipeline

### Gap 1: Only Two Languages Supported
Currently only Go (std compiler) and Rust are supported for WASM builds. Missing:
- TinyGo (smaller Go binaries, wasip2 support)
- C/C++ via wasi-sdk
- Python via componentize-py
- JavaScript/TypeScript via jco
- C#/.NET via componentize-dotnet

### Gap 2: No WASI Preview 2 / Component Model Build Support
All current builds target **wasip1** (WASI Preview 1):
- Go: `GOOS=wasip1`
- Rust: `--target wasm32-wasip1`

There's no support for building **wasi-http components** (wasip2) from source. Users can only get wasi-http support via prebuilt `.wasm` artifacts.

### Gap 3: Builds Run on Host, Not in Containers
Current builds execute `go build` and `cargo build` directly on the runner host. This means:
- Runner must have Go and Rust toolchains pre-installed
- No isolation between builds
- No reproducibility guarantees
- Cannot easily add new language toolchains

### Gap 4: No Build Container / Dockerfile Generation for WASM
The `docker_file.go` generates Dockerfiles for container deployments, but there's no equivalent for WASM compilation. Container-based WASM builds would:
- Remove host toolchain requirements
- Enable more languages (each in its own build container)
- Provide build isolation and reproducibility

### Gap 5: No `cargo-component` Support for Rust
The current Rust build uses `cargo build --target wasm32-wasip1`, which produces a core WASM module. To produce wasi-http components, Rust needs `cargo-component` which targets `wasm32-wasip2`.

### Gap 6: Strategy Detection Doesn't Differentiate Component vs Command
The detection logic checks for `go.mod` or `Cargo.toml` but doesn't determine whether the project targets wasi-http (component) or wasi-command. A Rust project with `cargo-component` configuration should be built differently than a plain Rust project.

### Gap 7: No TinyGo Detection
There's no detection for projects that should use TinyGo instead of standard Go compiler. TinyGo projects might have different markers (e.g., `tinygo.yaml`, or specific build tags).

---

## 6. Recommended Language Support Matrix for Janus

### Priority 1 — Enhance Existing (Low Effort)
| Language | Strategy Name | Build Command | Runtime Mode |
|----------|--------------|---------------|--------------|
| Rust (wasip1) | `rust_wasip1` | `cargo build --target wasm32-wasip1 --release` | `wasm/wasi-command` |
| Rust (wasip2/component) | `rust_wasip2_component` | `cargo component build --release` | `wasm/wasi-http-component` |
| Go (wasip1) | `go_wasip1` | `GOOS=wasip1 GOARCH=wasm go build` | `wasm/wasi-command` |
| Prebuilt | `prebuilt_wasm` | N/A | Auto-detected |

**Detection:** Check for `[package.metadata.component]` or `[component]` in Cargo.toml → use `rust_wasip2_component`. Check for `cargo-component.toml` file.

### Priority 2 — New Language Support (Medium Effort)
| Language | Strategy Name | Build Command | Runtime Mode |
|----------|--------------|---------------|--------------|
| Go (TinyGo wasip2) | `tinygo_wasip2` | `tinygo build -target=wasip2 -o app.wasm .` | `wasm/wasi-http-component` |
| C/C++ (wasi-sdk) | `c_wasip1` | `clang --target=wasm32-wasi -o app.wasm` | `wasm/wasi-command` |

**Detection:** TinyGo — look for `tinygo.yaml` or `.tinygo` config, or `//go:build tinygo` directives. C/C++ — look for `Makefile` + `*.c`/`*.cpp` without `go.mod` or `Cargo.toml`.

### Priority 3 — Componentize-* Languages (Higher Effort)
| Language | Strategy Name | Build Command | Runtime Mode |
|----------|--------------|---------------|--------------|
| Python | `python_wasi_component` | `componentize-py -w wasi:http/proxy componentize` | `wasm/wasi-http-component` |
| JavaScript/TS | `js_wasi_component` | `jco componentize -w wasi:http/proxy` | `wasm/wasi-http-component` |
| C# / .NET | `dotnet_wasi_component` | `dotnet build` (with componentize NuGet) | `wasm/wasi-http-component` |

**Detection:** Python — `componentize.toml` or `pyproject.toml` with wasi config. JS — `package.json` with `@bytecodealliance/jco` dependency. C# — `.csproj` with `BytecodeAlliance.ComponentizeSharp` reference.

---

## 7. Build Container Requirements Per Language

| Strategy | Base Docker Image | Key Packages | Approx Image Size |
|----------|-------------------|--------------|-------------------|
| `rust_wasip1` | `rust:1.82-bookworm` | `wasm32-wasip1` target | ~1.5GB |
| `rust_wasip2_component` | `rust:1.82-bookworm` | `wasm32-wasip2` target, `cargo-component` | ~1.7GB |
| `go_wasip1` | `golang:1.24-bookworm` | (built-in wasip1 support) | ~1.1GB |
| `tinygo_wasip2` | `tinygo/tinygo:0.34.0` | (built-in wasip2 support) | ~1.2GB |
| `c_wasip1` | `ubuntu:24.04` or wasi-sdk image | `wasi-sdk` (clang + sysroot) | ~800MB |
| `python_wasi_component` | `python:3.12-slim` | `componentize-py` pip package | ~500MB |
| `js_wasi_component` | `node:20-alpine` | `@bytecodealliance/jco`, `componentize-js` | ~300MB |
| `dotnet_wasi_component` | `mcr.microsoft.com/dotnet/sdk:9.0` | `BytecodeAlliance.ComponentizeSharp` NuGet | ~1.5GB |

---

## 8. Key Findings Summary

1. **Rust** is the gold standard for WASM/WASI — best tooling, smallest binaries, native wasi-http component support via `cargo-component`.

2. **TinyGo** is the best Go option for WASM — smaller binaries than std Go, supports wasip2 and the component model. Standard Go only supports wasip1.

3. **Six languages** can produce `wasmtime serve`-compatible wasi-http components: Rust, Go (TinyGo), Python, JavaScript/TypeScript, C#/.NET, and (with manual work) C/C++.

4. **Janus currently only builds wasip1 command modules** — it can run wasi-http components if they're pre-built, but cannot build them from source.

5. **Container-based builds** are essential for supporting multiple languages without requiring all toolchains on every runner host.

6. **WASI Preview 2 (wasip2) / Component Model** is the standard going forward (2025-2026). WASI 0.3 (async) is previewing in wasmtime 37+. Janus should target wasip2 for new build strategies.

---

## Sources

- Janus codebase: `backend/janus-api/internal/core/` (wasm_build_strategy.go, wasm_runtime_detection.go, build_executor.go, runtime_launcher.go, docker_file.go)
- [WASI.dev](https://wasi.dev/) — WASI specification
- [Bytecode Alliance](https://bytecodealliance.org/) — wasmtime, cargo-component, componentize-py, componentize-dotnet, jco
- [TinyGo WASI docs](https://tinygo.org/docs/guides/webassembly/wasi/)
- [Component Model docs](https://component-model.bytecodealliance.org/)
- [wasmtime docs](https://docs.wasmtime.dev/)
- CalmOps: "WebAssembly WASI 2026: Server-Side Wasm Revolution" (2026-03-03)
- Reintech: "WebAssembly Ecosystem 2026: Tools, Frameworks & Runtimes" (2026-02-23)
- ProgGosling: "WASI 0.3 previews land in Wasmtime 37+" (2026-02-10)
