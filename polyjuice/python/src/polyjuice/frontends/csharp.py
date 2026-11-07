"""C# frontend: tree-sitter-c-sharp -> neutral AST.

Supported: `static` methods over `int` and `double` (classes, static classes, namespaces);
locals including `var`; `if/else`, `while`, `for`, early `return`; calls by simple name;
`(int)`/`(double)` casts; `&& || !`. C# `int` arithmetic wraps (unchecked), like the shared
layer. Arrays are unsized and refused. Also refused: instance members, objects, `long`/
`float`/`decimal`/`bool`/`char`, strings, `break`/`continue`, `switch`, ternary, `Math.*`.
"""

import tree_sitter_c_sharp

from polyjuice import nast
from polyjuice.frontends import _ts
from polyjuice.frontends._ts import field, line, named, text
from polyjuice.frontends.clike import CLike
from polyjuice.lower import lower_module
from polyjuice.mlir_env import ensure_mlir_importable
from polyjuice.nast import FLOAT, INT, UnsupportedSyntax

ensure_mlir_importable()

from mlir import ir  # noqa: E402

_PARSER = None


def _parser():
    global _PARSER
    if _PARSER is None:
        _PARSER = _ts.make_parser(tree_sitter_c_sharp.language())
    return _PARSER


class CSharpFrontend(CLike):
    number_types = {"integer_literal", "real_literal"}
    index_types = set()
    init_fields, update_fields = ("initializer",), ("update",)

    def scalar(self, node, allow_void: bool = False):
        name = text(node)
        if name == "int":
            return INT
        if name == "double":
            return FLOAT
        if name == "void" and allow_void:
            return None
        if node.type == "array_type":
            raise UnsupportedSyntax(line(node), "C# arrays are unsized; the shared layer needs a fixed size")
        raise UnsupportedSyntax(line(node), f"unsupported type {name!r}")

    def module(self) -> nast.Module:
        functions: list = []
        self.members(self.root, functions)
        return nast.Module(functions)

    def members(self, container, functions: list) -> None:
        for node in named(container):
            if node.type == "using_directive":
                continue
            if node.type in ("namespace_declaration", "file_scoped_namespace_declaration"):
                body = field(node, "body")
                self.members(body if body is not None else node, functions)
            elif node.type == "class_declaration":
                for member in named(field(node, "body")):
                    if member.type != "method_declaration":
                        raise UnsupportedSyntax(line(member), f"unsupported class member {member.type}")
                    functions.append(self.method(member))
            elif node.type in ("identifier", "qualified_name"):
                continue  # the namespace's own name
            else:
                raise UnsupportedSyntax(line(node), f"unsupported top-level {node.type}")

    def method(self, node) -> nast.Function:
        modifiers = [text(c) for c in node.children if c.type == "modifier"]
        if "static" not in modifiers:
            raise UnsupportedSyntax(line(node), "only static methods are supported")
        params = []
        for p in named(field(node, "parameters")):
            if p.type != "parameter":
                raise UnsupportedSyntax(line(p), f"unsupported parameter {p.type}")
            params.append(nast.Param(text(field(p, "name")), self.scalar(field(p, "type"))))
        return nast.Function(
            text(field(node, "name")), params, self.scalar(field(node, "returns"), allow_void=True),
            self.block(field(node, "body")), line(node), static=True,
        )  # fmt: skip

    def declaration(self, n) -> list:
        decl = n if n.type == "variable_declaration" else next(c for c in named(n) if c.type == "variable_declaration")
        kind_node = field(decl, "type")
        kind = None if kind_node.type == "implicit_type" else self.scalar(kind_node)
        out = []
        for d in named(decl):
            if d.type != "variable_declarator":
                continue
            name = field(d, "name")
            rest = [c for c in named(d) if c != name]
            if rest and rest[0].type not in ("bracketed_argument_list", "equals_value_clause"):
                value = self.expr(rest[0])
            elif rest and rest[0].type == "equals_value_clause":
                value = self.expr(named(rest[0])[0])
            else:
                value = None
            if value is None and kind is None:
                raise UnsupportedSyntax(line(n), "`var` needs an initializer")
            out.append(nast.Declare(text(name), kind, value, line(n)))
        return out

    def call_parts(self, n):
        fn = field(n, "function")
        if fn is None or fn.type != "identifier":
            raise UnsupportedSyntax(line(n), "only calls to static methods by simple name are supported")
        args = [named(a)[0] for a in named(field(n, "arguments"))]
        return fn, args

    def cast_parts(self, n):
        if n.type == "cast_expression":
            return self.scalar(field(n, "type")), field(n, "value")
        return None

    def unary_parts(self, n):
        op = next(text(c) for c in n.children if not c.is_named)
        return op, named(n)[0]

    def update_parts(self, n):
        op = next(text(c) for c in n.children if not c.is_named)
        return named(n)[0], op


def parse(source: str) -> nast.Module:
    return CSharpFrontend(source, _parser()).module()


def build_module(source: str, context: ir.Context) -> ir.Module:
    return lower_module(parse(source), context)
