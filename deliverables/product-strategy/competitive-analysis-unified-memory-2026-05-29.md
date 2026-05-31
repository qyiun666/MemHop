# MemHop 统一记忆存储 — 竞品分析简报

**日期**：2026-05-29
**类型**：竞品分析简报
**作者**：竞析（Compa）· 竞品分析师
**上游文档**：[统一记忆架构愿景](./unified-memory-architecture-vision-2026-05-29.md)

---

## 📌 TL;DR

- **MemHop 的独特位置**：不是代码图谱、不是通用知识图谱、不是向量数据库、不是 Agent 记忆 SDK。是**带有涌现式图学习能力的本地优先统一记忆系统**。
- **最大的竞争差异化**：Hebbian 学习 + Dream 巩固（记忆涌现）是所有竞品均不具备的能力。
- **最值得借鉴的设计**：GraphRAG 的社区摘要层次检索、Graphify 的置信度标签、CodeGraph 的 tree-sitter → SQLite → MCP 管线、Mem0 的 ADD-only + 多信号融合。
- **当前竞品格局**：CodeGraph（代码结构图）和 MemHop（记忆涌现）是互补关系，不是竞争；Mem0 和 agentmemory 是直接竞品但密度不足；GraphRAG 是重量级方案不适合个人 Agent。
- **建议行动**：v0.10.0 统一重构后，优先对齐 Mem0 的 ADD-only 写入简化 + 多信号融合检索，同时保留 Hebbian 学习作为不可替代的竞争壁垒。

---

## 1. 竞品全景图

### 1.1 生态位地图

```
                     ┌─────────────────────────────────────┐
                     │          AI Agent / LLM              │
                     │     (Claude/GPT/Cursor/Copilot)      │
                     └──────────────┬──────────────────────┘
                                    │ 通过 MCP / SDK / API 获取上下文
              ┌─────────────────────┼─────────────────────────┐
              │                     │                         │
    ┌─────────▼──────┐   ┌─────────▼──────┐   ┌─────────────▼──────┐
    │  代码结构层     │   │   记忆/知识层   │   │    通用检索引擎     │
    │                │   │                │   │                    │
    │  CodeGraph     │   │  MemHop ⬅      │   │  FAISS             │
    │  (静态调用图)   │   │  (涌现记忆图)   │   │  Milvus            │
    │                │   │                │   │  ChromaDB          │
    │  Graphify      │   │  Mem0           │   │  Qdrant            │
    │  (通用知识图)   │   │  agentmemory    │   │  Pinecone          │
    │                │   │  GraphRAG       │   │                    │
    └────────────────┘   └────────────────┘   └────────────────────┘
         ↑                       ↑                      ↑
    图是预建的               图是涌现的             没有图，只有向量
    "告诉我结构"            "告诉我你经历过什么"     "告诉我相似的东西"
```

### 1.2 竞品分类矩阵

| 类别 | 代表 | 核心能力 | 与 MemHop 关系 |
|------|------|---------|---------------|
| **代码结构图** | CodeGraph | 静态调用图 / 符号索引 | 互补（MeowAgent 双通道） |
| **通用知识图** | Graphify | 任意文件 → 实体关系图 | 部分重叠（但无记忆涌现） |
| **Agent 记忆 SDK** | agentmemory | 工作/情景/语义三层记忆 | 直接竞品（密度低） |
| **云端记忆 API** | Mem0 | 统一记忆层 + 图 + 向量混合 | 直接竞品（云端 vs 本地） |
| **结构化 RAG** | GraphRAG | 文档 → 实体 → 社区 → 检索 | 参考架构（太重） |
| **向量数据库** | FAISS/Milvus/ChromaDB | KNN 向量检索 | 基础设施（不竞争） |
| **统一记忆系统** | **MemHop** | **语义 + 涌现图 + Hebbian** | — |

---

## 2. 逐竞品深度分析

---

### 2.1 CodeGraph (colbymchenry/codegraph)

> **一句话**：预建代码知识图谱，用 tree-sitter 解析 AST → SQLite 存储 → MCP 暴露，让 AI Coding Agent 零文件读取完成代码理解。

#### 架构

```
源码 → [tree-sitter AST] → [Resolution 补边] → [SQLite + FTS5] → [MCP Server: 9 tools]
      抽取层                解析层                 存储层             上下文层
                          (Worker 线程隔离)
```

#### 核心数据模型

- **节点**：函数、类、方法、接口、导入、路由（14 组 Web 框架的 route → handler）
- **边**：调用、被调用、导入、继承、实现、动态派发、回调
- **存储**：SQLite + FTS5 全文索引 + WAL 模式，`.codegraph/codegraph.db`
- **同步**：FSEvents/inotify 监听，2 秒安静窗口后增量更新，尊重 `.gitignore`

