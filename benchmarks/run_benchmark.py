#!/usr/bin/env python3
"""MemHop Unified Benchmark Entry Point.

Replaces all legacy run_*.py scripts with a single CLI entrypoint.
Supports multiple encoders, datasets, modes, and report comparison.

Usage:
    # BGE-M3 baseline
    python benchmarks/run_benchmark.py --encoder bge-m3 --datasets lme-s,nfcorpus --modes retrieval,associative

    # Dual-small model validation
    python benchmarks/run_benchmark.py --encoder dual-small --datasets lme-s,nfcorpus --modes retrieval,associative

    # Quick smoke test (first N problems)
    python benchmarks/run_benchmark.py --encoder bge-m3 --datasets lme-s --subset 10

    # Compare saved reports
    python benchmarks/run_benchmark.py --compare reports/bge_m3_*.json reports/dual_small_*.json

    # Pure retrieval only
    python benchmarks/run_benchmark.py --encoder bge-m3 --datasets nfcorpus --modes retrieval
"""

import argparse
import glob
import json
import os
import shutil
import sys
import time

import numpy as np

# Ensure benchmarks/ directory is on sys.path for internal imports.
# This is needed when importing run_benchmark from the project root
# (e.g. "python3 -c 'from benchmarks.run_benchmark import ...'").
_SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
if _SCRIPT_DIR not in sys.path:
    sys.path.insert(0, _SCRIPT_DIR)

from mcp_client import MemHopMCPClient
from quality.metrics import aggregate_metrics
from adapters.schema import (
    BenchmarkResult,
    EncoderInfo,
    DatasetInfo,
    SystemInfo,
    LatencyInfo,
)

# ── version & paths ────────────────────────────────────────

MEMHOP_VERSION = "0.11.0"
SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
REPORT_DIR = os.path.join(SCRIPT_DIR, "reports")
TEMP = "/tmp/memhop_bench"

MCP_BIN = os.environ.get(
    "MEMHOP_MCP_BIN",
    os.path.join(os.path.dirname(SCRIPT_DIR), "target/release/memhop-mcp-server"),
)

LME_DATA = os.environ.get(
    "LME_DATA_PATH",
    os.path.join(SCRIPT_DIR, "data", "lme", "longmemeval_s_cleaned.json"),
)

# ── runner ─────────────────────────────────────────────────


