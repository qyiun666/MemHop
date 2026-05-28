#!/usr/bin/env python3
"""MemHop v0.9.0 — LongMemEval-S (turn-level encoding)
Usage: python3 benchmarks/run_lme.py [N_problems]

每条 session 拆成 turn→逐 turn 编码（快10倍）→ 按 session 平均池化 → recall
"""
import json, os, subprocess, shutil, math, time, sys
import numpy as np

os.environ["TOKENIZERS_PARALLELISM"] = "false"

BINARY = os.path.join(os.path.dirname(__file__), "../target/release/quality_bench")
LME_DATA = "/Volumes/zt_hd/projects/meow/LongMemEval/data/longmemeval_s_cleaned.json"
N = int(sys.argv[1]) if len(sys.argv) > 1 else 100


def main():
    print(f"╔══════════════════════════════════════════════════════╗")
    print(f"║   MemHop v0.9.0 — LongMemEval-S ({N}题, turn级编码)  ║")
    print(f"╚══════════════════════════════════════════════════════╝")

    # 1. Load data + split into turns
    print("\n[1/4] 加载 + 拆分为 turns...")
    with open(LME_DATA) as f:
        lme_data = json.load(f)
    problems = lme_data[:N]

    all_turns, session_turns, queries_ref, qrels = [], {}, [], {}
    for p in problems:
        for si, turns in enumerate(p["haystack_sessions"]):
            sid = p["haystack_session_ids"][si]
            turn_texts = [t["content"] for t in turns if isinstance(t, dict)]
            session_turns[sid] = len(all_turns)  # start index
            for ti, text in enumerate(turn_texts):
                all_turns.append(f"{sid}__{ti}__{text}")
        queries_ref.append((p["question_id"], p["question"]))
        qrels[p["question_id"]] = {sid: 1 for sid in p["answer_session_ids"]}

    print(f"  {len(all_turns)} turns across {len(session_turns)} sessions")

    # 2. Encode each turn (fast — short texts)
    print(f"\n[2/4] 编码 {len(all_turns)} turns (BGE-base-zh)...")
    from sentence_transformers import SentenceTransformer

    model = SentenceTransformer("BAAI/bge-base-zh-v1.5")
    t0 = time.time()
    turn_vecs = model.encode(all_turns, normalize_embeddings=True, show_progress_bar=True)
    qv = model.encode([q[1] for q in queries_ref], normalize_embeddings=True, show_progress_bar=True)
    print(f"  编码: {time.time()-t0:.0f}s")
    del model; import gc; gc.collect()

    # 3. Pool turns → session vectors
    print(f"\n[3/4] Turn → Session 平均池化...")
    session_vecs, session_ids = [], []
    for sid in sorted(session_turns.keys()):
        si = session_turns[sid]
        # Find all turns for this session (search by prefix)
        turn_indices = [i for i, t in enumerate(all_turns) if t.startswith(sid + "__")]
        if turn_indices:
            session_vecs.append(np.mean([turn_vecs[i] for i in turn_indices], axis=0))
            session_ids.append(sid)

    docs_in = [{"id": sid, "text": sid, "vector": v.tolist()} for sid, v in zip(session_ids, session_vecs)]
    queries_in = [{"id": qid, "text": qt, "vector": v.tolist()} for (qid, qt), v in zip(queries_ref, qv)]

    # 4. Run MemHop
    print(f"\n[4/4] MemHop recall (Associative Mode)...")
    input_data = {
        "name": "lme-turn",
        "documents": docs_in,
        "queries": queries_in,
        "qrels": qrels,
        "limit": 10,
        "spread_top_k": 20,
        "dream_interval": 999999,
    }

    os.makedirs("/tmp/lme3", exist_ok=True)
    with open("/tmp/lme3/i.json", "w") as f:
        json.dump(input_data, f)
    db = "/tmp/lme3/db"
    if os.path.exists(db):
        shutil.rmtree(db)

    subprocess.run(["pkill", "-f", "quality_bench"], timeout=2)
    time.sleep(1)

    t1 = time.time()
    r = subprocess.run(
        [BINARY, "--input", "/tmp/lme3/i.json", "--output", "/tmp/lme3/o.json",
         "--db-dir", db, "--mode", "associative"],
        capture_output=True, text=True, timeout=600,
    )
    print(f"  Recall: {time.time()-t1:.0f}s")

    with open("/tmp/lme3/o.json") as f:
        out = json.load(f)
    res = out["results"][0]

    # Cosine baseline (on session vectors)
    sv_arr = np.array(session_vecs)
    cos_r5, cos_r10, cos_ndcg = [], [], []
    for qi in range(len(queries_in)):
        rel = set(qrels[queries_in[qi]["id"]].keys())
        sims = sorted(
            [(si, float(np.dot(qv[qi], sv_arr[si]))) for si in range(len(sv_arr))],
            key=lambda x: -x[1],
        )
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
║   MemHop v0.9.0 vs agentmemory — LongMemEval-S        ║
╠══════════════════╦═══════════╦═══════════╦═════════════╣
║ Metric           ║ MemHop    ║ Cos Upper ║ agentmemory ║
╠══════════════════╬═══════════╬═══════════╬═════════════╣
║ NDCG@10          ║ {m_ndcg:.4f}  ║ {np.mean(cos_ndcg):.4f}    ║     —       ║
║ MRR              ║ {m_mrr:.4f}  ║     —     ║     —       ║
║ R@1  → 失忆率    ║ {m_r1:.1%}    ║ {np.mean([1.0 if ranked[0] in rel else 0.0 for ranked, rel in zip(ranked, rel)]):.1%}     ║  ~95% → 5%  ║
║ R@5  → 幻听率    ║ {m_r5:.1%}    ║ {np.mean(cos_r5):.1%}     ║ 95.2% → 4.8%║
║ R@10             ║ {m_r10:.1%}    ║ {np.mean(cos_r10):.1%}     ║    98.6%    ║
║ Latency          ║ {lat:.0f}µs    ║     —     ║   ~14ms      ║
╚══════════════════╩═══════════╩═══════════╩═════════════╝
{N} 题 × {len(session_ids)} 会话 × {len(all_turns)} turns | BGE-base-zh | Associative
""")

    subprocess.run(["pkill", "-f", "quality_bench"], timeout=2)
    shutil.rmtree("/tmp/lme3", ignore_errors=True)


if __name__ == "__main__":
    main()
