#!/usr/bin/env python3
"""MemHop v0.11.0 — Unified Benchmark (5 dimensions, MCP-based)

Usage:
  python3 benchmarks/run_all.py [N_lme_problems]

五维度:
  1. 记忆力  — LongMemEval-S (via MCP memhop_store/memhop_recall)
  2. 知识检索 — nfcorpus + SciFact
  3. 代码检索 — CodeSearchNet (scaffold)
  4. 延迟    — 1K/10K/100K/1M  P50/P99 (MCP round-trip)
  5. Dream 效果 — Dream 前后 R@5 对比

Encoding: ONNX models loaded server-side via MEMHOP_ONNX_MODEL env var.
Result: benchmarks/reports/summary_*.json
"""
import gc, json, os, shutil, math, time, sys, random
import numpy as np

from benchmarks.mcp_client import MemHopMCPClient
from benchmarks.quality.metrics import ndcg_at_k, mrr, recall_at_k

os.environ["TOKENIZERS_PARALLELISM"] = "false"
os.environ["HF_HUB_OFFLINE"] = "1"

# ═══════════════════════════════════════════════════
#  Paths
# ═══════════════════════════════════════════════════
SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
PROJECT_DIR = os.path.join(SCRIPT_DIR, "..")
MCP_BIN = os.path.join(PROJECT_DIR, "target/release/memhop-mcp-server")
REPORT_DIR = os.path.join(SCRIPT_DIR, "reports")
BASE_DATA = os.path.join(SCRIPT_DIR, "data")
TEMP = "/tmp/memhop_full_bench"
MEMHOP_VERSION = "0.11.0"

# Dataset paths
LME_DATA = "/Volumes/zt_hd/projects/meow/LongMemEval/data/longmemeval_s_cleaned.json"
NF_DATA = os.path.join(BASE_DATA, "beir/nfcorpus/nfcorpus")
SCIFACT_DIR = os.path.join(BASE_DATA, "scifact")
CSN_DIR = os.path.join(BASE_DATA, "codesearchnet")

# ── CLI args ──
N_LME = int(sys.argv[1]) if len(sys.argv) > 1 else 100

# ── ONNX Encoder models ──
MODELS = [
    {"id": "bge-m3", "name": "BGE-M3",
     "onnx_path": os.path.join(PROJECT_DIR, "models/bge-m3")},
    {"id": "all-minilm", "name": "all-MiniLM",
     "onnx_path": os.path.join(PROJECT_DIR, "models/all-minilm")},
]
NF_DOCS, NF_QUERIES, TEXT_LIMIT = 500, 50, 256
DREAM_TOPICS = 8
DREAM_TURNS_PER_TOPIC = 3


