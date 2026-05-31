# MemHop 产品设计：类人脑记忆系统

**日期**：2026-05-31
**类型**：产品设计规格书
**参与成员**：方向明（Fang）· 产品舵手（主理人）
**版本**：v2.0（替代 v1.0 三段式增量方案）

> ⚠️ 此文档为 MemHop 项目**产品愿景 + 架构设计**的权威参考。**此后所有开发工作以此文档为准。**

---

## 一、产品模型

### 1.1 核心理念

**人以知识树构成，树间纠缠形成自我。**

```
                           人
                            │
           ┌────────────────┼────────────────┐
           ▼                ▼                ▼
      ┌─────────┐      ┌─────────┐      ┌─────────┐
      │ 工作树   │      │ 旅游树   │      │ 孩子树   │      ← 每棵树 = 该领域的全部记忆
      │         │      │         │      │         │
      │ 对话记忆 │      │ 对话记忆 │      │ 对话记忆 │
      │ 想法记忆 │      │ 想法记忆 │      │ 想法记忆 │
      │ Plan归档 │      │ Plan归档 │      │ Plan归档 │
      └────┬────┘      └────┬────┘      └────┬────┘
           │                │                │
      ┌────┴────┐      ┌────┴────┐      ┌────┴────┐
      │ 书架     │      │ 书架     │      │ 书架     │      ← 每棵树关联的知识来源
      │/books/rust│    │/books/travel│   │/books/kids│
      │ (按目录  │      │ (按目录  │      │ (按目录  │
      │  自动挂载)│      │  自动挂载)│      │  自动挂载)│
      └─────────┘      └─────────┘      └─────────┘
           │                │                │
           └────────┬───────┘                │
                    │                        │
                    ▼                        │
              ┌──────────┐                   │
              │  纠缠图   │←──────────────────┘    ← 跨树关联事件
              │          │
              │ A树记忆  │      "在京都看枯山水，
              │   触发    │       灵感带到API设计中"
              │ B树决策  │
              └────┬─────┘
                   │
                   ▼
             ┌──────────┐
             │ 三观/性格  │                        ← 稳定模式涌现
             │ 世界观    │
             └──────────┘
```

### 1.2 每轮对话的处理流程

```
用户输入
    │
    ▼
┌─────────────────┐
│ 1. 识别属于哪棵树 │  ← 用当前会话上下文 + 语义匹配
│   "Rust异步模型"  │
│   → 工作树       │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ 2. 两路召回       │
│                 │
│ a) 树内对话记忆   │  ← recall(tree="work", kind=Episode)
│    近期上下文     │
│ b) 树关联书架    │  ← recall(tree="work", kind=Knowledge)
│    /books/rust  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ 3. 合并返回       │
│   上下文 + 知识   │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ 4. 跨树纠缠       │  ← 如果本次召回命中了其他树
│   检测           │     创建 EntanglementEvent
│                 │     "Rust所有权 → 海马体类比"
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ 5. 话题结束       │  ← PlanGate 检测到话题边界
│   → 自动压缩     │     compress_plan → PlanNode
│   → Plan归档    │     合并多轮对话为一条记忆
└─────────────────┘
```

---

## 二、核心数据结构设计

### 2.1 树 (Tree)

```rust
struct Tree {
    id: String,
    name: String,              // "工作" / "旅游" / "孩子"
    domain: String,            // "work" / "travel" / "parenting"
    description: Option<String>,
    created_at: i64,
    
    // 关联的知识来源
    shelves: Vec<ShelfRef>,
    // 统计
    memory_count: u64,
    last_active_at: i64,
}

struct ShelfRef {
    tree_id: String,
    source_path: String,       // "/Users/zt/books/rust-async"
    domain: ShelfDomain,       // "code" / "book" / "paper"
    chunk_count: u64,          // 挂载后自动统计
}
```

**和现有 `tree_path` 字段的区别**：`tree_path` 是 engram 上的一个 string 标签。Tree 是一个**独立实体**，有自己的 ID、统计、书架列表。树是主动管理的，不是被动标记的。

### 2.2 纠缠事件 (EntanglementEvent)

