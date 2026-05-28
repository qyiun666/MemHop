# MemHop 第一梯队架构重设计方案

**日期**：2026-05-28
**类型**：架构设计
**触发数据**：T2Retrieval NDCG 0.36 (FAISS 0.99)，recall O(N) P50=104ms@10K
**前提**：不考虑离线场景、不考虑工作量/风险，纯设计目标驱动

---

## 📌 TL;DR

MemHop 当前在 Hopfield 语义召回（R@10=0.91）和 O(1) 性能两方面掉队。方案：
- **L1 HNSW** 替换 O(N) Hopfield 扫描 → O(log N)，P50 < 1ms
- **L2 Hopfield + EntangleGraph** 保留为关联扩散层 → 差异化能力
- **L3 Cross-Encoder** ONNX reranker → NDCG > 0.95
- **Dream 持续优化** → 长期护城河（独有能力）
- 双模式设计：Retrieval Mode（纯质量）/ Associative Mode（类脑联想）

---

## 🎯 核心结论卡片

| 项目 | 内容 |
|------|------|
| 推荐方案 | 三层检索架构：HNSW → Hopfield 关联扩散 → Cross-Encoder 精排 |
| 优先级 | P0 — 当前质量/性能距第一梯队差两个数量级 |
| 预期影响 | NDCG@10: 0.36→0.96, recall P50@10K: 104ms→<1ms |
| 风险等级 | 中 — 所有组件有成熟开源实现 (hnsw_rs, ort, BGE-Reranker) |

---

## 1. 差距诊断

### 1.1 质量差距（T2Retrieval, BGE-M3）

| 系统 | NDCG@10 | R@10 | 瓶颈点 |
|------|---------|------|--------|
| FAISS-HNSW | 0.99 | 0.99 | — |
| MemHop (v0.8.0) | **0.36** | **0.91** | ranking pipeline |

**根因**：Hopfield 召回已经找到相关文档（R@10=0.91），但后续 pipeline 把语义排序摧毁了：

```
Hopfield cosine → competitive_spread → emotional_alignment → ngram_overlap
     ✅               graph 增强           💀 噪声排序          💀 关键词覆盖
```

- `emotional_alignment`（brain.rs:716）：benchmark 中所有文档 emotional_state=default，valence/arousal 恒定，排序退化为随机
- `ngram_overlap`（brain.rs:732）：字节三元组重叠排序，T2Retrieval 是语义检索，字面重叠与相关性弱相关

### 1.2 性能差距

| 规模 | MemHop recall P50 | MemHop recall P99 | FAISS-HNSW | 差距 |
|------|-------------------|-------------------|------------|------|
| 1K | 5.9ms | 13.6ms | <1ms | 6x |
| 5K | 46ms | 90ms | <1ms | 46x |
| 10K | 104ms | 541ms | <1ms | 100x+ |

**根因**：`Hopfield::recall_topk`（hopfield.rs:228）对所有 N 个模式做 dot product → O(N·d)，rayon 并行只能线性加速，不改变复杂度。

---

## 2. 三层检索架构

```
┌─────────────────────────────────────────────────────┐
│                    Query                             │
│              (BGE-M3 1024-dim)                       │
└────────────────────┬────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────┐
│  L1: HNSW ANN Index                    O(log N)     │
│  ─────────────────────────────────────              │
│  • hnsw_rs, M=16, ef_construction=200               │
│  • 返回 top-200 候选                                 │
│  • 延迟：P99 < 1ms @ 1M                             │
│  • 内存：1M × 1024 × f32 ≈ 4GB（可 BQ 压到 128MB）  │
└────────────────────┬────────────────────────────────┘
                     │ 200 candidates
┌────────────────────▼────────────────────────────────┐
│  L2: Hopfield + EntangleGraph           O(200)      │
│  ─────────────────────────────────────              │
│  • Hopfield::recall_among_raw 精排                    │
│  • competitive_spread 沿 EntangleGraph 扩散          │
│  • 产出：ranked seeds + graph 增强关联               │
│  • 延迟：~0.1ms（仅 200 个候选）                      │
└────────────────────┬────────────────────────────────┘
                     │ top-20
┌────────────────────▼────────────────────────────────┐
│  L3: Cross-Encoder Reranker            O(20)        │
│  ─────────────────────────────────────              │
│  • ONNX BGE-Reranker-v2-m3                          │
│  • 逐对 (query, doc) 打分                            │
│  • 延迟：~50ms（20 对，ONNX）                         │
│  • 目标：NDCG@10 > 0.95                              │
└────────────────────┬────────────────────────────────┘
                     │ final ranked top-K
                     ▼
```

