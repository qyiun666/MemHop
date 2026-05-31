# MemHop v0.12.2 — Agent 层接入指南

> 面向 MeowAgent 开发者。MemHop 通过 MCP JSON-RPC 协议暴露，不需要 Cargo 依赖。

---

## 一、概念模型

```
┌─ memhop（一个进程，无状态）───────────────────┐
│  BGE-M3 向量模型 + LMDB 存储                 │
│  收到请求→开DB→处理→返回                     │
│  不记住任何 agent 的会话状态                   │
│  多个 agent 可共用（传自己的 agent_path）       │
└─────────────────────────────────────────────┘
           ▲ MCP (stdio)
           │
┌──────────┴───────────┐
│  meowAgent（有状态）    │
│  cortex（工作记忆）     │
│  session（当前话题）    │
│  自主调 memhop 存/取   │
└──────────────────────┘
```

**MemHop 完全无状态**，不维护任何 agent 会话上下文。Agent 进程维护自己的 cortex（工作记忆）和 session（当前话题）。每轮调用时传 `agent_path`，memhop 开对应 DB 处理完返回。多个 agent 可共享同一个 memhop 进程。

```
~/meow/cats/rust-cat/            ← Agent A 的数据目录
├── brain.db                     ← A 的记忆
└── knowledge/                   ← A 的知识树

~/meow/cats/python-agent/        ← Agent B 的数据目录
├── brain.db                     ← B 的记忆
└── knowledge/                   ← B 的知识树
```

---

## 二、启动 MemHop MCP Server

```bash
MEMHOP_DB_PATH=~/meow/cats/rust-cat/brain.db \
  memhop-mcp-server
```

环境变量：

| 变量 | 用途 | 默认 |
|------|------|------|
| `MEMHOP_DB_PATH` | 脑文件路径。多 agent 时传各自的 agent_path | `./memhop.db` |
| `MEMHOP_ONNX_MODEL` | Candle 编码器路径（BGE-M3） | 内置 ngram 降级 |
| `MEMHOP_RERANKER_PATH` | CrossEncoder 重排模型路径 | 不开启重排 |
| `MEMHOP_WARMUP_ROUNDS` | 暖场轮数（见 §4） | `5` |

Agent 通过 stdin/stdout 的 JSON-RPC 与 MCP server 通信。

---

## 三、MCP 工具清单

### 核心记忆操作

| 工具 | 用途 | v0.12 变化 |
|------|------|-----------|
| `memhop_store` | 写记忆（ADD-only + 自动去重） | 无变化 |
| `memhop_recall` | 召回记忆 | 新增 time_from/to/attach_knowledge/context_id |
| `memhop_dream` | 触发记忆巩固 | 无变化 |
| `memhop_reflect` | 创建反思 engram | 无变化 |

### 知识树管理

| 工具 | 用途 |
|------|------|
| `memhop_mount_tree` | 挂载知识树（书架）— 用户指定路径，MemHop 扫描索引 |
| `memhop_unmount_tree` | 卸载知识树 |
| `memhop_tree_status` | 查看已挂载知识树 |

### 查询与统计

| 工具 | 用途 |
|------|------|
| `memhop_stats` | 脑统计信息（含 phase、活跃上下文数） |
| `memhop_count` | 总 engram 数量 |
| `memhop_health` | 健康指标（uptime、版本、phase） |
| `memhop_list_schemas` | 列出涌现的 Schema engram |
| `memhop_forget` | 删除指定对话轮次 |

### Plan 管理（v0.12.0 自动触发）

| 工具 | 用途 | 说明 |
|------|------|------|
| `memhop_complete_plan` | 手动标记 Plan 完成 | 非必须：Plan 边界检测会自动完成 |
| `memhop_get_plan_tree` | 获取 Plan 树 | 含已完成计划的压缩摘要 |
| `memhop_get_chat_history` | 获取 Plan 关联的对话历史 | 已完成 Plan 的原文可展开 |
| `memhop_plan_stats` | Plan 统计数据 | |

> v0.12.0 新增：Plan 话题切换时自动触发 `compress_plan`，生成 Knowledge 摘要 + 归档原文。

