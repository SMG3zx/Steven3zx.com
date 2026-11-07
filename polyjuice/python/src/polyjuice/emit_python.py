"""Idiomatic Python emitter: the Python syntax table for the shared structured emitter.

Known gap: Python integers do not wrap at 32 bits, while the shared layer's
`i32` does. Results agree as long as values stay in range.
"""

import builtins
import keyword

from polyjuice.emit_structured import LEGAL_OPS, PRIMARY, Expr, Syntax, UnsupportedForTarget, emit_module
from polyjuice.mlir_env import ensure_mlir_importable

ensure_mlir_importable()

from mlir import ir  # noqa: E402

# Shadowing `range`, `int`, `list`, ... would break the code we emit.
PY_RESERVED = frozenset(keyword.kwlist) | frozenset(dir(builtins)) | {"math"}


def _type(t: ir.Type) -> str:
    text = str(t)
    if text in ("i32", "index"):
        return "int"
    if text == "f64":
        return "float"
    if text == "i1":
        return "bool"
    raise UnsupportedForTarget(f"no Python type for {text}")


def _param(t: ir.Type, name: str) -> str:
    if str(t).startswith("memref<"):
        return f"{name}: list[{_type(ir.MemRefType(t).element_type)}]"
    return f"{name}: {_type(t)}"


def _declare(type_text: str, name: str, init: str | None) -> str | None:
    return None if init is None else f"{name} = {init}"


def _for_header(iv: str, lower: str, upper: str, step: str) -> str:
    args = [upper] if lower == "0" and step == "1" else [lower, upper]
    if step != "1":
        args.append(step)
    return f"for {iv} in range({', '.join(args)}):"


def _function_header(ret: str | None, name: str, params: list[str]) -> str:
    return f"def {name}({', '.join(params)})" + (f" -> {ret}:" if ret else ":")


def _int_div(a: Expr, b: Expr) -> Expr:
    # Python's // floors; C-style division truncates toward zero.
    return Expr(f"int({a.text} / {b.text})", PRIMARY, a.reads | b.reads, effects=a.effects or b.effects)


def _int_rem(a: Expr, b: Expr) -> Expr:
    return Expr(f"int(math.fmod({a.text}, {b.text}))", PRIMARY, a.reads | b.reads, effects=a.effects or b.effects)


def _prelude(body: str) -> str:
    return "import math\n\n" if "math.fmod(" in body else ""


PY_SYNTAX = Syntax(
    language="python",
    reserved=PY_RESERVED,
    type_name=_type,
    param=_param,
    function_header=_function_header,
    declare=_declare,
    for_header=_for_header,
    if_header=lambda kw, cond: f"{kw} {cond}:",
    while_header=lambda cond: f"while {cond}:",
    if_keyword="if",
    elif_keyword="elif",
    else_header="else:",
    close=None,
    terminator="",
    ternary=lambda cond, a, b: f"{a} if {cond} else {b}",
    range_evaluated_once=True,
    tuple_assign=True,
    increment=False,
    empty_block="pass",
    function_separator="\n\n\n",
    prelude=_prelude,
    return_stmt=lambda value: f"return {value}" if value is not None else "return",
    cast=lambda type_text, operand: f"{type_text}({operand})",
    not_expr=lambda a: Expr(f"not ({a.text})" if a.prec <= 45 else f"not {a.text}", 45, a.reads, effects=a.effects),
    logic_symbols=("and", "or"),
    bool_text=lambda value: "True" if value else "False",
    float_rem=lambda a, b: Expr(f"math.fmod({a.text}, {b.text})", PRIMARY, a.reads | b.reads, effects=a.effects or b.effects),
    int_div=_int_div,
    int_rem=_int_rem,
)


def emit_python(module: ir.Module) -> str:
    return emit_module(module, PY_SYNTAX)


__all__ = ["LEGAL_OPS", "emit_python"]
