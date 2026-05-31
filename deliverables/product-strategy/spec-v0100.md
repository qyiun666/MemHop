# Spec: MemHop v0.10.0 — 知识树 + BM25 + Cross-Encoder + Dream Crystallizer

## 需求概述

v0.10.0 是 MemHop 从"能工作"到"能投产"的关键版本。当前 v0.9.1 (LongMemEval-S R@5=60%, 失忆率 66%) 在稀疏检索和精排两个维度存在明显短板。本版本通过 5 个 Phase 的增量改进，目标是将 LongMemEval-S R@5 提升至 90%+，并为知识库检索提供生产级能力。

核心思路：BM25 替代 ngram 提升关键词区分度 → Cross-Encoder 精排消除语义混淆 → Dream Crystallizer 自动合并多轮对话 → Shelf 知识树支持代码/文档检索 → 统一 Benchmark 量化全维度。

## 功能清单

| # | 功能 | Phase | 优先级 | 预期效果 |
|---|------|-------|--------|---------|
| 1 | BM25 稀疏检索（IDF 加权 + 文档长度归一化） | P1 | P0 | LongMemEval-S R@5: 60%→80% |
| 2 | RRF rank-based → BM25 score-based 融合 | P1 | P0 | 同上（融合算法升级） |
| 3 | BGE-Reranker 常驻组件化（消除每次重建开销） | P2 | P1 | R@5: 80%→90%, 精排延迟 <100ms |
| 4 | Dream Crystallizer: Hebbian 边（turn → Schema 双向） | P3 | P1 | Schema 质量提升，失忆率下降 |
| 5 | Dream Crystallizer: 三层记忆归档（active/schema/archive） | P3 | P2 | 存储效率，长期记忆保真 |
| 6 | Shelf 模块化拆分（单文件→目录） | P4 | P1 | 架构清晰，支持后续扩展 |
| 7 | ShelfTree BM25 全文索引 | P4 | P1 | 代码/文档检索精度 |
| 8 | Shelf Scanner + Semantic Chunker | P4 | P2 | AST 解析代码切片，语义边界切片 |
| 9 | 统一 Benchmark 五维度 (记忆力/知识/代码/延迟/Dream) | P5 | P2 | 持续量化跟踪 |

## 技术约束

- **零依赖默认模式不可破**：BM25 不引入新依赖，Cross-Encoder 在 `feature = "onnx"` gate 内
- **BM25 新增 `bm25_search()` 方法，不替换现有的 `search_weighted()`**：保留 `search_weighted()` 用于向后兼容，`bm25_search()` 新增 BM25 实现。Brain 使用新方法。旧代码路径不受影响。
  - 理由：ngram-weighted 搜索在某些场景仍有价值（如短文档），不删除现有能力。此决策与架构文档 Phase 1 的"API 不变"表述不一致——以本 spec 为准，因为新增方法比替换内部实现更安全。
- **Reranker 常驻化采用 `Option<Reranker>`**：Reranker 本身内部已有 `Mutex<Session>`，外层不需要再加锁。`BrainConfig` 新增 `reranker_model_path: Option<String>` 字段。
  - 理由：减少锁竞争，符合 Rust 惯用法，配置化加载路径。
- **SchemaExtra 不新增 `turn_ids` 字段**：复用 `source_episodes` 存储 turn schema 的来源 turn IDs。新增 `turn_ids` 会增加序列化开销且破坏向后兼容。
  - 理由：最小化数据模型变更，减少反序列化兼容问题。
- **Hebbian 边使用 `AssociationKind::Hierarchical`**：不新增 `AssociationKind::Hebbian` 变体，避免 bincode 序列化后向兼容问题。以 `weight = 2.0`（常规边的两倍）表达 Hebbian 增强语义。
  - 理由：bincode 序列化 enum 不保证 `#[serde(other)]` 兼容，新增变体会破坏已序列化的 LMDB 数据。
- **Shelf 模块化：ShelfTree 作为新的核心抽象**：`ShelfTree` 封装 HNSW + BM25 + chunks，`Shelf` 持有一个 `ShelfTree`。后续跨 tree 搜索会用到这个抽象。
  - 理由：每一轮重构都为下一轮打基础，避免重复改动。