---

## 四、v0.12.0 新增核心概念

### 4.1 三阶段暖场（Warmup）

Agent 无需做任何事，MemHop 内部自动管理：

| 阶段 | 触发条件 | Agent 感知 |
|------|---------|-----------|
| `warmup` | 前 N 轮对话（默认 5） | perceive 返回 `phase: "warmup"` |
| `early` | N 到 2N 轮 | perceive 返回 `phase: "early"`，recall 最多 3 条 |
| `full` | 2N 轮后 | 全部能力激活 |

Agent 可以从 `PerceptionOutput.phase` 知道当前阶段。阶段不影响 API 调用方式。

### 4.2 活跃上下文自动管理

MemHop 内部维护 5 个活跃上下文（类似人脑工作记忆）。Agent 不需要传 `context_id`——MemHop 自动匹配。

但 Agent 可以利用 `PerceptionOutput.context_id` 来知道"当前在聊什么"：

```json
// perceive 返回值新增
{
  "engram_id": "eng_123",
  "current_plan_id": "plan_456",
  "plan_hint": "continuing",
  "plan_name": "登录页面开发",
  "context_id": "ctx_789",     // ← v0.12.0 新增
  "phase": "full"               // ← v0.12.0 新增
}
```

### 4.3 Plan 自动完成与压缩

v0.12.0 中，当 PlanGate 检测到话题切换（`boundary_score > 0.7`），当前 Plan 自动：

1. 生成摘要 → 创建 Knowledge Engram
2. 归档原始对话轮次（`is_archived = true`）
3. PlanNode.state → `Completed`
4. 新话题 → 新 Plan 自动创建

Agent 不需要手动调用 `memhop_complete_plan`，除非需要强制完成。

### 4.4 书架知识自动附带

用户挂载书架后，recall 自动附带相关知识。Agent 不需要：

- 不需要每次调用时传 `tree` 参数
- 不需要补调 `knowledge_search`
- 不需要在 Agent 侧做知识合并

`RecallResponse.knowledge_memories` 字段自动填充。

---

## 五、核心操作详解

### 5.1 召回记忆（memhop_recall）

**v0.12.0 新增参数：**

```json
{
  "method": "tools/call",
  "params": {
    "name": "memhop_recall",
    "arguments": {
      "query": "登录页验证码怎么做的",
      "session_id": "sess_001",
      "limit": 10,
      "time_from": 1717000000000,    // ← v0.12.0: 只查这个时间之后
      "time_to": null,                // ← v0.12.0: 不限时间上限
      "attach_knowledge": true,       // ← v0.12.0: 默认 true
      "context_id": null              // ← v0.12.0: 不传则自动匹配
    }
  }
}
```

**时间范围用法：**

| 场景 | time_from | time_to |
|------|-----------|---------|
| "昨天聊的方案" | 昨天 00:00 时间戳 | 昨天 23:59 时间戳 |
| "最近一周的问题" | 7 天前时间戳 | null |
| "上个月" | 上月 1 号 | 上月最后一天 |

**返回格式（v0.12.0 增强）：**

```json
{
  "results": [
    {
      "id": "eng_456",
      "text": "登录页面验证码用 TOTP 算法",
      "kind": "knowledge",
      "score": 0.91,
      "turn_ids": ["turn_1", "turn_3", "turn_5", "turn_6"],
      "meta": {
        "compressed_from_plan": "plan_login",
        "turn_count": 4
      }
    },
    {
      "id": "eng_123",
      "text": "加个验证码，用 TOTP 算法",
      "kind": "episode",
      "score": 0.82
    }
  ],
  "knowledge_memories": [
    {
      "id": "eng_456",
      "text": "登录页面验证码用 TOTP 算法",
      "kind": "knowledge"
    }
  ],
  "tree_contexts": {
    "/books/rust-async": {
      "domain": "book",
      "source_count": 2
    }
  },
  "graph_associations": []
}
```

> Knowledge 结果带 `turn_ids`，Agent 可以据此展开原文。
> Agent 不需要主动调用 `memhop_get_chat_history`——除非用户要求"具体说说是怎么讨论的"。