### 2.1 对比：纯 FAISS 方案

FAISS-HNSW 只做了 L1 + 余弦排序。这能达到 NDCG 0.99，所以我们**最保守方案只需 L1**。但 L2+L3 是 MemHop 的差异化护城河：

- **L2 EntangleGraph**：FAISS 做不到的"找相关联记忆"——比如查询"Python 性能优化"，Hopfield 扩散能找到"GIL 机制"、"async/await 实战"、"Cython 编译"，即使它们的向量距离不近，但在记忆图中有关联边
- **L3 Cross-Encoder**：FAISS 是纯 bi-encoder，cross-encoder 精排是信息检索 SOTA 的标配

---

## 3. 双模式设计

同一套引擎，两种运行模式：

### Retrieval Mode（检索场景 / Benchmark）
```
HNSW → cosine sort → [可选 Cross-Encoder] → 返回
```
- 纯质量优化，无情绪/ngram 干扰
- 用于 C-MTEB/BEIR benchmark、RAG 检索、文档搜索

### Associative Mode（类脑记忆场景）
```
HNSW → Hopfield spread → emotional boost → ngram boost → 返回
```
- 情绪对齐、ngram 增强作为 **boost 而非覆盖排序**
- 用于 MeowAgent 个性化记忆、日记检索、叙事生成

### 切换方式
```rust
pub enum RecallMode {
    Retrieval,    // 纯语义检索
    Associative,  // 类脑联想
}
```
`RecallRequest` 新增 `mode: RecallMode` 字段，默认 `Retrieval`。

---

## 4. O(1) 达成路径

### 4.1 HNSW 替代 Hopfield 全量扫描

当前 `Hopfield::recall_topk`：O(N·d) 全扫描 → 替换为 HNSW graph search

```rust
// 新增：HnswIndex 封装
pub struct HnswIndex {
    graph: Hnsw<f32, DistCosine>,  // hnsw_rs crate
    id_to_idx: HashMap<String, usize>,
}

impl HnswIndex {
    pub fn search(&self, query: &[f32], k: usize) -> Vec<(String, f32)>;
    pub fn insert(&mut self, id: &str, vector: &[f32]);
    pub fn remove(&mut self, id: &str);
}
```

HNSW 参数：
- `M=16`：每层每个节点的最大连接数
- `ef_construction=200`：构建时的搜索宽度
- `ef_search=100`：查询时的搜索宽度
- 规模：1M vectors, 1024-dim, f32 → ~4GB（可用 BQ 压到 128MB）

### 4.2 可选的 Binary Quantization

对内存敏感场景：
```
BQ encode: sign(v) → 1024 bits = 128 bytes
HNSW distance: Hamming (popcount on XOR)
1M vectors: 128MB (vs f32 的 4GB)
recall 损失: ~2-5% (Qdrant BQ 论文)
```

第一阶段先用 f32 保证质量，BQ 作为 feature-gate 可选开关。

### 4.3 Hopfield 保留场景

Hopfield 不删，保留在 L2：
- **Associative Mode**：HNSW 候选 → Hopfield 关联扩散 → 情感/ngram boost
- **Dream 阶段**：利用 Hopfield 权重矩阵做 pattern consolidation
- **小规模场景**（< 10K）：Hopfield 全量扫描也可接受，保留纯 Hopfield 路径作为无 HNSW fallback

---

## 5. Cross-Encoder 集成

### 5.1 模型选择

BGE-Reranker-v2-m3（BAAI，ONNX 格式）：
- 输入：(query, document) 对
- 输出：relevance score [0, 1]
- 中文+英文多语言
- ONNX 推理延迟：~2-3ms/pair（CPU）

### 5.2 集成方式

```rust
pub struct Reranker {
    session: ort::Session,           // ONNX runtime
    tokenizer: Tokenizer,            // HuggingFace tokenizer
}

impl Reranker {
    pub fn rerank(
        &self,
        query: &str,
        documents: &[(String, String)],  // [(id, text)]
        top_k: usize,
    ) -> Vec<(String, f32)>;
}
```

