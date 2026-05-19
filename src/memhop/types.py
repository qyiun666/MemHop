"""Core data types for MemHop."""

from dataclasses import dataclass, field
from datetime import datetime, timezone
from enum import Enum
from typing import Any

import numpy as np


# ── Dense vector dimension ─────────────────────────────────
VECTOR_DIM = 1024


# ── Protection levels ──────────────────────────────────────

class Protection(str, Enum):
    """Memory protection level for forget/purge operations."""

    PERMANENT = "permanent"  # Never deleted by forget() or purge_before()
    PROTECTED = "protected"  # purge_before() skips, forget() allowed
    NORMAL = "normal"        # All operations allowed


# ── Exceptions ─────────────────────────────────────────────

class MemHopError(Exception):
    """Base exception for all MemHop errors."""
    pass


class MemHopClosedError(MemHopError):
    """Raised when operating on a closed database."""

    def __init__(self, method: str = ""):
        msg = f"MemHop database is closed. Cannot call {method}() after close()."
        super().__init__(msg)


# ── Memory ─────────────────────────────────────────────────

@dataclass
class Memory:
    """A single recalled memory, returned by recall()."""

    id: str
    text: str
    meta: dict[str, Any] = field(default_factory=dict)
    confidence: float = 0.0  # 0.0–1.0, Hopfield attractor convergence score
    created_at: str = ""     # ISO 8601 timestamp, auto-filled by remember()


# ── Encoder output ─────────────────────────────────────────

@dataclass
class EncoderOutput:
    """
    Unified encoder output. Different encoder modes populate different fields.

    - API mode: dense only
    - BGE-M3 local: dense + sparse + multi
    - Custom: any combination
    """

    dense: np.ndarray  # shape: (VECTOR_DIM,) float32
    sparse: dict[str, float] | None = None  # bag-of-words or lexical weights
    multi: np.ndarray | None = None  # shape: (N_tokens, VECTOR_DIM), ColBERT-style


# ── Encoder configuration ──────────────────────────────────

class EncoderMode(str, Enum):
    API = "api"
    LOCAL = "local"
    CUSTOM = "custom"
    MOCK = "mock"


@dataclass
class EncoderConfig:
    mode: EncoderMode = EncoderMode.API

    # API mode
    api_base_url: str = "https://api.deepseek.com/v1"
    api_model: str = "deepseek-embed"

    # Local mode (BGE-M3 ONNX)
    local_model_path: str | None = None  # None = auto-download
    local_use_int8: bool = True

    # Custom mode
    custom_class: str | None = None  # fully qualified class path