### 5.2 写记忆（memhop_store）

v0.12.0 无变化。ADD-only 语义，不覆盖已有 engram。

### 5.3 挂载知识树（memhop_mount_tree）

用户主动挂载一次，后续 recall 自动附带：

```json
{
  "name": "memhop_mount_tree",
  "arguments": {
    "path": "/Users/me/projects/rust-learning",
    "domain": "book"
  }
}
```

domain 取值：`code` | `book` | `paper` | `doc` | `generic`。

行为：扫描目录 → 按 domain 策略切分 → 编码 → 批量写入 Knowledge engram。
路径本身就是标识。卸载时用相同路径。

### 5.4 挂载与自动附带的关系

```
用户操作（一次性）:
  memhop_mount_tree(path="/books/rust-async", domain=book)
  → 扫描 /books/rust-async → 12 章节 → 存为 Knowledge engram
  → 注册到 ShelfManager

后续 recall（自动）:
  recall("Rust async 怎么设计")
    → 自动附带 /books/rust-async 中相关章节
    → 返回在 knowledge_memories 字段
    → Agent 不需要知道路径
```

### 5.5 Plan 完成与原文展开

```json
// 查看已完成 Plan 及其摘要
{
  "name": "memhop_get_plan_tree",
  "arguments": {}
}
// 返回:
{
  "plans": [
    {
      "id": "plan_login",
      "name": "登录页面",
      "state": "completed",
      "compressed_summary": "开发了登录页，含用户名密码+验证码",
      "dialogue_count": 4
    }
  ]
}

// 需要展开原文
{
  "name": "memhop_get_chat_history",
  "arguments": { "plan_id": "plan_login" }
}
// 返回所有 DialogueTurns（含 archived）
```

---

## 六、Agent 接入策略（推荐模式）

### v0.12.0 推荐的调用模式

```
每轮对话:

1. perceive(input)
   ← 返回 { engram_id, context_id, phase, plan_id }

2. recall(query, { attach_knowledge: true })
   ← 返回 results + knowledge_memories + tree_contexts

3. 组装 LLM context:
   ├── 对话历史（从 cortex/working_memory 取最近 N 条）
   ├── recall 结果（episode + knowledge 混合）
   └── knowledge_memories（书架附带，自动已有）

4. LLM 回复
```

**和 v0.11.0 的区别：**
- 不再需要单独 `knowledge_search`
- 不再需要管理 `shelf_id` / `tree` 参数
- 不再需要手动 `complete_plan`（自动）
- 不再需要担心噪音（上下文自动隔离）

### Warmup 期间的策略

```python
if output.phase == "warmup":
    # 前 5 轮：简单回复，不做深度回忆
    llm_context = recent_history_only()
elif output.phase == "early":
    # 第 6-10 轮：轻度检索，有限上下文
    recall_results = recall(query, limit=3)
    llm_context = recent_history() + recall_results
else:  # full
    # 完全模式
    recall_results = recall(query, limit=10, attach_knowledge=True)
    llm_context = recent_history() + recall_results + knowledge_memories
```

---

## 七、Benchmark 与第一梯队计划

### 当前状态

| 基准 | 当前分数 | 目标 | 阻塞项 |
|------|---------|------|--------|
| T2Retrieval NDCG@10 | 0.36（编码器故障） | > 0.95 | 编码器加载修复 |
| LongMemEval 失忆率 | 1.0 | < 0.10 | 编码器加载修复 |
| LongMemEval 幻听率 | 1.0 | < 0.10 | 编码器加载修复 |
| Recall P50@10K | < 1ms（HNSW） | < 1ms | 已达标 |
| 全量测试 | 170 通过 | 100% | 已达标 |

### v0.12.1 目标

```
1. 修复编码器加载（当前 BGE-M3 ONNX 模型加载失败 → 降级到 ngram）
   → 确认 models/bge-m3/ 目录下的模型文件格式
   → 确认 ORT (libonnxruntime) 版本匹配
   → 或切换到 Candle Encoder（纯 Rust，零 C 依赖）

2. 开启 CrossEncoder 重排
   → RecallRequest.use_reranker = true（当前默认 false）
   → NDCG 预期提升 +0.03~0.05

3. 通过 benchmark:
   cargo run --release --bin longmemeval_bench
   → 验证 NDCG@10 > 0.95
   → 验证失忆率/幻听率 < 0.10
```

