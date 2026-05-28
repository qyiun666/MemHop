#!/usr/bin/env python3
"""MemHop v0.9.0 — 全量权威 Benchmark（一键）
Usage: python3 benchmarks/run_all.py [N_lme_problems]

依次执行：
  1. nfcorpus 编码器对比 (3 模型, BEIR 权威测试)
  2. LongMemEval-S (100/500 题, 对标 agentmemory)
  3. 延迟 Benchmark (1K/5K/10K)

结果: benchmarks/reports/summary_*.json + encoder_comparison_*.json + lme_comparison_*.json
"""
import gc, json, os, subprocess, shutil, math, time, signal, sys
import numpy as np

os.environ["TOKENIZERS_PARALLELISM"] = "false"

# ═══════════════════════════════════════════
BINARY = os.path.join(os.path.dirname(__file__), "../target/release/quality_bench")
LATENCY_BIN = os.path.join(os.path.dirname(__file__), "../target/release/latency_bench")
REPORT_DIR = os.path.join(os.path.dirname(__file__), "reports")
DATA_DIR = os.path.join(os.path.dirname(__file__), "data/beir/nfcorpus/nfcorpus")
LME_DATA = "/Volumes/zt_hd/projects/meow/LongMemEval/data/longmemeval_s_cleaned.json"
TEMP = "/tmp/memhop_full_bench"

N_LME = int(sys.argv[1]) if len(sys.argv) > 1 else 100
MODELS = [
    {"id": "BAAI/bge-m3", "name": "BGE-M3", "dim": 1024},
    {"id": "BAAI/bge-base-zh-v1.5", "name": "BGE-base-zh", "dim": 768},
    {"id": "BAAI/bge-small-zh-v1.5", "name": "BGE-small-zh", "dim": 512},
]
NF_DOCS, NF_QUERIES, TEXT_LIMIT = 500, 50, 256


def kill():
    for s in [signal.SIGTERM, signal.SIGKILL]:
        try:
            subprocess.run(["pkill", "-f", "quality_bench"], timeout=2)
            subprocess.run(["pkill", "-f", "latency_bench"], timeout=2)
        except:
            pass
    time.sleep(2)


def build():
    print("🔨 编译...")
    subprocess.run(["cargo", "build", "--release", "--features", "onnx"], cwd=os.path.dirname(__file__) + "/..", check=True)


