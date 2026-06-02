"""BEIR dataset adapter.

Loads BEIR datasets (from beir package) into the unified RetrievalDataset format.
"""

import os
import numpy as np
from typing import Optional
from pathlib import Path

from .schema import RetrievalDataset


# All 14 BEIR datasets (MS MARCO is training-only, excluded)
BEIR_DATASETS = [
    "nfcorpus",
    "fiqa",
    "scidocs",
    "scifact",
    "arguana",
    "webis-touche2020",
    "cqadupstack",
    "quora",
    "dbpedia-entity",
    "trec-covid",
    "climate-fever",
    "fever",
    "hotpotqa",
    "nq",
]


def load_beir_dataset(
    dataset_name: str,
    data_dir: Optional[str] = None,
    subset_size: Optional[int] = None,
) -> RetrievalDataset:
    """Load a BEIR dataset.

    Args:
        dataset_name: BEIR dataset name (e.g., "nfcorpus").
        data_dir: Directory for BEIR data. Defaults to ./data/beir/.
        subset_size: If set, randomly sample this many documents.

    Returns:
        RetrievalDataset.
    """
    try:
        from beir.datasets.data_loader import GenericDataLoader
    except ImportError:
        raise ImportError(
            "beir not installed. Run: pip install beir"
        )

    if data_dir is None:
        data_dir = os.path.join(os.path.dirname(__file__), "..", "data", "beir")

    data_path = os.path.join(data_dir, dataset_name)

    # Check if data exists locally — no auto-download
    if not os.path.exists(data_path):
        raise FileNotFoundError(
            f"BEIR dataset '{dataset_name}' not found at {data_path}.\n"
            f"  Please run: bash benchmarks/download_data.sh"
        )

    loader = GenericDataLoader(data_folder=data_path)
    corpus_raw, queries_raw, qrels_raw = loader.load(split="test")

    # Convert to string keys
    corpus = {str(k): str(v.get("title", "") + " " + v.get("text", "")).strip()
              for k, v in corpus_raw.items()}
    queries = {str(k): str(v) for k, v in queries_raw.items()}
    qrels = {str(k): {str(d): int(s) for d, s in v.items()}
             for k, v in qrels_raw.items()}

    # Subset if needed
    if subset_size and len(corpus) > subset_size:
        rng = np.random.RandomState(42)
        sampled_ids = rng.choice(list(corpus.keys()), size=subset_size, replace=False)
        sampled_set = set(sampled_ids)
        corpus = {did: corpus[did] for did in sampled_ids}
        qrels = {
            qid: {did: score for did, score in rels.items() if did in sampled_set}
            for qid, rels in qrels.items()
        }
        qrels = {qid: rels for qid, rels in qrels.items() if rels}
        queries = {qid: queries[qid] for qid in qrels if qid in queries}

    return RetrievalDataset(
        name=f"BEIR-{dataset_name}",
        corpus=corpus,
        queries=queries,
        qrels=qrels,
    )


def load_all_beir(
    data_dir: Optional[str] = None,
    subset_size: Optional[int] = 100000,
    datasets: Optional[list[str]] = None,
) -> list[RetrievalDataset]:
    """Load multiple BEIR datasets.

    Args:
        data_dir: BEIR data directory.
        subset_size: Subset size per dataset.
        datasets: List of dataset names (defaults to all 14).

    Returns:
        List of RetrievalDataset.
    """
    names = datasets or BEIR_DATASETS
    results = []
    for name in names:
        print(f"  Loading BEIR/{name}...")
        try:
            ds = load_beir_dataset(name, data_dir, subset_size)
            print(f"    {ds.num_docs} docs, {ds.num_queries} queries")
            results.append(ds)
        except Exception as e:
            print(f"    SKIPPED: {e}")
    return results
