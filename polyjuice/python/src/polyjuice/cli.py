import argparse
import sys
from pathlib import Path

from polyjuice.pipeline import TARGETS, run


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="polyjuice", description="Source-to-source compiler")
    parser.add_argument("source", type=Path, help="Python-subset source file")
    parser.add_argument("--target", choices=sorted(TARGETS), default="c")
    parser.add_argument(
        "--out",
        type=Path,
        help="directory for per-stage artifacts (default: polyjuice-out/<source stem>)",
    )
    args = parser.parse_args(argv)
    out = args.out or Path("polyjuice-out") / args.source.stem
    result = run(args.source.read_text(), args.target, out)
    for stage in result.stages:
        print(f"{stage.file:<20} {stage.seconds * 1000:8.1f} ms  {stage.bytes:>7} B", file=sys.stderr)
    print(f"artifacts in {out}", file=sys.stderr)
    sys.stdout.write(result.output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
