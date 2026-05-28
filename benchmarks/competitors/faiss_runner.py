"""FAISS competitor runner.

Supports both IVF (inverted file) and HNSW (hierarchical navigable small world)
index types, using BGE-M3 1024-dimensional vectors.
"""

import time
import numpy as np
import faiss
from typing import Optional
from .base import RetrieverRunner


class FAISSRunner(RetrieverRunner):
    """FAISS retriever with configurable index type."""

    VALID_INDICES = ("IVF", "HNSW", "Flat")

    def __init__(self, index_type: str = "IVF", dim: int = 1024):
        name = f"faiss-{index_type.lower()}"
        super().__init__(name, dim)
        self.index_type = index_type.upper()
        if self.index_type not in self.VALID_INDICES:
            raise ValueError(f"Invalid index type: {index_type}. Choose from {self.VALID_INDICES}")
        self._index: Optional[faiss.Index] = None
        self._id_map: Optional[dict[int, str]] = None  # faiss_id -> doc_id

    def index(self, doc_ids: list[str], vectors: np.ndarray) -> None:
        """Index documents into FAISS.

        Args:
            doc_ids: N document IDs.
            vectors: N × dim float32.
        """
        n, d = vectors.shape
        assert d == self.dim, f"Vector dim {d} != expected {self.dim}"
        assert len(doc_ids) == n

        vectors = vectors.astype(np.float32, copy=False)

        if self.index_type == "IVF":
            # IVF: need to train on representative data
            nlist = min(int(np.sqrt(n)), 1024)
            quantizer = faiss.IndexFlatIP(d)  # inner product = cosine for normalized vectors
            self._index = faiss.IndexIVFFlat(quantizer, d, nlist, faiss.METRIC_INNER_PRODUCT)
            self._index.train(vectors)
            self._index.nprobe = min(nlist // 4, 64)

        elif self.index_type == "HNSW":
            self._index = faiss.IndexHNSWFlat(d, 32, faiss.METRIC_INNER_PRODUCT)
            # HNSW doesn't need separate training but can be slow with many vectors

        elif self.index_type == "Flat":
            self._index = faiss.IndexFlatIP(d)

        self._index.add(vectors)
        self._id_map = {i: did for i, did in enumerate(doc_ids)}
        self._indexed_count = n

    def search(
        self,
        query_vectors: np.ndarray,
        top_k: int = 10,
    ) -> tuple[list[list[str]], list[list[float]]]:
        """Search FAISS index.

        Args:
            query_vectors: M × dim float32.
            top_k: Results per query.

        Returns:
            (ids, scores): M × top_k lists.
        """
        if self._index is None or self._id_map is None:
            raise RuntimeError("Index not built. Call index() first.")

        query_vectors = query_vectors.astype(np.float32, copy=False)
        scores, indices = self._index.search(query_vectors, top_k)

        # Map internal FAISS ids to document ids
        id_results = []
        score_results = []
        for row_idx, row_scores in zip(indices, scores):
            ids = []
            sc = []
            for faiss_id, s in zip(row_idx, row_scores):
                if faiss_id >= 0 and faiss_id in self._id_map:
                    ids.append(self._id_map[faiss_id])
                    sc.append(float(s))
            id_results.append(ids)
            score_results.append(sc)

        return id_results, score_results

    def clear(self) -> None:
        """Reset the FAISS index."""
        self._index = None
        self._id_map = None
        self._indexed_count = 0
