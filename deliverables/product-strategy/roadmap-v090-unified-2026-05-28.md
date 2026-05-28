# MemHop v0.9.0 统一路线图

**日期**：2026-05-28（最终版）
**类型**：路线图
**合并源**：t1-roadmap + first-tier-redesign + meowagent-memhop-integration + agentmemory 对标分析
**前提**：在线优先，不计工作量与风险，目标第一梯队
**接口策略**：**MCP-only**（MeowAgent 不再 Cargo 依赖 MemHop，全机统一 MCP 连接）

---

## 📌 TL;DR

- **版本号**：v0.9.0（ranking + 性能双维度质变 + MCP-only 架构统一）
- **部署模式**：全机单 `memhop-mcp-server` 进程 + 多数据库路径（猫 A → `A/`，猫 B → `B/`）
- **编码器**：单进程天然共享 BGE-M3，不需要 EncoderPool 跨进程同步
- **五大核心变更**：修复 ranking → HNSW O(log N) → API/ONNX 编码器 → Cross-Encoder 精排 → RRF 融合
- **新增能力**：LongMemEval-S benchmark、token 预算管理、隐私过滤、会话多样化
- **对标 agentmemory**：MemHop 是引擎（Hopfield 补全 + Hebbian 图学习 + 逐轮实时记忆），agentmemory 是产品
- **不做**：LLM 不在检索热路径、不替代 CodeGraph、不引入外部向量数据库

---

## 🎯 核心结论卡片

| 项目 | 内容 |
|------|------|
| 目标版本 | v0.9.0 |
| 核心方案 | 单进程 MCP Server + 多数据库路径 + HNSW + RRF 融合 + Cross-Encoder + Dream LLM |
| 接口策略 | **MCP-only**（MeowAgent、Cursor、脚本 统一 MCP 协议，不再 Cargo 依赖） |
| 预期 NDCG@10 | 0.36 → > 0.95 |
| 预期 recall P50@10K | 104ms → < 1ms |
| 多猫内存 | 单进程 1×BGE-M3 (2GB)，多数据库路径隔离 |
| 对标 agentmemory | 引擎层差异化：Hopfield 补全 + Hebbian 图学习 + 逐轮实时；产品层由 MeowAgent 覆盖 |
| 关键风险 | ONNX ort 死锁（已知）、HNSW crate 兼容性（低风险） |

---

## 1. 架构总览

### 1.1 单机部署拓扑

```
┌─────────────────────────────────────────────────────────┐
│            memhop-mcp-server (v0.9.0)                    │
│                    单进程                                │
│  ┌──────────────────────────────────────────────────┐   │
│  │            BGE-M3 ONNX (1×2GB)                    │   │
│  │         单进程天然共享，无需 EncoderPool            │   │
│  │         [api-encoder fallback when available]      │   │
│  └──────────────────────────────────────────────────┘   │
│                          │                               │
│          ┌───────────────┼───────────────┐               │
│          ▼               ▼               ▼               │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐    │
│  │ Brain(cat_a) │ │ Brain(cat_b) │ │ Brain(shared)│    │
│  │ LMDB: A/     │ │ LMDB: B/     │ │ LMDB: shared/│    │
│  │ HNSW: 独立   │ │ HNSW: 独立   │ │ HNSW: 独立   │    │
│  │ Graph: 独立  │ │ Graph: 独立  │ │ Graph: 独立  │    │
│  └──────────────┘ └──────────────┘ └──────────────┘    │
│                                                          │
│  Dream Scheduler: 单调度器，轮询所有 Brain                   │
└─────────────────────────────────────────────────────────┘
         ▲         ▲         ▲          ▲
         │ MCP     │ MCP     │ MCP      │ MCP
    ┌────┴────┐ ┌──┴───┐ ┌──┴───┐ ┌───┴─────┐
    │ 猫 A    │ │ 猫 B │ │Cursor│ │ 任意 MCP │
    │MeowAgent│ │MeowA.│ │      │ │ 客户端   │
    └─────────┘ └──────┘ └──────┘ └─────────┘
```

