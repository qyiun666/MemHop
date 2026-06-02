# MemHop v0.13 — Agent 接入指南

MemHop 是一个嵌入式联想记忆引擎内核，通过 MCP JSON-RPC 2.0 over stdio 协议对外暴露。Agent 通过 stdio 子进程方式调用。

> 本文件是 meowAgent 对接 memhop 的唯一参考文档。
> 所有工具签名、参数、响应格式均对照代码生成，无需读代码。

---

## 一、架构与概念

```
┌─ memhop-mcp-server 进程（整机唯一）────────────────────┐
│  运行: memhop-mcp-server --socket-path=~/.memhop/memhop.sock
│  协议: JSON-RPC 2.0 over Unix Domain Socket            │
│  多客户端并发连接，共享 ONNX 模型 (~500MB)             │
│                                                         │
│  MEMHOP_BRAINS_DIR/~/.memhop/brains/{agent_id}/         │
│  ├── "zt_mac"   →  ~/.memhop/brains/zt_mac/memhop.db   │
│  ├── "zt_phone" →  ~/.memhop/brains/zt_phone/memhop.db │
│  └── "desk"     →  ~/.memhop/brains/desk/memhop.db     │
│  多 agent 隔离，每个 agent 有独立 HNSW/Engram/LMDB      │
└─────────────────────────────────────────────────────────┘
          ▲ Unix Domain Socket (JSON-RPC 2.0)
          │
┌──────────┴───────────┐
│  Agent 进程 × N      │
│  通过 socket 连接     │
│  传 agent_id 标识自己  │
│  共享同一 memhop      │
│  ONNX 模型只加载一次   │
└──────────────────────┘
```

### 核心设计原则

| 原则 | 说明 |
|------|------|
| **APPEND-ONLY** | 记忆创建后不可修改。所有"更新"是创建新记录 |
| **无状态** | 每个 MCP 调用是独立的。Brain 实例在进程内缓存，30分钟空闲过期 |
| **Immutable Engram** | engram 的 text/vector/kind 等核心字段写入后不变 |
| **Agent 隔离** | 不同 agent_id 使用完全独立的 LMDB 数据库 |

### 质量目标

| 指标 | 目标 | 设计对策 |
|------|------|---------|
| 上下文不爆炸 | 活跃 ≤5, 休眠 ≤1000 | 三阶管理：活跃→休眠→归档 |
| 失憶率 | <5% | `context_id` 范围过滤 + Cross-Encoder reranker |
| 幻听率 | <5% | Worldview 冲突过滤 + `use_worldview_filter` |

---

## 二、启动

```bash
memhop-mcp-server --socket-path=/tmp/memhop.sock
```

### 环境变量

| 变量 | 用途 | 默认 |
|------|------|------|
| `MEMHOP_BRAINS_DIR` | 多 agent 记忆文件根目录 | `~/.memhop/brains/` |
| `MEMHOP_ONNX_MODEL` | BGE-M3 编码器 ONNX 模型目录（必须） | 无，启动失败 |
| `MEMHOP_RERANKER_MODEL` | Cross-Encoder 重排序模型路径 | 无，不启用 |

### agent_id 规则

**所有 MCP 工具必须传 `agent_id` 参数**。校验规则：`[a-zA-Z0-9_-]{1,64}`。

```json
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"memhop_store","arguments":{
    "text":"帮我写一个脚本", "agent_id":"zt_mac"}}}
```

---

## 三、每轮对话流程