#### 9 个 MCP 工具

| 工具 | 用途 |
|------|------|
| `codegraph_context` | 根据任务描述定位入口点和关键符号 |
| `codegraph_trace` | 完整调用路径追踪（含动态派发） |
| `codegraph_explore` | 批量读取符号源码 + 关系 |
| `codegraph_search` | 按名称模糊搜索符号 |
| `codegraph_callers` / `callees` | 单跳展开调用/被调用关系 |
| `codegraph_impact` | 重构影响面分析 |
| `codegraph_node` | 单符号详情 |
| `codegraph_files` / `status` | 索引状态 |

#### Benchmark（v0.9.4，2026-05-24）

| 指标 | 平均收益 |
|------|---------|
| 成本 | **35% 更便宜** |
| Token 消耗 | **57% 更少** |
| 响应时间 | **46% 更快** |
| 工具调用 | **71% 更少** |

#### 对 MemHop 的启发

| 启发点 | 复用到 MemHop |
|--------|-------------|
| **"预建图"理念** | Shelf mount 时预计算 Knowledge engram 之间的关联边，而不只是独立 chunk |
| **Context-first MCP 工具** | 为 MemHop 设计 `memhop_context` 工具：输入任务 → 返回相关记忆 + 关联知识 + 图扩散结果 |
| **SQLite + FTS5** | MemHop 考虑在 LMDB 之外增加 FTS5 做关键词倒排索引，提升 BM25 召回 |
| **增量同步策略** | Shelf 文件变更时不做全量重索引，只更新变更 chunk |
| **Worker 线程隔离** | Dream 巩固和 Shelf mount 批量写入走独立线程，不阻塞 recall |
| **路由感知** | 对特定文档类型（如法律条款编号、论文章节号）做结构化路由索引 |

#### MemHop vs CodeGraph：互补关系

CodeGraph 告诉 Agent **代码长什么样**，MemHop 告诉 Agent **你之前在这干了什么、读过什么、踩过什么坑**。两者在 MeowAgent 中通过 Thalamus 协同，不竞争。

---

### 2.2 agentmemory (rohitg00/agentmemory)

> **一句话**：面向 AI Coding Agent 的持久记忆系统，三层记忆模型 + 自动压缩管道，主打"静默捕获、智能压缩、精准召回"。

#### 架构

```
Agent 交互 → [Capture Layer] → [Compression Engine] → [Memory Core] → [Storage Backend]
             事件拦截           增量摘要+语义提取        索引+生命周期      ChromaDB/Pinecone/FAISS

查询: Agent 请求 → Retrieval Pipeline → 混合排序 → Top-K
```

#### 三层记忆模型

| 类型 | 生命周期 | 内容 | 实现 |
|------|---------|------|------|
| **工作记忆** (Working) | 当前会话 | 对话上下文 | 固定 Token 窗口 |
| **情景记忆** (Episodic) | 30 天 | 交互事件 + 时间戳 | 压缩存储 |
| **语义记忆** (Semantic) | 90 天 | 提取的事实/偏好 | {subject, predicate, object} 三元组 |

#### 压缩管道（核心差异化）

```
情景压缩（第1层）：连续对话 → LLM 增量摘要，压缩比 5:1~10:1
语义压缩（第2层）：情景记忆 → 结构化三元组提取 + 去重 + 优先级评分

Priority = Base + Frequency_Bonus × log(1+mentions) - Time_Decay × age + Explicit_Boost
```

#### 检索管道

```
查询 → 查询扩展 → 向量语义搜索 + 时间衰减 + 优先级加权 + 元数据过滤 → Cross-encoder 重排序 → 去重 → Top-K
```

#### 设计模式（可借鉴）

| 模式 | MemHop 可复用 |
|------|-------------|
| **管道模式**（Capture→Compress→Store→Retrieve） | MemHop 已天然具备（store→index→recall→dream），可显式化 |
| **策略模式**（后端、模型、压缩可替换） | ShelfDomainTrait 就是策略模式的体现 |
| **观察者模式**（事件总线捕获） | MemHop 的自动 store 钩子本质上也是 |
| **去重策略**（语义相似度 >0.95 跳过，0.85-0.95 合并） | 可引入到 MemHop 的 store 写入前检查 |

#### 弱点（MemHop 的机会）

- ❌ **无图**：只有向量检索，没有图扩散和 Hebbian 学习
- ❌ **无 Dream**：没有记忆巩固和涌现关联
- ❌ **依赖外部 LLM** 做压缩（成本和延迟）
- ❌ **记忆类型隔离**：工作/情景/语义分开存储，不像 engram 统一模型
- ❌ **无 Shelf 概念**：不支持挂载外部文档作为知识源

