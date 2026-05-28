#!/usr/bin/env python3
"""MemHop v0.9.0 — 多模型编码器对比 Benchmark
Usage: python3 benchmarks/compare_encoders.py
依次测试 BGE-M3 → BGE-base-zh → BGE-small-zh，自动清进程，结果保存到 benchmarks/reports/
"""
import json, os, subprocess, shutil, math, time, signal, glob
import numpy as np

# 消 tokenizers fork 警告
os.environ["TOKENIZERS_PARALLELISM"] = "false"

# ═══════════════════════════════════════════════════════════════
# 配置
# ═══════════════════════════════════════════════════════════════
MODELS = [
    {"id": "BAAI/bge-m3",              "name": "BGE-M3",          "dim": 1024},
    {"id": "BAAI/bge-base-zh-v1.5",    "name": "BGE-base-zh",    "dim": 768},
    {"id": "BAAI/bge-small-zh-v1.5",   "name": "BGE-small-zh",   "dim": 512},
]

DATA_DIR = os.path.join(os.path.dirname(__file__), "data/beir/nfcorpus/nfcorpus")
REPORT_DIR = os.path.join(os.path.dirname(__file__), "reports")
BINARY = os.path.join(os.path.dirname(__file__), "../target/release/quality_bench")
TEMP_DIR = "/tmp/memhop_encoder_bench"

# 测试规模
N_DOCS = 500
N_QUERIES = 50
TEXT_LIMIT = 256  # 截断长文本

def kill_children():
    """清理残留进程"""
    for sig in [signal.SIGTERM, signal.SIGKILL]:
        try:
            subprocess.run(["pkill", "-f", "quality_bench"], timeout=2)
            subprocess.run(["pkill", "-f", "sentence_transformers"], timeout=2)
        except:
            pass
    time.sleep(1)

def load_data():
    """加载本地 BEIR nfcorpus 数据"""
    corpus = {}
    corpus_path = os.path.join(DATA_DIR, "corpus.jsonl")
    if not os.path.exists(corpus_path):
        # 生成合成测试数据
        print("  [warn] 无本地 BEIR 数据，使用合成数据")
        return generate_synthetic()
    
    for line in open(corpus_path):
        item = json.loads(line)
        corpus[item["_id"]] = item["text"][:TEXT_LIMIT]
    
    queries = {}
    for line in open(os.path.join(DATA_DIR, "queries.jsonl")):
        item = json.loads(line)
        queries[item["_id"]] = item["text"]
    
    qrels = {}
    qrel_path = os.path.join(DATA_DIR, "qrels/test.tsv")
    for line in open(qrel_path):
        parts = line.strip().split("\t")
        if len(parts) >= 3 and parts[0] != "query-id":
            qrels.setdefault(parts[0], set()).add(parts[1])
    
    import random; random.seed(42)
    all_docs = sorted(corpus.keys())
    all_queries = [q for q in sorted(queries.keys()) if q in qrels]
    
    doc_ids = random.sample(all_docs, min(N_DOCS, len(all_docs)))
    query_ids = random.sample(all_queries, min(N_QUERIES, len(all_queries)))
    
    qrels_f = {}
    valid_queries = []
    for qid in query_ids:
        rel = qrels.get(qid, set()) & set(doc_ids)
        if rel:
            qrels_f[qid] = {did: 1 for did in rel}
            valid_queries.append(qid)
    
    # 只保留有相关文档的查询
    query_ids = valid_queries[:N_QUERIES]
    
    return doc_ids, query_ids, corpus, queries, qrels_f

def generate_synthetic():
    """合成测试数据 (替代 BEIR)"""
    cats = ["Rust","Python","Docker","SQL","React","Linux","Go","Kafka","Redis","K8s"]
    variants = ["tutorial","guide","reference","patterns","best practices","examples","cheatsheet","internals","debugging","testing"]
    
    import random; random.seed(42)
    corpus = {}
    for ci, c in enumerate(cats):
        for vi, v in enumerate(variants):
            corpus[f"d_{ci}_{vi}"] = f"{c} programming language {v}"
    
    queries = {}
    for ci, c in enumerate(cats):
        queries[f"q_{ci}"] = f"How to write {c} code?"
    
    doc_ids = sorted(corpus.keys())
    query_ids = sorted(queries.keys())
    qrels = {f"q_{ci}": {f"d_{ci}_{vi}": 1 for vi in range(10)} for ci in range(len(cats))}
    
    return doc_ids[:N_DOCS], query_ids[:N_QUERIES], corpus, queries, qrels