```rust
struct EntanglementEvent {
    id: String,
    
    // 参与纠缠的记忆节点
    nodes: Vec<EngramId>,        // 至少 2 个，来自不同树
    
    // 跨了哪些树
    trees: Vec<String>,          // ["work", "travel"]
    
    // 纠缠描述
    context: String,             // "枯山水极简哲学 → API 设计简化"
    trigger_type: TriggerType,   // 召回触发 / Plan压缩触发 / Dream触发
    
    // 时间
    created_at: i64,
    strength: f32,               // 0-1，随再次命中增强
}

enum TriggerType {
    RecallCrossTree,             // 召回时跨树命中
    PlanCompression,             // Plan 压缩时发现跨树模式
    DreamEmergence,              // Dream REM 阶段发现
    Manual,                      // 用户手动标注
}
```

**和现有 `AssociationKind::CrossTree` 的区别**：CrossTree 边是 pairwise 的（A-B、B-C 各一条边）。EntanglementEvent 是 **N 元事件**——一条事件记录 A+B+C 共同参与了"某次跨域认知迁移"。事件可以展开为 N 条边，但事件本身是原子单元。

### 2.3 三观 (Worldview)

```rust
struct WorldviewPattern {
    id: String,
    
    // 来源：对所有 EntanglementEvent 的聚类
    source_events: Vec<String>,
    
    // 模式描述
    pattern: String,             // "反复将美学原则迁移到技术设计"
    category: PatternCategory,   // 思维模式 / 价值观 / 决策偏好
    
    // 统计
    occurrence_count: u64,
    stability: f32,              // 0-1，越高越稳定
    emerged_at: i64,
}

enum PatternCategory {
    ThinkingStyle,               // 思维模式："先抽象再具体"
    ValuePriority,               // 价值观："简洁优先于完备"
    DecisionBias,                // 决策偏好："倾向于选择自己实现"
}
```

**生成时机**：Dream REM 阶段，对所有 EntanglementEvent 做聚类。模式重复出现 N 次（如 ≥ 5）且 stability > 0.7 时，创建 WorldviewPattern。

**和使用场景**：
- recall 时：命中 WorldviewPattern → 作为"自我认知"维度附加到返回结果
- 新输入与 WorldviewPattern 矛盾时：触发认知冲突标记

---

## 三、现状 vs 目标 差距分析

### 3.1 树层

| 设计目标 | 现有代码 | 差距 |
|---------|---------|------|
| Tree 作为独立实体 | `tree_path: Option<String>` 只是字段标签 | 需要 `Tree` 结构体 + LMDB 表 |
| 自动识别输入属于哪棵树 | PlanGate 只做话题边界检测，不做树归属 | perceive() 需要加树识别步骤 |
| 书架自动关联到树 | `mount_tree()` 需要手动指定 tree_path | 挂载时自动匹配或创建树 |
| 树统计 | 无 | memory_count, last_active_at |

### 3.2 纠缠层

| 设计目标 | 现有代码 | 差距 |
|---------|---------|------|
| EntanglementEvent 事件 | `AssociationKind::CrossTree` 只有 pairwise 边 | 需要新数据结构 + LMDB 表 |
| 召回时自动创建纠缠 | 无 | recall() 末尾检测跨树命中 → 创建事件 |
| Plan 压缩时跨树关联 | PlanGate 不触发压缩 | compress_plan() 后检查跨树模式 |
| 纠缠事件展开 | BFS 扩散走 pairwise 边 | 命中事件直接展开所有 nodes |

### 3.3 三观层

| 设计目标 | 现有代码 | 差距 |
|---------|---------|------|
| WorldviewPattern | 无 | 新数据结构 + 聚类算法 |
| Dream 聚类 EntanglementEvent | Dream REM 做 cross_anchor_discovery 但不产出世界观 | REM 阶段增加世界观涌现 |
| 三观反向影响召回 | 无 | recall 命中 worldview → 附加到结果 |
| 认知冲突检测 | UnifiedGraph.contradiction_pairs_in 只查 pairwise | worldview 层面的矛盾检测 |

### 3.4 检索层（基础能力）

| 设计目标 | 现有代码 | 差距 |
|---------|---------|------|
| 跨 encoder（Candle/ONNX/Ngram fallback） | 三层回退已实现 | MCP server 未传 MEMHOP_ONNX_MODEL 给 benchmark |
| HNSW + BM25 融合 | recall_retrieval() 已实现 | 但 MCP server 默认走 Associative 模式 |
| CrossEncoder 精排 | 代码已写（L1192-1243），模型缺失 | 需要 BGE-Reranker-v2-m3 ONNX 模型 |
| 双模式 dispatch | Retrieval/Associative 已有 | MCP 工具未暴露 mode 参数 |

### 3.5 压缩层

