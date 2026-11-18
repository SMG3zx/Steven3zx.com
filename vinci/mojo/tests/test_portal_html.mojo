# Unit tests for portal_html.mojo. Expected values are WebUtility and TitleOf results from
# .NET 10 (mojo/py/vinci_diff/portal_truth/), checked before they were written here.

from std.testing import assert_equal, assert_true

from portal_html import html_decode, html_encode, title_of


def expect_decode(text: String, want: String) raises:
    assert_equal(html_decode(text), want)


def main() raises:
    expect_decode("&amp;", "&")
    expect_decode("&#65;", "A")
    expect_decode("&#x41;", "A")
    expect_decode("&#X41;", "A")
    expect_decode("&#65 ;", "A")
    expect_decode("&# 65;", "A")
    expect_decode("&#+65;", "A")
    expect_decode("&#00065;", "A")
    expect_decode("&#x1F600;", "\U0001F600")
    expect_decode("&#128512;", "\U0001F600")
    expect_decode("&nbsp;", " ")
    expect_decode("&amp;amp;", "&amp;")
    expect_decode("&#8217;", "’")
    expect_decode("&#65", "&#65")
    expect_decode("&#x41", "&#x41")
    expect_decode("&#x 41;", "&#x 41;")
    expect_decode("&#x41 ;", "&#x41 ;")
    expect_decode("&#0x41;", "&#0x41;")
    expect_decode("&#x110000;", "&#x110000;")
    expect_decode("&#xD800;", "&#xD800;")
    expect_decode("&#-1;", "&#-1;")
    expect_decode("&#;", "&#;")
    expect_decode("&#x;", "&#x;")
    expect_decode("&amp", "&amp")
    expect_decode("&AMP;", "&AMP;")
    expect_decode("&NBSP;", "&NBSP;")
    expect_decode("&unknown;", "&unknown;")
    expect_decode("Tom &amp Jerry", "Tom &amp Jerry")
    expect_decode("&copy 2026", "&copy 2026")
    expect_decode("&&amp;", "&&")
    expect_decode("&;", "&;")

    assert_equal(html_encode("a & b < c > d"), "a &amp; b &lt; c &gt; d")
    assert_equal(html_encode("\"q\" 'a'"), "&quot;q&quot; &#39;a&#39;")
    assert_equal(html_encode("é"), "&#233;")
    assert_equal(html_encode("…"), "…")
    assert_equal(html_encode("Ā"), "Ā")
    assert_equal(html_encode("\U0001F600"), "&#128512;")

    assert_equal(title_of("<title>Qwik Docs</title>").value(), "Qwik Docs")
    assert_equal(
        title_of('<TITLE lang="en">  Upper\tCase  </TITLE>').value(),
        "Upper Case",
    )
    assert_equal(
        title_of("<head><title>a\n  b &amp; c</title></head>").value(),
        "a b & c",
    )
    assert_equal(title_of("<title x=y>&#65;</title>").value(), "A")
    assert_true(not title_of("<title>one<b>two</b></title>"))
    assert_true(not title_of("<title>   </title>"))
    assert_true(not title_of("<p>no title here</p>"))
    assert_true(not title_of("<title>unclosed"))
    print("test_portal_html: ok")
