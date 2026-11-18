# The chat's read tool's guard (py/vinci_edge/read_guard.py): address, host, scheme, path and
# redirect checks, run by the self-test read_guard_checks.run() with stubbed resolver and
# connector (no network). Each failed check is printed, and the test then fails.

from std.python import Python
from std.testing import assert_equal

from embed_edge import edge_module


def main() raises:
    _ = edge_module()
    var checks = Python.import_module("vinci_edge.read_guard_checks")
    var failures = checks.run()
    var count = Int(String(Python.import_module("builtins").len(failures)))
    for item in failures:
        print("  " + String(item))
    assert_equal(count, 0)
    print("test_read_guard: ok")
