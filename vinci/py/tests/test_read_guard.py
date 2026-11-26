"""The chat's read tool guard (vinci_edge/read_guard.py): address, host, scheme, path and redirect
checks, run by read_guard_checks.run() with a stubbed resolver and connector (no network)."""

from vinci_edge import read_guard_checks


def test_read_guard_checks_all_pass() -> None:
    failures = read_guard_checks.run()
    for item in failures:
        print("  " + str(item))
    assert len(failures) == 0
