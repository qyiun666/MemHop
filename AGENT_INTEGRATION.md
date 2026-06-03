# MemHop v0.14 — meowAgent 集成指南

## 一、概述

MemHop 是一个嵌入式联想记忆引擎，通过 MCP (JSON-RPC 2.0 over Unix Socket) 对外提供记忆服务。

### 核心接口

| 接口 | 功能 | 优先级 |
|------|------|--------|
| `memhop_batch_store` | 批量写入记忆 | P0 |
| `memhop_recall` | 按内容检索记忆 | P0 |
| `memhop_consolidate` | 触发记忆巩固（当前版本返回错误，暂未实现） | P2 |
| `memhop_health` | 健康检查 | P1 |

---

## 二、启动

### 环境变量

```bash
MEMHOP_BRAINS_DIR=/path/to/brains   # 默认 ./memhop_brains
MEMHOP_SOCKET=/tmp/memhop.sock      # 默认 /tmp/memhop.sock
```

### 启动命令

```bash
memhop-mcp-server
# 输出: memhop-mcp-server v0.14.0 listening on /tmp/memhop.sock
```

> **注意**：MemHop 按 `agent_id` 缓存 Brain 实例，首次访问后常驻内存直至进程退出。同一 agent 的请求串行处理。切换 `MEMHOP_BRAINS_DIR` 需要重启进程。

---

## 三、MCP 工具

### 3.1 memhop_batch_store

批量写入记忆。**所有写入都通过此工具**。

```json
// 请求
{
  "jsonrpc": "2.0",
  "method": "memhop_batch_store",
  "params": {
    "agent_id": "cat_1",
    "items": [{
      "text": "user: 她喜欢喝可乐 | assistant: 了解",
      "source": "chat",
      "turn_id": "session_1_T5",
      "session_id": "session_1",
      "topic_label": "饮品偏好",
      "llm_keywords": ["可乐", "偏好"],
      "llm_compressed_summary": "用户告知她喜欢可乐",
      "chain_parent_id": null,
      "chain_label": null,
      "domain_id": null
    }]
  },
  "id": 1
}

// 响应
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "l1_nodes_created": 1,
    "l1_hyperedges_created": 0,
    "l2_topics_created": 1,
    "l3_nodes_created": 0,
    "l4_docs_stored": 1,
    "chains_created": 0,
    "total_duration_us": 1234
  }
}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识，用于隔离数据，默认 `"default"` |
| `items[].text` | string | 是 | 原始对话文本（必填，至少传一条） |
| `items[].turn_id` | string | 否 | 对话轮次 ID |
| `items[].session_id` | string | 否 | 会话 ID |
| `items[].source` | string | 否 | 来源，默认 `"chat"` |
| `items[].topic_label` | string | 推荐 | 话题分类标签（推荐提供） |
| `items[].llm_keywords` | string[] | 推荐 | 关键词（推荐提供） |
| `items[].llm_compressed_summary` | string | 推荐 | 摘要（推荐提供） |
| `items[].chain_parent_id` | string | 否 | 超边链：前一个事件的 hyperedge_id |
| `items[].chain_label` | string | 否 | 超边链标签：`correction`/`supplement`/`merge` |
| `items[].domain_id` | string | 否 | 关联的知识领域 ID |
| `items[].valence` | float | 否 | 情绪效价（预留，暂不持久化） |
| `items[].arousal` | float | 否 | 情绪唤醒度（预留，暂不持久化） |

> `chain_parent_id` + `chain_label` 用于纠正场景：用户说"可乐"，过一会纠正为"雪碧"，传入前一条的 hyperedge_id 建立纠正链。

---

### 3.2 memhop_recall

按内容检索记忆，支持指定检索层和时间范围过滤。

```json
// 请求
{
  "jsonrpc": "2.0",
  "method": "memhop_recall",
  "params": {
    "agent_id": "cat_1",
    "query": "用户喜欢什么饮料",
    "max_results": 10,
    "target_layers": ["L1", "L2", "L4"]
  },
  "id": 1
}

