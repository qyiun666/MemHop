#!/usr/bin/env python3
"""Run C-MTEB retrieval benchmarks on MemHop and competitors.

Usage:
  python run_c_mteb.py                          # all 8 tasks, all competitors
  python run_c_mteb.py --task T2Retrieval       # single task
  python run_c_mteb.py --no-competitors         # MemHop only
  python run_c_mteb.py --subset 5000            # small subset for quick test
"""

import os
import sys
import json
import time
import argparse
import subprocess
import numpy as np
from pathlib import Path

# Add parent to path
sys.path.insert(0, str(Path(__file__).parent.parent))

from adapters import load_all_c_mteb_retrieval, load_c_mteb_task, C_MTEB_RETRIEVAL_TASKS
from adapters.schema import RetrievalDataset, RetrievalResult
from quality.metrics import ndcg_at_k, mrr, recall_at_k, precision_at_k
from competitors import FAISSRunner, ChromaRunner, MilvusLiteRunner


def encode_dataset(dataset: RetrievalDataset, model, device: str = "cpu"):
    """Encode all documents and queries with BGE-M3."""
    from sentence_transformers import SentenceTransformer

    if isinstance(model, str):
        model = SentenceTransformer(model, device=device)

    print(f"  Encoding {dataset.num_docs} documents...")
    doc_texts = [dataset.corpus[did] for did in dataset.doc_ids]
    doc_vecs = model.encode(doc_texts, show_progress_bar=True, normalize_embeddings=True)

    print(f"  Encoding {dataset.num_queries} queries...")
    query_texts = [dataset.queries[qid] for qid in dataset.query_ids]
    query_vecs = model.encode(query_texts, show_progress_bar=True, normalize_embeddings=True)

    dataset.doc_vectors = {did: vec for did, vec in zip(dataset.doc_ids, doc_vecs)}
    dataset.query_vectors = {qid: vec for qid, vec in zip(dataset.query_ids, query_vecs)}

    return dataset


def run_memhop_quality(dataset: RetrievalDataset, quality_bench_bin: str, dream_interval: int = 50) -> RetrievalResult:
    """Run MemHop via Rust quality_bench binary."""
    import tempfile

    # Prepare input JSON
    input_data = {
        "name": dataset.name,
        "documents": [
            {
                "id": did,
                "text": dataset.corpus[did],
                "vector": dataset.doc_vectors[did].tolist() if dataset.doc_vectors else None,
            }
            for did in dataset.doc_ids
        ],
        "queries": [
            {
                "id": qid,
                "text": dataset.queries[qid],
                "vector": dataset.query_vectors[qid].tolist() if dataset.query_vectors else None,
            }
            for qid in dataset.query_ids
        ],
        "qrels": dataset.qrels,
        "dream_interval": dream_interval,
        "spread_top_k": 10,
        "limit": 10,
    }

    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f_in:
        json.dump(input_data, f_in, ensure_ascii=False)
        input_path = f_in.name

    output_path = input_path.replace(".json", "_out.json")

    t0 = time.time()
    result = subprocess.run(
        [quality_bench_bin, "--input", input_path, "--output", output_path],
        capture_output=True,
        text=True,
        timeout=600,
    )
    elapsed = (time.time() - t0) * 1000

    if result.returncode != 0:
        print(f"  MemHop quality_bench failed:")
        print(f"  stdout: {result.stdout}")
        print(f"  stderr: {result.stderr}")
        raise RuntimeError(f"quality_bench exit code {result.returncode}")

    # Parse result
    with open(output_path) as f:
        data = json.load(f)

    # Find ONNX result
    onnx_result = None
    for r in data.get("results", []):
        if r["method"] == "ONNX+BGE-M3":
            onnx_result = r
            break

    if not onnx_result:
        raise RuntimeError("No ONNX+BGE-M3 result found in quality_bench output")

    rr = RetrievalResult(
        system="memhop",
        dataset=dataset.name,
        encoder="bge-m3",
        ndcg_10=onnx_result["ndcg_10"]["mean"],
        mrr=onnx_result["mrr"]["mean"],
        recall_1=onnx_result["recall_1"]["mean"],
        recall_5=onnx_result["recall_5"]["mean"],
        recall_10=onnx_result["recall_10"]["mean"],
        precision_10=onnx_result["precision_10"]["mean"],
        total_latency_ms=elapsed,
        avg_query_latency_ms=onnx_result.get("avg_recall_latency_us", 0) / 1000,
    )

    # Cleanup
    os.unlink(input_path)
    os.unlink(output_path)

    return rr