```
Agent 进程内部:
  loop {
    user_input = 等待用户输入

    // 1. 从 memhop 检索相关上下文
    response = memhop_recall(query=user_input, agent_id="zt_mac",
                              session_id="sess_001", context_id="ctx_xxx")
    // ← 返回: results[], worldview_context[], cognitive_conflicts[]

    // 2. 注入上下文到 LLM prompt
    llm_prompt = response.worldview_context
                + response.results[].text
                + agent.cortex.最近几轮()

    // 3. LLM 生成回复
    ai_response = llm.generate(llm_prompt)

    // 4. 存入记忆
    store_resp = memhop_store(text=user_input,
                              agent_response=ai_response,
                              agent_id="zt_mac", session_id="sess_001")
    // ← 返回: engram_id, context_id, phase, plan_id

    // 5. Agent 更新工作记忆
    agent.cortex.push(user_input, ai_response)

    // 6. Agent 自主决定何时巩固
    if agent.should_dream():
        dream_resp = memhop_dream(agent_id="zt_mac")
  }
```

---

## 四、MCP 工具清单（完整）

> 共 **30 个工具**（含 1 个 deprecated + 2 个 deprecated alias）。

### 4.1 核心记忆操作

| 工具 | 用途 | 版本 |
|------|------|------|
| `memhop_store` | 存入一轮对话或知识块 | v0.1 |
| `memhop_recall` | 检索相关记忆 | v0.1 |
| `memhop_reflect` | 创建反思/内省记忆 | v0.9 |
| `memhop_dream` | 触发记忆巩固周期 | v0.5 |
| `memhop_forget` | 删除指定轮次的记忆 | v0.8 |

#### memhop_store

请求：
```json
{
  "name": "memhop_store",
  "arguments": {
    "agent_id": "zt_mac",
    "text": "帮我写一个 Python 脚本处理 CSV",
    "agent_response": "好的，我来写一个 CSV 处理脚本...",
    "session_id": "sess_001",
    "kind": "episode",
    "valence": 0.3,
    "arousal": 0.5,
    "turn_id": "turn_001",
    "turn_index": 0,
    "topic_label": "CSV脚本",
    "tree_id": "tree_xxx",
    "vector": [0.1, 0.2, ...],
    "auto_create_tree": true,
    "auto_compress": true,
    "match_threshold": 0.75,
    "context_half_life": 12.0,
    "llm_compressed_summary": "用户需要CSV处理脚本的讨论摘要",
    "llm_keywords": ["python", "csv", "脚本"]
  }
}
```

**参数说明**：

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `text` | string | 是 | 记忆内容 |
| `agent_response` | string | 否 | AI 回复，用于创建 DialogueTurn |
| `session_id` | string | 否 | 会话 ID，默认 "default" |
| `kind` | string | 否 | `"episode"`（默认）或 `"knowledge"` |
| `valence` | number | 否 | 情感价 -1.0~1.0 |
| `arousal` | number | 否 | 唤醒度 0.0~1.0 |
| `turn_id` | string | 否 | 对话轮次 ID |
| `turn_index` | integer | 否 | 轮次序号 |
| `topic_label` | string | 否 | 话题标签 |
| `tree_id` | string | 否 | 关联的知识树 ID（自动创建时获取） |
| `vector` | number[] | 否 | 外部编码器预计算向量（如 sentence-transformers），会自动 pad 到 1024 维 |
| `auto_create_tree` | boolean | 否 | 自动创建知识树（默认 true） |
| `auto_compress` | boolean | 否 | 自动压缩上下文（默认 true） |
| `match_threshold` | number | 否 | 上下文匹配余弦阈值（默认 0.75） |
| `context_half_life` | number | 否 | 上下文时间衰减半衰期（小时，默认 12.0） |
| `llm_compressed_summary` | string | 否 | LLM 生成的压缩摘要 |
| `llm_keywords` | string[] | 否 | LLM 提取的关键词 |

kind=knowledge 时额外必填：
- `tree_path`: 知识树挂载路径
- `source_path`: 源文件路径
- `source_textunit`: 源文本单元（如 "§3.2"）

响应（kind=episode）：
```json
{
  "status": "stored",
  "engram_id": "eng_xxx",
  "plan_id": "plan_xxx",
  "plan_hint": "Continue",
  "plan_name": "Python CSV处理",
  "context_id": "ctx_1717200000",
  "context_summary": null,
  "phase": "full"
}
```

