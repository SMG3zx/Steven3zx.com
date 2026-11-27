"""Embedding client and vector cache for vinci's index and eval code. Stdlib only.

Vectors come from an OpenAI-shaped /v1/embeddings server (llama-server). The cache is keyed by
sha256(label + "\\n" + text), where label is the model name unless a label is given (a device label such
as "qwen3-embedding:4b@gpu": CPU and GPU vectors must never share one cache) and text is exactly the
string sent to the server, so a query's key includes its instruction. Vectors are stored as float32, little-endian.
"""

import hashlib
import json
import sqlite3
import struct
import urllib.error
import urllib.request
from collections.abc import Sequence
from pathlib import Path
from types import TracebackType
from typing import Final, Self, cast

DEFAULT_CACHE: Final = Path.home() / ".vinci" / "index" / "embed-cache.sqlite"
DEFAULT_BASE_URL: Final = "http://127.0.0.1:8081/v1"
DEFAULT_MODEL: Final = "qwen3-embedding:4b"
# Same default as EmbeddingClient in src/Memory/Providers.cs.
DEFAULT_INSTRUCTION: Final = "Retrieve memories and source events relevant to the user's query"
MEMORY_ENV: Final = Path.home() / ".config" / "vinci" / "memory.env"
INSTRUCTION_KEY: Final = "EMBEDDING_QUERY_INSTRUCTION"
BATCH_SIZE: Final = 32
TIMEOUT_S: Final = 600
_SQL_CHUNK: Final = 500


class EmbedRejected(Exception):
    """The server refused a request with a 4xx status, for example an input over the context."""

    def __init__(self, status: int, detail: str) -> None:
        super().__init__(f"embedding server rejected the request ({status}): {detail}")
        self.status: int = status
        self.detail: str = detail


def cache_key(label: str, text: str) -> str:
    return hashlib.sha256(f"{label}\n{text}".encode()).hexdigest()


def format_query(instruction: str, question: str) -> str:
    """The exact text sent to the server for a query. The C# side wraps queries the same way."""
    return f"Instruct: {instruction}\nQuery: {question}"


def read_query_instruction(path: Path = MEMORY_ENV) -> str:
    """EMBEDDING_QUERY_INSTRUCTION from memory.env, or the C# default. Reads only that key."""
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError:
        return DEFAULT_INSTRUCTION
    for line in lines:
        name, sep, value = line.partition("=")
        if not sep or name.strip() != INSTRUCTION_KEY:
            continue
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        if value:
            return value
    return DEFAULT_INSTRUCTION


def pack_vector(vec: Sequence[float]) -> bytes:
    return struct.pack(f"<{len(vec)}f", *vec)


def _unpack(blob: bytes, dims: int) -> list[float]:
    return list(struct.unpack(f"<{dims}f", blob))


class EmbedCache:
    """SQLite store of vectors, one row per (label, text). The label goes in the model column."""

    def __init__(self, path: Path | str = DEFAULT_CACHE) -> None:
        self.path: Path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._db: sqlite3.Connection = sqlite3.connect(self.path)
        self._db.execute(
            "CREATE TABLE IF NOT EXISTS vectors ("
            "key TEXT PRIMARY KEY, model TEXT NOT NULL, dims INTEGER NOT NULL, vec BLOB NOT NULL)"
        )
        self._db.commit()

    def get_many(self, keys: Sequence[str]) -> dict[str, list[float]]:
        found: dict[str, list[float]] = {}
        unique = list(dict.fromkeys(keys))
        for start in range(0, len(unique), _SQL_CHUNK):
            chunk = unique[start : start + _SQL_CHUNK]
            marks = ",".join("?" for _ in chunk)
            rows = cast(
                "list[tuple[str, int, bytes]]",
                self._db.execute(
                    f"SELECT key, dims, vec FROM vectors WHERE key IN ({marks})", chunk
                ).fetchall(),
            )
            for key, dims, blob in rows:
                found[key] = _unpack(blob, dims)
        return found

    def put_many(self, label: str, rows: Sequence[tuple[str, Sequence[float]]]) -> None:
        self._db.executemany(
            "INSERT OR IGNORE INTO vectors (key, model, dims, vec) VALUES (?, ?, ?, ?)",
            [(key, label, len(vec), pack_vector(vec)) for key, vec in rows],
        )
        self._db.commit()

    def count(self) -> int:
        row = cast("tuple[int]", self._db.execute("SELECT COUNT(*) FROM vectors").fetchone())
        return row[0]

    def close(self) -> None:
        self._db.close()

    def __enter__(self) -> Self:
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        tb: TracebackType | None,
    ) -> None:
        self.close()


