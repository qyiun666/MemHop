# MemHop v0.9.0 路线图

**日期**：2026-05-28
**定位**：脑启发记忆引擎。全机单 MCP Server。只做记忆存储和检索。
**对端**：MeowAgent 通过 MCP 连接。MemHop 不关心谁在调用自己。

---

## 📌 TL;DR

- **MCP-only**：单进程，全机一个。MeowAgent、Cursor、任何 MCP 客户端都走统一协议
- **多数据库路径**：猫 A → `A/`，猫 B → `B/`，共享书架 → `shelves/rust-book/`
- **编码器**：单进程加载一份 BGE-M3 (2GB)
- **8 个 Phase**：ranking → HNSW+RRF → 编码器 → Cross-Encoder → Dream LLM → 知识库挂载 → MCP Server 产品化 → 发布

---

## 🎯 核心结论卡片

| 项目 | 内容 |
|------|------|
| 目标版本 | v0.9.0 |
| 部署模式 | 单进程 MCP Server，多数据库路径 |
| 核心变更 | RecallMode 双模式、HNSW O(log N)、RRF 融合、Cross-Encoder、知识库挂载 |
| NDCG@10 | 0.36 → > 0.95 |
| recall P50@10K | 104ms → < 1ms |
| 对标 | 引擎 vs 引擎：Hopfield 补全 + Hebbian 图学习 + 逐轮实时 ← agentmemory 没有的 |

---

## 1. 当前基线

| 指标 | v0.8.0 | 根因 |
|------|--------|------|
| NDCG@10 | 0.36 | emotional_alignment + ngram_overlap 摧毁语义排序 |
| R@10 | 0.91 | Hopfield 召回本身没问题 |
| recall P50@10K | 104ms | Hopfield::recall_topk O(N) 全扫描 |

---

## 2. 部署拓扑

```
memhop-mcp-server (单进程)
  ├── BGE-M3 ONNX (1×2GB)
  ├── Brain(cat_a)    → LMDB: /data/cats/A/
  │     HNSW / Hopfield / EntangleGraph 独立
  ├── Brain(cat_b)    → LMDB: /data/cats/B/
  ├── Brain(shared)   → LMDB: /data/cats/shared/
  └── Shelf(rust-book)→ LMDB: /data/shelves/rust-book/
        HNSW-only（无 Hopfield，纯知识索引）
```

---

## 3. MemHop 的职责边界

| 做什么 | 不做什么 |
|--------|---------|
| 存储记忆（perceive → store） | 不自动捕获（那是 MeowAgent 的事） |
| 语义检索（recall, 双模式） | 不做推理 |
| 知识库挂载（mount path → 自动索引） | 不做代码分析（那是 CodeGraph） |
| Dream 记忆整合 | 不做多猫协调（那是 MeowAgent Thalamus） |
| MCP Server 运维（health、指标） | 不做 UI / 看板 |
| 隐私过滤（store 剥离 secrets） | 不做对话管理 |

---

## 4. 两条存储管线

### 记忆管线（Episodic Memory）

```
perceive(text, vector, meta, source?)
  → 隐私过滤（剥离 secrets）
  → Hopfield store（模式存储）
  → HNSW insert（语义索引）
  → SparseIndex insert（ngram 索引）
  → EntangleGraph 建边（auto-entangle）
  → 返回 engram_id
```

### 知识库管线（Knowledge Shelf）

```
mount_shelf(path, domain?)
  → 扫描路径（文件/目录/git repo/URL）
  → 按 domain 策略切片（code→AST, doc→heading, book→chapter）
  → 逐片 encode（BGE-M3）
  → HNSW insert（纯语义索引，不走 Hopfield）
  → 元信息存 LMDB（source, shelf_id, location, url）
  → 返回 shelf_id
```

```
unmount_shelf(shelf_id)
  → 删除 HNSW 条目
  → 清理 LMDB 元信息
  → 不删原始文件
```

**两种记忆共享同一个 Brain，但存储策略不同**：

| | 记忆 (Episodic) | 知识库 (Shelf) |
|---|---|---|
| 索引 | HNSW + Hopfield + SparseIndex | HNSW only |
| 生命周期 | Dream 衰减 | 手动 unmount |
| 图 | EntangleGraph 动态学习 | 静态引用图 |
| 召回模式 | Retri eval / Associative 双模式 | Retrieval only |
| API | `memhop_recall(mode=...)` | `memhop_knowledge_search(shelf_id)` |

---

## 5. 实施 Phase

### Phase 1: 修复 ranking 管线（P0）

唯一瓶颈：emotional_alignment + ngram_overlap 摧毁语义排序。

```rust
pub enum RecallMode {
    Retrieval,    // HNSW → cosine sort → 返回
    Associative,  // HNSW → Hopfield spread → 情绪/ngram boost
}
```

| 变更 | 文件 |
|------|------|
| RecallMode 枚举 + RecallRequest.mode | types.rs, brain.rs |
| Retrieval 路径：跳过 emotional/ngram 主排序 | brain.rs |
| Associative 路径：降级为 boost（×0.9-1.1） | brain.rs |
| quality_bench 默认 Retrieval Mode | quality_bench.rs |

**验证**：重跑 T2Retrieval，NDCG 应与 R@10 对齐（~0.9）。

---

### Phase 2: HNSW + RRF 融合（P0）

HNSW 替代 Hopfield::recall_topk O(N) 全扫描。RRF 融合替代 max-union。

