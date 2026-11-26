"""Starts a fake llama-server (a script that answers /health), then checks the supervisor's
start, readiness, status and stop. No model is loaded.

Run from the py/ folder:  python -m unittest tests.test_supervisor -v
"""

from __future__ import annotations

import socket
import stat
import sys
import tempfile
import unittest
from pathlib import Path

from vinci_infer.config import ServerSpec, Settings
from vinci_infer.supervisor import Supervisor, is_healthy

FAKE_SERVER = '''\
import http.server, sys
port = int(sys.argv[sys.argv.index("--port") + 1])
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        self.send_response(200); self.send_header("Content-Length", "2"); self.end_headers()
        self.wfile.write(b"ok")
http.server.ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()
'''


def _free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return int(s.getsockname()[1])


class SupervisorTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp(prefix="vinci-infer-test-"))
        script = self.tmp / "fake-llama-server"
        script.write_text(f"#!/bin/sh\nexec {sys.executable} {self.tmp / 'fake.py'} \"$@\"\n")
        (self.tmp / "fake.py").write_text(FAKE_SERVER)
        script.chmod(script.stat().st_mode | stat.S_IEXEC)
        self.port = _free_port()
        self.settings = Settings(
            binary=str(script), host="127.0.0.1", front_port=0,
            chat=ServerSpec(alias="fake", model_path="/none.gguf", port=self.port,
                            ctx=16384, parallel=1, gpu_layers=999),
            embed=None, state_dir=self.tmp / "state", startup_timeout_s=20,
        )

    def tearDown(self) -> None:
        if getattr(self, "_sup", None) is not None:
            self._sup.stop_all()

    def test_start_wait_status_and_stop(self) -> None:
        sup = Supervisor(self.settings)
        self._sup = sup
        sup.start_all()
        sup.wait_ready()
        self.assertTrue(is_healthy("127.0.0.1", self.port))
        row = sup.status()["fake"]
        assert isinstance(row, dict)
        self.assertTrue(row["healthy"])
        pid = row["pid"]
        self.assertTrue(Path(f"/proc/{pid}").exists())
        log = (self.tmp / "state" / "logs" / "fake.log")
        self.assertTrue(log.exists())

        sup.stop_all()
        self.assertFalse(is_healthy("127.0.0.1", self.port))
        self.assertEqual(sup.children, [])

    def test_child_that_exits_is_reported(self) -> None:
        bad = self.tmp / "bad-llama-server"
        bad.write_text("#!/bin/sh\nexit 7\n")
        bad.chmod(bad.stat().st_mode | stat.S_IEXEC)
        settings = Settings(
            binary=str(bad), host="127.0.0.1", front_port=0,
            chat=self.settings.chat, embed=None, state_dir=self.tmp / "state",
            startup_timeout_s=20,
        )
        sup = Supervisor(settings)
        sup.start_all()
        try:
            with self.assertRaises(RuntimeError) as ctx:
                sup.wait_ready()
            self.assertIn("exited with code 7", str(ctx.exception))
        finally:
            sup.stop_all()


if __name__ == "__main__":
    unittest.main()