响应（kind=knowledge）：
```json
{
  "status": "Stored",
  "engram_id": "eng_xxx",
  "duplicate_of": null
}
```

store 后自动触发：
- 上下文匹配（活跃集 → 休眠池 → 新建）
- 上下文 turn_count 递增
- 如果 turn_count >= 5 + 无关联树 → 自动建树
- LangGraph 关联更新
- organize（提取实体 → 链接图 → 检测话题边界）

#### memhop_recall

请求：
```json
{
  "name": "memhop_recall",
  "arguments": {
    "agent_id": "zt_mac",
    "query": "CSV 脚本",
    "session_id": "sess_001",
    "limit": 10,
    "mode": "retrieval",
    "use_reranker": true,
    "kind_filter": ["episode"],
    "tree": "/path/to/knowledge",
    "query_vector": [0.1, 0.2, ...],
    "context_id": "ctx_1717200000",
    "use_worldview_filter": true,
    "llm_conflict_check": "{\"type\": \"consistent\", \"description\": \"...\"}"
  }
}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `query` | string | 是 | 检索查询 |
| `session_id` | string | 否 | 获取该会话的 cortex（工作记忆） |
| `limit` | integer | 否 | 返回结果上限，默认 5 |
| `mode` | string | 否 | `"retrieval"`（HNSW+CrossEncoder，默认）或 `"associative"`（Hopfield+spread） |
| `use_reranker` | boolean | 否 | 启用 CrossEncoder 重排序（retrieval 模式默认 true） |
| `kind_filter` | string[] | 否 | 类型过滤：`"episode"`、`"knowledge"`，空=全部 |
| `tree` | string | 否 | 按知识树路径过滤 |
| `query_vector` | number[] | 否 | 外部编码器预计算查询向量 |
| `context_id` | string | 否 | 限定到指定上下文中检索 |
| `use_worldview_filter` | boolean | 否 | 过滤与世界观冲突的结果（默认 true） |
| `llm_conflict_check` | string | 否 | LLM 提供的冲突检测结果 JSON 字符串 |

响应（v0.13.2 分层格式 + 可信度分数）：
```json
{
  "working_memory": [
    {
      "id": "eng_001",
      "text": "CSV 处理脚本的讨论...",
      "kind": "episode",
      "tree_path": null,
      "score": 0.95
    }
  ],
  "associations": [
    {
      "id": "eng_003",
      "text": "CSV 解析性能问题...",
      "kind": "episode",
      "tree_path": null,
      "score": 0.82
    }
  ],
  "knowledge_memories": [
    {
      "id": "eng_002",
      "text": "pandas read_csv 用法...",
      "tree_path": "/docs/python",
      "source_path": "/docs/python/pandas.md",
      "source_textunit": "§3.2",
      "score": 0.67
    }
  ],
  "schemas": [
    {"id": "sch_001", "text": "用户偏好先抽象后具体"}
  ],
  "emotional_echoes": [],
  "hit_turns": [
    {
      "engram_id": "eng_001",
      "turn_id": "turn_001",
      "session_id": "sess_001",
      "score": 0.85,
      "snippet": "CSV 处理脚本的讨论..."
    }
  ],
  "aggregated_sessions": [
    {
      "session_id": "sess_001",
      "total_score": 1.7,
      "top_turn_ids": ["turn_001", "turn_002"]
    }
  ],
  "tree_contexts": [
    {
      "tree_path": "/docs/python",
      "domain": "doc",
      "source_count": 3
    }
  ],
  "graph_associations": [
    {
      "source_id": "eng_001",
      "target_id": "eng_002",
      "kind": "semantic",
      "weight": 0.6,
      "description": "CoShelf: same knowledge tree"
    }
  ],
  "worldview_context": ["[工作方式] 倾向于先抽象再具体 (稳定度: 0.8)"],
  "cognitive_conflicts": [],
  "trace": {
    "latency_us": 1234,
    "hopfield_candidates": 80,
    "spread_steps": 3
  }
}
```

**响应说明**：
- 结果按记忆层级分层返回：`working_memory`(L0)、`associations`(L1)、`knowledge_memories`(L2)
- 每条结果附带 `score` 可信度分数（0~1），可直接用于 Prompt 标注 `[高/中/低可信度]`
- 不再有扁平 `results` 数组和硬编码的 `contexts_summary`/`recall_quality`

**recall 质量机制**：
- `context_id` 指定时 → 只返回该上下文内的记忆（降失憶率）
- `use_reranker=true` → Cross-Encoder 重排序（降幻听率）
- `use_worldview_filter=true` → 过滤与世界观冲突的结果（降幻听率）
- 无 `context_id` → 全局搜索

#### memhop_reflect

请求：
```json
{
  "name": "memhop_reflect",
  "arguments": {
    "agent_id": "zt_mac",
    "content": "用户倾向于在讨论技术问题时先要求抽象方案再深入细节",
    "kind": "pattern",
    "session_id": "sess_001"
  }
}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `content` | string | 是 | 反思内容 |
| `kind` | string | 是 | 类型：`"pattern"`、`"evaluation"`、`"intention"`、`"confusion"` |
| `session_id` | string | 否 | 会话 ID，默认 "default" |

