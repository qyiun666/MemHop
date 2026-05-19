"""
Modern Hopfield Network (MHN) core.

Implements one-step attractor convergence via softmax energy function:

    E(x) = -lse(β, Xᵀx) + ½xᵀx + C
    x_new = softmax(β Xᵀx) · X

Key properties:
    - Storage capacity: N ∝ exp(d)  (exponential, not 0.14d)
    - Convergence: One step to nearest attractor
    - O(1) recall: independent of memory count
"""

import numpy as np

from memhop.types import Memory, VECTOR_DIM


class ModernHopfield:
    """
    Modern Hopfield Network for associative memory.

    Stores patterns as a dense matrix X ∈ R^(d×N).
    Recalls converge to the nearest attractor in one step.

    Args:
        beta: Temperature parameter controlling attractor sharpness (default: 8.0)
        threshold: Minimum confidence to accept a recall (default: 0.7)
    """

    def __init__(self, beta: float = 8.0, threshold: float = 0.7):
        self.beta = beta
        self.threshold = threshold

        # Pattern matrix: X ∈ R^(d×N)
        # Initialized as empty, grows with remember() calls
        self._X: np.ndarray | None = None  # shape: (VECTOR_DIM, N)
        self._ids: list[str] = []           # memory IDs in column order

    @property
    def num_memories(self) -> int:
        return len(self._ids)

    @property
    def pattern_matrix(self) -> np.ndarray | None:
        return self._X

    def add_pattern(self, memory_id: str, vector: np.ndarray) -> None:
        """
        Add a new memory pattern to the network.

        Args:
            memory_id: Unique identifier for the memory
            vector: Dense embedding vector, shape (VECTOR_DIM,) float32
        """
        vec = np.asarray(vector, dtype=np.float32).reshape(-1)

        if self._X is None:
            self._X = vec.reshape(-1, 1)  # (d, 1)
        else:
            self._X = np.column_stack([self._X, vec])  # (d, N+1)

        self._ids.append(memory_id)

    def remove_pattern(self, memory_id: str) -> bool:
        """Remove a memory pattern by ID. Returns False if not found."""
        try:
            idx = self._ids.index(memory_id)
        except ValueError:
            return False

        # Remove column
        self._X = np.delete(self._X, idx, axis=1)
        self._ids.pop(idx)
        return True

    def recall(self, query: np.ndarray) -> tuple[str | None, float]:
        """
        Recall the nearest memory via one-step Hopfield attractor convergence.

        Args:
            query: Query embedding vector, shape (VECTOR_DIM,) float32

        Returns:
            (memory_id, confidence) — (None, 0.0) if no match above threshold
        """
        if self._X is None or self._X.shape[1] == 0:
            return None, 0.0

        q = np.asarray(query, dtype=np.float32).reshape(-1)

        # One-step update: x_new = softmax(β Xᵀ q) · X
        # Step 1: compute similarity scores
        scores = self._X.T @ q  # shape: (N,)  — cosine-like since vectors are normalized

        # Step 2: softmax with temperature
        scores_scaled = scores * self.beta
        scores_scaled -= scores_scaled.max()  # numerical stability
        attention = np.exp(scores_scaled)
        attention /= attention.sum()

        # Step 3: retrieve pattern
        best_idx = int(np.argmax(attention))
        confidence = float(attention[best_idx])

        if confidence < self.threshold:
            return None, confidence

        return self._ids[best_idx], confidence

    def recall_batch(
        self, query: np.ndarray, top_k: int = 5
    ) -> list[tuple[str | None, float]]:
        """
        Batch recall returning top-k matches (for debugging / fallback).

        Args:
            query: Query embedding vector
            top_k: Number of top matches to return

        Returns:
            List of (memory_id, confidence) sorted by confidence descending
        """
        if self._X is None or self._X.shape[1] == 0:
            return [(None, 0.0)] * min(top_k, 1)

        q = np.asarray(query, dtype=np.float32).reshape(-1)

        scores = self._X.T @ q
        scores_scaled = scores * self.beta
        scores_scaled -= scores_scaled.max()
        attention = np.exp(scores_scaled)
        attention /= attention.sum()

        # Top-k indices
        top_indices = np.argsort(attention)[::-1][:top_k]

        return [
            (self._ids[i], float(attention[i]))
            for i in top_indices
        ]

    def get_pattern(self, memory_id: str) -> np.ndarray | None:
        """Get the embedding vector for a specific memory."""
        try:
            idx = self._ids.index(memory_id)
            return self._X[:, idx].copy()
        except ValueError:
            return None

    def clear(self) -> None:
        """Remove all patterns."""
        self._X = None
        self._ids.clear()