def _parse_vectors(raw: object, count: int) -> list[list[float]]:
    if not isinstance(raw, dict):
        raise TypeError("embedding response is not a JSON object")
    data = cast("dict[str, object]", raw).get("data")
    if not isinstance(data, list):
        raise TypeError("embedding response has no data list")
    rows: list[tuple[int, list[float]]] = []
    for item in cast("list[object]", data):
        if not isinstance(item, dict):
            raise TypeError("embedding item is not a JSON object")
        obj = cast("dict[str, object]", item)
        index = obj.get("index")
        embedding = obj.get("embedding")
        if not isinstance(index, int) or not isinstance(embedding, list):
            raise TypeError("embedding item lacks an integer index or a vector")
        vec: list[float] = []
        for value in cast("list[object]", embedding):
            if not isinstance(value, int | float):
                raise TypeError("embedding vector holds a non-number")
            vec.append(float(value))
        rows.append((index, vec))
    if sorted(index for index, _ in rows) != list(range(count)):
        raise ValueError(f"embedding response does not return {count} indexed vectors")
    rows.sort(key=lambda row: row[0])
    return [vec for _, vec in rows]


class Embedder:
    """Embeds exact texts through the server, reusing and filling the cache.

    `embed` sends each text exactly as given. `documents` is the same, with documents sent
    without an instruction. `query` wraps the question as the C# side does before sending it.
    """

    def __init__(
        self,
        base_url: str = DEFAULT_BASE_URL,
        model: str = DEFAULT_MODEL,
        cache: EmbedCache | None = None,
        instruction: str | None = None,
        label: str | None = None,
    ) -> None:
        self.base_url: str = base_url.rstrip("/")
        self.model: str = model
        self.label: str = label if label is not None else model
        self.cache: EmbedCache = cache if cache is not None else EmbedCache()
        self.instruction: str = instruction if instruction is not None else read_query_instruction()

    def query_text(self, question: str) -> str:
        return format_query(self.instruction, question)

    def query(self, question: str) -> list[float]:
        return self.embed([self.query_text(question)])[0]

    def documents(self, texts: Sequence[str]) -> list[list[float]]:
        return self.embed(texts)

    def embed(self, texts: Sequence[str]) -> list[list[float]]:
        keys = [cache_key(self.label, text) for text in texts]
        known = self.cache.get_many(keys)
        pending: list[tuple[str, str]] = []
        for key, text in zip(keys, texts, strict=True):
            if key not in known:
                known[key] = []  # placeholder, so a repeated text is queued once
                pending.append((key, text))
        for start in range(0, len(pending), BATCH_SIZE):
            batch = pending[start : start + BATCH_SIZE]
            vectors = self._request([text for _, text in batch])
            rows = [(key, vec) for (key, _), vec in zip(batch, vectors, strict=True)]
            self.cache.put_many(self.label, rows)
            for key, vec in rows:
                # Use the float32 value that the cache returns, so fresh and cached vectors agree.
                known[key] = _unpack(pack_vector(vec), len(vec))
        return [known[key] for key in keys]

    def _request(self, inputs: Sequence[str]) -> list[list[float]]:
        body = json.dumps({"input": list(inputs), "model": self.model}).encode("utf-8")
        request = urllib.request.Request(
            f"{self.base_url}/embeddings",
            data=body,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        # Bypass any proxy: the embedding server is on loopback.
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        try:
            with opener.open(request, timeout=TIMEOUT_S) as response:
                raw: object = json.loads(response.read().decode("utf-8"))
        except urllib.error.HTTPError as error:
            if 400 <= error.code < 500:
                detail = error.read().decode("utf-8", "replace")[:500]
                raise EmbedRejected(error.code, detail) from error
            raise
        return _parse_vectors(raw, len(inputs))
