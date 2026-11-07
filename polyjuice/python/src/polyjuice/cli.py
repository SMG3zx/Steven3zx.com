import argparse
import sys
from pathlib import Path

from polyjuice.languages import EMITTERS, FRONTENDS, UnsupportedPipeline, language_for_path, pipelines
from polyjuice.pipeline import run


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="polyjuice", description="Source-to-source compiler")
    parser.add_argument("source", type=Path, nargs="?", help="source file")
    parser.add_argument("--from", dest="source_language", help="source language (default: from extension)")
    parser.add_argument("--to", dest="target", default="c", help="target language (default: c)")
    parser.add_argument("--list", action="store_true", help="list supported languages and pipelines")
    parser.add_argument(
        "--out",
        type=Path,
        help="directory for per-stage artifacts (default: polyjuice-out/<source stem>)",
    )
    args = parser.parse_args(argv)
    if args.list:
        print("frontends:", ", ".join(sorted(FRONTENDS)))
        print("emitters: ", ", ".join(sorted(EMITTERS)))
        print("pipelines:", ", ".join(f"{s}->{t}" for s, t in pipelines()))
        return 0
    if args.source is None:
        parser.error("source file required")
    source_language = args.source_language or language_for_path(str(args.source))
    if source_language is None:
        parser.error(f"cannot infer source language from {args.source.name}; pass --from")
    out = args.out or Path("polyjuice-out") / args.source.stem
    try:
        result = run(args.source.read_text(), args.target, out, source_language)
    except UnsupportedPipeline as error:
        parser.error(str(error))
    for stage in result.stages:
        print(f"{stage.file:<20} {stage.seconds * 1000:8.1f} ms  {stage.bytes:>7} B", file=sys.stderr)
    print(f"artifacts in {out}", file=sys.stderr)
    sys.stdout.write(result.output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