---

### 2.3 Mem0 (mem0ai/mem0)

> **一句话**：云端优先的 Agent 统一记忆 API，ADD-only 写入 + 多信号融合检索 + 向量/图混合存储，LOCOMO 得分领先。

#### 统一记忆模型

Mem0 把 User、Session、Agent 三层记忆统一在同一向量-实体-时序空间：

```
User 记忆（偏好、历史） ──┐
Session 记忆（对话上下文）──┼──→ 统一 Retrieval Space
Agent 记忆（行动轨迹）   ──┘    （语义 + BM25 + 实体 + 时序）
```

#### 新算法核心（2026年4月发布）

1. **ADD-only**：只新增不覆盖，消除 CRUD 写冲突
2. **实体链接**：跨记忆提取实体 → 向量嵌入 → 链接增强检索
3. **多信号融合**：语义相似度 + BM25 关键词 + 实体匹配 → 融合排序
4. **时间推理**：区分"当前状态"、"过去事件"、"未来计划"

#### 性能基准

| 基准 | 得分 | Token 用量 |
|------|------|-----------|
| LoCoMo | **91.6** | 7.0K |
| LongMemEval | **94.8** | 6.8K |
| BEAM (1M tokens) | 64.1 | 6.7K |
| BEAM (10M tokens) | 48.6 | 6.9K |

**Token 节省**：100 轮对话从 1M → 100K（**节省 90%**），p95 延迟从 15.2s → 1.4s（**降低 91%**）。

#### 部署选项

| 模式 | 向量存储 | 图存储 | 适用场景 |
|------|---------|--------|---------|
| Library | Chroma (本地) | NetworkX | 测试 / 原型 |
| Self-Hosted | Qdrant | Neo4j (可选) | 自有基础设施 |
| Cloud | 托管 Qdrant | Neo4j (托管) | 零运维 |

#### 用户为什么可能/不愿用云端方案

| 愿意用 | 不愿用 |
|--------|--------|
| ✅ 零运维 | ❌ 数据出境风险（金融/医疗/法律） |
| ✅ 持续更新算法（新算法自建需跟进） | ❌ API 依赖，离线不可用 |
| ✅ 开箱即用集成（LangChain/CrewAI） | ❌ 定价不透明（未来可能涨价） |
| ✅ 社区生态（Chrome 扩展/Agent Skills） | ❌ 供应商锁定（切换成本高） |
| | ❌ LLM 调用成本（记忆提取仍要走 OpenAI） |
| | ❌ 隐私顾虑（对话内容上传至云端分析） |

#### 对 MemHop 的启发

| 启发点 | 具体建议 |
|--------|---------|
| **ADD-only 写入** | `store()` 默认只做插入，用 `dream()` 做合并/去重，避免覆盖冲突 |
| **多信号融合检索** | recall 在 HNSW 之外加入 BM25 关键词 + EntangleGraph 实体匹配，三路融合 |
| **时间推理** | meta 中加入时间戳，recall 结果按时间维度分组（现在/过去/计划） |
| **实体链接** | Dream 过程中自动提取 engram 中的关键实体，建立 Semantic 边 |
| **记忆压缩** | Dream 巩固时对低 vitality 的 Knowledge engram 做摘要压缩，而非直接删除 |
| **Platform Play 风险** | Mem0 正在做 Agent Skills 标准 + 浏览器扩展，可能形成平台锁定 |

---

### 2.4 Graphify (safishamsi/graphify)

> **一句话**："任意文件夹 → 知识图谱"，双层提取（本地 AST + LLM 语义），Leiden 社区检测，多 AI 平台支持。

#### 架构

```
源文件
  ├── 代码文件 → tree-sitter AST (本地，33+ 语言，零 API)
  ├── 文档/PDF → LLM 语义提取（多后端支持）
  ├── 图片 → LLM 视觉理解
  └── 音视频 → faster-whisper 本地转录
       │
       ▼
  图谱构建 → Leiden 社区检测 → 置信度标签
       │
       ▼
  输出: graph.json + graph.html + GRAPH_REPORT.md
       │
       ▼
  查询: /graphify query/path/explain + MCP server
```

#### 数据模型亮点

- **置信度标签**：每条边标记 `EXTRACTED`（直接提取）/ `INFERRED`（语义推断）/ `AMBIGUOUS`（不确定）
- **上帝节点**：图中连接数最高的枢纽概念
- **意外连接**：跨文件/模块的非预期关联
- **社区检测**：Leiden 算法多粒度聚类

#### 关键设计模式

