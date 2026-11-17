"""Exact token counts from llama-server's /tokenize, for the text recall packs. Stdlib only.

A request tokenizes one string and returns its token ids. Array input is not used: the
server returns one flat list for an array, with no boundary between the items, so the
count of each item cannot be read back from it. The packer asks for the whole text it is
checking instead, which is the count the model will read.
"""

from __future__ import annotations

import json
import urllib.request
from typing import Final, cast

TIMEOUT_S: Final = 60


def count_tokens(url: str, text: str) -> int:
    """The number of tokens llama-server's /tokenize gives text (url is the full /tokenize URL)."""
    body = json.dumps({"content": text}).encode("utf-8")
    req = urllib.request.Request(
        url,
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=TIMEOUT_S) as response:
        raw: object = json.loads(response.read().decode("utf-8"))
    if not isinstance(raw, dict):
        raise TypeError("tokenize: the reply is not a JSON object")
    reply = cast("dict[str, object]", raw)
    tokens = reply.get("tokens")
    if not isinstance(tokens, list):
        raise TypeError("tokenize: the reply has no tokens list")
    return len(cast("list[object]", tokens))
