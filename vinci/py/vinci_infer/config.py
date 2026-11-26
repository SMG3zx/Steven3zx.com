"""Settings and the llama-server command line for the vinci inference service.

The service runs two llama-server processes and one front door:

- chat: a GGUF model with every layer on the GPU and a fixed context
- embed: an embedding model on the CPU (the GPU stays free for chat)
- the front door (proxy.py) listens on one port and sends each request to the process
  named by the request's "model" field

Every setting can be changed in the environment; see load_settings().
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path

VINCI_ROOT: Path = Path(__file__).resolve().parents[2]

DEFAULT_LLAMA_SERVER: str = str(Path.home() / ".unsloth" / "llama.cpp" / "llama-server")
DEFAULT_CHAT_GGUF: str = str(
    Path.home()
    / ".cache/huggingface/hub/models--unsloth--Qwen3.5-9B-GGUF/snapshots"
    / "3885219b6810b007914f3a7950a8d1b469d598a5/Qwen3.5-9B-UD-Q4_K_XL.gguf"
)
DEFAULT_EMBED_GGUF: str = str(VINCI_ROOT / "models" / "Qwen3-Embedding-4B.gguf")

CHAT_ALIAS: str = "qwen3.5-9b"
EMBED_ALIAS: str = "qwen3-embedding:4b"

# All layers on the GPU. llama-server accepts any count at or above the layer count.
ALL_GPU_LAYERS: int = 999


@dataclass(frozen=True)
class ServerSpec:
    """One llama-server process: which model, where it listens, and how it runs."""

    alias: str
    model_path: str
    port: int
    ctx: int
    parallel: int
    gpu_layers: int
    embedding: bool = False
    extra: tuple[str, ...] = field(default=())


@dataclass(frozen=True)
class Settings:
    """Everything the service needs at start: the binary, both specs, and the front door."""

    binary: str
    host: str
    front_port: int
    chat: ServerSpec
    embed: ServerSpec | None
    state_dir: Path
    startup_timeout_s: float


def build_argv(binary: str, spec: ServerSpec, host: str) -> list[str]:
    """The llama-server command line for one spec. The order is fixed so the logs stay readable."""
    argv: list[str] = [
        binary,
        "-m", spec.model_path,
        "--alias", spec.alias,
        "--host", host,
        "--port", str(spec.port),
        "-c", str(spec.ctx),
        "-np", str(spec.parallel),
        "-ngl", str(spec.gpu_layers),
        "--no-context-shift",
        "--jinja",
    ]
    if spec.gpu_layers > 0:
        argv += ["--flash-attn", "on"]
    if spec.embedding:
        argv.append("--embeddings")
        argv += ["-dev", "none"]
    argv += list(spec.extra)
    return argv


def _int_env(env: dict[str, str], name: str, default: int, minimum: int = 1) -> int:
    raw = env.get(name, "").strip()
    if raw == "":
        return default
    value = int(raw)
    if value < minimum:
        raise ValueError(f"{name} must be at least {minimum}, got {raw!r}")
    return value


def load_settings(env: dict[str, str] | None = None) -> Settings:
    """Read the settings from the environment, with the defaults this machine uses.

    VINCI_INFER_BIN        llama-server binary (default: the unsloth build)
    VINCI_INFER_HOST       bind address for every process (default 127.0.0.1)
    VINCI_INFER_PORT       the front door (default 8080, the port vinci chat already calls)
    VINCI_INFER_HOME       state folder for logs (default ~/.vinci/infer)
    VINCI_INFER_STARTUP_S  seconds to wait for a model to load (default 300)
    VINCI_CHAT_GGUF        chat model file; VINCI_CHAT_ALIAS its name (default qwen3.5-9b)
    VINCI_CHAT_CTX         chat context in tokens for the whole server (default 16384)
    VINCI_CHAT_PARALLEL    simultaneous chat requests (default 1); each gets CTX / PARALLEL
    VINCI_CHAT_PORT        chat process port (default 8180)
    VINCI_CHAT_GPU_LAYERS  layers on the GPU (default: all)
    VINCI_EMBED_GGUF       embedding model file; set to "" to run without embeddings
    VINCI_EMBED_ALIAS      its name (default qwen3-embedding:4b)
    VINCI_EMBED_CTX        embedding context (default 8192)
    VINCI_EMBED_PORT       embedding process port (default 8181)
    """
    e: dict[str, str] = dict(os.environ) if env is None else dict(env)
    host = e.get("VINCI_INFER_HOST", "127.0.0.1").strip() or "127.0.0.1"
    state_dir = Path(e.get("VINCI_INFER_HOME", str(Path.home() / ".vinci" / "infer")))

    chat_ctx = _int_env(e, "VINCI_CHAT_CTX", 16384)
    chat_parallel = _int_env(e, "VINCI_CHAT_PARALLEL", 1)
    chat = ServerSpec(
        alias=e.get("VINCI_CHAT_ALIAS", CHAT_ALIAS),
        model_path=e.get("VINCI_CHAT_GGUF", DEFAULT_CHAT_GGUF),
        port=_int_env(e, "VINCI_CHAT_PORT", 8180),
        ctx=chat_ctx,
        parallel=chat_parallel,
        gpu_layers=_int_env(e, "VINCI_CHAT_GPU_LAYERS", ALL_GPU_LAYERS),
    )

    embed_path = e.get("VINCI_EMBED_GGUF", DEFAULT_EMBED_GGUF)
    embed: ServerSpec | None = None
    if embed_path != "":
        embed = ServerSpec(
            alias=e.get("VINCI_EMBED_ALIAS", EMBED_ALIAS),
            model_path=embed_path,
            port=_int_env(e, "VINCI_EMBED_PORT", 8181),
            ctx=_int_env(e, "VINCI_EMBED_CTX", 8192),
            parallel=1,
            gpu_layers=0,
            embedding=True,
        )

    return Settings(
        binary=e.get("VINCI_INFER_BIN", DEFAULT_LLAMA_SERVER),
        host=host,
        front_port=_int_env(e, "VINCI_INFER_PORT", 8080, minimum=0),
        chat=chat,
        embed=embed,
        state_dir=state_dir,
        startup_timeout_s=float(_int_env(e, "VINCI_INFER_STARTUP_S", 300)),
    )