- **BM25 融合使用 min-max 归一化 + 线性加权**：BM25 score 先 min-max 归一化到 [0,1] 区间，再与 HNSW cosine similarity 线性加权融合。权重 `α=0.4 (BM25), β=0.6 (HNSW)`。此值可通过实测调优。
  - 理由：BM25 score 无上界，直接加权会被 BM25 主导；min-max 归一化后两类 score 可比较。
- **归档沉默检测基于 `Engram.last_activated`**：沉默 > 30 天 = `now - engram.last_activated > 30 * 24 * 3600 * 1000` ms。在 NREM-1 vitality decay 循环中 piggyback 执行，不单独遍历。
  - 理由：避免额外全量扫描，复用现有衰减循环。
- **`turns_archived` 替换 `archived_count` 语义范畴**：`DreamReport.archived_count` 保留（记为"被归档的非对话 Engram 数"），新增 `turns_archived` 专记被归档的对话 turn 数。
  - 理由：两个概念统计口径不同，不能混淆。
- **`turn_cluster_emergence()` 保持返回类型不变**：从返回的 `SchemaExtra.source_episodes` 中提取 turn → schema_id 映射。不在 schema.rs 层面做返回类型变更。
  - 理由：最小化 API 变更，减少跨文件影响。
- **Benchmark 五维度在同一次运行中采集所有数据**：避免多次运行的数据漂移，确保结果可复现和可对比。
- **brain.rs 分区域修改**：brain.rs 当前 ~2469 行，每个子任务修改特定区段。实现 agent 应仅读取目标区段代码：
  - 子任务1: 行 89-112 (struct fields)，202-255 (Brain::open)，400-420 (perceive add)，915-1000 (recall_retrieval RRF)
  - 子任务2: 行 89-112 (struct fields)，202-255 (Brain::open)，915-1000 (recall_retrieval reranker)
  - 子任务3: 行 1155-1186 (nrem_turn_crystallizer)
  - 子任务4: 行 1155-1186 + 1256-1320 (dream 流程) + 1420-1540 (nrem_vitality_decay)

## 子任务拆分 (G-04 compliant, ≤5 files each)

---

## 子任务1: BM25 索引 + Score-based RRF 融合

- **涉及文件**: `memhop/src/index.rs`, `memhop/src/brain.rs`
- **brain.rs 区域**: 行 89-112 (struct fields), 202-255 (Brain::open), 400-420 (perceive 添加 doc_len), 915-1000 (recall_retrieval BM25 融合)
- **输入**: 无
- **产出**:
  1. `index.rs`: 新增 `doc_len: HashMap<String, usize>` 追踪，`SparseIndex::add()` 时记录文本长度, `remove()` 时删除, 新增 `avg_doc_len()` 方法
  2. `index.rs`: BM25 参数常量 `K1 = 1.2`, `B = 0.75`
  3. `index.rs`: 新增 `bm25_search()` 方法:
     - score = term_freq × IDF / (k1 × (1-b + b × doc_len/avg_len) + term_freq)
     - 空索引(forward 为空)或 avg_doc_len == 0 时回退到 `search_weighted()`，不抛 panic
     - 单文档时 avg_doc_len = doc_len，BM25 公式正常计算
  4. `brain.rs`: `recall_retrieval()` 中替换 RRF rank-based → BM25 score-based 融合:
     - BM25 score 先 min-max 归一化到 [0, 1]（min = 最小值 max = 最大值，max=min 时取 0.5）
     - 与 HNSW cosine similarity 线性加权: `score = 0.4 * bm25_norm + 0.6 * cos_sim`
     - 保留降序排序，truncate 到 req.limit
  5. `brain.rs`: `perceive()` 中传递文本长度到 `sparse_index.add()`
- **验收**:
  - `cargo test --lib index` — 所有现有 SparseIndex 测试通过 + 新增 BM25 测试（含空索引、单文档边界情况）
  - `cargo build --workspace` — 编译通过（默认 features）
- **预估工作量**: 中等

---

## 子任务2: Cross-Encoder Reranker 常驻组件化

