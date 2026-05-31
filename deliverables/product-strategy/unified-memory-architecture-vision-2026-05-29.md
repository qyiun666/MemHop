# MemHop 统一记忆架构 — 全景分析与产品定位

**日期**：2026-05-29
**类型**：产品架构分析 + 战略定位
**作者**：方向明（Fang）· 产品舵手

---

## 📌 TL;DR

- **核心判断**：统一 engram 存储（情景 + 语义融合）是正确的，当前 Shelf 独立架构是技术债
- **覆盖场景**：通用个人知识管理、开发者项目记忆、专业领域（法律/医疗/学术/金融）、企业协作 — 四层全吃
- **差异化壁垒**：CodeGraph 做代码结构，MemHop 做记忆涌现。不是替代关系，是 MeowAgent 的"海马体 + 皮层"双层中"海马体"这一层
- **垂类扩展**：通过 ShelfDomain 注册自定义 chunker + meta schema + ranker，不改核心里程
- **下一步**：v0.10.0 统一存储重构 → v0.11.0 垂类扩展框架 → v1.0 面向所有人的记忆系统

---

## 🎯 核心结论卡片

| 项目 | 内容 |
|------|------|
| 推荐方案 | 统一 engram 存储（Shelf 融入 store/recall），领域通过 trait 扩展 |
| 架构复杂度 | 中等。核心 API 不变（store/recall/dream），Shelf 变成上游管道 |
| 通用性 | 高。所有场景落在同一个抽象上：文本 → engram → 检索 |
| 垂类定制 | 通过 ShelfDomain trait 注册：自定义 chunker + meta schema + ranker |
| 竞争壁垒 | 记忆涌现（Hebbian 学习 + Dream 巩固）vs 静态索引 |
| 风险等级 | 中。重构 Shelf 有破坏性变更，但 Shelf 当前用户极少 |

---

## 1. 现状审计：裂脑架构

### 1.1 什么是裂脑

```
store("我读了 Rust 异步那本书")
    → Engram { kind: Episode, text: "我读了 Rust 异步" }
    → 存进 LMDB，HNSW 索引

mount_shelf("/books/rust-async.pdf")
    → Shelf { chunks: [chunk1, chunk2, ...], tree: ShelfTree(HNSW + BM25) }
    → 存在内存 HashMap，独立索引
```

**recall("Rust 异步调度器") 只查 Engram 主存储，不碰 Shelf。**
Agent 需要两次 MCP 调用才能拿到完整上下文。

### 1.2 裂在哪里

| 维度 | 主存储 (Engram) | Shelf |
|------|----------------|-------|
| 存储后端 | LMDB 持久化 | 内存 HashMap（重启丢失） |
| 向量索引 | HNSW + Hopfield | ShelfTree (另一个 HNSW) |
| recall 是否纳入 | ✅ | ❌ 需单独 knowledge_search |
| EntangleGraph 自动关联 | ✅ | ❌ |
| Dream 巩固 | ✅ | ❌ |
| 生命周期管理 | ✅ vitality/保护/眠 | ❌ |

### 1.3 Shelf 作为独立系统的历史原因

Shelf 是 v0.9.0 时快速加上去的"挂载文件搜索"功能。当时为了避免耦合主存储，选择了独立模块。结果这个临时方案活到现在，造成以下问题：

1. **两份 HNSW 索引**，内存翻倍
2. **restart 后 Shelf 丢失**（只在内存中）
3. **recall 不知道 Shelf 的存在**，Agent 需要自己拼接
4. **EntangleGraph 无法连接** Shelf chunk 和 Episodic engram
5. **Dream 无法处理**挂载的知识

---

## 2. 全场景覆盖分析

### 2.1 场景全景表