响应：
```json
{"reflection_id": "refl_001"}
```

#### memhop_dream

请求：
```json
{
  "name": "memhop_dream",
  "arguments": {
    "agent_id": "zt_mac",
    "context_compress": true,
    "llm_patterns": [{"pattern": "...", "category": "ThinkingStyle"}],
    "llm_contradictions": [{"type": "contradiction", "description": "..."}]
  }
}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `context_compress` | boolean | 否 | 压缩所有待处理上下文（默认 true） |
| `llm_patterns` | object[] | 否 | LLM 发现的模式 |
| `llm_contradictions` | object[] | 否 | LLM 发现的矛盾 |

响应：
```json
{
  "status": "ok",
  "consolidated_count": 12,
  "pruned_edges": 3,
  "duration_ms": 42,
  "knowledge_processed": 4,
  "cross_kind_new_associations": 2,
  "hnsw_compacted": 0,
  "contexts_compressed": 2,
  "dormant_moved": 1,
  "archived": 3
}
```

#### memhop_forget

请求：
```json
{
  "name": "memhop_forget",
  "arguments": {
    "agent_id": "zt_mac",
    "turn_id": "turn_001"
  }
}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `turn_id` | string | 是 | 要删除的对话轮次 ID |

响应：`{"status": "ok"}`

---

### 4.2 计划管理

| 工具 | 用途 | 版本 |
|------|------|------|
| `memhop_complete_plan` | 完成一个计划 | v0.8 |
| `memhop_get_plan_tree` | 获取计划树 | v0.8 |
| `memhop_get_chat_history` | 获取归档对话轮次 | v0.8 |
| `memhop_plan_stats` | 获取计划统计数据 | v0.8 |

#### memhop_complete_plan

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `plan_id` | string | 是 | 计划 ID |

响应：`{"status": "completed"}`

#### memhop_get_plan_tree

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `plan_id` | string | 否 | 指定根计划（不传返回所有根计划） |

响应：
```json
{
  "tree": [
    {
      "id": "plan_001",
      "parent_id": null,
      "name": "Python CSV处理",
      "level": "Root",
      "state": "Active",
      "dialogue_count": 5,
      "compressed_summary": "用户需要CSV处理脚本",
      "created_at": 1717200000000,
      "completed_at": null
    }
  ]
}
```

#### memhop_get_chat_history

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `plan_id` | string | 是 | 计划 ID |

响应：
```json
{
  "turns": [
    {
      "id": "turn_001",
      "plan_id": "plan_001",
      "user_input": "帮我写一个 Python 脚本处理 CSV",
      "agent_response": "好的，我来写一个 CSV 处理脚本...",
      "user_tone": {"valence": 0.3, "arousal": 0.5, "tags": []},
      "timestamp": 1717200000000
    }
  ],
  "total": 1
}
```

