"""The front door: one local HTTP port that routes OpenAI-style requests to llama-server.

Routing is by the request's "model" field, which must match a configured alias. A request
without a model goes to chat, or to embeddings when the path is /v1/embeddings. Responses
are streamed through as they arrive, so a streamed chat reply is not held back.

The front door binds to the host in the settings (127.0.0.1 by default) and has no
authentication, so it is only reachable from this machine unless the host is changed.
"""

from __future__ import annotations

import http.client
import json
import socket
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Final

from .config import Settings

UPSTREAM_TIMEOUT_S: Final = 600
CHUNK_BYTES: Final = 8192
MAX_BODY_BYTES: Final = 64 * 1024 * 1024
EMBEDDINGS_PATH: Final = "/v1/embeddings"


class Route:
    """An alias served on a local port, and whether it is the embedding model."""

    def __init__(self, alias: str, port: int, embedding: bool) -> None:
        self.alias: str = alias
        self.port: int = port
        self.embedding: bool = embedding


def routes_for(settings: Settings) -> list[Route]:
    routes = [Route(settings.chat.alias, settings.chat.port, embedding=False)]
    if settings.embed is not None:
        routes.append(Route(settings.embed.alias, settings.embed.port, embedding=True))
    return routes


def pick_route(path: str, body: bytes, routes: list[Route]) -> tuple[Route | None, str]:
    """The route for a request, or (None, message) when no route can take it.

    The "model" field decides when present. Without it, embeddings go to the embedding
    route and everything else goes to the chat route.
    """
    model: object = None
    if body:
        try:
            parsed: object = json.loads(body)
        except ValueError:
            return (None, "request body is not JSON")
        if isinstance(parsed, dict):
            model = parsed.get("model")
    if model is not None:
        if not isinstance(model, str):
            return (None, "model must be a string")
        for route in routes:
            if route.alias == model:
                return (route, "")
        names = ", ".join(r.alias for r in routes)
        return (None, f"unknown model {model!r}; served models: {names}")
    wants_embedding = path == EMBEDDINGS_PATH
    for route in routes:
        if route.embedding == wants_embedding:
            return (route, "")
    return (None, "no model is configured for this request")


def _json_bytes(payload: object) -> bytes:
    return json.dumps(payload, ensure_ascii=False).encode("utf-8")


class FrontDoorServer(ThreadingHTTPServer):
    daemon_threads: bool = True
    allow_reuse_address: bool = True

    def __init__(self, address: tuple[str, int], settings: Settings) -> None:
        self.settings: Settings = settings
        self.routes: list[Route] = routes_for(settings)
        super().__init__(address, FrontDoorHandler)


class FrontDoorHandler(BaseHTTPRequestHandler):
    server: FrontDoorServer
    server_version: str = "vinci-infer"

    def log_message(self, format: str, *args: object) -> None:
        # Request logging goes to the service's own log; keep the terminal quiet.
        return

    def _send_json(self, status: int, payload: object) -> None:
        data = _json_bytes(payload)
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self) -> None:
        if self.path == "/health":
            self._health()
        elif self.path in ("/v1/models", "/models"):
            data = [{"id": r.alias, "object": "model"} for r in self.server.routes]
            self._send_json(200, {"object": "list", "data": data})
        else:
            self._send_json(404, {"error": f"no route for GET {self.path}"})

    def do_POST(self) -> None:
        if not self.path.startswith("/v1/"):
            self._send_json(404, {"error": f"no route for POST {self.path}"})
            return
        length_text = self.headers.get("Content-Length", "0")
        try:
            length = int(length_text)
        except ValueError:
            self._send_json(400, {"error": "bad Content-Length"})
            return
        if length < 0 or length > MAX_BODY_BYTES:
            self._send_json(413, {"error": "request body is too large or has a bad length"})
            return
        body = self.rfile.read(length) if length else b""
        route, message = pick_route(self.path, body, self.server.routes)
        if route is None:
            self._send_json(404, {"error": message})
            return
        self._forward(route, body)

    def _health(self) -> None:
        rows: dict[str, object] = {}
        ok = True
        for route in self.server.routes:
            healthy = _upstream_healthy(self.server.settings.host, route.port)
            rows[route.alias] = {"port": route.port, "healthy": healthy}
            if not healthy and not route.embedding:
                ok = False
        self._send_json(200 if ok else 503, {"ok": ok, "models": rows})

    def _forward(self, route: Route, body: bytes) -> None:
        host = self.server.settings.host
        connection = http.client.HTTPConnection(host, route.port, timeout=UPSTREAM_TIMEOUT_S)
        headers = {"Content-Type": self.headers.get("Content-Type", "application/json")}
        try:
            connection.request("POST", self.path, body=body, headers=headers)
            upstream = connection.getresponse()
        except (OSError, http.client.HTTPException) as err:
            connection.close()
            self._send_json(
                503,
                {"error": f"{route.alias} is not answering on port {route.port}: {err}"},
            )
            return
        try:
            self.send_response(upstream.status)
            content_type = upstream.getheader("Content-Type")
            if content_type:
                self.send_header("Content-Type", content_type)
            length = upstream.getheader("Content-Length")
            if length:
                self.send_header("Content-Length", length)
            self.end_headers()
            while True:
                chunk = upstream.read(CHUNK_BYTES)
                if not chunk:
                    break
                self.wfile.write(chunk)
                self.wfile.flush()
        except (OSError, http.client.HTTPException):
            # The client or the model went away mid-reply; nothing more can be sent.
            return
        finally:
            connection.close()


def _upstream_healthy(host: str, port: int) -> bool:
    try:
        connection = http.client.HTTPConnection(host, port, timeout=2)
        connection.request("GET", "/health")
        status = connection.getresponse().status
        connection.close()
        return status == 200
    except (OSError, http.client.HTTPException, socket.timeout):
        return False


def make_server(settings: Settings) -> FrontDoorServer:
    """The front door bound to the configured host and port. Port 0 picks a free port."""
    return FrontDoorServer((settings.host, settings.front_port), settings)
