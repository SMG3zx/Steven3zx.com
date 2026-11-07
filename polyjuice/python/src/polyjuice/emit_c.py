"""Idiomatic C emitter: the C syntax table for the shared structured emitter.

Known gap: C signed overflow is undefined, while the source subset defines
`int` as wrapping 32-bit. Build the output with `-fwrapv` if that matters.
"""

from polyjuice.emit_structured import LEGAL_OPS, Syntax, UnsupportedForTarget, emit_module
from polyjuice.mlir_env import ensure_mlir_importable

ensure_mlir_importable()

from mlir import ir  # noqa: E402

UnsupportedForC = UnsupportedForTarget

C_RESERVED = frozenset(
    """auto break case char const continue default do double else enum extern float for goto if
    inline int long register restrict return short signed sizeof static struct switch typedef
    union unsigned void volatile while bool true false NULL int32_t size_t ptrdiff_t""".split()
)


def _type(t: ir.Type) -> str:
    text = str(t)
    if text == "i32":
        return "int32_t"
    if text == "i1":
        return "bool"
    if text == "index":
        return "ptrdiff_t"
    raise UnsupportedForC(f"no C type for {text}")


def _param(t: ir.Type, name: str) -> str:
    if str(t).startswith("memref<"):
        memref_type = ir.MemRefType(t)
        dims = "".join(f"[{d}]" for d in memref_type.shape)
        return f"{_type(memref_type.element_type)} {name}{dims}"
    return f"{_type(t)} {name}"


def _declare(type_text: str, name: str, init: str | None) -> str:
    return f"{type_text} {name}" if init is None else f"{type_text} {name} = {init}"


def _prelude(body: str) -> str:
    includes = ["<stdint.h>"]
    if "bool " in body:
        includes.append("<stdbool.h>")
    if "ptrdiff_t" in body:
        includes.append("<stddef.h>")
    return "".join(f"#include {inc}\n" for inc in sorted(includes))


def _for_header(iv: str, lower: str, upper: str, step: str) -> str:
    increment = f"{iv}++" if step == "1" else f"{iv} += {step}"
    return f"for (int32_t {iv} = {lower}; {iv} < {upper}; {increment}) {{"


C_SYNTAX = Syntax(
    language="c",
    reserved=C_RESERVED,
    type_name=_type,
    param=_param,
    function_header=lambda ret, name, params: f"{ret or 'void'} {name}({', '.join(params) or 'void'}) {{",
    declare=_declare,
    for_header=_for_header,
    if_header=lambda keyword, cond: f"{keyword} ({cond}) {{",
    if_keyword="if",
    elif_keyword="} else if",
    else_header="} else {",
    close="}",
    terminator=";",
    ternary=lambda cond, a, b: f"{cond} ? {a} : {b}",
    range_evaluated_once=False,
    tuple_assign=False,
    increment=True,
    empty_block=None,
    function_separator="\n\n",
    prelude=_prelude,
    return_stmt=lambda value: f"return {value}" if value is not None else "return",
    cast=lambda type_text, operand: f"({type_text}){operand}",
)


def emit_c(module: ir.Module) -> str:
    return emit_module(module, C_SYNTAX)


__all__ = ["LEGAL_OPS", "UnsupportedForC", "emit_c"]
