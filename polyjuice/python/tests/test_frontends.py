"""Every frontend, same canonical programs: the original, emitted C, emitted Python and the
shared-layer IR run through LLVM must all agree with the Python reference."""

import shutil

import pytest

import harness
from polyjuice.languages import FRONTENDS
from polyjuice.pipeline import run

LANGS = sorted(
    lang for lang, f in FRONTENDS.items() if (harness.PROGRAMS / f"canon.{f.extension}").exists() and lang != "python"
)


def source(lang: str) -> str:
    return harness.source_of(FRONTENDS[lang].extension)


@pytest.mark.parametrize("lang", LANGS)
def test_original_matches_the_reference(lang):
    tool, runner = harness.ORIGINALS.get(lang, (None, None))
    if runner is None:
        pytest.skip(f"no toolchain wired up for {lang}")
    if shutil.which(tool) is None:
        pytest.skip(f"{tool} is not installed")
    harness.check_results(runner(source(lang)), f"original {lang}")


@pytest.mark.parametrize("target", ["c", "python"])
@pytest.mark.parametrize("lang", LANGS)
def test_transpiled_program_matches_the_reference(lang, target):
    output = run(source(lang), target, source_language=lang).output
    runner = harness.run_emitted_c if target == "c" else harness.run_emitted_python
    harness.check_results(runner(output, floats=lang in harness.FLOAT_ONLY), f"{lang} -> {target}")


@pytest.mark.parametrize("lang", LANGS)
def test_shared_layer_through_llvm_matches_the_reference(lang):
    build = FRONTENDS[lang].build_module
    results = harness.run_llvm(lambda ctx: build(source(lang), ctx), floats=lang in harness.FLOAT_ONLY)
    harness.check_results(results, f"{lang} -> LLVM")
