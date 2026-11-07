"""The emitted C should read like hand-written C, not like compiler output."""

import json
import textwrap

import pytest

from polyjuice import compile_to_c
from polyjuice.frontend import UnsupportedSyntax
from polyjuice.pipeline import run


def c_of(source: str) -> str:
    return compile_to_c(textwrap.dedent(source))


def test_array_sum_reads_like_c():
    assert c_of(
        """
        def total(a: Array[int, 8], n: int) -> int:
            s = 0
            for i in range(n):
                s += a[i]
            return s
        """
    ) == textwrap.dedent(
        """\
        #include <stdint.h>

        int32_t total(int32_t a[8], int32_t n) {
            int32_t s = 0;
            for (int32_t i = 0; i < n; i++) {
                s += a[i];
            }
            return s;
        }
        """
    )


def test_elif_chain_stays_an_else_if_chain():
    out = c_of(
        """
        def clamp(x: int, lo: int, hi: int) -> int:
            r = x
            if x < lo:
                r = lo
            elif x > hi:
                r = hi
            return r
        """
    )
    assert "} else if (x > hi) {" in out
    assert out.count("else") == 2  # else if, final else


def test_if_without_else_initialises_then_assigns():
    out = c_of(
        """
        def relu(x: int) -> int:
            r = x
            if x < 0:
                r = 0
            return r
        """
    )
    assert "int32_t r = x;" in out
    assert "else" not in out


def test_no_compiler_temporaries_in_simple_code():
    out = c_of(
        """
        def f(a: Array[int, 4], n: int) -> int:
            s = 0
            for i in range(1, n):
                s += a[i] * 2 - a[i - 1]
            return s
        """
    )
    assert "s += a[i] * 2 - a[i - 1];" in out
    assert "(uint32_t)" not in out and "size_t" not in out
    assert " t0" not in out


def test_unary_minus_and_parentheses():
    out = c_of("def f(x: int, y: int) -> int:\n    return -(x + y) * 2\n")
    assert "return -(x + y) * 2;" in out


def test_parallel_assignment_uses_temporaries_only_when_needed():
    swap = c_of(
        """
        def fib(n: int) -> int:
            a = 0
            b = 1
            for i in range(n):
                a, b = b, a + b
            return a
        """
    )
    assert "next_a" in swap and "next_b" in swap
    independent = c_of(
        """
        def g(n: int) -> int:
            a = 0
            b = 0
            for i in range(n):
                a, b = a + 1, b + 2
            return a + b
        """
    )
    assert "next_" not in independent
    assert "a++;" in independent and "b += 2;" in independent


def test_each_stage_writes_a_file(tmp_path):
    result = run("def f(x: int) -> int:\n    return x + 1\n", "c", tmp_path)
    expected = {
        "00-source.py", "01-ast.txt", "02-frontend.mlir", "03-cleaned.mlir",
        "04-selection.json", "05-detected.mlir", "06-optimized.mlir", "07-legalized.mlir",
        "08-output.c", "manifest.json",
    }  # fmt: skip
    assert {p.name for p in tmp_path.iterdir()} == expected
    manifest = json.loads((tmp_path / "manifest.json").read_text())
    assert [s["file"] for s in manifest["stages"]] == sorted(expected - {"manifest.json"})
    assert (tmp_path / "08-output.c").read_text() == result.output
    report = json.loads((tmp_path / "04-selection.json").read_text())
    assert report["dialects_used"] == ["arith", "func"]
    assert "polyjuice.name" not in (tmp_path / "08-output.c").read_text()


def test_python_names_survive_into_the_ir(tmp_path):
    run("def f(n: int) -> int:\n    s = 0\n    for i in range(n):\n        s += i\n    return s\n", "c", tmp_path)
    ir_text = (tmp_path / "02-frontend.mlir").read_text()
    assert 'polyjuice.iv = "i"' in ir_text and 'polyjuice.carried = ["s"]' in ir_text


def test_update_that_cannot_be_carried_is_an_error_not_a_silent_drop():
    # `n` is a function argument assigned in the loop: it is carried, so this must work.
    compile_to_c("def f(n: int) -> int:\n    for i in range(3):\n        n += 1\n    return n\n")
    # Reassigning inside a branch of a loop is also carried; an array name is not a scalar.
    with pytest.raises(UnsupportedSyntax):
        compile_to_c(
            "def f(a: Array[int, 4], b: Array[int, 4]) -> int:\n"
            "    for i in range(3):\n        a = b\n    return 0\n"
        )


@pytest.mark.parametrize(
    "artifact", ["02-frontend.mlir", "03-cleaned.mlir", "05-detected.mlir", "06-optimized.mlir", "07-legalized.mlir"]
)
def test_ir_stages_round_trip(tmp_path, artifact):
    """parse -> print -> reparse is stable for every IR stage (see CLAUDE.md)."""
    from polyjuice.mlir_env import ensure_mlir_importable

    ensure_mlir_importable()
    from mlir import ir

    source = (
        "def f(a: Array[int, 4], n: int) -> int:\n    s = 0\n    for i in range(n):\n"
        "        if a[i] > 0:\n            s += a[i]\n    return s\n"
    )
    text = run(source, "c", tmp_path).artifacts[artifact]
    with ir.Context():
        printed = str(ir.Module.parse(text))
        assert printed == text
        assert str(ir.Module.parse(printed)) == printed


def test_unknown_pipeline_names_what_exists():
    from polyjuice.languages import UnsupportedPipeline

    with pytest.raises(UnsupportedPipeline, match="no emitter for 'rust'.*c"):
        run("def f() -> int:\n    return 1\n", "rust")
    with pytest.raises(UnsupportedPipeline, match="no frontend for 'cobol'"):
        run("", "c", source_language="cobol")


def test_cli_lists_pipelines_and_infers_source_language(tmp_path, capsys):
    from polyjuice.cli import main

    assert main(["--list"]) == 0
    assert "python->c" in capsys.readouterr().out
    src = tmp_path / "prog.py"
    src.write_text("def f(x: int) -> int:\n    return x + 1\n")
    assert main([str(src), "--out", str(tmp_path / "out")]) == 0
    assert "return x + 1;" in capsys.readouterr().out
    assert (tmp_path / "out" / "00-source.py").exists()
