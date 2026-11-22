# The vector route's data: the vectors of one label, loaded as a Matrix, and the
# candidates they give. The vectors come from the index's `vectors` table, written by
# `vinci index embed`. Bytes move from Python to Mojo by address, not float by float.

from std.python import Python, PythonObject
from std.memory import Pointer

from fusion import Candidate
from index_store import IndexStore, py_params
from embed_edge import EmbedConfig, embed_documents, embed_question
from vector_scan import Matrix, normalize, scan_simd

comptime FLOAT_BYTES = 4


def address_of(blob: PythonObject) raises -> Int:
    """The address of a bytes object's buffer. Valid only while the object is alive, so
    never keep it past the call that owns the object. Use insert_blob or floats_of, which
    take the object as an argument and read it inside the same call.
    """
    var ctypes = Python.import_module("ctypes")
    return Int(
        String(ctypes.cast(ctypes.c_char_p(blob), ctypes.c_void_p).value)
    )


def insert_blob(mut matrix: Matrix, blob: PythonObject) raises:
    """Append one row from a packed float32 bytes object.

    The copy reads the buffer while `blob` is borrowed, so the caller's reference is
    alive for the whole copy. Use this function instead of taking the address directly,
    to avoid using freed memory.
    """
    matrix.insert_from_address(address_of(blob))


def floats_of(blob: PythonObject) raises -> List[Float32]:
    """The float32 values in a packed bytes object, unnormalised."""
    var builtins = Python.import_module("builtins")
    var count = Int(String(builtins.len(blob))) // FLOAT_BYTES
    var out = List[Float32](length=count, fill=0)
    var src = Pointer[Float32, MutAnyOrigin](
        unsafe_from_address=address_of(blob)
    )
    var dst = out.unsafe_ptr()
    for i in range(count):
        dst[unsafe_offset=i] = src[unsafe_offset=i]
    return out^


def embed_text_for_query(
    cfg: EmbedConfig, question: String
) raises -> List[Float32]:
    """The normalised query vector, with the instruction (the cache key is the text).
    """
    var blob = embed_question(cfg, question)
    var v = floats_of(blob)
    normalize(v)
    return v^


struct VectorIndex(Movable):
    """The vectors of one label whose facts are eligible for recall, as one matrix.

    Eligible means the same as the lexical route: current and active, or (for the
    temporal route) superseded but active. meta[i] is the candidate for row i.
    """

    var label: String
    var matrix: Matrix
    var meta: List[Candidate]
    var position: Dict[Int, Int]

    def __init__(out self, label: String):
        self.label = label
        self.matrix = Matrix(0)
        self.meta = List[Candidate]()
        self.position = Dict[Int, Int]()

    def count(self) -> Int:
        return self.matrix.count

    def search(self, query: List[Float32], k: Int) raises -> List[Candidate]:
        """The k nearest eligible facts by cosine, best first, as vector candidates.
        """
        var out = List[Candidate]()
        if self.matrix.count == 0:
            return out^
        var hits = scan_simd(self.matrix, query, k)
        for hit in hits:
            var c = self.meta[hit.index].copy()
            c.in_vector = True
            c.has_cosine = True
            c.cosine = Float64(hit.score)
            out.append(c^)
        return out^

    def cosine_of(
        self, rowid: Int, query: List[Float32]
    ) raises -> Optional[Float64]:
        """The cosine of a fact's stored vector and the normalised query, if it has one.
        """
        if rowid not in self.position:
            return None
        var row = self.position[rowid]
        var dim = self.matrix.dim
        var acc: Float64 = 0
        for j in range(dim):
            acc += Float64(self.matrix.data[row * dim + j]) * Float64(query[j])
        return Optional[Float64](acc)


def load_vectors(
    store: IndexStore, label: String, include_superseded: Bool
) raises -> VectorIndex:
    """Read the label's vectors for the eligible facts, in rowid order."""
    var out = VectorIndex(label)
    var status_clause = "f.valid_until IS NULL AND f.status = 'active'"
    if include_superseded:
        status_clause = "f.status IN ('active', 'superseded')"
    var rows = store.conn.execute(
        (
            "SELECT f.rowid, f.cite, f.kind, f.text, f.at, f.valid_from,"
            " f.source, f.trusted, v.dims, v.vec FROM vectors AS v JOIN facts"
            " AS f ON f.rowid = v.fact WHERE v.label = ? AND "
            + status_clause
            + " ORDER BY f.rowid"
        ),
        py_params(PythonObject(label)),
    ).fetchall()
    var dim = -1
    for row in rows:
        var dims = Int(String(row[8]))
        if dim < 0:
            dim = dims
            out.matrix = Matrix(dim)
        if dims != dim:
            raise Error("vectors: label " + label + " has mixed dimensions")
        var rowid = Int(String(row[0]))
        var c = Candidate(
            rowid,
            String(row[1]),
            String(row[2]),
            String(row[3]),
            String(row[4]),
            String(row[5]),
            String(row[6]),
            String(row[7]) == "1",
        )
        insert_blob(out.matrix, row[9])
        out.position[rowid] = len(out.meta)
        out.meta.append(c^)
    return out^


comptime EMBED_CHUNK = 128


def embed_missing(store: IndexStore, cfg: EmbedConfig) raises -> Int:
    """vinci index embed: vectors for every active fact the label does not have yet.

    Returns how many facts still have no vector. A chunk that fails (the server is
    unreachable, or rejects a text as too long) is retried one text at a time, so a
    text that cannot be embedded is left without a vector and counted, instead of
    stopping the run. Chunks are committed as they finish, so a stopped run resumes.
    The texts are the facts' own text, sent as documents (no instruction).
    """
    var missing = store.missing_vectors(cfg.label)
    var builtins = Python.import_module("builtins")
    var cache = cfg.cache
    if cache.byte_length() == 0:
        cache = "~/.vinci/index/embed-cache.sqlite (default)"
    if len(missing) == 0:
        print("embed " + cfg.label + ": every active fact already has a vector")
        return 0
    print(
        "embed "
        + cfg.label
        + ": "
        + String(len(missing))
        + " facts to embed via "
        + cfg.base_url
        + ", cache "
        + cache
    )
    var left_out = 0
    var start = 0
    while start < len(missing):
        var end = start + EMBED_CHUNK
        if end > len(missing):
            end = len(missing)
        var texts = List[String]()
        var ids = List[Int]()
        for i in range(start, end):
            texts.append(missing[i].text)
            ids.append(missing[i].rowid)
        var blobs = List[PythonObject]()
        var batch_ok = True
        try:
            for blob in embed_documents(cfg, texts):
                blobs.append(blob)
        except:
            batch_ok = False
        var keep_ids = List[Int]()
        if batch_ok:
            keep_ids = ids^
        else:
            # The batch failed: embed each text alone. Cached texts are still hits,
            # so only the uncached ones reach the server.
            blobs = List[PythonObject]()
            for k in range(len(texts)):
                var one = List[String]()
                one.append(texts[k])
                try:
                    for blob in embed_documents(cfg, one):
                        blobs.append(blob)
                        keep_ids.append(ids[k])
                except:
                    left_out += 1
        store.begin()
        for k in range(len(blobs)):
            var dims = Int(String(builtins.len(blobs[k]))) // FLOAT_BYTES
            store.put_vector(keep_ids[k], cfg.label, blobs[k], dims)
        store.commit()
        start = end
        print("  " + String(start) + "/" + String(len(missing)))
    if left_out > 0:
        print(
            "embed: "
            + String(left_out)
            + " facts have no vector (text not cached, and the server failed or"
            " rejected it)"
        )
    return left_out