| 模式 | 说明 | 对 MemHop 的启发 |
|------|------|-----------------|
| **双层提取** | 结构化文件走本地 AST，非结构化走 LLM | Shelf mount 时按 domain 自动选择 chunker |
| **置信度标签** | 每条关系标记确定性 | EntangleGraph 边可加 confidence 字段 |
| **增量更新** | `--update` 仅重提变更文件 | Shelf 文件变更时增量更新 engram |
| **Git 原生合并** | union-merge driver 永不冲突 | 多设备同步时可借鉴 |
| **文件监听 + Hook** | 文件变更自动重建 | `mount_shelf` 支持 `--watch` 模式 |
| **多平台适配器** | 20+ AI 助手统一安装接口 | MemHop MCP 兼容主流 Agent |

#### 局限性（MemHop 优势区）

- ❌ 静态图：图是"拍照"而非"学习"，没有使用中的边强化
- ❌ 无持续性：每次运行重新构建（虽然有缓存），不是持久记忆系统
- ❌ 无 Dream 巩固：没有记忆涌现和时间衰减
- ❌ 依赖 LLM：文档/PDF 处理必须调用 LLM API

#### MemHop vs Graphify 关系

Graphify 做的是**一次性知识提取**（"这个项目是什么"），MemHop 做的是**持续性记忆积累**（"我在这个项目上经历过什么"）。两者可在 MeowAgent 中并存。

---

### 2.5 Microsoft GraphRAG

> **一句话**：文档 → 实体提取 → 社区检测 → 层次摘要 → 本地/全局双模检索，重量级结构化 RAG 标杆。

#### 架构

```
索引阶段（离线）：
  文档 → TextUnit 切分 → LLM 提取实体/关系/协变量
    → Leiden 层次社区检测
    → Node2Vec 图嵌入
    → LLM 生成社区报告 + 社区摘要 + 嵌入

查询阶段（在线）：
  本地搜索：向量匹配实体 → 图邻居遍历 → 关联文本块 → 排序
  全局搜索：Map-Reduce 聚合所有社区摘要 → LLM 综合回答
```

#### 数据模型

| 概念 | 说明 |
|------|------|
| 实体 (Entities) | 具体/抽象事物 |
| 关系 (Relationships) | 实体间的边 |
| 文本单元 (TextUnits) | 原始文档最小分析单元 |
| 社区 (Communities) | Leiden 层次聚类结果 |
| 社区摘要 | LLM 生成的每层社群摘要 |
| 协变量 (Covariates) | 声明类附加信息 |

#### 本地搜索 vs 全局搜索

| 维度 | 本地搜索 | 全局搜索 |
|------|---------|---------|
| 触发方式 | 实体推理 | Map-Reduce |
| 适用问题 | 细粒度（"林黛玉有哪些关系"） | 宏观（"数据的核心主题"） |
| 资源消耗 | 较低 | 高（资源密集型） |

#### 对 MemHop 的启发（重要！）

| 启发点 | 具体建议 |
|--------|---------|
| **社区摘要层次** | Dream 巩固时对 EntangleGraph 做社区检测，生成分层的"记忆主题摘要" |
| **本地/全局双模召回** | recall 分两档：`recall(query)` 本地精度模式 + `recall_global(topic)` 宏观俯瞰模式 |
| **Map-Reduce 聚合** | Dream 阶段用 Map-Reduce 合并相似 engram，生成高层次的 Schema/锚点 |
| **TextUnit 追溯** | 每个 Knowledge engram 保留 `source_textunit` 引用，可回溯原文 |
| **Node2Vec 图嵌入** | EntangleGraph 上训练轻量 Node2Vec，补充纯语义向量的图结构信息 |
| **分层粒度** | 检索结果按"原子记忆 → 模式总结 → 主题摘要"三层组织 |

#### 为什么不直接使用 GraphRAG

- ❌ **太重**：需要 LLM 提取实体/关系/摘要，每次索引成本极高（数万 Token）
- ❌ **静态**：也是"拍照"模式，不是持续学习的记忆系统
- ❌ **不适合个人 Agent**：为企业和文档集设计，不适合对话流式记忆
- ❌ **无 Hebbian 学习**：图是 LLM 构建的，不是从使用中涌现的

---

### 2.6 向量数据库（FAISS / Milvus / ChromaDB）

> **一句话**：它们提供"向量检索基础设施"，不提供"记忆系统"——这是 MemHop 存在的根本理由。

#### 为什么它们只做向量检索

