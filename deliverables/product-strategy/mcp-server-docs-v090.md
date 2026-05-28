# MemHop MCP Server v0.9.0 — 工具文档

## 概述

MemHop MCP Server 是一个 **MCP (Model Context Protocol)** 服务器，提供脑启发的长期记忆引擎。任何支持 MCP 的客户端（MeowAgent、Claude Desktop、Cursor 等）都可以接入。

- 协议：MCP JSON-RPC 2.0（stdin/stdout）
- 版本：v0.9.0
- 端口：无网络端口，走进程标准输入输出

---

## 快速启动

```bash
# 设置数据库路径
export MEMHOP_DB_PATH=/path/to/brain.db

# 启动 MCP Server
cargo run --release -p memhop-mcp-server
```

---

## 配置

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `MEMHOP_DB_PATH` | `/tmp/memhop-mcp.db` | Brain 数据库路径（LMDB 单文件） |

可选模型文件（需用户自行下载，放到 `models/` 目录）：

| 路径 | 用途 | 下载 |
|------|------|------|
| `models/bge-m3/model.onnx` | BGE-M3 语义编码 | HuggingFace BAAI/bge-m3 ONNX |
| `models/bge-reranker-v2-m3/` | Cross-Encoder 精排 | HuggingFace BAAI/bge-reranker-v2-m3 ONNX |

不带模型文件也能运行 —— 自动降级到 NgramEncoder（纯 Rust，基于 trigram 哈希）。

---

## MCP Tools 完整清单

所有工具通过 MCP `tools/call` 调用，使用 JSON-RPC 2.0 格式。

---

### 1. memhop_store

存储一条新记忆。

**Input**:

```json
{
  "text": "用户说今天心情很好",
  "session_id": "chat-123",        // 可选，默认 "default"
  "valence": 0.8,                  // 可选，情绪价 [-1, 1]，默认 0
  "arousal": 0.3                   // 可选，情绪唤醒度 [0, 1]，默认 0.5
}
```

**Output**:

```json
{
  "memory_id": "mem_abc123",
  "plan_id": "plan_def456",
  "plan_hint": "Continue",
  "plan_name": "general_chat"
}
```

**说明**：
- 文本存储前自动经过 `privacy_filter`，剥离 API Key、secret 等敏感信息
- 自动分配 plan（PlanGate 边界检测），支持 Plan 级记忆管理

---

### 2. memhop_recall

召回与查询最相关的记忆。

**Input**:

```json
{
  "query": "关于心情的记忆",
  "session_id": "chat-123",           // 可选
  "limit": 5,                         // 可选，最大返回条数，默认 5
  "max_tokens": 200,                  // 可选，每条结果文本截断
  "query_vector": [0.1, 0.2, ...]     // 可选，预编码向量（跳过服务端编码）
}
```

**Output**:

```json
{
  "results": [
    {
      "id": "mem_abc123",
      "text": "用户说今天心情很好",
      "kind": "Episode",
      "source": "working_memory"
    }
  ],
  "schemas": [],
  "trace": {
    "latency_us": 3250,
    "hopfield_candidates": 80,
    "spread_steps": 0
  }
}
```

**说明**：
- 默认使用 Retrieval 模式：HNSW + RRF 融合（余弦相似度 + ngram），跳过情绪/关键词主导排序
- 同 session 最多返回 3 条（会话多样化）
- 支持传入 `query_vector` 跳过服务端编码（外部编码器 benchmark 用）

---

### 3. memhop_reflect

创建一条反思/元认知记忆。

**Input**:

```json
{
  "content": "用户似乎对编程很感兴趣",
  "kind": "pattern",          // pattern | evaluation | intention | confusion
  "session_id": "chat-123"    // 可选
}
```

**Output**:

```json
{
  "reflection_id": "mem_def789"
}
```

---

### 4. memhop_dream

手动触发 Dream 记忆整合周期。

