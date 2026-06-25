# MemHop API 集成文档 v0.47.0

> JSON-in JSON-out 跨语言接口协议。文件格式 .meh，六层认知架构（L0-L5）。所有交互通过 4 个 C 函数完成，所有业务接口通过 `memhop_execute` 传入 JSON 命令。

---

## 目录

- [获取库文件](#获取库文件)
- [4 个 C 函数](#4-个-c-函数)
- [通用格式](#通用格式)
- [架构与性能特性](#架构与性能特性)
- [JSON 命令参考](#json-命令参考)
  - [search](#search--检索记忆)
  - [update](#update--更新记忆)
  - [query_layer](#query_layer--统一查询l0-l5)
  - [update_title](#update_title--统一修改标题)
  - [dream](#dream--记忆整合)
  - [merge_topics](#merge_topics--合并-l2-主题)
  - [import](#import--导入记忆)
  - [session](#session--会话管理)
  - [batch_store](#batch_store--批量存储)
  - [graph_query](#graph_query--l3-图遍历)
  - [delete](#delete--删除记录)
  - [sync](#sync--同步到磁盘)
  - [close](#close--关闭数据库)
- [错误处理](#错误处理)
- [语言绑定](#语言绑定)
  - [C 示例](#c-示例)
  - [Rust FFI 绑定](#rust-ffi-绑定)
  - [Python 示例](#python-示例ctypes)

---

## 获取库文件

从 GitHub Releases 下载对应平台的两个文件：

| 平台                          | 动态库                            | 头文件     |
| ----------------------------- | --------------------------------- | ---------- |
| macOS (Intel + Apple Silicon) | `libmemhop-macos-universal.dylib` | `memhop.h` |
| Linux x86_64                  | `libmemhop-linux-x86_64.so`       | `memhop.h` |
| Windows x86_64                | `memhop-windows-x86_64.dll`       | `memhop.h` |

---

## 4 个 C 函数

```c
// 1. 打开/创建数据库（失败返回 NULL）
void* memhop_open(const char* config_json);

// 2. 执行 JSON 命令（返回 JSON 字符串，必须 memhop_free_string）
char* memhop_execute(void* handle, const char* command_json);

// 3. 释放字符串（NULL 安全）
void memhop_free_string(char* str);

// 4. 关闭数据库，释放句柄
void memhop_close(void* handle);
```

### memhop_open 配置

| 字段                | 类型           | 必需 | 描述                                                                                               |
| ------------------- | -------------- | ---- | -------------------------------------------------------------------------------------------------- |
| `db_path`           | string         | 是   | `.meh` 数据库文件路径                                                                              |
| `vector_dim`        | integer        | 是   | 向量维度（创建时确定，不可更改，通常 768）                                                         |
| `encoder_grpc_addr` | string / null  | 否   | gRPC 编码器地址（TCP）。默认 `"http://127.0.0.1:27110"`；环境变量 `MEMHOP_ENCODER_GRPC_ADDR` 可覆盖；设为 `null` 禁用编码器 |
| `crystal_path`      | string         | 否   | 结晶化知识存储路径                                                                                 |
| `llm`               | object         | 否   | 默认 LLM 配置（见 [LLM 配置](#llm-配置)）                                                          |

```json
{ "db_path": "./data/agent.meh", "vector_dim": 768 }
```

> 编码器连接失败时返回 `MemHopError::EncoderError`，不会静默降级。将 `encoder_grpc_addr` 设为 `null` 可禁用编码器，但依赖向量编码的接口（如向量语义检索、`batch_store`）将不可用。

---

## 通用格式

### 响应格式

```json
// 成功
{"success": true, "data": { ... }}
// 失败
{"success": false, "error": "错误描述"}
```

### 通用分页

所有列表查询返回：

```json
{ "items": [...], "total": 10, "page": 1, "page_size": 20, "has_more": false }
```

### LLM 配置

`dream` 命令与 `search` 命令的 `llm_enhance` 使用同一个 `config::LlmConfig` 类型（OpenAI 兼容格式）。通用配置如下：

| 字段           | 类型   | 默认值                          | 说明                                   |
| -------------- | ------ | ------------------------------- | -------------------------------------- |
| `api_url`      | string | 无（调用方提供）                | 完整聊天补全地址（含 `/chat/completions`）；兼容旧字段 `api_base` |
| `api_key`      | string | 无（调用方提供）                | API 密钥                               |
| `model`        | string | 无（调用方提供）                | 模型名称                               |
| `temperature`  | number | `0.2`                           | 记忆场景高确定性                       |
| `timeout_secs` | number | `30`                            | 请求超时（秒）                         |
| `language`     | string | `"zh"`                          | 默认输出语言                           |

```json
{
  "api_url": "https://api.example.com/v1/chat/completions",
  "api_key": "sk-xxx",
  "model": "your-model",
  "temperature": 0.2,
  "timeout_secs": 30,
  "language": "zh"
}
```

System prompt 按场景拆分：记忆压缩、知识蒸馏、技能结晶、习惯分析。

---

## 架构与性能特性

### 六层认知架构（L0-L5）

| 层级 | 内容                                                         |
| ---- | ------------------------------------------------------------ |
| L0   | Agent 画像 + 用户语言习惯（lexicon / style_traits / emotion_patterns） |
| L1   | 超图结构，关联多个 L2 上下文                                 |
| L2   | 多层级嵌套压缩上下文，共 4 层（Scene / Sub-scene / Turn group / Semantic summary） |
| L3   | 多源超图（Path / Context / Url / Manual）                    |
| L4   | 聊天记录原文 / 归档                                          |
| L5   | 动作链集合 → Skill（含 ActionStep 持久化）                   |

### 核心性能特性

- 文件动态扩展（自动增长 2MB）
- Linear Hash Table O(1) 查找（多页存储，无容量上限）
- IVF 向量索引（近 O(1) 查询）
- SparseIndex 多页序列化（无容量上限）
- 倒排表驱动 BM25 搜索
- L1 反向索引 O(1) 关联查询
- CJK 分词（jieba-rs）
- ARM NEON SIMD 支持

---

## JSON 命令参考

### search — 检索记忆

根据对话检索相关记忆，采用 L2 中心化扇出检索：三路召回 + 加权融合。

- **BM25 关键词检索**（权重 0.50）：基于倒排索引对 L2 上下文标题进行词级打分。
- **向量语义检索**（权重 0.35）：通过编码器计算查询文本与 L2 主题质心的余弦相似度。
- **Entity 实体识别**（权重 0.15）：从 L3 知识图谱节点和 L0 用户词典构建实体词典，使用 BK-Tree 进行精确/模糊匹配。

三路结果分别归一化后按权重融合：`score = 0.50 × BM25 + 0.35 × Vector + 0.15 × Entity`。

L2 嵌套层级支持 4 层，检索范围为 Depth 1-3，Depth 3 结果额外乘以 0.5 降权：

- Depth 1：Scene（场景）
- Depth 2：Sub-scene（子场景）
- Depth 3：Turn group（对话轮次组）
- Depth 4：Semantic summary（语义要点，不参与检索）

**请求**：

```json
{
  "command": "search",
  "dialogue": "我想学习Rust编程",
  "context_id": null,
  "l3_id": null,
  "context_limit": 10,
  "llm_enhance": null,
  "auto_create": 0,
  "min_score": 0.0,
  "context_history": null
}
```

| 字段              | 类型    | 必需 | 默认 | 描述                           |
| ----------------- | ------- | ---- | ---- | ------------------------------ |
| `dialogue`        | string  | 是   | -    | 当前对话内容                   |
| `context_id`      | string  | 否   | null | L2 主题 ID，指定后跳过三重检索 |
| `l3_id`           | string  | 否   | null | 限制只检索包含该 L3 的 L2      |
| `context_limit`   | integer | 否   | 10   | 返回上限                       |
| `llm_enhance`     | object  | 否   | null | LLM 增强配置，字段与 [LLM 配置](#llm-配置) 一致 |
| `auto_create`     | integer | 否   | 0    | 空结果时自动创建 L2            |
| `min_score`       | number  | 否   | 0.0  | 最小相关性阈值                 |
| `context_history` | string  | 否   | null | 前文（LLM 消歧用）             |

**响应 `data`**：

```json
{
  "profile": {
    "id": "...",
    "name": "助手",
    "role": "AI助手",
    "personality": "Rust, AI, 编程, 技术, 游戏",
    "worldview": "",
    "preferences": {
      "top_keywords": "Rust,AI,编程,...",
      "total_engrams": "42"
    },
    "lexicon": { "6": "厉害/牛", "摸鱼": "偷懒休息" },
    "style_traits": ["prefers_brevity", "uses_casual_tone"],
    "emotion_patterns": { "呵呵": "不满或敷衍" },
    "created_at": 1718304000000,
    "updated_at": 1718304000000
  },
  "contexts": [
    {
      "id": "a1b2c3d4e5f67890",
      "parent_id": null,
      "depth": 1,
      "title": "Rust编程学习",
      "summary": "用户学习Rust的过程",
      "activation_score": 0.85,
      "turn_count": 5,
      "l3_refs": ["knowledge_001"],
      "archive_refs": ["archive_001"]
    }
  ],
  "associated_contexts": [],
  "l3_ids": ["knowledge_001"],
  "l3_previews": [
    {
      "id": "knowledge_001",
      "title": "Rust所有权",
      "top_nodes": ["所有权规则", "借用", "生命周期"],
      "keywords": ["ownership", "borrowing", "lifetime"],
      "node_count": 15
    }
  ],
  "archive_refs": [
    {
      "id": "archive_001",
      "context_id": "...",
      "content_type": "text",
      "created_at": 1718304000000
    }
  ]
}
```

---

### update — 更新记忆

将当前对话写入已激活的 L2 上下文（写入 L4 + L5，更新 L2 索引）。

**前置条件**：L2 必须已通过 `search` 激活。

**请求**：

```json
{
  "command": "update",
  "topic_id": "a1b2c3d4e5f67890",
  "dialogue_text": "用户：Rust的借用规则是什么？\n助手：每个引用...",
  "summary": "用户询问Rust借用规则",
  "action_chain": [
    {
      "title": "解释借用规则",
      "description": "向用户解释Rust的借用和引用规则",
      "action_type": "Execute",
      "parameters": null
    }
  ],
  "instant_distill": false
}
```

| 字段              | 类型    | 必需 | 默认  | 描述                                    |
| ----------------- | ------- | ---- | ----- | --------------------------------------- |
| `topic_id`        | string  | 是   | -     | 已激活的 L2 主题 ID                    |
| `dialogue_text`   | string  | 是   | -     | 当前轮对话原文                         |
| `summary`         | string  | 否   | null  | 当前轮压缩摘要                         |
| `action_chain`    | array   | 是   | -     | 动作链                                 |
| `instant_distill` | boolean | 否   | false | 即时蒸馏：从对话提取关键词关联已有 L3 知识图 |

**`action_type`** 枚举：`Create` `Read` `Update` `Delete` `Execute` `Query` `Custom`

**响应**：

```json
{
  "topic_id": "a1b2c3d4e5f67890",
  "archive_id": "archive_002",
  "status": "Updated"
}
```

---

### query_layer — 统一查询（L0-L5）

通过 `layer` + `action` 选择具体操作。

**请求**：

```json
{
  "command": "query_layer",
  "layer": "l0|l1|l2|l3|l4|l5",
  "action": "get|list",
  "get": { "id": "..." },
  "list": {
    "page": 1,
    "page_size": 20,
    "keyword": null,
    "state_filter": null,
    "min_importance": null,
    "active_only": false,
    "domain_filter": null,
    "knowledge_type": null,
    "start_time": null,
    "end_time": null,
    "content_type": null,
    "topic_id": null,
    "node_ids": null,
    "status_filter": null,
    "min_trigger_count": null
  }
}
```

| layer | action | 分支            | 功能            | 响应类型              |
| ----- | ------ | --------------- | --------------- | --------------------- |
| `l0`  | `get`  | —               | 获取 Agent 画像 | `ProfileResult`       |
| `l1`  | `get`  | `get.id` 存在   | 获取单个 Engram | `EngramResult`        |
| `l1`  | `list` | —               | 分页查询 Engram | `EngramListResult`    |
| `l2`  | `get`  | `get.id` 存在   | 获取主题详情    | `TopicDetail`         |
| `l2`  | `list` | —               | 分页查询主题    | `TopicListResult`     |
| `l3`  | `get`  | `get.id` 存在   | 获取知识详情    | `KnowledgeDetail`     |
| `l3`  | `list` | —               | 分页查询知识    | `KnowledgeListResult` |
| `l4`  | `list` | `topic_id` 存在 | 按主题查归档    | `ArchiveListResult`   |
| `l4`  | `list` | `node_ids` 存在 | 按节点查归档    | `ArchiveListResult`   |
| `l4`  | `list` | 两者都无        | 查全部归档      | `ArchiveListResult`   |
| `l5`  | `list` | —               | 查结晶技能      | `CrystalListResult`   |

**各层 List 特有参数**：

| layer | 特有参数                                                                     |
| ----- | ---------------------------------------------------------------------------- |
| L1    | `state_filter` (Active/Latent/Dormant), `min_importance`                     |
| L2    | `active_only` (bool)                                                         |
| L3    | `domain_filter`, `knowledge_type` (Factual/Procedural/Conceptual/Contextual) |
| L4    | `start_time`, `end_time`, `content_type`, `topic_id`, `node_ids`             |
| L5    | `status_filter` (active/inactive/deprecated), `min_trigger_count`            |

---

### update_title — 统一修改标题

**请求**：

```json
{
  "command": "update_title",
  "layer": "l0|l2|l3|l5",
  "params": {
    "id": "...",
    "new_title": "...",
    "name": null,
    "role": null,
    "personality": null,
    "worldview": null,
    "preferences": null,
    "lexicon": null,
    "style_traits": null,
    "emotion_patterns": null
  }
}
```

| layer | 必需字段                                                                                               | 功能                        | 响应类型           |
| ----- | ------------------------------------------------------------------------------------------------------ | --------------------------- | ------------------ |
| `l0`  | 可选 `name`/`role`/`personality`/`worldview`/`preferences`/`lexicon`/`style_traits`/`emotion_patterns` | 更新 Agent 画像（合并策略） | `ProfileResult`    |
| `l2`  | `id`, `new_title`                                                                                      | 修改 L2 主题标题            | `TopicSummary`     |
| `l3`  | `id`, `new_title`                                                                                      | 修改 L3 知识标题            | `KnowledgeSummary` |
| `l5`  | `id`, `new_title`                                                                                      | 修改 L5 结晶标题            | `CrystalSummary`   |

---

### dream — 记忆整合

触发 5 阶段记忆整合管线（L2 压缩 → L1 重建 → L0 画像 → 用户习惯学习 → L3 蒸馏 → L5 结晶）。

`dream` 命令的 LLM 参数直接展开在 JSON 顶层，字段与 [LLM 配置](#llm-配置) 完全一致。

**请求**：

```json
{
  "command": "dream",
  "api_url": "https://api.example.com/v1/chat/completions",
  "api_key": "sk-xxx",
  "model": "your-model",
  "temperature": 0.2,
  "timeout_secs": 30,
  "language": "zh"
}
```

**响应**：

```json
{
  "demoted_to_secondary": [
    {
      "context_id": "ctx-001",
      "original_title": "主题",
      "compressed_summary": "摘要",
      "new_depth": 2
    }
  ],
  "demoted_to_tertiary": ["ctx-002"],
  "removed_contexts": ["ctx-003"],
  "new_compressed": [],
  "l1_updated": ["node-001"],
  "l0_updated": ["profile_001", ["personality"]],
  "habits_updated": {
    "new_lexicon": 3,
    "new_style_traits": 1,
    "new_emotion_patterns": 2,
    "total_dialogues_analyzed": 25
  },
  "new_l3_nodes": ["l3-node-001"],
  "new_crystals": ["crystal-001"],
  "pruned_crystals": [],
  "l1_decayed_nodes": 12,
  "l1_pruned_edges": 5,
  "l1_removed_nodes": 2,
  "l1_removed_edges": 3,
  "duration_ms": 1250
}
```

---

### merge_topics — 合并 L2 主题

**请求**：

```json
{
  "command": "merge_topics",
  "primary_id": "topic_001",
  "secondary_ids": ["topic_002", "topic_003"]
}
```

**响应**：`TopicDetail`

---

### import — 导入记忆

支持两个子动作：

**action=`import`**（导入数据）：

```json
{
  "command": "import",
  "params": {
    "action": "import",
    "target_layer": "profile|topic|knowledge",
    "mode": "merge|overwrite|skip",
    "data": { ... },
    "knowledge_title": null
  }
}
```

**action=`build_l3`**（从文件构建 L3 超图）：

```json
{
  "command": "import",
  "params": {
    "action": "build_l3",
    "path": "/docs/rust-book"
  }
}
```

**`data` 格式**（取决于 `target_layer`）：

Profile：

```json
{
  "Profile": {
    "name": "助手",
    "role": "编程助手",
    "personality": null,
    "worldview": null,
    "preferences": null
  }
}
```

> `Profile` 导入仅支持 `name`、`role`、`personality`、`worldview`、`preferences`。如需更新 `lexicon`、`style_traits`、`emotion_patterns` 等语言习惯，请使用 `update_title` 命令的 `l0` 层。

Topics：

```json
{
  "Topics": [
    {
      "title": "Rust所有权",
      "summary": null,
      "keywords": ["ownership"],
      "knowledge_domain": null
    }
  ]
}
```

Knowledge：

```json
{
  "Knowledge": [
    {
      "title": "Rust所有权规则",
      "domain": "编程",
      "knowledge_type": "Factual",
      "text": "...",
      "summary": null,
      "keywords": [],
      "source_ref": null
    }
  ]
}
```

---

### session — 会话管理

| action       | 必需参数                  | 功能                      |
| ------------ | ------------------------- | ------------------------- |
| `activate`   | `topic_id`                | 激活主题（可选 `ttl_ms`） |
| `deactivate` | `topic_id`                | 停用主题                  |
| `list`       | —                         | 列出所有激活主题          |
| `adjust`     | `topic_id`, `delta` (f32) | 调整激活优先级            |

```json
{"command": "session", "params": {"action": "activate", "topic_id": "a1b2c3d4", "ttl_ms": 600000}}
{"command": "session", "params": {"action": "list"}}
```

**响应**：

```json
// activate
{"activated": "a1b2c3d4"}
// list
{"active_topics": ["a1b2c3d4", "e5f67890"]}
```

---

### batch_store — 批量存储

```json
{
  "command": "batch_store",
  "items": [
    {
      "text": "Rust的所有权系统...",
      "topic_label": "Rust编程",
      "domain_id": "编程",
      "importance": 0.8,
      "valence": null,
      "arousal": null,
      "source": {
        "source_type": "UserInput",
        "source_id": null,
        "timestamp": 1718304000000
      },
      "is_structural": true,
      "source_ref": null
    }
  ],
  "session_id": "session_001",
  "turn_id": "turn_001"
}
```

**`source_type`** 枚举：`UserInput` `SystemGenerated` `ExternalAPI` `FileImport`

**响应**：

```json
{
  "l4_docs": 1,
  "l1_nodes_created": 1,
  "l1_nodes_updated": 0,
  "l2_topics_updated": 1,
  "l3_nodes": 0,
  "edges_created": 0,
  "dedup_skipped": 0
}
```

---

### graph_query — L3 图遍历

从 `start_node` 出发，在指定 L3 超图中按 `edge_kinds` 过滤进行 BFS 遍历，返回可达子图与遍历步信息。

```json
{
  "command": "graph_query",
  "graph_id": "a1b2c3d4e5f67890",
  "start_node": "b2c3d4e5f67890a1",
  "max_depth": 2,
  "edge_kinds": ["Dependency", "Related"]
}
```

| 字段        | 类型            | 必需 | 描述                                                         |
| ----------- | --------------- | ---- | ------------------------------------------------------------ |
| `graph_id`  | string (16 进制 hash) | 是   | L3 超图 ID                                                   |
| `start_node`| string (16 进制 hash) | 是   | 起始节点 ID                                                  |
| `max_depth` | integer         | 是   | 最大遍历深度                                                 |
| `edge_kinds`| string[] / null | 否   | 边类型过滤，可选值：`Related` `Causal` `PartOf` `Sequence` `Dependency` `Custom`；为空或省略时不过滤 |

**响应 `data`**：

```json
{
  "nodes": [
    {
      "id_hash": 1234567890,
      "graph_id": 9876543210,
      "title": "所有权规则",
      "node_type": "concept",
      "content": "Rust 中每个值都有且只有一个所有者",
      "keywords": ["ownership", "rust"],
      "source_ref": "/docs/rust-book/ch04-01-ownership.md",
      "importance": 0.9,
      "created_at": 1718304000000,
      "updated_at": 1718304000000,
      "version": 1
    }
  ],
  "edges": [
    {
      "id_hash": 1111111111,
      "graph_id": 9876543210,
      "kind": "Dependency",
      "node_ids": [1234567890, 9876543210],
      "weight": 0.85,
      "label": "依赖",
      "created_at": 1718304000000
    }
  ],
  "hops": [
    {
      "depth": 1,
      "from_node": 1234567890,
      "edge": { ... },
      "to_node": 9876543210
    }
  ]
}
```

`nodes` 元素字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id_hash` | integer (u64) | 节点 ID，序列化为十进制整数 |
| `graph_id` | integer (u64) | 所属超图 ID |
| `title` | string | 节点标题 |
| `node_type` | string | 类型标签，如 `concept`、`function`、`file` |
| `content` | string | 节点内容 |
| `keywords` | string[] | 关键词 |
| `source_ref` | string / null | 来源引用，如 `/path/file.rs:L10-L50` |
| `importance` | number | 重要度 0.0-1.0 |
| `created_at` | integer | 创建时间戳（毫秒） |
| `updated_at` | integer | 更新时间戳（毫秒） |
| `version` | integer | 版本号 |

`edges` 元素字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id_hash` | integer (u64) | 边 ID |
| `graph_id` | integer (u64) | 所属超图 ID |
| `kind` | string | 边类型：`Related` / `Causal` / `PartOf` / `Sequence` / `Dependency` / `Custom` |
| `node_ids` | integer[] | 连接的节点 ID 数组（超边，长度 ≥ 2） |
| `weight` | number | 权重 |
| `label` | string / null | 语义标签 |
| `created_at` | integer | 创建时间戳（毫秒） |

`hops` 元素字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `depth` | integer | 从起点出发的步数 |
| `from_node` | integer (u64) | 出发节点 ID |
| `edge` | object | 本步经过的边（同 `edges` 元素结构） |
| `to_node` | integer (u64) | 到达节点 ID |

> 注意：请求中的 `graph_id` 与 `start_node` 使用 16 进制字符串，但响应中的 `id_hash`、`graph_id`、`node_ids` 被序列化为十进制整数。如需将其作为字符串 ID 用于其他命令，请自行转换为 16 进制字符串。

---

### delete — 删除记录

按 `layer` 删除指定 ID 的记录。

```json
{ "command": "delete", "layer": "l2", "id": "a1b2c3d4e5f67890" }
{ "command": "delete", "layer": "l3", "id": "a1b2c3d4e5f67890" }
{ "command": "delete", "layer": "l5", "id": "a1b2c3d4e5f67890" }
```

| layer 值              | 含义            | 删除内容                                                     |
| --------------------- | --------------- | ------------------------------------------------------------ |
| `l2` / `topic`        | L2 主题         | ContextSlot 页面、关联 L1 ContextNode、L4 归档、centroid 向量页；更新 SparseIndex 与 L1ReverseIndex |
| `l3` / `knowledge` / `graph` | L3 超图         | 调用内部 `delete_graph`，并清理所有引用该图的 L2 `l3_refs`    |
| `l5` / `crystal` / `action_chain` | L5 行动链       | ActionChainSlot 及其关联的 ActionStep 页面                   |

**响应**：

```json
{ "deleted": true }
```

**错误处理**：

- `layer` 不是上表支持的值时返回：`unsupported delete layer: xxx`
- 各层删除对不存在的 `id` 均为幂等：返回 `{ "deleted": true }`，不会报错
- 删除 L3 超图时会级联清理引用该图的所有 L2 `l3_refs`

---

### sync — 同步到磁盘

```json
{ "command": "sync" }
```

响应：`{"synced": true}`

---

### close — 关闭数据库

```json
{ "command": "close" }
```

执行 checkpoint + sync，然后标记为已关闭。之后应调用 `memhop_close(handle)` 释放句柄。

响应：`{"closed": true}`

---

## 错误处理

所有错误通过 JSON 响应返回，**不会** crash 宿主进程：

```json
{ "success": false, "error": "描述信息" }
```

常见错误：

- `invalid config JSON: ...` — JSON 格式错误
- `invalid command JSON: ...` — 命令 JSON 格式错误
- `missing 'id' for L1 get` — query_layer L1 get 缺少 id
- `unsupported query_layer: layer=l4, action=get` — 不支持的 layer/action 组合
- `unknown import action: 'xxx'` — import action 必须是 `import` 或 `build_l3`
- `unsupported delete layer: xxx` — delete 的 layer 必须是 `l2`/`topic`、`l3`/`knowledge`/`graph`、`l5`/`crystal`/`action_chain` 之一
- `Encoder error: ...` — 编码器未配置或 gRPC 连接失败
- `handle is null` — 传入了空句柄

极罕见情况下 Rust panic 也会被捕获：

```json
{ "success": false, "error": "internal panic: ..." }
```

---

## 语言绑定

### C 示例

```c
#include "memhop.h"
#include <stdio.h>

int main() {
    void* db = memhop_open("{\"db_path\":\"/tmp/test.meh\",\"vector_dim\":768}");
    if (!db) { fprintf(stderr, "open failed\n"); return 1; }

    char* res = memhop_execute(db,
        "{\"command\":\"search\",\"dialogue\":\"Rust的所有权系统\",\"auto_create\":1}");
    if (res) { printf("search result: %s\n", res); memhop_free_string(res); }

    memhop_close(db);
    return 0;
}
```

编译：

```bash
# macOS: clang main.c -L. -lmemhop -o main
# Linux: gcc main.c -L. -lmemhop -o main
# Windows (MSVC): cl main.c memhop.lib
```

### Rust FFI 绑定

```toml
# Cargo.toml
[dependencies]
serde_json = "1.0"
```

```rust
use std::ffi::{CStr, CString};
use std::os::raw::c_char;

extern "C" {
    fn memhop_open(config: *const c_char) -> *mut std::ffi::c_void;
    fn memhop_execute(handle: *mut std::ffi::c_void, cmd: *const c_char) -> *mut c_char;
    fn memhop_free_string(s: *mut c_char);
    fn memhop_close(handle: *mut std::ffi::c_void);
}

pub struct MemHop { handle: *mut std::ffi::c_void }

impl MemHop {
    pub fn open(config: &str) -> Result<Self, String> {
        let cfg = CString::new(config).map_err(|e| format!("CString: {}", e))?;
        let handle = unsafe { memhop_open(cfg.as_ptr()) };
        if handle.is_null() { Err("memhop_open returned null".to_string()) }
        else { Ok(MemHop { handle }) }
    }

    pub fn execute(&self, command: &str) -> Result<serde_json::Value, String> {
        let cmd = CString::new(command).map_err(|e| format!("CString: {}", e))?;
        let res_ptr = unsafe { memhop_execute(self.handle, cmd.as_ptr()) };
        if res_ptr.is_null() { return Err("memhop_execute returned null".to_string()); }
        let res_str = unsafe { CStr::from_ptr(res_ptr) }
            .to_str().map_err(|e| format!("UTF-8: {}", e))?.to_string();
        unsafe { memhop_free_string(res_ptr) };
        let val: serde_json::Value = serde_json::from_str(&res_str).map_err(|e| format!("JSON: {}", e))?;
        if val["success"].as_bool().unwrap_or(false) { Ok(val["data"].clone()) }
        else { Err(val["error"].as_str().unwrap_or("unknown error").to_string()) }
    }
}

impl Drop for MemHop {
    fn drop(&mut self) { if !self.handle.is_null() { unsafe { memhop_close(self.handle) }; } }
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let db = MemHop::open(r#"{"db_path":"/tmp/test.meh","vector_dim":768}"#)?;

    let res = db.execute(r#"{"command":"search","dialogue":"Rust借用检查器","auto_create":1}"#)?;
    let topic_id = res["contexts"][0]["id"].as_str().unwrap_or("").to_string();

    db.execute(&format!(r#"{{"command":"update","topic_id":"{}","dialogue_text":"对话内容","summary":"测试","action_chain":[{{"title":"测试","action_type":"Execute"}}]}}"#, topic_id))?;

    let topics = db.execute(r#"{"command":"query_layer","layer":"l2","action":"list"}"#)?;
    println!("{}", topics);
    Ok(())
}
```

编译运行：

```bash
# macOS: rustc -L. -lmemhop src/main.rs && DYLD_LIBRARY_PATH=. ./main
# Linux: rustc -L. -lmemhop src/main.rs && LD_LIBRARY_PATH=. ./main
```

### Python 示例（ctypes）

```python
import ctypes, json

lib = ctypes.cdll.LoadLibrary("libmemhop-macos-universal.dylib")
lib.memhop_open.restype = ctypes.c_void_p
lib.memhop_execute.restype = ctypes.c_char_p

def exec(db, cmd):
    res = lib.memhop_execute(db, json.dumps(cmd).encode())
    result = json.loads(res.value)
    lib.memhop_free_string(res)
    if result["success"]: return result.get("data")
    raise Exception(result.get("error"))

db = lib.memhop_open(json.dumps({"db_path":"/tmp/test.meh","vector_dim":768}).encode())
print(exec(db, {"command":"search","dialogue":"hello","auto_create":1}))
lib.memhop_close(db)
```
