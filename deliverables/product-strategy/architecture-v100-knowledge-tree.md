# MemHop v0.10.0 — 知识树 + BM25 + Cross-Encoder + Dream Crystallizer

**日期**: 2026-05-29
**当前**: v0.9.1 (turn 级存储, LongMemEval-S R@5=60%)
**目标**: v0.10.0 (R@5 > 90%, 知识树投产)

---

## 架构总览

```
┌─────────────────────────────────────────────────────────┐
│                   MeowAgent                              │
│  Thalamus: "JWT 过期怎么修？"                              │
│     │                                                    │
│     ├─ Brain::recall  ──→ 记忆 (上次改过..., 那个bug...)   │
│     └─ Shelf::search   ──→ 知识 (auth.rs代码, RFC 7519)   │
│     │                                                    │
│     └─ 融合 → LLM prompt                                  │
└─────────────────────────────────────────────────────────┘
         │                          │
    ┌────▼────┐              ┌──────▼──────┐
    │  Brain  │              │    Shelf    │
    │ (海马体) │              │  (新皮层)     │
    │         │              │             │
    │ HNSW    │              │ HNSW (共享)  │
    │ Hopfield│              │ BM25 全文    │
    │ RRF     │              │ Chunk 切片   │
    │ Dream   │              │ Tree 隔离    │
    └─────────┘              └─────────────┘
         │                          │
         └────────┬─────────────────┘
                  │
          ┌───────▼────────┐
          │  EncoderPool   │
          │  BGE-M3 ×1     │
          └────────────────┘
```

---

## Phase 1: SparseIndex → BM25 升级

**问题**: 当前 ngram sparse index 无 IDF 加权，关键词区分度弱

**改**: BM25 替代 ngram，IDF 加权 + 文档长度归一化

```
当前 ngram: "degree" 在所有文档都出现 → 权重不变
BM25:       "degree" 在 5/25000 条出现 → IDF=log(25000/5)≈8.5 → 高权重
            "the" 在 24000/25000 出现 → IDF=log(25000/24000)≈0.04 → 忽略
```

API 不变——`SparseIndex` 内部替换为 BM25，`search_weighted` 接口保持一致。

| 文件 | 变更 |
|------|------|
| `memhop/src/index.rs` | BM25 实现：term_freq × IDF / (k1 × (1-b + b × doc_len/avg_len) + term_freq) |
| `memhop/src/brain.rs` | RRF 融合 BM25 score 替代 dense rank |

**目标**: LongMemEval-S R@5 60% → 80%

---

## Phase 2: BGE-Reranker Cross-Encoder 精排

**问题**: bi-encoder 余弦无法区分 "JWT 签名错误" 和 "签名格式不对" 的细微差异

**改**: 对 HNSW top-20 候选走 Cross-Encoder 重排

```
recall("JWT 过期怎么修？"):
  L1 HNSW → 20 candidates (cosine top-20)
  L2 Cross-Encoder → 20 对 (query, doc) 逐对打分 → 重排 top-10
```

| 文件 | 变更 |
|------|------|
| `memhop/src/encoder/reranker.rs` | safetensors → ONNX 转换脚本，CPU 推理 |
| `memhop/src/brain.rs` | `RecallRequest.use_reranker` 已支持，接线 |
| `models/` | 下载 `BAAI/bge-reranker-v2-m3` → ONNX |

**目标**: LongMemEval-S R@5 80% → 90%

---

## Phase 3: Dream Crystallizer — Turn 自动合并

**问题**: 修一个 bug 可能涉及 20 轮对话，25K 条 turn 信噪比低

**改**: Dream NREM 语义聚类 + Schema 生成

```
Dream NREM (store 满 1000 条或定时触发):

  语义聚类: 余弦 > 0.85 的 turn → 自动成组
  ↓
  合并组内 turn → Schema {
    summary: "修 JWT 过期问题, 方案: RSA SHA256",
    source_turns: [T1,T3,T5,T7]
  }
  ↓
  Hebbian 边: 原 turn → Schema 双向增强
```