# ═══════════════════════════════════════════════════
#  MCP Benchmark Helpers
# ═══════════════════════════════════════════════════
def run_with_mcp(model, docs, queries, qrels):
    """Run IR benchmark through MCP server.

    Stores documents via memhop_store, queries via memhop_recall,
    and computes NDCG/MRR/Recall metrics in Python.

    Returns dict with mean/std for each metric.
    Requires: MEMHOP_ONNX_MODEL env var in model dict.
    """
    db_dir = os.path.join(TEMP, f"mcp_{model['id']}_{os.urandom(4).hex()}")

    if os.path.exists(db_dir):
        shutil.rmtree(db_dir)

    mcp = MemHopMCPClient(
        MCP_BIN, db_dir,
        env_extra={"MEMHOP_ONNX_MODEL": model["onnx_path"]}
    )
    mcp.start_reader()

    try:
        # ── Store documents ──
        id_map = {}  # {engram_id: doc_id}
        for doc in docs:
            result = mcp.store(
                doc["text"],
                session_id=doc.get("session_id", "bench"),
                turn_id=doc.get("turn_id", ""),
                turn_index=doc.get("turn_index", 0),
                topic_label=doc.get("topic_label"),
            )
            id_map[result["memory_id"]] = doc["id"]

        # ── Run queries ──
        ndcg_scores, mrr_scores = [], []
        r1_scores, r5_scores, r10_scores = [], [], []
        latencies = []

        for q in queries:
            rel = qrels.get(q["id"], {})
            t0 = time.time()
            result = mcp.recall(q["text"], limit=10)
            latencies.append((time.time() - t0) * 1e6)

            # Map engram_id back to original doc_id
            ranked = []
            for item in result.get("results", []):
                doc_id = id_map.get(item["id"])
                if doc_id and doc_id not in ranked:
                    ranked.append(doc_id)

            ndcg_scores.append(ndcg_at_k(ranked, rel, k=10))
            mrr_scores.append(mrr(ranked, rel))
            r1_scores.append(recall_at_k(ranked, rel, k=1))
            r5_scores.append(recall_at_k(ranked, rel, k=5))
            r10_scores.append(recall_at_k(ranked, rel, k=10))

        return {
            "ndcg_10": {"mean": round(float(np.mean(ndcg_scores)), 4),
                        "std": round(float(np.std(ndcg_scores, ddof=1)), 4)},
            "mrr": {"mean": round(float(np.mean(mrr_scores)), 4),
                    "std": round(float(np.std(mrr_scores, ddof=1)), 4)},
            "recall_1": {"mean": round(float(np.mean(r1_scores)), 4),
                         "std": round(float(np.std(r1_scores, ddof=1)), 4)},
            "recall_5": {"mean": round(float(np.mean(r5_scores)), 4),
                         "std": round(float(np.std(r5_scores, ddof=1)), 4)},
            "recall_10": {"mean": round(float(np.mean(r10_scores)), 4),
                          "std": round(float(np.std(r10_scores, ddof=1)), 4)},
            "avg_recall_latency_us": round(float(np.mean(latencies))),
        }
    finally:
        mcp.close()
        shutil.rmtree(db_dir, ignore_errors=True)


# ═══════════════════════════════════════════════════
#  Dimension 1: 记忆力 — LongMemEval-S
# ═══════════════════════════════════════════════════
def run_longmemeval():
    """LongMemEval-S associative memory benchmark via MCP."""
    if not os.path.exists(LME_DATA):
        print("  ⚠ LongMemEval-S data not found, skipping memory dimension")
        return None

    with open(LME_DATA) as f:
        lme_data = json.load(f)
    problems = lme_data[:N_LME]

    results = []
    for mi, m in enumerate(MODELS):
        print(f"  [{mi+1}/{len(MODELS)}] {m['name']} (LongMemEval-S, {N_LME}题)...",
              end=" ", flush=True)
        t0 = time.time()

        docs, queries, qrels = [], [], {}
        for p in problems:
            for si, turns in enumerate(p["haystack_sessions"]):
                sid = p["haystack_session_ids"][si]
                text = " ".join(
                    t["content"] for t in turns if isinstance(t, dict)
                )
                docs.append({"id": sid, "text": text[:800]})
            queries.append({"id": p["question_id"], "text": p["question"]})
            qrels[p["question_id"]] = {sid: 1 for sid in p["answer_session_ids"]}

        out = run_with_mcp(m, docs, queries, qrels)

        rpt = {
            "model": m["name"],
            "problems": N_LME, "sessions": len(docs),
            "ndcg@10": out["ndcg_10"]["mean"],
            "mrr": out["mrr"]["mean"],
            "r@1": out["recall_1"]["mean"],
            "r@5": out["recall_5"]["mean"],
            "r@10": out["recall_10"]["mean"],
            "失忆率": round(1 - out["recall_1"]["mean"], 4),
            "幻听率": round(1 - out["recall_5"]["mean"], 4),
            "latency_us": out["avg_recall_latency_us"],
            "elapsed_s": round(time.time() - t0, 1),
        }
        results.append(rpt)
        print(f"R@5={rpt['r@5']:.4f} 失忆={rpt['失忆率']:.1%} {rpt['elapsed_s']:.0f}s")

    return results


# ═══════════════════════════════════════════════════
#  Dimension 2: 知识检索 — nfcorpus + SciFact
# ═══════════════════════════════════════════════════