class MemHopMCPRunner:
    """MemHop through MCP server — the only correct way to test."""

    def __init__(self, mode="retrieval", dream=True, encoder="bge-m3"):
        self.mode = mode
        self.dream = dream
        self.encoder_name = encoder

        # Lazy-loaded dual-small encoder
        self._dual_encoder = None
        if encoder == "dual-small":
            from encoders.dual_small import DualSmallEncoder

            self._dual_encoder = DualSmallEncoder()

        # MCP subprocess
        self._db_dir = os.path.join(TEMP, f"bench_{os.urandom(4).hex()}")
        env_extra = {}

        # Map encoder name → model directory name
        model_map = {
            "bge-m3": "bge-m3",
            "bge-small-en": "bge-small-en-v1.5",
            "bge-small-zh": "bge-small-zh-v1.5",
            "bge-base-en": "bge-base-en-v1.5",
            "bge-base-zh": "bge-base-zh-v1.5",
        }
        if encoder in model_map:
            model_path = os.path.join(
                os.path.dirname(os.path.dirname(__file__)),
                "models",
                model_map[encoder],
            )
            # Candle uses model.safetensors
            if os.path.exists(os.path.join(model_path, "model.safetensors")):
                env_extra["MEMHOP_ONNX_MODEL"] = model_path
                print(f"  Model: {model_path}")
        self._mcp = MemHopMCPClient(
            MCP_BIN, self._db_dir, env_extra=env_extra, recv_timeout=3600
        )
        self._mcp.start_reader()

        # ID mapping (retrieval mode)
        self._id_map = {}  # engram_id -> doc_id

    # ── public API ─────────────────────────────────────────

    def index(self, docs):
        """Store docs via MCP.

        If encoder="dual-small", pre-encode text and pass vector + tree_path.
        If dream=True, runs dream() after all stores.

        Args:
            docs: list of {"id", "text", "session_id", "turn_id", "turn_index", "topic_label"}
        Returns:
            Dream result dict, or None.
        """
        if not docs:
            return None

        for doc in docs:
            text = doc.get("text", "")
            if not text:
                continue

            kwargs = {
                "session_id": doc.get("session_id", "bench"),
                "turn_id": doc.get("turn_id", ""),
                "turn_index": doc.get("turn_index", 0),
            }

            topic_label = doc.get("topic_label")
            if topic_label:
                kwargs["topic_label"] = topic_label

            if self._dual_encoder:
                vector, tree_name = self._dual_encoder.encode(text)
                kwargs["vector"] = vector
                if tree_name:
                    kwargs["tree_path"] = tree_name

            result = self._mcp.store(text, **kwargs)
            eid = result.get("engram_id") or result.get("memory_id")
            if eid:
                doc_id = doc.get("id", "")
                self._id_map[eid] = doc_id

        if self.dream:
            return self._mcp.dream()
        return None

    def search(self, query, top_k=10):
        """Recall via MCP.

        Args:
            query: Query string.
            top_k: Maximum number of results.

        Returns:
            (ranked_ids, raw_response)
              ranked_ids: [session_id, ...] for associative mode,
                          [doc_id/session_id, ...] for retrieval mode.
              raw_response: Full MCP response dict.
        """
        kwargs = {"limit": top_k}

        if self._dual_encoder:
            query_vector, tree_name = self._dual_encoder.encode(query)
            kwargs["query_vector"] = query_vector
            if tree_name:
                kwargs["tree"] = tree_name

        raw = self._mcp.recall(query, **kwargs)
        resp_result = raw.get("result", {})

        if self.mode == "associative":
            agg_sessions = resp_result.get("aggregated_sessions", [])
            agg_sessions.sort(
                key=lambda s: s.get("total_score", 0), reverse=True
            )
            ranked_ids = [
                s["session_id"]
                for s in agg_sessions
                if s.get("session_id")
            ]
        else:
            results = resp_result.get("results", [])
            ranked_ids = []
            seen = set()
            for item in results:
                eid = item.get("id") or item.get("engram_id")
                doc_id = self._id_map.get(eid, eid)  # fallback to engram_id
                if doc_id not in seen:
                    seen.add(doc_id)
                    ranked_ids.append(doc_id)

        return ranked_ids[:top_k], raw

    def clear(self):
        """Kill MCP subprocess and delete temporary database."""
        try:
            self._mcp.close()
        except Exception:
            pass
        if os.path.exists(self._db_dir):
            shutil.rmtree(self._db_dir, ignore_errors=True)


# ── helpers ────────────────────────────────────────────────


def _build_encoder_info(encoder_name):
    """Build EncoderInfo for a given encoder name."""
    if encoder_name == "bge-m3":
        return EncoderInfo(model_id="BAAI/bge-m3", dim=1024, source="mcp_builtin")
    return EncoderInfo(
        model_id="BAAI/bge-small-zh-v1.5",
        alt_model_id="sentence-transformers/all-MiniLM-L6-v2",
        dim=512,
        source="python",
        tree_dims={"zh": 512, "en": 384},
    )


def _make_latency_info(latencies_us):
    """Build LatencyInfo from a list of per-query latencies in microseconds."""
    if not latencies_us:
        return LatencyInfo()
    return LatencyInfo(
        avg_recall_us=float(np.mean(latencies_us)),
        p95_recall_us=float(np.percentile(latencies_us, 95)),
    )


# ── dataset runners ────────────────────────────────────────