# ═══════════════════════════════════════════
# Phase 1: nfcorpus 编码器对比
# ═══════════════════════════════════════════
def load_nfcorpus():
    corpus, queries, qrels_raw = {}, {}, {}
    for line in open(os.path.join(DATA_DIR, "corpus.jsonl")):
        item = json.loads(line)
        corpus[item["_id"]] = item["text"][:TEXT_LIMIT]
    for line in open(os.path.join(DATA_DIR, "queries.jsonl")):
        item = json.loads(line)
        queries[item["_id"]] = item["text"]
    for line in open(os.path.join(DATA_DIR, "qrels/test.tsv")):
        parts = line.strip().split("\t")
        if len(parts) >= 3 and parts[0] != "query-id":
            qrels_raw.setdefault(parts[0], set()).add(parts[1])

    import random; random.seed(42)
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
    from sentence_transformers import SentenceTransformer

    all_results = []
    for mi, m in enumerate(MODELS):
        print(f"\n  [{mi+1}/3] {m['name']} ({m['dim']}d)...", end=" ", flush=True)
        t0 = time.time()

        try:
            model = SentenceTransformer(m["id"], device="cpu")  # CPU 避免 MPS OOM
        except:
            model = SentenceTransformer(m["id"])

        params = sum(p.numel() for p in model.parameters()) / 1e6
        doc_texts = [corpus[did] for did in doc_ids]
        query_texts = [queries[qid] for qid in query_ids]
        dv = model.encode(doc_texts, normalize_embeddings=True, show_progress_bar=False)
        qv = model.encode(query_texts, normalize_embeddings=True, show_progress_bar=False)
        del model; import gc; gc.collect()

        docs_in = [{"id": did, "text": corpus[did], "vector": dv[i].tolist()} for i, did in enumerate(doc_ids)]
        queries_in = [{"id": qid, "text": queries[qid], "vector": qv[i].tolist()} for i, qid in enumerate(query_ids)]

        input_data = {"name": f"nf-{m['name']}", "documents": docs_in, "queries": queries_in, "qrels": qrels, "limit": 10, "spread_top_k": 20, "dream_interval": 50}
        os.makedirs(TEMP, exist_ok=True)
        ip = os.path.join(TEMP, f"nf_{m['name']}.json")
        op = os.path.join(TEMP, f"nf_{m['name']}_o.json")
        db = os.path.join(TEMP, f"nf_{m['name']}_db")
        with open(ip, "w") as f: json.dump(input_data, f)
        if os.path.exists(db): shutil.rmtree(db)

        kill()
        subprocess.run([BINARY, "--input", ip, "--output", op, "--db-dir", db, "--mode", "retrieval"], capture_output=True, text=True, timeout=600)
        with open(op) as f: out = json.load(f)

        res = out["results"][0]
        rpt = {
            "model": m["name"], "dim": m["dim"], "params_m": round(params, 1),
            "ndcg@10": round(res["ndcg_10"]["mean"], 4),
            "mrr": round(res["mrr"]["mean"], 4),
            "r@1": round(res["recall_1"]["mean"], 4),
            "r@5": round(res["recall_5"]["mean"], 4),
            "r@10": round(res["recall_10"]["mean"], 4),
            "失忆率": round(1 - res["recall_1"]["mean"], 4),
            "幻听率": round(1 - res["recall_5"]["mean"], 4),
            "latency_us": round(res.get("avg_recall_latency_us", 0)),
            "encode_s": round(time.time() - t0, 1),
        }

        cos_ndcg = []
        for qi in range(len(query_ids)):
            rel = set(qrels[query_ids[qi]].keys())
            sims = sorted([(i, float(np.dot(qv[qi], dv[i]))) for i in range(len(dv))], key=lambda x: -x[1])
            ranked = [doc_ids[i] for i, _ in sims[:10]]
            dcg = sum(1.0 / math.log2(j + 2) for j, d in enumerate(ranked) if d in rel)
            idcg = sum(1.0 / math.log2(j + 2) for j in range(min(10, len(rel))))
            cos_ndcg.append(dcg / idcg if idcg > 0 else 0)
        rpt["cos_ndcg@10"] = round(np.mean(cos_ndcg), 4)
        rpt["ratio_pct"] = round(rpt["ndcg@10"] / rpt["cos_ndcg@10"] * 100, 1) if rpt["cos_ndcg@10"] > 0 else 0

        all_results.append(rpt)
        kill(); shutil.rmtree(db, ignore_errors=True)
        print(f"NDCG={rpt['ndcg@10']:.4f} 失忆={rpt['失忆率']:.1%} 延迟={rpt['latency_us']}µs {rpt['encode_s']:.0f}s")

    return all_results