#### memhop_plan_stats

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `start_time` | integer | 否 | 起始时间戳 |
| `end_time` | integer | 否 | 结束时间戳 |

响应：
```json
{
  "plan_count": 3,
  "domain_distribution": [
    {"domain": "编程", "plan_count": 2, "dialogue_count": 10, "avg_valence": 0.4}
  ],
  "tone_trend": {
    "avg_valence": 0.3,
    "avg_arousal": 0.5,
    "valence_trend": [0.2, 0.4, 0.3],
    "top_tone_tags": ["curious", "satisfied"]
  }
}
```

---

### 4.3 知识树管理

memhop 有两套不同的知识树概念：

| 系统 | 创建方式 | 用途 | 数据来源 |
|------|---------|------|---------|
| **ShelfTree**（书架树） | `memhop_mount_tree` | 挂载外部知识源（文件/目录） | 外部文件系统 |
| **LogicalTree**（逻辑树） | `memhop_create_tree` | 组织记忆的逻辑分类 | memhop 内部 |

| 工具 | 用途 | 系统 | 版本 |
|------|------|------|------|
| `memhop_mount_tree` | 挂载知识源（文件/目录） | ShelfTree | v0.11 |
| `memhop_unmount_tree` | 卸载知识源 | ShelfTree | v0.11 |
| `memhop_tree_status` | 查看已挂载的知识源 | ShelfTree | v0.11 |
| `memhop_create_tree` | 创建逻辑知识树 | LogicalTree | v0.12 |
| `memhop_list_trees` | 列出所有逻辑树 | LogicalTree | v0.12 |
| `memhop_get_tree` | 获取单棵逻辑树详情 | LogicalTree | v0.12 |
| `memhop_move_to_tree` | 将 engram 移动到指定树 | LogicalTree | v0.12 |
| `memhop_delete_tree` | 删除逻辑树（不解绑 engram） | LogicalTree | v0.12 |

#### memhop_mount_tree

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `path` | string | 是 | 文件/目录路径（作为唯一标识） |
| `domain` | string | 否 | `"code"`、`"book"`、`"paper"`、`"doc"`、`"generic"`（默认） |

响应：
```json
{
  "tree_path": "/path/to/knowledge",
  "chunk_count": 42,
  "domain": "code",
  "warnings": []
}
```

#### memhop_unmount_tree

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `tree_path` | string | 是 | 要卸载的路径 |

响应：
```json
{
  "tree_path": "/path/to/knowledge",
  "deleted_count": 15
}
```

#### memhop_tree_status

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `tree_path` | string | 否 | 指定路径则返回单个树，不传返回全部 |

响应（单个）：
```json
{
  "tree_path": "/path/to/knowledge",
  "domain": "Code",
  "chunk_count": 42,
  "file_count": 3,
  "mounted_at": 1717200000
}
```

响应（全部）：
```json
{
  "trees": [
    {"tree_path": "/path/a", "domain": "Code", "chunk_count": 42, "file_count": 3}
  ],
  "count": 1
}
```

#### memhop_create_tree

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `name` | string | 是 | 树名称 |
| `domain` | string | 否 | `"work"`、`"travel"`、`"parenting"`、`"generic"` 等，默认 `"generic"` |

响应：
```json
{
  "tree_id": "tree_001",
  "name": "Python学习",
  "domain": "work"
}
```

#### memhop_list_trees

响应：`[{"id": "tree_001", "name": "Python学习", "domain": "work", ...}]`

#### memhop_get_tree

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `tree_id` | string | 是 | 树 ID |

响应：`{"id": "tree_001", "name": "Python学习", "domain": "work", ...}`

#### memhop_move_to_tree

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `engram_id` | string | 是 | 要移动的 engram ID |
| `tree_id` | string | 是 | 目标树 ID |

响应：`{"status": "ok"}`

#### memhop_delete_tree

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `tree_id` | string | 是 | 要删除的树 ID |

响应：`{"status": "ok"}`