### 向量模型研究计划

| 阶段 | 模型 | 维度 | 内存占用 | 目标 |
|------|------|------|---------|------|
| 当前 | BGE-M3 (ONNX) | 1024 | ~2GB | 通用中英双语 |
| 调研 | BGE-Small-ZH (ONNX) | 512 | ~500MB | 中文轻量 |
| 调研 | All-MiniLM-L6-v2 (ONNX) | 384 | ~400MB | 英文轻量 |

**未来架构**：双向量库并行

```rust
// 未来 BrainConfig 可能扩展:
pub struct BrainConfig {
    // ...
    /// 中文编码器路径
    pub zh_encoder_path: Option<String>,
    /// 英文编码器路径
    pub en_encoder_path: Option<String>,
    /// 双向量库模式: 根据输入语言自动选择或融合
    pub dual_encoder_mode: DualEncoderMode,
}
```

双向量库的优势：
- 中文场景只需加载 ~500MB（BGE-Small-ZH），而不是 2GB（BGE-M3）
- 英文场景切换至轻量模型
- 中英混合场景双模型同时检索后融合
- 整体内存占用降低 60-75%

当前实现优先级：
1. **先修复 BGE-M3 加载问题**，确保第一梯队基准达标
2. **再调研 BGE-Small-ZH**，测试 C-MTEB 中文检索质量
3. **再调研 All-MiniLM-L6**，测试 BEIR 英文检索质量
4. **最后实现双向量库自动路由**（根据输入语言特征选择/融合）

---

## 八、迁移指南（v0.11.0 → v0.12.0）

### 新增参数（Agent 需要更新的）

| 位置 | 新增 | 默认值 | Agent 是否需要关心 |
|------|------|--------|-------------------|
| perceive 返回 | `context_id` | `null` | 可选，用于知道当前话题 |
| perceive 返回 | `phase` | `"warmup"` | 建议用，控制回复策略 |
| recall 参数 | `time_from` | `null` | 可选，用于时间范围查询 |
| recall 参数 | `time_to` | `null` | 可选 |
| recall 参数 | `attach_knowledge` | `true` | 不需要改，默认就是对的 |
| recall 参数 | `context_id` | `null` | 不需要传，自动匹配 |

### Agent 可以移除的逻辑

```
❌ 移除: 每次 recall 后补调 knowledge_search
   → 书架知识已经在 knowledge_memories 字段

❌ 移除: Agent 侧的知识合并/去重
   → MemHop 已经融合好了 Episode + Knowledge

❌ 移除: 手动管理 context/session/tree 参数
   → 活跃上下文自动匹配

❌ 移除: 手动调用 complete_plan
   → PlanGate 自动检测话题边界并压缩
```

---

## 九、SLA 承诺

| 指标 | 目标 | 条件 |
|------|------|------|
| recall p50 | < 2ms | 含 JSON 序列化 |
| recall p99 | < 5ms | @ 100K engrams |
| perceive p50 | < 1ms | 含编码 + 上下文匹配 |
| mount_tree (100 chunks) | < 5s | 含扫描+编码+写入 |
| unmount_tree (100 chunks) | < 1s | 软删除 |
| 启动时间 | < 3s | @ 100K engrams |
| 内存占用（BGE-M3） | < 2GB | @ 200K engrams |
| 内存占用（BGE-Small-ZH） | < 1GB | @ 200K engrams |

---

## 十、v0.12.1 新功能：知识树 · 纠缠 · 三观

### 10.1 知识树（Tree）

知识树是人脑中的"领域"概念——工作、旅游、孩子、编程……每棵树独立管理记忆。

Agent 可以通过 MCP 工具创建和管理知识树：

