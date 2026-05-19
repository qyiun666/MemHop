"""
MemHop — SQLite for associative memory.

Embedded, single-file, zero-config associative memory database
for AI Agents. O(1) recall via Modern Hopfield Networks.

Usage:
    import memhop

    db = memhop.open("memhop.db")
    mid = db.remember("今天早上吃了豆浆油条", meta={"time": "2026-05-19T07:30"})
    memory = db.recall("今天早上吃了什么")
    # → Memory(id="m_001", text="今天早上吃了豆浆油条", confidence=0.94)
"""

from memhop.types import Memory, EncoderOutput, EncoderConfig
from memhop.encoder import Encoder, ApiEncoder, get_encoder
from memhop.hopfield import ModernHopfield
from memhop.storage import LmdbStorage
from memhop.engine import MemHopEngine


def open(
    path: str = "memhop.db",
    encoder: EncoderConfig | None = None,
    confidence_threshold: float = 0.7,
    beta: float = 8.0,
    max_memories: int = 1_000_000,
) -> "MemHopEngine":
    """
    Open or create a MemHop database.

    Args:
        path: Path to the single-file database (default: memhop.db)
        encoder: Encoder configuration (default: API mode, DeepSeek Embedding)
        confidence_threshold: Minimum confidence to accept a recall (default: 0.7)
        beta: Hopfield temperature parameter (default: 8.0)
        max_memories: Soft upper bound (default: 1M)

    Returns:
        MemHopEngine instance with remember / recall / forget API
    """
    if encoder is None:
        encoder = EncoderConfig(mode="api")

    enc = get_encoder(encoder)
    storage = LmdbStorage(path, max_memories=max_memories)
    mhn = ModernHopfield(beta=beta, threshold=confidence_threshold)

    return MemHopEngine(encoder=enc, storage=storage, hopfield=mhn)


__version__ = "0.1.0"
__all__ = ["open", "Memory", "EncoderOutput", "EncoderConfig"]