---

### 4.4 诊断工具

| 工具 | 用途 | 版本 |
|------|------|------|
| `memhop_stats` | 获取大脑统计数据 | v0.5 |
| `memhop_count` | 获取总 engram 数量 | v0.9 |
| `memhop_health` | 获取健康检查指标 | v0.9 |
| `memhop_list_schemas` | 列出所有 Schema 模式 | v0.9 |

#### memhop_stats

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |

响应：
```json
{
  "total_memories": 1234,
  "cortex_len": 7,
  "hippocampus_len": 500,
  "total_perceptions": 1000,
  "total_reflections": 50,
  "total_engrams_created": 1050,
  "total_consolidated": 200,
  "dream_cycles": 15,
  "total_schemas_emerged": 8,
  "total_contradictions": 3,
  "version": "0.13.1"
}
```

#### memhop_count

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |

响应：`{"count": 1234}`

#### memhop_health

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |

响应：
```json
{
  "status": "ok",
  "version": "0.13.1",
  "uptime_secs": 3600,
  "active_brains": 2,
  "total_memories": 1234,
  "cortex_len": 7,
  "hippocampus_len": 500,
  "total_engrams_created": 1050,
  "total_consolidated": 200,
  "dream_cycles": 15
}
```

#### memhop_list_schemas

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |

响应：
```json
{
  "schemas": [
    {
      "id": "sch_001",
      "text": "用户偏好先抽象后具体",
      "summary": null,
      "keywords": ["抽象", "具体"],
      "stability": 0.8,
      "internal_consistency": 0.9,
      "match_count": 10,
      "contradiction_count": 1,
      "activation_count": 15
    }
  ]
}
```

---

### 4.5 纠缠与世界观

| 工具 | 用途 | 版本 |
|------|------|------|
| `memhop_list_entanglements` | 列出所有跨树纠缠事件 | v0.12 |
| `memhop_entanglement_detail` | 获取单个纠缠事件详情 | v0.12 |
| `memhop_list_worldviews` | 列出所有涌现的世界观模式 | v0.12 |
| `memhop_worldview_detail` | 获取单个世界观模式详情 | v0.12 |
| `memhop_my_worldview` | 获取稳定的世界观自然语言摘要 | v0.12 |

#### memhop_list_entanglements

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |

响应：按 strength 降序排列的纠缠事件数组。

#### memhop_entanglement_detail

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `event_id` | string | 是 | 事件 ID |

响应：单个纠缠事件详情。

#### memhop_list_worldviews

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |

响应：世界观模式数组（含 category、pattern、stability 等）。

#### memhop_worldview_detail

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `wv_id` | string | 是 | 世界观 ID |

响应：单个世界观模式详情。

#### memhop_my_worldview

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |

响应：
```json
{
  "summary": "[工作方式] 倾向于先抽象再具体 (稳定度: 0.8)\n[沟通方式] 偏好结构化回答 (稳定度: 0.7)",
  "patterns": [...]
}
```

---

### 4.6 上下文管理（v0.13.2）

| 工具 | 用途 | 版本 |
|------|------|------|
| `memhop_list_contexts` | 列出活跃上下文 | v0.13.2 |
| `memhop_compress_context` | 将上下文压缩到知识树 | v0.13.2 |
| `memhop_context_stats` | 获取上下文系统统计 | v0.13.2 |

#### memhop_list_contexts

请求：
```json
{
  "name": "memhop_list_contexts",
  "arguments": {
    "agent_id": "zt_mac"
  }
}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |

响应：
```json
{
  "active_contexts": [
    {
      "id": "ctx_1717200000",
      "summary": "Python CSV 处理讨论",
      "plan_id": "plan_xxx",
      "turn_count": 8,
      "hit_count": 3,
      "last_active": 1717203600000,
      "created_at": 1717200000000,
      "phase": "full",
      "compressed_summary": "用户需要CSV处理脚本...",
      "tree_id": "tree_xxx"
    }
  ],
  "active_count": 1,
  "dormant_count": 5
}
```

#### memhop_compress_context

请求：
```json
{
  "name": "memhop_compress_context",
  "arguments": {
    "agent_id": "zt_mac",
    "context_id": "ctx_1717200000"
  }
}
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 标识 |
| `context_id` | string | 是 | 要压缩的上下文 ID |