### 5.3 延迟预算

Cross-encoder 是 latency 瓶颈（~50ms for 20 pairs），设计为可选：
- `RecallMode::Retrieval` + `use_reranker: bool`
- Benchmark 默认开 reranker（追求质量上限）
- 生产环境可按需关闭（追求低延迟）

---

## 6. Dream 持续优化（长期护城河）

这是其他 vector DB 完全不具备的能力。Dream 阶段（异步，不影响在线服务）：

### 6.1 Recall Quality Feedback Loop
```
监控 recall 日志 → 发现低质量模式
  → 调整 HNSW 参数 (ef_search, M)
  → 清理低质量/过期记忆
  → 重新索引
```

### 6.2 Pattern Consolidation
```
NREM 阶段：检测相似/重复记忆 → merge → 减少索引噪音
REM 阶段：跨域关联发现 → 补充 EntangleGraph 边 → 增强 L2 扩散质量
```

### 6.3 Auto-tuning
```
分析 query 分布 → 热点 cluster 加大 ef_search
低频 cluster → 降低搜索精度 → 节省计算
```

---

## 7. 新的 recall 管线（完整流程）

```rust
pub fn recall_v2(&self, req: &RecallRequest) -> Result<RecallResponse> {
    // 0. 获取 query vector（BGE-M3 纯语义，不再 ngram fuse）
    let q_vec = self.encode_query(req);

    // 1. L1: HNSW 候选集
    let hnsw_candidates = self.hnsw_index.search(&q_vec, HNSW_TOP_K); // 200

    match req.mode {
        RecallMode::Retrieval => {
            // 2a. Cosine 排序
            let mut ranked = sort_by_score(hnsw_candidates);
            ranked.truncate(req.limit);

            // 3a. [可选] Cross-Encoder 精排
            if req.use_reranker {
                ranked = self.reranker.rerank(&req.query, &ranked, req.limit);
            }
        }
        RecallMode::Associative => {
            // 2b. Hopfield 关联扩散
            let seeds = hopfield_among(&q_vec, &hnsw_candidates);
            let spread = competitive_spread(&self.graph, &seeds, &self.personality, SPREAD_TOP_K);

            // 3b. 情绪/ngram 作为 boost（不覆盖主排序）
            let mut ranked = spread.activated;
            emotional_boost(&mut ranked, req.emotional_state);   // ×0.9-1.1
            ngram_boost(&mut ranked, &req.query);                // ×0.95-1.05
            sort_by_boosted_score(&mut ranked);
            ranked.truncate(req.limit);
        }
    }

    // 4. 加载 Engram 元信息
    let results = self.load_engrams(&ranked);
    Ok(results)
}
```

关键改动：
- **L1 从 Hopfield::recall_topk → HNSW**：O(N·d) 变 O(log N)
- **Retrieval Mode 删除 emotional_alignment + ngram_overlap 排序**：NDCG 从 0.36 → 0.9+
- **Associative Mode 保留但改为 boost 方式**：不覆盖而是微调语义排序
- **Cross-Encoder 作为可选 L3**：NDCG 从 0.9 → 0.95+

---

## 8. 性能预测

### 8.1 Recall 延迟

| 规模 | 当前 (Hopfield) | 方案后 (HNSW) | 方案后 (+ Reranker) |
|------|----------------|---------------|---------------------|
| 1K | P50 5.9ms | < 0.1ms | ~2ms |
| 10K | P50 104ms | < 0.3ms | ~5ms |
| 100K | — | < 1ms | ~10ms |
| 1M | — | < 3ms | ~50ms |

> HNSW P50 基于 hnsw_rs benchmark (M=16, ef=100, 1024-dim)；Reranker 基于 ONNX 2ms/pair × 20 pairs = 40ms，可进一步优化（批处理、量化模型）

### 8.2 质量预测

| 方案 | NDCG@10 | 备注 |
|------|---------|------|
| 当前 (v0.8.0) | 0.36 | emotional+ngram 摧毁排序 |
| 方案 L1 only (HNSW + cosine) | **0.99** | 对标 FAISS-HNSW |
| 方案 L1 + L3 (HNSW + Cross-Encoder) | **0.995+** | 超越 FAISS |
| 方案 L1 + L2 (HNSW + Hopfield spread) | ~0.92 | graph 扩散引入少量噪声但有联想价值 |