### 1.2 关键设计决策

| 决策 | 理由 |
|------|------|
| MCP-only，MeowAgent 不再 Cargo 依赖 | 版本完全解耦；单进程内存可控；任何 MCP 客户端可接入 |
| 单进程多数据库路径（猫 A→`A/`，猫 B→`B/`） | 猫之间完全隔离，不需要 tree_id 做软件边界 |
| BGE-M3 单进程加载，不需要 EncoderPool | 单进程天然共享，无 Arc 跨进程同步需求 |
| Brain 只存记忆，不存代码结构 | CodeGraph 是 MeowAgent 的独立组件，正交互补 |
| RRF 融合替代 max-union | agentmemory 验证过的稳定混合检索方案 |

---

## 2. 当前基线数据

| 指标 | MemHop v0.8.0 | FAISS-HNSW | 差距 |
|------|---------------|------------|------|
| NDCG@10 (T2Retrieval) | 0.36 | 0.99 | 2.7x |
| R@10 | 0.91 | 0.99 | 1.1x |
| Recall P50@10K | 104ms | < 1ms | 100x+ |
| Recall P99@10K | 541ms | < 5ms | 100x+ |
| Store P50 | 13ms | < 1ms | 13x |

**诊断**：R@10=0.91 证明 Hopfield 召回可以找到文档。NDCG=0.36 是 downstream ranking 管线（emotional_alignment + ngram_overlap）摧毁了语义排序。Recall 延迟是 Hopfield O(N) 全扫描。

---

## 3. 实施阶段

### Phase 1: 修复 ranking 管线（P0）

**问题**：`Brain::recall` 中 `competitive_spread → emotional_alignment → ngram_overlap` 三连击把 Hopfield cosine 排序摧毁。

**方案**：引入 `RecallMode`，Retrieval Mode 跳过情绪/ngram 排序，只做 cosine sort。

```rust
pub enum RecallMode {
    Retrieval,    // HNSW/Hopfield → cosine sort → 返回
    Associative,  // HNSW/Hopfield → graph spread → 情绪/ngram boost
}
```

| 变更 | 文件 |
|------|------|
| `RecallMode` 枚举 + `RecallRequest.mode` | `brain.rs`, `types.rs` |
| Retrieval 路径：删除 emotional_alignment + ngram_overlap | `brain.rs` |
| Associative 路径：情绪/ngram 降级为 boost（×0.9-1.1） | `brain.rs`, `activation.rs` |
| `quality_bench.rs` 默认使用 Retrieval Mode | `quality_bench.rs` |

**目标**：NDCG 0.36 → > 0.9

**验证**：重新跑 T2Retrieval benchmark，NDCG 应与 R@10 一致（~0.9）。

---

### Phase 2: HNSW 索引（P0）

**问题**：`Hopfield::recall_topk` O(N·d) 全扫描，10K 时 P50=104ms。

**方案**：引入 HNSW 图搜索替代全扫描。Crate 选择 `instant-distance`（纯 Rust，MIT，零依赖）。

```rust
// 新增 memhop/src/hnsw_index.rs
pub struct HnswIndex {
    graph: Hnsw<f32, DistCosine>,
    id_to_idx: HashMap<String, usize>,
    idx_to_id: Vec<String>,
}

impl HnswIndex {
    pub fn search(&self, query: &[f32], k: usize) -> Vec<(String, f32)>;
    pub fn insert(&mut self, id: &str, vector: &[f32]) -> usize;
    pub fn remove(&mut self, id: &str);
}
```

| BrainConfig 新增 | 默认值 |
|------------------|--------|
| `recall_index_type` | `Hnsw` |
| `hnsw_m` | 16 |
| `hnsw_ef_construction` | 200 |
| `hnsw_ef_search` | 100 |