```json
// 创建知识树
{
  "name": "memhop_create_tree",
  "arguments": { "name": "Rust 学习", "domain": "programming" }
}
// 返回: {"tree_id": "tree_1717000000000", "name": "Rust 学习", "domain": "programming"}

// 列出所有树
{ "name": "memhop_list_trees", "arguments": {} }

// 获取单棵树
{ "name": "memhop_get_tree", "arguments": { "tree_id": "tree_xxx" } }

// 将记忆移动到指定树
{ "name": "memhop_move_to_tree", "arguments": { "engram_id": "eng_xxx", "tree_id": "tree_xxx" } }

// 删除树（不解绑 engram）
{ "name": "memhop_delete_tree", "arguments": { "tree_id": "tree_xxx" } }
```

recall 新增 `tree_id` 参数，只返回指定树的记忆：
```json
{
  "name": "memhop_recall",
  "arguments": {
    "query": "async/await",
    "tree_id": "tree_xxx",
    "limit": 10
  }
}
```

### 10.2 纠缠事件（EntanglementEvent）

当 recall 结果来自 ≥ 2 棵不同树时，MemHop 自动创建纠缠事件——记录"跨领域的认知迁移"。

```json
// 列出所有纠缠事件（按强度排序）
{ "name": "memhop_list_entanglements", "arguments": {} }

// 查看单个事件详情
{ "name": "memhop_entanglement_detail", "arguments": { "event_id": "ent_xxx" } }
```

纠缠事件自动在以下时机创建：
- **recall 时**：结果跨树命中 ≥ 2 棵 → 创建 `RecallCrossTree` 事件
- **Plan 压缩时**：涉及 ≥ 2 棵树 → 创建 `PlanCompression` 事件
- **Dream 时**：跨 Anchor 发现跨树关联 → 创建 `DreamEmergence` 事件

事件衰减：30 天未再次触发 → 每天强度衰减 10%，< 0.1 自动删除。

### 10.3 三观模式（WorldviewPattern）

纠缠事件积累 ≥ 10 个后，Dream REM 阶段自动聚类涌现出用户的稳定思维模式。

```json
// 列出所有三观模式
{ "name": "memhop_list_worldviews", "arguments": {} }

// 查看单个模式详情
{ "name": "memhop_worldview_detail", "arguments": { "wv_id": "wv_xxx" } }

// 自然语言摘要
{ "name": "memhop_my_worldview", "arguments": {} }
// 返回: {"summary": "[ThinkingStyle] 倾向于先抽象再具体 (稳定度: 0.8)\n...", "patterns": [...]}
```

三观召回介入：稳定度 > 0.7 的模式自动附加到 recall 的 `worldview_context` 字段。
认知冲突检测：用户输入包含否定/转折词时，标记可能的认知冲突。

### 10.4 和旧版本的兼容

| 旧字段 | v0.12.1 替代 | 状态 |
|--------|-------------|------|
| `tree_path: Option<String>` | `tree_ref: Option<TreeRef>` | 保留，deprecated |
| `AssociationKind::CrossTree` | `EntanglementEvent` | 保留，补充层 |
| — | `WorldviewPattern` | 全新 |

---

## 十一、v0.12.2 架构变更

### 11.1 无状态架构

v0.12.2 将 memhop 改造为**无状态记忆引擎**：

- **MemHop 进程**：无状态，只存储+检索，不维护任何 agent 会话上下文
- **Agent 进程**：维护自己的 cortex（工作记忆）和 session（当前话题状态）
- **共享模型**：BGE-M3 向量模型只加载一份，所有 agent 共享

### 11.2 多 Agent 共享

```json
// 所有 MCP 工具新增 agent_path 可选参数
{"name":"memhop_store",  "arguments":{
    "text":"...", "agent_path":"~/agent_a/memhop"}}
{"name":"memhop_recall", "arguments":{
    "query":"...", "agent_path":"~/agent_a/memhop"}}
{"name":"memhop_dream",  "arguments":{
    "agent_path":"~/agent_a/memhop"}}
```

- `agent_path` 缺省时走 `MEMHOP_DB_PATH` 环境变量
- memhop 内部按路径缓存 Brain 实例
- 同一 agent 的连续调用共享同一个 Brain（含 HNSW/Hopfield 内存索引）

### 11.3 架构重构

