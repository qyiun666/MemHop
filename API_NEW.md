# MemHop FFI Protocol

> JSON-in JSON-out 跨语言接口协议。文件格式 .meh，六层认知架构（L0-L5）。

---

## 目录

- [快速开始](#快速开始)
- [4 个 C 函数](#4-个-c-函数)
- [数据结构](#数据结构)
- [命令参考](#命令参考)
  - [search](#command-search)：检索记忆
  - [update](#command-update)：更新记忆
  - [query_layer](#command-query_layer)：统一查询（L0-L5）
  - [update_title](#command-update_title)：统一修改标题
  - [dream](#command-dream)：记忆整合
  - [merge_topics](#command-merge_topics)：合并 L2 主题
  - [import](#command-import)：导入记忆
  - [session](#command-session)：会话管理
  - [batch_store](#command-batch_store)：批量存储
  - [graph_query](#command-graph_query)：L3 图遍历
  - [delete](#command-delete)：删除记录
  - [sync](#command-sync)：同步到磁盘
  - [close](#command-close)：关闭数据库
- [错误处理](#错误处理)
- [跨平台构建](#跨平台构建)

---

## 快速开始

```c
#include "memhop.h"
#include <stdio.h>
#include <stdlib.h>

int main() {
    // 1. 打开数据库
    void* handle = memhop_open("{\"db_path\":\"/tmp/test.meh\",\"encoder_grpc_addr\":\"http://127.0.0.1:27110\",\"vector_dim\":768}");
    if (!handle) { fprintf(stderr, "open failed\n"); return 1; }

    // 2. 执行命令（检索记忆）
    char* res = memhop_execute(handle,
        "{\"command\":\"search\",\"dialogue\":\"hello\",\"context_limit\":10,\"min_score\":0.0}");
    printf("%s\n", res);
    memhop_free_string(res);

    // 3. 关闭
    memhop_close(handle);
    return 0;
}
```

---

## 4 个 C 函数

所有交互通过 4 个 `extern "C"` 函数完成：

| 函数                                                     | 作用                            |
| -------------------------------------------------------- | ------------------------------- |
| `memhop_open(config_json)` → `handle`                    | 打开/创建数据库，返回不透明句柄 |
| `memhop_execute(handle, command_json)` → `response_json` | 执行 JSON 命令，返回 JSON 响应  |
| `memhop_free_string(ptr)`                                | 释放 `execute` 返回的字符串     |
| `memhop_close(handle)`                                   | 关闭数据库，释放资源            |

### memhop_open

```c
void* memhop_open(const char* config_json);
```

**config_json** 格式：

| 字段                | 类型    | 必需 | 描述                                                                     |
| ------------------- | ------- | ---- | ------------------------------------------------------------------------ |
| `db_path`           | string  | 是   | `.meh` 数据库文件路径                                                    |
| `encoder_grpc_addr` | string  | 否   | gRPC 编码器地址（TCP，如 `http://127.0.0.1:27110`） |
| `vector_dim`        | integer | 是   | 向量维度（创建时确定，不可更改）                                         |
| `crystal_path`      | string  | 否   | 结晶化知识存储路径                                                       |

示例：

```json
{ "db_path": "./data/agent.meh", "vector_dim": 768 }
```

返回：不透明 `handle` 指针，失败返回 `NULL`。

### memhop_execute

```c
char* memhop_execute(void* handle, const char* command_json);
```

返回 JSON 字符串，必须通过 `memhop_free_string` 释放。响应格式见下方。

### memhop_free_string

```c
void memhop_free_string(char* str);
```

释放 `memhop_execute` 返回的字符串。传入 `NULL` 安全无操作。

### memhop_close

```c
void memhop_close(void* handle);
```

关闭数据库并释放句柄。调用后句柄失效。

---

## 数据结构

### LLM 配置

```json
{
  "api_url": "https://api.example.com/v1/chat/completions",
  "api_key": "sk-xxx",
  "model": "your-model"
}
```

### 配置项

```json
{
  "db_path": "./data/agent.meh",
  "encoder_grpc_addr": "http://127.0.0.1:27110",
  "vector_dim": 768,
  "crystal_path": null
}
```

| 字段                | 类型    | 必需 | 描述                                                                       |
| ------------------- | ------- | ---- | -------------------------------------------------------------------------- |
| `db_path`           | string  | 是   | `.meh` 数据库文件路径                                                      |
| `encoder_grpc_addr` | string  | 否   | gRPC 编码器地址（TCP，环境变量 `MEMHOP_ENCODER_GRPC_ADDR` 可覆盖） |
| `vector_dim`        | integer | 是   | 向量维度（创建时确定，不可更改）                                           |
| `crystal_path`      | string  | 否   | 结晶化知识存储路径                                                         |

**编码器**：仅支持 gRPC over TCP。连接失败时 `memhop_open` 会返回 `NULL`。

### 通用分页

所有列表查询返回：

```json
{
  "items": [...],
  "total": 10,
  "page": 1,
  "page_size": 20,
  "has_more": false
}
```

---

## 命令参考

### 通用响应格式

```json
// 成功
{"success": true, "data": { ... }}

// 失败
{"success": false, "error": "错误描述"}
```

---

<h3 id="command-search">search — 检索记忆</h3>

**接口 2**：根据对话检索相关记忆，采用 L2 中心化扇出检索（向量 + BM25 + n-gram）。

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

| 字段              | 类型    | 必需 | 默认 | 描述                         |
| ----------------- | ------- | ---- | ---- | ---------------------------- |
| `dialogue`        | string  | 是   | -    | 当前对话内容                 |
| `context_id`      | string  | 否   | null | L2主题ID，指定后跳过三重检索 |
| `l3_id`           | string  | 否   | null | 限制只检索包含该 L3 的 L2    |
| `context_limit`   | integer | 否   | 10   | 返回上限                     |
| `llm_enhance`     | object  | 否   | null | LLM增强配置                  |
| `auto_create`     | integer | 否   | 0    | 空结果时自动创建 L2          |
| `min_score`       | number  | 否   | 0.0  | 最小相关性阈值               |
| `context_history` | string  | 否   | null | 前文（LLM消歧用）            |

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

<h3 id="command-update">update — 更新记忆</h3>

**接口 3**：将当前对话写入已激活的 L2 上下文（写入 L4 + L5，更新 L2 索引）。

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
  ]
}
```

| 字段            | 类型   | 必需 | 描述                |
| --------------- | ------ | ---- | ------------------- |
| `topic_id`      | string | 是   | 已激活的 L2 主题 ID |
| `dialogue_text` | string | 是   | 当前轮对话原文      |
| `summary`       | string | 否   | 当前轮压缩摘要      |
| `action_chain`  | array  | 是   | 动作链              |

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

<h3 id="command-query_layer">query_layer — 统一查询（接口 5-12 合并）</h3>

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

**各层 List 参数**：

| layer | 特有参数                                                                     |
| ----- | ---------------------------------------------------------------------------- |
| L1    | `state_filter` (Active/Latent/Dormant), `min_importance`                     |
| L2    | `active_only` (bool)                                                         |
| L3    | `domain_filter`, `knowledge_type` (Factual/Procedural/Conceptual/Contextual) |
| L4    | `start_time`, `end_time`, `content_type`, `topic_id`, `node_ids`             |
| L5    | `status_filter` (active/inactive/deprecated), `min_trigger_count`            |

---

<h3 id="command-update_title">update_title — 统一修改标题（接口 13-16 合并）</h3>

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

<h3 id="command-dream">dream — 记忆整合（接口 4）</h3>

**请求**：

```json
{
  "command": "dream",
  "api_url": "https://api.example.com/v1/chat/completions",
  "api_key": "sk-xxx",
  "model": "your-model"
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
  "duration_ms": 1250
}
```

---

<h3 id="command-merge_topics">merge_topics — 合并 L2 主题（接口 18）</h3>

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

<h3 id="command-import">import — 导入记忆（接口 19）</h3>

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

**`data` 格式**（取决于 `target_layer`，使用 serde 外部标签枚举）：

Profile:

```json
{
  "Profile": {
    "name": "助手",
    "role": "编程助手",
    "personality": null,
    "worldview": null,
    "preferences": null,
    "lexicon": null,
    "style_traits": null,
    "emotion_patterns": null
  }
}
```

Topics:

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

Knowledge:

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

<h3 id="command-session">session — 会话管理（接口 20）</h3>

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

<h3 id="command-batch_store">batch_store — 批量存储（接口 21）</h3>

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

<h3 id="command-graph_query">graph_query — L3 图遍历</h3>

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

**响应**：

```json
{
  "nodes": [...],
  "edges": [...],
  "hops": [...]
}
```

- `nodes`: `HypergraphNode` 数组，包含遍历涉及的所有节点。
- `edges`: `HypergraphEdge` 数组，去重后的边。
- `hops`: 遍历路径步信息（`TraversalHop`），描述从起点出发的每一步。

---

<h3 id="command-delete">delete — 删除记录</h3>

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

不支持的 layer 返回错误。

---

<h3 id="command-sync">sync — 同步到磁盘</h3>

```json
{ "command": "sync" }
```

响应：`{"synced": true}`

---

<h3 id="command-close">close — 关闭数据库</h3>

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

- `invalid UTF-8 in config: ...` — config_json 不是有效 UTF-8
- `invalid config JSON: ...` — JSON 格式错误
- `invalid command JSON: ...` — 命令 JSON 格式错误
- `missing 'id' for L1 get` — query_layer L1 get 缺少 id
- `unsupported query_layer: layer=l4, action=get` — 不支持的 layer/action 组合
- `unknown import action: 'xxx'` — import action 必须是 `import` 或 `build_l3`
- `handle is null` — 传入了空句柄

如果内部发生 Rust panic（极罕见），返回：

```json
{ "success": false, "error": "internal panic: ..." }
```

---

## 跨平台构建

在目标平台上运行：

```bash
cargo build --release
```

产物：

| 平台                          | 产物                                             |
| ----------------------------- | ------------------------------------------------ |
| macOS (Intel + Apple Silicon) | `target/release/libmemhop.dylib`                 |
| Linux                         | `target/release/libmemhop.so`                    |
| Windows                       | `target/release/memhop.dll` (+ `memhop.dll.lib`) |

macOS 通用二进制（Universal Binary）可通过 lipo 合并：

```bash
lipo -create target/x86_64/release/libmemhop.dylib target/aarch64/release/libmemhop.dylib -output libmemhop-universal.dylib
```
