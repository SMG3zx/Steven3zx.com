"""Shared differential harness for the language frontends.

Every language gets the same canonical programs (tests/programs/canon.<ext>). For a
language we (1) run the original with its real toolchain where one is installed and
compare against a Python reference, then (2) transpile to each target and compare the
emitted code, and the shared-layer IR executed by LLVM, against the same reference.
"""

import ctypes
import json
import math
import shutil
import subprocess
import tempfile
from pathlib import Path

PROGRAMS = Path(__file__).parent / "programs"

# name -> (argument tuples, result is float)
CALLS: dict[str, tuple[list[tuple], bool]] = {
    "gcd": ([(12, 18), (7, 7), (35, 14), (1, 9), (48, 36)], False),
    "sum_to": ([(0,), (1,), (10,)], False),
    "fact": ([(0,), (1,), (5,), (10,)], False),
    "collatz": ([(1,), (6,), (27,)], False),
    "mean2": ([(1.0, 2.0), (3.5, -1.5), (0.0, 0.0)], True),
    "is_prime": ([(1,), (2,), (15,), (17,), (97,)], False),
    "sumsq": ([(0,), (4,), (10,)], False),
    "abs_diff": ([(3, 9), (9, 3), (5, 5)], False),
    "power": ([(2, 10), (3, 0), (5, 3)], False),
}


def _gcd(a, b):
    while b != 0:
        a, b = b, a % b
    return a


def _collatz(n):
    steps = 0
    while n != 1:
        n = n // 2 if n % 2 == 0 else 3 * n + 1
        steps += 1
    return steps


def _is_prime(n):
    if n < 2:
        return 0
    i = 2
    while i * i <= n:
        if n % i == 0:
            return 0
        i += 1
    return 1


def _power(base, exp):
    r = 1
    for _ in range(exp):
        r *= base
    return r


REFERENCE = {
    "gcd": _gcd,
    "sum_to": lambda n: n * (n + 1) // 2,
    "fact": math.factorial,
    "collatz": _collatz,
    "mean2": lambda x, y: (x + y) / 2,
    "is_prime": _is_prime,
    "sumsq": lambda n: sum(i * i for i in range(n)),
    "abs_diff": lambda a, b: abs(a - b),
    "power": _power,
}

EXPECTED = {name: [REFERENCE[name](*args) for args in calls] for name, (calls, _) in CALLS.items()}


def source_of(extension: str) -> str:
    return (PROGRAMS / f"canon.{extension}").read_text()


def same(actual, expected) -> bool:
    return math.isclose(actual, expected, rel_tol=1e-12, abs_tol=1e-12)


def check_results(results: dict, label: str) -> None:
    """results: name -> list of values aligned with CALLS[name]."""
    for name, values in EXPECTED.items():
        assert name in results, f"{label}: missing {name}"
        assert len(results[name]) == len(values), f"{label}: {name} count"
        for args, got, want in zip(CALLS[name][0], results[name], values):
            assert same(got, want), f"{label}: {name}{args} = {got}, expected {want}"


# -- running emitted code --------------------------------------------------------------------


def run_emitted_python(code: str) -> dict:
    namespace: dict = {}
    exec(code, namespace)
    return {n: [namespace[n](*a) for a in calls] for n, (calls, _) in CALLS.items()}


def run_emitted_c(code: str) -> dict:
    cc = shutil.which("cc") or shutil.which("gcc")
    with tempfile.TemporaryDirectory() as tmp:
        c_file, so_file = Path(tmp) / "out.c", Path(tmp) / "out.so"
        c_file.write_text(code)
        subprocess.run([cc, "-std=c11", "-Wall", "-shared", "-fPIC", "-o", so_file, c_file, "-lm"], check=True)
        lib = ctypes.CDLL(str(so_file))
        results = {}
        for name, (calls, is_float) in CALLS.items():
            fn = getattr(lib, name + "_" if name in ("power",) and not hasattr(lib, name) else name)
            fn.restype = ctypes.c_double if is_float else ctypes.c_int32
            results[name] = [
                fn(*[ctypes.c_double(a) if isinstance(a, float) else ctypes.c_int32(a) for a in args])
                for args in calls
            ]
        return results


def run_llvm(module_builder) -> dict:
    """Execute the shared-layer IR with MLIR's ExecutionEngine."""
    from polyjuice import verify
    from polyjuice.mlir_env import ensure_mlir_importable

    ensure_mlir_importable()
    from mlir import ir

    context = ir.Context()
    module = module_builder(context)
    with context:
        lowered = verify.lower_to_llvm_dialect(module)
        out = {}
        for name, (calls, is_float) in CALLS.items():
            result = ctypes.c_double if is_float else ctypes.c_int32
            out[name] = [verify.execute(lowered, name, list(a), result)[0] for a in calls]
        return out


# -- running the originals -------------------------------------------------------------------


def run_in_tmp(command: list[str], files: dict[str, str], timeout: int = 120) -> str:
    with tempfile.TemporaryDirectory() as tmp:
        for filename, content in files.items():
            (Path(tmp) / filename).write_text(content)
        done = subprocess.run(command, cwd=tmp, capture_output=True, text=True, timeout=timeout)
        assert done.returncode == 0, f"{command}: {done.stderr[:800]}"
        return done.stdout


def parse_lines(output: str) -> dict:
    """Driver output is one `name args... = result` line per call, in CALLS order."""
    results: dict = {}
    for row in output.strip().splitlines():
        name, value = row.split("=")[0].split()[0], row.split("=")[1].strip()
        results.setdefault(name, []).append(float(value))
    return results


def call_list():
    for name, (calls, is_float) in CALLS.items():
        for args in calls:
            yield name, args, is_float


def lit(value, style: str = "plain") -> str:
    if isinstance(value, float):
        return repr(value)
    return str(value)


def json_dumps(obj):
    return json.dumps(obj)
