"""Unit tests for portal_html.py. Expected values are WebUtility and TitleOf results from .NET 10
(py/vinci_diff/portal_truth/), checked before they were written here."""

import pytest

from vinci_core.portal_html import html_decode, html_encode, title_of


@pytest.mark.parametrize(
    "text, want",
    [
        ("&amp;", "&"),
        ("&#65;", "A"),
        ("&#x41;", "A"),
        ("&#X41;", "A"),
        ("&#65 ;", "A"),
        ("&# 65;", "A"),
        ("&#+65;", "A"),
        ("&#00065;", "A"),
        ("&#x1F600;", "\U0001F600"),
        ("&#128512;", "\U0001F600"),
        ("&nbsp;", "\u00a0"),
        ("&amp;amp;", "&amp;"),
        ("&#8217;", "’"),
        ("&#65", "&#65"),
        ("&#x41", "&#x41"),
        ("&#x 41;", "&#x 41;"),
        ("&#x41 ;", "&#x41 ;"),
        ("&#0x41;", "&#0x41;"),
        ("&#x110000;", "&#x110000;"),
        ("&#xD800;", "&#xD800;"),
        ("&#-1;", "&#-1;"),
        ("&#;", "&#;"),
        ("&#x;", "&#x;"),
        ("&amp", "&amp"),
        ("&AMP;", "&AMP;"),
        ("&NBSP;", "&NBSP;"),
        ("&unknown;", "&unknown;"),
        ("Tom &amp Jerry", "Tom &amp Jerry"),
        ("&copy 2026", "&copy 2026"),
        ("&&amp;", "&&"),
        ("&;", "&;"),
    ],
)
def test_decode(text: str, want: str) -> None:
    assert html_decode(text) == want


def test_encode() -> None:
    assert html_encode("a & b < c > d") == "a &amp; b &lt; c &gt; d"
    assert html_encode('"q" \'a\'') == "&quot;q&quot; &#39;a&#39;"
    assert html_encode("é") == "&#233;"
    assert html_encode("…") == "…"
    assert html_encode("Ā") == "Ā"
    assert html_encode("\U0001F600") == "&#128512;"


def test_title() -> None:
    assert title_of("<title>Qwik Docs</title>") == "Qwik Docs"
    assert title_of('<TITLE lang="en">  Upper\tCase  </TITLE>') == "Upper Case"
    assert title_of("<head><title>a\n  b &amp; c</title></head>") == "a b & c"
    assert title_of("<title x=y>&#65;</title>") == "A"
    assert title_of("<title>one<b>two</b></title>") is None
    assert title_of("<title>   </title>") is None
    assert title_of("<p>no title here</p>") is None
    assert title_of("<title>unclosed") is None
