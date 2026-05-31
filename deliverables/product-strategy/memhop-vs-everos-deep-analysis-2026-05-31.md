# MemHop vs EverOS (HyperMem) 深度对比分析

**日期**: 2026-05-31
**分析范围**: 架构理念、存储模型、检索机制、人脑拟合度、差异与借鉴

---

## 目录

1. [项目定位对比](#1-项目定位对比)
2. [EverOS/HyperMem 架构全貌](#2-everoshypermem-架构全貌)
3. [人脑拟合度逐项对比](#3-人脑拟合度逐项对比)
4. [检索机制对比](#4-检索机制对比)
5. [存储与数据模型对比](#5-存储与数据模型对比)
6. [MemHop 的 "每轮对话照上下文" 问题分析](#6-memhop-的-每轮对话照上下文-问题分析)
7. [超图设计借鉴分析](#7-超图设计借鉴分析)
8. [合并行动建议](#8-合并行动建议)
9. [MemHop 改进方案：三管齐下](#9-memhop-改进方案三管齐下)
10. [三层检索层级设计](#10-三层检索层级设计)

---

## 1. 项目定位对比

| 维度 | MemHop (v0.11.0) | EverOS / HyperMem |
|------|-------------------|-------------------|
| **本质** | 嵌入式记忆数据库引擎 | AI 记忆方法论 + Agent 编排框架 |
| **一句话** | "SQLite for associative memory" | "A self-organizing memory OS for agents" |
| **技术栈** | 纯 Rust，LMDB 单文件持久化 | Python，未指定存储后端 |
| **部署模型** | 嵌入式库 / MCP Server | REST API 服务 |
| **LLM 依赖** | 可选（默认向量+规则驱动） | **必须**（全流程依赖 LLM） |
| **延迟** | perceive <1ms, recall <50ms | perceive 秒级（等 LLM）, recall 秒级 |
| **开源** | 闭源（All Rights Reserved） | 开源方法论（Markdown + 配置） |
| **论文** | 无（工程产品） | ACL 2026 Main（HyperMem） |
| **评测** | LongMemEval-S, C-MTEB | LoCoMo, EverMemBench, EvoAgentBench |

### 关键认知

EverOS 本质上是 **LLM 编排管线**——靠 LLM 做 episode 检测、topic 聚合、fact 提取、rerank。存储是什么不重要。GitHub 仓库里没有实现代码，只有 Markdown 文档和 agent 配置。

MemHop 本质上是 **Rust 嵌入式记忆引擎**——靠向量索引 + Hopfield + 图 + 梦境规则。LLM 是可选的锦上添花。

**两者解决不同层次的问题**：EverOS 是"告诉 LLM 怎么管理记忆"，MemHop 是"给你一个记忆硬件"。

---

## 2. EverOS/HyperMem 架构全貌

### 2.1 数据模型：三段式超图

```
超图 H = (V^T ∪ V^E ∪ V^F,  E^E ∪ E^F)

节点类型:
  V^T (Topic)      话题节点
    ├── title:      "Rust异步编程"
    ├── summary:    "讨论了async/await、Pin、协作式调度"
    └── embedding:  语义向量

  V^E (Episode)    会话段节点
    ├── turns:      原始对话轮次
    ├── title:      "Rust异步模型讨论"
    ├── summary:    简要叙事摘要
    └── embedding:  语义向量

  V^F (Fact)       原子事实节点
    ├── content:    "Rust用协作式调度而非抢占式"
    ├── potential_queries: ["Rust async 调度方式", "async 协作还是抢占"]
    ├── keywords:   ["Rust", "async", "协作式调度"]
    └── embedding:  语义向量

超边类型:
  E^E: 连接一个 Topic 下的所有 Episode（w^E ∈ [0,1]）
  E^F: 连接一个 Episode 下的所有 Fact（w^F ∈ [0,1]）
```

### 2.2 构造流程（全过程依赖 LLM）

```
对话流 → LLM Episode Detection（检测语义完整性/时间间隔/语言信号）
         ↓
       Episode 创建
         ↓
       LLM Topic Matching（匹配已有 topic，新建/更新/初始化）
         ↓
       LLM Fact Extraction（提取原子事实 + 预测 query pattern）
         ↓
       创建超边：Topic → Episode, Episode → Fact
```

### 2.3 检索流程：Coarse-to-Fine 三步递进

```
query
  │
  ├── Stage 1: Topic 检索
  │     ├── BM25 + Semantic (RRF fusion)
  │     ├── Reranker refinement
  │     └── top-k^T topics
  │
  ├── Stage 2: Episode 检索
  │     ├── 对于每个选中 topic t
  │     ├── 展开超边 e^E_t → 得到所有 Episode
  │     ├── RRF + Reranker
  │     └── top-k^E episodes
  │
  ├── Stage 3: Fact 检索
  │     ├── 对于每个选中 episode
  │     ├── 展开超边 e^F → 得到所有 Fact
  │     └── top-k^F facts
  │
  └── Context 组装
        ├── 主要: retrieved facts content
        └── 可选: episode summaries
```

### 2.4 Embedding Propagation

```python
# 超边 embedding = 加权聚合成员节点
h_e = Σ α_{e,v} · h_v       # α = 权重

# 节点 embedding 精炼
h'_v = h_v + λ · Agg_{e∈N(v)}(h_e)   # λ = 传播强度
```

### 2.5 评测结果 (LoCoMo)

| 指标 | 分数 |
|------|------|
| 总体 | 92.73% |
| 单跳 | 96.08% |
| 多跳 | 93.62% |
| 时序推理 | 89.72% |
| 开放域 | 70.83% |
| Token 效率 | 7.5x (vs baseline 25-35x) |

---

## 3. 人脑拟合度逐项对比

### 3.1 MemHop 的神经科学拟合度

| 人脑机制 | 脑区 | MemHop | 实现方式 |
|---------|------|--------|---------|
| 工作记忆 | 前额叶皮层 | ✅ L0 Cortex | 7项 FIFO 环缓冲，零延迟 |
| 情景缓冲 | 海马体 | ✅ L1 Hippocampus | LMDB 持久化，~500项 FIFO |
| 长期存储 | 新皮层 | ✅ L2 Neocortex | UnifiedGraph + HNSW + Hopfield |
| 遗忘曲线 | 全脑 | ✅ vitality decay | 时间衰减 + 干扰衰减 + 情绪保护 + 调用保护 |
| 再巩固 | 海马体-新皮层 | ✅ reconsolidation | recall 时 vitality += 0.05 + 0.15×(1-vitality) |
| 编码特异性 | 海马体 | ✅ PlanGate | 当前话题上下文参与编码 |
| 情绪调制 | 杏仁核 | ✅ Emotional alignment | valence/arousal 影响 recall 排序 |
| 模式完成 | 海马体CA3 | ✅ Modern Hopfield | 单步能量收敛，指数容量 |
| 突触可塑性 | 全脑 | ✅ Hebbian learning | co-recall → edge weight += delta |
| 睡眠巩固 | 全脑 | ✅ Dream (NREM+REM) | 6 阶段：衰减/修剪/结晶/涌现/检测/跨锚点 |
| 干扰效应 | 海马体 | ✅ Interference decay | sigmoid(interference - 0.5) |
| 个体差异 | 全脑 | ✅ Personality | 情绪敏感度/遗忘速度/联想广度 |
| 概念形成 | 新皮层 | ✅ Schema emergence | 3+ episode 聚类 → Schema engram |
| 矛盾检测 | 前额叶 | ✅ Contradiction detection | 高余弦 + 低关键词重叠 = 潜在矛盾 |

**MemHop 拟合度：14/14 ✅ 全部覆盖**

### 3.2 EverOS/HyperMem 的认知拟合度

| 认知机制 | EverOS/HyperMem | 实现方式 |
|---------|----------------|---------|
| 层次化组织（主题→事件→事实） | ✅ | Topic/Episode/Fact 三层节点 |
| 多元素联合回忆 | ✅ | 超边 N-ary 关系 |
| 渐进式检索（从粗到细） | ✅ | Coarse-to-Fine 三步检索 |
| 语义传播（类似扩散激活） | ✅ | Embedding propagation |
| 查询模式预测 | ✅ | potential_queries 字段 |
| 工作记忆 | ❌ | 无 |
| 遗忘曲线 | ❌ | 无 |
| 再巩固 | ❌ | 无 |
| 情绪调制 | ❌ | 无 |
| 睡眠巩固 | ❌ | 无 |
| 突触可塑性 | ❌ | 无 |
| 模式完成 | ❌ | 无 |
| 矛盾检测 | ❌ | 无 |

**EverOS 拟合度：5/14 ✅ 仅覆盖认知层次组织**

### 3.3 核心结论

```
MemHop:              ⎡⎣⎣⎣⎣⎣⎣⎣⎣⎣⎣⎣⎣⎣⎤  14/14 神经机制全覆盖
EverOS/HyperMem:     ⎡⎣⎣⎣⎣⎣⎤⎦⎦⎦⎦⎦⎦⎦⎦⎦   5/14 仅认知层次组织
```

**MemHop 是"大脑硬件模拟器"，EverOS 是"大脑软件组织器"。**

两者不冲突，而是互补——MemHop 提供了神经机制的完整性（遗忘、再巩固、情绪、梦境），但缺少 EverOS 的认知组织层次（Topic→Episode→Fact）。MemHop 的 PlanNode 层次可以充当这个角色，只是没有被用于检索路径。

---

## 4. 检索机制对比

### 4.1 MemHop 检索管道

```
retrieval_path(query):
  │
  ├── 1. Encode (Ngram/Candle/ONNX)
  │
  ├── 2. HNSW search (O(log N) ANN)
  │     ef_search=50, top candidates
  │
  ├── 3. BM25 search (稀疏检索)
  │     ngram inverted index
  │
  ├── 4. Fusion: 0.4×BM25 + 0.6×HNSW cosine
  │     min-max normalization
  │
  ├── [可选] 5. CrossEncoder rerank
  │     BGE-Reranker-v2-m3, top-k
  │
  └── 6. Return top-limit results

associative_path(query):
  │
  ├── 1-4. (同上)
  │
  ├── 5. PGT 4-layer recall
  │     ├── L0: Plan-scoped ngram (Jaccard)
  │     ├── L1: Graph BFS from L0 seeds
  │     ├── L2: Temporal recency within plan
  │     └── L3: Global ngram fallback
  │
  ├── 6. Hopfield pattern completion
  │     one-step energy convergence
  │
  ├── 7. Competitive diffusion activation
  │     3-hop spread + lateral inhibition
  │
  ├── 8. Emotional alignment bonus (×0.9-1.1)
  │
  └── 9. Ngram overlap bonus (×0.9-1.1)
```

### 4.2 HyperMem 检索管道

```
retrieve(query):
  │
  ├── Stage 1: Topic Retrieval
  │     ├── BM25 (sparse index)
  │     ├── Semantic (Qwen3-Embedding-4B)
  │     ├── RRF fusion
  │     ├── Reranker refinement
  │     └── top-k^T topics
  │
  ├── Stage 2: Episode Retrieval (per topic)
  │     ├── Expand hyperedge → get episodes
  │     ├── RRF (BM25+semantic)
  │     ├── Reranker
  │     └── top-k^E episodes
  │
  ├── Stage 3: Fact Retrieval (per episode)
  │     ├── Expand hyperedge → get facts
  │     └── top-k^F facts
  │
  └── Assemble context
```

### 4.3 差异分析

| 维度 | MemHop | HyperMem |
|------|--------|----------|
| 检索路径 | 1 条管道，2 种模式 | 1 条路径，3 步递进 |
| 分步策略 | 无（一次到位） | 话题→事件→事实（递进） |
| 索引层 | HNSW + BM25 | BM25 + Semantic |
| 精排 | CrossEncoder (ONNX, 代码存在) | Reranker (LLM?) |
| 层级利用 | 不利用 Plan 层次 | 核心利用 Topic/Episode 层次 |
| 上下文构成 | 返回 engram 列表 | 返回 facts + 可选 episode summary |

**核心差异**：HyperMem 的层次在检索时被**主动利用**（展开超边），MemHop 的 Plan 层次在检索时被**忽略**。

---

## 5. 存储与数据模型对比

### 5.1 MemHop 存储模型

```rust
// 9 个 LMDB 数据库
├── engrams          // Engram 主存储
├── hippocampus      // 短期情景缓冲
├── graph_edges      // 关联边
├── schemas          // Schema 元数据
├── anchor_index     // 场景门控索引
├── config           // 版本 + HNSW tombstones
├── dialogue_turns   // 对话轮次
├── plan_tree        // PlanNode 层次树
└── hnsw_index       // HNSW 序列化索引

// Engram 结构 (所有记忆统一存储)
Engram {
    id, text, vector(f16, 1024d),
    keywords, content_type,
    valence, arousal,              // 情绪
    vitality,                      // 遗忘抵抗
    protection,                    // 保护级别
    created_at, last_activated,
    activation_count,
    kind: Episode|Schema|Anchor|Reflection|Knowledge,
    meta: HashMap<String, Value>,  // 可扩展元数据
    is_archived, is_dormant,
    turn_id, tree_path,
    source_path, source_textunit,
}

// 关联边 (pairwise)
Association {
    target_id,
    weight: [0, 1],
    kind: Semantic|Temporal|Causal|Emotional|...,
    last_activated,
}
```

### 5.2 HyperMem 存储模型

```python
# 存储模型未明确指定（文档级），以下是论文推导

# 超图结构
Hypergraph:
  topics:    List[TopicNode]     # 话题节点
  episodes:  List[EpisodeNode]   # 事件节点
  facts:     List[FactNode]      # 事实节点
  e_edges:   HyperEdge[Topic→Episodes]   # 话题→事件超边
  f_edges:   HyperEdge[Episode→Facts]    # 事件→事实超边

# 节点结构
TopicNode:
  id, title, summary, embedding

EpisodeNode:
  id, title, summary, turns, embedding

FactNode:
  id, content, potential_queries, keywords, embedding

# 没有：情绪、遗忘、保护、活性
# 没有：关联边、Hebbian、图扩散
```

### 5.3 对比结论

| 维度 | MemHop | HyperMem | 谁更好 |
|------|--------|----------|--------|
| 存储密度 | f16 向量，2KB/条 | 未指定 | MemHop |
| 数据完整性 | 情绪+活性+保护+元数据 | 仅内容 | MemHop |
| 存储引擎 | LMDB 嵌入式，零配置 | 未指定，需外部依赖 | MemHop |
| 层次结构 | PlanNode (有但不用于检索) | Topic/Episode/Fact (核心) | HyperMem |
| 关联模型 | pairwise 边 (8种) + Hopfield | 超边 N-ary | 互补（不冲突） |
| 扩展性 | ADD-only，字段增长灵活 | 未明确 | MemHop |
| 检索粒度 | 单 engram | 层次展开 | HyperMem |

---

## 6. MemHop 的 "每轮对话照上下文" 问题分析

### 6.1 问题定义

```
用户连续对话:
  Turn 1: "Rust async 怎么设计？"
  Turn 2: "async/await 是协作式调度"
  Turn 3: "那 Pin 有什么用？"
  Turn 4: "Pin 防止 Future 被移动"
  Turn 5: "和 Go goroutine 有什么区别？"

当前 MemHop 存储为 5 个独立 Engram:
  Engram1: "Rust async 怎么设计？"     ← 每个有自己的 embedding
  Engram2: "async/await 是协作式调度"   ← 各有各的 vitality
  Engram3: "那 Pin 有什么用？"          ← 各有各的召回概率
  Engram4: "Pin 防止 Future 被移动"
  Engram5: "和 Go goroutine 有什么区别？"

检索 "Rust 调度方式" 时:
  → 可能召回 Engram2（命中）
  → 可能召回 Engram5（关联 Go goroutine）
  → 可能召不回 Engram4（虽然相关）
  → 不知道这些属于"同一个对话事件"
```

### 6.2 人脑怎么做的

```
人脑的同一个事件记忆存储:

Event: "和张三讨论Rust异步" (2026-05-31 下午)
├── Context: 我们在看 Rust book 第10章
├── Key points:
│   ├── "Rust 用协作式调度不是抢占式" (重点)
│   ├── "Pin 确保不被移动" (次要)
│   ├── "和 Go goroutine 不同: Rust 是编译时确定" (关联)
├── Emotional: 困惑 → 理解 → 兴奋 (valance 变化)
├── Connections:
│   ├── 关联到已有知识: "Go 是 M:N 调度"
│   └── 关联到实践: "我昨天写的 async 代码"
└── Importance: 中高 (新知识)
```

人脑**不**存储：
- Turn 1 单独、Turn 2 单独、Turn 3 单独...
- 各自计算 embedding、各自有遗忘曲线

### 6.3 问题出在哪

**当前 perceive() 的粒度是"对话轮次"，不是"对话事件"。**

```rust
// 当前 perceive 每次被调用一次 → 创建 1 个 Engram
perceive("Rust async 怎么设计？")    → Engram (Episode, 单条)
perceive("async/await 是协作式调度") → Engram (Episode, 单条)
perceive("那 Pin 有什么用？")       → Engram (Episode, 单条)
```

而人脑的粒度是"事件隔离"（Event segmentation）—— 在 topic 转移时创建新事件，同一个 topic 内的对话是同一个事件。

### 6.4 现有的缓解和它们的不足

| 现有机制 | 效果 | 为什么不够 |
|---------|------|-----------|
| plan_id 分组 | 5 个 Engram 在同一 plan_id 下 | 计划层面分组，不用于检索展开 |
| PlanNode.compressed_summary | 压缩摘要 | 只在 Dream 时生成，从不自动触发 |
| Turn Crystallizer | 聚合同类 turns → Schema | 只在 Dream 时运行，不是实时的 |
| plan_context in recall | 返回 plan 上下文 | 代码骨架存在但未接线 |

**核心矛盾**：压缩/聚合（Dream time）和用户期望（任何时候都能得到结构化上下文）之间存在时间差。

### 6.5 解决方案：Perceive-time 实时 Episode 分组

借鉴 HyperMem 的想法，但不依赖 LLM：

```
改造后的 perceive() 流程:

perceive(input):
  ├── 1. encode → vector
  ├── 2. detect_plan_boundary()  ← 已有 PlanGate
  │     └── if score > 0.7:
  │           ├── compress_current_episode()  ← 新接线
  │           │     ├── 创建 summary engram
  │           │     ├── 标记原始 turns archived
  │           │     └── 更新 PlanNode.compressed_summary
  │           └── start_new_episode()
  │
  ├── 3. create Engram(Episode, current_episode_id)
  │     ├── episode_id = 当前事件 ID
  │     └── 加入 Episode→Engram 索引
  │
  ├── 4. 更新 Episode centroid
  │     └── 滑动平均 embedding
  │
  ├── 5. persist PlanNode + DialogueTurn
  │
  └── 6. return { engram_id, episode_id, plan_id }

改造后的 recall() 流程:

recall(query):
  ├── 1. (现有) HNSW + BM25 + rerank → 命中 Engram
  │
  ├── 2. (新增) 对每个命中，查 episode_id
  │     ├── 获取 episode 内所有 Engram
  │     ├── 返回 episode consolidated summary
  │     └── 附加同 episode 的其他 Engram
  │
  ├── 3. (新增) 从 episode 展开到 plan
  │     ├── 获取 plan_id → PlanNode hierarchy
  │     └── 返回 plan context
  │
  └── 4. 返回 consolidated RecallResponse
```

**实现量评估**：
- Episode 索引：少量字段（episode_id: String, episode_centroid: Vec<f16>）
- 自动压缩接线：compress_plan() 已有骨架，接上 PlanGate 回调
- 检索展开：已有 plan_id 和 PlanNode 层次，加一步展开逻辑

**不做**：
- 不依赖 LLM（用现有向量+规则做 episode 检测）
- 不引入新存储引擎（LMDB 加一张 episode_index 表即可）
- 不改变现有 Engram 结构（加 episode_id 字段）

### 6.6 和 Brain 结构的关系

这不是否定现有设计，而是**在现有设计上加一层上下文组织**：

```
现有路径（细粒度）:
  perceive() → Engram (单轮次) → recall() → 单轮次召回
               保留不变，用于精确检索

新增路径（粗粒度）:
  perceive() → Episode 分组 → compress → Episode 摘要
                                  ↓
                            recall() → Episode 摘要优先返回
                                    → 展开相关 Engram
                                    → 展开 Plan 上下文

两条路径共存，粗粒度优先，细粒度兜底。
```

---

## 7. 超图设计借鉴分析

### 7.1 要不要引入超图存储

**结论：不需要重构为超图。MemHop 的现有架构覆盖了超图的核心能力，且更好的方式。**

| HyperMem 的超图能力 | MemHop 的等价实现 | 差距 |
|---------------------|-------------------|------|
| N-ary 关系（超边） | Hopfield 天然全局 N-ary + PlanNode 层级分组 | Hopfield 比超边更强（能量收敛 vs 静态关系） |
| Topic 节点 | PlanNode (level=Domain/Plan) | PlanNode 不用于检索展开 |
| Episode 节点 | 隐式 = plan_id 下的所有 DialogueTurn | 无显式 episode_id |
| Fact 节点 | Schema engram | Schema 只在 Dream 时创建，不是实时 |
| 超边展开检索 | 无 | 需新增 episode 索引 |
| Embedding Propagation | 无 direct 等效 | 可用 Dream REM 新增阶段实现 |

### 7.2 等价关系

```rust
// HyperMem 超边:
//   e^E_t: 连接所有属于 Topic_t 的 Episode
//   
// MemHop 等价概念:
//   PlanNode(Domain=xxx, level=Domain)
//     → child PlanNodes (level=Plan)
//       → 下属 DialogueTurns
//         → Engrams (通过 turn_id)
//
// 数据已经有了，缺的是:
// 1. 检索时展开 PlanNode 的代码路径
// 2. Episode 作为显式的中间分组概念

// HyperMem 的 Topic→Episode→Fact 检索:
//   query → 找 Topic → 展开到 Episode → 展开到 Fact
//
// MemHop 应该加的检索路径:
//   query → recall() 命中 Engram → 
//     1. up: engram.turn_id → plan_id → PlanNode hierarchy
//     2. expand: plan_id 下所有相关 Engram
//     3. compress: 返回 plan context summary
```

### 7.3 可借鉴的设计模式

按价值从高到低排列：

#### 借鉴 1：Perceive-time Episode 分组 ← 最高价值

**HyperMem 做法**：LLM 在 perceive 时检测 Episode 边界，创建 Episode 节点

**MemHop 做法**：PlanGate 有 boundary_score，但只用来创建新 Plan，不创建 Episode 分组

**借鉴方式**：在 PlanGate 边界检测触发的回调中，自动打包当前 Plan 内的所有 DialogueTurn 为 Episode 摘要

**实现量**：中（~200 行，接线 + 摘要创建）

#### 借鉴 2：层次化检索展开

**HyperMem 做法**：Topic → Episode → Fact 三级展开

**MemHop 做法**：返回 flat list of Engram，不做层次展开

**借鉴方式**：在 RecallResponse 中新增 `episode_contexts` 和 `plan_contexts` 字段，命中 Engram 时向上展开到 Plan

**实现量**：小（~100 行，读取 PlanNode + 组装响应）

#### 借鉴 3：Query Pattern Prediction

**HyperMem 做法**：Fact 节点存 `potential_queries`

**MemHop 做法**：无

**借鉴方式**：存储时可选（无 LLM 则从 text 提取关键名词短语），检索时额外匹配

**实现量**：中（~150 行，提取逻辑 + 检索匹配）

#### 借鉴 4：Embedding Propagation

**HyperMem 做法**：通过超边传播 embedding，同组节点互相靠拢

**MemHop 做法**：无

**借鉴方式**：Dream 阶段新增 propagation 步骤，Plan 内 engram embedding 向 centroid 微调

**实现量**：小（~50 行，向量加权平均 + 更新）

### 7.4 不借鉴的部分

| 设计 | 不借鉴的原因 |
|------|-------------|
| LLM-dependent 全流程 | MemHop 的优势是离线/低延迟/零依赖 |
| 三层节点类型 (T/E/F) | MemHop 的 5 种 EngramKind 已覆盖 |
| 超图数据结构 | Hopfield + PlanNode 层次可替代 |
| Python REST 架构 | MemHop 嵌入式 Rust 是差异化定位 |
| 无遗忘/无情绪/无梦境 | 这些是 MemHop 的独有优势，绝不可抛弃 |

---

## 8. 合并行动建议

### 8.1 当前版本 (v0.11.0) 内的优先改进

| # | 改进项 | 类型 | 工程量 | 效果 |
|---|--------|------|--------|------|
| 1 | 修复编码器加载 bug | Bug | 小 | 第一梯队基础 |
| 2 | compress_plan() 接 PlanGate 回调 | 新接线 | 小 | 自动压缩功能 |
| 3 | 压缩产物写为新 Engram | 新功能 | 小 | 可检索的压缩记忆 |
| 4 | RecallResponse 加 plan_contexts | 字段新增 | 小 | 检索有层次 |

### 8.2 下一版本 (v0.12.0) 的架构变更

| # | 改进项 | 类型 | 工程量 | 效果 |
|---|--------|------|--------|------|
| 5 | Brain 拆分为 Person / Tree 结构 | 重构 | 大 | 多树架构基础 |
| 6 | Episode 显式索引 | 新功能 | 中 | 事件级检索 |
| 7 | 感知时 episode 分组 | 新功能 | 中 | 替代"每轮独立" |
| 8 | 层次化检索展开 | 新功能 | 小 | 粗→细检索 |

### 8.3 跨版本长期方向

| # | 方向 | 目标版本 | 来源 |
|---|------|---------|------|
| 9 | EntanglementEvent 超边 | v0.13.0 | brain-loop plan |
| 10 | Dream 阶段加 embedding propagation | v0.13.0 | 借鉴 HyperMem |
| 11 | Query pattern 索引 | v0.13.0 | 借鉴 HyperMem |
| 12 | Worldview 三观涌现 | v1.0.0 | brain-loop plan |

### 8.4 MemHop 的差异化定位

```
我们的赛道:  本地嵌入式联想记忆引擎
对手赛道:   云端 AI 记忆编排框架

不竞争: LLM 编排（让 EverOS 做）
不竞争: REST API 服务（让云服务做）
不竞争: 大模型训练（让Foundation Model做）

只竞争: 零配置 / 毫秒级 / 可嵌入 / 脑启发记忆

别人没有的:
  ● 梦境整合（NREM+REM 全阶段）
  ● Hebbian 学习
  ● 情绪调制
  ● 遗忘曲线 + 再巩固
  ● 5 种 EngramKind + 8 种 Association
  ● 单文件持久化

需要补的:
  ● 感知时实时 episode 分组
  ● 层次化检索展开
  ● 多树架构
  ● 跨树纠缠
```

---

## 附录 A：MemHop 和 HyperMem 人脑模型完整对比矩阵

```
                           MemHop               HyperMem
                         ────────            ───────────
神经科学层面:
  工作记忆               ✅ L0 Cortex         ❌
  情景缓冲               ✅ L1 Hippocampus    ❌
  长期存储               ✅ L2 Neocortex      ❌ (所有存储无分层)
  遗忘曲线               ✅ vitality decay    ❌
  再巩固                ✅ reconsolidation    ❌
  情绪调制               ✅ valence/arousal    ❌
  赫布学习               ✅ Hebbian edges      ❌
  睡眠巩固               ✅ Dream 6阶段        ❌
  模式完成               ✅ Hopfield           ❌
  个体差异               ✅ Personality        ❌
  概念形成               ✅ Schema emerge      ❌
  矛盾检测               ✅ Contradiction      ❌

认知层面:
  层次化组织             ⚠️ PlanNode (未用于检索) ✅ Topic→Episode→Fact
  多元素关联             ✅ Hopfield + Edges    ✅ 超边 N-ary
  从粗到细检索           ❌                     ✅ 3-step cascade
  语义传播               ❌                     ✅ Embedding propagation
  查询模式预测           ❌                     ✅ potential_queries

工程层面:
  嵌入部署               ✅ Rust lib + MCP      ❌ Python + REST
  零配置                 ✅ LMDB 单文件         ❌ 需要 LLM API
  延迟                  <1ms perceive         秒级 (LLM)
  多语言                 ✅ Rust (任何平台)      ❌ Python-only
  离线可用               ✅                     ❌ (无 LLM 不可用)
```

谁更像人脑？**MemHop 14/14 神经机制 vs HyperMem 5/14 认知组织**。MemHop 更接近。

谁更实用？**取决于场景**——如果需要 LLM agent 简单接入记忆，HyperMem 的文档+配置可能更方便；如果需要高性能嵌入式记忆引擎，MemHop 是唯一选择。

---

## 9. MemHop 改进方案：三管齐下

基于 EverOS/HyperMem 的对比分析和人脑设计原则，MemHop 需要三个并行改进来解决"每轮对话照上下文"问题。

### 9.1 活跃上下文集（Active Context Set）

**解决的问题**：交错话题（同一会话里项目/周末/吃饭话题交替）导致的全量检索噪音。

**核心机制**：

```
Brain 内部维护 active_contexts: VecDeque<ContextSnapshot> (max 5 个)

perceive(input):
  └── 1. encode → vector
  └── 2. 匹配活跃上下文
         for each ctx:
           score = cosine(vector, ctx.centroid)
         if max_score > MATCH_THRESHOLD (0.75):
           → 命中，在该上下文中操作
           → ctx.hit_count += 1
           → ctx.last_active = now
           → 不污染其他树
         else:
           → 全量 identify_tree/plan
           → 创建新 ContextSnapshot
           → 如果超过 5 个，淘汰最久未活跃的
  └── 3. 在当前上下文中存储
         → create Engram(tree_id=ctx.tree_id, plan_id=ctx.plan_id)
  └── 4. PlanGate 边界检测 → 如需压缩则触发

recall(query):
  └── 如果在活跃上下文中
         → 上下文内检索（只搜该 tree + 该 plan）
         → 不搜全量，噪音大幅降低
  └── 如果不在（新输入/跨话题）
         → 全量检索
         → 自动创建新 ContextSnapshot
```

**数据来源**：纯运行时（不持久化，类似 Cortex）。每次 Brain::open() 时重建。

**和现有代码的关系**：
- `ContextSnapshot` 新增结构体
- `Brain` 新增 `active_contexts: VecDeque<ContextSnapshot>` 字段
- perceive() 和 recall() 各自加一段匹配/调度逻辑
- 不需要新 LMDB 表，不需要改数据库 schema

### 9.2 Plan 完成 → 压缩 → Knowledge 摘要 + 原文归档

**解决的问题**：纠缠图中混存了原文 Episode（细粒度）和摘要 Knowledge（粗粒度），导致检索噪音。

**核心原则**：纠缠图只存 Knowledge/Schema 级别，原文标记 archived。

```
Plan "登录页面" 包含 Turn 1,3,5,6
  │
  ├── Plan 进行中（PlanState::Active）
  │     └── 所有 Turn 正常可检索（Episode, plan_id="login"）
  │
  ├── Plan 完成（PlanState::Completed）→ PlanGate 回调
  │     ├── compress_plan("登录页面")
  │     │     ├── 读取 DialogueTurns
  │     │     ├── 生成摘要: "开发了登录页，含用户名密码+验证码"
  │     │     └── 创建 Engram(Knowledge, plan_id="login",
  │     │           turn_ids=[1,3,5,6])     ← 纠缠图入口
  │     ├── Turn 1,3,5,6 → is_archived = true  ← 原文归档
  │     └── PlanNode.compressed_summary = 摘要
  │
  └── 此后检索"登录页面"时
        ├── 纠缠图命中 Knowledge（不返回 4 个原始 Turn）
        ├── 不够详细 → 展开 turn_ids → 读原文
        └── 噪音降低、精度提升
```

**和现有代码的关系**：
- `compress_plan()` 已存在骨架，需要: 写 Knowledge Engram + 归档 turns
- `Engram.is_archived` 字段已存在
- `Engram` 需新增 `turn_ids: Vec<String>` 字段
- PlanGate 的 PlanState 变更需要接回调

### 9.3 挂载路径自动附带（Shelf Auto-Attach）

**解决的问题**：书架内容需要用户手动调用 `knowledge_search()`，不能自动关联到记忆检索。

**核心机制**：

```
挂载 /books/rust-async (domain=Book)
  → 扫描 → chunk → 存为 Knowledge Engram
  → 每个 Knowledge Engram 记录 tree_path="/books/rust-async"
  → 注册到 ShelfManager

检索时的自动附带:

recall("Rust async 怎么设计", tree="/project/rust-learn")
  │
  ├── 1. 在 /project/rust-learn 范围内检索
  │     → 命中 Episode "我们讨论了 async/await"
  │
  ├── 2. 自动附带 /books/rust-async 的相关内容
  │     → cosine(query, Knowledge) > 0.6:
  │       "async/await 是协作式调度 (§3)"
  │       "Pin 确保 Future 不被移动 (§7)"
  │
  └── 3. RecallResponse.knowledge_memories 自动填充
        → 不需要用户手动 knowledge_search()

自动附带的触发条件:
  - recall 的 query 与某个 Knowledge Engram 的 cosine > 0.6
  - 或 recall 命中 Plan 关联了书架路径
  - recall 时 tree 参数指定的范围匹配了书架路径
```

**和现有代码的关系**：
- `ShelfManager` + scanner + chunker 已全部实现
- `RecallResponse.knowledge_memories` 字段已存在
- `Brain::recall()` 已支持 `tree` 和 `kind_filter` 参数
- 缺的是 recall 时的**自动附带逻辑**（把 knowledge 检索合并进 recall 管道）

### 9.4 三个改进的依赖关系

```
v0.11.0 现状
    │
    ├── 活跃上下文集 (9.1)      ← 可独立实现，不依赖其他
    │     └── 需要: ContextSnapshot + 匹配逻辑
    │
    ├── 压缩+归档 (9.2)         ← 需要: Engram.turn_ids + compress 接线  
    │     └── 需要: PlanState 回调
    │
    └── 书架自动附带 (9.3)      ← 可独立实现，依赖现有 shelf 代码
          └── 需要: recall 加 knowledge 融合逻辑
```

三个改进之间没有硬依赖，可以并行开发。

---

## 10. 三层检索层级设计

结合 MemHop 现有能力和 EverOS 启发，MemHop 应构建自己的三层检索层级。这和 HyperMem 的 Topic→Episode→Fact 对应，但更贴合 MemHop 的现有架构：

```
MemHop 三层检索                     HyperMem 三层检索
─────────────────                  ─────────────────
Layer 1: Active Context            Layer 1: Topic
  运行时的上下文匹配                   固定的话题节点
  (3-5 个滑动窗口)                   (持久化的 Topic 节点)

Layer 2: Plan Knowledge             Layer 2: Episode
  Plan 完成后的压缩摘要               事件/会话段节点
  (Knowledge engram)                (Episode 节点)

Layer 3: Original Episodes          Layer 3: Fact
  对话原文轮次                        原子事实节点
  (DialogueTurn 存档)
```

### 10.1 核心差异

| 维度 | HyperMem | MemHop 方案 | 谁更好 |
|------|----------|-------------|--------|
| Layer 1 机制 | 持久化 Topic 节点 | 运行时活跃上下文 | **MemHop**: 轻量、自适应、无需持久化 |
| Layer 1 持久化 | 是，LMDB / 向量数据库 | 否，纯运行时不持久 | **HyperMem**: 跨会话保留，各有利弊 |
| Layer 2 来源 | LLM 提取（perceive 时） | Plan 完成时压缩（事件驱动） | **MemHop**: 事件驱动更符合人脑（不需要等 LLM） |
| Layer 3 粒度 | 原子事实（Fact） | 对话轮次（DialogueTurn） | **HyperMem**: 粒度更细（但依赖 LLM） |
| 层级间跳转 | 超边展开（静态） | PlanIndex 查询（动态） | 持平 |

### 10.2 检索过程对比

```
HyperMem 检索:
  query → Topic 检索 → 超边展开到 Episode → 超边展开到 Fact

MemHop 检索:
  query → 匹配 ContextSnapshot (Layer 1)
    ├── 命中 → 上下文内检索
    │     → 只搜该 tree + plan 范围内
    │     → 返回 Knowledge + Episode 混合
    └── 不命中 → 全量检索
          → HNSW + BM25 + rerank
          → 优先返回 Knowledge (Layer 2)
          → 需要细节时展开 DialogueTurn (Layer 3)

关键区别:
  HyperMem 是"先找到 Topic，再展开"
  MemHop 是"先匹配上下文，匹配不上再全量搜"
  
  MemHop 的上下文匹配避免了超图维护的复杂度，
  同时实现了类似的效果（窄范围检索）。
```

### 10.3 与 HyperMem 超图的等效关系

```
HyperMem 概念          MemHop 等价实现
──────────────────     ──────────────────────
Topic 节点             ContextSnapshot + PlanNode(Domain)
Episode 节点           Plan + 下属 DialogueTurns
Fact 节点              Schema/Knowledge engram
超边 E^E (T→E)         PlanIndex.entries[plan_id] → engram_ids
超边 E^F (E→F)         Engram.turn_ids → DialogueTurns
Embedding Prop.        Dream 新增 Propagation 阶段
Coarse-to-Fine         上下文匹配 → 全量 → 展开
```

**结论**：MemHop 不需要引入超图数据结构来达到等价效果。现有 PlanIndex + Engram 字段 + 运行时 ContextSnapshot 已经覆盖。

---

> **本分析基于源代码阅读（MemHop v0.11.0）、arXiv 论文 2604.08256（HyperMem, ACL 2026 Main）和 EverOS GitHub 仓库（EverMind-AI/EverOS）完成。**
>
> **2026-05-31 更新补充**：
> - 新增第 9 节：MemHop 三管齐下改进方案
> - 新增第 10 节：三层检索层级对比设计
> - 核心结论：Active Context Set 替代"每轮全量检索"，Plan 压缩解决纠缠图噪音，书架自动附带解决知识集成
