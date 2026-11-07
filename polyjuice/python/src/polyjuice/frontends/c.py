"""C frontend: tree-sitter-c -> neutral AST.

Supported: functions over `int`/`int32_t` and `double`, with `T name[N]` array parameters;
local scalars; `if/else`, `while`, `for`, early `return`; calls; arithmetic, comparisons,
`&& || !`, `(int)`/`(double)` casts. Refused with a line number: pointers, structs,
`long`/`float`/`char`, bit operators, `break`/`continue`, `switch`, `do`, ternary, strings,
local arrays.
"""

import tree_sitter_c

from polyjuice import nast
from polyjuice.frontends import _ts
from polyjuice.frontends.clike import CLike
from polyjuice.frontends._ts import field, line, named, text
from polyjuice.lower import lower_module
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.nast import FLOAT, INT, ArrayType, UnsupportedSyntax

ensure_mlir_importable()

from mlir import ir  # noqa: E402

_PARSER = None


def _parser():
    global _PARSER
    if _PARSER is None:
        _PARSER = _ts.make_parser(tree_sitter_c.language())
    return _PARSER


class CFrontend(CLike):
    int_types = {"int", "int32_t"}
    float_types = {"double"}
    skipped_top_level = ("preproc_include", "preproc_def")

    def scalar(self, node, allow_void: bool = False):
        name = text(node)
        if name in self.int_types:
            return INT
        if name in self.float_types:
            return FLOAT
        if name == "void" and allow_void:
            return None
        raise UnsupportedSyntax(line(node), f"unsupported type {name!r}")

    def module(self) -> nast.Module:
        functions = []
        for node in named(self.root):
            if node.type == "function_definition":
                functions.append(self.function(node))
            elif node.type in self.skipped_top_level or self.is_prototype(node):
                continue
            else:
                raise UnsupportedSyntax(line(node), f"unsupported top-level {node.type}")
        return nast.Module(functions)

    def is_prototype(self, node) -> bool:
        return node.type == "declaration" and any(c.type == "function_declarator" for c in node.children)

    def function(self, node) -> nast.Function:
        declarator = field(node, "declarator")
        if declarator.type != "function_declarator" or field(declarator, "declarator").type != "identifier":
            raise UnsupportedSyntax(line(node), "unsupported function declarator (pointers?)")
        params = []
        for p in named(field(declarator, "parameters")):
            if p.type != "parameter_declaration":
                raise UnsupportedSyntax(line(p), f"unsupported parameter {p.type}")
            if text(field(p, "type")) == "void" and field(p, "declarator") is None:
                continue  # f(void)
            params.append(self.param(p))
        ret = self.scalar(field(node, "type"), allow_void=True)
        return nast.Function(
            text(field(declarator, "declarator")), params, ret, self.block(field(node, "body")),
            line(node), static=True,
        )  # fmt: skip

    def param(self, p) -> nast.Param:
        elem = self.scalar(field(p, "type"))
        d = field(p, "declarator")
        if d is not None and d.type == "identifier":
            return nast.Param(text(d), elem)
        if d is not None and d.type == "array_declarator" and field(d, "declarator").type == "identifier":
            size = field(d, "size")
            if size is None or size.type != "number_literal":
                raise UnsupportedSyntax(line(p), "array parameters need a constant size")
            return nast.Param(text(field(d, "declarator")), ArrayType(elem, int(text(size), 0)))
        raise UnsupportedSyntax(line(p), "unsupported parameter declarator (pointer?)")

    def declaration(self, n) -> list:
        kind = self.scalar(field(n, "type"))
        out = []
        for d in n.children_by_field_name("declarator"):
            if d.type == "identifier":
                out.append(nast.Declare(text(d), kind, None, line(n)))
            elif d.type == "init_declarator" and field(d, "declarator").type == "identifier":
                out.append(nast.Declare(text(field(d, "declarator")), kind, self.expr(field(d, "value")), line(n)))
            else:
                raise UnsupportedSyntax(line(n), "unsupported declarator (local arrays and pointers are not supported)")
        return out

    def cast_parts(self, n):
        if n.type == "cast_expression":
            return self.scalar(field(field(n, "type"), "type")), field(n, "value")
        return None


def parse(source: str) -> nast.Module:
    return CFrontend(source, _parser()).module()


def build_module(source: str, context: ir.Context) -> ir.Module:
    return lower_module(parse(source), context)
