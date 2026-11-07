"""Java frontend: tree-sitter-java -> neutral AST.

Supported: `static` methods over `int` and `double` in any number of classes; locals,
`if/else`, `while`, `for`, early `return`, calls to other static methods by simple name,
`(int)`/`(double)` casts, `&& || !`. Arrays are unsized in Java and the shared layer needs a
fixed size, so array parameters are refused. Also refused: instance members, objects,
`long`/`float`/`char`/`boolean`, strings, `break`/`continue`, `switch`, ternary, `Math.*`.
"""

import tree_sitter_java

from polyjuice import nast
from polyjuice.frontends import _ts
from polyjuice.frontends._ts import field, line, named, operator, text
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
        _PARSER = _ts.make_parser(tree_sitter_java.language())
    return _PARSER


class JavaFrontend(CLike):
    number_types = {"decimal_integer_literal", "decimal_floating_point_literal", "hex_integer_literal"}
    index_types = set()  # arrays are unsized in Java
    unary_operand_field = "operand"
    init_fields, update_fields = ("init",), ("update",)

    def scalar(self, node, allow_void: bool = False):
        name = text(node)
        if name == "int":
            return INT
        if name == "double":
            return FLOAT
        if name == "void" and allow_void:
            return None
        if node.type == "array_type":
            raise UnsupportedSyntax(line(node), "Java arrays are unsized; the shared layer needs a fixed size")
        raise UnsupportedSyntax(line(node), f"unsupported type {name!r}")

    def module(self) -> nast.Module:
        functions = []
        for node in named(self.root):
            if node.type in ("package_declaration", "import_declaration"):
                continue
            if node.type != "class_declaration":
                raise UnsupportedSyntax(line(node), f"unsupported top-level {node.type}")
            for member in named(field(node, "body")):
                if member.type != "method_declaration":
                    raise UnsupportedSyntax(line(member), f"unsupported class member {member.type}")
                functions.append(self.method(member))
        return nast.Module(functions)

    def method(self, node) -> nast.Function:
        modifiers = next((c for c in named(node) if c.type == "modifiers"), None)
        if modifiers is None or "static" not in text(modifiers).split():
            raise UnsupportedSyntax(line(node), "only static methods are supported")
        params = []
        for p in named(field(node, "parameters")):
            if p.type != "formal_parameter":
                raise UnsupportedSyntax(line(p), f"unsupported parameter {p.type}")
            params.append(nast.Param(text(field(p, "name")), self.scalar(field(p, "type"))))
        body = field(node, "body")
        return nast.Function(
            text(field(node, "name")), params, self.scalar(field(node, "type"), allow_void=True),
            self.block(body), line(node), static=True,
        )  # fmt: skip

    def declaration(self, n) -> list:
        kind_node = field(n, "type")
        kind = None if text(kind_node) == "var" else self.scalar(kind_node)
        out = []
        for d in n.children_by_field_name("declarator"):
            value = field(d, "value")
            if field(d, "dimensions") is not None:
                raise UnsupportedSyntax(line(n), "arrays are not supported")
            if value is None and kind is None:
                raise UnsupportedSyntax(line(n), "`var` needs an initializer")
            out.append(nast.Declare(text(field(d, "name")), kind, self.expr(value) if value is not None else None, line(n)))
        return out

    def call_parts(self, n):
        if field(n, "object") is not None:
            raise UnsupportedSyntax(line(n), "only calls to static methods by simple name are supported")
        return field(n, "name"), named(field(n, "arguments"))

    def cast_parts(self, n):
        if n.type == "cast_expression":
            return self.scalar(field(n, "type")), field(n, "value")
        return None

    def update_parts(self, n):
        ops = [text(c) for c in n.children if not c.is_named]
        return named(n)[0], "++" if "++" in ops else "--"


def parse(source: str) -> nast.Module:
    return JavaFrontend(source, _parser()).module()


def build_module(source: str, context: ir.Context) -> ir.Module:
    return lower_module(parse(source), context)