| 数据库 | 定位 | 核心能力 | 欠缺什么 |
|--------|------|---------|---------|
| **FAISS** | 向量检索库 | GPU KNN / 多种索引 | 无持久化设计、无图、无生命周期 |
| **Milvus** | 分布式向量数据库 | 十亿级 ANN / 混合查询 | 无图、无记忆语义 |
| **ChromaDB** | 嵌入式向量数据库 | 轻量持久化 / 元数据过滤 | 无图、无 Hebbian、无 Dream |
| **Qdrant** | 高性能向量数据库 | 量化索引 / 过滤 / 多向量 | 无图、无记忆涌现 |
| **Pinecone** | 云端向量数据库 | 零运维 / 自动扩缩 | 无图、云端锁定 |

#### 它们和"记忆系统"之间缺什么

```
向量数据库提供:
  store(vector, metadata) → KNN_search(query_vector, top_k)

记忆系统需要:
  store(text) → auto-chunk → embed → index → vitality → Hebbian → Dream → graph → recall
       ↑              ↑        ↑       ↑        ↑          ↑        ↑       ↑       ↑
    自然语言       自动分块  嵌入模型  向量索引   生命周期   边强化  巩固合并  图扩散  统一检索
```

**差距 = Memory Semantics（记忆语义层）**。向量数据库是存储引擎，MemHop 在存储引擎上构建了完整的记忆生命周期。

#### MemHop 的独特位置

```
应用层:  "Agent 需要一个记忆系统，不是一个向量数据库"

MemHop:  提供 store(text) / recall(query) / dream() 的自然语言接口
          ↓
         内部用 HNSW (类似 FAISS) + Hebbian 图 (独有) + LMDB (持久化)

向量库:  提供 ANN 索引，但不懂"记忆"是什么
```

#### 对 MemHop 的启发

- MemHop 永远不需要"让用户选择向量后端"——HNSW 内嵌足够
- 但如果未来扩展到大规模式部署，可考虑 Milvus/Qdrant 作为可选的远程后端
- ChromaDB 的元数据过滤设计值得借鉴到 MemHop 的 recall filter

---

## 3. 功能对比矩阵

### 3.1 核心能力矩阵

| 能力 | MemHop | CodeGraph | agentmemory | Mem0 | Graphify | GraphRAG | FAISS/Milvus |
|------|--------|-----------|-------------|------|----------|----------|--------------|
| **统一记忆模型** | ✅ engram | ❌ 只管代码 | ⚠️ 三层分离 | ✅ 三层统一 | ❌ 只管知识 | ❌ 只管文档 | ❌ |
| **语义检索** | ✅ HNSW | ❌ 符号搜索 | ✅ 向量检索 | ✅ 多信号 | ❌ 图查询 | ✅ 向量+图 | ✅ 纯向量 |
| **图扩散** | ✅ Hebbian 涌现 | ✅ 静态调用图 | ❌ | ⚠️ 实体链接 | ✅ 静态知识图 | ✅ 静态实体图 | ❌ |
| **Dream 巩固** | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **记忆涌现** | ✅ Hebbian 学习 | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **外部知识挂载** | ✅ Shelf | ❌ | ❌ | ❌ | ✅ 任意文件 | ✅ 文档 | ❌ |
| **本地优先** | ✅ LMDB | ✅ SQLite | ✅ ChromaDB 本地 | ⚠️ 可本地 | ✅ 本地 | ⚠️ 需 LLM API | ✅ |
| **MCP 协议** | ✅ | ✅ 9 tools | ❌ | ⚠️ REST API | ✅ MCP server | ❌ | ❌ |
| **离线可用** | ✅ | ✅ | ⚠️ 需 embedding | ❌ 默认需 API | ⚠️ LLM 部分 | ❌ | ✅ |
| **多用户/协作** | ❌ (v1.0) | ❌ | ❌ | ✅ | ❌ | ❌ | ✅ (Milvus) |
| **垂类扩展** | ✅ ShelfDomain | ❌ | ❌ | ❌ | ✅ 自定义提取 | ⚠️ 本体自定义 | ❌ |
| **生命力管理** | ✅ vitality | ❌ | ⚠️ 时间衰减 | ⚠️ TTL | ❌ | ❌ | ❌ |
| **记忆压缩** | ⚠️ Dream 中 | ❌ | ✅ 增量摘要 | ✅ 92%压缩 | ❌ | ✅ 社区摘要 | ❌ |

### 3.2 检索能力矩阵

| 检索维度 | MemHop | agentmemory | Mem0 | GraphRAG |
|---------|--------|-------------|------|----------|
| 语义相似度 | ✅ HNSW | ✅ 向量 | ✅ embedding | ✅ Node2Vec |
| 关键词匹配 | ⚠️ 计划中 | ⚠️ 可选 | ✅ BM25 | ❌ |
| 图结构 | ✅ EntangleGraph | ❌ | ✅ 实体链接 | ✅ 实体-社区 |
| 时间推理 | ⚠️ meta 级别 | ✅ 时间衰减 | ✅ 时序推理 | ❌ |
| 多跳推理 | ✅ 图扩散 | ❌ | ⚠️ 实体链路 | ✅ 图遍历 |
| 全局摘要 | ❌ | ❌ | ❌ | ✅ Map-Reduce |

