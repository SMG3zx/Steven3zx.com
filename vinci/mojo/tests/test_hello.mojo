from std.testing import assert_equal

from hello import add


def main() raises:
    assert_equal(add(2, 3), 5)
    assert_equal(add(-1, 1), 0)
    print("test_hello: ok")
