import ctypes
import shutil
import subprocess
import tempfile
from pathlib import Path

import pytest

from polyjuice import compile_to_c


@pytest.fixture
def compile_c():
    """Translate a Python-subset source to C, build it with cc, return the loaded library."""
    cc = shutil.which("cc") or shutil.which("gcc")
    assert cc, "need a C compiler"
    workdirs = []

    def build(source: str) -> ctypes.CDLL:
        workdir = tempfile.TemporaryDirectory()
        workdirs.append(workdir)
        c_file = Path(workdir.name) / "out.c"
        so_file = Path(workdir.name) / "out.so"
        c_file.write_text(compile_to_c(source))
        subprocess.run(
            [cc, "-std=c11", "-Wall", "-Werror", "-shared", "-fPIC", "-o", so_file, c_file, "-lm"],
            check=True,
        )
        return ctypes.CDLL(str(so_file))

    yield build
    for workdir in workdirs:
        workdir.cleanup()