**Input**: `{}`

**Output**:

```json
{
  "status": "ok",
  "consolidated_count": 5,
  "pruned_edges": 12,
  "duration_ms": 350
}
```

**说明**：
- Dream 自动在每 50 次 perceive 后触发，此工具可手动触发
- Dream 周期包含：Hippocampus→Neocortex 转移、模式合并、低活力衰减、Schema 涌现

---

### 5. memhop_stats

获取 Brain 统计信息。

**Input**: `{}`

**Output**:

```json
{
  "total_memories": 1520,
  "cortex_len": 7,
  "hippocampus_len": 128,
  "total_perceptions": 1500,
  "total_reflections": 20,
  "total_engrams_created": 1500,
  "total_consolidated": 1350,
  "dream_cycles": 30,
  "total_schemas_emerged": 5,
  "total_contradictions": 2,
  "version": "0.9.0"
}
```

---

### 6. memhop_count

获取记忆总数。

**Input**: `{}`

**Output**: `{ "count": 1520 }`

---

### 7. memhop_health

健康检查。

**Input**: `{}`

**Output**:

```json
{
  "status": "ok",
  "version": "0.9.0",
  "uptime_secs": 3600,
  "total_memories": 1520,
  "cortex_len": 7,
  "hippocampus_len": 128,
  "total_engrams_created": 1500,
  "total_consolidated": 1350,
  "dream_cycles": 30
}
```

---

### 8. memhop_complete_plan

标记一个 Plan 为完成。

**Input**:

```json
{ "plan_id": "plan_def456" }
```

**Output**: `{ "status": "completed" }`

---

### 9. memhop_get_plan_tree

获取 Plan 层级树。

**Input**:

```json
{
  "plan_id": "plan_def456"    // 可选，不传返回所有根 Plan
}
```

**Output**:

```json
{
  "tree": [
    {
      "id": "plan_def456",
      "parent_id": null,
      "name": "general_chat",
      "level": "Ultra",
      "state": "Active",
      "dialogue_count": 12,
      "compressed_summary": "Chat about programming",
      "created_at": 1716800000000,
      "completed_at": null
    }
  ]
}
```

---

### 10. memhop_get_chat_history

获取某个 Plan 的对话历史。

**Input**:

```json
{ "plan_id": "plan_def456" }
```

**Output**:

```json
{
  "turns": [
    {
      "id": "turn_001",
      "plan_id": "plan_def456",
      "user_input": "今天心情很好",
      "agent_response": "那很好啊！",
      "user_tone": { "valence": 0.8, "arousal": 0.3, "tags": ["positive"] },
      "timestamp": 1716800000000
    }
  ],
  "total": 12
}
```

---

### 11. memhop_plan_stats

获取 Plan 统计（主题分布 + 情绪趋势）。

**Input**:

```json
{
  "start_time": 1716800000000,   // 可选，Unix ms
  "end_time": 1716886400000      // 可选，Unix ms
}
```

**Output**:

```json
{
  "plan_count": 5,
  "domain_distribution": [
    { "domain": "general_chat", "plan_count": 3, "dialogue_count": 20, "avg_valence": 0.6 }
  ],
  "tone_trend": {
    "avg_valence": 0.5,
    "avg_arousal": 0.4,
    "valence_trend": [0.5, 0.6, 0.4],
    "top_tone_tags": ["positive", "neutral"]
  }
}
```

---

### 12. memhop_mount_shelf

挂载一个外部知识源（文件/目录）作为知识架。

**Input**:

```json
{
  "path": "/path/to/rust-book/",
  "domain": "book"        // 可选：code | doc | book | paper，默认 doc
}
```

**Output**:

```json
{
  "shelf_id": "shelf_1234567890"
}
```

**说明**：
- 扫描路径下所有常见文本文件（.rs .py .js .ts .go .md .txt .toml .json .yaml）
- 按 domain 切片：book→段落、doc→heading、code→文件级
- 用 Brain 的编码器编码后建立 HNSW 索引
- 返回 shelf_id 供后续搜索