| 变化 | 说明 |
|------|------|
| Dream 阶段抽出 dream/ 模块 | brain.rs 减少 ~900 行 |
| 新增 organize/ 模块 | 每轮 perceive 后自动提取实体+链接图+边界检测 |
| 新增 session/ 模块 | Agent 侧会话上下文聚合（Cortex + ActiveContexts） |
| main.rs 多 Brain 缓存 | HashMap<String, Brain> 按 agent_path 路由 |
| Agent 自主控制 Dream | memhop 不再自动触发 dream，由 agent 决定时机 |

### 11.4 Agent 推荐接入模式

```
每轮对话（v0.12.2 推荐）:

1. agent 从自己的 Session 中取出 cortex（工作记忆）
2. memhop_recall(query, agent_path) → 返回 L2 联想召回结果
3. agent 组装精简上下文：cortex(最近N轮) + recall(相关记忆)
4. LLM 生成回复
5. memhop_store(input, response, agent_path) → 自动触发 organize
6. agent 更新自己的 Session（cortex、话题等）
7. agent 自主决定何时调 dream / 读 worldview
```

### v0.12.3 清理

P0: 删除 engine/ 僵尸代码（4空文件+旧API）
P1: 8个方法从 brain.rs 搬到 entanglement/worldview/organize 正确模块
Dream：完全移除内部自动触发，Dream 仅由 agent 通过 memhop_dream 显式调用
brain.rs 从 v0.12.1 的 4195 行 → v0.12.3 的 ~3040 行（净减 ~1150 行）

---

## 附录 A: MemoryOrgan trait 完整适配映射

### 方法级映射

| MemoryOrgan trait | MCP 工具 | JSON-RPC 工具名 | 适配状态 |
|------------------|---------|----------------|---------|
| `store(req: StoreRequest)` | memhop_store | tools/call | ✅ 已适配 |
| `recall(req: RecallRequest)` | memhop_recall | tools/call | ✅ 已适配 |
| `forget(id: &str)` | memhop_forget | tools/call | ✅ 已适配 |
| `reflect(input: ReflectInput)` | memhop_reflect | tools/call | ✅ 已适配 |
| `list_schemas()` | memhop_list_schemas | tools/call | ✅ 已适配 |
| `create_tree(name: &str)` | memhop_create_tree | tools/call | ❌ 返回 no-op，待实现 |
| `remove_tree(name: &str)` | memhop_delete_tree | tools/call | ❌ 返回 no-op，待实现 |
| `update(id: &str, data: Value)` | — | — | ❌ 返回 no-op，memhop 无对应工具 |

### StoreRequest 字段映射

| meowAgent StoreRequest 字段 | memhop_store 参数 | 转换逻辑 |
|----------------------------|-------------------|---------|
| `text: String` | `content` | 直接映射 |
| `layer: MemoryLayer` | `emotional_state` | layer → (valence, arousal) 映射表见下 |
| `meta: Value` | `meta` | 直接映射为 JSON object |
| `tree: Option<String>` | `tree_id` | v0.12.1 新增映射 |
| `turn_id: Option<String>` | `turn_id` | 直接映射 |
| `turn_index: Option<usize>` | `segment_index` | 类型转换 usize → u32 |
| `topic_label: Option<String>` | `topic_label` | 直接映射 |

**MemoryLayer → EmotionalState 映射** (在 `memory_memhop.rs:310-317`):

| MemoryLayer | valence | arousal | 语义 |
|------------|---------|---------|------|
| `Working` | 0.2 | 0.4 | 中性，中度唤醒 |
| `Episodic` | 0.1 | 0.5 | 略偏负面，高唤醒 |
| `Semantic` | 0.0 | 0.3 | 中性，低唤醒 |
| `Procedural` | 0.0 | 0.2 | 中性，低唤醒 |

### RecallRequest 字段映射

| meowAgent RecallRequest 字段 | memhop_recall 参数 | 转换逻辑 |
|----------------------------|-------------------|---------|
| `query: String` | `query` | 直接映射 |
| `k: usize` | `limit` | 直接映射 |
| `scope: Option<RecallScope>` | `kind_filter` + `tree_id` | 枚举展开 |
| `recent_turns: Vec<String>` | — | v0.12.1 暂未使用 |
| `tree: Option<String>` | `tree_id` | 直接映射 |