---

## 4. SWOT 分析（主要竞品）

### 4.1 Mem0

| | |
|---|---|
| **优势 (S)** | **劣势 (W)** |
| • LOCOMO 得分领先 (91.6) | • 云端优先，数据出境 |
| • ADD-only 简化写入 | • 依赖外部 LLM（成本） |
| • 多信号融合检索（语义+BM25+实体+时序） | • 无 Hebbian 学习 / Dream 巩固 |
| • 开源 + 自托管选项 | • 定价不透明 |
| • 强生态（LangChain/CrewAI/Agent Skills） | • 图是隐式的（实体链接），非显式可操作 |
| • Token 节省 90%，延迟降低 91% | • BEAM 10M tokens 仅 48.6 分 |
| **机会 (O)** | **威胁 (T)** |
| • 正在做 Agent Skills 标准 | • 可能被 OpenAI 内置记忆功能替代 |
| • 浏览器扩展扩大覆盖面 | • 云端方案的隐私法规风险（GDPR/数据出境法） |
| • 企业自托管市场 | • 竞品快速跟进多信号融合 |

### 4.2 agentmemory

| | |
|---|---|
| **优势 (S)** | **劣势 (W)** |
| • 三层记忆模型清晰 | • 无图结构 |
| • 自动压缩管道（5:1~10:1） | • 无 Dream / Hebbian |
| • 去重策略成熟 | • 记忆类型隔离（非统一模型） |
| • 可插拔后端 | • 无 Shelf 概念 |
| • 时间衰减权重 | • 依赖外部 LLM 做压缩 |
| **机会 (O)** | **威胁 (T)** |
| • Coding Agent 记忆标准化 | • Mem0 功能更全更快 |
| • 开源社区贡献 | • 大厂内置记忆可能吃掉市场 |

### 4.3 GraphRAG

| | |
|---|---|
| **优势 (S)** | **劣势 (W)** |
| • 社区摘要层次检索 | • 极重（每次索引需要数万 LLM Token） |
| • 本地/全局双模搜索 | • 静态图（无学习/涌现） |
| • Map-Reduce 宏观回答质量高 | • 不适合个人 Agent |
| • 微软背书 + 活跃社区 | • 无实时更新（离线批处理） |
| • Node2Vec 图嵌入 | • 无记忆生命周期 |
| **机会 (O)** | **威胁 (T)** |
| • 企业知识管理场景 | 不直接竞争个人 Agent 记忆市场 |
| • 多层次摘要思想可被借鉴 | |

### 4.4 CodeGraph

| | |
|---|---|
| **优势 (S)** | **劣势 (W)** |
| • tree-sitter 多语言 AST | • 仅限代码（不做通用记忆） |
| • SQLite 持久化 + FTS5 | • 静态图（无学习） |
| • 9 个 MCP 工具覆盖全场景 | • 1MB 文件不索引入图 |
| • 平均 57% Token 减少 | • 部分语言 Partial support |
| • 增量同步 + 文件监听 | • 需 per-project init |
| **机会 (O)** | **威胁 (T)** |
| • 与 MemHop 互补集成 | 不直接竞争 |
| • CI 集成（`codegraph affected`） | |

---

## 5. MemHop 差异化定位

### 5.1 一句话定位

> **MemHop 是唯一做"记忆涌现"的本地优先统一记忆系统。Heabbian 学习 + Dream 巩固是所有竞品的空白区。**

### 5.2 三层不可替代性

| 层 | MemHop 独有 | 竞品状态 |
|----|-----------|---------|
| **L1: 统一 engram 存储** | Episode + Knowledge + Schema + Anchor 走同一 HNSW | agentmemory 分离、Mem0 部分统一 |
| **L2: 涌现式图学习** | Hebbian 边强化 + Dream 新关联发现 | 所有竞品都是静态图或没有图 |
| **L3: 本地优先持久化** | LMDB 单文件 + 零外部依赖 | Mem0 默认云端、GraphRAG 要 LLM API |

### 5.3 护城河分析

```
竞品可以复制:                    MemHop 护城河内:
  - 向量检索 (HNSW)              - Hebbian 学习算法
  - 统一存储 (LMDB/SQLite)       - Dream 巩固策略
  - MCP 工具协议                 - vitality 生命周期
  - Shelf 文件挂载               - EntangleGraph 涌现机制
                                 - 跨类型的自动关联
```

