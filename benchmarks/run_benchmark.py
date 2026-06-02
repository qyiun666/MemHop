#!/usr/bin/env python3
"""MemHop v0.13 Unified Benchmark Entry Point.

Single MCP process for all datasets, agent_id for isolation,
per-turn perceive flow with caller-controlled Dream.

Usage:
    python benchmarks/run_benchmark.py --all
    python benchmarks/run_benchmark.py --datasets nfcorpus --subset 10
    python benchmarks/run_benchmark.py --compare reports/*.json
"""

import os
os.environ.setdefault("HF_DATASETS_OFFLINE", "1")

import argparse
import glob
import json
import os
import shutil
import sys
import time

import numpy as np

_SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
if _SCRIPT_DIR not in sys.path:
    sys.path.insert(0, _SCRIPT_DIR)

from config import (
    MEMHOP_VERSION,
    SCRIPT_DIR,
    REPORT_DIR,
    MCP_BIN,
    LME_DATA,
    MODEL_MAP,
    MODEL_DIR,
    ALL_DATASETS,
    COMPETITOR_DATA,
)
from mcp_client import MemHopMCPClient
from quality.metrics import aggregate_metrics, aggregate_locomo_f1
from adapters.schema import (
    BenchmarkResult,
    EncoderInfo,
    DatasetInfo,
    SystemInfo,
    LatencyInfo,
)