**Hopfield 保留**：不再用于候选生成，保留在 L2（spread 关联扩散）和 Dream（pattern consolidation）。

| 变更 | 文件 |
|------|------|
| `hnsw_index.rs` 新建 | 新文件 |
| Brain 结构体新增 `hnsw_index` | `brain.rs` |
| `perceive()` → 同步写入 HNSW + Hopfield | `brain.rs` |
| `recall()` → HNSW.search 替代 `hopfield.recall_topk` | `brain.rs` |
| **RRF 融合**（来自 agentmemory）：HNSW score + SparseIndex score + Graph spread score → Reciprocal Rank Fusion (k=60) | `brain.rs` |
| HNSW 持久化到 LMDB | `hnsw_index.rs` |
| `BrainConfig` 增加 HNSW 参数 | `types.rs` |
| `Cargo.toml` 添加 `instant-distance` | `Cargo.toml` |

**目标**：recall P50@10K 104ms → < 1ms；RRF 融合比当前 max-union 更稳定。

**RRF 公式**：`score(id) = Σ 1/(k + rank_i)`，其中 k=60，rank_i 是 id 在三个检索流中的排名。比简单归一化后再 union 更抗噪声。

---

### Phase 3: 编码器策略（P1）

**方案**：三层回退，单进程加载。

| 优先级 | 编码器 | 延迟 | 内存 |
|--------|--------|------|------|
| 1 | api-encoder (OpenAI-compatible) | ~200ms | 0 |
| 2 | ONNX BGE-M3 | ~30ms | 2GB |
| 3 | NgramEncoder | ~0.1ms | < 1MB |

单进程天然共享 ONNX session，不需要 EncoderPool 跨进程同步。

| 变更 | 文件 |
|------|------|
| 修复 ort 初始化死锁 | `encoder/onnx.rs` |
| api-encoder 移入 default features | `Cargo.toml`, `encoder/api.rs` |
| `NgramEncoder` pub export（MeowAgent 删除 MeowEncoder 复用） | `lib.rs`, `encoder/ngram.rs` |
| `BrainConfig` 增加 `api_base_url`, `api_model_name` | `types.rs` |

**目标**：在线优先用 api-encoder；BGE-M3 单份内存所有 Brain 共享。

---

### Phase 4: Cross-Encoder 精排（P1）

**方案**：ONNX BGE-Reranker-v2-m3 对 top-20 做 pairwise 精排。

```rust
pub struct Reranker {
    session: ort::Session,
    tokenizer: Tokenizer,
}

impl Reranker {
    pub fn rerank(&self, query: &str, docs: &[(String, String)], top_k: usize)
        -> Vec<(String, f32)>;
}
```

| 配置项 | 值 |
|--------|-----|
| 模型 | BAAI/bge-reranker-v2-m3 |
| 候选数 | 20 |
| 延迟 | ~40ms (ONNX CPU, 20 pairs) |
| 使用方式 | Retrieval Mode + `use_reranker: true` |

| 变更 | 文件 |
|------|------|
| `encoder/reranker.rs` 新建 | 新文件 |
| `RecallRequest` 增加 `use_reranker: bool` | `types.rs` |
| `Brain::recall` Retrieval Mode 增加精排步骤 | `brain.rs` |

**目标**：NDCG > 0.95

**注意**：Reranker 复用同一个 ONNX session，单进程不额外增加内存。

---

### Phase 5: Dream + LLM 能力激活（P2）

**方案**：激活 `LlmProvider` 已有但未使用的 4 个模板。

| Dream 阶段 | 当前 | LLM 增强 |
|-----------|------|---------|
| NREM-1 遗忘 | vitality<0.01 规则 | LLM 判断"真的不重要吗？" |
| REM-1 整合 | cosine>0.9 建边 | LLM 判断相关 + 生成摘要 |
| REM-2 Schema | 规则聚类 | LLM 命名 + 描述 |
| NREM-3 矛盾 | cosine+keyword 规则 | LLM 语义判断 |