Dream 后内存结构：
- **active 层**: 最新 1000 条 turn（完整向量 + 原文）
- **schema 层**: 合并后的 Schema（摘要 + turn 指针）
- **archive 层**: 沉默 > 30 天的 turn → 仅保留 Schema + 磁盘原文

| 文件 | 变更 |
|------|------|
| `memhop/src/brain.rs` | Dream NREM 语义聚类 + Schema 生成逻辑 |
| `memhop/src/storage.rs` | Schema 存储格式（summary + turn_ids） |

**目标**: LongMemEval-S R@5 90% → 95%+

---

## Phase 4: Shelf 知识树 — 挂载→索引→检索→注入

### 4.1 挂载流程

```
MeowAgent: memhop.mount_shelf("/projects/memhop", domain="code")

MemHop ShelfManager:
  [1] 扫描目录
      code/ → tree-sitter 解析 → AST chunk 切片
      doc/  → markdown heading → paragraph chunk 切片
      paper/→ PDF section → text chunk 切片

  [2] 每个 chunk:
      text → BGE-M3 → vector → HNSW (Shelf 独立索引)
      text → BM25 分词 → SparseIndex (Shelf 独立索引)

  [3] 完成后返回 shelf_id
      状态: mount_shelf → "indexing" → "ready" (异步)
```

### 4.2 检索注入

```
MeowAgent recall 自动双搜:

  Brain::recall("JWT 过期")     → 记忆: "上次改 auth.rs 时..."
  Shelf::search("JWT 过期")     → 知识: "auth.rs:45 JWT_EXPIRY = 3600"
  
  融合:
  prompt = f"""
  相关记忆: {memories}
  项目代码: {code_snippets}
  问题: JWT 过期怎么修？
  """
```

### 4.3 Tree 隔离

```rust
// 每个 shelf 独立 HNSW + BM25，召回时可选跨 tree 搜索
ShelfManager {
    trees: HashMap<String, ShelfTree>,  // "code.memhop" → ShelfTree
}

ShelfTree {
    hnsw: HnswIndex,
    bm25: SparseIndex,
    chunks: Vec<Chunk>,  // text + location
}

fn search(query: &str, tree: &str, k: usize) -> Vec<Chunk> {
    // 默认限在指定 tree，tree="*" 时跨 tree
}
```

| 新增文件 | 说明 |
|----------|------|
| `memhop/src/shelf/scanner.rs` | 目录扫描 + tree-sitter AST 切片 |
| `memhop/src/shelf/chunker.rs` | 语义边界切片（code/doc/paper） |
| `memhop/src/shelf/tree.rs` | Tree 隔离 + 跨树搜索 |

---

## Phase 5: 统一 Benchmark

`benchmarks/run_all.py` 重构为五个维度：

| 维度 | 测试 | 路径 |
|------|------|------|
| 记忆力 | LongMemEval-S | Brain::recall (turn-level) |
| 知识检索 | nfcorpus + SciFact | Shelf::search (HNSW + BM25) |
| 代码检索 | CodeSearchNet | Shelf::search (code chunk) |
| 延迟 | 1K/10K/100K | store + recall P50/P99 |
| Dream 效果 | 自建多轮对话 | Dream 前后 R@5 对比 |

---

## 性能目标

| 指标 | v0.9.1 | v0.10.0 目标 |
|------|--------|-------------|
| LongMemEval-S R@5 | 60% | > 90% |
| 失忆率 | 66% | < 10% |
| recall latency (1M turns) | < 10ms | < 1ms (HNSW) |
| store latency (per turn) | ~15ms | < 5ms (batch write) |
| Cross-Encoder latency (top-20) | — | < 100ms |
| Shelf mount (10K files) | — | < 60s |

---

## 不变

- MCP-only 接口
- 双模式 (Retrieval / Associative)
- 编码器策略 (api > ONNX > ngram)
- turn 级存储
- EncoderPool 单份 BGE-M3