---

### 13. memhop_knowledge_search

在已挂载的知识架中搜索。

**Input**:

```json
{
  "query": "Rust ownership",
  "shelf_id": "shelf_1234567890",
  "limit": 5,             // 可选，默认 5
  "max_tokens": 200       // 可选，截断结果文本
}
```

**Output**:

```json
{
  "status": "ok",
  "results": [
    {
      "text": "Ownership is Rust's most unique feature...",
      "location": "paragraph_42",
      "score": 0.92,
      "source": "/path/to/rust-book/ch04.md"
    }
  ]
}
```

---

### 14. memhop_unmount_shelf

卸载一个知识架。

**Input**:

```json
{ "shelf_id": "shelf_1234567890" }
```

**Output**: `{ "status": "ok" }`

---

## MeowAgent 接入配置

MeowAgent 作为 MCP 客户端连接 MemHop 的配置：

### 方式一：subprocess（推荐）

```json
{
  "mcpServers": {
    "memhop": {
      "command": "cargo",
      "args": ["run", "--release", "-p", "memhop-mcp-server"],
      "env": {
        "MEMHOP_DB_PATH": "/path/to/meowagent/memories/brain.db"
      }
    }
  }
}
```

### 方式二：pre-built binary

```json
{
  "mcpServers": {
    "memhop": {
      "command": "/path/to/memhop-mcp-server",
      "args": [],
      "env": {
        "MEMHOP_DB_PATH": "/path/to/meowagent/memories/brain.db"
      }
    }
  }
}
```

### 建议的多数据库路径策略

MeowAgent 可以为不同用途创建独立的 Brain 数据库：

| 用途 | 建议路径 |
|------|---------|
| 猫 A 的记忆 | `/data/meowagent/cat-a/memories/` |
| 猫 B 的记忆 | `/data/meowagent/cat-b/memories/` |
| 共享书架 | 通过 `memhop_mount_shelf` 运行时挂载 |

---

## 协议细节

### JSON-RPC 2.0 格式

请求：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "memhop_store",
    "arguments": { "text": "hello world" }
  }
}
```

响应：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "memory_id": "mem_abc123",
    "plan_id": "plan_def456",
    "plan_hint": "Continue",
    "plan_name": "general_chat"
  }
}
```

### 初始化

```
→ {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}
← {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"memhop-mcp-server","version":"0.9.0"},"capabilities":{"tools":{}}}}
→ {"jsonrpc":"2.0","method":"notifications/initialized"}
```

---

## 架构说明

```
MeowAgent ──MCP──→ memhop-mcp-server (单进程)
                      │
                      ├── BGE-M3 ONNX (可选, ~2GB)
                      ├── Brain
                      │     ├── Cortex (L0, 工作记忆, 7条)
                      │     ├── Hippocampus (L1, 短期缓冲, 500条)
                      │     └── Neocortex (L2, 长期存储)
                      │            ├── HNSW Index (O(log N) 检索)
                      │            ├── Hopfield Network (联想召回)
                      │            ├── SparseIndex (ngram 索引)
                      │            └── EntangleGraph (关联图)
                      ├── Shelf Manager
                      │     └── 多个 HNSW-only 知识架
                      └── LMDB (单文件 brain.db)
```

### 双召回模式

| 模式 | 管线 | 适用场景 |
|------|------|---------|
| Retrieval（默认） | HNSW → RRF → cosine 排序 | 事实检索、质量基准 |
| Associative | HNSW → Hopfield spread → 情绪/ngram boost | 联想、创意、类比 |

### 生命周期

```
perceive → 记忆管线（Hopfield + HNSW + Graph）
dream    → 整合周期（每 50 次 perceive 自动触发）
mount_shelf → 知识管线（仅 HNSW，手动 unmount）
```