响应：
```json
{
  "status": "ok",
  "context_id": "ctx_1717200000",
  "tree_id": "tree_xxx"
}
```

#### memhop_context_stats

请求：
```json
{
  "name": "memhop_context_stats",
  "arguments": {
    "agent_id": "zt_mac"
  }
}
```

响应：
```json
{
  "phase": "full",
  "active_count": 2,
  "dormant_count": 5,
  "max_active_contexts": 5,
  "context_match_threshold": 0.75,
  "context_half_life_hours": 12.0,
  "total_engrams": 1234
}
```

---

### 4.7 废弃工具（向后兼容）

| 工具 | 状态 | 替代方案 |
|------|------|---------|
| `memhop_knowledge_search` | **DEPRECATED** | `memhop_recall` + `tree=<path>` + `kind_filter=["knowledge"]` |
| `memhop_mount_shelf` | **DEPRECATED alias** | `memhop_mount_tree` |
| `memhop_unmount_shelf` | **DEPRECATED alias** | `memhop_unmount_tree` |

`memhop_knowledge_search` 响应：
```json
{
  "status": "ok",
  "results": [{"text": "...", "score": 0.0, "source": "...", "location": "..."}],
  "deprecation_warning": "Use memhop_recall with tree and kind_filter instead."
}
```

> 注意：`score` 字段在 deprecated 实现中**硬编码为 0.0**。需要真实分数的请迁移到 `memhop_recall`。

---

## 五、上下文生命周期

```
三阶管理:
├── 活跃上下文 (ActiveSet, 内存, max=5)
│   ├── 当前对话直接匹配（余弦相似度 + 时间衰减）
│   ├── miss_streak≥5 或 idle>24h → 移入休眠池
│   └── 每个上下文累计 turn_count，>=5 轮自动压缩建树
│
├── 休眠上下文 (DormantPool, LMDB 持久化, max=1000)
│   ├── 活跃集 miss 时自动检索
│   ├── 命中(余弦>0.65) → reactivate 到活跃集
│   ├── last_active > 7天 → 归档
│   └── 归档后不再 reactivate（仅保留 trace）
│
└── 三观模式 (WorldviewPattern, LMDB 持久化)
    ├── Dream REM 阶段从纠缠事件涌现
    ├── 稳定度 > 0.7 → 注入 worldview_context
    ├── embedding 冲突检测 → cognitive_conflicts
    └── 通过 memhop_my_worldview 注入 LLM system prompt
```

> v0.13.2 新增 `memhop_list_contexts`、`memhop_compress_context`、`memhop_context_stats` 三个上下文管理工具。
> Agent 现在可以主动管理上下文：列出活跃/休眠上下文、压缩上下文到知识树、获取统计信息。

---

## 六、接入要点

### agent_id 代替 agent_path

- 所有工具传 `agent_id` 而非 `agent_path`
- memhop 自动映射到 `{MEMHOP_BRAINS_DIR}/{agent_id}/memhop.db`
- 每个 agent 使用独立的 LMDB 数据库，完全隔离

### 上下文过滤降失憶率

- `memhop_recall` 传 `context_id` 指定范围
- 只返回该上下文内的记忆
- 无 `context_id` 时全局搜索（精度较低）

### Worldview 过滤降幻听率

- `use_worldview_filter=true` 启用世界观冲突过滤
- 冲突检测支持 embedding 对比（自动）和 LLM 结果（手动）
- 通过 `llm_conflict_check` 参数传入应用层 LLM 的检测结果

### 自动建树

