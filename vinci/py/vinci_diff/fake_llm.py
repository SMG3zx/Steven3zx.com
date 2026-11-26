"""A scripted stand-in for llama-server's /v1/chat/completions, for the chat differential test.

Each request gets the next scripted reply (text, or a JSON tool action), and every request body
is kept, so a test can check what the model was shown. Once the script runs out, the reply is
a fixed sentence. Stdlib only, and bound to 127.0.0.1 on a port the OS picks.
"""

from __future__ import annotations

import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Final, cast

EXHAUSTED: Final = "Nothing more to add."


class FakeLlm:
    """One scripted chat endpoint. Start it, point the chat at base_url, then stop it."""

    def __init__(self, replies: list[str]) -> None:
        self._replies: list[str] = list(replies)
        self._lock = threading.Lock()
        self.requests: list[dict[str, object]] = []
        self._server = ThreadingHTTPServer(("127.0.0.1", 0), _handler_for(self))
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)

    @property
    def base_url(self) -> str:
        """The base URL for CHAT_BASE_URL, ending in /v1."""
        host, port = self._server.server_address[:2]
        return f"http://{host}:{port}/v1"

    def start(self) -> None:
        self._thread.start()

    def stop(self) -> None:
        self._server.shutdown()
        self._server.server_close()

    def next_reply(self, body: dict[str, object]) -> str:
        """Records one request and returns the next scripted reply."""
        with self._lock:
            self.requests.append(body)
            if self._replies:
                return self._replies.pop(0)
            return EXHAUSTED

    def contents(self) -> str:
        """Every message content the model was sent, joined: what the model could read."""
        parts: list[str] = []
        for body in self.requests:
            messages = body.get("messages")
            if not isinstance(messages, list):
                continue
            for message in cast("list[object]", messages):
                if isinstance(message, dict):
                    content = cast("dict[str, object]", message).get("content")
                    if isinstance(content, str):
                        parts.append(content)
        return "\n".join(parts)


def _read_body(handler: BaseHTTPRequestHandler) -> bytes:
    """The request body, whether it has a Content-Length or is chunked (the .NET client sends
    chunked)."""
    if handler.headers.get("Transfer-Encoding", "").lower() == "chunked":
        data = bytearray()
        while True:
            size = int(handler.rfile.readline().split(b";")[0].strip(), 16)
            if size == 0:
                handler.rfile.readline()
                return bytes(data)
            data += handler.rfile.read(size)
            handler.rfile.readline()
    return handler.rfile.read(int(handler.headers.get("Content-Length", "0")))


def _handler_for(fake: FakeLlm) -> type[BaseHTTPRequestHandler]:
    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:
            raw: object = json.loads(_read_body(self).decode("utf-8"))
            body: dict[str, object] = (
                cast("dict[str, object]", raw) if isinstance(raw, dict) else {}
            )
            reply = fake.next_reply(body)
            payload = json.dumps(
                {"choices": [{"message": {"role": "assistant", "content": reply}}]}
            ).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def log_message(self, format: str, *args: Any) -> None:
            return

    return Handler