def run_model(model_info, doc_ids, query_ids, corpus, queries, qrels):
    """运行单个模型的 benchmark"""
    print(f"\n{'='*60}")
    print(f"  测试: {model_info['name']} ({model_info['dim']}d)")
    print(f"  Docs: {len(doc_ids)} | Queries: {len(query_ids)}")
    print(f"{'='*60}")
    
    # 加载模型 (仅本地，不连网)
    from sentence_transformers import SentenceTransformer
    print(f"  加载 {model_info['id']}...")
    t0 = time.time()
    os.environ["HF_HUB_OFFLINE"] = "1"
    try:
        model = SentenceTransformer(model_info["id"])
    except:
        try:
            model = SentenceTransformer(model_info["id"], device="cpu")
        except Exception as e:
            print(f"  ❌ 加载失败: {e}")
            return None
    
    params = sum(p.numel() for p in model.parameters()) / 1e6
    load_time = time.time() - t0
    print(f"  参数量: {params:.0f}M | 加载: {load_time:.1f}s")
    
    # 编码（Rust quality_bench 会自动 pad 到 1024）
    print("  编码...")
    t1 = time.time()
    doc_texts = [corpus[did] for did in doc_ids]
    query_texts = [queries[qid] for qid in query_ids]
    dv = model.encode(doc_texts, normalize_embeddings=True, show_progress_bar=True)
    qv = model.encode(query_texts, normalize_embeddings=True, show_progress_bar=True)
    enc_time = time.time() - t1
    print(f"  编码完成: {enc_time:.1f}s ({len(dv)} 文档, {len(qv)} 查询)")
    
    # 释放 Python 模型 (腾内存)
    del model
    import gc; gc.collect()
    
    # 构建输入
    docs_in = [{"id": did, "text": corpus[did], "vector": dv[i].tolist()} 
               for i, did in enumerate(doc_ids)]
    queries_in = [{"id": qid, "text": queries[qid], "vector": qv[i].tolist()}
                  for i, qid in enumerate(query_ids)]
    
    input_data = {
        "name": f"encoder-bench-{model_info['name']}",
        "documents": docs_in,
        "queries": queries_in,
        "qrels": qrels,
        "limit": 10,
        "spread_top_k": 20,
        "dream_interval": 50,
    }
    
    input_path = os.path.join(TEMP_DIR, f"input_{model_info['name']}.json")
    output_path = os.path.join(TEMP_DIR, f"output_{model_info['name']}.json")
    db_dir = os.path.join(TEMP_DIR, f"db_{model_info['name']}")
    
    os.makedirs(TEMP_DIR, exist_ok=True)
    with open(input_path, "w") as f:
        json.dump(input_data, f)
    
    if os.path.exists(db_dir):
        shutil.rmtree(db_dir)
    
    # 运行 quality_bench
    print(f"  运行 quality_bench (Retrieval Mode)...")
    kill_children()
    t2 = time.time()
    result = subprocess.run(
        [BINARY, "--input", input_path, "--output", output_path,
         "--db-dir", db_dir, "--mode", "retrieval"],
        capture_output=True, text=True, timeout=600,
    )
    recall_time = time.time() - t2
    
    if result.returncode != 0:
        print(f"  ❌ 失败: {result.stderr[-200:]}")
        return None
    
    with open(output_path) as f:
        out = json.load(f)
    
    # 计算余弦基线
    cos_ndcg = []
    for qi, qv_i in enumerate(qv):
        rel = set(qrels[query_ids[qi]].keys())
        sims = sorted([(i, float(np.dot(qv_i, dv[i]))) for i in range(len(dv))],
                      key=lambda x: -x[1])
        ranked = [doc_ids[i] for i, _ in sims[:10]]
        dcg = sum(1.0 / math.log2(j + 2) for j, d in enumerate(ranked) if d in rel)
        idcg = sum(1.0 / math.log2(j + 2) for j in range(min(10, len(rel))))
        cos_ndcg.append(dcg / idcg if idcg > 0 else 0)
    
    # 清理
    kill_children()
    shutil.rmtree(db_dir, ignore_errors=True)
    
    # 结果
    res = out["results"][0]
    rpt = {
        "model": model_info["name"],
        "model_id": model_info["id"],
        "dim": model_info["dim"],
        "params_m": round(params, 1),
        "load_sec": round(load_time, 1),
        "encode_sec": round(enc_time, 1),
        "recall_sec": round(recall_time, 1),
        "total_sec": round(time.time() - t0, 1),
        "ndcg@10": round(res["ndcg_10"]["mean"], 4),
        "mrr": round(res["mrr"]["mean"], 4),
        "r@1": round(res["recall_1"]["mean"], 4),
        "r@5": round(res["recall_5"]["mean"], 4),
        "r@10": round(res["recall_10"]["mean"], 4),
        "失忆率(1-R@1)": round(1 - res["recall_1"]["mean"], 4),
        "幻听率(≈1-R@5)": round(1 - res["recall_5"]["mean"], 4),
        "cos_ndcg@10": round(float(np.mean(cos_ndcg)), 4),
        "ratio": round(res["ndcg_10"]["mean"] / np.mean(cos_ndcg) * 100, 1) if np.mean(cos_ndcg) > 0 else 0,
        "avg_latency_us": round(res.get("avg_recall_latency_us", 0)),
    }
    
    return rpt