### 8.3 内存

| 组件 | 1K | 10K | 100K | 1M |
|------|-----|------|-------|-----|
| HNSW graph (f32) | 4MB | 40MB | 400MB | 4GB |
| HNSW graph (BQ) | 128KB | 1.3MB | 13MB | 128MB |
| EntangleGraph | < 1MB | < 10MB | < 100MB | < 1GB |
| Hopfield patterns | 已包含在 HNSW | — | — | — |

---

## 9. 实施阶段

### Phase 1: 修复 ranking（1-2 天）
- 在现有 Brain::recall 中增加 RecallMode
- Retrieval Mode：删除 emotional_alignment + ngram_overlap，保留 cosine 排序
- **目标**：NDCG 0.36 → > 0.9（仅凭删除破坏性排序步骤）
- **风险**：零，纯删除代码路径

### Phase 2: HNSW 集成（1-2 周）
- 引入 `hnsw_rs` crate
- 实现 HnswIndex 封装，与现有 Hopfield API 对齐
- 在 Retrieval Mode 中替换 Hopfield::recall_topk
- **目标**：recall P50@10K 104ms → < 1ms
- **风险**：低，hnsw_rs 是成熟 crate

### Phase 3: Cross-Encoder（1 周）
- 集成 ONNX BGE-Reranker-v2-m3
- 实现 Reranker 封装
- 作为 Retrieval Mode 可选 L3
- **目标**：NDCG > 0.95
- **风险**：中，ONNX 模型管理、tokenizer 集成有工程复杂度

### Phase 4: Dream 优化（2-4 周）
- Recall quality feedback loop
- Pattern consolidation
- Auto-tuning
- **目标**：长期质量自我改进（独有能力）
- **风险**：高（研究性质），但可渐进上线

---

## 10. 不做的事（Non-goals）

- ❌ **不用 LLM 做检索质量**——cross-encoder 已经足够，且更轻量
- ❌ **不删 Hopfield**——保留为 Associative Mode 和 Dream 阶段的核心
- ❌ **不做分布式**——单机第一梯队先达成，分布式是 v0.10+ 的事
- ❌ **不做 GPU batch inference**——ONNX CPU 对单条查询足够，批处理留给未来
- ❌ **不引入外部向量数据库**（Pinecone/Weaviate/Qdrant）——自己实现，保持自主性

---

## ✅ 行动清单

| # | 行动 | 负责方 | 时间窗 |
|---|------|--------|--------|
| 1 | Phase 1: RecallMode 枚举 + Retrieval 路径 | 开发 | 1-2 天 |
| 2 | Phase 2: hnsw_rs 集成 | 开发 | 1-2 周 |
| 3 | Phase 3: BGE-Reranker ONNX 集成 | 开发 | 1 周 |
| 4 | 跑 C-MTEB 全量验证 NDCG | 开发 + 测试 | Phase 1-3 后 |
| 5 | Phase 4: Dream 质量反馈回路 | 开发 | 2-4 周 |

---

## ⚠️ 待确认 / 假设

- 假设 `hnsw_rs` 与当前 Rust toolchain 兼容
- 假设 BGE-Reranker-v2-m3 ONNX 模型在 1024-dim 输入下正常工作
- 假设 HNSW graph 的内存占用在目标硬件可接受范围内（或用 BQ 降级）
- L2 Hopfield spread 的保留价值取决于 MeowAgent 对关联检索的实际需求

---

## 📚 数据来源

- `benchmarks/reports/comparison_t2retrieval_100.json` — T2Retrieval 质量对比
- `/tmp/memhop_latency_1k_5k_10k.json` — 延迟评测
- `memhop/src/brain.rs:575-779` — 当前 recall 管线
- `memhop/src/hopfield.rs:228-254` — Hopfield::recall_topk O(N) 实现
- `memhop/src/activation.rs:41-190` — competitive_spread + emotional_alignment
- `memhop/src/encoder/hybrid.rs:1-58` — HybridEncoder 0.3/0.7 融合权重
- `memhop/src/bin/quality_bench.rs:298-370` — benchmark 调用路径确认

---

> 本报告由产品战略团队 AI 协作生成，重要决策请由产品负责人审定。