- **涉及文件**: `memhop/src/brain.rs`, `memhop/src/types.rs`, `memhop/src/encoder/reranker.rs`
- **brain.rs 区域**: 行 89-112 (struct fields), 202-255 (Brain::open), 915-1000 (recall_retrieval reranker)
- **输入**: 无（独立）
- **产出**:
  1. `types.rs`: `BrainConfig` 新增 `pub reranker_model_path: Option<String>` 字段（含 `#[serde(default)]`），默认值为 `Some("models/bge-reranker-v2-m3".into())`（保留当前硬编码行为）
  2. `brain.rs`: `Brain` struct 新增 `reranker: Option<Reranker>` 字段（feature-gated `#[cfg(feature = "onnx")]`）
  3. `brain.rs`: `Brain::open()` 中初始化 Reranker：读取 `config.reranker_model_path`，若为 `Some` 则 `Reranker::from_path()`，失败时输出 warning 并设 `self.reranker = None`
  4. `brain.rs`: `recall_retrieval()` 中 `use_reranker` 分支改为使用 `self.reranker` 而非每次调用 `Reranker::from_path()`
  5. `brain.rs`: 如果 `self.reranker.is_none()` 且 `use_reranker=true`，输出 warning 日志而非静默跳过（warning 仅一次，避免刷屏）
- **验收**:
  - `cargo build --workspace` — 编译通过（no-default-features, 即无 onnx）
  - `cargo build --workspace --features onnx` — 编译通过（有 onnx）
- **预估工作量**: 小

---

## 子任务3: Dream Crystallizer — Hebbian 边增强

- **涉及文件**: `memhop/src/brain.rs`
- **brain.rs 区域**: 行 1155-1186 (nrem_turn_crystallizer)
- **输入**: 无（独立）
- **产出**:
  1. `brain.rs`: `nrem_turn_crystallizer()` 中，对 `turn_cluster_emergence()` 返回的每个 Schema：
     - 从 `SchemaExtra.source_episodes` 提取原 turn IDs（`source_episodes` 字段已存储来源 turn 的 ID 列表）
     - 使用 `graph.add_bidirectional_edge(&self.storage, turn_id, schema_id, 2.0, AssociationKind::Hierarchical, now)` 创建双向增强边
     - `weight = 2.0`：Heebian 增强语义通过高权重表达，`AssociationKind::Hierarchical` 表示 turn→Schema 的层级关系
- **验收**:
  - `cargo build --workspace` — 编译通过
  - 手动验证：store 5+ 语义相似的 turns，触发 Dream，验证 `list_schemas()` 返回的 Schema 存在关联图边
- **预估工作量**: 中等

---

## 子任务4: Dream Crystallizer — 三层归档 (active/schema/archive)

- **涉及文件**: `memhop/src/brain.rs`, `memhop/src/types.rs`
- **brain.rs 区域**: 行 1155-1186 (nrem_turn_crystallizer), 1256-1320 (dream 流程), 1420-1540 (nrem_vitality_decay)
- **输入**: 建议在子任务3之后执行（archive 逻辑不依赖 Hebbian 边，但共享 nrem_turn_crystallizer 区域）
- **产出**:
  1. `brain.rs`: `nrem_vitality_decay()` 中 piggyback 添加沉默检测归档逻辑：
     - 沉默 > 30 天检测：`now - engram.last_activated > 30 * 24 * 3600 * 1000`
     - 当前遍历 L2 engrams 的循环中，检查该条件
     - 满足条件的 engram 设置 `engram.is_archived = true`，写入 LMDB
     - 记录到 `report.turns_archived`（仅计数对话类型 engrams，即 `EngramKind::Episode` 且有 `turn_id` 的 engram）
     - 此归档不删除原 turn 数据，仅修改 `is_archived` 标记（原文本、向量保持不动，仅 recall 时降权）
  2. `brain.rs`: `recall_retrieval()` 中，对 `is_archived=true` 的 engram，最终 score 乘以 penalty `0.3`
  3. `types.rs`: `DreamReport` 新增字段 `turns_archived: usize`（与现有 `archived_count` 区分：前者特指对话 turn 归档数，后者指非对话 engram 归档数）
- **验收**:
  - `cargo build --workspace` — 编译通过
  - 手动验证：store 大量 turns，强制 Dream，验证 `turns_archived > 0`，recall 中 archived 结果排在非 archived 之后