| 变更 | 文件 |
|------|------|
| hnsw_index.rs（instant-distance crate） | 新文件 |
| Brain 结构体新增 hnsw_index | brain.rs |
| perceive → HNSW insert（同步） | brain.rs |
| recall → HNSW.search 替代 hopfield.recall_topk | brain.rs |
| RRF 融合：HNSW + SparseIndex + Graph（k=60） | brain.rs |
| HNSW LMDB 持久化 | hnsw_index.rs |

**目标**：recall P50@10K < 1ms。

---

### Phase 3: 编码器（P1）

三层回退：api-encoder > ONNX BGE-M3 > NgramEncoder。

| 变更 | 文件 |
|------|------|
| 修复 ort 初始化死锁 | encoder/onnx.rs |
| NgramEncoder pub export | lib.rs |
| Brain::open 按优先级加载编码器 | brain.rs |
| BrainConfig 增加 api_base_url | types.rs |

---

### Phase 4: Cross-Encoder 精排（P1）

ONNX BGE-Reranker-v2-m3 对 top-20 精排。

| 变更 | 文件 |
|------|------|
| encoder/reranker.rs | 新文件 |
| RecallRequest.use_reranker | types.rs |
| Retrieval Mode 末尾接入精排 | brain.rs |

**目标**：NDCG > 0.95。

---

### Phase 5: Dream + LLM（P2）

激活已有但未用的 LlmProvider 模板。

| 变更 | 文件 |
|------|------|
| 激活 suggest_keywords / detect_contradiction 等模板 | dream.rs |
| Dream 自动调度 | brain.rs |

---

### Phase 6: 知识库挂载（P1）

MeowAgent 传路径，MemHop 自动建书架。

```rust
// MCP tool
memhop_mount_shelf(path: "/Users/zt/books/rust-book", domain: "book")
  → 扫描 → 切片 → 编码 → HNSW 索引 → 返回 shelf_id

memhop_knowledge_search(query: "Rust ownership", shelf_id: "rust-book", limit: 5)
  → HNSW 检索 → 返回 { text, location, score }

memhop_unmount_shelf(shelf_id: "rust-book")
  → 清理索引
```

| 变更 | 文件 |
|------|------|
| knowledge/shelf.rs（切片 + 索引） | 新文件 |
| Brain 结构体新增 shelves: HashMap | brain.rs |
| MCP tool: mount_shelf / knowledge_search / unmount_shelf | mcp-server |
| PerceptionInput.source 字段（记忆关联知识来源） | types.rs |

切片策略按 domain：

| domain | 切片单元 | 编码策略 |
|--------|---------|---------|
| code | AST function/struct 级 | code-embed |
| doc | heading 段落 | text-embed |
| book | chapter 小节 | text-embed |
| paper | section + abstract | text-embed |
| custom | 固定 token 窗口 | text-embed |

---

### Phase 7: MCP Server 产品化（P1）

| 能力 | 方案 |
|------|------|
| Token 预算 | recall/knowledge_search 增加 max_tokens 参数 |
| 会话多样化 | 每 session 最多返回 N 条 |
| 隐私过滤 | store 自动剥离 API key / secret 标签 |
| LongMemEval-S | 新增 benchmark，对标 agentmemory R@5=95.2% |
| Health endpoint | /health 返回内存/磁盘/延迟 |
| Git 快照（可选） | memhop_snapshot / memhop_rollback |

**目标**：MemHop MCP Server 可作为独立记忆引擎发布，任何 MCP 客户端接入即用。

---

### Phase 8: 发布

- MCP Server 独立二进制发布
- 文档：MCP tool schema + 部署指南
- 示例：Cursor / Claude Code 接入配置

---

## 6. 依赖图

```
P1 (ranking) ─── P2 (HNSW+RRF) ─── P3 (编码器) ─── P4 (Cross-Encoder)
                                                         │
                                          P5 (Dream LLM) │
                                                         │
                                          P6 (知识库挂载) │
                                                         │
                                          P7 (产品化)  ◄─┘
                                                         │
                                          P8 (发布)    ◄─┘
```

---

## 7. MCP Tool 清单

| Tool | Phase | 说明 |
|------|-------|------|
| `memhop_store` | P0 | 存储记忆（text, vector?, meta, session_id, source?） |
| `memhop_recall` | P0 | 召回（query, mode, limit, use_reranker?, max_tokens?） |
| `memhop_mount_shelf` | P6 | 挂载知识库（path, domain） |
| `memhop_knowledge_search` | P6 | 知识库检索（query, shelf_id, limit, max_tokens?） |
| `memhop_unmount_shelf` | P6 | 卸载知识库 |
| `memhop_update` | P6 | 更新记忆 |
| `memhop_forget` | P6 | 删除记忆 |
| `memhop_create_tree` | P6 | 创建领域树（猫内 code/chat/fact 隔离） |
| `memhop_dream` | P5 | 手动触发 Dream |
| `memhop_stats` | P7 | 数据库统计 |
| `memhop_health` | P7 | 健康检查 |
| `memhop_snapshot` | P7 | 记忆快照（可选） |

**不做的事**：

- ❌ 代码结构分析（CodeGraph 是 MeowAgent 的事）
- ❌ 对话管理、自动 hook（MeowAgent 的事）
- ❌ 多猫协调、路由决策（MeowAgent Thalamus）
- ❌ LLM 在 recall 热路径
- ❌ 引入外部向量数据库
- ❌ 删除 Hopfield

---

> MemHop 是引擎。谁用它、怎么用、要不要加自动 hook——那是调用方的事。
