# One card on the portal page: a Link, with its screenshot name.
# A saved link is named by the slug of its title; a discovered one by its port, because
# the title of a page is whatever the page calls itself.

from portal_io import lower_invariant
from portal_url import local_port_of


@fieldwise_init
struct Link(Copyable, Movable):
    var title: String
    var url: String
    var description: Optional[String]
    var tags: List[String]
    var discovered: Bool


def is_slug_char(c: Int) -> Bool:
    return (c >= 97 and c <= 122) or (c >= 48 and c <= 57)


def slug(title: String) raises -> String:
    """Lowercase, every run of other characters becomes one dash, and the
    dashes at both ends go. "Qwik Docs" becomes "qwik-docs"."""
    var lowered = lower_invariant(title)
    var out = List[UInt8]()
    var pending_dash = False
    for b in lowered.as_bytes():
        var c = Int(b)
        if is_slug_char(c):
            if pending_dash and len(out) > 0:
                out.append(45)
            pending_dash = False
            out.append(b)
        else:
            pending_dash = True
    return String(unsafe_from_utf8=out)


def preview_of(link: Link) raises -> String:
    """The screenshot name: local-<port> for a discovered page, else the slug of the title.
    """
    if link.discovered:
        var port = local_port_of(link.url)
        return "local-" + String(port.value())
    return slug(link.title)
