# MemHop 脑回路开发计划（嵌套 Loop 版）

**日期**：2026-05-31
**前置**：[产品设计规格书](./system-audit-first-tier-plan-2026-05-31.md)
**版本**：v2.0

---

## 核心结构：五层嵌套 Loop

```
┌─────────────────────────────────────────────────────────────┐
│ Loop 0: Person Loop（人）                                    │
│                                                             │
│  ┌───────────────────────────────────────────────────────┐ │
│  │ for each 知识树 in person.trees:                       │ │
│  │   ┌─────────────────────────────────────────────────┐ │ │
│  │   │ Loop 1: Tree Loop（树）                          │ │ │
│  │   │                                                 │ │ │
│  │   │  ┌───────────┐  ┌──────────┐  ┌─────────────┐  │ │ │
│  │   │  │ Loop 1a    │  │ Loop 1b  │  │ Loop 1c     │  │ │ │
│  │   │  │ Perceive   │  │ Recall   │  │ Compress    │  │ │ │
│  │   │  │ 感知循环   │  │ 召回循环 │  │ 压缩循环    │  │ │ │
│  │   │  └─────┬─────┘  └────┬─────┘  └──────┬──────┘  │ │ │
│  │   │        │             │               │         │ │ │
│  │   │        └─────────────┴───────────────┘         │ │ │
│  │   │                  共享树上下文                     │ │ │
│  │   └─────────────────────────────────────────────────┘ │ │
│  └───────────────────────────────────────────────────────┘ │
│                                                             │
│  ┌───────────────────────────────────────────────────────┐ │
│  │ Loop 2: Entanglement Loop（纠缠）                      │ │
│  │   跨树 recall → 检测 → 创建 EntanglementEvent         │ │
│  └───────────────────────────────────────────────────────┘ │
│                                                             │
│  ┌───────────────────────────────────────────────────────┐ │
│  │ Loop 3: Dream Loop（梦境）                              │ │
│  │   NREM(树内合并) → REM(跨树发现) → Worldview(三观)    │ │
│  └───────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

---

## Loop 0: Person Loop（人）

```
┌──────────────────────────────────────────────────────┐
│                    Loop 0: Person                     │
│                                                      │
│   person {                                           │
│     trees: [工作树, 旅游树, 孩子树]                     │
│                                                      │
│     on_input(input):                                 │
│       tree = identify_tree(input)   ← Loop 0 唯一职责 │
│       tree.perceive(input)          ← 委托给 Loop 1  │
│                                                      │
│     on_recall(query):                                │
│       results = []                                   │
│       for each tree:                                 │
│         results += tree.recall(query) ← 委托给 Loop 1│
│       entangle(results)              ← 触发 Loop 2   │
│       return results                                 │
│                                                      │
│     on_dream():                                      │
│       for each tree: tree.dream()   ← 委托给 Loop 1  │
│       entangle_emerge()             ← 触发 Loop 2    │
│       worldview_emerge()            ← 触发 Loop 3    │
│   }                                                  │
└──────────────────────────────────────────────────────┘
```

**Loop 0 的输入/输出**：

| | 输入 | 输出 |
|---|------|------|
| perceive | 用户文本 + 会话上下文 | 归属树 ID + 调用 Loop 1a |
| recall | 查询文本 | 所有树的合并结果 + 纠缠事件 |
| dream | 无（定时触发） | 调用 Loop 1c + Loop 2 + Loop 3 |

**Loop 0 的唯一新增逻辑**：`identify_tree(input)` —— 语义匹配 + 最近活跃树优先。

**和现有代码的关系**：Brain struct 现有字段 `trees: HashMap<String, Tree>` 需要新增。其余逻辑通过调用现有 Brain API 完成。

---

## Loop 1: Tree Loop（树）

```
┌──────────────────────────────────────────────────────┐
│                   Loop 1: Tree                       │
│                                                      │
│   tree {                                             │
│     id, name, domain                                 │
│     shelves: [书架1, 书架2]                           │
│                                                      │
│     ┌────────────────┐                               │
│     │ Loop 1a         │  perceive(input)             │
│     │ Perceive Loop  │                               │
│     │ 感知循环        │  1. store engram(tree_id)    │
│     │                │  2. match shelf content       │
│     │                │  3. detect topic boundary     │
│     │                │  4. if ended → compress       │
│     └────────────────┘                               │
│                                                      │
│     ┌────────────────┐                               │
│     │ Loop 1b         │  recall(query)               │
│     │ Recall Loop    │                               │
│     │ 召回循环        │  1. HNSW + BM25 candidate    │
│     │                │  2. CrossEncoder rerank       │
│     │                │  3. recall shelf knowledge    │
│     │                │  4. merge + return            │
│     └────────────────┘                               │
│                                                      │
│     ┌────────────────┐                               │
│     │ Loop 1c         │  compress(plan)              │
│     │ Compress Loop  │                               │
│     │ 压缩循环        │  1. merge dialogue turns     │
│     │                │  2. create summary engram     │
│     │                │  3. archive original turns    │
│     └────────────────┘                               │
│   }                                                  │
└──────────────────────────────────────────────────────┘
```

### Loop 1a: Perceive Loop（感知循环）

```
┌──────────────────────────────────────────────────────────┐
│                    Loop 1a: Perceive                      │
│                                                          │
│  input ──→ PlanGate.boundary_score()                     │
│     │          ↓                                         │
│     │     score > 0.7? ──→ compress pending plan         │
│     │          ↓                                         │
│     ├──→ encode_text(input) ──→ vector                   │
│     │          ↓                                         │
│     ├──→ store_engram(Episode, tree_id=...)              │
│     │          ↓                                         │
│     ├──→ match_shelves(input, tree.shelves)              │
│     │     │                                              │
│     │     └──→ for each shelf:                           │
│     │            shelf.recall(input) ──→ shelf_results   │
│     │          ↓                                         │
│     ├──→ plan_id = matched_plan or new_plan              │
│     │          ↓                                         │
│     ├──→ persist PlanNode + DialogueTurn                 │
│     │          ↓                                         │
│     └──→ return { engram_id, shelf_matches, plan_id }    │
│                                                          │
│  输出:                                                   │
│    engram_id: 新建的记忆 ID                               │
│    shelf_matches: 与本轮输入相关的书架内容                  │
│    plan_id: 本轮归属的 Plan                               │
│    topic_ended: 是否触发了压缩                             │
└──────────────────────────────────────────────────────────┘
```

**shelf.match() 如何工作**：
```
input: "Rust 的异步模型怎么设计"
  ↓