### 5.4 在生态中的位置

```
MeowAgent 收到查询 "重构 auth 模块的建议"
  │
  ├── CodeGraph:     "auth 模块有哪些文件和调用链"
  │   → 静态代码图
  │
  ├── MemHop:        "你三周前改 auth 踩过 session 泄漏的坑，
  │                   你读过《OAuth 2.0 实战》第 5 章"
  │   → 涌动记忆图（经验 + 知识）
  │
  └── Thalamus 融合  → 完整上下文
```

---

## 6. 可借鉴的设计模式

### 6.1 MCP 工具设计（借鉴 CodeGraph + Graphify）

当前 MemHop MCP 工具较简单（`store`/`recall`/`dream`/`knowledge_search`）。借鉴 CodeGraph 的 9 工具矩阵：

```
建议新增 MCP 工具：

memhop_context(task_description)
  → 自动定位相关 engram + 扩散关联 + 返回结构化上下文
  → 类比 codegraph_context

memhop_explore(engram_ids)
  → 批量获取 engram 完整内容和关联图
  → 类比 codegraph_explore

memhop_graph(engram_id, depth=2)
  → 以某 engram 为中心展开邻居子图
  → 类比 codegraph_trace

memhop_associations(query, top_k=5)
  → 返回"Agent 可能没意识到的关联"（Dream 发现的跨领域连接）
  → 这是 MemHop 独有的能力

memhop_shelf_status()
  → 挂载的 Shelf 状态（类比 codegraph_status）
```

### 6.2 Chunk 策略（借鉴 GraphRAG + agentmemory）

| 来源 | 可取之处 | MemHop 实现 |
|------|---------|------------|
| GraphRAG | TextUnit 作为最小分析单元，保留 source 引用 | `Knowledge engram` 自带 `source_textunit` |
| agentmemory | 语义去重 (0.85 threshold) | `store()` 前检查语义重复 |
| Graphify | 代码走 AST、文档走 LLM 的分层策略 | `ShelfDomain` 自定义 chunker |
| Mem0 | ADD-only 写入避免冲突 | `store()` 默认 ADD，Dream 做合并 |

### 6.3 Meta Schema 设计（借鉴 Mem0 + GraphRAG）

```rust
// 建议 Knowledge engram 的 meta schema
KnowledgeMeta {
    // 来源追溯
    source_path: String,        // 原始文件路径
    source_textunit: String,    // 原文引用（类比 GraphRAG TextUnit）
    shelf_id: String,
    domain: ShelfDomain,

    // 分块信息
    chunk_index: u32,
    chunk_total: u32,

    // 实体信息（类比 Mem0 Entity Linking）
    entities: Vec<Entity>,      // 提取的关键实体
    // Entity { name, type, confidence }

    // 时间信息（类比 Mem0 Temporal Reasoning）
    created_at: DateTime,
    content_date: Option<DateTime>,  // 文档内容的日期（如论文发表日期）

    // 置信度（类比 Graphify 置信度标签）
    confidence: Confidence,     // EXTRACTED / INFERRED / AMBIGUOUS

    // 领域特定扩展
    domain_meta: HashMap<String, Value>,
}
```

### 6.4 Ranker 设计（借鉴 Mem0 多信号融合）

```rust
// 当前 MemHop recall: 纯 HNSW 语义相似度
// 建议改为三路融合：

fn recall(query) -> Vec<Engram> {
    // 路1: 语义相似度 (HNSW) — 当前已有
    let semantic = hnsw.search(query_embedding, top_k * 3);

    // 路2: 关键词匹配 (BM25/FTS5) — 借鉴 Mem0
    let keyword = fts5.search(query, top_k * 2);

    // 路3: 图扩散 (EntangleGraph) — MemHop 独有
    let graph_diffusion = entangle_graph.spread(semantic[..5], depth=2);

    // 三路融合 + 重排序
    fusion_rank(semantic, keyword, graph_diffusion, query)
}
```

### 6.5 记忆压缩（借鉴 agentmemory + Mem0）

```
当前 MemHop Dream: vitality 衰减 + 边强化

建议增加:
  Dream 阶段检测高密度区域（engram 聚集的语义区）
    → LLM 生成"区域摘要"（类比 GraphRAG 社区摘要）
    → 摘要作为 Schema engram 写入
    → 原始 Knowledge engram vitality 降低但不删除
```

---

## 7. 行动建议

### 7.1 立即行动（v0.10.0 统一重构中）

