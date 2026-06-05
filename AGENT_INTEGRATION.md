# MemHop v0.18.0 — meowAgent 集成指南

## 概述

MemHop 是嵌入式联想记忆引擎，通过 MCP (JSON-RPC 2.0 over Unix Socket) 对外提供记忆服务。

5 层架构：L0 角色画像 + L1 纠缠超图 + L2 话题图 + L3 领域超图 + L4 原文库。

## 启动

```bash
# 环境变量
MEMHOP_BRAINS_DIR=/path/to/brains   # 默认 ./memhop_brains
MEMHOP_SOCKET=/tmp/memhop.sock      # 默认 /tmp/memhop.sock
MEMHOP_MODEL_PATH=/path/to/model    # 可选，Candle 编码器模型路径

# 启动
memhop-mcp-server
# 输出: memhop-mcp-server v0.18.0 listening on /tmp/memhop.sock
```

---

## 接口总览

| 接口 | 功能 | 优先级 |
|------|------|--------|
| `memhop_batch_store` | 批量写入记忆 | P0 |
| `memhop_recall` | 语义检索记忆 | P0 |
| `memhop_health` | 健康检查 | P0 |
| `memhop_consolidate` / `memhop_dream` | 记忆巩固（梦境模拟） | P1 |
| `memhop_organize` | 记忆组织（节点归类） | P1 |
| `memhop_mount_shelf` | 挂载知识库到 L3 | P1 |
| `memhop_unmount_shelf` | 卸载知识库 | P1 |
| `memhop_list_shelf` | 列出已挂载知识库 | P1 |
| `memhop_get_profile` | 获取 L0 角色画像 | P1 |
| `memhop_set_profile` | 设置 L0 角色画像（简版） | P1 |
| `memhop_set_l0` | 设置 L0 角色画像（完整版） | P1 |
| `memhop_get_activated` | 获取当前激活的话题列表 | P2 |
| `memhop_activate` | 激活话题 | P2 |
| `memhop_deactivate` | 去激活话题 | P2 |
| `memhop_feedback` | 检索结果反馈（调整激活权重） | P2 |
| `memhop_get_l4_raw` | 获取 L4 原始文档 | P2 |
| `memhop_list_l3_paths` | 列出 L3 领域路径 | P2 |
| `memhop_list_topics` | 列出所有 L2 话题 | P2 |
| `memhop_re_search` | 正则搜索记忆 | P2 |
| `memhop_update_topic` | 更新话题元数据 | P2 |
| `memhop_stats` | 获取引擎统计信息 | P2 |

---

## P0 核心接口

### memhop_batch_store

批量写入记忆。**所有写入都通过此接口**。

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
      "domain_id": null,
      "importance": 0.8
    }]
  },
  "id": 1
}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识，默认 `"default"` |
| `items[].text` | string | **是** | 原始文本 |
| `items[].source` | string | 否 | 来源，默认 `"chat"` |
| `items[].turn_id` | string | 否 | 对话轮次 ID |
| `items[].session_id` | string | 否 | 会话 ID |
| `items[].topic_label` | string | 推荐 | 话题标签 |
| `items[].llm_keywords` | string[] | 推荐 | 关键词 |
| `items[].llm_compressed_summary` | string | 推荐 | 摘要 |
| `items[].chain_parent_id` | string | 否 | 超边链前驱 ID |
| `items[].chain_label` | string | 否 | 链标签：`correction`/`supplement`/`merge` |
| `items[].domain_id` | string | 否 | 关联领域 ID |
| `items[].importance` | float | 否 | 重要性权重 |