**注意**：LLM 只在 Dream 异步调用（~秒级延迟可接受），不在 recall 热路径。

| 变更 | 文件 |
|------|------|
| 激活 `suggest_keywords` 模板 | `dream.rs`, `llm_provider.rs` |
| 激活 `detect_contradiction` 模板 | `dream.rs` |
| 激活 `resolve_contradiction` 模板 | `dream.rs` |
| Dream 自动调度（替换 `dream_interval: usize::MAX`） | `brain.rs` |

---

### Phase 6: MeowAgent 集成（MCP-only，P1）

**MeowAgent 通过 MCP 连接 MemHop**，不再 Cargo 依赖。版本完全解耦，单机内存可控。

**准入条件**（来自 integration spec，按依赖关系重排）：

| 阶段 | 准入 | 内容 |
|------|------|------|
| 6.1 | Phase 1 完成 | MemHopAdapter（改为 MCP 客户端）集成测试 |
| 6.2 | Phase 1+3 完成 | BrainLoop 全链路 E2E（store→recall→reflect） |
| 6.3 | Phase 2+3 完成 | 并发/错误恢复/数据完整性 |
| 6.4 | Phase 4 完成 | 性能回归 + Dream 管道 |
| 6.5 | 长期 | 多猫 / 长对话 (1000+ turn) |

**MCP Tool API 冻结清单（P0）**：

| Tool | 说明 |
|------|------|
| `memhop_store(text, vector?, meta?, session_id, plan_id?)` | 存储记忆 |
| `memhop_recall(query, mode, limit, use_reranker?, tree_id?, max_tokens?)` | 召回 |
| `memhop_reflect(input)` | 反思创建 |
| `memhop_update(id, text, meta)` | **从 no-op 实现** |
| `memhop_forget(id)` | **从 no-op 实现** |
| `memhop_create_tree(name)` | **从 no-op 实现** |
| `memhop_dream()` | 手动触发 Dream |
| `memhop_stats()` | 数据库统计 |

**NgramEncoder pub export（P0）**：meowagent 删除 MeowEncoder，需要本地编码时调 `memhop_encode_ngram` tool。

---

### Phase 7: MCP Server 产品化能力（P1）

**目标**：MemHop MCP Server 作为独立记忆引擎发布，对标 agentmemory 的产品化能力，但保持引擎定位。

| 能力 | 方案 | 来源 |
|------|------|------|
| **Token 预算** | `recall()` 新增 `max_tokens: usize`，返回结果按 token 数截断 | agentmemory 借鉴 |
| **会话多样化** | 每个 session 最多返回 N 条，避免单一会话主导结果 | agentmemory 借鉴 |
| **隐私过滤** | `store()` 自动剥离 API key / secret / `<private>` 标签 | agentmemory 借鉴 |
| **LongMemEval-S** | 新增 benchmark，对标 agentmemory R@5=95.2% | 新 benchmark |
| **Git 快照**（可选） | 记忆状态版本控制，`memhop_snapshot()` / `memhop_rollback()` | agentmemory 借鉴 |
| **健康监控** | MCP Server /health endpoint，内存/磁盘/延迟指标 | 运维能力 |

| 变更 | 文件 |
|------|------|
| `RecallRequest` 增加 `max_tokens` | `types.rs` |
| `Brain::recall` 增加会话多样化逻辑 | `brain.rs` |
| `Brain::perceive` 增加隐私过滤预处理 | `brain.rs` |
| LongMemEval-S benchmark 脚本 | `benchmarks/quality/run_longmemeval.py` |
| MCP Server health endpoint | `memhop-mcp-server/` |

**目标**：MemHop MCP Server 可作为独立产品使用，任何 MCP 客户端接入即可获得生产级记忆系统。