**RecallScope → memhop 参数映射建议:**

| RecallScope | kind_filter | tree_id | limit 调整 |
|-------------|-------------|---------|-----------|
| `All` | `[]` (空) | null | 不变 |
| `Episodes` | `["episode"]` | null | 不变 |
| `Knowledge` | `["knowledge"]` | null | 不变 |
| `Tree(id)` | `[]` | `id` | 不变 |

### 接口未使用的 memhop 能力

以下是 meowAgent 的 MemoryOrgan trait 当前**没有暴露**但 memhop 支持的能力。建议在 MemoryOrgan trait 中新增方法或扩展参数：

| 缺失能力 | 对应 MCP 工具 | 实现方式 | 优先级 |
|---------|-------------|---------|--------|
| 创建/删除逻辑树 | `memhop_create_tree` / `memhop_delete_tree` | 修复 create_tree/remove_tree no-op | **P0** |
| 列出所有树 | `memhop_list_trees` | 新增方法 | **P0** |
| 获取三观摘要 | `memhop_my_worldview` | 新增方法，注入 system prompt | **P0** |
| 列出纠缠事件 | `memhop_list_entanglements` | 新增方法，用于跨领域联想 | **P0** |
| 列出三观模式 | `memhop_list_worldviews` | 新增方法 | **P0** |
| 手动触发 Dream | `memhop_dream` | 新增方法，定期调用 | P1 |
| Plan 统计 | `memhop_plan_stats` | 新增方法，了解话题分布 | P1 |

---

## 附录 B: 26 个 MCP 工具 JSON-RPC Schema 参考

### B.1 核心记忆操作

| # | 工具 | 请求参数 | 响应字段 |
|---|------|---------|---------|
| 1 | **memhop_store** | `{content, vector?, emotional_state, attention_anchors, session_id, plan_id?, turn_id?, tree_id?, ...}` | `{engram_id, current_plan_id, plan_hint, context_id?, phase?}` |
| 2 | **memhop_recall** | `{query, query_vector?, session_id, emotional_state, limit, mode, use_reranker?, tree_id?, kind_filter?, time_from?, time_to?, attach_knowledge?, ...}` | `{working_memory[], associations[], schemas[], knowledge_memories[], tree_contexts{}, worldview_context[], cognitive_conflicts[]}` |
| 3 | **memhop_reflect** | `{content, kind, session_id}` | `{reflection_id, ...}` |
| 4 | **memhop_dream** | `{}` | `{engrams_consolidated, schemas_emerged, entanglements, worldview_updates}` |
| 5 | **memhop_forget** | `{turn_id}` | `{success}` |

### B.2 查询与统计

| # | 工具 | 请求参数 | 响应字段 |
|---|------|---------|---------|
| 6 | **memhop_stats** | `{}` | `{total_engrams, config, growth_state, personality, ...}` |
| 7 | **memhop_count** | `{}` | `{count}` |
| 8 | **memhop_health** | `{}` | `{uptime, version, phase, total_engrams, ...}` |
| 9 | **memhop_list_schemas** | `{}` | `{schemas: [{id, text, summary, keywords, extra: {stability, consistency, ...}}]}` |

### B.3 Plan 管理

| # | 工具 | 请求参数 | 响应字段 |
|---|------|---------|---------|
| 10 | **memhop_complete_plan** | `{plan_id}` | `{compressed_to_knowledge_id?, status}` |
| 11 | **memhop_get_plan_tree** | `{}` | `{tree: [{id, name, state, compressed_summary, dialogue_count}]}` |
| 12 | **memhop_get_chat_history** | `{plan_id}` | `{turns: [{id, user_input, agent_response, timestamp}]}` |
| 13 | **memhop_plan_stats** | `{start_time?, end_time?}` | `{plan_count, domain_distribution[], tone_trend}` |

### B.4 知识树挂载 (Shelf, v0.11.0)