| 设计目标 | 现有代码 | 差距 |
|---------|---------|------|
| 话题结束时自动压缩 | PlanGate 检测边界但不触发 | perceive() 接 compress_plan() |
| 压缩时合并多轮对话 | Turn Crystallizer 产出 Schema 而非压缩 Episode | compress_plan() 利用 LLM 做摘要 |
| 压缩后替代原始碎片 | 原始 engram 保留不处理 | 压缩后标记原始 engram 为 archived |

---

## 四、架构方案：按层推进

### 阶段一：检索层 —— 让基础召回正确

**目标**：NDCG@10 > 0.95

**涉及模块**：`brain.rs` recall_retrieval(), `mcp-server main.rs`, `benchmarks/`

**工作项**：

1. **MCP server 默认 Retrieval 模式**
   - tool schema 暴露 `mode` 参数（retrieval / associative）
   - `tool_recall()` 默认 retrieval、默认 use_reranker=true
   - 已完成（2026-05-31）

2. **CrossEncoder 模型部署**
   - 下载 BGE-Reranker-v2-m3 → `optimum-cli export onnx` → `models/bge-reranker-v2-m3/model.onnx`
   - MCP server 启动时传 `MEMHOP_RERANKER_MODEL=models/bge-reranker-v2-m3`
   - 确认 reranker 加载成功（看 stderr 日志）

3. **BGE-M3 encoder 正确加载**
   - benchmark 启动时传 `MEMHOP_ONNX_MODEL=models/bge-m3`
   - 此前所有 benchmark 数据因 encoder 降级为 NgramEncoder 而无效，全部重跑

4. **Retrieval 模式 benchmark 重跑**
   - T2Retrieval, MIRACL, DuRetrieval, CMedQAv2
   - 目标：NDCG@10 > 0.95

**不涉及**：Tree、Entanglement、Worldview、Plan 压缩。纯检索管线。

### 阶段二：树层 —— 让记忆有归属

**目标**：每条记忆知道自己属于哪棵树；每棵树关联自己的书架

**涉及模块**：新增 `tree.rs`，改 `brain.rs` perceive() / recall()

**新增数据结构**：
```rust
// memhop/src/tree.rs
struct Tree { id, name, domain, shelves: Vec<ShelfRef>, memory_count, last_active_at }
struct ShelfRef { tree_id, source_path, domain, chunk_count }
```

**工作项**：

1. **Tree 实体**
   - 新增 LMDB 表 `trees`，key 前缀 `tree:`
   - CRUD：create_tree(name, domain), list_trees(), get_tree(id)
   - 统计：每次 store/forget 更新 memory_count

2. **输入自动归属树**
   - perceive() 新增步骤：用当前会话 context + 语义匹配，识别 input 属于哪棵树
   - 识别规则：优先匹配最近活跃树 → 回落语义匹配 → 回落创建新树
   - engram.tree_id = Some(tree_id)

3. **书架关联树**
   - mount_tree(path) 时：自动匹配或创建同名树
   - `/books/rust` → tree(name="工作", domain="work") 或创建新树

4. **recall 按树召回**
   - `recall(tree="work")`: 同时召回 tree="work" 的 Episode + Knowledge
   - `recall(tree="work", kind=Episode)`: 只召回对话记忆
   - `recall(tree="work", kind=Knowledge)`: 只召回书架内容
   - 如果不指定 tree：检索所有树，按树分组返回

5. **MCP 工具新增**
   - `memhop_create_tree`: 创建知识树
   - `memhop_list_trees`: 列出所有树及统计
   - `memhop_recall` tree 参数升级：支持按 tree name 或 tree_id 查询

**不涉及**：Entanglement、Worldview、Plan 压缩。

### 阶段三：纠缠层 —— 让跨树关联可见

**目标**：跨树的灵感迁移被显式记录和召回

**涉及模块**：新增 `entanglement.rs`，改 `brain.rs` recall() / dream()

**新增数据结构**：
```rust
// memhop/src/entanglement.rs
struct EntanglementEvent { id, nodes, trees, context, trigger_type, created_at, strength }
```

**工作项**：

1. **EntanglementEvent 实体**
   - 新增 LMDB 表 `entanglement_events`，key 前缀 `ent:`
   - CRUD：create_event(nodes, trees, context), list_events(), get_event(id)

2. **recall 时自动创建纠缠**
   - recall() 末尾：如果返回结果中 ≥ 2 条来自不同树，且相关性 score > 0.8 → 创建 EntanglementEvent
   - context 用 LLM（如果有）或模板（"来自 {treeA} 的记忆与来自 {treeB} 的记忆在查询 '{query}' 中关联"）