- **预估工作量**: 中等

---

## 子任务5: Shelf 模块化拆分 + ShelfTree BM25

- **涉及文件**: `memhop/src/shelf/mod.rs`, `memhop/src/shelf/tree.rs`, `memhop/src/lib.rs`
- **输入**: 无（独立）
- **产出**:
  1. `memhop/src/shelf.rs` → 移动到 `memhop/src/shelf/mod.rs`（内容不变，仅路径移动）
  2. `memhop/src/shelf/tree.rs`: 新建 `ShelfTree` struct，包含 `HnswIndex` + `SparseIndex`（BM25）+ `texts: HashMap<u64, String>` + `chunk_meta: HashMap<u64, ChunkMeta>`
     - `ShelfTree.SparseIndex` 在 `mount()` 过程中通过 `NgramEncoder` 对 chunk 文本分词后填充
     - `ShelfTree::search()`: 同时做 HNSW 余弦检索 + `SparseIndex.bm25_search()`（调用 index.rs 新增的 BM25 方法），结果 RRF 融合
  3. `memhop/src/shelf/mod.rs`: `Shelf` struct 改用 `ShelfTree` 替代原来的裸 `HnswIndex` + `chunk_meta` + `texts` 三个独立字段
     - 保留现有的四个 chunking 函数（`chunk_code_file`, `chunk_by_heading`, `chunk_by_paragraph`, `chunk_by_tokens`）在 `mod.rs` 中作为私有辅助函数（子任务6将提取到 `chunker.rs`）
  4. `memhop/src/lib.rs`: 模块声明 `pub mod shelf;` 不变（Rust 自动找 `shelf/mod.rs`）
- **验收**:
  - `cargo build --workspace` — 编译通过
  - 所有引用 `memhop::shelf::ShelfManager` 的代码编译无误（brain.rs, MCP server）
- **预估工作量**: 中等（有重构风险，需确认所有调用方）

---

## 子任务6: Shelf Scanner + Semantic Chunker

- **涉及文件**: `memhop/src/shelf/scanner.rs`, `memhop/src/shelf/chunker.rs`, `memhop/src/shelf/mod.rs`
- **输入**: 子任务5（需要 `shelf/` 目录结构已就位）
- **产出**:
  1. `memhop/src/shelf/scanner.rs`: 目录扫描器，递归遍历目录，按 domain 过滤文件（`extensions` 列表），返回 `Vec<ScannedFile>`（自定义 struct: `ScannedFile { path: String, text: String, domain: ShelfDomain }`）
  2. `memhop/src/shelf/chunker.rs`: Semantic boundary chunker，从 `mod.rs` 提取 chunking 逻辑并增强:
     - `chunk_code(path, text)`: 按空行/连续代码段切片（每段 50-200 行），后退方案是整个文件一个 chunk。此版本不集成 tree-sitter（AST 解析推迟到后续版本）
     - `chunk_doc(text)`: 按 markdown heading 切片（复用现有 `chunk_by_heading` 逻辑）
     - `chunk_paper(text)`: 按 section + paragraph 切片（复用现有 `chunk_by_paragraph` 逻辑）
  3. `memhop/src/shelf/mod.rs`: `ShelfManager::mount()` 中集成 scanner → chunker → encode → index 流程:
     - 调用 `scanner::scan_directory()` 获取文件列表
     - 对每个文件调用 `chunker::chunk_*()` 获取 chunks
     - 编码后在每个 chunk 上调用 `ShelfTree::add_chunk()` 填入 `HnswIndex` + `SparseIndex`
     - 移除现有的内嵌 `scan_and_chunk()` 函数及其内部的四个 chunking 私有函数（转移到 chunker.rs）
  4. `memhop/src/shelf/mod.rs`: 添加 `pub mod scanner; pub mod chunker;` 声明
- **验收**:
  - `cargo build --workspace` — 编译通过
  - 手动验证：mount 一个包含 .rs 文件的目录到 Code domain，验证 chunk 数量 > 文件数（说明代码被切片了）
- **预估工作量**: 中等

---

## 子任务7: 统一 Benchmark 五维度