# ── nfcorpus data loading ──
def load_nfcorpus():
    corpus, queries, qrels_raw = {}, {}, {}
    for line in open(os.path.join(NF_DATA, "corpus.jsonl")):
        item = json.loads(line)
        corpus[item["_id"]] = item["text"][:TEXT_LIMIT]
    for line in open(os.path.join(NF_DATA, "queries.jsonl")):
        item = json.loads(line)
        queries[item["_id"]] = item["text"]
    for line in open(os.path.join(NF_DATA, "qrels/test.tsv")):
        parts = line.strip().split("\t")
        if len(parts) >= 3 and parts[0] != "query-id":
            qrels_raw.setdefault(parts[0], set()).add(parts[1])

    random.seed(42)
    all_docs = sorted(corpus.keys())
    all_queries = [q for q in sorted(queries.keys()) if q in qrels_raw]
    doc_ids = random.sample(all_docs, min(NF_DOCS, len(all_docs)))
    query_ids = random.sample(all_queries, min(NF_QUERIES * 3, len(all_queries)))

    qrels_f, valid_q = {}, []
    for qid in query_ids:
        rel = qrels_raw.get(qid, set()) & set(doc_ids)
        if rel:
            qrels_f[qid] = {did: 1 for did in rel}
            valid_q.append(qid)

    return doc_ids, valid_q[:NF_QUERIES], corpus, queries, qrels_f


def run_nfcorpus(doc_ids, query_ids, corpus, queries, qrels):
    """nfcorpus retrieval benchmark via MCP."""
    all_results = []
    for mi, m in enumerate(MODELS):
        print(f"\n  [{mi+1}/{len(MODELS)}] {m['name']} (nfcorpus)...",
              end=" ", flush=True)
        t0 = time.time()

        docs = [{"id": did, "text": corpus[did]} for did in doc_ids]
        queries_in = [{"id": qid, "text": queries[qid]} for qid in query_ids]

        out = run_with_mcp(m, docs, queries_in, qrels)

        rpt = {
            "model": m["name"],
            "ndcg@10": out["ndcg_10"]["mean"],
            "mrr": out["mrr"]["mean"],
            "r@1": out["recall_1"]["mean"],
            "r@5": out["recall_5"]["mean"],
            "r@10": out["recall_10"]["mean"],
            "失忆率": round(1 - out["recall_1"]["mean"], 4),
            "幻听率": round(1 - out["recall_5"]["mean"], 4),
            "latency_us": out["avg_recall_latency_us"],
            "elapsed_s": round(time.time() - t0, 1),
        }
        all_results.append(rpt)
        print(
            f"NDCG={rpt['ndcg@10']:.4f} "
            f"失忆={rpt['失忆率']:.1%} "
            f"延迟={rpt['latency_us']}µs {rpt['elapsed_s']:.0f}s"
        )
    return all_results