3. **Dream REM 增强**
   - REM-3 cross_anchor_discovery 发现跨树关联后 → 创建 EntanglementEvent
   - 统计所有事件 → 输出 "跨树关联报告"

4. **recall 时展开纠缠**
   - 命中 engram → 查参与的 EntanglementEvent → 展开所有 nodes
   - 纠缠事件作为上下文附加到 RecallResponse
   - 新增 `recall_entangled()` 模式：不做 HNSW，纯沿纠缠图扩散

5. **MCP 工具新增**
   - `memhop_list_entanglements`: 列出所有纠缠事件
   - `memhop_entanglement_detail`: 查看某个事件详情

**不涉及**：Worldview、Plan 压缩。

### 阶段四：压缩层 —— 让对话不碎片化

**目标**：同一话题的多轮对话自动压缩为一条记忆

**涉及模块**：`brain.rs` perceive() / compress_plan()

**工作项**：

1. **PlanGate → 压缩接线**
   - perceive() 中 boundary_score > 0.7 → 触发 compress_plan()
   - compress_plan() 取该 plan 下所有 DialogueTurn → LLM 压缩 → 写回 PlanNode.compressed_summary

2. **压缩产物：合并 Episode**
   - 压缩后不只是 summary string，同时创建一条新的 Engram（kind=Episode，内容为压缩摘要）
   - 原始 DialogueTurn 的 engram 标记为 archived（不在常规 recall 中出现，但可从 plan 历史查看）

3. **压缩时跨树检测**
   - compress_plan() 后检查：该 plan 涉及的记忆是否跨树？
   - 如果跨树 → 同时创建 EntanglementEvent

4. **recall 返回 plan 上下文**
   - 命中 engram → 查 plan_id → 返回 PlanNode.compressed_summary + 同 plan 下其他 turn 摘要
   - RecallResponse 新增 `plan_context: Option<PlanContext>` 字段

**不涉及**：Worldview。

### 阶段五：三观层 —— 让模式涌现

**目标**：长期使用后，系统涌现出用户的稳定思维模式

**涉及模块**：新增 `worldview.rs`，改 `brain.rs` dream()

**新增数据结构**：
```rust
// memhop/src/worldview.rs
struct WorldviewPattern { id, source_events, pattern, category, occurrence_count, stability, emerged_at }
```

**工作项**：

1. **WorldviewPattern 实体**
   - 新增 LMDB 表 `worldview_patterns`，key 前缀 `wv:`

2. **Dream REM 世界观涌现**
   - REM 新增步骤：对所有 EntanglementEvent 做聚类
   - 聚类算法：基于 context 文本的语义聚类（用 encoder 编码 + cosine 相似度）
   - 同类事件 ≥ 5 次 + stability > 0.7 → 创建 WorldviewPattern

3. **recall 时三观介入**
   - recall 命中 WorldviewPattern → 附加 pattern 描述到结果
   - 新输入与 WorldviewPattern 矛盾 → 标记为认知冲突 → 作为 recall 结果中的 conflicts 返回

4. **MCP 工具新增**
   - `memhop_list_worldviews`: 列出所有涌现的三观模式
   - `memhop_my_worldview`: 以自然语言输出当前三观摘要

---

## 五、路线图

| 阶段 | 版本 | 核心交付 | 依赖 | 关键指标 |
|------|------|---------|------|---------|
| 阶段一 | v0.11.1 | 检索管线完整接线 | 无 | NDCG > 0.95 |
| 阶段二 | v0.12.0 | Tree 实体 + 自动树归属 + 树内召回 | 阶段一 | 树识别准确率 |
| 阶段三 | v0.13.0 | EntanglementEvent + 跨树召回 | 阶段二 | 纠缠事件质量 |
| 阶段四 | v0.14.0 | Plan 自动压缩 + 碎片消除 | 阶段二 | 压缩准确率 |
| 阶段五 | v1.0.0 | WorldviewPattern 涌现 | 阶段三 | 三观模式稳定性 |

**依赖关系**：
```
阶段一（检索） → 阶段二（树） → 阶段三（纠缠）
                              → 阶段四（压缩）
              阶段三 + 阶段四 → 阶段五（三观）
```

阶段二之后的各项可以并行：纠缠（阶段三）、压缩（阶段四）在树层（阶段二）基础上独立开发。

