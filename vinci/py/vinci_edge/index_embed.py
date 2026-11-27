"""Embedding calls for the Mojo index (M3 step 2b), kept to plain bytes at the edge.

The Mojo side never loops over floats through interop. Each function returns packed
little-endian float32 bytes: one bytes object per text for documents, one for a query.
Every call goes through vinci_edge.embed.Embedder, so the cache is the same one the
C# side and lme_embed.py use, keyed by label and exact text.
"""

import json
import os
from pathlib import Path

from vinci_edge.embed import DEFAULT_CACHE, EmbedCache, Embedder, cache_key, pack_vector

LIVE_CACHE_ENV = "VINCI_EVAL_LIVE_CACHE"
QUERY_LOG_ENV = "VINCI_EVAL_QUERY_LOG"


def _embedder(
    base_url: str, model: str, label: str, cache: str, instruction: str
) -> tuple[Embedder, EmbedCache]:
    cache_store = EmbedCache(Path(cache) if cache else DEFAULT_CACHE)
    embedder = Embedder(
        base_url,
        model,
        cache_store,
        instruction if instruction else None,
        label=label,
    )
    return embedder, cache_store


def documents(
    base_url: str,
    model: str,
    label: str,
    cache: str,
    instruction: str,
    texts: list[str],
) -> list[bytes]:
    """One packed vector per text, exactly as given (documents take no instruction)."""
    embedder, cache_store = _embedder(base_url, model, label, cache, instruction)
    try:
        vectors = embedder.documents(texts)
    finally:
        cache_store.close()
    return [pack_vector(vec) for vec in vectors]


def query(
    base_url: str,
    model: str,
    label: str,
    cache: str,
    instruction: str,
    question: str,
) -> bytes:
    """The packed vector of a question, wrapped with the query instruction."""
    embedder, cache_store = _embedder(base_url, model, label, cache, instruction)
    try:
        vector = embedder.query(question)
    finally:
        cache_store.close()
    return pack_vector(vector)


def query_with_fallback(
    base_url: str,
    model: str,
    label: str,
    cache: str,
    instruction: str,
    question: str,
    fallback_label: str,
    fallback_url: str,
) -> bytes:
    """A question's vector, looked up in order: under label in the cache, then under
    fallback_label in the cache, then embedded at fallback_url and cached under
    fallback_label. A fallback vector is never written under label, so the document
    label keeps only vectors from its own device. A miss with a closed fallback URL
    raises.

    Eval only (the M4 chat-loop evaluation): VINCI_EVAL_LIVE_CACHE names a separate cache
    file. Fallback vectors are then looked up there after the cache, and written there, so
    the cache named by the caller is only read. VINCI_EVAL_QUERY_LOG names a JSON lines
    file that gets one record per call: "gpu" (label hit), "cpu-cached" (fallback_label hit
    in the cache), "cpu-live" (embedded at fallback_url, or found in the live cache), or
    "error" (the call raised). Unset, neither changes anything."""
    source = "error"
    cache_store = EmbedCache(Path(cache) if cache else DEFAULT_CACHE)
    live_path = os.environ.get(LIVE_CACHE_ENV, "")
    live_store = EmbedCache(Path(live_path)) if live_path else None
    try:
        primary = Embedder(
            base_url, model, cache_store, instruction if instruction else None, label=label
        )
        query_text = primary.query_text(question)
        key = cache_key(label, query_text)
        found = cache_store.get_many([key])
        if key in found:
            source = "gpu"
            return pack_vector(found[key])
        fallback_key = cache_key(fallback_label, query_text)
        found = cache_store.get_many([fallback_key])
        if fallback_key in found:
            source = "cpu-cached"
            return pack_vector(found[fallback_key])
        writer = cache_store
        if live_store is not None:
            found = live_store.get_many([fallback_key])
            if fallback_key in found:
                source = "cpu-live"
                return pack_vector(found[fallback_key])
            writer = live_store
        fallback = Embedder(
            fallback_url,
            model,
            writer,
            instruction if instruction else None,
            label=fallback_label,
        )
        vector = fallback.query(question)
        source = "cpu-live"
        return pack_vector(vector)
    finally:
        cache_store.close()
        if live_store is not None:
            live_store.close()
        _log_query(source)


def _log_query(source: str) -> None:
    """One record for the query log (eval only, see query_with_fallback)."""
    log = os.environ.get(QUERY_LOG_ENV, "")
    if not log:
        return
    with Path(log).open("a", encoding="utf-8") as handle:
        handle.write(json.dumps({"source": source}) + "\n")