def run_lme_s(runner, docs, queries, qrels):
    """LongMemEval-S benchmark: session-level associative retrieval.

    Uses pre-loaded data from lme_adapter.  Metrics are evaluated at
    session level (aggregated_sessions for associative mode,
    doc-level ranking for retrieval mode).
    """
    dream_result = runner.index(docs)

    rankings = {}
    latencies = []
    for q in queries:
        t0 = time.time()
        ranked_ids, _ = runner.search(q["text"], top_k=10)
        latencies.append((time.time() - t0) * 1e6)
        rankings[q["id"]] = ranked_ids

    metrics = aggregate_metrics(rankings, qrels)

    return BenchmarkResult(
        timestamp=time.strftime("%Y-%m-%dT%H:%M:%S+08:00"),
        memhop_version=MEMHOP_VERSION,
        encoder=_build_encoder_info(runner.encoder_name),
        dataset=DatasetInfo(
            name="LongMemEval-S",
            num_docs=len(docs),
            num_queries=len(queries),
        ),
        system=SystemInfo(
            mode=runner.mode, dream=runner.dream, dream_result=dream_result
        ),
        metrics=metrics,
        latency=_make_latency_info(latencies),
    )


def run_nfcorpus(runner, subset_size=500):
    """BEIR nfcorpus: pure text retrieval evaluation."""
    from adapters.beir_adapter import load_beir_dataset

    dataset = load_beir_dataset("nfcorpus", subset_size=subset_size)

    docs = [
        {"id": did, "text": dataset.corpus[did], "session_id": "nfcorpus"}
        for did in dataset.doc_ids
    ]
    queries_list = [
        {"id": qid, "text": dataset.queries[qid]} for qid in dataset.query_ids
    ]

    dream_result = runner.index(docs)

    rankings = {}
    latencies = []
    for q in queries_list:
        t0 = time.time()
        ranked_ids, _ = runner.search(q["text"], top_k=10)
        latencies.append((time.time() - t0) * 1e6)
        rankings[q["id"]] = ranked_ids

    metrics = aggregate_metrics(rankings, dataset.qrels)

    return BenchmarkResult(
        timestamp=time.strftime("%Y-%m-%dT%H:%M:%S+08:00"),
        memhop_version=MEMHOP_VERSION,
        encoder=_build_encoder_info(runner.encoder_name),
        dataset=DatasetInfo(
            name="BEIR-nfcorpus",
            num_docs=len(docs),
            num_queries=len(queries_list),
        ),
        system=SystemInfo(
            mode=runner.mode, dream=runner.dream, dream_result=dream_result
        ),
        metrics=metrics,
        latency=_make_latency_info(latencies),
    )


def run_c_mteb(runner, subset_size=None):
    """C-MTEB T2Retrieval tasks: Chinese retrieval evaluation.

    Returns a list of BenchmarkResult, one per task.
    """
    from adapters.c_mteb_adapter import load_c_mteb_task

    task_names = [
        "T2Retrieval",
        "MMarcoRetrieval",
        "DuRetrieval",
        "CovidRetrieval",
        "CmedqaRetrieval",
        "EcomRetrieval",
        "MedicalRetrieval",
        "VideoRetrieval",
    ]
    if subset_size:
        task_names = task_names[:subset_size]

    results = []
    for task_name in task_names:
        try:
            task = load_c_mteb_task(task_name)
        except Exception as e:
            print(f"  ⚠ Failed to load {task_name}: {e}")
            continue

        # Each C-MTEB sub-task gets its own runner (independent MCP + DB)
        task_runner = MemHopMCPRunner(
            mode=runner.mode, dream=runner.dream, encoder=runner.encoder_name
        )
        try:
            docs = [
                {"id": did, "text": task.corpus[did], "session_id": task_name}
                for did in task.doc_ids
            ]
            queries_list = [
                {"id": qid, "text": task.queries[qid]} for qid in task.query_ids
            ]

            dream_result = task_runner.index(docs)

            rankings = {}
            latencies = []
            for q in queries_list:
                t0 = time.time()
                ranked_ids, _ = task_runner.search(q["text"], top_k=10)
                latencies.append((time.time() - t0) * 1e6)
                rankings[q["id"]] = ranked_ids

            metrics = aggregate_metrics(rankings, task.qrels)

            result = BenchmarkResult(
                timestamp=time.strftime("%Y-%m-%dT%H:%M:%S+08:00"),
                memhop_version=MEMHOP_VERSION,
                encoder=_build_encoder_info(runner.encoder_name),
                dataset=DatasetInfo(
                    name=f"C-MTEB-{task_name}",
                    num_docs=len(docs),
                    num_queries=len(queries_list),
                ),
                system=SystemInfo(
                    mode=runner.mode,
                    dream=runner.dream,
                    dream_result=dream_result,
                ),
                metrics=metrics,
                latency=_make_latency_info(latencies),
            )
            results.append(result)
        finally:
            task_runner.clear()

    return results


