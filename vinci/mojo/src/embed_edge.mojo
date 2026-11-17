# The Python edge of the index's vectors: calls into vinci_edge.index_embed, which
# goes through vinci_edge.embed (the same client and cache as lme_embed.py and the
# C# side). Interop is kept to a few calls per command. Floats cross as bytes.

from std.python import Python, PythonObject
from std.sys import argv

comptime DEFAULT_BASE_URL = "http://127.0.0.1:8081/v1"
comptime DEFAULT_MODEL = "qwen3-embedding:4b"


struct EmbedConfig(Copyable, Movable):
    """Where vectors come from. label names the vector set (the model, or a device
    label such as qwen3-embedding:4b@gpu). cache "" is vinci_edge's default cache.
    """

    var base_url: String
    var model: String
    var label: String
    var cache: String
    var instruction: String
    # Query vectors only: a question missing under label is looked up under
    # fallback_label, then embedded at fallback_url and cached under fallback_label.
    # Empty fallback_url means no fallback. Documents never use these.
    var fallback_label: String
    var fallback_url: String

    def __init__(
        out self,
        var base_url: String,
        var model: String,
        var label: String,
        var cache: String,
        var instruction: String,
    ):
        self.base_url = base_url^
        self.model = model^
        self.label = label^
        self.cache = cache^
        self.instruction = instruction^
        self.fallback_label = String()
        self.fallback_url = String()

    def set_fallback(mut self, var label: String, var url: String):
        self.fallback_label = label^
        self.fallback_url = url^


def env_or(name: String, fallback: String) raises -> String:
    var os = Python.import_module("os")
    var value = os.environ.get(PythonObject(name), Python.none())
    if Bool(value == Python.none()):
        return fallback
    return String(value)


def default_config() raises -> EmbedConfig:
    """EMBEDDING_BASE_URL and EMBEDDING_MODEL from the environment (memory.env is loaded
    first), else the CPU server and the model name. The label is the model name.
    """
    var model = env_or("EMBEDDING_MODEL", DEFAULT_MODEL)
    return EmbedConfig(
        env_or("EMBEDDING_BASE_URL", DEFAULT_BASE_URL),
        model,
        model,
        "",
        "",
    )


def edge_module() raises -> PythonObject:
    """vinci_edge.index_embed, with mojo/py on sys.path. VINCI_MOJO_PY names that
    directory; otherwise it is ../py from the binary (mojo/build/vinci -> mojo/py).
    """
    var sys = Python.import_module("sys")
    var os = Python.import_module("os")
    var py_dir = env_or("VINCI_MOJO_PY", "")
    if py_dir.byte_length() == 0:
        var argv0 = String(argv()[0])
        var dirname = os.path.dirname(PythonObject(argv0))
        py_dir = String(
            os.path.join(dirname, PythonObject(".."), PythonObject("py"))
        )
    var resolved = String(os.path.abspath(PythonObject(py_dir)))
    var present = False
    for entry in sys.path:
        if String(entry) == resolved:
            present = True
    if not present:
        sys.path.insert(0, PythonObject(resolved))
    return Python.import_module("vinci_edge.index_embed")


def embed_documents(
    cfg: EmbedConfig, texts: List[String]
) raises -> PythonObject:
    """A Python list with one bytes object per text, in order."""
    if len(texts) == 0:
        return Python.list()
    var edge = edge_module()
    var py_texts = Python.list()
    for text in texts:
        py_texts.append(PythonObject(text))
    return edge.documents(
        PythonObject(cfg.base_url),
        PythonObject(cfg.model),
        PythonObject(cfg.label),
        PythonObject(cfg.cache),
        PythonObject(cfg.instruction),
        py_texts,
    )


def embed_question(cfg: EmbedConfig, question: String) raises -> PythonObject:
    """The packed bytes of a question, wrapped with the query instruction. With a
    fallback set, the lookup order is in vinci_edge.index_embed.query_with_fallback.
    """
    var edge = edge_module()
    if cfg.fallback_url.byte_length() > 0:
        return edge.query_with_fallback(
            PythonObject(cfg.base_url),
            PythonObject(cfg.model),
            PythonObject(cfg.label),
            PythonObject(cfg.cache),
            PythonObject(cfg.instruction),
            PythonObject(question),
            PythonObject(cfg.fallback_label),
            PythonObject(cfg.fallback_url),
        )
    return edge.query(
        PythonObject(cfg.base_url),
        PythonObject(cfg.model),
        PythonObject(cfg.label),
        PythonObject(cfg.cache),
        PythonObject(cfg.instruction),
        PythonObject(question),
    )
