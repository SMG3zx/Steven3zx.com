# The portal page: Portal.Page in src/Portal.cs, byte for byte. The text between the markup
# pieces is the C# text with its line breaks; the stylesheet, script and placeholder come
# from portal_assets.mojo, generated from the C# literals.

from portal_assets import portal_css, portal_script
from portal_html import html_encode_into, put
from portal_io import lower_invariant
from portal_link import Link, preview_of
from portal_url import host_of


def ordinal_ignore_case_key(text: String) -> List[Int]:
    """The code points of text with ASCII letters uppercased: the order OrdinalIgnoreCase gives
    for ASCII tags."""
    var out = List[Int]()
    for b in text.as_bytes():
        var c = Int(b)
        if c >= 97 and c <= 122:
            c -= 32
        out.append(c)
    return out^


def key_less(a: List[Int], b: List[Int]) -> Bool:
    var n = min(len(a), len(b))
    for i in range(n):
        if a[i] != b[i]:
            return a[i] < b[i]
    return len(a) < len(b)


def tags_in_order(links: List[Link]) -> List[String]:
    """Distinct tags (first spelling wins), then ordered by OrdinalIgnoreCase, stably.
    """
    var distinct = List[String]()
    for link in links:
        for tag in link.tags:
            var seen = False
            for have in distinct:
                if have == tag:
                    seen = True
                    break
            if not seen:
                distinct.append(tag)
    # Insertion sort is stable, as LINQ's OrderBy is.
    for i in range(1, len(distinct)):
        var j = i
        while j > 0 and key_less(
            ordinal_ignore_case_key(distinct[j]),
            ordinal_ignore_case_key(distinct[j - 1]),
        ):
            var swap = distinct[j].copy()
            distinct[j] = distinct[j - 1].copy()
            distinct[j - 1] = swap^
            j -= 1
    return distinct^


def join_tags(tags: List[String], sep: String) -> String:
    var out = String("")
    for i in range(len(tags)):
        if i > 0:
            out += sep
        out += tags[i]
    return out


def page(links: List[Link]) raises -> String:
    """Portal.Page: the whole HTML document for the links, in the order given.
    """
    var out = List[UInt8]()
    var count = String(len(links))
    put(out, "<!doctype html>\n")
    put(out, '<html lang="en">\n')
    put(out, "<head>\n")
    put(out, '<meta charset="utf-8">\n')
    put(
        out,
        (
            '<meta name="viewport" content="width=device-width,'
            ' initial-scale=1">\n'
        ),
    )
    put(
        out,
        (
            '<meta name="description" content="Saved links, searchable and'
            ' filterable by tag.">\n'
        ),
    )
    put(out, "<title>Portal</title>\n")
    put(out, "<style>" + portal_css() + "</style>\n")
    put(out, "</head>\n")
    put(out, "<body>\n")
    put(out, '<main class="portal">\n')
    put(
        out,
        '<header><h1>Portal</h1><p class="count"><span id="shown">'
        + count
        + "</span> of "
        + count
        + " links</p></header>\n",
    )
    put(
        out,
        (
            '<input class="search" id="search" type="search"'
            ' placeholder="Search links, tags, descriptions…"'
            ' aria-label="Search links">\n'
        ),
    )
    put(
        out,
        (
            '<nav class="tags" aria-label="Filter by tag"><button type="button"'
            ' class="tag active" data-tag="">all</button>'
        ),
    )
    for tag in tags_in_order(links):
        put(out, '<button type="button" class="tag" data-tag="')
        html_encode_into(out, tag)
        put(out, '">')
        html_encode_into(out, tag)
        put(out, "</button>")
    put(
        out,
        (
            '</nav><p class="empty" id="empty" hidden>No links match.</p><ul'
            ' class="links">'
        ),
    )
    for link in links:
        var description = link.description.copy()
        var words = List[String]()
        words.append(link.title)
        words.append(link.url)
        words.append(description.value() if description else String(""))
        for tag in link.tags:
            words.append(tag)
        var text = lower_invariant(join_tags(words, " "))
        put(out, '<li class="card" data-text="')
        html_encode_into(out, text)
        put(out, '" data-tags="')
        html_encode_into(out, join_tags(link.tags, "\n"))
        put(out, '">\n<a href="')
        html_encode_into(out, link.url)
        put(
            out,
            '" target="_blank" rel="noopener noreferrer"><span class="title">',
        )
        html_encode_into(out, link.title)
        put(out, '</span><span class="host">')
        html_encode_into(out, host_of(link.url))
        put(out, '</span></a>\n<img class="preview" src="/previews/')
        html_encode_into(out, preview_of(link))
        put(out, '.png" alt="Screenshot of ')
        html_encode_into(out, link.title)
        put(out, '" width="1200" height="750" loading="lazy">')
        if description and description.value().byte_length() > 0:
            put(out, '<p class="description">')
            html_encode_into(out, description.value())
            put(out, "</p>")
        if len(link.tags) > 0:
            put(out, '<ul class="card-tags">')
            for tag in link.tags:
                put(out, "<li>")
                html_encode_into(out, tag)
                put(out, "</li>")
            put(out, "</ul>")
        put(out, "</li>")
    put(
        out,
        "</ul></main><script>" + portal_script() + "</script></body></html>\n",
    )
    return String(unsafe_from_utf8=out)