def main():
    print("╔══════════════════════════════════════════════════════╗")
    print("║   MemHop v0.9.0 — 编码器对比 Benchmark              ║")
    print("╠══════════════════════════════════════════════════════╣")
    for m in MODELS:
        print(f"║  {m['name']:<20s} {m['dim']}d                       ║")
    print("╚══════════════════════════════════════════════════════╝")
    
    kill_children()
    
    # 加载数据 (一次性)
    print("\n[1/4] 加载测试数据...")
    doc_ids, query_ids, corpus, queries, qrels = load_data()
    print(f"  数据就绪: {len(doc_ids)} docs, {len(query_ids)} queries")
    
    # 依次测试每个模型
    results = []
    for i, model_info in enumerate(MODELS):
        print(f"\n[{i+2}/4] 测试 {model_info['name']}...")
        rpt = run_model(model_info, doc_ids, query_ids, corpus, queries, qrels)
        if rpt:
            results.append(rpt)
        kill_children()
        time.sleep(2)  # 等系统释放内存
    
    # 保存报告
    os.makedirs(REPORT_DIR, exist_ok=True)
    timestamp = time.strftime("%Y%m%d_%H%M%S")
    report_path = os.path.join(REPORT_DIR, f"encoder_comparison_{timestamp}.json")
    
    with open(report_path, "w") as f:
        json.dump({
            "benchmark": "MemHop v0.9.0 Encoder Comparison",
            "timestamp": timestamp,
            "docs": len(doc_ids),
            "queries": len(query_ids),
            "results": results,
        }, f, indent=2, ensure_ascii=False)
    
    # 打印摘要
    print(f"\n{'='*90}")
    print(f"  结果已保存: {report_path}")
    print(f"{'='*90}")
    print(f"{'模型':<16s} {'参数':>5s} {'NDCG':>7s} {'R@1':>7s} {'R@5':>7s} {'失忆':>6s} {'幻听':>6s} {'Cos':>7s} {'延迟':>7s}")
    print("-" * 90)
    for r in results:
        print(f"{r['model']:<16s} {r['params_m']:4.0f}M "
              f"{r['ndcg@10']:6.4f} {r['r@1']:6.4f} {r['r@5']:6.4f} "
              f"{r['失忆率(1-R@1)']:5.1%} {r['幻听率(≈1-R@5)']:5.1%} "
              f"{r['cos_ndcg@10']:6.4f} {r['avg_latency_us']:6.0f}µs")
    print("-" * 90)
    print(f"  agentmemory: LongMemEval-S R@5=95.2% | 失忆率 ≈ 4.8%")
    print(f"  ⚠️ 本测试在 BEIR nfcorpus，与 LongMemEval-S 不可直接对比")
    print(f"  仅供参考编码器相对性能")
    
    # 清临时文件
    shutil.rmtree(TEMP_DIR, ignore_errors=True)
    kill_children()
    print("\n✅ 完成")

if __name__ == "__main__":
    main()
