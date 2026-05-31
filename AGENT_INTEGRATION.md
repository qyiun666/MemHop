# MemHop — Agent 接入指南

MemHop 是一个无状态联想记忆引擎内核，通过 MCP JSON-RPC 协议对外暴露。Agent 通过 stdio 子进程方式调用。

---

## 一、概念

```
┌─ memhop 进程（无状态）────────────────────────┐
│  BGE-M3 向量模型 + LMDB 持久化                │
│  收到请求 → 开 DB → 处理 → 返回               │
│  不记住任何 agent 的对话状态                   │
│  多个 agent 可共用（传各自的 agent_path）       │
└──────────────────────────────────────────────┘
          ▲ MCP (JSON-RPC 2.0 over stdio)
          │
┌──────────┴───────────┐
│  Agent 进程（有状态）   │
│  cortex（工作记忆）     │
│  session（当前话题）    │
│  自主调 memhop 存/取   │
└──────────────────────┘
```

**Agent 维护自己的会话状态**（cortex、当前话题等）。memhop 只做：收到请求 → 开 DB → 处理 → 返回。

### 每轮对话流程

```
Agent 进程内部:
  loop {
    user_input = 等待用户输入

    // 1. 从 memhop 获取相关上下文
    context = memhop_recall(user_input, agent_path)

    // 2. 组装精简上下文给 LLM（不是全量历史）
    llm_prompt = agent.cortex.最近几轮() + context.关联记忆(摘要)

    // 3. LLM 生成回复
    ai_response = llm.generate(llm_prompt)

    // 4. 同时存入用户输入 + AI 回复
    memhop_store(user_input, agent_response=ai_response, agent_path)

    // 5. agent 更新自己的工作记忆
    agent.cortex.push(user_input, ai_response)

    // 6. agent 自主决定何时触发巩固
    if agent.decides_to_dream():
        memhop_dream(agent_path)
  }
```

---

## 二、启动

```bash
MEMHOP_DB_PATH=~/agent_data/brain.db \
  MEMHOP_ONNX_MODEL=~/models/bge-m3 \
  memhop-mcp-server
```

环境变量：

| 变量 | 用途 | 默认 |
|------|------|------|
| `MEMHOP_DB_PATH` | 记忆存储路径 | `./memhop.db` |
| `MEMHOP_ONNX_MODEL` | BGE-M3 编码器目录路径（必须） | 无，启动失败 |

Agent 通过 stdin/stdout 的 JSON-RPC 与 MCP server 通信。

多 agent 共用时，每个 MCP 调用传 `agent_path` 参数：

```json
{"name":"memhop_store", "arguments":{
    "text":"...", "agent_path":"~/agent_a/memhop"}}
```

`agent_path` 缺省时走 `MEMHOP_DB_PATH` 环境变量。

---

## 三、MCP 工具清单

### 3.1 核心记忆操作

| 工具 | 用途 | 关键参数 |
|------|------|---------|
| `memhop_store` | 存一轮对话 | `text`(用户输入), `agent_response`(AI回复,可选), `session_id`, `valence`, `arousal` |
| `memhop_recall` | 召回相关记忆 | `query`, `session_id`, `limit`, `mode`, `tree_id` |
| `memhop_reflect` | 创建反思记忆 | `content`, `kind`, `session_id` |
| `memhop_dream` | 触发记忆巩固 | 无参数 |
| `memhop_forget` | 删除指定轮次 | `turn_id` |

**memhop_store 示例：**

```json
{
  "name": "memhop_store",
  "arguments": {
    "text": "帮我写一个 Python 脚本处理 CSV",
    "agent_response": "好的，我来写一个 CSV 处理脚本...",
    "session_id": "sess_001",
    "valence": 0.3,
    "arousal": 0.5
  }
}
```

返回：
```json
{"engram_id": "eng_xxx", "plan_id": "plan_xxx", "plan_hint": "Continuing", "context_id": "ctx_xxx"}
```

store 后自动触发 organize（提取实体 → 链接图 → 更新上下文 → 检测话题边界）。

**memhop_recall 示例：**

