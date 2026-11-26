"""Unit tests for portal_json.py: the links read from a portal.json, as System.Text.Json reads them
in src/Portal.cs. Depth, duplicate-key and escaped-name results were checked on .NET 10."""

import pytest

from vinci_core.portal_json import SURROGATE_MARKER, JsonScan, portal_entries


def nested(depth: int) -> str:
    return "[" * depth + "]" * depth


@pytest.mark.parametrize(
    "text",
    ["", "{}", "null", "[1,]", "[1] x", "[01]", "[TRUE]", '[{"title":"a","url":"https://a.example/"},'],
)
def test_invalid_or_non_array_gives_no_entries(text: str) -> None:
    assert len(portal_entries(text)) == 0


def test_empty_arrays() -> None:
    assert len(portal_entries("[]")) == 0
    assert len(portal_entries("[] \n")) == 0


def test_entry_fields_and_tags() -> None:
    one = portal_entries(
        '["skip", {"title":"A","url":"https://a.example/","description":"d","tags":["x",1,"y"]}]'
    )
    assert len(one) == 1
    assert one[0].title == "A"
    assert one[0].url == "https://a.example/"
    assert one[0].description == "d"
    assert len(one[0].tags) == 2
    assert one[0].tags[1] == "y"


def test_last_occurrence_wins_and_bad_types_are_none() -> None:
    last = portal_entries('[{"title":"first","title":"second","url":"https://x.example/"}]')
    assert last[0].title == "second"
    non_string = portal_entries('[{"title":"a","title":5,"url":"https://x.example/"}]')
    assert non_string[0].title is None
    tags_not_array = portal_entries('[{"title":"t","url":"https://x.example/","tags":"no"}]')
    assert len(tags_not_array[0].tags) == 0
    empty_array_title = portal_entries('[{"url":"https://x.example/"}]')
    assert empty_array_title[0].title is None


def test_escaped_key_is_read() -> None:
    escaped_key = portal_entries('[{"tit\\u006ce":"via escape","url":"https://x.example/"}]')
    assert escaped_key[0].title == "via escape"


def test_depth_limit() -> None:
    JsonScan(nested(64)).validate_document()
    with pytest.raises(ValueError):
        JsonScan(nested(65)).validate_document()


def test_lone_surrogate_raises_the_marker() -> None:
    with pytest.raises(ValueError) as info:
        portal_entries('[{"title":"\\uD800","url":"https://x.example/"}]')
    assert str(info.value) == SURROGATE_MARKER
