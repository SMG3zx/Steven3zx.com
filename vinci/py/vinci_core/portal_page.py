"""The portal page: the whole HTML document for a list of links, in the order given. The text
between the markup pieces keeps its line breaks, and the stylesheet, script and placeholder come
from portal_assets.

Port of mojo/src/portal_page.mojo.
"""

from __future__ import annotations

from .portal_assets import portal_css, portal_script
from .portal_html import html_encode
from .portal_io import lower_invariant
from .portal_link import Link, preview_of
from .portal_url import host_of


def ordinal_ignore_case_key(text: str) -> list[int]:
    """The code points of text with ASCII letters uppercased: the order OrdinalIgnoreCase gives
    for ASCII tags."""
    return [c - 32 if 97 <= c <= 122 else c for c in text.encode("utf-8")]


def key_less(a: list[int], b: list[int]) -> bool:
    n = min(len(a), len(b))
    for i in range(n):
        if a[i] != b[i]:
            return a[i] < b[i]
    return len(a) < len(b)


def tags_in_order(links: list[Link]) -> list[str]:
    """Distinct tags (first spelling wins), then ordered by OrdinalIgnoreCase, stably."""
    distinct: list[str] = []
    for link in links:
        for tag in link.tags:
            if tag not in distinct:
                distinct.append(tag)
    # Insertion sort is stable: equal tags keep their order.
    for i in range(1, len(distinct)):
        j = i
        while j > 0 and key_less(ordinal_ignore_case_key(distinct[j]), ordinal_ignore_case_key(distinct[j - 1])):
            distinct[j], distinct[j - 1] = distinct[j - 1], distinct[j]
            j -= 1
    return distinct


def join_tags(tags: list[str], sep: str) -> str:
    return sep.join(tags)


def page(links: list[Link]) -> str:
    """The whole HTML document for the links, in the order given."""
    out: list[str] = []
    count = str(len(links))
    out.append("<!doctype html>\n")
    out.append('<html lang="en">\n')
    out.append("<head>\n")
    out.append('<meta charset="utf-8">\n')
    out.append('<meta name="viewport" content="width=device-width, initial-scale=1">\n')
    out.append('<meta name="description" content="Saved links, searchable and filterable by tag.">\n')
    out.append("<title>Portal</title>\n")
    out.append("<style>" + portal_css() + "</style>\n")
    out.append("</head>\n")
    out.append("<body>\n")
    out.append('<main class="portal">\n')
    out.append('<header><h1>Portal</h1><p class="count"><span id="shown">' + count + "</span> of " + count + " links</p></header>\n")
    out.append(
        '<input class="search" id="search" type="search" placeholder="Search links, tags, descriptions…" aria-label="Search links">\n'
    )
    out.append('<nav class="tags" aria-label="Filter by tag"><button type="button" class="tag active" data-tag="">all</button>')
    for tag in tags_in_order(links):
        out.append('<button type="button" class="tag" data-tag="')
        out.append(html_encode(tag))
        out.append('">')
        out.append(html_encode(tag))
        out.append("</button>")
    out.append('</nav><p class="empty" id="empty" hidden>No links match.</p><ul class="links">')
    for link in links:
        words = [link.title, link.url, link.description if link.description else ""]
        words.extend(link.tags)
        text = lower_invariant(join_tags(words, " "))
        out.append('<li class="card" data-text="')
        out.append(html_encode(text))
        out.append('" data-tags="')
        out.append(html_encode(join_tags(link.tags, "\n")))
        out.append('">\n<a href="')
        out.append(html_encode(link.url))
        out.append('" target="_blank" rel="noopener noreferrer"><span class="title">')
        out.append(html_encode(link.title))
        out.append('</span><span class="host">')
        out.append(html_encode(host_of(link.url)))
        out.append('</span></a>\n<img class="preview" src="/previews/')
        out.append(html_encode(preview_of(link)))
        out.append('.png" alt="Screenshot of ')
        out.append(html_encode(link.title))
        out.append('" width="1200" height="750" loading="lazy">')
        if link.description and len(link.description.encode("utf-8")) > 0:
            out.append('<p class="description">')
            out.append(html_encode(link.description))
            out.append("</p>")
        if len(link.tags) > 0:
            out.append('<ul class="card-tags">')
            for tag in link.tags:
                out.append("<li>" + html_encode(tag) + "</li>")
            out.append("</ul>")
        out.append("</li>")
    out.append("</ul></main><script>" + portal_script() + "</script></body></html>\n")
    return "".join(out)
