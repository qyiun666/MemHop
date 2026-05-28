#!/usr/bin/env python3
"""MemHop v0.9.1 — LongMemEval-S (turn-level, 对标 agentmemory)
Usage: python3 benchmarks/run_lme.py [N_problems]

每条 turn 独立存 MemHop，recall → hit_turns → 按 session 聚合 → 对标 agentmemory
"""
import json, os, subprocess, shutil, math, time, sys
import numpy as np

os.environ["TOKENIZERS_PARALLELISM"] = "false"

BINARY = os.path.join(os.path.dirname(__file__), "../target/release/quality_bench")
LME_DATA = "/Volumes/zt_hd/projects/meow/LongMemEval/data/longmemeval_s_cleaned.json"
N = int(sys.argv[1]) if len(sys.argv) > 1 else 100


def main():
    print(f"v0.9.1 LongMemEval-S ({N}题, turn-level)")
    print("=" * 60)

    # 1. Load
    with open(LME_DATA) as f:
        lme_data = json.load(f)
    problems = lme_data[:N]

    turns_in, queries_in, qrels = [], [], {}
    for pi, p in enumerate(problems):
        for si, turns_raw in enumerate(p["haystack_sessions"]):
            sid = p["haystack_session_ids"][si]
            for ti, turn in enumerate(turns_raw):
                if isinstance(turn, dict) and turn.get("content"):
                    turns_in.append({
                        "id": f"{sid}__t{ti}",
                        "text": turn["content"][:500],
                        "session_id": sid,
                        "turn_id": f"{sid}__t{ti}",
                        "topic_label": None,
                    })
        queries_in.append({"id": p["question_id"], "text": p["question"]})
        qrels[p["question_id"]] = {sid: 1 for sid in p["answer_session_ids"]}

    print(f"  {len(turns_in)} turns, {len(queries_in)} queries")

    # 2. Encode
    from sentence_transformers import SentenceTransformer

    print("Encoding BGE-base-zh...")
    model = SentenceTransformer("BAAI/bge-base-zh-v1.5")
    t0 = time.time()
    dv = model.encode([t["text"] for t in turns_in], normalize_embeddings=True, show_progress_bar=True)
    qv = model.encode([q["text"] for q in queries_in], normalize_embeddings=True, show_progress_bar=True)
    print(f"  {time.time()-t0:.0f}s")
    del model; import gc; gc.collect()

    for i, t in enumerate(turns_in): t["vector"] = dv[i].tolist()
    for i, q in enumerate(queries_in): q["vector"] = qv[i].tolist()

    # 3. MemHop (turn-mode aggregation)
    input_data = {
        "name": "lme-turn",
        "documents": turns_in,
        "queries": queries_in,
        "qrels": qrels,
        "limit": 10,
        "spread_top_k": 50,
        "dream_interval": 999999,
        "aggregate_mode": "turn",
    }

    os.makedirs("/tmp/lme_v091", exist_ok=True)
    with open("/tmp/lme_v091/i.json", "w") as f:
        json.dump(input_data, f)
    db = "/tmp/lme_v091/db"
    if os.path.exists(db): shutil.rmtree(db)

    subprocess.run(["pkill", "-f", "quality_bench"], timeout=2)
    time.sleep(1)
    print("Running MemHop...")
    t1 = time.time()
    r = subprocess.run(
        [BINARY, "--input", "/tmp/lme_v091/i.json", "--output", "/tmp/lme_v091/o.json",
         "--db-dir", db, "--mode", "associative"],
        capture_output=True, text=True, timeout=1200,
    )
    print(f"  {time.time()-t1:.0f}s")

    with open("/tmp/lme_v091/o.json") as f:
        out = json.load(f)
    res = out["results"][0]

    # Cosine baseline (session-level: average turn vectors per session)
    from collections import defaultdict
    session_turns = defaultdict(list)
    for i, t in enumerate(turns_in):
        session_turns[t["session_id"]].append(i)

    session_vecs, session_ids = [], []
    for sid, idxs in sorted(session_turns.items()):
        session_vecs.append(np.mean([dv[i] for i in idxs], axis=0))
        session_ids.append(sid)
    sv_arr = np.array(session_vecs)

    cos_ndcg, cos_r5, cos_r10 = [], [], []
    for qi in range(len(queries_in)):
        rel = set(qrels[queries_in[qi]["id"]].keys())
        sims = sorted([(si, float(np.dot(qv[qi], sv_arr[si]))) for si in range(len(sv_arr))], key=lambda x: -x[1])
        ranked = [session_ids[i] for i, _ in sims[:10]]
        dcg = sum(1.0 / math.log2(j + 2) for j, d in enumerate(ranked) if d in rel)
        idcg = sum(1.0 / math.log2(j + 2) for j in range(min(10, len(rel))))
        cos_ndcg.append(dcg / idcg if idcg > 0 else 0)
        cos_r5.append(sum(1 for d in ranked[:5] if d in rel) / len(rel))
        cos_r10.append(sum(1 for d in ranked[:10] if d in rel) / len(rel))

    m_r1 = res["recall_1"]["mean"]
    m_r5 = res["recall_5"]["mean"]
    m_r10 = res["recall_10"]["mean"]
    m_ndcg = res["ndcg_10"]["mean"]
    m_mrr = res["mrr"]["mean"]
    lat = res.get("avg_recall_latency_us", 0)

    print(f"""
╔════════════════════════════════════════════════════════╗
║   MemHop v0.9.1 vs agentmemory — LongMemEval-S        ║
╠══════════════════╦═══════════╦═══════════╦═════════════╣
║ Metric           ║ MemHop    ║ Cos Upper ║ agentmemory ║
╠══════════════════╬═══════════╬═══════════╬═════════════╣
║ NDCG@10          ║ {m_ndcg:.4f}  ║ {np.mean(cos_ndcg):.4f}    ║     —       ║
║ MRR              ║ {m_mrr:.4f}  ║     —     ║     —       ║
║ R@1 → 失忆率     ║ {m_r1:.1%}    ║   —       ║  ~95% → 5%  ║
║ R@5 → 幻听率     ║ {m_r5:.1%}    ║ {np.mean(cos_r5):.1%}     ║ 95.2% → 4.8%║
║ R@10             ║ {m_r10:.1%}    ║ {np.mean(cos_r10):.1%}     ║    98.6%    ║
║ Latency          ║ {lat:.0f}µs    ║     —     ║   ~14ms      ║
╚══════════════════╩═══════════╩═══════════╩═════════════╝
{N} 题 × {len(session_ids)} sessions × {len(turns_in)} turns | BGE-base-zh | turn-level
""")

    subprocess.run(["pkill", "-f", "quality_bench"], timeout=2)
    shutil.rmtree("/tmp/lme_v091", ignore_errors=True)


if __name__ == "__main__":
    main()