```json
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

---

### memhop_recall

语义检索记忆，支持指定检索层、时间范围、话题过滤。

```json
// 请求
{
  "jsonrpc": "2.0",
  "method": "memhop_recall",
  "params": {
    "agent_id": "cat_1",
    "query": "用户喜欢什么饮料",
    "max_results": 10,
    "target_layers": ["L1", "L2", "L4"],
    "spread_depth": 1,
    "topic_filter": null,
    "exclude_ids": [],
    "exclude_topic_ids": [],
    "l3_domain_id": null,
    "l2_topic_id": null,
    "session_id": null,
    "time_decay_lambda": 0.01,
    "time_range": null
  },
  "id": 1
}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识，默认 `"default"` |
| `query` | string | 否 | 搜索文本，为空返回空结果 |
| `max_results` | int | 否 | 返回条数上限，默认 10 |
| `target_layers` | string[] | 否 | 目标层：`L1`/`L2`/`L3`/`L4`，默认 `[L1, L2, L4]` |
| `spread_depth` | int | 否 | 关联扩散深度，0=不扩散 |
| `topic_filter` | string | 否 | 话题过滤关键词 |
| `exclude_ids` | string[] | 否 | 排除的节点/文档 ID |
| `exclude_topic_ids` | string[] | 否 | 排除的话题 ID |
| `l3_domain_id` | string | 否 | 限定 L3 领域 |
| `l2_topic_id` | string | 否 | 限定 L2 话题 |
| `session_id` | string | 否 | 限定会话 |
| `time_decay_lambda` | float | 否 | 时间衰减系数 |
| `time_range` | [i64, i64] | 否 | 毫秒时间戳范围 `[start, end]` |

```json
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

---

### memhop_health

健康检查。

```json
// 请求
{"jsonrpc":"2.0","method":"memhop_health","params":{"agent_id":"cat_1"},"id":1}
// 响应
{"jsonrpc":"2.0","id":1,"result":{"status":"ok","version":"0.18.0"}}
```

---

## P1 记忆管理接口

### memhop_consolidate / memhop_dream

触发记忆巩固（梦境模拟）。两个方法等价，`consolidate` 是别名。

```json
// 请求
{"jsonrpc":"2.0","method":"memhop_dream","params":{"agent_id":"cat_1"},"id":1}
// 响应
{"jsonrpc":"2.0","id":1,"result":{
  "l1_engrams_consolidated": 5,
  "l2_topics_merged": 1,
  "l2_topics_reflected": 3,
  "duration_us": 12345
}}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识，默认 `"default"` |

---

### memhop_organize

对指定节点执行记忆组织（归类到话题、提取关键词等）。

```json
// 请求
{
  "jsonrpc": "2.0",
  "method": "memhop_organize",
  "params": {"agent_id": "cat_1", "node_id": "kn_1749123456000"},
  "id": 1
}
// 响应
{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |
| `node_id` | string | **是** | L1 节点 ID |

---

### memhop_mount_shelf

挂载本地文件/目录到 L3 领域图，支持自动分块。

```json
// 请求
{
  "jsonrpc": "2.0",
  "method": "memhop_mount_shelf",
  "params": {
    "agent_id": "cat_1",
    "path": "/path/to/docs",
    "name": "技术文档",
    "doc_type": "doc"
  },
  "id": 1
}
// 响应
{"jsonrpc":"2.0","id":1,"result":{
  "domain_id": "domain_技术文档",
  "name": "技术文档",
  "doc_type": "doc",
  "files_scanned": 15,
  "chunks_created": 42
}}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |
| `path` | string | **是** | 文件或目录路径 |
| `name` | string | **是** | 领域名称 |
| `doc_type` | string | 否 | 文档类型：`code`/`doc`/`book`/`paper`/`generic`，默认 `generic` |

---

### memhop_unmount_shelf

卸载已挂载的知识库。

```json
// 请求
{
  "jsonrpc": "2.0",
  "method": "memhop_unmount_shelf",
  "params": {"agent_id": "cat_1", "domain_id": "domain_技术文档"},
  "id": 1
}
// 响应
{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |
| `domain_id` | string | **是** | 领域 ID |

---

### memhop_list_shelf

列出所有已挂载的知识库。

```json
// 请求
{"jsonrpc":"2.0","method":"memhop_list_shelf","params":{"agent_id":"cat_1"},"id":1}
// 响应
{"jsonrpc":"2.0","id":1,"result":[
  {"domain_id":"domain_技术文档","name":"技术文档","doc_type":"doc","node_count":42}
]}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |

---

## P1 角色画像接口

### memhop_get_profile

获取 L0 角色画像。

```json
// 请求
{"jsonrpc":"2.0","method":"memhop_get_profile","params":{"agent_id":"cat_1"},"id":1}
// 响应
{"jsonrpc":"2.0","id":1,"result":{
  "role_name": "小猫",
  "personality": ["温柔", "活泼"],
  "values": ["真诚"],
  "worldview": ["世界是美好的"],
  "traits": {"说话风格": "可爱"}
}}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |

---

### memhop_set_profile

设置 L0 角色画像（简版，兼容旧接口）。

```json
// 请求
{
  "jsonrpc": "2.0",
  "method": "memhop_set_profile",
  "params": {
    "agent_id": "cat_1",
    "role_name": "小猫",
    "role": "AI助手",
    "position": "陪伴型",
    "traits": {"说话风格": "可爱", "语气": "温柔"}
  },
  "id": 1
}
// 响应
{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |
| `role_name` | string | 否 | 角色名 |
| `role` | string | 否 | 角色类型 |
| `position` | string | 否 | 定位 |
| `traits` | object | 否 | 特征键值对 |

---

### memhop_set_l0

设置 L0 角色画像（完整版）。

```json
// 请求
{
  "jsonrpc": "2.0",
  "method": "memhop_set_l0",
  "params": {
    "agent_id": "cat_1",
    "role_name": "小猫",
    "personality": ["温柔", "活泼", "好奇"],
    "values": ["真诚", "善良"],
    "worldview": ["世界是美好的", "知识改变命运"],
    "traits": {"说话风格": "可爱", "语气": "温柔"}
  },
  "id": 1
}
// 响应
{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |
| `role_name` | string | 否 | 角色名 |
| `personality` | string[] | 否 | 性格特征列表 |
| `values` | string[] | 否 | 价值观列表 |
| `worldview` | string[] | 否 | 世界观列表 |
| `traits` | object | 否 | 其他特征键值对 |

---

## P2 会话管理接口

### memhop_activate

激活指定话题（提升检索权重）。

```json
// 请求
{
  "jsonrpc": "2.0",
  "method": "memhop_activate",
  "params": {
    "agent_id": "cat_1",
    "session_id": "session_1",
    "topic_id": "topic_xxx",
    "ttl_ms": 3600000
  },
  "id": 1
}
// 响应
{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |
| `session_id` | string | 否 | 会话 ID，默认 `"default"` |
| `topic_id` | string | **是** | 话题 ID |
| `ttl_ms` | int | 否 | 激活有效期（毫秒），默认 3600000（1小时） |

---

### memhop_deactivate

去激活指定话题。

```json
// 请求
{
  "jsonrpc": "2.0",
  "method": "memhop_deactivate",
  "params": {"agent_id": "cat_1", "session_id": "session_1", "topic_id": "topic_xxx"},
  "id": 1
}
// 响应
{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |
| `session_id` | string | 否 | 会话 ID，默认 `"default"` |
| `topic_id` | string | **是** | 话题 ID |

---

### memhop_get_activated

获取当前激活的话题列表。

```json
// 请求
{"jsonrpc":"2.0","method":"memhop_get_activated","params":{"agent_id":"cat_1"},"id":1}
// 响应
{"jsonrpc":"2.0","id":1,"result":[
  {"topic_id":"topic_xxx","session_id":"session_1","weight":1.0,"expires_at":1749127056000}
]}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |

---

### memhop_feedback

对检索结果反馈，调整激活话题权重。

```json
// 请求
{
  "jsonrpc": "2.0",
  "method": "memhop_feedback",
  "params": {
    "agent_id": "cat_1",
    "result_ids": ["kn_xxx", "topic_yyy"],
    "relevant": true,
    "session_id": "session_1"
  },
  "id": 1
}
// 响应
{"jsonrpc":"2.0","id":1,"result":{"adjusted":2,"relevant":true}}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |
| `result_ids` | string[] | 否 | 反馈的结果 ID 列表 |
| `relevant` | bool | 否 | `true`=正反馈(+0.1)，`false`=负反馈(-0.1)，默认 `true` |
| `session_id` | string | 否 | 会话 ID，默认 `"default"` |

---

## P2 查询浏览接口

### memhop_get_l4_raw

获取 L4 原始文档全文。

```json
// 请求
{
  "jsonrpc": "2.0",
  "method": "memhop_get_l4_raw",
  "params": {"agent_id": "cat_1", "doc_id": "l4d_1749123456000"},
  "id": 1
}
// 响应
{"jsonrpc":"2.0","id":1,"result":{
  "id":"l4d_1749123456000",
  "text":"user: 她喜欢喝可乐 | assistant: 了解",
  "source":"chat",
  "turn_id":"session_1_T5",
  "session_id":"session_1",
  "created_at":1749123456000
}}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |
| `doc_id` | string | **是** | L4 文档 ID |

---

### memhop_list_l3_paths

列出所有 L3 领域路径。

```json
// 请求
{"jsonrpc":"2.0","method":"memhop_list_l3_paths","params":{"agent_id":"cat_1"},"id":1}
// 响应
{"jsonrpc":"2.0","id":1,"result":[
  {"domain_id":"domain_技术文档","path":"/path/to/docs","name":"技术文档"}
]}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |

---

### memhop_list_topics

列出所有 L2 话题。

```json
// 请求
{"jsonrpc":"2.0","method":"memhop_list_topics","params":{"agent_id":"cat_1"},"id":1}
// 响应
{"jsonrpc":"2.0","id":1,"result":[
  {"id":"topic_xxx","label":"饮品偏好","summary":"用户告知她喜欢可乐",
   "keywords":["可乐","偏好"],"node_count":3,"created_at":1749123456000}
]}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |

---

### memhop_re_search

正则搜索记忆（使用正则表达式匹配文本）。

```json
// 请求
{
  "jsonrpc": "2.0",
  "method": "memhop_re_search",
  "params": {
    "agent_id": "cat_1",
    "query": "喜欢.*可乐",
    "max_results": 10,
    "target_layers": ["L1", "L2", "L4"]
  },
  "id": 1
}
```

参数与 `memhop_recall` 相同（不含 `time_range`）。

---

### memhop_update_topic

更新话题元数据（summary、keywords、扩展字段）。

```json
// 请求
{
  "jsonrpc": "2.0",
  "method": "memhop_update_topic",
  "params": {
    "agent_id": "cat_1",
    "topic_id": "topic_xxx",
    "summary": "用户喜欢可乐等碳酸饮料",
    "keywords": ["可乐", "碳酸饮料", "偏好"],
    "extended_meta": {"source": "llm_refined"}
  },
  "id": 1
}
// 响应
{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |
| `topic_id` | string | **是** | 话题 ID |
| `summary` | string | 否 | 更新摘要 |
| `keywords` | string[] | 否 | 更新关键词 |
| `extended_meta` | object | 否 | 扩展元数据键值对 |

---

### memhop_stats

获取引擎统计信息。

```json
// 请求
{"jsonrpc":"2.0","method":"memhop_stats","params":{"agent_id":"cat_1"},"id":1}
// 响应
{"jsonrpc":"2.0","id":1,"result":{
  "version":"0.18.0",
  "encoder_mode":"candle",
  "encoder_dim":768,
  "brain_stats":{
    "l1_nodes":150,
    "l2_topics":12,
    "l3_nodes":42,
    "l4_docs":200
  },
  "total_engrams":404
}}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 否 | Agent 标识 |
