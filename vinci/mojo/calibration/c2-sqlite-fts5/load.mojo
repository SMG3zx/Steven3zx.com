from std.python import Python, PythonObject
from std.sys import argv

from index import Index


def main() raises:
    var args = argv()
    if len(args) < 3:
        raise Error("usage: load <journal.jsonl> <db.sqlite> [query ...]")
    var journal_path = String(args[1])
    var db_path = String(args[2])

    var index = Index(db_path)

    # Edge interop: json and hashlib are read from Python. Each line costs a
    # few Python calls; the index itself is still one call per write.
    var builtins = Python.import_module("builtins")
    var json = Python.import_module("json")
    var hashlib = Python.import_module("hashlib")
    var lines = builtins.open(PythonObject(journal_path))
    var indexed = 0
    for pair in builtins.enumerate(lines, 1):
        var lineno = String(pair[0])
        var line = pair[1].strip()
        if not Bool(line):
            continue
        var obj = json.loads(line)
        var text = obj.get("text")
        if text == Python.none():
            continue
        var kind = obj.get("kind", PythonObject(""))
        var at = obj.get("at", PythonObject(""))
        var digest = hashlib.sha256(line.encode()).hexdigest()
        index.add(
            String("journal"),
            String(lineno),
            String(at),
            String(kind),
            String(text),
            String(text),
            String(digest),
        )
        indexed += 1
    lines.close()
    print("indexed", indexed, "journal lines")

    for i in range(3, len(args)):
        var query = String(args[i])
        var hits = index.search(query, 3)
        print('query: "' + query + '" (' + String(len(hits)) + " hits)")
        for hit in hits:
            print(
                "  "
                + hit.source
                + ":"
                + hit.id
                + "  score="
                + String(hit.score)
                + "  "
                + hit.title
            )
