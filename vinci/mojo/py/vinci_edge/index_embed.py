"""Embedding calls for the Mojo index (M3 step 2b), kept to plain bytes at the edge.

The Mojo side never loops over floats through interop. Each function returns packed
little-endian float32 bytes: one bytes object per text for documents, one for a query.
Every call goes through vinci_edge.embed.Embedder, so the cache is the same one the
C# side and lme_embed.py use, keyed by label and exact text.
"""

import struct
from pathlib import Path

from vinci_edge.embed import DEFAULT_CACHE, EmbedCache, Embedder, cache_key


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


def _pack(vec: list[float]) -> bytes:
    return struct.pack(f"<{len(vec)}f", *vec)


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
    return [_pack(vec) for vec in vectors]


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
    return _pack(vector)


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
    raises."""
    cache_store = EmbedCache(Path(cache) if cache else DEFAULT_CACHE)
    try:
        primary = Embedder(
            base_url, model, cache_store, instruction if instruction else None, label=label
        )
        key = cache_key(label, primary.query_text(question))
        found = cache_store.get_many([key])
        if key in found:
            return _pack(found[key])
        fallback = Embedder(
            fallback_url,
            model,
            cache_store,
            instruction if instruction else None,
            label=fallback_label,
        )
        return _pack(fallback.query(question))
    finally:
        cache_store.close()
