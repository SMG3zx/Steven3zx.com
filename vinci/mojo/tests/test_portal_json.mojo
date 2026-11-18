# Unit tests for portal_json.mojo: the links read from a portal.json, as System.Text.Json
# reads them in src/Portal.cs. Depth, duplicate-key and escaped-name results were checked on .NET 10.

from std.testing import assert_equal, assert_true

from portal_json import JsonScan, SURROGATE_MARKER, portal_entries


def nested(depth: Int) -> String:
    var text = String("")
    for _ in range(depth):
        text += "["
    for _ in range(depth):
        text += "]"
    return text


def main() raises:
    assert_equal(len(portal_entries("[]")), 0)
    assert_equal(len(portal_entries("[] \n")), 0)
    assert_equal(len(portal_entries("{}")), 0)
    assert_equal(len(portal_entries("null")), 0)
    assert_equal(len(portal_entries("")), 0)
    assert_equal(len(portal_entries("[1,]")), 0)
    assert_equal(len(portal_entries("[1] x")), 0)
    assert_equal(len(portal_entries("[01]")), 0)
    assert_equal(len(portal_entries("[TRUE]")), 0)
    assert_equal(
        len(portal_entries('[{"title":"a","url":"https://a.example/"},')), 0
    )

    var one = portal_entries(
        '["skip",'
        ' {"title":"A","url":"https://a.example/","description":"d","tags":["x",1,"y"]}]'
    )
    assert_equal(len(one), 1)
    assert_equal(one[0].title.value(), "A")
    assert_equal(one[0].url.value(), "https://a.example/")
    assert_equal(one[0].description.value(), "d")
    assert_equal(len(one[0].tags), 2)
    assert_equal(one[0].tags[1], "y")

    var last = portal_entries(
        '[{"title":"first","title":"second","url":"https://x.example/"}]'
    )
    assert_equal(last[0].title.value(), "second")
    var non_string = portal_entries(
        '[{"title":"a","title":5,"url":"https://x.example/"}]'
    )
    assert_true(not non_string[0].title)
    var tags_not_array = portal_entries(
        '[{"title":"t","url":"https://x.example/","tags":"no"}]'
    )
    assert_equal(len(tags_not_array[0].tags), 0)
    var escaped_key = portal_entries(
        '[{"tit\\u006ce":"via escape","url":"https://x.example/"}]'
    )
    assert_equal(escaped_key[0].title.value(), "via escape")

    var empty_array_title = portal_entries('[{"url":"https://x.example/"}]')
    assert_true(not empty_array_title[0].title)

    var ok64 = JsonScan(nested(64))
    ok64.validate_document()
    var failed = False
    try:
        var too_deep = JsonScan(nested(65))
        too_deep.validate_document()
    except:
        failed = True
    assert_true(failed)

    var raised = False
    try:
        _ = portal_entries('[{"title":"\\uD800","url":"https://x.example/"}]')
    except e:
        raised = String(e) == SURROGATE_MARKER
    assert_true(raised)
    print("test_portal_json: ok")
