"""Tests for the vinci inference service. Stdlib only; no model is loaded.

Run from the py/ folder:  python -m unittest tests.test_vinci_infer -v
"""

from __future__ import annotations

import json
import threading
import unittest
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from vinci_infer.config import ServerSpec, build_argv, load_settings
from vinci_infer.proxy import Route, make_server, pick_route


def _chat_spec(port: int = 8180) -> ServerSpec:
    return ServerSpec(alias="qwen3.5-9b", model_path="/m/chat.gguf", port=port,
                      ctx=16384, parallel=1, gpu_layers=999)


class ArgvTest(unittest.TestCase):
    def test_chat_command_line_puts_every_layer_on_gpu_with_16k_context(self) -> None:
        argv = build_argv("llama-server", _chat_spec(), "127.0.0.1")
        self.assertEqual(argv[0], "llama-server")
        self.assertEqual(argv[argv.index("-ngl") + 1], "999")
        self.assertEqual(argv[argv.index("-c") + 1], "16384")
        self.assertEqual(argv[argv.index("-np") + 1], "1")
        self.assertEqual(argv[argv.index("--port") + 1], "8180")
        self.assertNotIn("--embeddings", argv)
        self.assertNotIn("-dev", argv)
        self.assertIn("--flash-attn", argv)

    def test_embedding_command_line_runs_on_cpu(self) -> None:
        spec = ServerSpec(alias="emb", model_path="/m/e.gguf", port=8181, ctx=8192,
                          parallel=1, gpu_layers=0, embedding=True)
        argv = build_argv("llama-server", spec, "127.0.0.1")
        self.assertIn("--embeddings", argv)
        self.assertEqual(argv[argv.index("-dev") + 1], "none")
        self.assertEqual(argv[argv.index("-ngl") + 1], "0")
        self.assertNotIn("--flash-attn", argv)

    def test_extra_arguments_are_appended(self) -> None:
        spec = ServerSpec(alias="x", model_path="/m", port=1, ctx=2, parallel=1, gpu_layers=3,
                          extra=("--metrics",))
        self.assertEqual(build_argv("b", spec, "h")[-1], "--metrics")


class SettingsTest(unittest.TestCase):
    def test_defaults_match_this_machine(self) -> None:
        settings = load_settings({})
        self.assertEqual(settings.front_port, 8080)
        self.assertEqual(settings.chat.ctx, 16384)
        self.assertEqual(settings.chat.parallel, 1)
        self.assertEqual(settings.chat.gpu_layers, 999)
        self.assertEqual(settings.chat.port, 8180)
        assert settings.embed is not None
        self.assertEqual(settings.embed.port, 8181)
        self.assertEqual(settings.embed.alias, "qwen3-embedding:4b")

    def test_environment_overrides(self) -> None:
        settings = load_settings({"VINCI_CHAT_CTX": "32768", "VINCI_EMBED_GGUF": "",
                                  "VINCI_INFER_PORT": "9000"})
        self.assertEqual(settings.chat.ctx, 32768)
        self.assertIsNone(settings.embed)
        self.assertEqual(settings.front_port, 9000)

    def test_non_positive_number_is_rejected(self) -> None:
        with self.assertRaises(ValueError):
            load_settings({"VINCI_CHAT_CTX": "0"})


class PickRouteTest(unittest.TestCase):
    routes = [Route("chat", 8180, embedding=False), Route("emb", 8181, embedding=True)]

    def test_model_field_decides(self) -> None:
        route, message = pick_route("/v1/embeddings", b'{"model": "chat"}', self.routes)
        self.assertEqual((route and route.alias, message), ("chat", ""))

    def test_unknown_model_is_refused_with_the_served_names(self) -> None:
        route, message = pick_route("/v1/chat/completions", b'{"model": "nope"}', self.routes)
        self.assertIsNone(route)
        self.assertIn("chat, emb", message)

    def test_missing_model_goes_by_path(self) -> None:
        chat, _ = pick_route("/v1/chat/completions", b"{}", self.routes)
        emb, _ = pick_route("/v1/embeddings", b"{}", self.routes)
        assert chat is not None and emb is not None
        self.assertEqual((chat.alias, emb.alias), ("chat", "emb"))

    def test_bad_json_and_non_string_model_are_refused(self) -> None:
        self.assertIsNone(pick_route("/v1/chat/completions", b"{", self.routes)[0])
        self.assertIsNone(pick_route("/v1/chat/completions", b'{"model": 3}', self.routes)[0])