class MemHopMCPRunner:
    """MemHop through a SINGLE MCP process with agent_id isolation (v0.13)."""

    def __init__(self, mode="retrieval", encoder="bge-m3"):
        self.mode = mode
        self.encoder_name = encoder
        self._mcp = None
        self._id_map = {}

        self._dual_encoder = None
        if encoder == "dual-small":
            from encoders.dual_small import DualSmallEncoder
            self._dual_encoder = DualSmallEncoder()

    def ensure_mcp(self):
        if self._mcp is not None:
            return
        env_extra = {}
        if self.encoder_name in MODEL_MAP:
            model_path = os.path.join(MODEL_DIR, MODEL_MAP[self.encoder_name])
            if os.path.exists(os.path.join(model_path, "model.safetensors")):
                env_extra["MEMHOP_ONNX_MODEL"] = model_path
                print(f"  Model: {model_path}")
        self._mcp = MemHopMCPClient(
            MCP_BIN,
            socket_path=os.path.expanduser("~/.memhop/memhop_bench.sock"),
            env_extra=env_extra, recv_timeout=3600,
        )
        self._mcp.start_reader()
        print("  MCP: ready for all datasets")

    def perceive(self, doc: dict, agent_id: str) -> dict:
        text = doc.get("text", "")
        if not text:
            return {}
        kwargs = {
            "agent_id": agent_id,
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
                kwargs["tree_id"] = tree_name
        result = self._mcp.store(text, **kwargs)
        eid = result.get("engram_id") or result.get("memory_id")
        doc_id = doc.get("id", "")
        if eid and doc_id:
            self._id_map[eid] = doc_id
        return result

    def dream(self, agent_id: str) -> dict:
        return self._mcp.dream(agent_id=agent_id)

    def index(self, docs: list, dream_interval: int = 0, dream_timeout: int = 0,
              agent_id: str = "") -> dict:
        dream_result = None
        last_dream_time = time.time()
        for i, doc in enumerate(docs):
            self.perceive(doc, agent_id)
            elapsed = time.time() - last_dream_time
            if (dream_interval > 0 and (i + 1) % dream_interval == 0) \
               or (dream_timeout > 0 and elapsed >= dream_timeout):
                dream_result = self.dream(agent_id)
                last_dream_time = time.time()
        return dream_result

    def search(self, query: str, agent_id: str, top_k: int = 10, context_id: str = ""):
        kwargs = {"limit": top_k, "agent_id": agent_id}
        if self._dual_encoder:
            query_vector, tree_name = self._dual_encoder.encode(query)
            kwargs["query_vector"] = query_vector
            if tree_name:
                kwargs["tree"] = tree_name
        if context_id:
            kwargs["context_id"] = context_id
        raw = self._mcp.recall(query, **kwargs)
        resp_result = raw
        if self.mode == "associative":
            agg_sessions = resp_result.get("aggregated_sessions", [])
            agg_sessions.sort(key=lambda s: s.get("total_score", 0), reverse=True)
            ranked_ids = [s["session_id"] for s in agg_sessions if s.get("session_id")]
        else:
            results = resp_result.get("results", [])
            ranked_ids = []
            seen = set()
            for item in results:
                eid = item.get("id") or item.get("engram_id")
                if not eid:
                    continue
                doc_id = self._id_map.get(eid, eid)
                if doc_id not in seen:
                    seen.add(doc_id)
                    ranked_ids.append(doc_id)
        return ranked_ids[:top_k], raw

    def clear(self):
        if self._mcp is not None:
            try:
                self._mcp.close()
            except Exception:
                pass
            self._mcp = None


def _build_encoder_info(encoder_name):
    if encoder_name == "bge-m3":
        return EncoderInfo(model_id="BAAI/bge-m3", dim=1024, source="mcp_builtin")
    return EncoderInfo(
        model_id="BAAI/bge-small-zh-v1.5",
        alt_model_id="sentence-transformers/all-MiniLM-L6-v2",
        dim=512, source="python",
        tree_dims={"zh": 512, "en": 384},
    )


def _make_latency_info(latencies_us):
    if not latencies_us:
        return LatencyInfo()
    return LatencyInfo(
        avg_recall_us=float(np.mean(latencies_us)),
        p95_recall_us=float(np.percentile(latencies_us, 95)),
    )


def run_lme_s(runner, docs, queries, turn_qrels, session_qrels, agent_id, mode="retrieval", dream_interval=50, dream_timeout=0):
    runner.index(docs, dream_interval=dream_interval, dream_timeout=dream_timeout, agent_id=agent_id)
    rankings = {}
    latencies = []
    for q in queries:
        t0 = time.time()
        ranked_ids, _ = runner.search(q["text"], agent_id, top_k=10)
        latencies.append((time.time() - t0) * 1e6)
        rankings[q["id"]] = ranked_ids
    # Use mode-appropriate qrels:
    #   retrieval  → turn-level qrels (doc IDs match turn doc_ids)
    #   associative → session-level qrels (aggregated_sessions returns session IDs)
    qrels = session_qrels if mode == "associative" else turn_qrels
    metrics = aggregate_metrics(rankings, qrels)
    return BenchmarkResult(
        timestamp=time.strftime("%Y-%m-%dT%H:%M:%S+08:00"),
        memhop_version=MEMHOP_VERSION,
        encoder=_build_encoder_info(runner.encoder_name),
        dataset=DatasetInfo(name="LongMemEval-S", num_docs=len(docs), num_queries=len(queries)),
        system=SystemInfo(mode=mode, dream=True),
        metrics=metrics, latency=_make_latency_info(latencies),
    )


def run_nfcorpus(runner, subset_size, agent_id, mode="retrieval", dream_interval=50, dream_timeout=0):
    from adapters.beir_adapter import load_beir_dataset
    dataset = load_beir_dataset("nfcorpus", subset_size=subset_size)
    docs = [{"id": did, "text": dataset.corpus[did], "session_id": "nfcorpus"} for did in dataset.doc_ids]
    queries_list = [{"id": qid, "text": dataset.queries[qid]} for qid in dataset.query_ids]
    runner.index(docs, dream_interval=dream_interval, dream_timeout=dream_timeout, agent_id=agent_id)
    rankings = {}
    latencies = []
    for q in queries_list:
        t0 = time.time()
        ranked_ids, _ = runner.search(q["text"], agent_id, top_k=10)
        latencies.append((time.time() - t0) * 1e6)
        rankings[q["id"]] = ranked_ids
    metrics = aggregate_metrics(rankings, dataset.qrels)
    return BenchmarkResult(
        timestamp=time.strftime("%Y-%m-%dT%H:%M:%S+08:00"),
        memhop_version=MEMHOP_VERSION,
        encoder=_build_encoder_info(runner.encoder_name),
        dataset=DatasetInfo(name="BEIR-nfcorpus", num_docs=len(docs), num_queries=len(queries_list)),
        system=SystemInfo(mode=mode, dream=True),
        metrics=metrics, latency=_make_latency_info(latencies),
    )


def run_c_mteb(runner, subset_size, agent_id_base, mode="retrieval", dream_interval=50, dream_timeout=0):
    from adapters.c_mteb_adapter import load_c_mteb_task
    task_names = ["T2Retrieval", "MMarcoRetrieval", "DuRetrieval", "CovidRetrieval",
                  "CmedqaRetrieval", "EcomRetrieval", "MedicalRetrieval", "VideoRetrieval"]
    if subset_size:
        task_names = task_names[:1]
        doc_limit = subset_size
    else:
        doc_limit = None
    results = []
    for task_name in task_names:
        try:
            task = load_c_mteb_task(task_name, subset_size=doc_limit)
        except Exception as e:
            print(f"  \u26a0 Failed to load {task_name}: {e}")
            continue
        sub_agent = f"{agent_id_base}_{task_name}"
        runner._id_map = {}
        try:
            docs = [{"id": did, "text": task.corpus[did], "session_id": task_name} for did in task.doc_ids]
            queries_list = [{"id": qid, "text": task.queries[qid]} for qid in task.query_ids]
            runner.index(docs, dream_interval=dream_interval, dream_timeout=dream_timeout, agent_id=sub_agent)
            rankings = {}
            latencies = []
            for q in queries_list:
                t0 = time.time()
                ranked_ids, _ = runner.search(q["text"], sub_agent, top_k=10)
                latencies.append((time.time() - t0) * 1e6)
                rankings[q["id"]] = ranked_ids
            metrics = aggregate_metrics(rankings, task.qrels)
            result = BenchmarkResult(
                timestamp=time.strftime("%Y-%m-%dT%H:%M:%S+08:00"),
                memhop_version=MEMHOP_VERSION,
                encoder=_build_encoder_info(runner.encoder_name),
                dataset=DatasetInfo(name=f"C-MTEB-{task_name}", num_docs=len(docs), num_queries=len(queries_list)),
                system=SystemInfo(mode=mode, dream=True),
                metrics=metrics, latency=_make_latency_info(latencies),
            )
            results.append(result)
        finally:
            pass
    return results


def run_locomo(runner, subset, agent_id, mode="retrieval", eval_method="llm_judge", dream_interval=50, dream_timeout=0):
    from adapters.locomo_adapter import load_locomo_dataset
    data_dir = os.path.join(SCRIPT_DIR, "data", "locomo")
    docs, queries, answers = load_locomo_dataset(data_dir, subset)
    runner.index(docs, dream_interval=dream_interval, dream_timeout=dream_timeout, agent_id=agent_id)
    recalled_texts_list: list[list[str]] = []
    latencies: list[float] = []
    for q in queries:
        t0 = time.time()
        ranked_ids, raw = runner.search(q["text"], agent_id, top_k=10)
        latencies.append((time.time() - t0) * 1e6)
        texts = []
        resp_result = raw if isinstance(raw, dict) else {}
        for item in resp_result.get("results", []):
            text = item.get("text", "")
            if text:
                texts.append(text)
        recalled_texts_list.append(texts)
    use_llm = eval_method == "llm_judge"
    if use_llm:
        deepseek_key = os.environ.get("DEEPSEEK_API_KEY", "")
        if not deepseek_key:
            print("  \u26a0 DEEPSEEK_API_KEY not set, falling back to F1")
            use_llm = False
    if use_llm:
        from utils.llm_client import DeepSeekJudge
        judge = DeepSeekJudge()
        scores = []
        for qi, (q, ans) in enumerate(zip(queries, answers)):
            context = "\n".join(recalled_texts_list[qi][:5])
            try:
                score = judge.evaluate_answer(q["text"], ans, context)
            except Exception as e:
                print(f"  \u26a0 LLM judge failed for q{qi}: {e}")
                score = 0.0
            scores.append(score)
        arr = np.array(scores)
        metrics = {"accuracy": {"mean": float(arr.mean()), "std": float(arr.std(ddof=1)) if len(arr) > 1 else 0.0}, "num_queries": len(scores)}
    else:
        metrics = aggregate_locomo_f1(recalled_texts_list, answers, k=10)
    return BenchmarkResult(
        timestamp=time.strftime("%Y-%m-%dT%H:%M:%S+08:00"),
        memhop_version=MEMHOP_VERSION,
        encoder=_build_encoder_info(runner.encoder_name),
        dataset=DatasetInfo(name="LoCoMo", num_docs=len(docs), num_queries=len(queries)),
        system=SystemInfo(mode=mode, dream=True),
        metrics=metrics, latency=_make_latency_info(latencies),
    )


def run_dmr(runner, subset, agent_id, mode="retrieval", dream_interval=50, dream_timeout=0):
    from adapters.dmr_adapter import load_dmr_dataset
    cache_dir = os.path.join(SCRIPT_DIR, "data", "dmr")
    docs, queries, answers, conv_meta = load_dmr_dataset(cache_dir, subset, n_questions_per_conv=3)
    if not queries:
        print("  \u26a0 No DMR questions loaded")
        return None
    runner.index(docs, dream_interval=dream_interval, dream_timeout=dream_timeout, agent_id=agent_id)
    from utils.llm_client import DeepSeekJudge
    judge = DeepSeekJudge()
    scores: list[float] = []
    latencies: list[float] = []
    total = len(queries)
    for qi, (q, expected) in enumerate(zip(queries, answers)):
        print(f"  DMR [{qi + 1}/{total}] {q['text'][:60]}...", end=" ", flush=True)
        t0 = time.time()
        ranked_ids, raw = runner.search(q["text"], agent_id, top_k=10)
        latencies.append((time.time() - t0) * 1e6)
        context_lines = []
        resp_result = raw if isinstance(raw, dict) else {}
        for item in resp_result.get("results", []):
            text = item.get("text", "")
            if text:
                context_lines.append(text)
        context = "\n".join(context_lines[:5])
        try:
            score = judge.evaluate_answer(q["text"], expected, context)
        except Exception as e:
            print(f"\u26a0 judge failed: {e}")
            score = 0.0
        scores.append(score)
        print("PASS" if score > 0 else "FAIL")
    arr = np.array(scores)
    accuracy = {"accuracy": {"mean": float(arr.mean()), "std": float(arr.std(ddof=1)) if len(arr) > 1 else 0.0}, "num_queries": len(scores)}
    return BenchmarkResult(
        timestamp=time.strftime("%Y-%m-%dT%H:%M:%S+08:00"),
        memhop_version=MEMHOP_VERSION,
        encoder=_build_encoder_info(runner.encoder_name),
        dataset=DatasetInfo(name="DMR", num_docs=len(docs), num_queries=len(queries)),
        system=SystemInfo(mode=mode, dream=True),
        metrics=accuracy, latency=_make_latency_info(latencies),
    )


def run_context_benchmark(runner):
    """v0.13 context lifecycle benchmark."""
    print("\n  \u2500\u2500 Context Lifecycle Benchmark (v0.13) \u2500\u2500")
    agent_id = "bench_context_test"
    print("  [1/4] Context explosion test (1000 topics)...")
    for i in range(1000):
        runner.perceive({"id": f"ctx_{i}", "text": f"Random topic #{i}", "session_id": f"s{i%10}", "turn_id": f"t{i}", "turn_index": i}, agent_id)
    print("  [2/4] Context filtered recall (\u5931\u61b6\u7387)...")
    for i in range(5):
        runner.perceive({"id": f"a_{i}", "text": f"Context A fact #{i} about Rust", "session_id": "ctx_a", "turn_id": f"a{i}", "turn_index": i}, agent_id)
    for i in range(5):
        runner.perceive({"id": f"b_{i}", "text": f"Context B fact #{i} about Go", "session_id": "ctx_b", "turn_id": f"b{i}", "turn_index": i}, agent_id)
    runner.dream(agent_id)
    ranked_a, raw_a = runner.search("Rust", agent_id, top_k=10)
    print(f"    Recall results: {len(ranked_a)}")
    return {"status": "needs v0.13 MCP for full context metrics"}


def run_worldview_benchmark(runner):
    """v0.13 worldview filtering benchmark."""
    print("\n  \u2500\u2500 Worldview Filter Benchmark (v0.13) \u2500\u2500")
    agent_id = "bench_worldview_test"
    runner.perceive({"id": "safe_1", "text": "Type safety prevents memory bugs", "session_id": "safe", "turn_id": "s1", "turn_index": 0}, agent_id)
    runner.perceive({"id": "unsafe_1", "text": "Unsafe code is needed for performance", "session_id": "unsafe", "turn_id": "u1", "turn_index": 0}, agent_id)
    runner.dream(agent_id)
    print("  Worldview filter: needs v0.13 MCP for use_worldview_filter param")
    return {"status": "needs v0.13 MCP"}


def run_dream_quality(runner, agent_id):
    print("  Dream quality benchmark...")
    facts = ["Alice leads auth refactor", "Bob manages frontend", "Charlie found payment bug"]
    docs = []
    for i in range(30):
        docs.append({"id": f"dream_turn_{i}", "text": f"Update #{i}: {facts[i % 3]}", "session_id": "dream", "turn_id": f"t{i}", "turn_index": i})
    queries = [{"id": f"q_{j}", "text": fact} for j, fact in enumerate(facts)]
    for doc in docs:
        runner.perceive(doc, agent_id)
    pre_rankings = {}
    for q in queries:
        ranked_ids, _ = runner.search(q["text"], agent_id, top_k=10)
        pre_rankings[q["id"]] = ranked_ids
    dream_result = runner.dream(agent_id)
    post_rankings = {}
    for q in queries:
        ranked_ids, _ = runner.search(q["text"], agent_id, top_k=10)
        post_rankings[q["id"]] = ranked_ids
    qrels = {}
    for j in range(3):
        qrels[f"q_{j}"] = {}
        for i in range(30):
            if i % 3 == j:
                qrels[f"q_{j}"][f"dream_turn_{i}"] = 1
    pre_metrics = aggregate_metrics(pre_rankings, qrels)
    post_metrics = aggregate_metrics(post_rankings, qrels)
    print(f"    Dream: {dream_result.get('consolidated_count', '?')}")
    print(f"    Pre/post R@5: {pre_metrics['recall_5']['mean']:.3f} / {post_metrics['recall_5']['mean']:.3f}")
    return {"pre_dream": pre_metrics, "post_dream": post_metrics, "dream_report": dream_result}


def run_latency_benchmark(mcp_bin, scales=(1000, 5000, 10000), queries_per_scale=50):
    from performance.run_latency import run_latency_via_mcp
    return run_latency_via_mcp(mcp_bin, list(scales), queries_per_scale)


def _load_competitor_data():
    if not os.path.exists(COMPETITOR_DATA):
        return None
    try:
        with open(COMPETITOR_DATA) as f:
            return json.load(f)
    except Exception as e:
        print(f"  \u26a0 Failed to load competitor data: {e}")
        return None


def _get_metric_val(report, *keys):
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
    paths = []
    for p in patterns:
        matched = glob.glob(p)
        if matched:
            paths.extend(matched)
        elif os.path.isfile(p):
            paths.append(p)
    if not paths:
        print("No report files found.")
        return
    reports = []
    for path in paths:
        try:
            data = json.load(open(path))
            if isinstance(data, dict):
                reports.append(data)
        except Exception as e:
            print(f"  \u26a0 Skipping {path}: {e}")
    if not reports:
        print("No valid report files.")
        return
    competitors = _load_competitor_data()
    groups = {}
    for r in reports:
        ds_val = r.get("dataset", "unknown")
        ds = ds_val.get("name", "unknown") if isinstance(ds_val, dict) else str(ds_val)
        groups.setdefault(ds, []).append(r)
    try:
        from tabulate import tabulate
        use_tabulate = True
    except ImportError:
        use_tabulate = False
    for ds, entries in sorted(groups.items()):
        print(f"\n{'='*60}\n  Dataset: {ds}\n{'='*60}")
        headers = ["Run", "NDCG@10", "MRR", "R@1", "R@5"]
        rows = []
        for i, r in enumerate(entries):
            m = r.get("metrics", r)
            rows.append([f"MemHop #{i+1}",
                         f"{_get_metric_val(m, 'ndcg_10', 'ndcg@10'):.4f}",
                         f"{_get_metric_val(m, 'mrr'):.4f}",
                         f"{_get_metric_val(m, 'recall_1', 'r@1'):.4f}",
                         f"{_get_metric_val(m, 'recall_5', 'r@5'):.4f}"])
        if use_tabulate:
            print(tabulate(rows, headers=headers, tablefmt="pipe"))
        if competitors:
            for comp_ds_key, comp_ds in competitors.get("datasets", {}).items():
                if comp_ds_key.lower() in ds.lower() or ds.lower() in comp_ds_key.lower():
                    systems = comp_ds.get("systems", {})
                    metric_name = comp_ds.get("metric", "Accuracy")
                    print(f"\n  Competitors ({comp_ds_key}):")
                    comp_rows = []
                    for sys_id, sys_data in systems.items():
                        val = sys_data.get(metric_name, "N/A")
                        if isinstance(val, (int, float)):
                            val = f"{val:.1%}" if val < 1 else f"{val:.4f}"
                        comp_rows.append([sys_data.get("display_name", sys_id), val, sys_data.get("source", "")])
                    if use_tabulate:
                        print(tabulate(comp_rows, headers=["System", metric_name, "Source"], tablefmt="pipe"))
                    break


def _dataset_modes(ds_name: str) -> list[str]:
    """Per-dataset mode selection. Document-level datasets skip associative (returns zeros)."""
    doc_datasets = {"nfcorpus", "c-mteb"}
    if ds_name in doc_datasets:
        return ["retrieval"]
    return ["retrieval", "associative"]


def main():
    _env_path = os.path.join(os.path.dirname(os.path.dirname(__file__)), ".env")
    if os.path.exists(_env_path):
        with open(_env_path) as _f:
            for _line in _f:
                _line = _line.strip()
                if _line and not _line.startswith("#") and "=" in _line:
                    _k, _v = _line.split("=", 1)
                    os.environ.setdefault(_k.strip(), _v.strip())

    parser = argparse.ArgumentParser(description="MemHop v0.13 Unified Benchmark")
    parser.add_argument("--encoder", choices=["bge-m3", "dual-small"], default="bge-m3")
    parser.add_argument("--all", action="store_true", default=False)
    parser.add_argument("--datasets", default="lme-s,nfcorpus")
    parser.add_argument("--modes", default=None,
                        help="DEPRECATED: modes auto-selected per dataset")
    parser.add_argument("--subset", type=int, default=None)
    parser.add_argument("--dream-interval", type=int, default=50)
    parser.add_argument("--dream-timeout", type=int, default=0,
                        help="Trigger Dream every N seconds (0 = disabled)")
    parser.add_argument("--eval-method", choices=["f1", "llm_judge"], default="llm_judge")
    parser.add_argument("--latency", action="store_true", default=False)
    parser.add_argument("--latency-scales", default="1000,5000,10000")
    parser.add_argument("--dream-quality", action="store_true", default=False)
    parser.add_argument("--context-benchmark", action="store_true", default=False)
    parser.add_argument("--worldview-benchmark", action="store_true", default=False)
    parser.add_argument("--compare", nargs="+", default=None)

    args = parser.parse_args()

    if args.compare:
        compare_reports(args.compare)
        return

    if args.all:
        datasets = list(ALL_DATASETS)
    else:
        datasets = [d.strip() for d in args.datasets.split(",")]

    os.makedirs(REPORT_DIR, exist_ok=True)
    all_reports = []

    runner = MemHopMCPRunner(encoder=args.encoder)
    runner.ensure_mcp()

    # Unique run ID for data isolation between runs
    run_id = time.strftime("%Y%m%d_%H%M%S")

    for ds_name in datasets:
        agent_id = f"bench_{run_id}_{ds_name}"
        runner._id_map = {}
        modes = _dataset_modes(ds_name)
        for mode in modes:
            runner.mode = mode
            print(f"\n{'─'*50}\n  {ds_name} / {mode} ({args.encoder})\n{'─'*50}")
            try:
                if ds_name == "lme-s":
                    from adapters.lme_adapter import load_lme_dataset
                    if not os.path.exists(LME_DATA):
                        print(f"  \u274c LME data not found at {LME_DATA}")
                        continue
                    results = run_lme_s(runner, *load_lme_dataset(LME_DATA, args.subset), agent_id, mode, args.dream_interval, args.dream_timeout)
                elif ds_name == "nfcorpus":
                    results = run_nfcorpus(runner, args.subset, agent_id, mode, args.dream_interval, args.dream_timeout)
                elif ds_name == "c-mteb":
                    results = run_c_mteb(runner, args.subset, agent_id, mode, args.dream_interval, args.dream_timeout)
                elif ds_name == "locomo":
                    results = run_locomo(runner, args.subset, agent_id, mode, args.eval_method, args.dream_interval, args.dream_timeout)
                elif ds_name == "dmr":
                    results = run_dmr(runner, args.subset, agent_id, mode, args.dream_interval, args.dream_timeout)
                    if results is None:
                        continue
                else:
                    print(f"  \u274c Unknown dataset: {ds_name}")
                    continue
                if not isinstance(results, list):
                    results = [results]
                for r in results:
                    rpt_path = os.path.join(REPORT_DIR, f"{args.encoder}_{r.dataset.name}_{mode}_{time.strftime('%Y%m%d_%H%M%S')}.json")
                    r.to_json(rpt_path)
                    print(f"  \u2705 Saved: {rpt_path}")
                    all_reports.append(r)
            except Exception as e:
                print(f"  \u274c {ds_name}/{mode} failed: {e}")
                import traceback
                traceback.print_exc()

    if args.context_benchmark:
        r = run_context_benchmark(runner)
        json.dump(r, open(os.path.join(REPORT_DIR, f"context_benchmark_{time.strftime('%Y%m%d_%H%M%S')}.json"), "w"), indent=2)

    if args.worldview_benchmark:
        r = run_worldview_benchmark(runner)
        json.dump(r, open(os.path.join(REPORT_DIR, f"worldview_benchmark_{time.strftime('%Y%m%d_%H%M%S')}.json"), "w"), indent=2)

    if args.dream_quality:
        r = run_dream_quality(runner, "bench_dream_quality")
        json.dump(r, open(os.path.join(REPORT_DIR, f"dream_quality_{time.strftime('%Y%m%d_%H%M%S')}.json"), "w"), indent=2)

    if args.latency:
        scales = [int(s) for s in args.latency_scales.split(",")]
        lr = run_latency_benchmark(MCP_BIN, scales)
        json.dump({"results": lr}, open(os.path.join(REPORT_DIR, f"latency_{time.strftime('%Y%m%d_%H%M%S')}.json"), "w"), indent=2)
        print(f"\n  {'Scale':>8}  {'Store P95':>12}  {'Recall P95':>12}")
        for r in lr:
            print(f"  {r['scale']:>6}  {r['store_p95_us']:>10.0f}\u00b5s  {r['recall_p95_us']:>10.0f}\u00b5s")

    runner.clear()

    print(f"\n{'='*60}\n  MemHop v{MEMHOP_VERSION} \u2014 {args.encoder}\n  {len(all_reports)} reports generated\n{'='*60}")


if __name__ == "__main__":
    main()
