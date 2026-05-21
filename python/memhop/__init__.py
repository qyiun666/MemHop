"""MemHop - SQLite for associative memory.

Supports text memories with optional binary blob attachments (images, audio, etc).
The encoder only processes the text description; blobs are stored as-is with zstd compression.
"""
from memhop._core import (
    MemHopEngine,
    Memory,
    MemHopError,
    MemHopClosedError,
)

__version__ = "0.3.0"
__all__ = [
    "open",
    "MemHopEngine",
    "Memory",
    "MemHopError",
    "MemHopClosedError",
]


def open(
    path: str = "memhop.db",
    *,
    encoder: str = "ngram",
    confidence_threshold: float = 0.7,
    beta: float = 8.0,
    max_memories: int = 1_000_000,
    timezone: str = "UTC",
) -> MemHopEngine:
    """Open or create a MemHop database.

    Args:
        path: Database file path.
        encoder: Encoder type. Currently only "ngram" is supported.
        confidence_threshold: Minimum confidence for recall results.
        beta: Hopfield temperature parameter.
        max_memories: Soft cap, FIFO eviction of normal memories when exceeded.
        timezone: Timezone for timestamps (currently UTC only).
    """
    return MemHopEngine(
        path=path,
        encoder=encoder,
        confidence_threshold=confidence_threshold,
        beta=beta,
        max_memories=max_memories,
        timezone=timezone,
    )