| # | 场景 | 记忆来源 | 检索需求 | 当前状态 | 统一后 |
|---|------|---------|---------|---------|--------|
| 1 | 看了本技术书 | mount PDF/book | "这本书里讲 X 的部分" | ⚠️ 需两次调用 | ✅ 一次 recall |
| 2 | 读了一篇论文 | mount/拖入 PDF | "论文 Y 的核心结论" | ⚠️ | ✅ |
| 3 | 写了一个项目 | 对话中 store 踩坑经验 | "上次 auth 模块的 bug" | ✅ | ✅ |
| 4 | 项目文档挂载 | mount /docs | "认证流程怎么配" | ⚠️ | ✅ |
| 5 | 日常聊天记忆 | 对话自动 store | "我上周说了想学 Rust 吗" | ✅ | ✅ |
| 6 | 浏览网页/文章 | 浏览器插件 → store | "那篇 tokio 文章说了什么" | ✅ | ✅ |
| 7 | 看视频/听播客 | 转录 → store | "那期播客提到的观点" | ✅ | ✅ |
| 8 | 多项目交叉 | 自动 store + 手动 link | "项目 A 的方案能用在 B 吗" | ✅ | ✅ + 跨树自动关联 |
| 9 | 法律：案例库 | mount case_law/*.pdf | "类似判例的裁判要旨" | ⚠️ Shelf 独立 | ✅ 自定义 chunker |
| 10 | 医学：临床指南 | mount guidelines/*.pdf | "某疾病的诊疗路径" | ⚠️ | ✅ |
| 11 | 学术：文献综述 | mount papers/*.pdf | "某研究方向的进展" | ⚠️ | ✅ |
| 12 | 金融：研报 | mount reports/*.pdf | "某行业的最新判断" | ⚠️ | ✅ |
| 13 | 企业：内部 SOP | mount /internal-docs | "某流程的标准操作" | ⚠️ | ✅ |
| 14 | 团队共享知识 | 共享 memory DB | "团队对 XX 的决策历史" | ❌ | ✅ 多用户 |
| 15 | 多设备同步 | iCloud/Git sync LMDB | "我手机上读的那本书" | ❌ | ✅ 文件级同步 |

### 2.2 场景按复杂度分级

**L1 — 纯对话记忆（当前已完整支持）**
- 用户场景：日常聊天、编码经验、个人决策
- 输入：对话文本
- 输出：语义相关记忆
- MemHop 覆盖：✅ 完整

**L2 — 对话 + 静态文档（当前裂脑，统一后完整支持）**
- 用户场景：读书后讨论、看论文后写综述、挂着项目文档问问题
- 输入：对话 + 文档 chunk
- 输出：记忆 + 文档片段（关联返回）
- MemHop 覆盖：⏳ 统一后可完整支持

**L3 — 专业领域定制（需要 chunker/ranker 扩展）**
- 用户场景：法律案例检索、医学文献综述、金融研报分析
- 输入：领域特定文档（需特殊 chunk 策略）
- 输出：领域相关的记忆 + 文档片段 + 结构化 meta
- MemHop 覆盖：⏳ 需 ShelfDomain trait 扩展

**L4 — 协作/多设备（架构级扩展）**
- 用户场景：团队知识库、跨设备记忆
- 输入：多用户 store + 多设备同步
- 输出：共享记忆 + 权限控制
- MemHop 覆盖：❌ 本期不做，但架构需预留

---

## 3. 统一架构设计

### 3.1 核心理念

> 所有知识都是 engram。对话是 engram，书的章节是 engram，论文段落是 engram，代码文件是 engram。区别只在 `kind` 和 `meta`，不在存储位置和检索路径。

### 3.2 新的 EngramKind

```
当前:
  EngramKind::Episode     — 对话/事件
  EngramKind::Schema      — 抽象出的模式
  EngramKind::Anchor      — 场景锚点
  EngramKind::Reflection  — 自我反思

新增:
  EngramKind::Knowledge   — 挂载的外部知识（书的章节、论文段落、文档片段）
                           meta: { source: "rust-async.pdf", shelf_id: "shelf_xxx",
                                   chunk: 3, domain: "book", ... }
```

### 3.3 新的数据流

```
mount_shelf("/books/rust-async.pdf")
  ↓
  不建独立 Shelf！直接走 store 批量写入：
    store(Engram {
        id: "shelf_xxx_chunk_0",
        text: "tokio 使用 work-stealing 实现...",
        kind: Knowledge,
        vector: [f16; 1024],
        meta: { shelf_id: "shelf_xxx", source: "rust-async.pdf",
                chunk: 0, domain: "book" },
    })
    store(Engram {
        id: "shelf_xxx_chunk_1",
        text: "async/await 的底层状态机...",
        ...
    })
  ↓
  所有 chunk 进入主 HNSW + Hopfield + EntangleGraph
  ↓
  ShelfManager 退化为元数据管理者：
    - 记录 shelf_id → { path, domain, chunk_count }
    - 提供 unmount（批量删除 chunk engrams）
    - 提供 knowledge_search → 映射到 recall(shelf_filter: shelf_id)
```

### 3.4 unified recall

```
recall("tokio 调度器设计")
  ↓
  HNSW 召回 (不限 kind):
    engram_123 (Episode, "上周决定用 tokio 替代 async-std")
    engram_456 (Knowledge, "tokio 使用 work-stealing...", meta: { shelf_id: "s_1" })
    engram_789 (Episode, "上次改 tokio runtime 配置踩坑")
    engram_012 (Knowledge, "async/await 状态机...", meta: { shelf_id: "s_1" })
  ↓
  EntangleGraph 扩散:
    发现 engram_456 和 engram_012 同属 shelf_1
    发现 engram_123 和 engram_456 有 Semantic 边（关键词 "tokio"）
  ↓
  返回:
    {
        results: [engram_123, engram_456, engram_789, engram_012],
        contexts: {
            shelf_1: "从《Rust 异步编程》第 3 章挂载"
        },
        associations: [
            "你上周决定用 tokio，这本书第 3 章详细讲了调度器设计"
        ]
    }
```

### 3.5 ShelfManager 的新角色

ShelfManager 不再是独立存储，而是：

1. **mount(path, domain)** → 调用 scanner + chunker，对每个 chunk 调用 `brain.store(kind=Knowledge)`
2. **unmount(shelf_id)** → 删除所有 `meta.shelf_id == shelf_id` 的 engram
3. **knowledge_search(query, shelf_id)** → `recall(query, filter: { shelf_id })`
4. **get_shelf_status()** → 返回挂载的 shelf 元数据（不涉及内容）

### 3.6 性能影响

| 维度 | 当前（裂脑） | 统一后 |
|------|------------|--------|
| HNSW 实例 | 2 个（主 + 每个 Shelf） | 1 个 |
| 内存占用 | 两份索引 | 一份，轻微增加（多了 Knowledge engram） |
| recall 延迟 | 1 次 HNSW 查询 | 1 次 HNSW 查询（结果中多了 Knowledge 类型） |
| recall 质量 | 不知道 Shelf 存在 | EntangleGraph 自动关联 |
| 重启后 Shelf 可用性 | ❌ 丢失 | ✅ LMDB 持久化 |

---

## 4. 垂类扩展机制

### 4.1 不牺牲通用性的原则

核心 API 不变：

```
store(text, meta)    ← 任何人都能用的最简单的接口
recall(query)         ← 任何人一次调用拿回所有相关记忆
dream()               ← 自动巩固
```

垂类用户看不到这些：

```
ShelfDomain::Code / Doc / Book / Paper / Custom
自定义 chunker trait
自定义 meta schema
自定义 ranker（按领域加权）
```

**通用用户在基础路径上走，垂类用户在扩展点上接。**

### 4.2 ShelfDomain 扩展为 trait

```rust
pub trait ShelfDomainTrait {
    /// 扫描：从路径提取文件
    fn scan(path: &str) -> Vec<ScannedFile>;

    /// 分块：按领域策略切分
    fn chunk(path: &str, text: &str) -> Vec<(String, ChunkMeta)>;

    /// 自定义元数据提取（可选）
    fn extract_meta(text: &str) -> HashMap<String, Value> {
        HashMap::new()  // 默认不提取
    }

    /// 排名字段权重（可选）
    fn ranking_bias() -> HashMap<String, f32> {
        HashMap::new()  // 默认无偏好
    }
}
```

### 4.3 垂类示例

**法律**（Law 领域）：
```rust
impl ShelfDomainTrait for Law {
    fn chunk(...) { /* 按条款/判例号切分 */ }
    fn extract_meta(text) -> {
        // 提取：案件编号、法院、裁判日期、适用法条
        "case_number": "...", "court": "...", "date": "..."
    }
    fn ranking_bias() -> {
        // 近期的、同法院的权重更高
        "recency": 0.4, "same_court": 0.3
    }
}
```

**医学**（Medical 领域）：
```rust
impl ShelfDomainTrait for Medical {
    fn chunk(...) { /* 按病症/指南章节切分 */ }
    fn extract_meta(text) -> {
        "disease": "...", "guideline_level": "A/B/C",
        "publication_year": "..."
    }
    fn ranking_bias() -> {
        "guideline_level": 0.5, "recency": 0.3
    }
}
```

**金融**（Finance 领域）：
```rust
impl ShelfDomainTrait for Finance {
    fn chunk(...) { /* 按研报板块/公司切分 */ }
    fn extract_meta(text) -> {
        "ticker": "...", "sector": "...", "report_date": "..."
    }
}
```

### 4.4 自定义扩展不需要 fork

垂类用户只需：
1. 实现 `ShelfDomainTrait`
2. 在 `ShelfDomain` 注册新变体（或直接用 `Custom` + config JSON）
3. 调用 `mount_shelf(path, domain=law, config=...)`

**不改 MemHop 核心代码，不改 MeowAgent。**

---

## 5. 竞品定位分析

### 5.1 MemHop 在生态中的位置

```
                    ┌──────────────────────────┐
                    │     LLM / AI Agent        │
                    │  (Claude/GPT/Copilot)     │
                    └──────────┬───────────────┘
                               │ 需要上下文
                    ┌──────────▼───────────────┐
                    │    MeowAgent (Thalamus)   │
                    │  路由 + LLM 编排           │
                    └──┬────────┬──────┬───────┘
                       │        │      │
              ┌────────▼──┐ ┌───▼───┐ ┌▼──────────┐
              │ MemHop     │ │CodeGraph│ │KnowledgeBase│
              │ 记忆系统    │ │ 代码图  │ │ 知识库      │
              │ (海马体)    │ │ (空间记忆)│ │ (语义皮层)  │
              └────────────┘ └────────┘ └────────────┘
