"""Where vectors come from: the embedding config and the calls into vinci_edge.index_embed.

Port of mojo/src/embed_edge.mojo. Floats cross the edge as packed bytes.
"""

from __future__ import annotations

import os
from dataclasses import dataclass

from vinci_edge import index_embed

DEFAULT_BASE_URL = "http://127.0.0.1:8081/v1"
DEFAULT_MODEL = "qwen3-embedding:4b"


@dataclass
class EmbedConfig:
    """Where vectors come from. label names the vector set (the model, or a device label such
    as qwen3-embedding:4b@gpu). cache "" is vinci_edge's default cache.

    Query vectors only: a question missing under label is looked up under fallback_label,
    then embedded at fallback_url and cached under fallback_label. Empty fallback_url means no
    fallback. Documents never use these.
    """

    base_url: str
    model: str
    label: str
    cache: str
    instruction: str
    fallback_label: str = ""
    fallback_url: str = ""


def env_or(name: str, fallback: str) -> str:
    value = os.environ.get(name)
    return fallback if value is None else value


def default_config() -> EmbedConfig:
    """EMBEDDING_BASE_URL and EMBEDDING_MODEL from the environment (memory.env is loaded
    first), else the CPU server and the model name. The label is the model name.

    Eval only: VINCI_EVAL_EMBED_LABEL, VINCI_EVAL_EMBED_CACHE, VINCI_EVAL_EMBED_FALLBACK_LABEL
    and VINCI_EVAL_EMBED_FALLBACK_URL set the label, the cache file and the query fallback
    that lme_index_eval uses. Unset, they change nothing.
    """
    model = env_or("EMBEDDING_MODEL", DEFAULT_MODEL)
    cfg = EmbedConfig(
        env_or("EMBEDDING_BASE_URL", DEFAULT_BASE_URL),
        model,
        env_or("VINCI_EVAL_EMBED_LABEL", model),
        env_or("VINCI_EVAL_EMBED_CACHE", ""),
        "",
    )
    fallback_url = env_or("VINCI_EVAL_EMBED_FALLBACK_URL", "")
    if len(fallback_url) > 0:
        cfg.fallback_label = env_or("VINCI_EVAL_EMBED_FALLBACK_LABEL", "")
        cfg.fallback_url = fallback_url
    return cfg


def embed_documents(cfg: EmbedConfig, texts: list[str]) -> list[bytes]:
    """One packed bytes object per text, in order."""
    if len(texts) == 0:
        return []
    return list(
        index_embed.documents(cfg.base_url, cfg.model, cfg.label, cfg.cache, cfg.instruction, texts)
    )


def embed_question(cfg: EmbedConfig, question: str) -> bytes:
    """The packed bytes of a question, wrapped with the query instruction. With a fallback
    set, the lookup order is in vinci_edge.index_embed.query_with_fallback."""
    if len(cfg.fallback_url) > 0:
        return index_embed.query_with_fallback(
            cfg.base_url,
            cfg.model,
            cfg.label,
            cfg.cache,
            cfg.instruction,
            question,
            cfg.fallback_label,
            cfg.fallback_url,
        )
    return index_embed.query(cfg.base_url, cfg.model, cfg.label, cfg.cache, cfg.instruction, question)