# ── SciFact ──
def run_scifact():
    """SciFact knowledge retrieval via MCP (BEIR format)."""
    data_dir = SCIFACT_DIR
    if not os.path.exists(os.path.join(data_dir, "corpus.jsonl")):
        print("  ⚠ SciFact data not found, skipping")
        return None

    corpus, queries, qrels_raw = {}, {}, {}
    for line in open(os.path.join(data_dir, "corpus.jsonl")):
        item = json.loads(line)
        corpus[item["_id"]] = item["text"][:TEXT_LIMIT]
    for line in open(os.path.join(data_dir, "queries.jsonl")):
        item = json.loads(line)
        queries[item["_id"]] = item["text"]
    for line in open(os.path.join(data_dir, "qrels/test.tsv")):
        parts = line.strip().split("\t")
        if len(parts) >= 3 and parts[0] != "query-id":
            qrels_raw.setdefault(parts[0], set()).add(parts[1])

    random.seed(42)
    all_docs = sorted(corpus.keys())
    all_queries = [q for q in sorted(queries.keys()) if q in qrels_raw]
    doc_ids = random.sample(all_docs, min(NF_DOCS, len(all_docs)))
    query_ids = random.sample(all_queries, min(NF_QUERIES * 3, len(all_queries)))

    qrels_f, valid_q = {}, []
    for qid in query_ids:
        rel = qrels_raw.get(qid, set()) & set(doc_ids)
        if rel:
            qrels_f[qid] = {did: 1 for did in rel}
            valid_q.append(qid)

    valid_queries = valid_q[:NF_QUERIES]
    if not valid_queries:
        print("  ⚠ SciFact: no valid query-doc pairs after sampling, skipping")
        return None

    m = MODELS[0]
    print(f"\n  [SciFact] {m['name']} ({len(doc_ids)} docs, {len(valid_queries)} queries)...",
          end=" ", flush=True)

    docs = [{"id": did, "text": corpus[did]} for did in doc_ids]
    queries_in = [{"id": qid, "text": queries[qid]} for qid in valid_queries]

    out = run_with_mcp(m, docs, queries_in, qrels_f)

    rpt = {
        "model": m["name"],
        "ndcg@10": out["ndcg_10"]["mean"],
        "mrr": out["mrr"]["mean"],
        "r@1": out["recall_1"]["mean"],
        "r@5": out["recall_5"]["mean"],
        "r@10": out["recall_10"]["mean"],
        "latency_us": out["avg_recall_latency_us"],
    }
    print(f"NDCG={rpt['ndcg@10']:.4f}")
    return rpt


# ═══════════════════════════════════════════════════
#  Dimension 3: 代码检索 — CodeSearchNet (scaffold)
# ═══════════════════════════════════════════════════
def run_codesearchnet():
    """CodeSearchNet code retrieval — scaffold only, skips if data missing."""
    if not os.path.exists(CSN_DIR):
        print("  ⚠ CodeSearchNet data not found, skipping code dimension")
        return None
    print("  ℹ CodeSearchNet: scaffold only, skipping actual benchmark")
    return None


# ═══════════════════════════════════════════════════
#  Dimension 4: 延迟 — Latency (1K/10K/100K/1M)
# ═══════════════════════════════════════════════════
SUBJECTS = [
    "Alice", "Bob", "Charlie", "Dana", "Eli", "Fiona", "Greg", "Hana",
    "Ivan", "Jules", "Kira", "Liam", "Maya", "Noah",
]
VERBS = [
    "refactored", "deployed", "debugged", "profiled", "reviewed",
    "merged", "shipped", "reverted", "audited", "hardened",
]
MODULES = [
    "auth handler", "payment gateway", "search index", "rate limiter",
    "telemetry agent", "schema registry", "event bus", "checkout flow",
]