# ── LoCoMo ─────────────────────────────────────────────────


def run_locomo(runner, subset=None):
    """LoCoMo benchmark: long-context conversational memory.

    Stores each conversation turn individually, recalls with questions,
    and evaluates via text-containment F1 (answer string found in recalled text).
    """
    from adapters.locomo_adapter import load_locomo_dataset

    data_dir = os.path.join(SCRIPT_DIR, "data", "locomo")
    docs, queries, answers = load_locomo_dataset(data_dir, subset)

    dream_result = runner.index(docs)

    recalled_texts_list: list[list[str]] = []
    latencies: list[float] = []
    for q in queries:
        t0 = time.time()
        ranked_ids, raw = runner.search(q["text"], top_k=10)
        latencies.append((time.time() - t0) * 1e6)

        # Extract recalled texts from raw MCP response for F1 evaluation
        texts = []
        resp_result = raw.get("result", {})
        for item in resp_result.get("results", []):
            text = item.get("text", "")
            if text:
                texts.append(text)
        recalled_texts_list.append(texts)

    from quality.metrics import aggregate_locomo_f1

    metrics = aggregate_locomo_f1(recalled_texts_list, answers, k=10)

    return BenchmarkResult(
        timestamp=time.strftime("%Y-%m-%dT%H:%M:%S+08:00"),
        memhop_version=MEMHOP_VERSION,
        encoder=_build_encoder_info(runner.encoder_name),
        dataset=DatasetInfo(
            name="LoCoMo",
            num_docs=len(docs),
            num_queries=len(queries),
        ),
        system=SystemInfo(
            mode=runner.mode, dream=runner.dream, dream_result=dream_result
        ),
        metrics=metrics,
        latency=_make_latency_info(latencies),
    )


# ── DMR ────────────────────────────────────────────────────


def run_dmr(runner, subset=None):
    """DMR (Deep Memory Retrieval) benchmark.

    Multi-Session Chat 5-session conversations: sessions 1-4 stored as
    memories, questions about their content evaluated via DeepSeek LLM judge.
    """
    from adapters.dmr_adapter import load_dmr_dataset

    cache_dir = os.path.join(SCRIPT_DIR, "data", "dmr")
    docs, queries, answers, conv_meta = load_dmr_dataset(
        cache_dir, subset, n_questions_per_conv=3
    )

    if not queries:
        print("  ⚠ No DMR questions loaded (check dataset/DeepSeek API)")
        return None

    dream_result = runner.index(docs)

    from utils.llm_client import DeepSeekJudge

    judge = DeepSeekJudge()
    scores: list[float] = []
    latencies: list[float] = []
    total = len(queries)

    for qi, (q, expected) in enumerate(zip(queries, answers)):
        print(f"  DMR [{qi + 1}/{total}] {q['text'][:60]}...", end=" ", flush=True)

        t0 = time.time()
        ranked_ids, raw = runner.search(q["text"], top_k=10)
        latencies.append((time.time() - t0) * 1e6)

        # Build context from recalled texts
        context_lines = []
        resp_result = raw.get("result", {})
        for item in resp_result.get("results", []):
            text = item.get("text", "")
            if text:
                context_lines.append(text)
        context = "\n".join(context_lines[:5])

        # Evaluate with DeepSeek judge
        try:
            score = judge.evaluate_answer(q["text"], expected, context)
        except Exception as e:
            print(f"⚠ judge failed: {e}")
            score = 0.0
        scores.append(score)
        print("PASS" if score > 0 else "FAIL")

    # Aggregate accuracy
    arr = np.array(scores)
    accuracy = {
        "accuracy": {
            "mean": float(arr.mean()),
            "std": float(arr.std(ddof=1)) if len(arr) > 1 else 0.0,
        },
        "num_queries": len(scores),
    }

    return BenchmarkResult(
        timestamp=time.strftime("%Y-%m-%dT%H:%M:%S+08:00"),
        memhop_version=MEMHOP_VERSION,
        encoder=_build_encoder_info(runner.encoder_name),
        dataset=DatasetInfo(
            name="DMR",
            num_docs=len(docs),
            num_queries=len(queries),
        ),
        system=SystemInfo(
            mode=runner.mode, dream=runner.dream, dream_result=dream_result
        ),
        metrics=accuracy,
        latency=_make_latency_info(latencies),
    )


