"""Milvus Lite competitor runner.

Uses Milvus Lite (embedded), which runs in-process without Docker.
Same API surface as Milvus standalone but zero infrastructure.
"""

import time
import numpy as np
from typing import Optional
from .base import RetrieverRunner


class MilvusLiteRunner(RetrieverRunner):
    """Milvus Lite retriever — in-process, no Docker."""

    def __init__(self, dim: int = 1024, collection_prefix: str = "memhop_bench"):
        super().__init__("milvus-lite", dim)
        self._prefix = collection_prefix
        self._db_file: Optional[str] = None
        self._connections = None
        self._collection = None
        self._doc_ids: list[str] = []

    def index(self, doc_ids: list[str], vectors: np.ndarray) -> None:
        """Index documents into Milvus Lite.

        Args:
            doc_ids: N document IDs.
            vectors: N × dim float32.
        """
        try:
            from pymilvus import MilvusClient, connections
            self._connections = connections
        except ImportError:
            raise ImportError(
                "pymilvus not installed. Run: pip install pymilvus"
            )

        n, d = vectors.shape
        assert d == self.dim, f"Vector dim {d} != expected {self.dim}"
        assert len(doc_ids) == n

        import tempfile
        import os
        self._db_file = os.path.join(
            tempfile.mkdtemp(prefix="memhop_milvus_"), "milvus.db"
        )

        self._client = MilvusClient(self._db_file)

        # Drop if exists
        try:
            self._client.drop_collection(self._prefix)
        except Exception:
            pass

        # Create collection
        self._client.create_collection(
            collection_name=self._prefix,
            dimension=d,
            metric_type="COSINE",
        )

        # Insert in batches
        data = []
        for i, (did, vec) in enumerate(zip(doc_ids, vectors)):
            data.append({
                "id": i,
                "vector": vec.tolist(),
                "doc_id": did,
            })

        batch_size = 5000
        for start in range(0, len(data), batch_size):
            batch = data[start:start + batch_size]
            self._client.insert(collection_name=self._prefix, data=batch)

        # Build index
        self._client.create_index(
            collection_name=self._prefix,
            field_name="vector",
            index_type="HNSW",
            metric_type="COSINE",
            params={"M": 16, "efConstruction": 200},
        )

        self._doc_ids = doc_ids
        self._indexed_count = n

    def search(
        self,
        query_vectors: np.ndarray,
        top_k: int = 10,
    ) -> tuple[list[list[str]], list[list[float]]]:
        """Search Milvus Lite.

        Args:
            query_vectors: M × dim float32.
            top_k: Results per query.

        Returns:
            (ids, scores): M × top_k lists.
        """
        if self._client is None:
            raise RuntimeError("Client not initialized. Call index() first.")

        # Load collection into memory if needed
        self._client.load_collection(self._prefix)

        id_results = []
        score_results = []

        for qvec in query_vectors:
            results = self._client.search(
                collection_name=self._prefix,
                data=[qvec.tolist()],
                limit=top_k,
                output_fields=["doc_id"],
            )
            ids = []
            scores = []
            for hit in results[0]:
                doc_id = hit.get("entity", {}).get("doc_id", "")
                ids.append(doc_id)
                scores.append(float(hit.get("distance", 0.0)))
            id_results.append(ids)
            score_results.append(scores)

        return id_results, score_results

    def clear(self) -> None:
        """Reset Milvus state."""
        if self._client is not None:
            try:
                self._client.drop_collection(self._prefix)
            except Exception:
                pass
        self._client = None
        self._collection = None
        self._doc_ids = []
        self._indexed_count = 0

        # Clean up temp file
        if self._db_file:
            import shutil
            import os
            db_dir = os.path.dirname(self._db_file)
            try:
                shutil.rmtree(db_dir, ignore_errors=True)
            except Exception:
                pass
            self._db_file = None
