"""Language-neutral AST: what every frontend produces and `lower.py` consumes.

Deliberately small: it is the subset the shared layer can express today
(ints, floats, fixed-size arrays, loops, ifs, calls). A frontend maps its
language's syntax onto these nodes or raises `UnsupportedSyntax` with a line
number; it never guesses.

Conventions frontends must follow:
- `x += e`, `x++` and friends are desugared to `Assign(Name(x), Binary("+", Name(x), e))`.
- A counted loop is `For(var, start, stop, body)` over [start, stop) with step 1. Anything
  else (other steps, conditions that change) becomes `While`, preceded by its initializer.
- Names are function-scoped. Shadowing an existing name in a nested block is refused.
"""

from dataclasses import dataclass, field

INT = "int"  # 32-bit signed, wraps
FLOAT = "float"  # 64-bit IEEE double


class UnsupportedSyntax(Exception):
    """A construct the shared layer cannot carry correctly yet."""

    def __init__(self, where, message: str):
        line = where if isinstance(where, int) else getattr(where, "line", getattr(where, "lineno", None))
        super().__init__(f"line {line if line is not None else '?'}: {message}")


@dataclass(frozen=True)
class ArrayType:
    elem: str  # INT or FLOAT
    size: int


# -- expressions ----------------------------------------------------------------------------
@dataclass
class Const:
    value: int | float
    line: int = 0


@dataclass
class Name:
    id: str
    line: int = 0


@dataclass
class Binary:
    op: str  # + - * /
    left: object
    right: object
    line: int = 0


@dataclass
class Unary:
    op: str  # -
    operand: object
    line: int = 0


@dataclass
class Compare:
    op: str  # < <= > >= == !=
    left: object
    right: object
    line: int = 0


@dataclass
class Index:
    array: str
    index: object
    line: int = 0


@dataclass
class Call:
    func: str
    args: list
    line: int = 0


@dataclass
class Cast:
    to: str  # INT or FLOAT
    operand: object
    line: int = 0


# -- statements -----------------------------------------------------------------------------
@dataclass
class Assign:
    target: object  # Name or Index
    value: object
    line: int = 0


@dataclass
class ParallelAssign:
    """`a, b = b, a + b`: every right-hand side is read before any name is bound."""

    targets: list  # Names
    values: list
    line: int = 0


@dataclass
class For:
    var: str
    start: object | None  # None means 0
    stop: object
    body: list
    line: int = 0


@dataclass
class While:
    cond: object
    body: list
    line: int = 0


@dataclass
class If:
    cond: object
    then: list
    orelse: list = field(default_factory=list)
    line: int = 0


@dataclass
class Return:
    value: object | None = None
    line: int = 0


@dataclass
class ExprStmt:
    call: Call
    line: int = 0


@dataclass
class Param:
    name: str
    type: object  # INT, FLOAT or ArrayType


@dataclass
class Function:
    name: str
    params: list
    ret: str | None  # INT, FLOAT or None
    body: list
    line: int = 0


@dataclass
class Module:
    functions: list


def assigned_names(stmts) -> set[str]:
    """Names assigned anywhere in `stmts`, including nested blocks and loop variables."""
    names: set[str] = set()
    for st in stmts:
        if isinstance(st, Assign) and isinstance(st.target, Name):
            names.add(st.target.id)
        elif isinstance(st, ParallelAssign):
            names.update(t.id for t in st.targets)
        elif isinstance(st, For):
            names.add(st.var)
            names |= assigned_names(st.body)
        elif isinstance(st, While):
            names |= assigned_names(st.body)
        elif isinstance(st, If):
            names |= assigned_names(st.then) | assigned_names(st.orelse)
    return names
