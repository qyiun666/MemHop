#!/usr/bin/env python3
"""Run BEIR benchmarks on MemHop and competitors.

Usage:
  python run_beir.py                              # all 14 datasets
  python run_beir.py --dataset nfcorpus           # single dataset
  python run_beir.py --subset 5000 --no-competitors  # quick MemHop-only test
"""

import os
import sys
import json
import time
import argparse
import subprocess
import numpy as np
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent))

from adapters import load_beir_dataset, load_all_beir, BEIR_DATASETS
from adapters.schema import RetrievalDataset, RetrievalResult
from quality.metrics import ndcg_at_k, mrr, recall_at_k, precision_at_k
from competitors import FAISSRunner, ChromaRunner, MilvusLiteRunner


def encode_dataset(dataset, model):
    """Encode with BGE-M3."""
    print(f"  Encoding {dataset.num_docs} docs...")
    doc_texts = [dataset.corpus[did] for did in dataset.doc_ids]
    doc_vecs = model.encode(doc_texts, show_progress_bar=True, normalize_embeddings=True)

    print(f"  Encoding {dataset.num_queries} queries...")
    query_texts = [dataset.queries[qid] for qid in dataset.query_ids]
    query_vecs = model.encode(query_texts, show_progress_bar=True, normalize_embeddings=True)

    dataset.doc_vectors = {did: vec for did, vec in zip(dataset.doc_ids, doc_vecs)}
    dataset.query_vectors = {qid: vec for qid, vec in zip(dataset.query_ids, query_vecs)}
    return dataset


def run_memhop_quality(dataset, quality_bench_bin, dream_interval=50):
    """Run MemHop via quality_bench binary."""
    import tempfile

    input_data = {
        "name": dataset.name,
        "documents": [
            {"id": did, "text": dataset.corpus[did],
             "vector": dataset.doc_vectors[did].tolist() if dataset.doc_vectors else None}
            for did in dataset.doc_ids
        ],
        "queries": [
            {"id": qid, "text": dataset.queries[qid],
             "vector": dataset.query_vectors[qid].tolist() if dataset.query_vectors else None}
            for qid in dataset.query_ids
        ],
        "qrels": dataset.qrels,
        "dream_interval": dream_interval,
        "spread_top_k": 10,
        "limit": 10,
    }

    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        json.dump(input_data, f, ensure_ascii=False)
        input_path = f.name

    output_path = input_path.replace(".json", "_out.json")
    t0 = time.time()
    result = subprocess.run(
        [quality_bench_bin, "--input", input_path, "--output", output_path],
        capture_output=True, text=True, timeout=600,
    )
    elapsed = (time.time() - t0) * 1000

    if result.returncode != 0:
        print(f"  FAILED: {result.stderr}")
        raise RuntimeError(f"quality_bench exit {result.returncode}")

    with open(output_path) as f:
        data = json.load(f)

    onnx = next((r for r in data["results"] if r["method"] == "ONNX+BGE-M3"), None)
    if not onnx:
        raise RuntimeError("No ONNX result found")

    rr = RetrievalResult(
        system="memhop", dataset=dataset.name, encoder="bge-m3",
        ndcg_10=onnx["ndcg_10"]["mean"], mrr=onnx["mrr"]["mean"],
        recall_1=onnx["recall_1"]["mean"], recall_5=onnx["recall_5"]["mean"],
        recall_10=onnx["recall_10"]["mean"], precision_10=onnx["precision_10"]["mean"],
        total_latency_ms=elapsed,
        avg_query_latency_ms=onnx.get("avg_recall_latency_us", 0) / 1000,
    )
    os.unlink(input_path)
    os.unlink(output_path)
    return rr


