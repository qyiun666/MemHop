"""Unified data schema for all benchmark datasets.

All adapters (C-MTEB, BEIR, self-built) produce this format.
All competitors (FAISS, ChromaDB, Milvus) consume this format.
"""

from dataclasses import dataclass, field
from typing import Optional
import json
import numpy as np


@dataclass
class RetrievalDataset:
    """Standard retrieval dataset format.

    corpus:    {doc_id: text}
    queries:   {query_id: text}
    qrels:     {query_id: {doc_id: relevance_score}}
    """

    name: str
    corpus: dict[str, str]
    queries: dict[str, str]
    qrels: dict[str, dict[str, int]]

    # Pre-encoded vectors (populated by encoder)
    doc_vectors: Optional[dict[str, np.ndarray]] = None
    query_vectors: Optional[dict[str, np.ndarray]] = None

    def __post_init__(self):
        if self.doc_vectors is None:
            self.doc_vectors = {}
        if self.query_vectors is None:
            self.query_vectors = {}

    @property
    def doc_ids(self) -> list[str]:
        return list(self.corpus.keys())

    @property
    def query_ids(self) -> list[str]:
        return list(self.queries.keys())

    @property
    def num_docs(self) -> int:
        return len(self.corpus)

    @property
    def num_queries(self) -> int:
        return len(self.queries)

    def to_json(self, path: str):
        """Serialize to JSON for Rust quality_bench consumption."""
        data = {
            "name": self.name,
            "documents": [
                {
                    "id": did,
                    "text": self.corpus[did],
                    "vector": self.doc_vectors.get(did, np.zeros(1024)).tolist()
                    if self.doc_vectors else [0.0] * 1024,
                }
                for did in self.corpus
            ],
            "queries": [
                {
                    "id": qid,
                    "text": self.queries[qid],
                    "vector": self.query_vectors.get(qid, np.zeros(1024)).tolist()
                    if self.query_vectors else [0.0] * 1024,
                }
                for qid in self.queries
            ],
            "qrels": self.qrels,
        }
        with open(path, "w") as f:
            json.dump(data, f, ensure_ascii=False)

    @classmethod
    def from_json(cls, path: str) -> "RetrievalDataset":
        """Deserialize from JSON (e.g., pre-encoded cache)."""
        with open(path, "r") as f:
            data = json.load(f)
        corpus = {d["id"]: d["text"] for d in data["documents"]}
        queries = {q["id"]: q["text"] for q in data["queries"]}
        doc_vecs = {d["id"]: np.array(d["vector"], dtype=np.float32)
                     for d in data["documents"]}
        query_vecs = {q["id"]: np.array(q["vector"], dtype=np.float32)
                       for q in data["queries"]}
        return cls(
            name=data["name"],
            corpus=corpus,
            queries=queries,
            qrels=data["qrels"],
            doc_vectors=doc_vecs,
            query_vectors=query_vecs,
        )


@dataclass
class RetrievalResult:
    """Result from one retrieval run."""

    system: str               # "memhop" | "faiss-ivf" | "chromadb" | ...
    dataset: str              # dataset name
    encoder: str              # "bge-m3"

    # Per-query ranked lists: {query_id: [doc_id, ...]}
    rankings: dict[str, list[str]] = field(default_factory=dict)

    # Aggregate metrics (populated by metrics.py)
    ndcg_10: float = 0.0
    mrr: float = 0.0
    recall_1: float = 0.0
    recall_5: float = 0.0
    recall_10: float = 0.0
    precision_10: float = 0.0

    # Timing
    total_latency_ms: float = 0.0
    avg_query_latency_ms: float = 0.0

    def to_dict(self) -> dict:
        return {
            "system": self.system,
            "dataset": self.dataset,
            "encoder": self.encoder,
            "ndcg@10": round(self.ndcg_10, 4),
            "mrr": round(self.mrr, 4),
            "r@1": round(self.recall_1, 4),
            "r@5": round(self.recall_5, 4),
            "r@10": round(self.recall_10, 4),
            "p@10": round(self.precision_10, 4),
            "total_latency_ms": round(self.total_latency_ms, 1),
            "avg_query_latency_ms": round(self.avg_query_latency_ms, 2),
        }

    def to_json(self, path: str):
        with open(path, "w") as f:
            json.dump(self.to_dict(), f, indent=2, ensure_ascii=False)


@dataclass
class LatencyResult:
    """Result from a latency benchmark run."""

    scale: int                # number of documents
    system: str               # "memhop" | "faiss" | ...

    # store (insert)
    store_p50_us: float = 0.0
    store_p95_us: float = 0.0
    store_p99_us: float = 0.0
    store_ops_per_sec: float = 0.0

    # recall (query)
    recall_p50_us: float = 0.0
    recall_p95_us: float = 0.0
    recall_p99_us: float = 0.0
    recall_ops_per_sec: float = 0.0

    # memory
    disk_size_mb: float = 0.0
    memory_mb: float = 0.0

    def to_dict(self) -> dict:
        return {
            "scale": self.scale,
            "system": self.system,
            "store_p50_us": round(self.store_p50_us, 1),
            "store_p95_us": round(self.store_p95_us, 1),
            "store_p99_us": round(self.store_p99_us, 1),
            "store_ops_per_sec": round(self.store_ops_per_sec, 0),
            "recall_p50_us": round(self.recall_p50_us, 1),
            "recall_p95_us": round(self.recall_p95_us, 1),
            "recall_p99_us": round(self.recall_p99_us, 1),
            "recall_ops_per_sec": round(self.recall_ops_per_sec, 0),
            "disk_size_mb": round(self.disk_size_mb, 2),
            "memory_mb": round(self.memory_mb, 2),
        }
