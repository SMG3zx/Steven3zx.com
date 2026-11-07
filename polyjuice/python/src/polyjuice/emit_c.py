"""Idiomatic C emitter: the C syntax table for the shared structured emitter.

Known gap: C signed overflow is undefined, while the source subset defines
`int` as wrapping 32-bit. Build the output with `-fwrapv` if that matters.
"""

from polyjuice.emit_structured import (
    LEGAL_OPS, MUL, PRIMARY, UNARY, Expr, Syntax, UnsupportedForTarget, binary_expr, emit_module,
)  # fmt: skip
from polyjuice.mlir_env import ensure_mlir_importable

ensure_mlir_importable()

from mlir import ir  # noqa: E402

UnsupportedForC = UnsupportedForTarget

C_KEYWORDS = """auto break case char const continue default do double else enum extern float
    for goto if inline int long register restrict return short signed sizeof static struct
    switch typedef union unsigned void volatile while bool true false NULL int32_t size_t
    ptrdiff_t"""

# Standard-library names a user function must not shadow: compilers treat many as
# builtins, and the rest collide at link time.
C_LIBRARY = """abs labs llabs fabs fabsf floor ceil round trunc rint fmod fmin fmax sqrt cbrt pow exp
    exp2 expm1 log log2 log10 log1p sin cos tan asin acos atan atan2 sinh cosh tanh hypot erf tgamma
    printf fprintf sprintf snprintf scanf puts putchar getchar fopen fclose fread fwrite fgets fputs
    malloc calloc realloc free exit abort atexit atoi atol atof strtol strtod rand srand qsort bsearch
    memcpy memmove memset memcmp strlen strcpy strncpy strcat strcmp strncmp strchr strstr main
    time clock signal raise assert errno stdin stdout stderr"""

C_RESERVED = frozenset(C_KEYWORDS.split()) | frozenset(C_LIBRARY.split())


def _type(t: ir.Type) -> str:
    text = str(t)
    if text == "i32":
        return "int32_t"
    if text == "f64":
        return "double"
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
    if "bool " in body or " true" in body or " false" in body or "(true" in body or "(false" in body:
        includes.append("<stdbool.h>")
    if "fmod(" in body:
        includes.append("<math.h>")
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
    while_header=lambda cond: f"while ({cond}) {{",
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
    not_expr=lambda a: Expr(f"!({a.text})" if a.prec < UNARY else f"!{a.text}", UNARY, a.reads, effects=a.effects),
    logic_symbols=("&&", "||"),
    bool_text=lambda value: "true" if value else "false",
    float_rem=lambda a, b: Expr(f"fmod({a.text}, {b.text})", PRIMARY, a.reads | b.reads, effects=a.effects or b.effects),
    int_div=lambda a, b: binary_expr("/", MUL, a, b),
    int_rem=lambda a, b: binary_expr("%", MUL, a, b),
)


def emit_c(module: ir.Module) -> str:
    return emit_module(module, C_SYNTAX)


__all__ = ["LEGAL_OPS", "UnsupportedForC", "emit_c"]