- `auto_create_tree=true`（默认）：上下文满 5 轮自动创建知识树
- `auto_compress=true`（默认）：上下文自动压缩
- 可通过 `llm_compressed_summary` 提供 LLM 生成的优质摘要
- `auto_create_tree=false` 完全关闭自动建树

### Dream 由 Agent 控制

- memhop 不自动触发 dream，Agent 自主控制
- dream 时自动压缩所有待处理的上下文
- 建议每 50 轮或系统空闲时调一次
- 可通过 `llm_patterns` 和 `llm_contradictions` 传入 LLM 发现的模式

### 世界观注入（降幻听率）

- Agent 可在对话开始时调 `memhop_my_worldview`
- 将三观摘要注入 LLM system prompt
- recall 响应中的 `worldview_context` 和 `cognitive_conflicts` 可直接使用

### Agent 维护工作记忆

- memhop 不保存 cortex（工作记忆），Agent 自己维护最近 N 轮
- 三块记忆区域：上下文（L1）、知识树（L2）、纠缠图（L0）

---

## 七、常见模式（Best Practices）

### 7.1 Amygdala 情感权重持久化

**不推荐的做法**：向 memhop 请求 `memhop_update`（该工具不存在，也不应该存在）

memhop 是 APPEND-ONLY 的记忆系统，engram 创建后不可修改。

**推荐的做法**：

```
Amygdala 的权重持久化 → Agent 自行管理本地状态
  ├── 方案 A: Agent 进程内缓存（重启丢失，可接受则最简单）
  ├── 方案 B: 侧写文件 ~/.meowagent/amygdala_weights.json
  │           每次情感学习后写入，启动时读取
  └── 方案 C: 使用 memhop_reflect 记录学习事件
              每次情感权重变更时创建一条 Reflection
              通过 context_id 将 Reflection 与原始 engram 关联
              下次 recall 时一并获取
```

方案 C 的优势：保留情感学习轨迹，符合 APPEND-ONLY 语义。

### 7.2 engram_id 的获取与保存

- `memhop_store` 返回 `engram_id`，Agent 应保存到本地上下文
- 后续通过 `memhop_recall` 重新获取完整数据
- engram_id 在重启后保持不变（LMDB 持久化）

### 7.3 外部编码器支持

- `memhop_store` 和 `memhop_recall` 都支持 `vector` / `query_vector` 参数
- 如果 Agent 使用外部编码器（如 Python sentence-transformers），可以传入预计算向量
- memhop 会自动将不足 1024 维的向量 pad 并 renormalize
- 不传则使用 memhop 内部的 BGE-M3 ONNX 编码器

### 7.4 知识搜索迁移指南

如果当前使用 `memhop_knowledge_search`（已废弃）：

```
旧: memhop_knowledge_search(query="xxx", shelf_id="/path")
新: memhop_recall(query="xxx", tree="/path", kind_filter=["knowledge"])
```

区别：
- 新 API 返回的分层结构中每条结果包含 `score` 可信度分数，可直接用于 Prompt 标注
- 支持 `kind_filter` 按类型过滤
- 支持 `mode` 切换检索策略

### 7.5 不存在的工具清单（防止幻觉）

以下工具**不存在**、不存在、不存在。不要调用：

| 不存在的工具 | 建议替代 |
|-------------|---------|
| `memhop_update` | 不用。见 7.1 Amygdala 方案 |
| `memhop_context_detail` | 不存在，用 `memhop_list_contexts` |
| `memhop_set_context_active` | 不存在 |
| `memhop_get_plans` | 用 `memhop_get_plan_tree` |
| `memhop_mount_shelf` / `memhop_unmount_shelf` | 存在但 deprecated，直接用 `mount_tree` / `unmount_tree` |

---

## 八、版本信息

| 组件 | 版本 |
|------|------|
| memhop core | 0.13.2 |
| memhop-mcp-server | 0.13.2 |
| 本指南最后更新 | v0.13.2 |

API 变更协议参见 [G-06: 跨项目依赖链协议](.qoder/rules/G-06-跨项目依赖链协议.md)。