---

### Phase 8: CodeGraph 集成（MeowAgent 侧，P2）

不在 MemHop v0.9.0 范围内，但架构设计已确定：

```
MeowAgent Thalamus (L0 路由)
  ├── "认证中间件怎么改？"
  │     ├── CodeGraph: 找到 auth.rs + login.rs + middleware 调用链
  │     └── MemHop (MCP): 召回"上次改鉴权 bug"、"JWT 过期" 记忆
  │     → 融合注入 prompt
  │
  └── "上周我踩了什么坑？"
        └── MemHop (MCP): 直接语义召回（不需要 CodeGraph）
```

CodeGraph 后端：tree-sitter 符号索引 + git blame 历史，索引缓存到 LMDB。

---

## 4. 依赖关系图

```
Phase 1 (ranking) ──────────────────────┐
  │ 无依赖，最快见效                       │
  │ NDCG 0.36 → 0.9                      │
  │                                      │
  ├── Phase 6.1 (集成测试准入) ←─────────┘
  │
  ├── Phase 2 (HNSW + RRF) ─────────────┐
  │     │ Phase 1 可并行                  │
  │     │ recall 104ms → 1ms             │
  │     │ RRF 融合替代 max-union          │
  │     │                                │
  │     ├── Phase 6.3 (并发测试) ←───────┘
  │     │
  ├── Phase 3 (编码器) ──────────────────┐
  │     │ 依赖 Phase 2（HNSW 也要编码器）  │
  │     │ BGE-M3 单进程加载               │
  │     │ NgramEncoder pub export        │
  │     │                                │
  │     ├── Phase 6.2 (E2E 测试) ←───────┘
  │     │
  │     ├── Phase 4 (Cross-Encoder) ─────┐
  │     │     依赖稳定编码器               │
  │     │     NDCG > 0.95               │
  │     │                                │
  │     └── Phase 5 (Dream LLM)          │
  │           依赖 LlmProvider 稳定       │
  │                                       │
  ├── Phase 7 (MCP Server 产品化) ←──────┐
  │     依赖 Phase 4（质量达标才值得发布）  │
  │     token 预算 + 会话多样化 + 隐私过滤  │
  │     LongMemEval-S benchmark          │
  │                                       │
  └── Phase 8 (CodeGraph) ← 独立并行      │
        MeowAgent 侧，不等 MemHop          │
```

---

## 5. 性能预测

| 指标 | v0.8.0 | P1 | +P2 | +P4 |
|------|--------|-----|------|------|
| NDCG@10 | 0.36 | > 0.9 | > 0.9 | > 0.95 |
| Recall P50@10K | 104ms | 104ms | < 1ms | ~5ms |
| Recall P99@10K | 541ms | 541ms | < 5ms | ~50ms |
| Store P50 | 13ms | 13ms | ~2ms | ~2ms |

| 规模（单 Brain） | HNSW 内存 (f32) | HNSW 内存 (BQ 可选) |
|-------------------|----------------|---------------------|
| 1K | 4MB | 128KB |
| 10K | 40MB | 1.3MB |
| 100K | 400MB | 13MB |
| 1M | 4GB | 128MB |
| 多猫共享 | + BGE-M3 2GB × 1 | 同左 |

---

## 6. 不做的事

- ❌ **多实例每猫一个 BGE-M3** — 单进程天然共享
- ❌ **tree 替代数据库隔离** — tree 保留为猫内领域划分（code/chat/fact），猫之间用独立 LMDB
- ❌ **LLM 在检索热路径** — 只在 Dream 异步用
- ❌ **MemHop 替代 CodeGraph** — 正交互补，CodeGraph 是 MeowAgent 组件
- ❌ **引入外部向量数据库** — 自主实现
- ❌ **删除 Hopfield** — 保留为 Associative Mode + Dream 核心
- ❌ **离线场景优化** — 本期前提：在线优先
- ❌ **MemHop 变成 agentmemory 那样的完整产品** — 保持引擎定位，产品层留给 MeowAgent
- ❌ **MeowAgent Cargo 依赖 MemHop** — MCP-only，版本解耦