// 响应
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "results": [
      {
        "layer": "L2",
        "id": "topic_xxx",
        "text": "用户告知她喜欢可乐",
        "score": 0.61,
        "topic_label": "饮品偏好",
        "created_at": 1749123456000,
        "version": 1
      }
    ],
    "total_count": 1
  }
}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识，用于隔离数据，默认 `"default"` |
| `query` | string | 否 | 搜索文本，为空返回空结果 |
| `max_results` | int | 否 | 返回条数上限，默认 10 |
| `target_layers` | string[] | 否 | 目标层：`L1`/`L2`/`L3`/`L4`（大写敏感），默认 `L1, L2, L4` |
| `time_range` | [i64, i64] | 否 | 毫秒时间戳范围 `[start, end]`（预留，暂未生效） |

> **响应字段说明**：
> - `score`：各层原始匹配分数（L1 BM25 / L2-L4 ngram 重叠分），非概率值，仅用于相对排序
> - `total_count`：返回结果数（≤ `max_results`），非原始匹配总数
> - `created_at`：毫秒 Unix 时间戳
> - `text`：L1/L3/L4 层截断为前 200 字符，L2 层为完整话题内容

---

### 3.3 memhop_health

健康检查。

```json
// 请求
{"jsonrpc":"2.0","method":"memhop_health","params":{"agent_id":"cat_1"},"id":1}
// 响应
{"jsonrpc":"2.0","id":1,"result":{"status":"ok","version":"0.14.0"}}
```

---

### 3.4 memhop_consolidate

触发记忆巩固。**当前版本尚未实现，调用将返回错误**。

```json
// 请求
{"jsonrpc":"2.0","method":"memhop_consolidate","params":{"agent_id":"cat_1"},"id":1}
// 响应
{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"internal error: consolidate not yet implemented"}}
```

---

### 3.5 已删除的工具

以下 v0.13 时期的工具已移除：

| 工具 | 替代方案 |
|------|----------|
| `memhop_store` | 已移除，请使用 `memhop_batch_store` 批量写入 |
| `memhop_dream` | 暂未实现，后续补充 |
| `memhop_reflect` | 不再需要，batch_store 时传入 llm_compressed_summary |
| `memhop_mount/unmount/list_trees` | L3 领域通过 domain_id 管理 |
| `memhop_create/delete/get_tree` | 不再需要 |
| `memhop_move_to_tree` | 不再需要 |
| `memhop_complete/get/plan_stats` | 不再需要 |
| `memhop_get_chat_history` | 待实现 |
| `memhop_list/compress_context` | 不再需要 |
| `memhop_list_entanglements` | 暂不提供，后续版本计划通过 recall 返回链信息 |
| `memhop_list_worldviews` | 待实现 |
| `memhop_list_schemas` | 不再需要 |
| `memhop_knowledge_search` | 待实现 |

---

## 四、meowAgent 接入说明

### 4.1 建议提供的字段

| meowAgent 侧功能 | 说明 |
|-----------------|------|
| `topic_label` | 话题分类标签，topic_splitter 的输出，传入 `memhop_batch_store` |
| `llm_keywords` | LLM 提取的关键词，用于改善检索 |
| `llm_compressed_summary` | LLM 生成的摘要 |
| `chain_parent_id` / `chain_label` | 用户纠正旧记忆时传入，建立超边链 |

### 4.2 MemoryOrgan trait 参考

以下 trait 定义在 **meowAgent 侧**（非 memhop 导出）：

```rust
#[async_trait]
pub trait MemoryOrgan: Send + Sync {
    async fn batch_store(&self, items: Vec<StoreItem>) -> Result<BatchReport>;
    async fn recall(&self, query: &str, max_results: usize) -> Result<Vec<MemoryEntry>>;
    async fn consolidate(&self) -> Result<ConsolidateReport>;
    fn name(&self) -> &'static str;
}
```

### 4.3 接入示例

```rust
// 批量写入
let mut items = Vec::new();
for (topic_label, text) in segments {
    items.push(StoreItem {
        text: text.to_string(),
        topic_label: Some(topic_label.to_string()),
        llm_keywords: Some(keywords.clone()),
        llm_compressed_summary: Some(summary.clone()),
        turn_id: Some(turn_id.clone()),
        session_id: Some(session_id.clone()),
        source: "chat".to_string(),
        ..Default::default()
    });
}
let report = memhop.batch_store(items).await?;

// 检索
let results = memhop.recall("用户喜欢什么饮料", 10).await?;
```