| # | 建议 | 优先级 | 借鉴来源 |
|---|------|--------|---------|
| 1 | `store()` 默认 ADD-only 语义，避免覆盖冲突 | 🔴 P0 | Mem0 |
| 2 | Knowledge engram 保留 `source_textunit` 引用 | 🔴 P0 | GraphRAG |
| 3 | MCP 新增 `memhop_context` 工具（一站式上下文） | 🟡 P1 | CodeGraph |
| 4 | Shelf mount 预计算 Knowledge engram 间的语义边 | 🟡 P1 | CodeGraph "预建图" |
| 5 | SQLite FTS5 作为 LMDB 的并行全文索引 | 🟡 P1 | CodeGraph |
| 6 | `store()` 前语义去重检查（threshold 0.9） | 🟡 P1 | agentmemory |
| 7 | meta 加入 `confidence` 字段 | 🟢 P2 | Graphify |

### 7.2 短期行动（v0.11.0 垂类扩展中）

| # | 建议 | 优先级 | 借鉴来源 |
|---|------|--------|---------|
| 8 | recall 三路融合：语义 + BM25 + EntangleGraph 扩散 | 🔴 P0 | Mem0 |
| 9 | Dream 阶段社区检测 + 生成主题摘要 Schema engram | 🟡 P1 | GraphRAG |
| 10 | Shelf 文件变更增量更新（不重做全量） | 🟡 P1 | CodeGraph + Graphify |
| 11 | ShelfDomainTrait 内置 Code/Book/Paper/Law 示例 | 🟡 P1 | Graphify 分层提取 |
| 12 | MCP 新增 `memhop_associations`（涌现关联发现） | 🟢 P2 | 独有能力 |
| 13 | meta 增加时间推理字段（`content_date`, `temporal_type`） | 🟢 P2 | Mem0 |

### 7.3 中长期行动（v1.0 及以后）

| # | 建议 | 优先级 | 借鉴来源 |
|---|------|--------|---------|
| 14 | recall 双模：`recall()` 精度 + `recall_global()` 宏观 | 🟡 P1 | GraphRAG |
| 15 | EntangleGraph 上训练轻量 Node2Vec 图嵌入 | 🟢 P2 | GraphRAG |
| 16 | 文件监听 + 自动 mount（`mount_shelf --watch`） | 🟢 P2 | CodeGraph + Graphify |
| 17 | 多设备 LMDB 文件级同步（iCloud/Git） | 🟢 P3 | Graphify union-merge |
| 18 | 与 CodeGraph 的集成协议（Thalamus 统一路由） | 🟢 P3 | 生态互补 |

### 7.4 竞争态势监控

| 监控点 | 触发行动 |
|--------|---------|
| Mem0 推出本地离线版（不加 LLM API） | 加速 v0.10.0 发布，强调 Hebbian 学习差异化 |
| agentmemory 加入图结构 | 加速 EntangleGraph 的 MCP 暴露 |
| Graphify 增加持久化记忆 | 强调 Dream 巩固 + 涌现的不可替代性 |
| CodeGraph 扩展非代码文件 | 转向互补集成（Thalamus 双通道） |
| 大厂（OpenAI/Anthropic）内置记忆 | 强调本地优先 + 隐私 + 离线可用 |

---

## 8. 总结

### 8.1 MemHop 的"不可替代三角"

```
             统一 engram 模型
                  △
                 / \
                /   \
               /     \
              /  MemHop \
             /           \
            /             \
   本地优先 ─────────────── 记忆涌现
  (LMDB, 零外部依赖)       (Hebbian + Dream)
```

- **统一模型**：agentmemory 分离，Mem0 部分统一
- **本地优先**：Mem0 云端，GraphRAG 需 API
- **记忆涌现**：**竞品全空白**

### 8.2 竞争策略

> **短期**：借鉴 Mem0 的 ADD-only + 多信号融合 + 实体链接，追平检索质量差距。
> **中期**：借鉴 GraphRAG 的社区摘要 + 双模检索，提升宏观理解能力。
> **长期**：持续投资 Hebbian 学习 + Dream 巩固，这是任何竞品都难以复制的核心壁垒。

### 8.3 一句话

> **竞品在做"帮你记住"，MemHop 在做"帮你涌现"。这是护城河。**

---

## 📚 数据来源

- CodeGraph GitHub (`colbymchenry/codegraph`) + 深度解析文章
- agentmemory GitHub (`rohitg00/agentmemory`) + 深度解析文章
- Mem0 GitHub (`mem0ai/mem0`) + arXiv 论文 + 深度对比文章
- Graphify GitHub (`safishamsi/graphify`) + 官方文档
- Microsoft GraphRAG GitHub (`microsoft/graphrag`) + CSDN 深度拆解
- FAISS / Milvus / ChromaDB 官方文档 + 对比评测

---

> 本报告由竞析（Compa）独立产出，供产品战略团队决策参考。行动建议请由方向明（Fang）最终审定。
