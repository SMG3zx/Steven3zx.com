"""Compatibility alias: the Python frontend lives in `polyjuice.frontends.python`."""

from polyjuice.frontends.python import build_module
from polyjuice.nast import UnsupportedSyntax

__all__ = ["UnsupportedSyntax", "build_module"]
