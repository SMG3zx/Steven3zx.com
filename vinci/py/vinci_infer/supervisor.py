"""Starts llama-server processes, waits until each one answers, and stops them.

Each process writes its output to a log file under the state folder. The supervisor only
manages the processes it started; it never kills a process it did not start.
"""

from __future__ import annotations

import json
import subprocess
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import IO

from .config import ServerSpec, Settings, build_argv

STOP_GRACE_S: float = 15.0
HEALTH_TIMEOUT_S: float = 2.0


@dataclass
class Child:
    spec: ServerSpec
    process: subprocess.Popen[bytes]
    log: IO[bytes]
    log_path: Path


def health_url(host: str, port: int) -> str:
    return f"http://{host}:{port}/health"


def is_healthy(host: str, port: int) -> bool:
    """True when the process on host:port answers /health with HTTP 200."""
    try:
        with urllib.request.urlopen(health_url(host, port), timeout=HEALTH_TIMEOUT_S) as reply:
            return reply.status == 200
    except (urllib.error.URLError, OSError, ValueError):
        return False


class Supervisor:
    def __init__(self, settings: Settings) -> None:
        self.settings: Settings = settings
        self.children: list[Child] = []

    def specs(self) -> list[ServerSpec]:
        specs = [self.settings.chat]
        if self.settings.embed is not None:
            specs.append(self.settings.embed)
        return specs

    def start(self, spec: ServerSpec) -> Child:
        """Launch one process. Its log is state_dir/logs/<alias>.log, replaced on each start."""
        log_dir = self.settings.state_dir / "logs"
        log_dir.mkdir(parents=True, exist_ok=True)
        log_path = log_dir / f"{_safe_name(spec.alias)}.log"
        log = log_path.open("wb")
        argv = build_argv(self.settings.binary, spec, self.settings.host)
        process = subprocess.Popen(
            argv,
            stdout=log,
            stderr=subprocess.STDOUT,
            stdin=subprocess.DEVNULL,
            start_new_session=True,
        )
        child = Child(spec=spec, process=process, log=log, log_path=log_path)
        self.children.append(child)
        return child

    def start_all(self) -> None:
        for spec in self.specs():
            self.start(spec)

    def wait_ready(self) -> None:
        """Block until every child answers /health. Raises if one exits or time runs out."""
        deadline = time.monotonic() + self.settings.startup_timeout_s
        pending = list(self.children)
        while pending:
            if time.monotonic() > deadline:
                names = ", ".join(c.spec.alias for c in pending)
                raise TimeoutError(f"not ready after {self.settings.startup_timeout_s:.0f}s: {names}")
            for child in list(pending):
                code = child.process.poll()
                if code is not None:
                    raise RuntimeError(
                        f"{child.spec.alias} exited with code {code}; see {child.log_path}"
                    )
                if is_healthy(self.settings.host, child.spec.port):
                    pending.remove(child)
            if pending:
                time.sleep(1.0)

    def stop_all(self) -> None:
        """Ask each child to exit, wait, then force it if it has not. Closes the logs."""
        for child in self.children:
            if child.process.poll() is None:
                child.process.terminate()
        for child in self.children:
            try:
                child.process.wait(timeout=STOP_GRACE_S)
            except subprocess.TimeoutExpired:
                child.process.kill()
                child.process.wait()
            child.log.close()
        self.children = []

    def status(self) -> dict[str, object]:
        """Each child's alias, port, whether it is running, and whether it answers /health."""
        rows: dict[str, object] = {}
        for child in self.children:
            running = child.process.poll() is None
            rows[child.spec.alias] = {
                "port": child.spec.port,
                "pid": child.process.pid,
                "running": running,
                "healthy": running and is_healthy(self.settings.host, child.spec.port),
            }
        return rows


def _safe_name(alias: str) -> str:
    return "".join(ch if ch.isalnum() or ch in "-_." else "_" for ch in alias)


def describe(settings: Settings) -> str:
    """The plan as JSON, for a dry run: what would be started and on which port."""
    plan = {
        "front_door": f"http://{settings.host}:{settings.front_port}/v1",
        "processes": [
            {"alias": s.alias, "port": s.port, "argv": build_argv(settings.binary, s, settings.host)}
            for s in ([settings.chat] + ([settings.embed] if settings.embed else []))
        ],
    }
    return json.dumps(plan, indent=2)
