"""ChromaDB competitor runner.

Uses ChromaDB's Python API with a fresh in-memory/ephemeral collection for each benchmark.
"""

import time
import numpy as np
import chromadb
from typing import Optional
from .base import RetrieverRunner


class ChromaRunner(RetrieverRunner):
    """ChromaDB retriever using persistent client."""

    def __init__(self, dim: int = 1024, collection_prefix: str = "memhop_bench"):
        super().__init__("chromadb", dim)
        self._prefix = collection_prefix
        self._client: Optional[chromadb.Client] = None
        self._collection: Optional[chromadb.Collection] = None
        self._doc_ids: list[str] = []

    def index(self, doc_ids: list[str], vectors: np.ndarray) -> None:
        """Index documents into ChromaDB.

        ChromaDB doesn't need dimensionality specification at init;
        it infers from the first batch of vectors.

        Args:
            doc_ids: N document IDs.
            vectors: N × dim float32.
        """
        n, d = vectors.shape
        assert d == self.dim, f"Vector dim {d} != expected {self.dim}"
        assert len(doc_ids) == n

        # Fresh client per benchmark
        self._client = chromadb.Client(
            chromadb.config.Settings(
                anonymized_telemetry=False,
                allow_reset=True,
            )
        )
        try:
            self._client.delete_collection(self._prefix)
        except Exception:
            pass

        self._collection = self._client.create_collection(
            name=self._prefix,
            metadata={"hnsw:space": "cosine"},
        )

        # Batch insert (ChromaDB recommends batches for large datasets)
        batch_size = 5000
        for start in range(0, n, batch_size):
            end = min(start + batch_size, n)
            batch_ids = doc_ids[start:end]
            batch_vecs = vectors[start:end].tolist()
            self._collection.add(
                ids=batch_ids,
                embeddings=batch_vecs,
            )

        self._doc_ids = doc_ids
        self._indexed_count = n

    def search(
        self,
        query_vectors: np.ndarray,
        top_k: int = 10,
    ) -> tuple[list[list[str]], list[list[float]]]:
        """Search ChromaDB collection.

        Args:
            query_vectors: M × dim float32.
            top_k: Results per query.

        Returns:
            (ids, scores): M × top_k lists.
        """
        if self._collection is None:
            raise RuntimeError("Collection not created. Call index() first.")

        results = self._collection.query(
            query_embeddings=query_vectors.tolist(),
            n_results=top_k,
        )

        id_results = results.get("ids", [])
        distance_results = results.get("distances", [])

        # Convert distances to similarity (cosine distance -> similarity)
        score_results = []
        for dist_row in distance_results:
            score_results.append([1.0 - d for d in dist_row])

        return id_results, score_results

    def clear(self) -> None:
        """Delete the ChromaDB collection."""
        if self._client is not None:
            try:
                self._client.delete_collection(self._prefix)
            except Exception:
                pass
        self._client = None
        self._collection = None
        self._doc_ids = []
        self._indexed_count = 0