```json
{
  "name": "memhop_recall",
  "arguments": {
    "query": "CSV 脚本",
    "session_id": "sess_001",
    "limit": 10,
    "mode": "retrieval",
    "tree_id": null
  }
}
```

返回（摘要优先，不含原文全文）：
```json
{
  "working_memory": [],
  "associations": [{"text": "CSV 处理脚本的讨论...", "kind": "episode", "score": 0.91}],
  "knowledge_memories": [{"text": "pandas read_csv 用法...", "kind": "knowledge"}],
  "schemas": [],
  "tree_contexts": {},
  "worldview_context": ["倾向于先抽象再具体 (稳定度: 0.8)"],
  "cognitive_conflicts": []
}
```

### 3.2 知识树/项目图管理

| 工具 | 用途 |
|------|------|
| `memhop_mount_tree` | 挂载知识源（目录/文件）→ 扫描分块→编码→写入 |
| `memhop_unmount_tree` | 卸载知识源 |
| `memhop_tree_status` | 查看已挂载知识源 |
| `memhop_create_tree` | 创建逻辑知识树（按领域组织记忆） |
| `memhop_list_trees` | 列出所有知识树 |
| `memhop_get_tree` | 获取单棵树详情 |
| `memhop_move_to_tree` | 将记忆移动到指定树 |
| `memhop_delete_tree` | 删除知识树（不解绑记忆） |

### 3.3 纠缠图查询

| 工具 | 用途 |
|------|------|
| `memhop_list_entanglements` | 列出纠缠事件（跨树关联） |
| `memhop_entanglement_detail` | 查看单个纠缠事件详情 |

### 3.4 世界观查询

| 工具 | 用途 |
|------|------|
| `memhop_list_worldviews` | 列出三观模式列表 |
| `memhop_worldview_detail` | 查看单个模式详情 |
| `memhop_my_worldview` | 获取自然语言三观摘要（可注入 LLM system prompt） |

### 3.5 Plan 管理

| 工具 | 用途 |
|------|------|
| `memhop_complete_plan` | 手动标记 Plan 完成 |
| `memhop_get_plan_tree` | 查看 Plan 树（含已完成计划的压缩摘要） |
| `memhop_get_chat_history` | 查看指定 Plan 的对话历史 |
| `memhop_plan_stats` | Plan 统计数据 |

### 3.6 统计与健康

| 工具 | 用途 |
|------|------|
| `memhop_stats` | 脑统计信息 |
| `memhop_count` | 总记忆数 |
| `memhop_health` | 健康检查（uptime、版本、活跃 Brain 数） |
| `memhop_list_schemas` | 列出涌现的 Schema 模式 |

---

## 四、接入要点

**摘要优先，按需取原文**
- `memhop_recall` 默认返回摘要文本（标题/关键点），不返回对话全文
- 如需原文，通过 `memhop_get_chat_history` 按 plan_id 取
- Agent 组装 LLM context 时用摘要，不塞全量历史

**Agent 维护工作记忆**
- memhop 不保存 cortex（工作记忆），Agent 自己维护最近 N 轮
- 调用 `memhop_recall` 时传 `session_id`，memhop 匹配上下文

**三块记忆区域**
- **上下文**：当前聊的话题。知识树 + 活动上下文。每轮 store 后自动整理（organize）
- **项目图**：挂载的知识源。通过 `memhop_mount_tree` 导入代码/文档/笔记
- **纠缠图**：跨上下文、跨项目的关联。recall 和 dream 时自动创建

**世界观注入**
- Agent 可在对话开始时调 `memhop_my_worldview`，将三观摘要注入 LLM system prompt
- 例：`system_prompt += f"\n## 对用户的了解\n{worldview['summary']}"`

**Dream 由 Agent 控制**
- memhop 不自动触发 dream，Agent 自主决定何时巩固
- 建议：每 50 轮对话或系统空闲时调一次 `memhop_dream`

**memory 原文存储**
- `memhop_store` 的 `text` 字段存用户输入原文
- `agent_response` 字段存 AI 回复原文（可选）
- 两者组合形成完整 DialogueTurn，后续 organize 提取摘要
- recall 默认返回摘要，不返回原文（避免 token 浪费）
