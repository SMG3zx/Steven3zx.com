"""The vinci inference service: llama-server processes behind one OpenAI-compatible port.

See config.py for the settings, supervisor.py for the processes, proxy.py for the front door,
and `python -m vinci_infer help` for the commands.
"""

from .config import ServerSpec, Settings, build_argv, load_settings
from .proxy import make_server, pick_route, routes_for
from .supervisor import Supervisor

__all__ = [
    "ServerSpec",
    "Settings",
    "Supervisor",
    "build_argv",
    "load_settings",
    "make_server",
    "pick_route",
    "routes_for",
]
