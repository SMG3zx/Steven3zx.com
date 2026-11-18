# Unit tests for portal_url.mojo. The expected values are System.Uri results from .NET 10
# (mojo/py/vinci_diff/portal_truth/), and the path cases were checked the same way.

from std.testing import assert_equal, assert_true

from portal_url import host_of, is_web_address, local_port_of, request_path


def expect_path(target: String, want: String) raises:
    assert_equal(request_path(target), want)


def main() raises:
    assert_true(is_web_address("http://example.com"))
    assert_true(is_web_address("HTTP://EXAMPLE.COM/x"))
    assert_true(is_web_address(" http://x.com "))
    assert_true(is_web_address("https://localhost"))
    assert_true(not is_web_address("http:example.com"))
    assert_true(not is_web_address("http:/example.com"))
    assert_true(not is_web_address("http://"))
    assert_true(not is_web_address("http:///example.com"))
    assert_true(not is_web_address("javascript:alert(1)"))
    assert_true(not is_web_address("ftp://x.com"))
    assert_true(not is_web_address("http://a b.com/"))
    assert_true(not is_web_address("http://example.com:65536/"))
    assert_true(not is_web_address("http://localhost%2e:4000/"))
    assert_true(not is_web_address("/relative"))

    assert_equal(host_of("https://www.Example.com:8080/a?b#c"), "example.com")
    assert_equal(host_of("http://bücher.de/"), "bücher.de")
    assert_equal(host_of("not a link"), "not a link")
    assert_equal(host_of("/relative/path"), "")
    assert_equal(host_of("//relative.example/x"), "relative.example")
    assert_equal(
        host_of("//relative.example:9090/x"), "//relative.example:9090/x"
    )

    assert_equal(local_port_of("http://localhost").value(), 80)
    assert_equal(local_port_of("https://localhost").value(), 443)
    assert_equal(local_port_of("http://127.0.0.1:4000/").value(), 4000)
    assert_equal(local_port_of("http://[::1]:4000/").value(), 4000)
    assert_equal(local_port_of("http://LOCALHOST:4001").value(), 4001)
    assert_equal(local_port_of("http://127.1:4000/").value(), 4000)
    assert_equal(local_port_of("http://2130706433:4000/").value(), 4000)
    assert_equal(local_port_of("http://localhost:443/").value(), 443)
    assert_true(not local_port_of("http://localhost.:4000"))
    assert_true(not local_port_of("http://example.com"))

    expect_path("/", "/")
    expect_path("/?x=1", "/")
    expect_path("/#frag", "/")
    expect_path("//", "//")
    expect_path("/%2e%2e/", "/")
    expect_path("/a/../", "/")
    expect_path("/a/./b", "/a/b")
    expect_path("/a\\b", "/a/b")
    expect_path("/previews/%41.png", "/previews/A.png")
    expect_path("/previews/%.png", "/previews/%25.png")
    expect_path("/previews/%2e.png", "/previews/..png")
    expect_path("/previews/a%2fb.png", "/previews/a%2fb.png")
    expect_path("/previews/a%2Fb.png", "/previews/a%2Fb.png")
    expect_path("/previews/a%7Eb.png", "/previews/a~b.png")
    expect_path("/a<b", "/a%3Cb")
    expect_path("/a{b}", "/a%7Bb%7D")
    expect_path("/a[b]", "/a[b]")
    expect_path("/a!b$c", "/a!b$c")
    expect_path("/éx", "/%C3%A9x")
    expect_path("/€x", "/%E2%82%ACx")
    expect_path("/\U0001F600x", "/%F0%9F%98%80x")
    expect_path("/a%5Cb", "/a%5Cb")
    expect_path("/a%zz", "/a%25zz")
    print("test_portal_url: ok")