class _FakeUpstream(BaseHTTPRequestHandler):
    """Answers chat like llama-server: JSON for a plain request, a streamed body when asked."""

    protocol_version = "HTTP/1.0"

    def log_message(self, format: str, *args: object) -> None:
        return

    def do_GET(self) -> None:
        self._reply(200, b'{"status":"ok"}', "application/json")

    def do_POST(self) -> None:
        length = int(self.headers.get("Content-Length", "0"))
        request = json.loads(self.rfile.read(length) or b"{}")
        if request.get("stream"):
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            for piece in ("one", "two"):
                self.wfile.write(f"data: {piece}\n\n".encode())
                self.wfile.flush()
            return
        reply = {"echo": request.get("model"), "path": self.path}
        self._reply(200, json.dumps(reply).encode(), "application/json")

    def _reply(self, status: int, data: bytes, content_type: str) -> None:
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


class FrontDoorTest(unittest.TestCase):
    def setUp(self) -> None:
        self.upstream = ThreadingHTTPServer(("127.0.0.1", 0), _FakeUpstream)
        threading.Thread(target=self.upstream.serve_forever, daemon=True).start()
        up_port = self.upstream.server_address[1]
        settings = load_settings({"VINCI_INFER_PORT": "0", "VINCI_CHAT_PORT": str(up_port),
                                  "VINCI_EMBED_GGUF": "", "VINCI_CHAT_ALIAS": "chat"})
        self.front = make_server(settings)
        self.front_port = self.front.server_address[1]
        threading.Thread(target=self.front.serve_forever, daemon=True).start()
        self.base = f"http://127.0.0.1:{self.front_port}"

    def tearDown(self) -> None:
        self.front.shutdown()
        self.front.server_close()
        self.upstream.shutdown()
        self.upstream.server_close()

    def _post(self, path: str, payload: dict[str, object]) -> tuple[int, bytes]:
        request = urllib.request.Request(
            self.base + path, data=json.dumps(payload).encode(),
            headers={"Content-Type": "application/json"}, method="POST")
        try:
            with urllib.request.urlopen(request, timeout=10) as reply:
                return reply.status, reply.read()
        except urllib.error.HTTPError as err:
            body = err.read()
            err.close()
            return err.code, body

    def test_chat_request_reaches_the_upstream(self) -> None:
        status, body = self._post("/v1/chat/completions", {"model": "chat", "messages": []})
        self.assertEqual(status, 200)
        self.assertEqual(json.loads(body)["echo"], "chat")

    def test_streamed_reply_is_passed_through(self) -> None:
        status, body = self._post("/v1/chat/completions",
                                  {"model": "chat", "messages": [], "stream": True})
        self.assertEqual(status, 200)
        self.assertEqual(body, b"data: one\n\ndata: two\n\n")

    def test_unknown_model_is_404(self) -> None:
        status, body = self._post("/v1/chat/completions", {"model": "other"})
        self.assertEqual(status, 404)
        self.assertIn("unknown model", json.loads(body)["error"])

    def test_models_list_names_the_alias(self) -> None:
        with urllib.request.urlopen(self.base + "/v1/models", timeout=10) as reply:
            data = json.loads(reply.read())
        self.assertEqual([m["id"] for m in data["data"]], ["chat"])

    def test_health_reports_the_upstream(self) -> None:
        with urllib.request.urlopen(self.base + "/health", timeout=10) as reply:
            data = json.loads(reply.read())
        self.assertTrue(data["ok"])
        self.assertTrue(data["models"]["chat"]["healthy"])

    def test_dead_upstream_gives_503(self) -> None:
        self.upstream.shutdown()
        self.upstream.server_close()
        status, body = self._post("/v1/chat/completions", {"model": "chat"})
        self.assertEqual(status, 503)
        self.assertIn("not answering", json.loads(body)["error"])


if __name__ == "__main__":
    unittest.main()