# ── comparison ─────────────────────────────────────────────


def _get_metric_val(report, *keys):
    """Extract a metric value from a report, supporting new (nested) and old (flat) formats.

    Tries each key in order, returning the first found value.
    For nested format (value is a dict with "mean"), unwraps the mean.
    """
    for key in keys:
        val = report.get(key)
        if val is not None:
            if isinstance(val, dict):
                mean = val.get("mean")
                if mean is not None:
                    return float(mean)
            try:
                return float(val)
            except (TypeError, ValueError):
                pass
    return 0.0


def compare_reports(patterns):
    """Load multiple JSON reports and print a comparison table."""
    paths = []
    for p in patterns:
        matched = glob.glob(p)
        if matched:
            paths.extend(matched)
        elif os.path.isfile(p):
            paths.append(p)

    if not paths:
        print("No report files found matching the given patterns.")
        return

    reports = []
    for path in paths:
        try:
            data = json.load(open(path))
            if not isinstance(data, dict):
                print(f"  \u26a0\ufe0f  Skipping {path}: not a JSON object")
                continue
            reports.append(data)
        except Exception as e:
            print(f"  \u26a0\ufe0f  Failed to load {path}: {e}")

    if not reports:
        print("No valid report files could be loaded.")
        return

    # Group by (dataset, mode)
    groups = {}
    for r in reports:
        # Handle both new (nested object) and old (flat string) report formats
        ds_val = r.get("dataset", "unknown")
        ds = ds_val.get("name", "unknown") if isinstance(ds_val, dict) else str(ds_val)

        sys_val = r.get("system", "unknown")
        mode = sys_val.get("mode", "unknown") if isinstance(sys_val, dict) else str(sys_val)

        enc_val = r.get("encoder", "unknown")
        enc_id = enc_val.get("model_id", "unknown") if isinstance(enc_val, dict) else str(enc_val)

        key = (ds, mode)
        groups.setdefault(key, []).append((enc_id, r))

    # Optional tabulate
    try:
        from tabulate import tabulate

        use_tabulate = True
    except ImportError:
        use_tabulate = False

    for (ds, mode), entries in sorted(groups.items()):
        print(f"\nDataset: {ds}  |  Mode: {mode}")
        headers = ["Encoder", "NDCG@10", "MRR", "R@1", "R@5"]
        rows = []
        for enc_id, r in sorted(entries, key=lambda x: x[0]):
            m = r.get("metrics", r)  # fallback to report itself for old flat format
            ndcg = _get_metric_val(m, "ndcg_10", "ndcg@10")
            mrr_val = _get_metric_val(m, "mrr")
            r1 = _get_metric_val(m, "recall_1", "r@1", "session_r@1")
            r5 = _get_metric_val(m, "recall_5", "r@5", "session_r@5")
            enc_name = enc_id.split("/")[-1] if "/" in enc_id else enc_id
            rows.append(
                [enc_name, f"{ndcg:.4f}", f"{mrr_val:.4f}", f"{r1:.4f}", f"{r5:.4f}"]
            )

        if use_tabulate:
            print(tabulate(rows, headers=headers, tablefmt="pipe"))
        else:
            header_line = " | ".join(headers)
            sep = "-" * len(header_line)
            print(header_line)
            print(sep)
            for row in rows:
                print(" | ".join(row))