def run_competitor(dataset, runner, top_k=10):
    """Run a competitor."""
    doc_ids = dataset.doc_ids
    doc_vecs = np.stack([dataset.doc_vectors[did] for did in doc_ids])

    t0 = time.time()
    runner.index(doc_ids, doc_vecs)
    index_time = (time.time() - t0) * 1000

    query_ids = dataset.query_ids
    query_vecs = np.stack([dataset.query_vectors[qid] for qid in query_ids])

    t0 = time.time()
    id_results, _ = runner.search(query_vecs, top_k)
    search_time = (time.time() - t0) * 1000

    rankings = {qid: ids for qid, ids in zip(query_ids, id_results)}

    metrics_lists = {k: [] for k in ["ndcg", "mrr", "r1", "r5", "r10", "p10"]}
    for qid in query_ids:
        rel = dataset.qrels.get(qid, {})
        ranked = rankings.get(qid, [])
        metrics_lists["ndcg"].append(ndcg_at_k(ranked, rel, 10))
        metrics_lists["mrr"].append(mrr(ranked, rel))
        metrics_lists["r1"].append(recall_at_k(ranked, rel, 1))
        metrics_lists["r5"].append(recall_at_k(ranked, rel, 5))
        metrics_lists["r10"].append(recall_at_k(ranked, rel, 10))
        metrics_lists["p10"].append(precision_at_k(ranked, rel, 10))

    runner.clear()

    return RetrievalResult(
        system=runner.name, dataset=dataset.name, encoder="bge-m3",
        ndcg_10=float(np.mean(metrics_lists["ndcg"])),
        mrr=float(np.mean(metrics_lists["mrr"])),
        recall_1=float(np.mean(metrics_lists["r1"])),
        recall_5=float(np.mean(metrics_lists["r5"])),
        recall_10=float(np.mean(metrics_lists["r10"])),
        precision_10=float(np.mean(metrics_lists["p10"])),
        total_latency_ms=index_time + search_time,
        avg_query_latency_ms=search_time / len(query_ids) if query_ids else 0,
    )


def main():
    parser = argparse.ArgumentParser(description="BEIR benchmarks for MemHop")
    parser.add_argument("--dataset", type=str, help="Single dataset name")
    parser.add_argument("--subset", type=int, default=50000, help="Subset size")
    parser.add_argument("--no-competitors", action="store_true")
    parser.add_argument("--quality-bench-bin", type=str,
                        default="../target/release/quality_bench")
    parser.add_argument("--model", type=str, default="BAAI/bge-m3")
    parser.add_argument("--output", type=str, default=None)
    args = parser.parse_args()

    datasets = [args.dataset] if args.dataset else BEIR_DATASETS
    print(f"=== BEIR Benchmark === ({len(datasets)} datasets)")

    from sentence_transformers import SentenceTransformer
    model = SentenceTransformer(args.model, device="cpu")
    dim = model.get_sentence_embedding_dimension()

    all_results = []

    for ds_name in datasets:
        print(f"\n--- BEIR/{ds_name} ---")
        ds = load_beir_dataset(ds_name, subset_size=args.subset)
        print(f"  {ds.num_docs} docs, {ds.num_queries} queries")
        ds = encode_dataset(ds, model)

        # MemHop
        try:
            r = run_memhop_quality(ds, args.quality_bench_bin)
            all_results.append(r)
            print(f"  MemHop: NDCG@10={r.ndcg_10:.4f}")
        except Exception as e:
            print(f"  MemHop FAILED: {e}")

        # Competitors
        if not args.no_competitors:
            for name, runner_cls in [
                ("FAISS-HNSW", lambda: FAISSRunner("HNSW", dim)),
                ("ChromaDB", lambda: ChromaRunner(dim=dim)),
            ]:
                try:
                    runner = runner_cls()
                    r = run_competitor(ds, runner)
                    all_results.append(r)
                    print(f"  {name}: NDCG@10={r.ndcg_10:.4f}")
                except Exception as e:
                    print(f"  {name} SKIPPED: {e}")

    # Summary by dataset
    from collections import defaultdict
    by_dataset = defaultdict(list)
    for r in all_results:
        by_dataset[r.dataset].append(r)

    print(f"\n{'='*80}")
    print("BEIR Results Summary (NDCG@10)")
    print(f"{'='*80}")
    for ds, results in sorted(by_dataset.items()):
        print(f"\n  {ds}:")
        for r in sorted(results, key=lambda x: x.ndcg_10, reverse=True):
            print(f"    {r.system:<20} {r.ndcg_10:.4f}")

    if args.output:
        with open(args.output, "w") as f:
            json.dump({"results": [r.to_dict() for r in all_results]}, f, indent=2, ensure_ascii=False)
        print(f"\nSaved: {args.output}")


if __name__ == "__main__":
    main()
