"""Registry of frontends (language -> shared layer) and emitters (shared layer -> language).

A pipeline is any (frontend, emitter) pair, so adding one frontend gives it every
existing target and adding one emitter gives every existing source a new target.
Each emitter declares which ops it can express; the dialect stage checks the IR
against that set before emitting.
"""

from collections.abc import Callable
from dataclasses import dataclass

from polyjuice.emit_c import LEGAL_OPS as C_LEGAL_OPS
from polyjuice.emit_c import emit_c
from polyjuice.emit_python import LEGAL_OPS as PY_LEGAL_OPS
from polyjuice.emit_python import emit_python
from polyjuice.frontends.c import build_module as build_c_module
from polyjuice.frontends.cpp import build_module as build_cpp_module
from polyjuice.frontends.csharp import build_module as build_csharp_module
from polyjuice.frontends.go import build_module as build_go_module
from polyjuice.frontends.java import build_module as build_java_module
from polyjuice.frontends.rust import build_module as build_rust_module
from polyjuice.frontends.javascript import build_module as build_javascript_module
from polyjuice.frontends.typescript import build_module as build_typescript_module
from polyjuice.frontends.kotlin import build_module as build_kotlin_module
from polyjuice.frontends.lua import build_module as build_lua_module
from polyjuice.frontends.php import build_module as build_php_module
from polyjuice.frontends.powershell import build_module as build_powershell_module
from polyjuice.frontends.python import build_module as build_python_module


@dataclass(frozen=True)
class Frontend:
    language: str
    extension: str
    build_module: Callable  # (source: str, context: ir.Context) -> ir.Module


@dataclass(frozen=True)
class Emitter:
    language: str
    extension: str
    legal_ops: frozenset
    emit: Callable  # (module: ir.Module) -> str


FRONTENDS: dict[str, Frontend] = {}
EMITTERS: dict[str, Emitter] = {}


def register_frontend(frontend: Frontend) -> None:
    FRONTENDS[frontend.language] = frontend


def register_emitter(emitter: Emitter) -> None:
    EMITTERS[emitter.language] = emitter


class UnsupportedPipeline(Exception):
    pass


def pipelines() -> list[tuple[str, str]]:
    return [(s, t) for s in sorted(FRONTENDS) for t in sorted(EMITTERS)]


def resolve(source: str, target: str) -> tuple[Frontend, Emitter]:
    if source not in FRONTENDS:
        raise UnsupportedPipeline(
            f"no frontend for {source!r}; have: {', '.join(sorted(FRONTENDS)) or 'none'}"
        )
    if target not in EMITTERS:
        raise UnsupportedPipeline(
            f"no emitter for {target!r}; have: {', '.join(sorted(EMITTERS)) or 'none'}"
        )
    return FRONTENDS[source], EMITTERS[target]


def language_for_path(path: str) -> str | None:
    suffix = path.rsplit(".", 1)[-1] if "." in path else ""
    return next((f.language for f in FRONTENDS.values() if f.extension == suffix), None)


register_frontend(Frontend("python", "py", build_python_module))
register_frontend(Frontend("c", "c", build_c_module))
register_frontend(Frontend("cpp", "cpp", build_cpp_module))
register_frontend(Frontend("go", "go", build_go_module))
register_frontend(Frontend("java", "java", build_java_module))
register_frontend(Frontend("kotlin", "kt", build_kotlin_module))
register_frontend(Frontend("lua", "lua", build_lua_module))
register_frontend(Frontend("php", "php", build_php_module))
register_frontend(Frontend("powershell", "ps1", build_powershell_module))
register_frontend(Frontend("javascript", "js", build_javascript_module))
register_frontend(Frontend("typescript", "ts", build_typescript_module))
register_frontend(Frontend("rust", "rs", build_rust_module))
register_frontend(Frontend("csharp", "cs", build_csharp_module))
register_emitter(Emitter("c", "c", C_LEGAL_OPS, emit_c))
register_emitter(Emitter("python", "py", PY_LEGAL_OPS, emit_python))