# ═══════════════════════════════════════════
# Phase 2: LongMemEval-S
# ═══════════════════════════════════════════
def run_longmemeval():
    from sentence_transformers import SentenceTransformer

    with open(LME_DATA) as f:
        lme_data = json.load(f)
    problems = lme_data[:N_LME]

    results = []
    for mi, m in enumerate(MODELS):
        print(f"\n  [{mi+1}/3] {m['name']} (LongMemEval-S, {N_LME}题)...", end=" ", flush=True)
        t0 = time.time()

        docs, queries, qrels = [], [], {}
        for p in problems:
            for si, turns in enumerate(p["haystack_sessions"]):
                sid = p["haystack_session_ids"][si]
                text = " ".join(t["content"] for t in turns if isinstance(t, dict))
                docs.append({"id": sid, "text": text[:800]})  # 800 字符，保留关键信息
            queries.append({"id": p["question_id"], "text": p["question"]})
            qrels[p["question_id"]] = {sid: 1 for sid in p["answer_session_ids"]}

        try:
            model = SentenceTransformer(m["id"], device="cpu")  # CPU 避免 MPS OOM
        except:
            model = SentenceTransformer(m["id"])

        dv = model.encode([d["text"] for d in docs], normalize_embeddings=True, show_progress_bar=False)
        qv = model.encode([q["text"] for q in queries], normalize_embeddings=True, show_progress_bar=False)
        del model; gc.collect()

        for i, d in enumerate(docs): d["vector"] = dv[i].tolist()
        for i, q in enumerate(queries): q["vector"] = qv[i].tolist()

        input_data = {"name": "LME", "documents": docs, "queries": queries, "qrels": qrels, "limit": 10, "spread_top_k": 20, "dream_interval": 999999}
        ip = os.path.join(TEMP, f"lme_{m['name']}.json")
        op = os.path.join(TEMP, f"lme_{m['name']}_o.json")
        db = os.path.join(TEMP, f"lme_{m['name']}_db")
        with open(ip, "w") as f: json.dump(input_data, f)
        if os.path.exists(db): shutil.rmtree(db)

        kill()
        subprocess.run([BINARY, "--input", ip, "--output", op, "--db-dir", db, "--mode", "associative"], capture_output=True, text=True, timeout=1200)
        with open(op) as f: out = json.load(f)

        res = out["results"][0]
        rpt = {
            "model": m["name"], "problems": N_LME, "sessions": len(docs),
            "ndcg@10": round(res["ndcg_10"]["mean"], 4),
            "mrr": round(res["mrr"]["mean"], 4),
            "r@1": round(res["recall_1"]["mean"], 4),
            "r@5": round(res["recall_5"]["mean"], 4),
            "r@10": round(res["recall_10"]["mean"], 4),
            "失忆率": round(1 - res["recall_1"]["mean"], 4),
            "幻听率": round(1 - res["recall_5"]["mean"], 4),
            "latency_us": round(res.get("avg_recall_latency_us", 0)),
            "encode_s": round(time.time() - t0, 1),
        }

        cos_ndcg = []
        for qi in range(len(queries)):
            rel = set(qrels[queries[qi]["id"]].keys())
            sims = sorted([(i, float(np.dot(qv[qi], dv[i]))) for i in range(len(dv))], key=lambda x: -x[1])
            ranked = [docs[i]["id"] for i, _ in sims[:10]]
            dcg = sum(1.0 / math.log2(j + 2) for j, d in enumerate(ranked) if d in rel)
            idcg = sum(1.0 / math.log2(j + 2) for j in range(min(10, len(rel))))
            cos_ndcg.append(dcg / idcg if idcg > 0 else 0)
        rpt["cos_ndcg@10"] = round(np.mean(cos_ndcg), 4)

        results.append(rpt)
        kill(); shutil.rmtree(db, ignore_errors=True)
        print(f"R@5={rpt['r@5']:.4f} 失忆={rpt['失忆率']:.1%} {rpt['encode_s']:.0f}s")

    return results


# ═══════════════════════════════════════════
# Phase 3: 延迟
# ═══════════════════════════════════════════
def run_latency():
    print(f"\n  [延迟] 1K/5K/10K...", end=" ", flush=True)
    kill()
    r = subprocess.run([LATENCY_BIN, "--scales", "1000,5000,10000", "--queries", "30"], capture_output=True, text=True, timeout=600)
    # Parse latency output
    lat_data = {}
    for line in r.stdout.split("\n"):
        if "scale=" in line:
            parts = line.split()
            scale = parts[0].split("=")[1]
            for p in parts[1:]:
                if "recall_p50=" in p: lat_data.setdefault(scale, {})["p50"] = p.split("=")[1].rstrip("ms")
                if "recall_p99=" in p: lat_data.setdefault(scale, {})["p99"] = p.split("=")[1].rstrip("ms")
        if "peak_disk_MB" in line:
            lat_data["disk"] = line.split("=")[1].strip()
    kill()
    print("完成")
    return lat_data