# ── main ───────────────────────────────────────────────────


def main():
    parser = argparse.ArgumentParser(description="MemHop Unified Benchmark")
    parser.add_argument(
        "--encoder",
        choices=["bge-m3", "dual-small"],
        default="bge-m3",
        help="Encoder to use (default: bge-m3)",
    )
    parser.add_argument(
        "--datasets",
        default="lme-s,nfcorpus",
        help="Comma-separated dataset names: lme-s,nfcorpus,c-mteb,locomo,dmr",
    )
    parser.add_argument(
        "--modes",
        default="retrieval,associative",
        help="Comma-separated modes: retrieval,associative",
    )
    parser.add_argument(
        "--subset",
        type=int,
        default=None,
        help="Limit dataset size (e.g. --subset 10 for 10 problems)",
    )
    parser.add_argument(
        "--compare",
        nargs="+",
        default=None,
        help="Compare report files (glob patterns)",
    )
    parser.add_argument(
        "--dream",
        action="store_true",
        default=True,
        help="Enable dream consolidation (default: True)",
    )
    parser.add_argument(
        "--no-dream",
        dest="dream",
        action="store_false",
        help="Disable dream consolidation",
    )

    args = parser.parse_args()

    if args.compare:
        compare_reports(args.compare)
        return

    datasets = [d.strip() for d in args.datasets.split(",")]
    modes = [m.strip() for m in args.modes.split(",")]

    os.makedirs(REPORT_DIR, exist_ok=True)
    os.makedirs(TEMP, exist_ok=True)

    all_reports = []

    for mode in modes:
        for ds_name in datasets:
            print(f"\n{'─'*50}")
            print(f"  {ds_name} / {mode} ({args.encoder})")
            print(f"{'─'*50}")

            runner = MemHopMCPRunner(
                mode=mode, dream=args.dream, encoder=args.encoder
            )

            try:
                if ds_name == "lme-s":
                    from adapters.lme_adapter import load_lme_dataset

                    if not os.path.exists(LME_DATA):
                        print(f"  ❌ LME data not found at {LME_DATA}")
                        continue
                    docs, queries, qrels = load_lme_dataset(
                        LME_DATA, args.subset
                    )
                    results = run_lme_s(runner, docs, queries, qrels)
                elif ds_name == "nfcorpus":
                    results = run_nfcorpus(runner, args.subset or 500)
                elif ds_name == "c-mteb":
                    results = run_c_mteb(runner, args.subset)
                elif ds_name == "locomo":
                    results = run_locomo(runner, args.subset)
                elif ds_name == "dmr":
                    results = run_dmr(runner, args.subset)
                    if results is None:
                        continue
                else:
                    print(f"  ❌ Unknown dataset: {ds_name}")
                    continue

                if not isinstance(results, list):
                    results = [results]

                for r in results:
                    rpt_path = os.path.join(
                        REPORT_DIR,
                        f"{args.encoder}_{r.dataset.name}_{mode}_"
                        f"{time.strftime('%Y%m%d_%H%M%S')}.json",
                    )
                    r.to_json(rpt_path)
                    print(f"  ✅ Saved: {rpt_path}")
                    all_reports.append(r)

            except Exception as e:
                print(f"  ❌ {ds_name}/{mode} failed: {e}")
                import traceback

                traceback.print_exc()
            finally:
                runner.clear()

    # Summary
    print(f"\n{'='*60}")
    print(f"  MemHop v{MEMHOP_VERSION} — {args.encoder}")
    print(f"  {len(all_reports)} reports generated")
    print(f"{'='*60}")


if __name__ == "__main__":
    main()