```

| 产品 | 做什么 | 存储 | 检索 | 图 | MemHop 差异 |
|------|-------|------|------|---|------------|
| **CodeGraph** | 代码结构图 | SQLite | 符号查询 | ✅ 静态调用图 | MemHop 不解析代码结构 |
| **Graphify** | 通用知识图谱 | 本地 JSON | 实体-关系查询 | ✅ 静态实体图 | MemHop 是动态涌现图 |
| **agentmemory** | Agent 记忆 SDK | 向量库 | 语义搜索 | ❌ | MemHop 有 Dream + Hebbian |
| **Mem0** | Agent 记忆 API | 云端 | 语义搜索 | ❌ | MemHop 本地优先 + 图 |
| **FAISS** | 向量检索库 | 内存/磁盘 | KNN | ❌ | MemHop 是完整记忆系统 |
| **Milvus** | 向量数据库 | 分布式 | ANN | ❌ | MemHop 是嵌入式 |
| **MemHop** | **统一记忆系统** | LMDB+HNSW | 语义+图扩散 | ✅ 动态 Hebbian 图 | — |

**MemHop 的独特位置**：不是代码图谱（CodeGraph）、不是通用知识图谱（Graphify）、不是向量数据库（FAISS/Milvus）、不是 Agent 记忆 SDK（agentmemory/Mem0）。是**带有涌现式图学习能力的本地优先统一记忆系统**。

### 5.2 为什么这个位置有壁垒

1. **记忆涌现**：CodeGraph 图是静态解析的。MemHop 图是从使用中自动学习的（每次 recall 强化边，Dream 发现新关联）。这是静态解析做不到的。
2. **本地优先**：Mem0 是云的，FAISS 只是库。MemHop 是一个 self-contained 的二进制文件 + LMDB。企业可以自己部署，数据不出境。
3. **统一模型**：agentmemory 把代码/文档/对话拆成不同的记忆类型。MemHop 把它们统一为 engram，减少 Agent 的认知负担。

---

## 6. 分期路线

### Phase 1：统一存储重构（v0.10.0）

| 项目 | 内容 |
|------|------|
| Shelf 融入主存储 | mount → store(kind=Knowledge) 批量写入 |
| recall 统一 | 不区分来源，HNSW 自然返回混合结果 |
| EntangleGraph 跨类型 | Knowledge engram 自动参与图扩散 |
| Dream 支持 Knowledge | 非活跃 Knowledge engram vitality 衰减 |
| 废弃独立 Shelf | ShelfManager 退化为元数据管理者 |

**破坏性变更**：mount_shelf 的行为从"建独立索引"变为"批量 store"。ShelfTree 移除。

### Phase 2：垂类扩展框架（v0.11.0）

| 项目 | 内容 |
|------|------|
| ShelfDomainTrait | 可注册自定义 chunker + meta + ranker |
| 内置 Law / Medical 示例 | 作为 Custom 的参考实现 |
| MCP 兼容 | mount_shelf 保持，knowledge_search 映射到 recall |

### Phase 3：协作和多设备（v1.0.0）

| 项目 | 内容 |
|------|------|
| 多用户 access control | meta 级别的读/写/共享标记 |
| 文件级同步 | LMDB 可通过 iCloud/Git 同步（单写多读） |
| 团队知识库 | 共享 memory DB，权限细分 |

---

## 7. 和 CodeGraph 的最终关系

```
MeowAgent 收到用户查询 "重构 auth 模块的建议"
  │
  ├── Thalamus 判断查询类型 → 混合任务
  │
  ├── CodeGraph: "auth 模块包含 auth.rs + login.rs + middleware.rs"
  │     → 返回调用链 + 文件位置
  │
  ├── MemHop Knowledge: "《OAuth 2.0 实战》第 5 章: 常见安全漏洞"
  │     → 返回书中相关内容
  │
  ├── MemHop Episode: "三周前改 auth 引入了一个 session 泄漏 bug"
  │     → 返回踩坑经验
  │
  └── Thalamus 融合 → prompt
       "你需要改 auth 模块（auth.rs, login.rs, middleware.rs）。
        注意：三周前你改 session 时引入过泄漏 bug。
        参考《OAuth 2.0 实战》第 5 章的安全最佳实践。"
