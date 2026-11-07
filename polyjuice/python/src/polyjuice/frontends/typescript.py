"""TypeScript frontend: JavaScript plus `number` / `void` annotations.

Only `number` and `void` types are accepted (a `number` is an `f64`, as in JavaScript); other
annotations are refused. Everything else follows javascript.py.
"""

import tree_sitter_typescript

from polyjuice import nast
from polyjuice.frontends import _ts
from polyjuice.frontends._ts import field, line, named, text
from polyjuice.frontends.javascript import JavaScriptFrontend
from polyjuice.lower import lower_module
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.nast import FLOAT, UnsupportedSyntax

ensure_mlir_importable()

from mlir import ir  # noqa: E402

_PARSER = None


def _parser():
    global _PARSER
    if _PARSER is None:
        _PARSER = _ts.make_parser(tree_sitter_typescript.language_typescript())
    return _PARSER


class TypeScriptFrontend(JavaScriptFrontend):
    def annotation(self, node, allow_void: bool = False):
        inner = named(node)[0]
        name = text(inner)
        if name == "number":
            return FLOAT
        if name == "void" and allow_void:
            return None
        raise UnsupportedSyntax(line(node), f"unsupported type {name!r}")

    def parameter(self, p) -> nast.Param:
        if p.type != "required_parameter" or field(p, "pattern").type != "identifier":
            raise UnsupportedSyntax(line(p), f"unsupported parameter {p.type}")
        annotation = field(p, "type")
        if annotation is None:
            raise UnsupportedSyntax(line(p), "parameters need a type annotation")
        return nast.Param(text(field(p, "pattern")), self.annotation(annotation))

    def return_type(self, node, body):
        annotation = field(node, "return_type")
        if annotation is None:
            return super().return_type(node, body)
        return self.annotation(annotation, allow_void=True)

    def check_declarator_type(self, d) -> None:
        annotation = field(d, "type")
        if annotation is not None:
            self.annotation(annotation)


def parse(source: str) -> nast.Module:
    return TypeScriptFrontend(source, _parser()).module()


def build_module(source: str, context: ir.Context) -> ir.Module:
    return lower_module(parse(source), context)