---

## 六、MCP 工具清单（目标态）

| 工具名 | 层级 | 功能 |
|--------|------|------|
| `memhop_store` | 检索 | 存储记忆（episode/knowledge） |
| `memhop_recall` | 检索+树 | 召回（支持 mode/tree/kind_filter/use_reranker） |
| `memhop_dream` | 检索 | 运行 Dream 巩固 |
| `memhop_create_tree` | 树 | 创建知识树 |
| `memhop_list_trees` | 树 | 列出所有树及统计 |
| `memhop_mount_tree` | 树 | 挂载书架（自动关联到树） |
| `memhop_unmount_tree` | 树 | 卸载书架 |
| `memhop_list_entanglements` | 纠缠 | 列出纠缠事件 |
| `memhop_entanglement_detail` | 纠缠 | 查看纠缠事件详情 |
| `memhop_list_plans` | 压缩 | 列出所有 Plan |
| `memhop_plan_detail` | 压缩 | 查看 Plan 的压缩摘要和历史 |
| `memhop_list_worldviews` | 三观 | 列出涌现的三观模式 |
| `memhop_my_worldview` | 三观 | 自然语言三观摘要 |

---

## 七、Non-goals

- ❌ 不做多用户/多设备记忆（保持一人记忆定位）
- ❌ 不做云端同步（本地优先）
- ❌ 不做 CodeGraph 集成（MeowAgent 侧独立）
- ❌ 不引入数学超图库（EntanglementEvent 优于纯 pairwise 边，且不引入外部依赖）
- ❌ 阶段一至三不做 Dream 并发（阶段五后考虑）
- ❌ 不兼容 v0.10.x 之前版本（breaking changes 可接受）

---

## ⚠️ 风险矩阵

| 风险 | 可能性 | 影响 | 缓解 |
|------|--------|------|------|
| 树自动归属识别不准 | 中 | 高 | 先手动指定为主，自动归属为辅 |
| EntanglementEvent 噪声过多 | 高 | 低 | 需 score 阈值 + strength 衰减 |
| 压缩质量依赖 LLM 可用性 | 中 | 中 | 无 LLM 时用启发式降级 |
| WorldviewPattern 聚类质量 | 高 | 中 | 先人工 review，再自动化 |
| CrossEncoder 推理延迟 | 中 | 中 | ONNX 线程池调优 / Candle 备选 |

---

## ✅ 行动清单（阶段一优先）

| # | 阶段 | 行动 |
|---|------|------|
| 1 | 一 | 下载 BGE-Reranker-v2-m3 ONNX 模型 |
| 2 | 一 | 确认 MCP server 加载 reranker 成功 |
| 3 | 一 | benchmark 启动时传 MEMHOP_ONNX_MODEL（修复 BGE-M3 降级 bug） |
| 4 | 一 | Retrieval 模式跑 MTEB 全量基准测试 |
| 5 | 一 | NDCG@10 > 0.95 → 阶段一完成 |
| 6 | 二 | 设计 Tree 数据结构 + LMDB 表 |
| 7 | 二 | 实现 create_tree / list_trees / 自动树归属 |
| 8 | 二 | recall 按树召回 + MCP 工具升级 |
| 9 | 三 | 设计 EntanglementEvent 数据结构 |
| 10 | 三 | 实现召回时自动创建纠缠事件 |
| 11 | 四 | PlanGate → compress_plan 接线 |
| 12 | 四 | 压缩产物替代原始碎片 |
| 13 | 五 | WorldviewPattern 聚类 |
| 14 | 五 | 三观反向影响召回 |

---

## 📚 数据来源

**用户愿景**：方向明（Fang）在 2026-05-31 对话中澄清的完整产品模型。

**代码审阅**：`brain.rs` (2,900+), `unified_graph.rs` (284), `engram.rs` (~500), `engine/mod.rs` (197), `cortex.rs` (~80), `storage.rs` (~500), `mcp-server/main.rs`, `benchmarks/`

**竞品参考**：EverOS (mRAG + HyperMem), Mem0 (LOCOMO 91.6), FAISS + BGE-M3 + Reranker 标准 pipeline

---

> **此文档为 MemHop 产品设计的唯一权威参考**。阶段之间依赖关系严格：树是纠缠的基础，树+纠缠是三观的基础。不可跳阶段或交叉依赖。
>
> 本报告由产品舵手方向明（Fang）基于用户完整的产品愿景编写。不涉及代码实现，纯产品设计规格。