def make_text(i: int) -> str:
    s = SUBJECTS[i % len(SUBJECTS)]
    v = VERBS[(i // 3) % len(VERBS)]
    m = MODULES[(i // 7) % len(MODULES)]
    return f"Memo #{i}: {s} {v} the {m}. build-{i % 97}-r{i % 31}"


def make_query(i: int) -> str:
    s = SUBJECTS[(i * 7 + 3) % len(SUBJECTS)]
    m = MODULES[(i * 3 + 2) % len(MODULES)]
    return f"what did {s} change in the {m}"


def run_latency():
    """Latency benchmark via MCP round-trip timing."""
    print(f"\n  [延迟] 1K/10K/100K/1M (BGE-M3)...", end=" ", flush=True)

    if not os.path.exists(MCP_BIN):
        print("MCP server binary not found, skipping")
        return None

    model = MODELS[0]  # BGE-M3 for latency
    scales = [1000, 10000, 100000, 1000000]
    scale_labels = {1000: "1k", 10000: "10k", 100000: "100k", 1000000: "1M"}
    lat_data = {}
    num_queries = 50

    for scale in scales:
        db_dir = os.path.join(TEMP, f"lat_{scale}_{os.urandom(4).hex()}")
        if os.path.exists(db_dir):
            shutil.rmtree(db_dir)

        mcp = MemHopMCPClient(
            MCP_BIN, db_dir,
            env_extra={"MEMHOP_ONNX_MODEL": model["onnx_path"]}
        )
        mcp.start_reader()

        try:
            # ── Store benchmark ──
            store_lats = []
            for i in range(scale):
                text = make_text(i)
                t0 = time.time()
                mcp.store(text)
                store_lats.append((time.time() - t0) * 1e6)
            store_lats.sort()

            # ── Recall benchmark ──
            recall_lats = []
            for i in range(num_queries):
                q = make_query(i)
                t0 = time.time()
                mcp.recall(q, limit=10)
                recall_lats.append((time.time() - t0) * 1e6)
            recall_lats.sort()

            label = scale_labels[scale]

            def pct(sorted_list, p):
                if not sorted_list:
                    return 0
                idx = int(len(sorted_list) * p / 100)
                return sorted_list[min(idx, len(sorted_list) - 1)]

            lat_data[f"p50_{label}"] = round(pct(recall_lats, 50) / 1000, 2)
            lat_data[f"p99_{label}"] = round(pct(recall_lats, 99) / 1000, 2)
            lat_data[f"store_p50_{label}"] = round(pct(store_lats, 50) / 1000, 2)
            lat_data[f"store_p99_{label}"] = round(pct(store_lats, 99) / 1000, 2)

            # Disk usage
            try:
                du = shutil.disk_usage(db_dir).used // (1024 * 1024)
            except Exception:
                du = 0
            lat_data[f"disk_mb_{label}"] = du

            print(f"  {label}: recall P50={lat_data[f'p50_{label}']}ms "
                  f"P99={lat_data[f'p99_{label}']}ms", flush=True)

        finally:
            mcp.close()
            shutil.rmtree(db_dir, ignore_errors=True)

    print("完成")
    return lat_data


# ═══════════════════════════════════════════════════
#  Dimension 5: Dream 效果 — Dream 前后 R@5 对比
# ═══════════════════════════════════════════════════
def _generate_dream_data():
    """Generate synthetic multi-turn conversation data for Dream benchmark."""
    conversations = {
        "auth": {
            "turns": [
                "Alice refactored the auth handler. Added OAuth2 support for the API gateway.",
                "Alice fixed a critical bug in the JWT token refresh logic that caused session timeouts.",
                "Alice documented the new auth middleware configuration in the developer guide.",
            ],
            "query": "What changes did Alice make to the authentication system?",
        },
        "payment": {
            "turns": [
                "Bob deployed the payment gateway update. Integrated the new Stripe API v3 with webhook support.",
                "Bob debugged a payment timeout issue in the checkout flow. Processing times were exceeding 30 seconds.",
                "Bob profiled the payment processing pipeline and optimized the database queries for faster settlement.",
            ],
            "query": "Tell me about recent payment system changes made by Bob.",
        },
        "search": {
            "turns": [
                "Charlie upgraded the search index to support fuzzy matching across all document fields.",
                "Charlie added autocomplete suggestions to the search bar, reducing query entry time by 40%.",
                "Charlie optimized the search ranking algorithm to boost recently accessed documents.",
            ],
            "query": "What improvements were made to the search functionality?",
        },
        "cache": {
            "turns": [
                "Dana implemented a Redis-based caching layer for the session store to reduce database load.",
                "Dana fixed cache invalidation bugs where stale data was served after updates.",
                "Dana added cache warming on application startup to prevent cold-start latency spikes.",
            ],
            "query": "What cache-related changes did Dana implement?",
        },
        "telemetry": {
            "turns": [
                "Eli added distributed tracing spans to all API endpoints for better observability.",
                "Eli set up Prometheus metrics dashboards monitoring memory usage and request latency.",
                "Eli configured structured logging with correlation IDs across all microservices.",
            ],
            "query": "What telemetry and monitoring improvements were added?",
        },
        "deploy": {
            "turns": [
                "Fiona automated the CI/CD pipeline with GitHub Actions. Deployments now take 5 minutes.",
                "Fiona added blue-green deployment support to eliminate downtime during releases.",
                "Fiona set up automatic rollback triggers that activate when error rates exceed 1%.",
            ],
            "query": "What deployment pipeline changes did Fiona make?",
        },
        "database": {
            "turns": [
                "Greg migrated the primary database from PostgreSQL 13 to 16, gaining 25% query performance.",
                "Greg added database connection pooling to handle 10x concurrent user load.",
                "Greg implemented read replicas for the reporting queries to offload the primary instance.",
            ],
            "query": "What database infrastructure changes were made?",
        },
        "frontend": {
            "turns": [
                "Hana redesigned the dashboard UI using React Server Components for faster page loads.",
                "Hana added dark mode support across all application pages with system preference detection.",
                "Hana optimized the bundle size by implementing code splitting and lazy loading.",
            ],
            "query": "What frontend improvements did Hana ship?",
        },
    }

    docs = []
    queries = []
    qrels = {}
    for topic_name, topic_data in conversations.items():
        sid = f"dream_{topic_name}"
        for ti, turn_text in enumerate(topic_data["turns"]):
            docs.append({
                "id": f"{sid}_t{ti}",
                "text": turn_text,
                "session_id": sid,
                "turn_id": f"{sid}_t{ti}",
                "turn_index": ti,
                "topic_label": topic_name,
            })
        qid = f"q_{topic_name}"
        queries.append({"id": qid, "text": topic_data["query"]})
        qrels[qid] = {d["id"]: 1 for d in docs if d["session_id"] == sid}

    return docs, queries, qrels


def run_dream():
    """Dream effect benchmark: compare R@5 before and after Dream consolidation."""
    model = MODELS[0]  # BGE-M3
    docs, queries, qrels = _generate_dream_data()

    # ── Pre-Dream ──
    print(f"\n  [Dream] Pre-Dream baseline...", end=" ", flush=True)
    pre_out = run_with_mcp(model, docs, queries, qrels)
    pre_r5 = pre_out["recall_5"]["mean"]
    print(f"R@5={pre_r5:.4f}")

    # ── Post-Dream ──
    print(f"  [Dream] Post-Dream (with consolidation)...", end=" ", flush=True)
    db_dir = os.path.join(TEMP, f"dream_post_{os.urandom(4).hex()}")
    if os.path.exists(db_dir):
        shutil.rmtree(db_dir)

    mcp = MemHopMCPClient(
        MCP_BIN, db_dir,
        env_extra={"MEMHOP_ONNX_MODEL": model["onnx_path"]}
    )
    mcp.start_reader()

    try:
        # Store with turn tracking
        id_map = {}
        for doc in docs:
            result = mcp.store(
                doc["text"],
                session_id=doc.get("session_id", "bench"),
                turn_id=doc.get("turn_id", ""),
                turn_index=doc.get("turn_index", 0),
                topic_label=doc.get("topic_label"),
            )
            id_map[result["memory_id"]] = doc["id"]

        # Run Dream consolidation
        dream_result = mcp.dream()
        print(f"consolidated={dream_result.get('consolidated_count', 0)} "
              f"pruned={dream_result.get('pruned_edges', 0)} "
              f"duration={dream_result.get('duration_ms', 0)}ms",
              end=" ", flush=True)

        # Run queries
        r5_scores = []
        for q in queries:
            rel = qrels.get(q["id"], {})
            result = mcp.recall(q["text"], limit=10)
            ranked = []
            for item in result.get("results", []):
                doc_id = id_map.get(item["id"])
                if doc_id and doc_id not in ranked:
                    ranked.append(doc_id)
            r5_scores.append(recall_at_k(ranked, rel, k=5))

        post_r5 = float(np.mean(r5_scores))
        print(f"R@5={post_r5:.4f}")

    finally:
        mcp.close()
        shutil.rmtree(db_dir, ignore_errors=True)

    improvement = (
        round((post_r5 - pre_r5) / pre_r5 * 100, 1) if pre_r5 > 0 else 0.0
    )
    print(f"  [Dream] ΔR@5={post_r5 - pre_r5:+.4f} ({improvement:+.1f}%)")

    return {
        "pre_dream_r5": round(pre_r5, 4),
        "post_dream_r5": round(post_r5, 4),
        "improvement_pct": improvement,
    }


# ═══════════════════════════════════════════════════
#  Main
# ═══════════════════════════════════════════════════
def main():
    print("╔══════════════════════════════════════════════════════════════╗")
    print("║   MemHop v0.11.0 — Unified Benchmark (MCP-based)            ║")
    print("║   记忆力 | 知识检索 | 代码检索 | 延迟 | Dream 效果            ║")
    print("╚══════════════════════════════════════════════════════════════╝")

    os.makedirs(REPORT_DIR, exist_ok=True)
    os.makedirs(TEMP, exist_ok=True)

    report = {
        "version": MEMHOP_VERSION,
        "timestamp": time.strftime("%Y%m%d_%H%M%S"),
        "memory": None,
        "knowledge": None,
        "code": None,
        "latency": None,
        "dream": None,
    }

    # ── Dimension 1: Memory (LongMemEval-S) ──
    print("\n═══ Dimension 1/5: 记忆力 — LongMemEval-S ═══")
    try:
        lme_results = run_longmemeval()
        if lme_results:
            best_r5 = max(r["r@5"] for r in lme_results)
            report["memory"] = {
                "longmemeval_s_r5": round(best_r5, 4),
                "per_model": lme_results,
            }
    except Exception as e:
        print(f"  ⚠ Memory dimension failed: {e}")
        import traceback
        traceback.print_exc()
        report["memory"] = None

    # ── Dimension 2: Knowledge (nfcorpus + SciFact) ──
    print("\n═══ Dimension 2/5: 知识检索 — nfcorpus + SciFact ═══")
    knowledge = {}
    try:
        if os.path.exists(os.path.join(NF_DATA, "corpus.jsonl")):
            print("  nfcorpus:", end="")
            doc_ids, query_ids, corpus, queries, qrels = load_nfcorpus()
            print(f" {len(doc_ids)} docs x {len(query_ids)} queries")
            nf_results = run_nfcorpus(doc_ids, query_ids, corpus, queries, qrels)
            if nf_results:
                best_ndcg = max(r["ndcg@10"] for r in nf_results)
                knowledge["nfcorpus_ndcg10"] = round(best_ndcg, 4)
                knowledge["nfcorpus_details"] = nf_results
        else:
            print("  ⚠ nfcorpus data not found, skipping")
    except Exception as e:
        print(f"  ⚠ nfcorpus failed: {e}")
        import traceback
        traceback.print_exc()

    try:
        print("  SciFact:", end="")
        scifact_result = run_scifact()
        if scifact_result:
            knowledge["scifact_ndcg10"] = scifact_result["ndcg@10"]
            knowledge["scifact_details"] = scifact_result
        else:
            knowledge["scifact_ndcg10"] = None
    except Exception as e:
        print(f"  ⚠ SciFact failed: {e}")
        import traceback
        traceback.print_exc()
        knowledge["scifact_ndcg10"] = None

    if knowledge:
        report["knowledge"] = knowledge

    # ── Dimension 3: Code (CodeSearchNet) ──
    print("\n═══ Dimension 3/5: 代码检索 — CodeSearchNet ═══")
    try:
        code_result = run_codesearchnet()
    except Exception as e:
        print(f"  ⚠ Code dimension failed: {e}")
        code_result = None
    if code_result:
        report["code"] = code_result

    # ── Dimension 4: Latency ──
    print("\n═══ Dimension 4/5: 延迟 — 1K/10K/100K/1M ═══")
    try:
        lat_data = run_latency()
        if lat_data:
            report["latency"] = lat_data
    except Exception as e:
        print(f"  ⚠ Latency dimension failed: {e}")
        import traceback
        traceback.print_exc()

    # ── Dimension 5: Dream Effect ──
    print("\n═══ Dimension 5/5: Dream 效果 — Dream 前后 R@5 对比 ═══")
    try:
        dream_result = run_dream()
        if dream_result:
            report["dream"] = dream_result
    except Exception as e:
        print(f"  ⚠ Dream dimension failed: {e}")
        import traceback
        traceback.print_exc()

    # ── Save unified report ──
    ts = time.strftime("%Y%m%d_%H%M%S")
    report_path = os.path.join(REPORT_DIR, f"summary_{ts}.json")
    with open(report_path, "w") as f:
        json.dump(report, f, indent=2, ensure_ascii=False)

    # ── Print summary ──
    active_dims = sum(
        1 for k in ["memory", "knowledge", "code", "latency", "dream"]
        if report.get(k) is not None
    )
    print(f"\n{'=' * 80}")
    print(f"  MemHop v{MEMHOP_VERSION} Unified Benchmark Complete")
    print(f"  Report: {report_path}")
    print(f"  Successful dimensions: {active_dims}/5")
    print(f"{'=' * 80}")

    if report.get("memory"):
        m = report["memory"]
        print(f"\n  1. Memory (LongMemEval-S, {N_LME} problems):")
        print(f"     Best R@5 = {m['longmemeval_s_r5']:.4f}")
        print(f"     {'Model':<16s} {'R@5':>7s} {'R@10':>7s} {'Forget':>6s}")
        print("     " + "-" * 42)
        for r in m.get("per_model", []):
            print(f"     {r['model']:<16s} {r['r@5']:6.4f} {r['r@10']:6.4f} "
                  f"{r['失忆率']:5.1%}")

    if report.get("knowledge"):
        kn = report["knowledge"]
        print(f"\n  2. Knowledge Retrieval:")
        if "nfcorpus_ndcg10" in kn and kn["nfcorpus_ndcg10"] is not None:
            print(f"     nfcorpus NDCG@10 = {kn['nfcorpus_ndcg10']:.4f}")
            print(f"     {'Model':<16s} {'NDCG':>7s} {'R@1':>7s} {'R@5':>7s} "
                  f"{'Forget':>6s}")
            print("     " + "-" * 48)
            for r in kn.get("nfcorpus_details", []):
                print(f"     {r['model']:<16s} {r['ndcg@10']:6.4f} "
                      f"{r['r@1']:6.4f} {r['r@5']:6.4f} {r['失忆率']:5.1%}")
        if "scifact_ndcg10" in kn:
            val = kn["scifact_ndcg10"]
            label = f"{val:.4f}" if val is not None else "skipped"
            print(f"     SciFact NDCG@10 = {label}")

    if report.get("code"):
        print(f"\n  3. Code Retrieval: available")
    else:
        print(f"\n  3. Code Retrieval: skipped (no data)")

    if report.get("latency"):
        lat = report["latency"]
        print(f"\n  4. Latency (ms, MCP round-trip):")
        print(f"     {'Scale':>8s} {'Recall P50':>10s} {'Recall P99':>10s} "
              f"{'Store P50':>10s} {'Store P99':>10s}")
        print("     " + "-" * 52)
        for label in ["1k", "10k", "100k", "1M"]:
            rp50 = lat.get(f"p50_{label}", "?")
            rp99 = lat.get(f"p99_{label}", "?")
            sp50 = lat.get(f"store_p50_{label}", "?")
            sp99 = lat.get(f"store_p99_{label}", "?")
            print(f"     {label:>8s} {str(rp50):>10s} {str(rp99):>10s} "
                  f"{str(sp50):>10s} {str(sp99):>10s}")
        disk_1m = lat.get("disk_mb_1M", lat.get("disk_mb_100k", "?"))
        print(f"     Disk (1M): {disk_1m} MB")

    if report.get("dream"):
        dr = report["dream"]
        print(f"\n  5. Dream Effect:")
        print(f"     Pre-Dream  R@5 = {dr['pre_dream_r5']:.4f}")
        print(f"     Post-Dream R@5 = {dr['post_dream_r5']:.4f}")
        print(f"     Δ = {dr['post_dream_r5'] - dr['pre_dream_r5']:+.4f} "
              f"({dr['improvement_pct']:+.1f}%)")

    print("\n✅ All done")


if __name__ == "__main__":
    main()
