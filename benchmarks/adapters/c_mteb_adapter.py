"""C-MTEB dataset adapter.

Converts C-MTEB datasets (from mteb package or HuggingFace) into
the unified RetrievalDataset format used by all benchmarks.
"""

import os
import numpy as np
from typing import Optional
from pathlib import Path

from .schema import RetrievalDataset


# C-MTEB retrieval task names as used by mteb package
C_MTEB_RETRIEVAL_TASKS = [
    "T2Retrieval",
    "MMarcoRetrieval",
    "DuRetrieval",
    "CovidRetrieval",
    "CmedqaRetrieval",
    "EcomRetrieval",
    "MedicalRetrieval",
    "VideoRetrieval",
]

# Aliases for huggingface dataset names if different
TASK_ALIASES = {}


def load_c_mteb_task(
    task_name: str,
    cache_dir: Optional[str] = None,
    subset_size: Optional[int] = None,
) -> RetrievalDataset:
    """Load a C-MTEB retrieval task.

    Args:
        task_name: C-MTEB task name (e.g., "T2Retrieval").
        cache_dir: Directory to cache downloaded datasets.
        subset_size: If set, randomly sample this many documents.

    Returns:
        RetrievalDataset with corpus, queries, and qrels.
    """
    try:
        import mteb
    except ImportError:
        raise ImportError(
            "mteb not installed. Run: pip install mteb"
        )

    hf_name = TASK_ALIASES.get(task_name, task_name)

    # Load via mteb's task loader
    task = mteb.get_task(hf_name)
    task.load_data()

    corpus = {}
    queries = {}
    qrels = {}

    # Mteb v2 format: task.dataset = {"default": {"dev": DatasetDict(...)}}
    if hasattr(task, "dataset") and task.dataset:
        for subset_name, subset_data in task.dataset.items():
            for split_name, split in subset_data.items():
                if isinstance(split, dict):
                    # HuggingFace Dataset dict-style access
                    if "corpus" in split:
                        for row in split["corpus"]:
                            doc_id = str(row["id"])
                            text = str(row.get("text", "") or "")
                            title = str(row.get("title", "") or "")
                            corpus[doc_id] = f"{title} {text}".strip()
                    if "queries" in split:
                        for row in split["queries"]:
                            queries[str(row["id"])] = str(row["text"])
                    if "relevant_docs" in split:
                        for qid, rels in split["relevant_docs"].items():
                            qrels[str(qid)] = {str(did): int(score) for did, score in rels.items()}

    # Subset if requested
    if subset_size and len(corpus) > subset_size:
        # Keep all queries, but sample documents
        rng = np.random.RandomState(42)
        sampled_ids = rng.choice(list(corpus.keys()), size=subset_size, replace=False)
        sampled_set = set(sampled_ids)
        corpus = {did: corpus[did] for did in sampled_ids}
        # Filter qrels to only include sampled documents
        qrels = {
            qid: {did: score for did, score in rels.items() if did in sampled_set}
            for qid, rels in qrels.items()
        }
        # Remove queries with no remaining relevant documents
        qrels = {qid: rels for qid, rels in qrels.items() if rels}
        queries = {qid: queries[qid] for qid in qrels if qid in queries}

    return RetrievalDataset(
        name=task_name,
        corpus=corpus,
        queries=queries,
        qrels=qrels,
    )


def load_all_c_mteb_retrieval(
    cache_dir: Optional[str] = None,
    subset_size: Optional[int] = 100000,
) -> list[RetrievalDataset]:
    """Load all 8 C-MTEB retrieval tasks.

    Args:
        cache_dir: Cache directory.
        subset_size: Subset size per dataset (None = full).

    Returns:
        List of RetrievalDataset, one per task.
    """
    datasets = []
    for task_name in C_MTEB_RETRIEVAL_TASKS:
        print(f"  Loading {task_name}...")
        try:
            ds = load_c_mteb_task(task_name, cache_dir, subset_size)
            print(f"    {ds.num_docs} docs, {ds.num_queries} queries, "
                  f"{sum(len(v) for v in ds.qrels.values())} qrels")
            datasets.append(ds)
        except Exception as e:
            print(f"    SKIPPED: {e}")
    return datasets