| # | 工具 | 请求参数 | 响应字段 |
|---|------|---------|---------|
| 14 | **memhop_mount_tree** | `{path, domain?}` | `{tree_path, chunk_count, domain, warnings[]}` |
| 15 | **memhop_unmount_tree** | `{tree_path}` | `{tree_path, deleted_count}` |
| 16 | **memhop_tree_status** | `{tree_path?}` | `{trees: [{tree_path, domain, chunk_count}], count}` |

### B.5 逻辑知识树 (Tree, v0.12.1)

| # | 工具 | 请求参数 | 响应字段 |
|---|------|---------|---------|
| 17 | **memhop_create_tree** | `{name, domain}` | `{tree_id, name, domain}` |
| 18 | **memhop_list_trees** | `{}` | `[{id, name, domain, memory_count, last_active_at, shelf_paths}]` |
| 19 | **memhop_get_tree** | `{tree_id}` | `{id, name, domain, description, memory_count, last_active_at, shelf_paths, created_at}` |
| 20 | **memhop_move_to_tree** | `{engram_id, tree_id}` | `{status}` |
| 21 | **memhop_delete_tree** | `{tree_id}` | `{status}` |

### B.6 纠缠事件 (v0.12.1)

| # | 工具 | 请求参数 | 响应字段 |
|---|------|---------|---------|
| 22 | **memhop_list_entanglements** | `{}` | `[{id, nodes[], tree_ids[], context, trigger, strength, plan_ids[], created_at, last_hit_at, hit_count}]` |
| 23 | **memhop_entanglement_detail** | `{event_id}` | `{id, nodes[], tree_ids[], context, trigger, strength, plan_ids[], created_at, last_hit_at, hit_count}` |

### B.7 三观 (v0.12.1)

| # | 工具 | 请求参数 | 响应字段 |
|---|------|---------|---------|
| 24 | **memhop_list_worldviews** | `{}` | `[{id, pattern, category, stability, occurrence_count, emerged_at}]` |
| 25 | **memhop_worldview_detail** | `{wv_id}` | `{id, pattern, category, stability, source_events[], occurrence_count, emerged_at, last_reinforced_at}` |
| 26 | **memhop_my_worldview** | `{}` | `{summary: "自然语言描述...", patterns: [...]}` |

---

## 附录 C: P0 建议 — MeowAgent 优先接入的特性

以下特性对 Agent 能力提升最大，建议优先适配：

### P0-1: create_tree / remove_tree (修复 no-op)

当前 `MemoryOrgan` trait 的这两个方法返回空操作。v0.12.1 提供完整实现：

```rust
// memory_memhop.rs
async fn create_tree(&self, name: &str) -> Result<()> {
    let result = self.mcp.call("memhop_create_tree", json!({
        "name": name,
        "domain": "generic"
    })).await?;
    // 处理返回的 tree_id
    Ok(())
}

async fn remove_tree(&self, name: &str) -> Result<()> {
    // 先通过 list_trees 找到 tree_id
    let trees = self.mcp.call("memhop_list_trees", json!({})).await?;
    if let Some(tree_id) = find_tree_by_name(trees, name) {
        self.mcp.call("memhop_delete_tree", json!({"tree_id": tree_id})).await?;
    }
    Ok(())
}
```

### P0-2: memhop_my_worldview (注入 system prompt)

每次用户对话前，获取 MemHop 对用户的"三观摘要"，注入 LLM 的 system prompt：

```python
# 每 session 开始时
worldview = mcp.call("memhop_my_worldview")
system_prompt += f"\n## 对用户的了解\n{worldview['summary']}"
```

效果：LLM 回复更贴合用户的思维方式和偏好。

### P0-3: memhop_list_trees (对话中感知领域)

```python
# 感知当前在聊什么
trees = mcp.call("memhop_list_trees")
active_trees = [t for t in trees if t["last_active_at"] > (now - 3600000)]
# → 知道最近在聊哪些领域
```

### P0-4: memhop_list_entanglements (知识联想)

```python
# 当用户问题涉及混合领域时
entanglements = mcp.call("memhop_list_entanglements")
strong_crossings = [e for e in entanglements if e["strength"] > 0.5]
# → 用于激发更丰富的联想
```
