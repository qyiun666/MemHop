#!/usr/bin/env python3
"""MemHop v0.9.0 — LongMemEval-S Benchmark (对标 agentmemory)
Usage: python3 benchmarks/run_longmemeval.py [N_problems] [model_id]
默认 100 题，BGE-base-zh
"""
import json, os, subprocess, shutil, math, time, signal, sys
import numpy as np

os.environ["TOKENIZERS_PARALLELISM"] = "false"

DATA_PATH = "/Volumes/zt_hd/projects/meow/LongMemEval/data/longmemeval_s_cleaned.json"
BINARY = os.path.join(os.path.dirname(__file__), "../target/release/quality_bench")
TEMP_DIR = "/tmp/memhop_lme_bench"
REPORT_DIR = os.path.join(os.path.dirname(__file__), "reports")

N = int(sys.argv[1]) if len(sys.argv) > 1 else 100
MODEL_ID = sys.argv[2] if len(sys.argv) > 2 else "BAAI/bge-base-zh-v1.5"
TEXT_LIMIT = 300


def kill_children():
    for sig in [signal.SIGTERM, signal.SIGKILL]:
        try:
            subprocess.run(["pkill", "-f", "quality_bench"], timeout=2)
        except:
            pass
    time.sleep(1)


def main():
    print(f"╔══════════════════════════════════════════════════════════╗")
    print(f"║   MemHop v0.9.0 — LongMemEval-S Benchmark               ║")
    print(f"║   {N} problems | {MODEL_ID}                           ║")
    print(f"╚══════════════════════════════════════════════════════════╝")

    kill_children()

    # 1. Load data
    print("\n[1/5] 加载 LongMemEval-S 数据...")
    with open(DATA_PATH) as f:
        lme_data = json.load(f)

    problems = lme_data[:N]
    docs, queries, qrels = [], [], {}
    total_sessions = 0

    for p in problems:
        for si, turns in enumerate(p["haystack_sessions"]):
            sid = p["haystack_session_ids"][si]
            text = " ".join(t["content"] for t in turns if isinstance(t, dict))
            docs.append({"id": sid, "text": text[:TEXT_LIMIT]})
            total_sessions += 1
        queries.append({"id": p["question_id"], "text": p["question"]})
        qrels[p["question_id"]] = {sid: 1 for sid in p["answer_session_ids"]}

    print(f"  {len(docs)} sessions, {len(queries)} questions")
    avg_rel = np.mean([len(v) for v in qrels.values()])
    print(f"  平均每问 {avg_rel:.1f} 个相关会话")

    # 2. Encode
    print(f"\n[2/5] 编码 ({MODEL_ID})...")
    from sentence_transformers import SentenceTransformer

    t0 = time.time()
    model = SentenceTransformer(MODEL_ID)
    load_t = time.time() - t0
    print(f"  加载: {load_t:.1f}s")

    t1 = time.time()
    dv = model.encode([d["text"] for d in docs], normalize_embeddings=True, show_progress_bar=True)
    qv = model.encode([q["text"] for q in queries], normalize_embeddings=True, show_progress_bar=True)
    enc_t = time.time() - t1
    print(f"  编码: {enc_t:.1f}s")

    del model
    import gc; gc.collect()

    for i, d in enumerate(docs):
        d["vector"] = dv[i].tolist()
    for i, q in enumerate(queries):
        q["vector"] = qv[i].tolist()

    # 3. Run MemHop
    print(f"\n[3/5] 运行 MemHop quality_bench...")
    input_data = {
        "name": "LongMemEval-S",
        "documents": docs,
        "queries": queries,
        "qrels": qrels,
        "limit": 10,
        "spread_top_k": 20,
        "dream_interval": 999999,
    }

    os.makedirs(TEMP_DIR, exist_ok=True)
    input_path = os.path.join(TEMP_DIR, "lme_input.json")
    output_path = os.path.join(TEMP_DIR, "lme_output.json")
    db_dir = os.path.join(TEMP_DIR, "lme_db")

    with open(input_path, "w") as f:
        json.dump(input_data, f)
    if os.path.exists(db_dir):
        shutil.rmtree(db_dir)

    kill_children()
    t2 = time.time()
    result = subprocess.run(
        [BINARY, "--input", input_path, "--output", output_path, "--db-dir", db_dir, "--mode", "retrieval"],
        capture_output=True, text=True, timeout=1200,
    )
    recall_t = time.time() - t2

    if result.returncode != 0:
        print(f"  ❌ 失败: {result.stderr[-300:]}")
        return

    with open(output_path) as f:
        out = json.load(f)

    # 4. Cosine baseline
    print(f"\n[4/5] 计算余弦基线...")
    cos_ndcg, cos_r1, cos_r5, cos_r10 = [], [], [], []
    for qi in range(len(queries)):
        rel = set(qrels[queries[qi]["id"]].keys())
        if not rel:
            continue
        sims = sorted(
            [(i, float(np.dot(qv[qi], dv[i]))) for i in range(len(dv))],
            key=lambda x: -x[1],
        )
        ranked = [docs[i]["id"] for i, _ in sims[:10]]
        dcg = sum(1.0 / math.log2(j + 2) for j, d in enumerate(ranked) if d in rel)
        idcg = sum(1.0 / math.log2(j + 2) for j in range(min(10, len(rel))))
        cos_ndcg.append(dcg / idcg if idcg > 0 else 0)
        cos_r1.append(1.0 if ranked[0] in rel else 0.0)
        cos_r5.append(sum(1 for d in ranked[:5] if d in rel) / len(rel))
        cos_r10.append(sum(1 for d in ranked[:10] if d in rel) / len(rel))

    # 5. Report
    res = out["results"][0]
    m_ndcg = res["ndcg_10"]["mean"]
    m_r1 = res["recall_1"]["mean"]
    m_r5 = res["recall_5"]["mean"]
    m_r10 = res["recall_10"]["mean"]
    m_mrr = res["mrr"]["mean"]
    lat = res.get("avg_recall_latency_us", 0)

    c_ndcg = np.mean(cos_ndcg)
    c_r1 = np.mean(cos_r1)
    c_r5 = np.mean(cos_r5)
    c_r10 = np.mean(cos_r10)

    amnesia = 1 - m_r1
    halluc = 1 - m_r5

    print(f"\n╔══════════════════════════════════════════════════════════════╗")
    print(f"║   MemHop v0.9.0 — LongMemEval-S 结果                       ║")
    print(f"╠══════════════════╦═══════════╦═══════════╦══════════════════╣")
    print(f"║ Metric           ║ MemHop    ║ Cos Upper ║ Ratio            ║")
    print(f"╠══════════════════╬═══════════╬═══════════╬══════════════════╣")
    for name, mh, cs in [
        ("NDCG@10", m_ndcg, c_ndcg),
        ("MRR", m_mrr, 0),
        ("R@1  (失忆={:.0%})".format(amnesia), m_r1, c_r1),
        ("R@5  (幻听≈{:.0%})".format(halluc), m_r5, c_r5),
        ("R@10", m_r10, c_r10),
    ]:
        if cs > 0:
            print(f"║ {name:<18s} ║ {mh:8.4f}  ║ {cs:8.4f}  ║ {mh/cs:15.1%} ║")
        else:
            print(f"║ {name:<18s} ║ {mh:8.4f}  ║     —     ║                  ║")
    print(f"║ Latency          ║ {lat:7.0f}µs ║     —     ║                  ║")
    print(f"╠══════════════════╩═══════════╩═══════════╩══════════════════╣")
    print(f"║  agentmemory: LongMemEval-S R@5=95.2% R@10=98.6%          ║")
    print(f"║  失忆率 ≈ 4.8% | 幻听率 ≈ 4.8%                             ║")
    print(f"╚══════════════════════════════════════════════════════════════╝")
    print(f"  编码: {enc_t:.0f}s | 召回: {recall_t:.0f}s | 共 {len(queries)} 问 {len(docs)} 会话")

    # Save report
    os.makedirs(REPORT_DIR, exist_ok=True)
    ts = time.strftime("%Y%m%d_%H%M%S")
    report_path = os.path.join(REPORT_DIR, f"lme_comparison_{ts}.json")
    with open(report_path, "w") as f:
        json.dump(
            {
                "benchmark": "LongMemEval-S",
                "model": MODEL_ID,
                "problems": N,
                "sessions": len(docs),
                "queries": len(queries),
                "memhop": {
                    "ndcg@10": round(m_ndcg, 4),
                    "mrr": round(m_mrr, 4),
                    "r@1": round(m_r1, 4),
                    "r@5": round(m_r5, 4),
                    "r@10": round(m_r10, 4),
                    "失忆率": round(amnesia, 4),
                    "幻听率": round(halluc, 4),
                    "latency_us": round(lat),
                },
                "cosine_upper": {
                    "ndcg@10": round(c_ndcg, 4),
                    "r@1": round(c_r1, 4),
                    "r@5": round(c_r5, 4),
                    "r@10": round(c_r10, 4),
                },
                "agentmemory": {"r@5": 0.952, "r@10": 0.986},
            },
            f,
            indent=2,
            ensure_ascii=False,
        )
    print(f"\n  报告: {report_path}")

    kill_children()
    shutil.rmtree(TEMP_DIR, ignore_errors=True)


if __name__ == "__main__":
    main()
