# MemHop v0.11.0 统一记忆架构 — 产品需求规格书 (PRD)

**版本**：v1.0
**日期**：2026-05-29
**作者**：析客（Specky）· 需求分析师
**状态**：待评审

**上游依赖**：
- [愿景文档](./unified-memory-architecture-vision-2026-05-29.md)（方向明）
- [用户研究](./user-research-unified-memory-2026-05-29.md)（瑞思）
- [竞品分析](./competitive-analysis-unified-memory-2026-05-29.md)（竞析）
- [性能评估](./metrics-review-unified-memory-2026-05-29.md)（数析）

---

## 目录

1. [问题陈述](#1-问题陈述)
2. [目标与 Non-goals](#2-目标与-non-goals)
3. [目标用户与用户故事](#3-目标用户与用户故事)
4. [方案设计](#4-方案设计)
5. [MCP 工具清单与语义变更](#5-mcp-工具清单与语义变更)
6. [meta Schema 规范](#6-meta-schema-规范)
7. [技术考量](#7-技术考量)
8. [成功指标与 SLA](#8-成功指标与-sla)
9. [验收标准](#9-验收标准)
10. [里程碑与时间线](#10-里程碑与时间线)
11. [开放问题](#11-开放问题)
12. [附录](#12-附录)

---

## 1. 问题陈述

### 1.1 核心问题：裂脑架构

MemHop 当前存在"裂脑"问题——主存储（Engram）和知识树（挂载目录）是两套独立系统：

```
store("我读了 Rust 异步那本书")
    → Engram { kind: Episode } → LMDB + HNSW 索引

mount_tree("/books/rust-async.pdf")
    → 独立内存 HashMap + 第二个 HNSW（ShelfTree）
```

**recall("Rust 异步调度器") 只查主存储，不碰知识树。** Agent 需要两次 MCP 调用才能拿到完整上下文。

### 1.2 裂在哪（五维度断裂）

| 维度 | 主存储 (Engram) | 知识树 | 断裂后果 |
|------|----------------|-------|---------|
| 存储后端 | LMDB 持久化 | 内存 HashMap（重启丢失） | 用户重启后 Shelf 全丢 |
| 向量索引 | HNSW + Hopfield | ShelfTree（另一个 HNSW） | 内存翻倍，两份索引 |
| recall 是否纳入 | ✅ | ❌ 需单独 knowledge_search | Agent 两次 MCP 调用 |
| EntangleGraph 关联 | ✅ | ❌ | 读过的内容与对话记忆无法关联 |
| Dream 巩固 | ✅ | ❌ | 知识永不巩固、不涌现 |
| **持久化** | ✅ LMDB | ❌ 内存 HashMap | 重启全丢 |

### 1.3 用户端的痛

来自用户研究和竞品分析的三方一致结论：

1. **P0 — 统一 recall 缺失**：用户挂载书/论文后，recall 不返回文档内容，需手动两次调用并自行拼接。这是所有用户层最大的共同痛点。
2. **P0 — Shelf 重启丢失**：内存 HashMap 不持久化，每次重启需重新 mount + encode。用户对系统可靠性丧失信心。
3. **P1 — 跨类型关联缺失**：EntangleGraph 无法连接"读过的书的内容"和"聊过的踩坑经验"。这是差异化核心价值的缺失。
4. **P1 — 跨项目模式识别不足**：多项目并行时，用户需自己意识到"这和之前那个项目类似"。
5. **P2 — Dream 不处理外部知识**：挂载的知识不参与 vitality 衰减、自动巩固和关联涌现。

### 1.4 为什么现在做

- Shelf 当前用户极少（主要是 MeowAgent 自身），破坏性变更窗口正在收窄
- 竞品 Mem0 已实现统一记忆模型（LOCOMO 91.6），我们必须在本地优先赛道上追平
- 裂脑架构是技术债，"临时方案"已活了两个版本，越晚重构成本越高

---

## 2. 目标与 Non-goals

### 2.1 v0.11.0 目标

| # | 目标 | 一句话 |
|---|------|--------|
| G1 | **统一存储** | 知识树融入主存储，mount_tree 走 store(kind=Knowledge) 批量写入 |
| G2 | **统一 recall** | 一次 recall 返回所有相关记忆，不区分 Episode / Knowledge |
| G3 | **EntangleGraph 跨类型** | Knowledge engram 自动参与图扩散和 Hebbian 边强化 |
| G4 | **Shelf 持久化** | Knowledge engram 存入 LMDB，重启即时可用 |
| G5 | **Dream 覆盖 Knowledge** | Knowledge engram 参与 vitality 衰减、巩固和模式涌现 |
| G6 | **ADD-only 写入** | store() 默认只插入不覆盖，语义去重（threshold 0.9） |
| G7 | **source 追溯** | 每个 Knowledge engram 保留 source_textunit 引用 |

### 2.2 Non-goals（明确不做）

| # | Non-goal | 理由 | 计划 |
|---|----------|------|------|
| N1 | **CodeGraph 集成** | MeowAgent 侧独立，Thalamus 负责双通道协同 | 永不进 MemHop |
| N2 | **云端同步** | 本地优先，文件级同步用户自选（iCloud/Git） | v1.0 考虑 |
| N3 | **文件监听自动 mount** | 需要 FSEvents/inotify 集成，增加系统依赖 | v0.11.1 考虑 |
| N4 | **ShelfDomain trait 实现** | 架构预留但不下沉实现 | v0.11.1 |
| N5 | **SQLite FTS5 全文索引** | 当前 ngram 稀疏索引已提供关键词匹配，FTS5 是优化而非必需 | v0.11.1 P2 |
| N6 | **旧版本 Shelf 兼容** | Shelf 当前用户极少，主理人明确不兼容 | 不做迁移脚本 |
| N7 | **Node2Vec 图嵌入** | 需要训练基础设施，v0.11.0 聚焦架构统一 | v1.0 探索 |
| N8 | **memhop_associations MCP 工具** | 涌现关联需要数据积累验证，先不暴露工具 | v0.11.1 |
| N9 | **多设备 LMDB 同步** | 需要冲突解决策略（CRDT/union-merge） | v1.0 |

### 2.3 停车场（好想法，本期不做）

| 想法 | 来源 | 计划 |
|------|------|------|
| recall_global() 宏观俯瞰模式 | GraphRAG 双模检索启发 | v1.0 |
| Dream 阶段社区检测 + 主题摘要生成 | GraphRAG 社区摘要 | v0.11.1 |
| memhop_context 一站式上下文工具 | CodeGraph context 工具启发 | v0.11.1 |
| Shelf 文件变更增量更新 | CodeGraph 增量同步策略 | v0.11.1 |
| Knowledge engram 冷热分层存储 | 数析建议 | v0.11.1 |
| 垂类 Law/Medical 内置示例 | 用户研究建议 | v0.11.1 |
| mount_shelf --watch 文件监听模式 | Graphify + CodeGraph 启发 | v0.11.1 |

---

## 3. 目标用户与用户故事

### 3.1 目标用户

v0.11.0 的首要受益者是**深度个人用户**（用户分层金字塔的 Phase 1 层）：

- **技术人**：开发者、工程师。日常工作涉及大量代码、技术文档、踩坑记录
- **知识工作者**：需要持续阅读（书/论文/文章）并产出的人
- **"第二大脑"需求者**：已被工具碎片化困扰的个人知识管理者

### 3.2 用户故事

#### 故事 1：统一 recall（P0）

> **作为**一个读了技术书的开发者
> **我想要**一次 recall 就能同时拿到书里的内容和我的讨论笔记
> **以便**我不需要手动拼接两个 MCP 调用的结果

**当前状态**：需要 recall() + knowledge_search() 两次调用，自行合并去重

#### 故事 2：持久可靠（P0）

> **作为**一个挂载了多本书的用户
> **我想要**重启后挂载的知识仍然可用
> **以便**我不需要每次重启后重新 mount 所有文档

**当前状态**：重启后 Shelf 内容全丢，需重新 mount + encode

#### 故事 3：跨类型关联（P1）

> **作为**一个在多个项目间切换的开发者
> **我想要**系统自动告诉我"这本书的内容和我上次的踩坑经验有关联"
> **以便**我能发现意想不到的知识连接

**当前状态**：EntangleGraph 不跨类型，Knowledge chunk 无图连接

#### 故事 4：记忆涌现（P1）

> **作为**一个长期使用 MemHop 的用户
> **我想要**挂载的知识像自己的记忆一样随着时间巩固、关联、浮现
> **以便**我越是频繁使用某段知识，它越容易被召回

**当前状态**：Dream 不处理 Knowledge engram

#### 故事 5：来源可信（P1）

> **作为**一个依赖文档做决策的专业用户
> **我想要**知道每段召回的知识来自哪个文档、哪个章节
> **以便**我能评估信息的权威性和上下文

**当前状态**：Shelf chunk 有路径信息但不在统一 recall 中返回

#### 故事 6：写入简单（P2）

> **作为**一个通过 MeowAgent 和 MemHop 交互的开发者
> **我想要**store() 默认不去覆盖已有记忆，而是自动去重
> **以便**我不需要关心"这条要不要更新还是新增"的决策

**当前状态**：store() 语义对覆盖/新增行为不明确

### 3.3 用户分层与覆盖

```
           ┌──────────────┐
           │ 专业领域       │  ← Phase 2 (v0.11.1) — 本期预留架构
           ├──────────────┤
           │ 深度个人用户    │  ← Phase 1 (v0.11.0) ← 本期核心
           │ 读书 + 聊天     │
           ├──────────────┤
           │ 基础个人用户    │  ← 当前状态，向后兼容
           └──────────────┘
```

---

## 4. 方案设计

### 4.1 核心理念

> **猫在哪，脑就在哪。所有知识都是 engram。** 对话是 engram，书的章节是 engram，论文段落是 engram。区别只在 `kind` 和 `meta`，不在存储位置。

### 4.1.1 脑的路径

```
猫 = 一只 AI Agent
脑 = 一个 MemHop 实例

猫的工作目录/          ← MeowAgent 创建猫时指定
├── brain.db          ← 这只猫的记忆（对话+所有知识树）
└── knowledge/        ← 默认知识树（不指定路径的知识点）
```

- `Brain::open("~/meow/cats/rust-cat/brain.db")` — 脑文件在猫目录下
- 删除猫目录 = 脑一起删，干净
- 一只猫一个脑，多只猫完全隔离

### 4.2 EngramKind 变更

#### 当前类型

```rust
pub enum EngramKind {
    Episode,     // 对话/事件
    Schema,      // 抽象出的模式
    Anchor,      // 场景锚点
    Reflection,  // 自我反思
}
```

#### v0.11.0 新增

```rust
pub enum EngramKind {
    Episode,     // 对话/事件 — 无变化
    Schema,      // 抽象出的模式 — 无变化
    Anchor,      // 场景锚点 — 无变化
    Reflection,  // 自我反思 — 无变化
    Knowledge,   // 【新增】挂载的外部知识（书的章节、论文段落、文档片段）
}
```

#### Knowledge 与已有类型的本质区别

| 维度 | Episode | Knowledge |
|------|---------|-----------|
| 来源 | 对话/交互中产生 | 外部文档挂载导入 |
| 文本长度 | 50-300 chars | 500-2,000 chars |
| 生命周期 | 与对话节奏绑定 | 独立于对话，长期储备 |
| vitality 衰减 | 标准衰减曲线 | 更慢衰减（约 3-5× 更慢） |
| meta 结构 | 轻量（时间戳、会话 ID） | 丰富（source、shelf_id、domain、chunk_index） |
| 在 Hopfield 中的角色 | 主要模式（权重 1.0） | 辅助模式（权重 0.5，参与模式补全但不主导） |
| 去重策略 | 语义相似度 0.95 | 语义相似度 0.9 + source_path 相同 |

### 4.3 统一数据流

```
┌─────────────────────────────────────────────────────────────────┐
│                  mount_tree(path, domain)                        │
├─────────────────────────────────────────────────────────────────┤
│  1. Scanner: 扫描路径，提取文件列表                               │
│  2. Chunker: 按 domain 策略切分                                   │
│  3. Encoder: 批量编码 (1024-dim f16)                              │
│  4. 批量 store():                                                │
│     for each chunk:                                              │
│       store(Engram {                                             │
│         id: "{tree_path}::{chunk_index}",                        │
│         text: "tokio 使用 work-stealing 实现...",                 │
│         kind: Knowledge,                                         │
│         vector: [f16; 1024],                                     │
│         meta: {                                                  │
│           tree_path: "/Users/me/projects/rust-learning",         │  ← 路径即标识
│           source_path: "ch03-scheduler.md",                      │
│           source_textunit: "§3.2 调度器设计",                    │
│           domain: "book",                                        │
│         }                                                        │
│       })                                                         │
│  5. 路径本身就是标识。不需要 shelf_id 抽象。                       │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                    recall("tokio 调度器设计")                     │
├─────────────────────────────────────────────────────────────────┤
│  默认（不指定 tree）：搜索全部 ← 这就是"自然想起"                   │
│     HNSW 召回 (不限 kind):                                       │
│       engram_123 (Episode, "上周决定用 tokio")                    │
│       engram_456 (Knowledge, "work-stealing 原理",                 │
│                    tree: "/Users/me/projects/rust-learning")      │
│       engram_789 (Episode, "上次改 tokio 配置踩坑")                │
│                                                                  │
│  可选指定 tree：recall(query, tree="/Users/me/projects/rust")     │
│     → 搜索：所有对话记忆 + 指定树                                  │
│     → "这本书里怎么讲的"                                          │
│                                                                  │
│  Hopfield + EntangleGraph → 自动关联：                            │
│    "你上周决定用 tokio，关联知识树 /Users/me/projects/rust-learning│
│     第 3 章详细讲了调度器设计"                                    │
│                                                                  │
│  返回统一格式（每条结果自带 tree_path + source_path 标注）         │
└─────────────────────────────────────────────────────────────────┘
```

### 4.4 知识树管理器（原 ShelfManager）

**核心理念：路径即标识。** 不再有抽象的 `shelf_id`。挂载目录的绝对路径就是知识树的唯一标识。

| 旧角色 | 新角色 |
|--------|--------|
| 持有独立 HNSW (ShelfTree) | 不持索引，走主 HNSW |
| 持有内存 HashMap (texts) | 不持内容，内容在 LMDB |
| mount 建独立索引 | mount 走 store(kind=Knowledge) 批量写入 |
| knowledge_search(query, shelf_id) | 通过 recall(query, tree=path) 实现 |
| shelf_id 作为抽象标识 | 路径本身就是标识 |

**职责**：

1. `mount(path, domain)` → scanner + chunker → 对每个 chunk 调用 `brain.store(kind=Knowledge, meta.tree_path=path)`
2. `unmount(path)` → 删除所有 `meta.tree_path == path` 的 engram
3. `get_trees()` → 返回已挂载的目录列表（去重 meta.tree_path），不含内容

### 4.5 store() 新语义：ADD-only + 去重

```
store(engram) 流程:

1. 检查 engram.kind:
   - Episode/Schema/Anchor/Reflection: 语义去重 (cosine > 0.95 → 跳过)
   - Knowledge: 语义去重 (cosine > 0.9) + source_path 相同 → 跳过

2. 如果去重判定为重复:
   - 返回 StoreResult::Duplicate { existing_id }
   - 不修改已有 engram

3. 否则:
   - 生成 engram_id
   - 写入 LMDB
   - 写入 HNSW 索引
   - 写入 Hopfield (Knowledge: weight=0.5, 其他: weight=1.0)
   - 写入 EntangleGraph (初始节点，无边)
   - 返回 StoreResult::Stored { engram_id }
```

**关键行为变更**：
- store() 不再支持"覆盖"已有 engram（之前语义模糊）
- 如果用户意图是"更新"，应显式 forget() + store()
- Dream 阶段负责合并相似 engram（而非 store 时覆盖）

### 4.6 recall() 统一返回格式

```rust
pub struct RecallResult {
    /// 按相关度降序排列的所有 engram（不限 kind）
    pub results: Vec<RankedEngram>,

    /// 知识树上下文：每个涉及的知识树的摘要信息
    pub tree_contexts: HashMap<TreePath, TreeContext>,

    /// EntangleGraph 扩散发现的关键关联
    pub graph_associations: Vec<GraphAssociation>,

    /// 元信息
    pub meta: RecallMeta,
}

pub struct RankedEngram {
    pub engram: Engram,
    pub score: f32,             // 融合分数 (HNSW + BM25 + Hopfield + Graph)
    pub score_breakdown: ScoreBreakdown,
}

pub struct ScoreBreakdown {
    pub semantic_score: f32,    // HNSW cosine 相似度
    pub keyword_score: f32,     // ngram BM25 得分
    pub hopfield_score: f32,    // Hopfield 模式补全贡献
    pub graph_boost: f32,       // EntangleGraph 扩散加权
}

pub struct TreeContext {
    pub tree_path: String,      // 目录绝对路径（即标识）
    pub domain: ShelfDomain,
    pub chunk_count: u32,
    pub mounted_at: String,
}

pub struct GraphAssociation {
    pub description: String,    // 人类可读的关联描述
    pub engram_ids: Vec<EngramId>,
    pub edge_type: EdgeType,    // Semantic / Temporal / CoShelf / Hebbian
}

pub struct RecallMeta {
    pub total_hnsw_candidates: usize,
    pub knowledge_hit_count: usize,
    pub episode_hit_count: usize,
    pub graph_diffusion_depth: usize,
    pub latency_us: u64,
}
```

### 4.7 mount_tree() — 挂载知识树

```
旧行为:
  mount_shelf(path) → 创建独立内存 HashMap + ShelfTree(HNSW + BM25)
  → recall 不可见，重启丢失

新行为:
  mount_tree(path, domain="book") →
    1. Scanner: 扫描 path，提取文件列表
    2. Chunker: 按 domain 策略切分
    3. Encoder: 批量 embedding
    4. 对每个 chunk 调用 brain.store(kind=Knowledge, meta={tree_path: path, ...})
    5. 路径本身就是标识。不需要生成 shelf_id
    6. 返回 { tree_path, chunk_count, domain }

参数:
  - path: String (必需) — 文件或目录路径（即知识树标识）
  - domain: ShelfDomain (可选, 默认 "generic")

返回:
  - tree_path: 挂载的目录路径（即后续 recall 时用的 tree 参数）
  - chunk_count: 写入的 Knowledge engram 数量
  - domain: 使用的领域类型
```

### 4.8 unmount_tree() — 卸载知识树

```
旧行为:
  unmount_shelf(shelf_id) → 从 HashMap 移除 → 内存释放

新行为:
  unmount_tree(tree_path) →
    1. brain.forget_batch(filter: { meta.tree_path == tree_path })
       → 从 LMDB 删除所有 Knowledge engram
       → 从 HNSW 移除节点 (需 HNSW delete API，v0.11.0 软删除)
    2. 返回 { deleted_count, tree_path }

参数:
  - tree_path: String (必需) — 目录路径（即 mount 时用的 path）

返回:
  - deleted_count: 删除的 engram 数量
  - tree_path: 已卸载的路径
```

### 4.9 dream() 支持 Knowledge engram

Dream 阶段的所有操作现在覆盖 Knowledge engram：

| Dream 操作 | Episode 行为 | Knowledge 行为 |
|-----------|-------------|---------------|
| vitality 衰减 | 标准衰减曲线 | **慢速衰减**（约 3-5× 更慢），因为知识储备应比对话记忆更持久 |
| Hebbian 边强化 | ✅ 正常 | ✅ 正常（但 Knowledge↔Knowledge 边的强化速度可配置） |
| Schema 涌现 | ✅ | ✅ Knowledge 聚类可涌现 Schema（如"这本讲异步的书和你踩的 tokio 坑属于同一主题"） |
| 低 vitality 处理 | sleep → archive | sleep → archive（不删除，保留 source 引用可重建） |
| 新关联发现 | ✅ | ✅ 跨类型的意外连接（这是核心差异化价值） |

**Knowledge engram vitality 衰减配置**：

```rust
pub struct VitalityConfig {
    /// Episode 基础衰减率（每次 dream 衰减比例）
    pub episode_decay_rate: f32,       // 默认 0.05

    /// Knowledge 基础衰减率（比 Episode 慢）
    pub knowledge_decay_rate: f32,     // 默认 0.015 (~3.3× 更慢)

    /// 最近激活的 vitality 恢复量
    pub activation_boost: f32,         // 默认 0.1

    /// 最低 vitality（低于此值进入睡眠）
    pub sleep_threshold: f32,          // 默认 0.1

    /// 睡眠后最低 vitality（低于此值归档）
    pub archive_threshold: f32,        // 默认 0.01
}
```

### 4.10 Hopfield 网络：Knowledge engram 参与策略

**问题**：数析指出 Hopfield 网络随 Knowledge engram 线性增长（+100MB @ 100K chunks），建议仅索引 Episode + Schema。但这与"统一记忆"目标矛盾。

**析客决策**：**Knowledge engram 参与 Hopfield，但以可配置的降低权重参与。**

理由：
1. 统一记忆的核心价值是所有 engram 平等参与模式补全——排除 Knowledge 等于保留了裂脑
2. Memory 增长 +100MB 在现代硬件上可接受（RSS < 1GB @ 200K engrams）
3. 如果未来 Knowledge 膨胀超过预期，权重参数可调，不需要架构回滚

```rust
pub struct HopfieldConfig {
    /// 是否将 Knowledge engram 纳入 Hopfield
    pub include_knowledge: bool,        // 默认 true

    /// Knowledge engram 在 Hopfield 中的模式权重
    /// - 1.0: 与 Episode 同等参与
    /// - 0.5: 作为辅助模式（推荐默认值）
    /// - 0.0: 仅作为被扩散目标，不参与模式初始化
    pub knowledge_pattern_weight: f32,  // 默认 0.5

    /// 最大 Hopfield pattern 总数
    pub max_patterns: usize,            // 默认 200,000
}
```

**设计意图**：
- `knowledge_pattern_weight = 0.5` 意味着 Knowledge engram 可以接收 Hopfield 扩散激活，但对模式补全的贡献减半
- 这保留了"知识参与记忆涌现"的语义完整性，同时控制了内存影响
- 如果用户场景偏向纯对话记忆，可配置 `include_knowledge = false`
- 这是一个**可调参数**而非架构开关——可根据实际性能数据在 v0.11.1 中调整默认值

---

## 5. MCP 工具清单与语义变更

### 5.1 工具总览

| 工具 | v0.9.0 状态 | v0.11.0 状态 | 说明 |
|------|------------|-------------|------|
| `memhop_store` | ✅ | ✏️ **语义变更** | ADD-only + 去重，meta.tree_path 替代 shelf_id |
| `memhop_recall` | ✅ | ✏️ **语义变更** | 统一返回，tree 参数替代 shelf_id filter |
| `memhop_mount_tree` | `memhop_mount_shelf` | 🔄 **重命名** | 路径即标识，走 store 批量写入 |
| `memhop_unmount_tree` | `memhop_unmount_shelf` | 🔄 **重命名** | 按 tree_path 批量删除 |
| `memhop_tree_status` | `memhop_shelf_status` | 🔄 **重命名** | 返回已挂载知识树列表 |
| `memhop_dream` | ✅ | ✏️ **语义变更** | 覆盖 Knowledge engram |
| ~~`memhop_knowledge_search`~~ | ✅ | 🗑️ **废弃** | 功能合并进 `memhop_recall(tree=path)` |

### 5.2 详细语义变更

#### memhop_store — ADD-only + 去重

```
MCP tool: memhop_store

Input:
  - text: String (必需)
  - kind: String (可选，默认 "episode")
  - meta: Object (可选)
    - tree_path: String        — 知识树路径 (Knowledge engram 用)
    - source_path: String      — 原始文件相对路径
    - source_textunit: String  — 原文引用，如 "§3.2"
    - confidence: String       — "extracted" / "verified" / "inferred" / "contradicted"

Output:
  - status: "stored" | "duplicate"
  - engram_id: String

行为:
  - 单次调用。批量走 memhop_mount_tree
  - 语义去重: Episode (cosine > 0.95), Knowledge (cosine > 0.9 + same tree_path+source_path)
  - ADD-only，不覆盖
```

#### memhop_recall — 统一返回

```
MCP tool: memhop_recall

Input:
  - query: String (必需)
  - top_k: u32 (可选，默认 10)
  - tree: String (可选)         — 限定知识树路径。不传 = 搜索全部
  - kind: Vec<String> (可选)    — 限定类型 ["episode"/"knowledge"]

Output:
  - results: [{
      id, text, kind, score,
      tree_path: String | null,         // Knowledge engram 标注来源
      source_path: String | null,
      source_textunit: String | null
    }]
  - tree_contexts: {                     // 涉及的知识树摘要
      "/path/to/tree": { domain, chunk_count, mounted_at }
    }
  - graph_associations: [String]         // 人类可读的关联描述

行为:
  - 不指定 tree → 搜索全部 (对话 + 所有知识树)。这就是"自然想起"
  - 指定 tree → 搜索对话 + 指定树。"这本书里怎么讲的"
  - 指定 kind=["episode"] → 只要对话记忆
```

Output:
  - results: [
      {
        id: String,
        text: String,
        kind: String,
        score: f32,
        tree_path: String | null,       // Knowledge engram 标注来源路径
        source_path: String | null,
        source_textunit: String | null
      }
    ]
  - tree_contexts: {                    // 涉及的知识树摘要
      "/path/to/tree": {
        domain: String,
        chunk_count: u32,
        mounted_at: String
      }
    }
  - graph_associations: [String]        // 人类可读的关联描述
  - meta: {
      total_hnsw_candidates: u64,
      knowledge_hit_count: u64,
      episode_hit_count: u64,
      graph_diffusion_depth: u64,
      latency_us: u64
    }

行为:
  - 单次 HNSW 查询，返回所有 kind 的混合结果
  - Knowledge engram 标注 shelf_context（来源追溯）
  - graph_associations 提供跨类型的涌现关联
```

#### knowledge_search — 废弃，映射到 recall

```
MCP tool: knowledge_search  →  【废弃】

映射:
  knowledge_search(query, tree_path)
    → recall(query, tree=tree_path, kind=["knowledge"])

兼容期: v0.11.0 保留工具名但内部映射到 recall，输出 deprecation warning
移除期: v0.11.1 彻底移除

#### memhop_mount_tree — 批量 store

```
MCP tool: memhop_mount_tree

Input:
  - path: String (必需)         — 目录路径（即知识树标识）
  - domain: String (可选, 默认 "generic")

Output:
  - tree_path: String           — 同输入 path
  - chunk_count: u32
  - domain: String

行为:
  - 扫描 → 切分 → 编码 → 批量 store(kind=Knowledge)
  - 路径即标识，不生成 shelf_id
```

#### memhop_unmount_tree — 按路径删除

```
MCP tool: memhop_unmount_tree

Input:
  - tree_path: String (必需)

Output:
  - deleted_count: u32
  - tree_path: String

行为:
  - brain.forget_batch(filter: { meta.tree_path == tree_path })
  - 从 LMDB + HNSW + Hopfield + EntangleGraph 移除
```

#### dream — 覆盖 Knowledge

```
MCP tool: dream

Input: (无变化)
  - 无参数或可选配置

Output: (扩展)
  - processed_count: u64
  - knowledge_processed: u64    — 【新增】
  - schema_emerged: u64
  - vitality_changes: { archived, slept, reactivated }
  - new_associations: u64       — 【新增】跨类型新关联数
  - duration_ms: u64

行为:
  - Knowledge engram 参与 vitality 衰减（慢速衰减曲线）
  - Knowledge engram 参与 Hebbian 边强化
  - Knowledge engram 参与 Schema 涌现
  - Knowledge↔Episode 跨类型关联发现
```

---

## 6. meta Schema 规范

### 6.1 通用 meta 字段（所有 EngramKind）

```rust
pub struct EngramMeta {
    // —— 来源追溯 ——
    /// 产生此 engram 的会话 ID (Episode 特有)
    pub session_id: Option<String>,

    /// 产生此 engram 的时间戳
    pub created_at: DateTime<Utc>,

    // —— 生命周期 ——
    /// 最近一次被 recall 激活的时间
    pub last_activated: Option<DateTime<Utc>>,

    /// 激活次数 (用于 Hebbian 边强化权重)
    pub activation_count: u32,
}
```

### 6.2 Knowledge 特有 meta 字段

```rust
pub struct KnowledgeMeta {
    pub base: EngramMeta,

    // —— 知识树关联 ——
    /// 目录绝对路径（即知识树标识）。不需要 shelf_id 抽象。
    pub tree_path: String,

    /// 所属领域
    pub domain: ShelfDomain,

    // —— 来源追溯 ——
    pub source_path: String,        // 原始文件相对路径
    pub source_textunit: String,    // 引用 "§3.2 调度器设计"

    // —— 分块信息 ——
    pub chunk_index: u32,
    pub chunk_total: u32,

    // —— 置信度 ——
    pub confidence: Confidence,  // Extracted | Verified | Inferred | Contradicted

    // —— 领域扩展 ——
    /// 领域特有的结构化元数据 (如法律: 案件编号、法院; 医学: 疾病、证据等级)
    pub domain_meta: HashMap<String, Value>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum ShelfDomain {
    Generic,
    Book,
    Paper,
    Doc,
    Code,
    Custom(String),
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum Confidence {
    /// 从原文中直接提取 (如逐字引用)
    Extracted,
    /// 经人工或外部权威来源验证
    Verified,
    /// 从原文中推断 (如跨段落综合)
    Inferred,
    /// 来源模糊或存在矛盾信息
    Contradicted,
}
```

### 6.3 meta 字段使用指南

| 字段 | 必需性 | 谁负责填充 | 何时可为空 |
|------|--------|-----------|-----------|
| `created_at` | 必需 | MemHop 自动 | 永不 |
| `last_activated` | 自动 | MemHop recall 管线 | 初始为空 |
| `activation_count` | 自动 | MemHop recall 管线 | 初始为 0 |
| `session_id` | Episode 必需 | MeowAgent 传入 | Knowledge 类型为空 |
| `tree_path` | Knowledge 必需 | mount_tree 参数 | 非 Knowledge 类型为空 |
| `domain` | Knowledge 必需 | mount_tree 参数 | 非 Knowledge 类型为空 |
| `source_path` | Knowledge 强烈建议 | mount_tree 自动 | Knowledge 无源文件时为空 |
| `source_textunit` | Knowledge 强烈建议 | chunker 提取 | chunk 无法定位时为空 |
| `chunk_index` | Knowledge 自动 | chunker 自动 | 非 Knowledge 类型为空 |
| `chunk_total` | Knowledge 自动 | chunker 自动 | 非 Knowledge 类型为空 |
| `confidence` | Knowledge 推荐 | chunker 设置 | 默认为 Extracted |

---

## 7. 技术考量

### 7.1 架构约束

| 约束 | 说明 |
|------|------|
| **LMDB 单文件** | 所有 engram 存在同一个 LMDB 环境，9 个子数据库之一 (`hippocampus`) |
| **HNSW 单实例** | 当前 2 个 HNSW → 1 个。需验证 200K 节点下的搜索延迟 |
| **HNSW delete API 缺失** | `brain.rs:1282` 注释表明当前 `forget()` 跳过 HNSW 删除。unmount_shelf 依赖此能力 |
| **Hopfield pattern 上限** | 当前无上限，需加 `max_patterns` 配置 |
| **Vector dim 固定** | 1024-dim f16，不可变（切换需全量重编码） |
| **不兼容旧版本** | LMDB schema 变更，不做向后兼容。MeowAgent 是唯一下游，它跟着改 |

### 7.2 需要新增/变更的 API

```rust
// —— EngramKind 变更 ——
// engram.rs: 新增 Knowledge 变体

// —— Brain API 变更 ——
impl Brain {
    /// store: ADD-only 语义
    pub fn store(&mut self, engram: Engram) -> Result<StoreResult>;

    /// recall: 统一返回格式
    pub fn recall(&self, query: &str, config: RecallConfig) -> Result<RecallResult>;

    /// forget_batch: 批量删除（unmount 依赖）
    pub fn forget_batch(&mut self, filter: ForgetFilter) -> Result<usize>;

    /// dream: 覆盖 Knowledge
    pub fn dream(&mut self, config: DreamConfig) -> Result<DreamResult>;
}

// —— HNSW API 新增 ——
impl HnswIndex {
    /// delete: 从索引中移除节点（当前缺失）
    pub fn delete(&mut self, id: &NodeId) -> Result<()>;
}

// —— ShelfManager API 变更 ——
impl ShelfManager {
    /// mount: 走 store 批量写入
    pub fn mount(&mut self, brain: &mut Brain, path: &str, config: MountConfig)
        -> Result<MountResult>;

    /// unmount: 批量删除 Knowledge engrams
    pub fn unmount(&mut self, brain: &mut Brain, shelf_id: &str)
        -> Result<UnmountResult>;

    /// knowledge_search: 映射到 brain.recall
    pub fn knowledge_search(&self, brain: &Brain, query: &str, shelf_id: &str)
        -> Result<RecallResult>;

    /// 状态查询（不含内容）
    pub fn get_shelf_status(&self) -> Vec<ShelfMeta>;
    pub fn get_shelf(&self, shelf_id: &str) -> Option<ShelfMeta>;
}
```

### 7.3 存储估算

| 场景 | Episode | Knowledge | LMDB 估算 | RSS 估算 |
|------|---------|-----------|----------|---------|
| 轻量 | 10K | 5K | ~38MB | ~180MB |
| 中等 | 30K | 20K | ~155MB | ~350MB |
| 重度 | 50K | 50K | ~325MB | ~540MB |
| 上限 | 50K | 100K | ~525MB | ~800MB |

基于数析的性能评估，这些数值在 SLA 范围内。

### 7.4 风险与缓解

| # | 风险 | 概率 | 影响 | 缓解 |
|---|------|------|------|------|
| R1 | HNSW delete API 实现复杂度高 | 中 | 高 — unmount 无法完整清理 | P0 优先实现；若不及时完成，退化为标记删除（过滤层跳过已删除节点）+ 定期 compact |
| R2 | Knowledge engram text 过长导致 LMDB 膨胀 | 中 | 中 | 设置 text 截断上限 2,000 chars，超出部分存外部引用 |
| R3 | Hopfield 内存随 Knowledge 线性增长 | 高 | 中 | `knowledge_pattern_weight = 0.5` + `max_patterns` 上限 + 监控 |
| R4 | 单 HNSW 200K+ 节点延迟可感知 | 低 | 中 | 监控 p99；必要时提高 ef_search 或引入冷热分层 |
| R5 | Knowledge vitality 衰减过快（与 Episode 同一曲线） | 中 | 低 | 独立衰减曲线 (`knowledge_decay_rate = 0.015`) |
| R6 | unmount 批量删除耗时过长 | 中 | 低 | 分批次删除 + 异步后台清理 + 指标监控 |
| R7 | store 去重检查增加写入延迟 | 低 | 低 | 去重仅对最近 N 条做近似检查（缓存最近 1000 条向量） |

---

## 8. 成功指标与 SLA

### 8.1 产品成功指标 (KPI)

| # | 指标 | 目标值 | 测量方式 |
|---|------|--------|----------|
| K1 | recall 覆盖 Knowledge 的查询占比 | > 80%（挂载了 Shelf 的用户的 recall 中至少包含 1 条 Knowledge） | recall trace 统计 |
| K2 | MCP 调用次数减少 | > 40%（recall + knowledge_search → 单 recall） | MeowAgent 侧统计 |
| K3 | Shelf 重启可用性 | 100%（重启后无需重新 mount） | 功能验证 |
| K4 | EntangleGraph 跨类型边数量 | > 0（v0.11.0 发布后 1 周内出现 Semantic/CoShelf 类型边） | EntangleGraph metrics |
| K5 | store 去重准确率 | > 95%（手动标注的重复 engram 正确被拦截） | 人工抽样验证 |
| K6 | Dream 覆盖 Knowledge engram | 100%（Dream 统计中 knowledge_processed > 0） | DreamResult 统计 |

### 8.2 性能 SLA（面向 MeowAgent）

基于数析的性能评估，以下为 v0.11.0 的性能承诺：

| 指标 | 目标值 | 测量方式 | 告警阈值 |
|------|--------|----------|---------|
| recall p50 | < 2ms | MCP 端到端（含 JSON 序列化） | > 5ms |
| recall p99 | < 5ms | 同上 | > 10ms |
| store p99 | < 2ms | perceive() 端到端（含 LMDB write + 去重检查） | > 5ms |
| mount_tree (100 chunks) | < 5s | 含 scan + chunk + encode + store | > 15s |
| unmount_tree (100 chunks) | < 1s | 批量删除 Knowledge engrams | > 3s |
| Dream 阶段 | < 30s @ 100K engrams | dream() 总耗时 | > 60s |
| 启动时间 | < 3s @ 100K engrams | Brain::open() 含 HNSW 加载 | > 10s |
| LMDB 磁盘占用 | < 1GB @ 200K engrams | du -sh db/ | > 2GB |
| 内存占用 (RSS) | < 1GB @ 200K engrams | 进程 RSS | > 1.5GB |

### 8.3 降级策略

| 场景 | 操作 |
|------|------|
| recall p99 > 10ms | 自动降低 spread_top_k（10→5），关闭 graph_association 计算 |
| HNSW 节点 > 200K | 触发 Knowledge 冷数据淘汰（按 last_activated 排序） |
| LMDB > 1.5GB | 告警 + 建议用户 unmount 不活跃 Shelf |
| Dream > 60s | 减小 batch size，分多轮执行 |
| store 去重检查超时 | 跳过去重，写入后由 Dream 阶段异步合并 |

---

## 9. 验收标准

### 9.1 统一存储

- [ ] **AC-1.1** Given `mount_tree("/path/to/book")` 完成，Then 所有 chunk 作为 `kind=Knowledge` 存入 LMDB，`recall()` 可检索
- [ ] **AC-1.2** Given 挂载后重启 MemHop，Then 重启后 `recall()` 立即可检索，无需重新 mount
- [ ] **AC-1.3** Given 已挂载知识树，Then 系统不持有独立 HNSW 或内容 HashMap

### 9.2 统一 recall

- [ ] **AC-2.1** Given 不指定 tree，Then `recall("topic")` 返回 Episode + Knowledge 混合结果
- [ ] **AC-2.2** Given Knowledge 结果，Then 每条附带 `tree_path, source_path, source_textunit`
- [ ] **AC-2.3** Given `recall("query", tree="/path")`，Then 返回对话记忆 + 指定树
- [ ] **AC-2.4** Given `recall("query", kind=["episode"])`，Then 仅返回对话记忆

### 9.3 EntangleGraph 跨类型

- [ ] **AC-3.1** Given 同树的两个 Knowledge engram，When mount 完成，Then 自动建立 CoTree 边
- [ ] **AC-3.2** Given Knowledge 与 Episode 语义相关，When Dream 后，Then 存在 Semantic 边
- [ ] **AC-3.3** Given recall 有图扩散发现，Then `graph_associations` 包含可读关联

### 9.4 ADD-only store

- [ ] **AC-4.1** Given 已有相似文本（cosine > 0.95），When store，Then 返回 duplicate
- [ ] **AC-4.2** Given 不相似文本（cosine < 0.9），When store，Then 返回 stored

### 9.5 知识树管理

- [ ] **AC-5.1** Given 用户挂载了 PDF 书籍，When recall 返回 Knowledge engram，Then 每个 engram 的 meta 包含 `source_path` 和 `source_textunit`
- [ ] **AC-5.2** Given 用户挂载了多章节目录的文档，When recall 返回不同章节的 chunk，Then `source_textunit` 能区分不同章节（如 "第3章 §3.2" vs "第5章 §5.1"）

### 9.6 mount_shelf / unmount_shelf

- [ ] **AC-6.1** Given 用户调用 `mount_shelf("/path/to/doc.pdf", domain="paper")`，When mount 完成，Then 返回 `{ shelf_id, chunk_count, domain, source_path, warnings }`
- [ ] **AC-6.2** Given 空目录被 mount，Then 返回 `warnings: ["No readable files found in /path/to/empty"]`
- [ ] **AC-6.3** Given 已挂载的 shelf_id，When 调用 `unmount_shelf(shelf_id)`，Then 所有关联 Knowledge engram 被删除，ShelfManager 元数据被清除，返回 `{ shelf_id, deleted_count }`
- [ ] **AC-6.4** Given 不存在的 shelf_id，When 调用 `unmount_shelf("nonexistent")`，Then 返回错误 `ShelfNotFound`

### 9.7 Dream 覆盖 Knowledge

- [ ] **AC-7.1** Given Knowledge engram 长期未被 recall，When Dream 运行多次后，Then 其 vitality 衰减但速度慢于同等条件的 Episode engram（约 3-5× 更慢）
- [ ] **AC-7.2** Given Dream 运行，Then `DreamResult.knowledge_processed > 0`
- [ ] **AC-7.3** Given Dream 运行后，Then recall 的 `graph_associations` 中可能出现跨类型的新关联（如 Knowledge↔Episode）

### 9.8 knowledge_search 废弃

- [ ] **AC-8.1** Given 用户调用 `knowledge_search("query", "shelf_id")`，When v0.11.0 处理，Then 内部映射到 `recall(query, filter: {kind: [Knowledge], shelf_id: [shelf_id]})`，返回 RecallResult 格式，同时输出 deprecation warning
- [ ] **AC-8.2** Given deprecation warning，Then warning 信息明确指向 `recall` 作为替代方案

### 9.9 破坏性变更

- [ ] **AC-9.1** Given v0.9.0 的 LMDB 数据库，When v0.11.0 尝试打开，Then 返回明确的版本不兼容错误（不静默失败）
- [ ] **AC-9.2** Given v0.11.0 首次运行，Then 创建新的 LMDB schema（含 Knowledge engram 支持）

---

## 10. 里程碑与时间线

### Phase 1: v0.11.0 统一存储重构（本期）

```
Week 1-2: 核心数据结构变更
  ├── EngramKind::Knowledge 定义 + meta schema
  ├── LMDB schema migration (v1 → v2, 不兼容旧版)
  └── EngramMeta 结构扩展

Week 3-4: 存储层统一
  ├── store() ADD-only 语义 + 去重逻辑
  ├── mount_shelf → store 批量写入
  ├── unmount_shelf → forget_batch 批量删除
  └── HNSW delete API 实现

Week 5-6: recall 统一 + EntangleGraph
  ├── recall 统一返回格式 (RecallResult)
  ├── EntangleGraph 跨类型边 (Semantic/CoShelf)
  ├── recall filter 支持 (kind/shelf_id/domain)
  └── graph_associations 计算

Week 7-8: Dream + Hopfield 扩展
  ├── Knowledge engram vitality 独立衰减曲线
  ├── Hopfield knowledge_pattern_weight 参数
  ├── Dream 阶段跨类型关联发现
  └── knowledge_search → recall 映射 + deprecation

Week 9-10: MCP 层适配 + 测试
  ├── MCP 工具清单更新
  ├── 集成测试（完整 mount → store → recall → dream → unmount 链路）
  ├── 性能回归测试（vs v0.9.0 基线）
  └── MeowAgent 适配 + 联调
```

### Phase 2: v0.11.1 垂类扩展（后续）

- ShelfDomainTrait 框架实现
- Law/Medical 内置示例
- SQLite FTS5 并行全文索引 (P2)
- memhop_context 一站式上下文工具
- recall 三路融合：语义 + BM25 + EntangleGraph 扩散
- Dream 社区检测 + 主题摘要生成

### Phase 3: v1.0.0 文件同步与协作（远期）

- 文件级 LMDB 同步 (iCloud/Git)
- 团队轻量共享（只读 token + 手动同步）
- Node2Vec 图嵌入 (探索)

---

## 11. 开放问题

以下问题需要产品团队在评审中讨论并关闭：

| # | 问题 | 背景 | 建议方向 |
|---|------|------|---------|
| Q1 | **Knowledge vitality 衰减速率的确切值** | 瑞思说"外部知识和对话记忆不应该用相同曲线"，析客建议 3-5× 更慢。但具体倍数需要实验数据 | 默认 0.015（~3.3×），在 v0.11.0 中标记为可配置，待使用数据校准 |
| Q2 | **HNSW delete 的实现优先级** | 当前 `forget()` 跳过 HNSW 删除。如果没有 delete，unmount 只能做标记删除 + 过滤层跳过 | 析客建议 P0 实现 delete API；若 v0.11.0 时间不够，标记删除作为 fallback，v0.11.1 补 delete |
| Q3 | **Knowledge engram 的 chunk 策略默认值** | 愿景文档提到了自定义 chunker，但 v0.11.0 不做 trait。默认策略是什么？ | 默认按段落切分（双换行），max 500 chars，overlap 50 chars。domain 参数预留但暂不实现差异化 |
| Q4 | **去重检查的性能开销** | store 前做语义去重需要额外的向量检索，可能增加 store 延迟 | 析客建议：仅对最近 1000 条做近似检查（EngramCache 内比对），不做全量 HNSW 检索 |
| Q5 | **`confidence` 字段的填充策略** | 竞析建议借鉴 Graphify 的置信度标签，v0.11.0 已定义完整 Confidence 枚举（Extracted/Verified/Inferred/Contradicted）。但自动推理逻辑尚未实现 | v0.11.0 schema 就位，默认值 Extracted。v0.11.1 的 domain extractor 接管自动推理（如法律领域可标记 Verified） |
| Q6 | **mount_shelf 大文件超时处理** | 一个 PDF 可能切出数千 chunk，批量 store 可能超时 | mount_shelf 设计为同步返回但分批写入。单次 mount 上限 10,000 chunks。超大文件建议先外部预处理 |
| Q7 | **Hopfield knowledge_pattern_weight 的确切值** | 析客建议 0.5，但没有实验数据支撑。0.0（排除）、0.5（辅助）、1.0（平等）三个值的效果差异未知 | 默认 0.5，可配置。v0.11.1 根据 recall 质量基准测试校准 |
| Q8 | **`shelf_contexts` 的聚合粒度** | recall 可能返回同一 Shelf 的多个 chunk，shelf_contexts 是每个 chunk 都带还是聚合？ | 每个 chunk 附带自己的 shelf_context（含 source_textunit），顶层 shelf_contexts 做聚合摘要（去重） |

---

## 12. 附录

### A. 产品叙事建议

来自瑞思的用户研究：

> **对外叙事**："挂载一本书，它就成为你记忆的一部分。不是搜索，是记忆。"
>
> **避免**："我们重构了存储架构，把 Shelf 融入了主引擎"（技术叙事，用户听不懂）

### B. 术语表

| 术语 | 定义 |
|------|------|
| Engram | 记忆单元，MemHop 的最小存储和检索单位 |
| Knowledge engram | 新增类型，表示从外部文档挂载的知识片段 |
| Episode engram | 对话/交互中产生的记忆 |
| Shelf | 外部文档的逻辑分组，挂载后其内容成为 Knowledge engram |
| ShelfManager | 管理 Shelf 元数据的组件（v0.11.0 退化为元数据管理者） |
| EntangleGraph | MemHop 的核心图结构，记录 engram 之间的关联边 |
| Hebbian 学习 | 使用中强化关联边的机制（fire together, wire together） |
| Dream | 离线阶段的记忆巩固处理（vitality 衰减 + 关联发现 + Schema 涌现） |
| ADD-only | store() 的新语义：只新增不覆盖 |
| Hopfield 网络 | 模式补全网络，从部分线索恢复完整记忆 |

### C. 竞品对齐速查

| MemHop v0.11.0 特性 | 借鉴来源 | 对齐程度 |
|---------------------|---------|---------|
| ADD-only store | Mem0 | 完全对齐 |
| source_textunit 追溯 | GraphRAG | 完全对齐 |
| 统一 recall | Mem0 / 自我创新 | 对齐 + 增强（图扩散） |
| Knowledge engram 参与 Dream | 独有 | 无竞品 |
| EntangleGraph 跨类型 | 独有 | 无竞品（竞品全空白区） |
| meta.confidence 字段 | Graphify | schema 就位，自动推理延后到 v0.11.1 |
| recall filter (kind/shelf_id/domain) | ChromaDB 启发 | 对齐 |

### D. 参考文档

- `memhop/src/engram.rs` — Engram + EngramKind 定义
- `memhop/src/brain.rs` — recall 管线 + store + dream
- `memhop/src/hnsw.rs` — HNSW 参数和 API
- `memhop/src/shelf/mod.rs` — Shelf 当前实现（待重构）
- `memhop/src/shelf/tree.rs` — ShelfTree 双索引（待移除）
- `memhop/src/storage.rs` — LMDB 9 sub-databases
- `memhop-mcp-server/src/main.rs` — MCP 工具清单
- `benchmarks/reports/encoder_comparison_20260528_192655.json` — 检索质量基线

---

> **本 PRD 由析客（Specky）基于四方上游文档综合分析产出。关键架构决策（Hopfield Knowledge 权重、去重策略、HNSW delete 优先级）标记为"析客决策"，需方向明（Fang）最终审定。**