def run_competitor(dataset: RetrievalDataset, runner, top_k: int = 10) -> RetrievalResult:
    """Run a competitor (FAISS/ChromaDB/Milvus) on the dataset."""
    import time

    doc_ids = dataset.doc_ids
    doc_vecs = np.stack([dataset.doc_vectors[did] for did in doc_ids])

    t0 = time.time()
    runner.index(doc_ids, doc_vecs)
    index_time = (time.time() - t0) * 1000

    query_ids = dataset.query_ids
    query_vecs = np.stack([dataset.query_vectors[qid] for qid in query_ids])

    t0 = time.time()
    id_results, score_results = runner.search(query_vecs, top_k)
    search_time = (time.time() - t0) * 1000

    # Build rankings
    rankings = {}
    for qid, ids in zip(query_ids, id_results):
        rankings[qid] = ids

    # Compute metrics
    ndcg_scores = []
    mrr_scores = []
    r1_scores = []
    r5_scores = []
    r10_scores = []
    p10_scores = []

    for qid in query_ids:
        rel = dataset.qrels.get(qid, {})
        ranked = rankings.get(qid, [])
        ndcg_scores.append(ndcg_at_k(ranked, rel, 10))
        mrr_scores.append(mrr(ranked, rel))
        r1_scores.append(recall_at_k(ranked, rel, 1))
        r5_scores.append(recall_at_k(ranked, rel, 5))
        r10_scores.append(recall_at_k(ranked, rel, 10))
        p10_scores.append(precision_at_k(ranked, rel, 10))

    runner.clear()

    return RetrievalResult(
        system=runner.name,
        dataset=dataset.name,
        encoder="bge-m3",
        ndcg_10=float(np.mean(ndcg_scores)),
        mrr=float(np.mean(mrr_scores)),
        recall_1=float(np.mean(r1_scores)),
        recall_5=float(np.mean(r5_scores)),
        recall_10=float(np.mean(r10_scores)),
        precision_10=float(np.mean(p10_scores)),
        total_latency_ms=index_time + search_time,
        avg_query_latency_ms=search_time / len(query_ids) if query_ids else 0,
    )


def main():
    parser = argparse.ArgumentParser(description="C-MTEB benchmarks for MemHop")
    parser.add_argument("--task", type=str, help="Single task name")
    parser.add_argument("--subset", type=int, default=50000, help="Document subset size")
    parser.add_argument("--no-competitors", action="store_true", help="Skip competitors")
    parser.add_argument("--quality-bench-bin", type=str,
                        default="../target/release/quality_bench",
                        help="Path to quality_bench binary")
    parser.add_argument("--model", type=str, default="BAAI/bge-m3", help="Embedding model")
    parser.add_argument("--dream-interval", type=int, default=50, help="Dream interval")
    parser.add_argument("--output", type=str, default=None, help="Output JSON path")
    args = parser.parse_args()

    # Load tasks
    tasks = [args.task] if args.task else C_MTEB_RETRIEVAL_TASKS
    print(f"=== C-MTEB Benchmark ===")
    print(f"Tasks: {tasks}")
    print(f"Subset: {args.subset if args.subset else 'full'}")
    print(f"Competitors: {'skip' if args.no_competitors else 'faiss + chromadb + milvus'}")
    print()

    # Load encoder
    print("Loading BGE-M3 encoder...")
    from sentence_transformers import SentenceTransformer
    model = SentenceTransformer(args.model, device="cpu")
    print(f"  Model: {args.model} dim={model.get_sentence_embedding_dimension()}")
    print()

    all_results = []

    for task_name in tasks:
        print(f"\n{'='*60}")
        print(f"  Task: {task_name}")
        print(f"{'='*60}")

        # Load dataset
        ds = load_c_mteb_task(task_name, subset_size=args.subset)
        print(f"  Loaded: {ds.num_docs} docs, {ds.num_queries} queries")

        # Encode
        ds = encode_dataset(ds, model)

        # Run MemHop
        print(f"  Running MemHop...")
        try:
            mh_result = run_memhop_quality(ds, args.quality_bench_bin, args.dream_interval)
            all_results.append(mh_result)
            print(f"    NDCG@10={mh_result.ndcg_10:.4f}  MRR={mh_result.mrr:.4f}  R@10={mh_result.recall_10:.4f}")
        except Exception as e:
            print(f"    MemHop FAILED: {e}")

        # Run competitors
        if not args.no_competitors:
            competitors = [
                ("FAISS-IVF", FAISSRunner("IVF", dim=model.get_sentence_embedding_dimension())),
                ("FAISS-HNSW", FAISSRunner("HNSW", dim=model.get_sentence_embedding_dimension())),
                ("ChromaDB", ChromaRunner(dim=model.get_sentence_embedding_dimension())),
                ("Milvus-Lite", MilvusLiteRunner(dim=model.get_sentence_embedding_dimension())),
            ]

            for name, runner in competitors:
                print(f"  Running {name}...")
                try:
                    result = run_competitor(ds, runner)
                    all_results.append(result)
                    print(f"    NDCG@10={result.ndcg_10:.4f}  MRR={result.mrr:.4f}  R@10={result.recall_10:.4f}")
                except Exception as e:
                    print(f"    {name} SKIPPED: {e}")

    # Print summary
    print(f"\n{'='*80}")
    print(f"  C-MTEB Results Summary")
    print(f"{'='*80}")
    print(f"  {'System':<20} {'NDCG@10':>10} {'MRR':>10} {'R@10':>10}")
    print(f"  {'-'*50}")
    for r in sorted(all_results, key=lambda x: x.ndcg_10, reverse=True):
        print(f"  {r.system:<20} {r.ndcg_10:>10.4f} {r.mrr:>10.4f} {r.recall_10:>10.4f}")

    # Save results
    if args.output:
        output_data = {"results": [r.to_dict() for r in all_results]}
        with open(args.output, "w") as f:
            json.dump(output_data, f, indent=2, ensure_ascii=False)
        print(f"\nResults saved to: {args.output}")

    return all_results


if __name__ == "__main__":
    main()
