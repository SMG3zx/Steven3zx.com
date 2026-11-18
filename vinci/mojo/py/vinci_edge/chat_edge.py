"""The Python edge of `vinci chat`: the chat model's HTTP call, standard input, and the vinci child
process that runs one journal command for a tool call. Stdlib only.

Mojo calls each function once per request. The functions return a status word with the text,
never raise into Mojo: "ok" or "error" for the model and the child process, "line" or "eof" for
standard input. The model call is the OpenAI-compatible /v1/chat/completions that llama-server
serves, the same request OpenAiChat.cs sends.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Final, cast

CHAT_TIMEOUT_S: Final = 300
CHILD_TIMEOUT_S: Final = 120
NO_TRACK_ENV: Final = "VINCI_NO_TRACK"
# Eval only (the M4 chat-loop evaluation). VINCI_CHAT_TEMPERATURE is sent with the request when
# set; VINCI_CHAT_USAGE_LOG names a JSON lines file that gets one record per model call: the
# token counts, the reply, and whether the request carried a recall that fell back to lexical.
# Unset, neither changes the request or writes anything.
TEMPERATURE_ENV: Final = "VINCI_CHAT_TEMPERATURE"
USAGE_LOG_ENV: Final = "VINCI_CHAT_USAGE_LOG"
LEXICAL_FALLBACK_MARK: Final = "the embedding server did not answer"


def _field(value: object, key: str) -> object | None:
    """The value under key when value is a JSON object that has it, else None."""
    if not isinstance(value, dict):
        return None
    return cast("dict[str, object]", value).get(key)


def _log_usage(log: str, raw: object, messages_json: str) -> None:
    """Append one usage record for a model call (eval only, see USAGE_LOG_ENV)."""
    usage = _field(raw, "usage")
    choices = _field(raw, "choices")
    content: object = None
    if isinstance(choices, list) and cast("list[object]", choices):
        content = _field(_field(cast("list[object]", choices)[0], "message"), "content")
    record: dict[str, object] = {
        "prompt_tokens": _field(usage, "prompt_tokens"),
        "completion_tokens": _field(usage, "completion_tokens"),
        "reply": content,
        "lexical_fallback": LEXICAL_FALLBACK_MARK in messages_json,
    }
    with Path(log).open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(record, ensure_ascii=False) + "\n")


def complete(base_url: str, model: str, messages_json: str, think: bool) -> tuple[str, str]:
    """One chat completion. Returns ("ok", reply text) or ("error", message).

    messages_json is a JSON array of {"role", "content"} objects. think is the
    chat_template_kwargs switch that turns Qwen3's reasoning on or off.
    """
    try:
        body: dict[str, object] = {
            "model": model,
            "messages": json.loads(messages_json),
            "stream": False,
            "chat_template_kwargs": {"enable_thinking": think},
        }
        temperature = os.environ.get(TEMPERATURE_ENV, "")
        if temperature:
            body["temperature"] = float(temperature)
        request = urllib.request.Request(
            base_url.rstrip("/") + "/chat/completions",
            data=json.dumps(body).encode("utf-8"),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(request, timeout=CHAT_TIMEOUT_S) as response:
            raw: object = json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as err:
        return ("error", f"chat model {model} returned {err.code}")
    except (urllib.error.URLError, OSError, ValueError) as err:
        return ("error", str(err))
    usage_log = os.environ.get(USAGE_LOG_ENV, "")
    if usage_log:
        _log_usage(usage_log, raw, messages_json)
    if raw is None:
        return ("error", "chat model returned an empty reply")
    choices = _field(raw, "choices")
    if not isinstance(choices, list) or len(cast("list[object]", choices)) == 0:
        return ("ok", "")
    first = cast("list[object]", choices)[0]
    content = _field(_field(first, "message"), "content")
    return ("ok", content if isinstance(content, str) else "")


def read_line() -> tuple[str, str]:
    """One line of standard input without its line ending: ("line", text), or ("eof", "")."""
    line = sys.stdin.readline()
    if line == "":
        return ("eof", "")
    return ("line", line.rstrip("\r\n"))


def run_vinci(exe: str, args_json: str, no_track: bool) -> tuple[int, str]:
    """Runs exe with the JSON array of arguments, standard input empty, and returns its exit
    code with its standard output and error, in the order written. The environment is the
    chat's, so VINCI_HOME, VINCI_NOW and the rest reach the command as they reach the chat.
    no_track sets VINCI_NO_TRACK=1 in the child's environment (a read-only command).
    """
    args = cast("list[str]", json.loads(args_json))
    env = dict(os.environ)
    if no_track:
        env[NO_TRACK_ENV] = "1"
    done = subprocess.run(
        [exe, *args],
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        timeout=CHILD_TIMEOUT_S,
        check=False,
        env=env,
    )
    return (done.returncode, done.stdout.decode("utf-8", errors="replace"))
