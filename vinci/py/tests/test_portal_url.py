"""Unit tests for portal_url.py. The expected values are System.Uri results from .NET 10
(py/vinci_diff/portal_truth/), and the path cases were checked the same way."""

import pytest

from vinci_core.portal_url import host_of, is_web_address, local_port_of, request_path


def test_web_addresses() -> None:
    assert is_web_address("http://example.com")
    assert is_web_address("HTTP://EXAMPLE.COM/x")
    assert is_web_address(" http://x.com ")
    assert is_web_address("https://localhost")
    assert not is_web_address("http:example.com")
    assert not is_web_address("http:/example.com")
    assert not is_web_address("http://")
    assert not is_web_address("http:///example.com")
    assert not is_web_address("javascript:alert(1)")
    assert not is_web_address("ftp://x.com")
    assert not is_web_address("http://a b.com/")
    assert not is_web_address("http://example.com:65536/")
    assert not is_web_address("http://localhost%2e:4000/")
    assert not is_web_address("/relative")


def test_hosts() -> None:
    assert host_of("https://www.Example.com:8080/a?b#c") == "example.com"
    assert host_of("http://bücher.de/") == "bücher.de"
    assert host_of("not a link") == "not a link"
    assert host_of("/relative/path") == ""
    assert host_of("//relative.example/x") == "relative.example"
    assert host_of("//relative.example:9090/x") == "//relative.example:9090/x"


def test_local_ports() -> None:
    assert local_port_of("http://localhost") == 80
    assert local_port_of("https://localhost") == 443
    assert local_port_of("http://127.0.0.1:4000/") == 4000
    assert local_port_of("http://[::1]:4000/") == 4000
    assert local_port_of("http://LOCALHOST:4001") == 4001
    assert local_port_of("http://127.1:4000/") == 4000
    assert local_port_of("http://2130706433:4000/") == 4000
    assert local_port_of("http://localhost:443/") == 443
    assert local_port_of("http://localhost.:4000") is None
    assert local_port_of("http://example.com") is None


@pytest.mark.parametrize(
    "target, want",
    [
        ("/", "/"),
        ("/?x=1", "/"),
        ("/#frag", "/"),
        ("//", "//"),
        ("/%2e%2e/", "/"),
        ("/a/../", "/"),
        ("/a/./b", "/a/b"),
        ("/a\\b", "/a/b"),
        ("/previews/%41.png", "/previews/A.png"),
        ("/previews/%.png", "/previews/%25.png"),
        ("/previews/%2e.png", "/previews/..png"),
        ("/previews/a%2fb.png", "/previews/a%2fb.png"),
        ("/previews/a%2Fb.png", "/previews/a%2Fb.png"),
        ("/previews/a%7Eb.png", "/previews/a~b.png"),
        ("/a<b", "/a%3Cb"),
        ("/a{b}", "/a%7Bb%7D"),
        ("/a[b]", "/a[b]"),
        ("/a!b$c", "/a!b$c"),
        ("/éx", "/%C3%A9x"),
        ("/€x", "/%E2%82%ACx"),
        ("/\U0001F600x", "/%F0%9F%98%80x"),
        ("/a%5Cb", "/a%5Cb"),
        ("/a%zz", "/a%25zz"),
    ],
)
def test_request_paths(target: str, want: str) -> None:
    assert request_path(target) == want
