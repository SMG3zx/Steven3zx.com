"""C++ frontend: tree-sitter-cpp -> neutral AST.

The C subset (see c.py) plus `static_cast<int|double>(x)`, `using` directives and `//`
comments. Refused with a line number: classes, templates, references, namespaces other than
`using namespace`, `std::` anything, `bool`, `auto`, lambdas, exceptions.
"""

import tree_sitter_cpp

from polyjuice import nast
from polyjuice.frontends import _ts
from polyjuice.frontends.c import CFrontend
from polyjuice.frontends._ts import field, named, text
from polyjuice.lower import lower_module
from polyjuice.mlir_env import ensure_mlir_importable

ensure_mlir_importable()

from mlir import ir  # noqa: E402

_PARSER = None


def _parser():
    global _PARSER
    if _PARSER is None:
        _PARSER = _ts.make_parser(tree_sitter_cpp.language())
    return _PARSER


class CppFrontend(CFrontend):
    skipped_top_level = ("preproc_include", "preproc_def", "using_declaration")

    def cast_parts(self, n):
        c_cast = super().cast_parts(n)
        if c_cast is not None:
            return c_cast
        if n.type == "call_expression":
            fn = field(n, "function")
            if fn.type == "template_function" and text(field(fn, "name")) == "static_cast":
                target = named(field(fn, "arguments"))[0]
                args = named(field(n, "arguments"))
                if len(args) == 1:
                    return self.scalar(field(target, "type")), args[0]
        return None


def parse(source: str) -> nast.Module:
    return CppFrontend(source, _parser()).module()


def build_module(source: str, context: ir.Context) -> ir.Module:
    return lower_module(parse(source), context)
