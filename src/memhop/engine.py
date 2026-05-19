"""
MemHop Engine — the main orchestrator.

Ties together encoder + Modern Hopfield Network + LMDB storage
to deliver the remember/recall/forget API.

Two-stage retrieval (for Chinese short text robustness):
    Stage 1: Sparse coarse screening (lexical match, if sparse available)
    Stage 2: MHN fine ranking (Hopfield one-step convergence)
"""

from __future__ import annotations

import uuid
from typing import Any

from memhop.types import Memory, EncoderOutput
from memhop.encoder import Encoder
from memhop.hopfield import ModernHopfield
from memhop.storage import LmdbStorage


class MemHopEngine:
    """
    MemHop associative memory engine.

    This is what `memhop.open()` returns. Provides the full
    remember / recall / forget / search API.

    Args:
        encoder: Encoder instance (API, local, or custom)
        storage: LMDB storage backend
        hopfield: Modern Hopfield Network instance
    """

    def __init__(
        self,
        encoder: Encoder,
        storage: LmdbStorage,
        hopfield: ModernHopfield,
    ):
        self._encoder = encoder
        self._storage = storage
        self._mhn = hopfield

        # Warm-up: load existing patterns from storage into MHN
        self._warm_up()

    def _warm_up(self) -> None:
        """Load all persisted patterns into the Hopfield network at startup."""
        patterns = self._storage.load_all_patterns()
        for mid, vec in patterns.items():
            self._mhn.add_pattern(mid, vec)

    # ── Public API ──────────────────────────────────────────

    def remember(
        self,
        text: str,
        meta: dict[str, Any] | None = None,
        memory_id: str | None = None,
    ) -> str:
        """
        Store a new memory.

        Args:
            text: The text content of the memory
            meta: Optional metadata (tags, timestamp, etc.)
            memory_id: Optional custom ID (auto-generated if None)

        Returns:
            The memory ID
        """
        if memory_id is None:
            memory_id = f"m_{uuid.uuid4().hex[:12]}"

        # Encode
        output = self._encoder.encode(text)

        # Store in LMDB
        self._storage.put_memory(
            memory_id=memory_id,
            text=text,
            meta=meta or {},
            vector=output.dense,
        )

        # Add to Hopfield network
        self._mhn.add_pattern(memory_id, output.dense)

        return memory_id

    def recall(self, cue: str) -> Memory | None:
        """
        Recall the single best matching memory (O(1)).

        If the encoder provides sparse vectors, uses two-stage retrieval:
        Stage 1: Sparse coarse screen (lexical match)
        Stage 2: MHN fine rank (Hopfield attractor convergence)

        Args:
            cue: The query text to recall against

        Returns:
            Memory if confidence > threshold, None otherwise
        """
        # Encode cue
        output = self._encoder.encode(cue)

        # Stage 1: Sparse coarse screening (if available)
        if output.sparse and self._mhn.num_memories > 500:
            # TODO: Implement LSH bucket / sparse score coarse screening
            # For now, fall through to full MHN recall
            pass

        # Stage 2: MHN fine rank
        best_id, confidence = self._mhn.recall(output.dense)

        if best_id is None:
            return None

        # Load full memory from storage
        memory = self._storage.get_memory(best_id)
        if memory is None:
            return None

        memory.confidence = confidence
        return memory

    def recall_topk(self, cue: str, k: int = 5) -> list[Memory]:
        """
        Recall top-k matching memories (for debugging/inspection).

        Args:
            cue: Query text
            k: Number of top matches

        Returns:
            List of Memory objects sorted by confidence descending
        """
        output = self._encoder.encode(cue)
        matches = self._mhn.recall_batch(output.dense, top_k=k)

        results = []
        for mid, conf in matches:
            if mid is None:
                continue
            memory = self._storage.get_memory(mid)
            if memory:
                memory.confidence = conf
                results.append(memory)

        return results

    def forget(self, memory_id: str) -> bool:
        """
        Delete a memory by ID.

        Returns:
            True if deleted, False if not found
        """
        # Remove from Hopfield network
        self._mhn.remove_pattern(memory_id)
        # Remove from storage
        return self._storage.delete_memory(memory_id)

    def update(
        self,
        memory_id: str,
        text: str | None = None,
        meta: dict[str, Any] | None = None,
    ) -> bool:
        """
        Update a memory's text or metadata.

        If text is updated, re-encodes and updates the Hopfield pattern.

        Args:
            memory_id: The memory to update
            text: New text (None to keep existing)
            meta: New metadata (None to keep existing)

        Returns:
            True if updated, False if not found
        """
        existing = self._storage.get_memory(memory_id)
        if existing is None:
            return False

        new_text = text if text is not None else existing.text
        new_meta = meta if meta is not None else existing.meta

        if text is not None:
            # Re-encode and update pattern
            output = self._encoder.encode(new_text)
            self._storage.put_pattern(memory_id, output.dense)
            self._mhn.remove_pattern(memory_id)
            self._mhn.add_pattern(memory_id, output.dense)

        self._storage.update_meta(memory_id, new_meta)
        return True

    def search(
        self,
        tags: list[str] | None = None,
        text_contains: str | None = None,
    ) -> list[Memory]:
        """
        Exact metadata/tag search (not associative — for filtering).

        Args:
            tags: Filter by tags (exact match, OR logic)
            text_contains: Filter by substring in text

        Returns:
            Matching Memory objects
        """
        # TODO: Optimize with meta index for large datasets
        all_ids = self._storage.list_ids()
        results = []

        for mid in all_ids:
            mem = self._storage.get_memory(mid)
            if mem is None:
                continue

            if tags:
                mem_tags = mem.meta.get("tags", [])
                if not any(t in mem_tags for t in tags):
                    continue

            if text_contains:
                if text_contains.lower() not in mem.text.lower():
                    continue

            results.append(mem)

        return results

    # ── Properties ──────────────────────────────────────────

    @property
    def count(self) -> int:
        """Total number of stored memories."""
        return self._mhn.num_memories

    @property
    def stats(self) -> dict[str, Any]:
        """Runtime statistics."""
        return {
            "total_memories": self._mhn.num_memories,
            "storage_path": str(self._storage.path),
            "encoder_mode": type(self._encoder).__name__,
            "beta": self._mhn.beta,
            "threshold": self._mhn.threshold,
        }

    def close(self) -> None:
        """Close the database and release resources."""
        self._storage.close()

    def __enter__(self) -> "MemHopEngine":
        return self

    def __exit__(self, *args) -> None:
        self.close()

    def __repr__(self) -> str:
        return (
            f"MemHopEngine(memories={self._mhn.num_memories}, "
            f"encoder={type(self._encoder).__name__}, "
            f"path={self._storage.path})"
        )