tree.shelves 中有 /books/rust-async
  ↓
recall(query="Rust 异步模型", tree="/books/rust-async", kind=Knowledge, limit=5)
  ↓
返回: ["第3章: async/await 是协作式调度", "第7章: Pin 确保 Future 不被移动"]
```

**和现有代码的关系**：
- PlanGate 已实现 ✅
- store_engram 已实现 ✅
- PlanNode / DialogueTurn 已实现 ✅
- **新增**：shelf matching 步骤
- **新增**：topic boundary → compress 接线

### Loop 1b: Recall Loop（召回循环）

```
┌──────────────────────────────────────────────────────────┐
│                    Loop 1b: Recall                        │
│                                                          │
│  query ──→ mode dispatch                                 │
│     │                                                    │
│     ├──→ Retrieval 模式:                                  │
│     │     ┌──────────────────────────────────────┐       │
│     │     │ Loop 1b-i: Retrieval Pipeline         │       │
│     │     │                                       │       │
│     │     │ HNSW.search(query, k=80)              │       │
│     │     │     +                                  │       │
│     │     │ SparseIndex.bm25_search(query, k=80)  │       │
│     │     │     ↓                                  │       │
│     │     │ BM25-HNSW fusion (0.4*BM25 + 0.6*cos) │       │
│     │     │     ↓                                  │       │
│     │     │ CrossEncoder.rerank(query, top-20)    │       │
│     │     │     ↓                                  │       │
│     │     │ return top-K                          │       │
│     │     └──────────────────────────────────────┘       │
│     │                                                    │
│     └──→ Associative 模式:                                │
│           ┌──────────────────────────────────────┐       │
│           │ Loop 1b-ii: Associative Pipeline      │       │
│           │                                       │       │
│           │ PGT → Hopfield → competitive_spread   │       │
│           │     ↓                                  │       │
│           │ emotional_weight (not sort!)           │       │
│           │     ↓                                  │       │
│           │ contradiction_detect                   │       │
│           └──────────────────────────────────────┘       │
│                                                          │
│  两个模式共同的尾部:                                       │
│     │                                                    │
│     ├──→ tree knowledge recall:                           │
│     │     recall(tree_id, kind=Knowledge, query)          │
│     │          ↓                                          │
│     │     tree.knowledge_results                          │
│     │                                                    │
│     ├──→ plan context recall:                             │
│     │     命中 engram → plan_id → compressed_summary      │
│     │          ↓                                          │
│     │     plan_contexts                                   │
│     │                                                    │
│     └──→ return RecallResponse {                          │
│            associations,    // 树内对话记忆                │
│            knowledge,       // 树内书架知识                │
│            plan_contexts,   // 当前话题上下文               │
│            tree_contexts,   // 跨所有树的命中分布           │
│            cross_tree_hits, // 其他树的命中 ← Loop 2 输入  │
│          }                                                │
└──────────────────────────────────────────────────────────┘
```

**和现有代码的关系**：
- Retrieval Pipeline (1b-i) 已基本实现，缺 CrossEncoder ✅⚠️
- Associative Pipeline (1b-ii) 已实现 ✅
- tree knowledge recall 已实现（`tree` + `kind_filter` 参数）✅
- plan context recall **新增** ❌
- `cross_tree_hits` 字段 **新增** ❌

### Loop 1c: Compress Loop（压缩循环）

```
┌──────────────────────────────────────────────────────────┐
│                   Loop 1c: Compress                       │
│                                                          │
│  触发条件:                                               │
│    1. PlanGate boundary_score > 0.7（话题切换）           │
│    2. Plan 内 turn 数 > 10（累积阈值）                    │
│    3. 手动调用 compress_plan(plan_id)                    │
│                                                          │
│  执行流程:                                               │
│                                                          │
│  plan_id ──→ get all DialogueTurns in this plan           │
│     │                                                    │
│     ├──→ 如果 dialague_turns.len() < 3: skip             │
│     │                                                    │
│     ├──→ LLM compress:                                   │
│     │     prompt = "将以下多轮对话压缩为一条摘要记忆"       │
│     │     input = turns[0].user_input +                   │
│     │             turns[0].agent_response +               │
│     │             turns[1].user_input + ...               │
│     │     ↓                                              │
│     │     summary = llm.generate(prompt, input)           │
│     │                                                    │
│     ├──→ 无 LLM 时启发式降级:                              │
│     │     summary = turns.last().agent_response           │
│     │     或提取 turns 中重复出现的关键词组成摘要           │
│     │                                                    │
│     ├──→ 创建新的 Engram(Episode):                        │
│     │     text = summary                                 │
│     │     kind = Episode                                 │
│     │     tree_id = tree.id                              │
│     │     meta.compressed_from = [turn1_id, turn2_id, ...│
│     │                                                    │
│     ├──→ 标记原始 turns 为 archived:                       │
│     │     原始 engram.is_archived = true                  │
│     │     （不在常规 recall 中出现，但可查历史）            │
│     │                                                    │
│     └──→ 写回 PlanNode.compressed_summary                 │
│                                                          │
│  输出:                                                   │
│    compressed_engram_id: 压缩后的记忆 ID                   │
│    archived_count: 被归档的原始 engram 数量                │
└──────────────────────────────────────────────────────────┘
```

**和现有代码的关系**：
- compress_plan() 函数已存在但从未被自动触发 ✅⚠️
- Turn Crystallizer 在 Dream 中做类似的事 ✅
- **新增**：压缩产物写为新 Engram
- **新增**：原始 turns 标记 archived
- **新增**：PlanGate 接线触发

---

## Loop 2: Entanglement Loop（纠缠）

```
┌──────────────────────────────────────────────────────────┐
│                 Loop 2: Entanglement                      │
│                                                          │
│  触发条件:                                               │
│    1. recall 结果中 cross_tree_hits 非空                  │
│    2. Dream REM 阶段 cross_anchor_discovery 发现跨树关联  │
│    3. Plan 压缩时发现跨树模式                             │
│                                                          │
│  执行流程:                                               │
│                                                          │
│  ┌────────────────────────────────────────────────┐     │
│  │ Loop 2a: Recall-time Entanglement Detection    │     │
│  │                                                │     │
│  │ recall 结果:                                    │     │
│  │   工作树: [engram-A1, engram-A2]  score > 0.8   │     │
│  │   旅游树: [engram-B1]             score > 0.8   │     │
│  │       ↓                                        │     │
│  │  至少 2 棵树、每个至少 1 条、score > 0.8         │     │
│  │       ↓                                        │     │
│  │  创建 EntanglementEvent {                       │     │
│  │      nodes: [A1, A2, B1],                     │     │
│  │      trees: ["work", "travel"],                │     │
│  │      context: "Rust所有权模型与枯山水极简哲学"    │     │
│  │      trigger: RecallCrossTree,                 │     │
│  │      strength: 0.3  (首次创建，低强度)           │     │
│  │  }                                             │     │
│  │       ↓                                        │     │
│  │  如果已有同组 nodes 的事件 → strength += 0.2     │     │
│  └────────────────────────────────────────────────┘     │
│                                                          │
│  ┌────────────────────────────────────────────────┐     │
│  │ Loop 2b: Dream-time Entanglement Discovery     │     │
│  │                                                │     │
│  │ Dream REM 阶段:                                 │     │
│  │   cross_anchor_discovery() → 跨 Anchor 候选列表  │     │
│  │       ↓                                        │     │
│  │   对每个跨 Anchor 候选:                          │     │
│  │     if 语义相似度 > 阈值:                        │     │
│  │       create EntanglementEvent                  │     │
│  └────────────────────────────────────────────────┘     │
│                                                          │
│  ┌────────────────────────────────────────────────┐     │
│  │ Loop 2c: Recall-time Entanglement Expansion    │     │
│  │                                                │     │
│  │ recall 命中 engram-A1:                         │     │
│  │   → 查参与的 EntanglementEvent                  │     │
│  │   → 展开所有 nodes: [A2, B1]                   │     │
│  │   → 附加到 RecallResponse.entangled_results     │     │
│  └────────────────────────────────────────────────┘     │
└──────────────────────────────────────────────────────────┘
```

**和现有代码的关系**：
- AssociationKind::CrossTree 边已存在 ✅
- Dream REM cross_anchor_discovery 已存在 ✅
- **新增**：EntanglementEvent 数据结构 + LMDB 表 ❌
- **新增**：Loop 2a（recall 时检测）❌
- **新增**：Loop 2c（recall 时展开）❌

---

## Loop 3: Worldview Loop（三观）

```
┌──────────────────────────────────────────────────────────┐
│                   Loop 3: Worldview                       │
│                                                          │
│  触发条件: Dream REM 阶段（每次 dream 都跑）               │
│  前置条件: EntanglementEvent 数量 ≥ 10                    │
│                                                          │
│  执行流程:                                               │
│                                                          │
│  ┌────────────────────────────────────────────────┐     │
│  │ Loop 3a: Pattern Clustering                    │     │
│  │                                                │     │
│  │ all_entanglement_events:                       │     │
│  │   [                                            │     │
│  │     {nodes: [枯山水, API设计], context: "极简"}  │     │
│  │     {nodes: [禅修, 代码重构], context: "简化"}   │     │
│  │     {nodes: [断舍离, 模块拆分], context: "减法"}  │     │
│  │     {nodes: [跑步, 性能优化], context: "耐力"}   │     │
│  │   ]                                            │     │
│  │       ↓                                        │     │
│  │  对 context 文本做 encoder 编码 → 语义聚类        │     │
│  │       ↓                                        │     │
│  │  聚类结果:                                      │     │
│  │    簇1: [极简, 简化, 减法] → 3 次出现             │     │
│  │    簇2: [耐力] → 1 次出现                        │     │
│  └────────────────────────────────────────────────┘     │
│                                                          │
│  ┌────────────────────────────────────────────────┐     │
│  │ Loop 3b: Pattern Stabilization                 │     │
│  │                                                │     │
│  │ 对每个聚类簇:                                    │     │
│  │   if len(cluster) >= 5 AND stability > 0.7:    │     │
│  │     创建 WorldviewPattern {                     │     │
│  │       pattern: "反复将极简美学迁移到技术设计"    │     │
│  │       category: ThinkingStyle,                 │     │
│  │       occurrence_count: 5,                     │     │
│  │       stability: 0.8,                          │     │
│  │     }                                          │     │
│  │                                                │     │
│  │ stability 公式:                                 │     │
│  │   stability = min(1.0, occurrence_count / 10)   │     │
│  │             * avg_strength_of_source_events     │     │
│  └────────────────────────────────────────────────┘     │
│                                                          │
│  ┌────────────────────────────────────────────────┐     │
│  │ Loop 3c: Worldview → Recall                    │     │
│  │                                                │     │
│  │ recall 时:                                      │     │
│  │   查所有 WorldviewPattern                       │     │
│  │   → 附加到 RecallResponse.worldview_context      │     │
│  │                                                │     │
│  │ 新输入检测:                                      │     │
│  │   输入语义与 WorldviewPattern 矛盾?               │     │
│  │   → 标记 cognitive_conflict                    │     │
│  │   → 作为 conflicts 返回                         │     │
│  └────────────────────────────────────────────────┘     │
└──────────────────────────────────────────────────────────┘
```

**和现有代码的关系**：
- Dream REM 已存在 ✅
- **新增**：WorldviewPattern 数据结构 + LMDB 表 ❌
- **新增**：Loop 3a（聚类）❌
- **新增**：Loop 3b（模式稳定化）❌
- **新增**：Loop 3c（三观反向影响召回）❌

---

## 架构问题全面分析

### 问题 1：树归属的可靠性 + 活跃上下文匹配

**问题**：`identify_tree(input)` 的准确率是整个系统的基础。如果归属错误，后续的召回、压缩、纠缠全部受影响。

**场景**：用户在聊"用 Rust 写了个旅游规划工具"——这属于工作树还是旅游树？

**核心方案：Active Context Set（活跃上下文集）**

不同于全量检索 + 单快照的方案，MemHop 采用人脑的**多窗口工作记忆**机制：

```
Brain 内部维护 active_contexts: Vec<ContextSnapshot> (max 5 个)

每个 ContextSnapshot:
├── id: String
├── tree_id: Option<String>
├── plan_id: Option<String>
├── centroid: Vec<f16>               // 语义中心
├── summary: String                  // 最近压缩摘要
├── last_active: i64                 // 最后活跃时间
├── hit_count: u32                   // 命中次数（置信度）
└── turns: Vec<TurnRef>              // 本轮会话属于它的轮次

Perceive 流程:
  input → encode → vector
    → 先匹配 active_contexts (cosine > 0.75)
    → 匹配上: 在该上下文内操作，不污染其他树
    → 没匹配上: 全量 identify_tree → 新 ContextSnapshot

Recall 流程:
  query → 如果在活跃上下文中 → 上下文内检索（不搜全量！）
         → 如果不在 → 全量检索 + 自动创建新 ContextSnapshot
```

这解决了**交错话题**场景下的噪音和失忆率问题。详见 [memhop-vs-everos-deep-analysis.md](./memhop-vs-everos-deep-analysis.md) 第 6 节。

**缓解**：
- 活跃上下文优先匹配（同话题连续对话不走全量检索）
- 降噪：不同上下文的记忆自动隔离
- 关键词权重（"Rust" → 工作树偏重，"规划" → 可能多树）
- 不确定时显式询问用户（"这条记忆归到哪个领域？"）
- 允许手动迁移（`memhop_move_to_tree(engram_id, target_tree)`）

## 问题 2：书架映射与知识图召回集成

**问题**：每个书架需要按目录结构挂载，用户有多少个目录就有多少个书架。书架挂载后，检索记忆时如何自然地带上书架里相关内容？

**场景**：
```
用户: "Rust 的异步模型怎么设计"
  → 应该自动检索相关书架内容:
    /books/rust-async 第3章: "async/await 是协作式调度"
    /books/rust-async 第7章: "Pin 确保 Future 不被移动"
  → 这些应该作为 Knowledge 类型的 Engram 伴随召回结果返回
  → 不需要用户手动调用 knowledge_search()
```

**当前实现** (v0.11.0)：
- `ShelfManager` 已实现：扫描目录 → 分块 → 存为 Knowledge Engram
- `Brain::recall()` 已支持 `tree` 和 `kind_filter` 参数
- `RecallResponse.knowledge_memories` 字段已存在
- 缺的是：perceive/recall 时**自动**附带相关知识

**核心方案：知识图自动附带机制**

```
挂载路径就像给记忆"插上书签":

挂载 /projects/meow/memhop (domain=Code)
  → 扫描所有 .rs 文件 → chunk → 存为 Knowledge Engram
  → 建立 CoShelf 边（同文件相邻 chunk 互相连接）
  → 注册 TreeMeta 到 ShelfManager

挂载 /books/rust-async (domain=Book)
  → 扫描所有 .md 文件 → chunk → 存为 Knowledge Engram
  → 按章节分块自动建立 CoShelf 边
  → 注册 TreeMeta

检索时的自动附带:

recall("Rust async 怎么设计")
  ├── 1. 活跃上下文匹配 → 命中"项目"树
  ├── 2. 在"项目"树范围内检索
  │     ├── Engram(Episode): "我们讨论了 Rust async"
  │     └── Engram(Knowledge, tree_path="/books/rust-async"): 
  │           "async/await 是协作式调度"
  │
  ├── 3. 自动附带相关书架内容
  │     RecallResponse {
  │       associations: [...],
  │       knowledge_memories: [  ← 自动附带
  │         "async/await 是协作式调度 (来源: /books/rust-async 第3章)",
  │         "Pin 确保 Future 不被移动 (来源: /books/rust-async 第7章)"
  │       ],
  │       tree_contexts: [
  │         { tree_path: "/books/rust-async", domain: "book", source_count: 2 }
  │       ]
  │     }
  │
  └── 4. 不需要用户手动 knowledge_search()

自动附带的触发条件:
  - recall 的 query 与某个 Knowledge Engram 的 cosine > 0.6
  - 或 recall 命中的 Plan 关联了某个书架路径
  - (不需要每次都带，避免噪音)
```

**书架内容更新**：
- `mount_tree(path)` 做一次性索引，不监听文件变更
- 提供 `remount_tree(path)` 重新扫描（增量，只索引新增/修改的文件）
- v1.0 不做 FSEvents/文件监听

## 问题 3：压缩的不可逆性 + 纠缠图只存摘要

**问题**：压缩后原始 turns 被 archived，如果压缩质量差，原始信息永久丢失。同时纠缠图里不应该存原始对话轮次，应该只存压缩后的 Knowledge 摘要。

**场景**：
```
Plan "登录页面" (Turn 1,3,5,6) 完成后:
  → 纠缠图中不应该存:
    Turn1: "做一个登录页面"
    Turn3: "用户名密码验证流程"
    Turn5: "加个验证码"
    Turn6: "登录做好了"
  
  → 纠缠图应该只存:
    Knowledge: "开发了登录页面，含用户名密码+验证码"
      turn_ids: [1,3,5,6]  ← 原文归档，需要时展开

  → 检索"登录页面验证码"时:
    纠缠图返回 Knowledge 摘要
    不够详细时才展开原文:
    DialogueTurn 5: "加个验证码，用 TOTP 算法"
```

**核心原则**：
- **纠缠图只存 Knowledge/Schema 级别摘要**，不存原始 Episode
- **原文 archived**（`is_archived = true`），不参与常规召回
- **需要时展开**：从 Knowledge.turn_ids → 读取 archived DialogueTurns

**实现流程**：
```
Plan "登录页面" → PlanGate 检测到 PlanState::Completed
  → 自动触发 compress_plan("登录页面")
  → 读取所有 DialogueTurns
  → 生成摘要: "开发了登录页面，含用户名密码+验证码"
  → 创建 Engram(Knowledge, plan_id="login", turn_ids=[1,3,5,6])
  → Turn 1,3,5,6 → is_archived = true
  → PlanNode.compressed_summary = 摘要
  → PlanNode.state = Completed

此后对"登录页面"的检索:
  → 纠缠图命中 Knowledge (不返回 4 个原始 Turn)
  → 需要细节 → 展开 turn_ids → 读原文
  → 噪音大幅降低
```

**缓解**：
- 原始 turns 标记 archived 但**不删除**（可从 `memhop_plan_history` 查看）
- 压缩产物的 `turn_ids` 记录原始数据，可追溯
- 初次压缩后等待一次 recall 验证（如果被召回且被用户标记为不准确 → 重新压缩）

### 问题 4：纠缠事件的噪声

**问题**：每次 recall 跨树命中都创建 EntanglementEvent，噪声量可能爆炸。一次偶然的跨树命中不等于真正的认知迁移。

**场景**：用户搜"设计"，技术文档和旅游攻略都出现了"设计"这个词。这是偶然的词汇重叠，不是认知迁移。

**缓解**：
- strength 机制：首次创建 strength=0.3，再次命中同一对节点时 +0.2。strength < 0.5 的事件不参与三观聚类
- threshold: score > 0.8 才创建事件
- 事件有衰减（30 天未再次触发 → strength *= 0.9）

### 问题 5：三观聚类的质量

**问题**：语义聚类依赖于 encoder 质量。BGE-M3 对中文哲学/美学概念的理解可能不足。聚类结果可能是"把什么和什么都聚在一起"。

**场景**：所有"简化"相关的纠缠事件被聚为一类——但"代码简化"和"生活简化"是两种完全不同的思维模式。

**缓解**：
- 聚类时加入 tree context（同一棵树内的纠缠事件优先聚合）
- 先用 LLM 对 context 做一次主题标注，再做聚类
- 提供 `memhop_review_worldview` 让用户手动确认/修正三观模式

### 问题 6：阶段间耦合风险

**问题**：Tree → Entanglement → Worldview 的依赖链意味着：如果树层的设计错了，纠缠层和三观层都得重做。

**场景**：阶段二（树层）用了 tree_id 字符串作为关联键，但阶段三（纠缠层）发现需要树层提供 centroid_vector 来做语义匹配。需要回去改树层。

**缓解**：
- 每个阶段发布独立 tag，可回滚
- 阶段接口定义在前（Tree trait，Entanglement trait），实现细节在后
- 阶段二上线后，用真实数据验证一周再进入阶段三

### 问题 7：现有代码哪些要重写 vs 增强

| 模块 | 操作 | 原因 |
|------|------|------|
| `brain.rs` perceive() | **增强** | 加活跃上下文匹配 + shelf matching + compress 触发 |
| `brain.rs` recall() | **增强** | 加上下文内检索 + cross_tree_hits + knowledge 自动附带 |
| `brain.rs` recall_retrieval() | **增强** | 接 CrossEncoder（代码已有，只差模型） |
| `brain.rs` dream() | **增强** | 加 embedding propagation + worldview 涌现 |
| `unified_graph.rs` | **不改** | pairwise 边仍然是基础，EntanglementEvent 是补充层 |
| `engram.rs` | **增强** | 加 `turn_ids` 字段 + `tree_id` 字段替代 `tree_path` |
| `plan_gate.rs` | **增强** | 加 PlanState Completed → compress 回调 |
| `engine/mod.rs` MemHop | **删除/标记 deprecated** | 僵尸 API |
| **新增** `context.rs` | **新建** | ContextSnapshot + 活跃上下文管理器 |
| **新增** `tree.rs` | **新建** | Tree + ShelfRef 实体 |
| **新增** `entanglement.rs` | **新建** | EntanglementEvent 实体 |
| **新增** `worldview.rs` | **新建** | WorldviewPattern 实体 |

### 问题 8：数据模型演进风险

**问题**：从 `tree_path: Option<String>`（字段标签）到 `Tree`（独立实体）是 breaking change。现有 engram 的 tree_path 数据需要迁移。

**迁移路径**：
```
v0.11.0 数据：每个 engram.tree_path 是字符串标签
         ↓
v0.12.0 迁移：扫描所有 engram，对每个唯一的 tree_path 创建 Tree 实体
         设置 engram.tree_id = tree.id
         保留 tree_path 字段（deprecated 但可读）
         ↓
v0.13.0 清理：移除 tree_path 字段，只保留 tree_id
```

### 问题 9：对话轮次粒度（Episodic 粒度 vs 事件粒度）

**问题**：当前 perceive() 每被调用一次就创建一个 Engram(Episode)，粒度是"对话轮次"而不是"对话事件"。这导致：
- 连续 5 轮关于 Rust async 的对话 = 5 个独立 Engram，各有各的 embedding
- 检索时可能只命中其中 1-2 个，丢失完整的上下文
- 和人脑的"事件级编码"不符

**场景**：
```
当前行为:
  Turn 1: "Rust async 怎么设计"   → Engram1 [Ep, plan_id=P1]
  Turn 2: "async/await 协作式调度" → Engram2 [Ep, plan_id=P1]
  ...
  recall("Rust 调度方式") → 可能只命中 Engram2，不知道 Engram1-5 属于同一事件

目标行为:
  Turn 1-5: 同属 Plan "Rust异步学习"
    存储时每个 Turn 独立存（保留细粒度）
    但感知时: 自动分组到 plan_id 下
    检索时: 优先返回 Plan 摘要，展开时返回全部 5 个 Turn
    压缩后: 创建 Knowledge 摘要，原文 archived
```

**缓解**：
- 细粒度路径不变（保留每轮独立存储，支持精确检索）
- 新增粗粒度路径（Plan 级别摘要优先返回）
- 检索时从命中的 Engram 向上找到 Plan，展开 Plan 内所有 Engram
- Plan 完成后自动压缩为 Knowledge，原文 archived

---

## 嵌套 Loop 依赖关系

```
Loop 0: Person
  │
  ├──→ Loop 1: Tree (依赖: Loop 0 的 identify_tree)
  │      │
  │      ├──→ Loop 1a: Perceive (依赖: Loop 1 的 tree.shelves)
  │      │      └──→ shelf.match() 查询书架
  │      │
  │      ├──→ Loop 1b: Recall (依赖: Loop 1 的 tree 隔离)
  │      │      └──→ 产生 cross_tree_hits → 喂给 Loop 2
  │      │
  │      └──→ Loop 1c: Compress (依赖: Loop 1a 的 PlanGate)
  │
  ├──→ Loop 2: Entanglement (依赖: Loop 1b 的 cross_tree_hits)
  │      │
  │      ├──→ Loop 2a: Recall-time detection
  │      ├──→ Loop 2b: Dream-time discovery
  │      └──→ Loop 2c: Recall-time expansion
  │
  └──→ Loop 3: Worldview (依赖: Loop 2 的 EntanglementEvent 积累)
         │
         ├──→ Loop 3a: Pattern clustering
         ├──→ Loop 3b: Pattern stabilization
         └──→ Loop 3c: Worldview → Recall
```

**开发顺序**：
```
Loop 1b (Retrieval)  ← 先做，这是所有召回的基础
    ↓
Loop 0 + Loop 1 (Tree + identify_tree + shelf match)
    ↓
Loop 1a + Loop 1c (Perceive + Compress)
    ↓
Loop 2 (Entanglement)
    ↓
Loop 3 (Worldview)
```

---

## ✅ 行动清单（更新版）

| # | 阶段 | 行动 | 依赖 | 优先级 |
|---|------|------|------|--------|
| 1 | v0.11.0-hotfix | 修复编码器加载 bug（当前 LongMemEval 失忆率=1.0） | 无 | P0 |
| 2 | v0.11.0-hotfix | CrossEncoder 模型部署 + 接线 | 1 | P0 |
| 3 | v0.11.0-hotfix | Retrieval 模式 MTEB 全量 benchmark 验证 | 2 | P1 |
| 4 | v0.11.1 | **新增** ContextSnapshot + Brain.active_contexts | 无 | P0 |
| 5 | v0.11.1 | perceive 加活跃上下文匹配（先匹配 active_contexts） | 4 | P0 |
| 6 | v0.11.1 | recall 加上下文内检索（匹配时只搜当前上下文） | 4 | P0 |
| 7 | v0.11.1 | recall 加 knowledge 自动附带（书架内容随记忆返回） | 现有 | P1 |
| 8 | v0.11.1 | PlanGate PlanState Completed → compress 回调接线 | 现有 | P1 |
| 9 | v0.11.1 | compress_plan 写新 Knowledge Engram + 原文 archived | 8 | P1 |
| 10 | v0.11.1 | Engram 加 `turn_ids: Vec<String>` 字段 | 9 | P1 |
| 11 | v0.12.0 | 新增 Tree 实体 + LMDB 表 | 4 | P0 |
| 12 | v0.12.0 | identify_tree 语义匹配（全量保底路由） | 11 | P0 |
| 13 | v0.12.0 | tree.shelves 书架列表 | 11 | P1 |
| 14 | v0.12.0 | perceive 加 shelf match 步骤 | 13 | P1 |
| 15 | v0.12.0 | recall 加 cross_tree_hits + plan_context 字段 | 11 | P1 |
| 16 | v0.13.0 | EntanglementEvent 实体 + recall 时创建 | 15 | P0 |
| 17 | v0.13.0 | recall 时展开纠缠事件（超边等价） | 16 | P0 |
| 18 | v0.13.0 | Dream REM 创建纠缠事件 | 16 | P1 |
| 19 | v0.13.0 | Dream 加 embedding propagation 阶段 | 现有 | P2 |
| 20 | v0.14.0 | WorldviewPattern 聚类 | 16 | P0 |
| 21 | v0.14.0 | WorldviewPattern 稳定化 | 20 | P0 |
| 22 | v1.0.0 | 三观反向影响 recall | 21 | P0 |

---

## 🙅 Non-goals

- ❌ 不做 FSEvents/文件监听
- ❌ 不做多用户
- ❌ 不做云端同步
- ❌ 不重写 UnifiedGraph
- ❌ 不引入数学超图库（Hyperedges 用 EntanglementEvent + Hopfield 等效实现）
- ❌ 不依赖 LLM 做记忆存储/检索（可选增强）
- ❌ 不阶段一至三不做 Dream 并发

---

## 三大设计原则

```
原则 1: 纠缠图只存摘要，不存原文
  ── 纠缠图中的知识是压缩后的 Knowledge/Schema
  ── 原文 Episode 标记 archived，需要时展开
  ── 类比人脑: 新皮层存语义，海马体存情景

原则 2: 活跃上下文集代替每轮全量检索
  ── Brain 维护 3-5 个 ContextSnapshot
  ── Perceive 先匹配上下文，匹配不上才全量检索
  ── 匹配时考虑时间衰减：cosine × exp(-hours/12)
  ── 类比人脑: 工作记忆多窗口，不每句话都翻遍所有记忆

原则 3: 书架内容随记忆自动附带
  ── 挂载路径的内容索引为 Knowledge Engram
  ── 检索命中相关 Plan/上下文时，自动附带书架知识
  ── 类比人脑: 回忆一件事时，相关书籍知识自然浮现

原则 4: Warmup 暖场机制（新增 v0.12.0）
  ── 前 N 轮（默认 5）只存储不检索/不纠缠
  ── 模拟人脑"刚认识时不急着翻记忆"
  ── N 轮后全量能力逐步激活

原则 5: 时间锚点贯穿始终（新增 v0.12.0）
  ── 每轮对话有时间戳
  ── 检索支持 time_from/time_to 过滤
  ── 上下文匹配带时间衰减（跨天自然冷却）
  ── 类比人脑: "昨天聊过的"、"上周的方案"
```

> **本计划以嵌套 Loop 结构完整描述了 MemHop 从现状到目标态的架构方案。包括 9 个已识别问题及缓解措施。开发顺序严格按 Loop 依赖展开，优先级标注 P0/P1/P2。**
>
> **核心变更（和第二版对比）**：
> - 新增 Warmup 暖场机制（原则 4）
> - 新增时间锚点 + 时间衰减（原则 5）
> - 补全活跃上下文的时间衰减系数
> - 行动清单调整版本号到 v0.12.0
>
> 产品舵手方向明（Fang），2026-05-31。