- **涉及文件**: `benchmarks/run_all.py`
- **输入**: 无（独立于 Rust 代码变更）
- **产出**:
  1. 重构 `run_all.py` 为五维度结构（数据集路径默认从 `benchmarks/data/` 加载，不存在则跳过该维度并输出 warning）:
     - 记忆力 — LongMemEval-S（复用现有逻辑，数据集路径 `benchmarks/data/longmemeval/`）
     - 知识检索 — nfcorpus（已有）+ SciFact（路径 `benchmarks/data/scifact/`，不可用时跳过）
     - 代码检索 — CodeSearchNet（路径 `benchmarks/data/codesearchnet/`，暂为 scaffold，不可用时跳过）
     - 延迟 — 扩展 scale 到 1K/10K/100K/1M，增加 P50/P99 分位
     - Dream 效果 — 多轮对话 Dream 前后 R@5 对比（自建测试数据，嵌入 Python 脚本）
  2. 输出统一报告 JSON（含版本号 v0.10.0）
  3. 新增竞品对比表（agentmemory, mem0, MemHop v0.10.0，数据从 benchmark 结果收集）
- **验收**:
  - `python3 benchmarks/run_all.py 50` — 小样本运行无报错（缺失数据集的维度跳过）
  - 报告 JSON 包含至少 3 个维度数据
  - 延迟分位数值在合理范围（<100ms for recall P50 at 100K）
- **预估工作量**: 中等

---

## 依赖关系图

```
无依赖，可并发:          ┌─ 子任务1 (BM25)
                         │
                         ├─ 子任务2 (Reranker)
                         │
                         ├─ 子任务3 (Hebbian)
                         │
                         └─ 子任务5 (Shelf 模块化)

子任务5 完成后方可执行:   └─ 子任务6 (Scanner+Chunker)

建议子任务3后执行:        └─ 子任务4 (Archive)

完全独立:                └─ 子任务7 (Benchmark)
```

**推荐执行顺序**（考虑 brain.rs 文件冲突最小化）：
1. 子任务1 (BM25) → 修改 index.rs + brain.rs RRF 段
2. 子任务2 (Reranker) → 修改 brain.rs reranker 段（与子任务1 不冲突）
3. 子任务5 (Shelf 模块化) → 独立文件组
4. 子任务6 (Scanner+Chunker) → 依赖子任务5
5. 子任务3 (Hebbian) → 修改 brain.rs dream 段
6. 子任务4 (Archive) → 修改 brain.rs archive 段（与子任务3 相邻但独立）
7. 子任务7 (Benchmark) → 最后执行，验证整体效果

## 关键决策

1. **`AssociationKind::Hebbian` 不新增**：使用 `Hierarchical` 替代，`weight=2.0` 表达增强语义。避免 bincode 序列化后向兼容问题。
   - 理由：新增 enum 变体破坏已序列化 LMDB 数据。

2. **Reranker 采用 `Option<Reranker>`**：不嵌套 `Arc<Mutex<>>`。Reranker 内部已有 `Mutex<Session>`。
   - 理由：减少锁竞争，符合 Rust 惯用法。

3. **`SchemaExtra` 不新增字段**：`source_episodes` 已存储 turn → Schema 映射信息。
   - 理由：最小化数据模型变更，避免反序列化兼容问题。

4. **`ShelfTree` 作为核心抽象**：封装 HNSW + BM25 + chunks，为后续跨 tree 搜索铺垫。
   - 理由：每一轮重构为下一轮打基础，避免重复改动。

5. **tree-sitter 推迟**：Phase 4 不集成 tree-sitter AST 解析，代码 chunking 使用空行/函数边界启发式方法。
   - 理由：tree-sitter 作为外部 C 库引入复杂度过高，推迟至后续版本。

6. **Benchmark 数据集 gracefully degrade**：缺失数据集时跳过对应维度并输出 warning，不阻塞其他维度。
   - 理由：持续集成环境可能不全量数据，保证 benchmark 脚本的鲁棒性。

## 下一步

子任务1: **BM25 索引 + Score-based RRF 融合**, 从 `memhop/src/index.rs` 开始，添加 BM25 实现后，在 `memhop/src/brain.rs` 的 `recall_retrieval()` 方法中替换融合逻辑。