```

**一句话**：CodeGraph 告诉 Agent **代码长什么样**，MemHop 告诉 Agent **你之前在这干了什么、读过什么、踩过什么坑**。两个加在一起，才是"完整的上下文"。

---

## ✅ 行动清单

| # | 行动 | 负责方 | 时间窗 |
|---|------|--------|--------|
| 1 | 确认统一架构方向，批准 v0.10.0 重构 | Fang | 本周 |
| 2 | 写 v0.10.0 详细设计（EngramKind::Knowledge + Shelf 融入） | Specky | 下周 |
| 3 | ShelfDomain → trait 化设计文档 | Specky | v0.10.0 后 |
| 4 | 内置 Law/Medical 领域示例 | Specky | v0.11.0 |
| 5 | 竞品调研更新（CodeGraph/Graphify/agentmemory 最新动态） | Compa | 需求时 |

---

## ⚠️ 待确认 / 假设 / Non-goals

- **假设**：Shelf 当前用户极少（主要是 MeowAgent），破坏性变更可接受
- **假设**：统一后单 HNSW 索引的性能不会因 engram 数量翻倍而显著退化（100K 以内验证过）
- **假设**：Knowledge engram 的 vitality 衰减策略与 Episode 一致（待 Dream 中验证）
- **Non-goal**：本期不做多用户权限控制（v1.0 考虑）
- **Non-goal**：本期不做云端同步（本地优先，文件级同步留给用户自己选择）
- **Non-goal**：CodeGraph 不进 MemHop（MeowAgent 侧独立）

---

## 📚 数据来源

- `memhop/src/shelf/mod.rs` — Shelf 当前实现
- `memhop/src/engram.rs` — Engram + EngramKind 定义
- `memhop/src/brain.rs` — recall 管线（确认 Shelf 未接入）
- `memhop-mcp-server/src/main.rs` — MCP 工具清单（Shelf 独立暴露）
- `deliverables/product-strategy/roadmap-v090-unified-2026-05-28.md` — CodeGraph 定位文档
- CodeGraph GitHub (`colbymchenry/codegraph`) — 架构分析
- agentmemory GitHub (`rohitg00/agentmemory`) — 对标分析

---

> 本报告由产品战略团队 AI 协作生成，重要决策请由 Fang 审定。