---

## ✅ 行动清单

| # | 行动 | 负责方 | 阶段 |
|---|------|--------|------|
| 1 | RecallMode 枚举 + Retrieval 路径（删情绪/ngram 主排序） | memhop | Phase 1 |
| 2 | 重跑 T2Retrieval benchmark 验证 NDCG | memhop | Phase 1 |
| 3 | hnsw_index.rs + Brain 集成 | memhop | Phase 2 |
| 4 | RRF 融合（HNSW + SparseIndex + Graph）替代 max-union | memhop | Phase 2 |
| 5 | HNSW LMDB 持久化 | memhop | Phase 2 |
| 6 | 修复 ort 初始化死锁 | memhop | Phase 3 |
| 7 | NgramEncoder pub export | memhop | Phase 3 |
| 8 | 编码器三层回退（api > ONNX > ngram） | memhop | Phase 3 |
| 9 | Cross-Encoder Reranker 集成 | memhop | Phase 4 |
| 10 | 重跑 benchmark 验证 NDCG > 0.95 | memhop | Phase 4 |
| 11 | Dream 模板激活 (suggest_keywords, detect/resolve_contradiction) | memhop | Phase 5 |
| 12 | Dream 自动调度 | memhop | Phase 5 |
| 13 | update() / forget() / create_tree() 实现 | memhop | Phase 6 |
| 14 | MCP Server tool 冻结（8 个核心 tool） | memhop | Phase 6 |
| 15 | MeowAgent MCP 客户端适配（替换 Cargo 依赖） | meowagent | Phase 6 |
| 16 | 删除 MeowEncoder，改用 memhop_encode_ngram MCP tool | meowagent | Phase 6 |
| 17 | Token 预算 + 会话多样化 | memhop | Phase 7 |
| 18 | 隐私过滤（store 自动剥离 secrets） | memhop | Phase 7 |
| 19 | LongMemEval-S benchmark | memhop | Phase 7 |
| 20 | MCP Server health endpoint + 指标暴露 | memhop | Phase 7 |

---

## ⚠️ 待确认 / 假设

- 假设 `instant-distance` 与当前 Rust toolchain 兼容
- 假设 ONNX 模型（BGE-M3 + BGE-Reranker-v2-m3）在 macos arm64 CPU 上延迟可接受
- 假设 HNSW f32 内存占用在单 Brain 场景可接受（100K=400MB），BQ 作为按需降级
- 假设 MCP 序列化开销（~0.5ms）对 MeowAgent 的 LLM 推理延迟（~1-2s）可忽略
- CodeGraph 不在 MemHop v0.9.0 范围，MeowAgent 独立开发

---

## 📚 数据来源

- `benchmarks/reports/comparison_t2retrieval_100.json` — NDCG 0.36 vs FAISS 0.99
- `/tmp/memhop_latency_1k_5k_10k.json` — recall O(N) 证据
- `memhop/src/brain.rs:575-779` — ranking 管线
- `memhop/src/hopfield.rs:228-254` — O(N) 全扫描
- `.qoder/cli/specs/t1-roadmap.md` — HNSW + 编码器 + Dream LLM 方案
- `.qoder/cli/specs/meowagent-memhop-integration.md` — API 准入 + CodeGraph 设计
- `deliverables/product-strategy/architecture-first-tier-redesign-2026-05-28.md` — 三层检索 + 双模式
- `github.com/rohitg00/agentmemory` — 对标分析：RRF 融合、LongMemEval-S、token 预算、隐私过滤

---

> 本路线图综合四份设计文档 + 基准测试数据 + 竞品对标分析生成。
> 重要决策请由产品负责人审定。