# ═══════════════════════════════════════════
# Main
# ═══════════════════════════════════════════
def main():
    print("╔══════════════════════════════════════════════════════════════╗")
    print("║   MemHop v0.9.0 — 全量权威 Benchmark                        ║")
    print("║   nfcorpus + LongMemEval-S + 延迟 | 3 模型                   ║")
    print("╚══════════════════════════════════════════════════════════════╝")

    kill()
    build()

    os.makedirs(REPORT_DIR, exist_ok=True)
    full_report = {"timestamp": time.strftime("%Y%m%d_%H%M%S"), "memhop_version": "0.9.0"}

    # ── Phase 1: nfcorpus ──
    print("\n═══ Phase 1/3: BEIR nfcorpus 编码器对比 ═══")
    doc_ids, query_ids, corpus, queries, qrels = load_nfcorpus()
    print(f"  数据: {len(doc_ids)} docs × {len(query_ids)} queries")
    nf_results = run_nfcorpus(doc_ids, query_ids, corpus, queries, qrels)
    full_report["nfcorpus"] = nf_results

    # ── Phase 2: LongMemEval-S ──
    print(f"\n═══ Phase 2/3: LongMemEval-S ({N_LME}题, 对标 agentmemory) ═══")
    lme_results = run_longmemeval()
    full_report["longmemeval_s"] = lme_results

    # ── Phase 3: Latency ──
    print(f"\n═══ Phase 3/3: 延迟 Benchmark ═══")
    lat = run_latency()
    full_report["latency"] = lat

    # ── Save ──
    ts = time.strftime("%Y%m%d_%H%M%S")
    report_path = os.path.join(REPORT_DIR, f"summary_{ts}.json")
    with open(report_path, "w") as f:
        json.dump(full_report, f, indent=2, ensure_ascii=False)

    # ── Print summary ──
    print(f"\n{'='*80}")
    print(f"  全量 Benchmark 完成 — 报告: {report_path}")
    print(f"{'='*80}")
    print(f"\n  nfcorpus (3 模型):")
    print(f"  {'模型':<16s} {'NDCG':>7s} {'R@1':>7s} {'R@5':>7s} {'失忆':>6s} {'幻听':>6s} {'延迟':>7s}")
    print("  " + "-" * 62)
    for r in nf_results:
        print(f"  {r['model']:<16s} {r['ndcg@10']:6.4f} {r['r@1']:6.4f} {r['r@5']:6.4f} {r['失忆率']:5.1%} {r['幻听率']:5.1%} {r['latency_us']:6.0f}µs")

    print(f"\n  LongMemEval-S ({N_LME}题, 3 模型):")
    print(f"  {'模型':<16s} {'R@5':>7s} {'R@10':>7s} {'失忆':>6s} {'幻听':>6s}")
    print("  " + "-" * 48)
    for r in lme_results:
        print(f"  {r['model']:<16s} {r['r@5']:6.4f} {r['r@10']:6.4f} {r['失忆率']:5.1%} {r['幻听率']:5.1%}")

    print(f"\n  ─── 竞品对比 ───")
    print(f"  {'':16s} {'LongMemEval-S R@5':>18s} {'失忆率':>8s}")
    print(f"  {'agentmemory':16s} {'95.2%':>18s} {'4.8%':>8s}")
    for r in lme_results:
        print(f"  {r['model']:16s} {r['r@5']*100:17.1f}% {r['失忆率']*100:7.1f}%")

    print(f"\n  延迟 (BGE-M3):")
    for k, v in sorted(lat.items()):
        if isinstance(v, dict):
            print(f"    {k}: p50={v.get('p50','?')} p99={v.get('p99','?')}")
        else:
            print(f"    disk={v}")

    kill()
    shutil.rmtree(TEMP, ignore_errors=True)
    print("\n✅ 全部完成")


if __name__ == "__main__":
    main()
