"""MemHop - SQLite for associative memory.

Supports text memories with optional binary blob attachments (images, audio, etc).
The encoder only processes the text description; blobs are stored as-is with zstd compression.
"""
from memhop._core import (
    MemHopEngine,
    Memory,
    MemHopError,
    MemHopClosedError,
    BrainLoop,
    BrainConfig,
    BrainAction,
    BodyAction,
    BrainNotifications,
    CognitionHealth,
    BodyResult,
    HttpThinker,
    FastReflex,
)

__version__ = "0.5.0"
__all__ = [
    "open",
    "MemHopEngine",
    "Memory",
    "MemHopError",
    "MemHopClosedError",
    "BrainLoop",
    "BrainConfig",
    "BrainAction",
    "BodyAction",
    "BrainNotifications",
    "CognitionHealth",
    "BodyResult",
    "HttpThinker",
    "FastReflex",
    "build_idf",
    "normalize_time",
]


def open(
    path: str = "memhop.db",
    *,
    encoder: str = "ngram",
    confidence_threshold: float = 0.7,
    beta: float = 8.0,
    max_memories: int = 1_000_000,
    timezone: str = "UTC",
    gating_enabled: bool = True,
    gating_threshold: float = 0.6,
) -> MemHopEngine:
    """Open or create a MemHop database.

    Args:
        path: Database file path.
        encoder: Encoder type. Currently only "ngram" is supported.
        confidence_threshold: Minimum confidence for recall results.
        beta: Hopfield temperature parameter.
        max_memories: Soft cap, FIFO eviction of normal memories when exceeded.
        timezone: Timezone for timestamps (currently UTC only).
        gating_enabled: Enable scene-gated recall (v0.4.0).
        gating_threshold: Cosine similarity threshold for fingerprint matching.
    """
    return MemHopEngine(
        path=path,
        encoder=encoder,
        confidence_threshold=confidence_threshold,
        beta=beta,
        max_memories=max_memories,
        timezone=timezone,
        gating_enabled=gating_enabled,
        gating_threshold=gating_threshold,
    )
