"""Command line for the inference service.

    python -m vinci_infer plan     # print what would start, without starting anything
    python -m vinci_infer serve    # start llama-server processes and the front door (foreground)
    python -m vinci_infer health   # ask a running front door for its health

`serve` runs until Ctrl-C or SIGTERM, then stops the llama-server processes it started.
"""

from __future__ import annotations

import json
import signal
import sys
import urllib.error
import urllib.request

from .config import load_settings
from .proxy import make_server
from .supervisor import Supervisor, describe

USAGE = __doc__ or ""


def _on_term(signum: int, frame: object) -> None:
    raise KeyboardInterrupt


def serve() -> int:
    settings = load_settings()
    supervisor = Supervisor(settings)
    signal.signal(signal.SIGTERM, _on_term)
    try:
        supervisor.start_all()
        print("waiting for models to load (see the logs in "
              f"{settings.state_dir / 'logs'})", flush=True)
        supervisor.wait_ready()
        server = make_server(settings)
        print(f"ready on http://{settings.host}:{settings.front_port}/v1", flush=True)
        try:
            server.serve_forever()
        finally:
            server.server_close()
    except KeyboardInterrupt:
        print("stopping", flush=True)
        return 0
    except (RuntimeError, TimeoutError, OSError) as err:
        print(f"vinci_infer: {err}", file=sys.stderr)
        return 3
    finally:
        supervisor.stop_all()
    return 0


def health() -> int:
    settings = load_settings()
    url = f"http://{settings.host}:{settings.front_port}/health"
    try:
        with urllib.request.urlopen(url, timeout=5) as reply:
            body: object = json.loads(reply.read().decode("utf-8"))
            print(json.dumps(body, indent=2))
            return 0
    except urllib.error.HTTPError as err:
        print(err.read().decode("utf-8", errors="replace"))
        return 1
    except (urllib.error.URLError, OSError) as err:
        print(f"no front door at {url}: {err}", file=sys.stderr)
        return 1


def main(argv: list[str]) -> int:
    command = argv[0] if argv else "help"
    if command == "plan":
        print(describe(load_settings()))
        return 0
    if command == "serve":
        return serve()
    if command == "health":
        return health()
    print(USAGE.strip())
    return 0 if command in ("help", "-h", "--help") else 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
