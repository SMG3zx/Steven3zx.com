"""The vector route's data: the vectors of one label, loaded as a Matrix, and the candidates
they give. The vectors come from the index's `vectors` table, written by `vinci index embed`.

Port of mojo/src/vectors.mojo. The vector bytes are packed little-endian float32 (see
vinci_edge.index_embed); floats are unpacked here with struct.
"""

from __future__ import annotations

import struct
from dataclasses import replace
from typing import Optional

from .embed_config import EmbedConfig, embed_documents, embed_question
from .fusion import Candidate
from .index_store import IndexStore
from .vector_scan import Matrix, normalize, scan_simd

FLOAT_BYTES = 4
EMBED_CHUNK = 128


def floats_of(blob: bytes) -> list[float]:
    """The float32 values in a packed bytes object, unnormalised."""
    count = len(blob) // FLOAT_BYTES
    return list(struct.unpack("<%df" % count, blob[: count * FLOAT_BYTES]))


def embed_text_for_query(cfg: EmbedConfig, question: str) -> list[float]:
    """The normalised query vector, with the instruction (the cache key is the text)."""
    return normalize(floats_of(embed_question(cfg, question)))


class VectorIndex:
    """The vectors of one label whose facts are eligible for recall, as one matrix.

    Eligible means the same as the lexical route: current and active, or (for the temporal
    route) superseded but active. meta[i] is the candidate for row i.
    """

    def __init__(self, label: str) -> None:
        self.label: str = label
        self.matrix: Matrix = Matrix(0)
        self.meta: list[Candidate] = []
        self.position: dict[int, int] = {}

    def count(self) -> int:
        return self.matrix.count

    def search(self, query: list[float], k: int) -> list[Candidate]:
        """The k nearest eligible facts by cosine, best first, as vector candidates."""
        out: list[Candidate] = []
        if self.matrix.count == 0:
            return out
        for hit in scan_simd(self.matrix, query, k):
            c = replace(self.meta[hit.index])
            c.in_vector = True
            c.has_cosine = True
            c.cosine = hit.score
            out.append(c)
        return out

    def cosine_of(self, rowid: int, query: list[float]) -> Optional[float]:
        """The cosine of a fact's stored vector and the normalised query, if it has one."""
        if rowid not in self.position:
            return None
        row = self.position[rowid]
        dim = self.matrix.dim
        acc = 0.0
        for j in range(dim):
            acc += self.matrix.data[row * dim + j] * query[j]
        return acc


def load_vectors(store: IndexStore, label: str, include_superseded: bool) -> VectorIndex:
    """Read the label's vectors for the eligible facts, in rowid order."""
    out = VectorIndex(label)
    status_clause = "f.valid_until IS NULL AND f.status = 'active'"
    if include_superseded:
        status_clause = "f.status IN ('active', 'superseded')"
    rows = store.conn.execute(
        "SELECT f.rowid, f.cite, f.kind, f.text, f.at, f.valid_from,"
        " f.source, f.trusted, v.dims, v.vec FROM vectors AS v JOIN facts"
        " AS f ON f.rowid = v.fact WHERE v.label = ? AND "
        + status_clause
        + " ORDER BY f.rowid",
        (label,),
    ).fetchall()
    dim = -1
    for row in rows:
        dims = int(row[8])
        if dim < 0:
            dim = dims
            out.matrix = Matrix(dim)
        if dims != dim:
            raise ValueError("vectors: label " + label + " has mixed dimensions")
        rowid = int(row[0])
        c = Candidate(
            rowid,
            str(row[1]),
            str(row[2]),
            str(row[3]),
            str(row[4]),
            str(row[5]),
            str(row[6]),
            str(row[7]) == "1",
        )
        out.matrix.insert(floats_of(row[9]))
        out.position[rowid] = len(out.meta)
        out.meta.append(c)
    return out


def embed_missing(store: IndexStore, cfg: EmbedConfig) -> int:
    """vinci index embed: vectors for every active fact the label does not have yet.

    Returns how many facts still have no vector. A chunk that fails (the server is
    unreachable, or rejects a text as too long) is retried one text at a time, so a text that
    cannot be embedded is left without a vector and counted, instead of stopping the run.
    Chunks are committed as they finish, so a stopped run resumes. The texts are the facts'
    own text, sent as documents (no instruction).
    """
    missing = store.missing_vectors(cfg.label)
    cache = cfg.cache or "~/.vinci/index/embed-cache.sqlite (default)"
    if len(missing) == 0:
        print("embed " + cfg.label + ": every active fact already has a vector")
        return 0
    print(
        "embed " + cfg.label + ": " + str(len(missing)) + " facts to embed via " + cfg.base_url
        + ", cache " + cache
    )
    left_out = 0
    start = 0
    while start < len(missing):
        end = min(start + EMBED_CHUNK, len(missing))
        texts = [m.text for m in missing[start:end]]
        ids = [m.rowid for m in missing[start:end]]
        blobs: list[bytes] = []
        keep_ids: list[int] = []
        try:
            blobs = list(embed_documents(cfg, texts))
            keep_ids = ids
        except Exception:
            # The batch failed: embed each text alone. Cached texts are still hits, so only
            # the uncached ones reach the server.
            blobs = []
            for k, text in enumerate(texts):
                try:
                    blobs.extend(embed_documents(cfg, [text]))
                    keep_ids.append(ids[k])
                except Exception:
                    left_out += 1
        store.begin()
        for k, blob in enumerate(blobs):
            store.put_vector(keep_ids[k], cfg.label, blob, len(blob) // FLOAT_BYTES)
        store.commit()
        start = end
        print("  " + str(start) + "/" + str(len(missing)))
    if left_out > 0:
        print(
            "embed: " + str(left_out)
            + " facts have no vector (text not cached, and the server failed or rejected it)"
        )
    return left_out
