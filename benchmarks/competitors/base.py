"""Abstract base class for all retrieval system runners.

Each competitor (FAISS, ChromaDB, Milvus) implements this interface
so benchmarks can run the same data through every system identically.
"""

from abc import ABC, abstractmethod
import numpy as np
from typing import Optional


class RetrieverRunner(ABC):
    """Abstract retriever that all competitors must implement."""

    def __init__(self, name: str, dim: int = 1024):
        self.name = name
        self.dim = dim
        self._indexed_count = 0

    @abstractmethod
    def index(self, doc_ids: list[str], vectors: np.ndarray) -> None:
        """Index documents with pre-encoded vectors.

        Args:
            doc_ids: List of document IDs, length N.
            vectors: N × dim float32 numpy array.
        """
        ...

    @abstractmethod
    def search(
        self,
        query_vectors: np.ndarray,
        top_k: int = 10,
    ) -> tuple[list[list[str]], list[list[float]]]:
        """Search for top-k documents per query.

        Args:
            query_vectors: M × dim float32 numpy array.
            top_k: Number of results per query.

        Returns:
            (ids, scores) where:
                ids: M × top_k list of document ID strings
                scores: M × top_k list of float scores
        """
        ...

    @abstractmethod
    def clear(self) -> None:
        """Delete all indexed data. Fresh state for next benchmark."""
        ...

    @property
    def indexed_count(self) -> int:
        return self._indexed_count

    def __repr__(self) -> str:
        return f"{self.__class__.__name__}(name={self.name}, dim={self.dim}, indexed={self._indexed_count})"
