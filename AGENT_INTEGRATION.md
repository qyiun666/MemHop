# MemHop v0.13 — Agent 接入指南

MemHop 是一个嵌入式联想记忆引擎内核，通过 MCP JSON-RPC 协议对外暴露。Agent 通过 stdio 子进程方式调用。

**v0.13 BREAKING CHANGE**: `agent_path` 已移除，改用 `agent_id`。每个 agent 使用独立的记忆文件。

---

## 一、概念

```
┌─ memhop 进程（每台电脑一个）──────────────────────┐
│  MEMHOP_BRAINS_DIR/~/.memhop/brains/{agent_id}/   │
│  ├── "zt_mac"    →  ~/.memhop/brains/zt_mac/      │
│  ├── "zt_phone"  →  ~/.memhop/brains/zt_phone/    │
│  └── "desk"      →  ~/.memhop/brains/desk/        │
│  多 agent 隔离，每个 agent 有独立 HNSW/Engram      │
└──────────────────────────────────────────────────┘
          ▲ MCP (JSON-RPC 2.0 over stdio)
          │
┌──────────┴───────────┐
│  Agent 进程（有状态）  │
│  传 agent_id 标识自己  │
│  自主调 memhop 存/取   │
└──────────────────────┘
```

核心变化：
- **v0.12 及之前**：传 `agent_path`（文件路径），应用层管理路径映射
- **v0.13+**：传 `agent_id`（逻辑标识），memhop 自动解析到 `~/.memhop/brains/{agent_id}/memhop.db`

### 质量目标

| 指标 | 目标 | 设计对策 |
|------|------|---------|
| 上下文不爆炸 | 活跃 ≤5, 休眠 ≤1000 | 三阶管理：活跃→休眠→归档 |
| 失憶率 | <5% | `context_id` 过滤 + Cross-Encoder reranker |
| 幻听率 | <5% | Worldview 冲突过滤 + context 范围限制 |

---

## 二、启动

```bash
MEMHOP_BRAINS_DIR=~/.memhop/brains \
  MEMHOP_ONNX_MODEL=~/models/bge-m3 \
  memhop-mcp-server
```

环境变量：

| 变量 | 用途 | 默认 |
|------|------|------|
| `MEMHOP_BRAINS_DIR` | 多 agent 记忆文件根目录 | `~/.memhop/brains/` |
| `MEMHOP_ONNX_MODEL` | BGE-M3 编码器目录路径（必须） | 无，启动失败 |
| `MEMHOP_RERANKER_MODEL` | Cross-Encoder 重排序模型路径 | 无，不启用 |

**所有 MCP 工具必须传 `agent_id` 参数**（校验规则：`[a-zA-Z0-9_-]{1,64}`）：

```json
{"name":"memhop_store", "arguments":{
    "text":"...", "agent_id":"zt_mac"}}
```

---

## 三、每轮对话流程

```
Agent 进程内部:
  loop {
    user_input = 等待用户输入

    // 1. 从 memhop 获取相关上下文
    response = memhop_recall(user_input, agent_id="zt_mac")
    // ← 返回: results, worldview_context, contexts_summary, cognitive_conflicts

    // 2. 注入世界观 + 相关上下文到 LLM prompt
    llm_prompt = response.contexts_summary + response.worldview_context
                + agent.cortex.最近几轮()

    // 3. LLM 生成回复
    ai_response = llm.generate(llm_prompt)

    // 4. 存入记忆（memhop 自动匹配上下文、可自动建树）
    store_resp = memhop_store(user_input, agent_response=ai_response,
                              agent_id="zt_mac", session_id="sess_001")
    // ← 返回: context_id, phase, auto_tree_id (如触发自动建树)

    // 5. agent 更新工作记忆
    agent.cortex.push(user_input, ai_response)

    // 6. agent 自主决定何时巩固
    if agent.decides_to_dream():
        dream_resp = memhop_dream(agent_id="zt_mac")
        // ← 返回: contexts_compressed, new_entanglements, dormant_moved
  }
```

---

## 四、MCP 工具清单

### 4.1 核心记忆操作

| 工具 | 用途 | v0.13 关键变化 |
|------|------|---------------|
| `memhop_store` | 存一轮对话 | **新增**: `agent_id`(必填), `auto_create_tree`, `auto_compress`, `match_threshold`, `context_half_life`, `llm_compressed_summary`, `llm_keywords` |
| `memhop_recall` | 召回相关记忆 | **新增**: `agent_id`(必填), `context_id`, `use_worldview_filter`, `llm_conflict_check` |
| `memhop_reflect` | 创建反思记忆 | **新增**: `agent_id`(必填) |
| `memhop_dream` | 触发记忆巩固 | **新增**: `agent_id`(必填), `context_compress`, `llm_patterns`, `llm_contradictions` |
| `memhop_forget` | 删除指定轮次 | **新增**: `agent_id`(必填) |

#### memhop_store

```json
{
  "name": "memhop_store",
  "arguments": {
    "agent_id": "zt_mac",
    "text": "帮我写一个 Python 脚本处理 CSV",
    "agent_response": "好的，我来写一个 CSV 处理脚本...",
    "session_id": "sess_001",
    "valence": 0.3,
    "arousal": 0.5,
    "auto_create_tree": true,
    "auto_compress": true,
    "match_threshold": 0.75,
    "context_half_life": 12.0,
    "llm_compressed_summary": "用户需要CSV处理脚本的讨论摘要",
    "llm_keywords": ["python", "csv", "脚本"],
    "tree_id": "tree_xxx"
  }
}
```

返回：
```json
{
  "status": "stored",
  "engram_id": "eng_xxx",
  "plan_id": "plan_xxx",
  "plan_hint": "Continue",
  "plan_name": "Python CSV处理",
  "context_id": "ctx_1717200000",
  "context_summary": "关于Python CSV处理的讨论",
  "context_turn_count": 5,
  "phase": "full",
  "auto_tree_id": "tree_xxx"
}
```

**store 后自动触发**：
- 上下文匹配（活跃集 / 休眠池回退）
- 上下文 turn_count 递增
- 如果 turn_count >= 5 + 无关联树 → 自动建树
- L1↔L2 关联更新
- organize（提取实体 → 链接图 → 检测话题边界）

#### memhop_recall

```json
{
  "name": "memhop_recall",
  "arguments": {
    "agent_id": "zt_mac",
    "query": "CSV 脚本",
    "session_id": "sess_001",
    "limit": 10,
    "mode": "retrieval",
    "context_id": "ctx_1717200000",
    "use_reranker": true,
    "use_worldview_filter": true,
    "llm_conflict_check": "{\"type\": \"consistent\", \"description\": \"...\"}"
  }
}
```

返回：
```json
{
  "results": [{"text": "CSV 处理脚本的讨论...", "kind": "episode", "tree_path": null}],
  "knowledge_memories": [{"text": "pandas read_csv 用法...", "kind": "knowledge"}],
  "schemas": [],
  "hit_turns": [],
  "tree_contexts": [],
  "graph_associations": [],
  "contexts_summary": [
    {"id": "ctx_1717200000", "summary": "Python CSV处理",
     "match_score": 0.89, "turn_count": 5,
     "tree_ids": ["tree_xxx"], "dormant": false}
  ],
  "worldview_context": ["倾向于先抽象再具体 (稳定度: 0.8)"],
  "cognitive_conflicts": [],
  "recall_quality": {
    "scope": "context_filtered",
    "context_hit_count": 5,
    "total_candidates": 12
  },
  "trace": {"latency_us": 1234, "hopfield_candidates": 80, "spread_steps": 3}
}
```

**recall 质量机制**：
- `context_id` 指定时 → 只返回该上下文内的记忆（失憶率↓）
- `use_reranker=true` → Cross-Encoder 重排序（幻听率↓）
- `use_worldview_filter=true` → 过滤与世界观冲突的结果（幻听率↓）
- 无 `context_id` → 全局搜索

#### memhop_dream

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

返回：
```json
{
  "status": "ok",
  "consolidated_count": 12,
  "pruned_edges": 3,
  "duration_ms": 42,
  "knowledge_processed": 4,
  "hnsw_compacted": 0,
  "contexts_compressed": 2,
  "dormant_moved": 3,
  "archived": 1
}
```

### 4.2 上下文管理（v0.13 新增）

| 工具 | 用途 | 关键参数 |
|------|------|---------|
| `memhop_list_contexts` | 列出所有上下文（活跃+休眠池） | `agent_id` |
| `memhop_context_detail` | 查看上下文详情 | `agent_id`, `context_id` |
| `memhop_set_context_active` | 手动激活指定休眠上下文 | `agent_id`, `context_id` |

### 4.3 知识树管理

| 工具 | 用途 | v0.13 变化 |
|------|------|-----------|
| `memhop_mount_tree` | 挂载知识源（目录/文件） | **新增**: `agent_id` |
| `memhop_unmount_tree` | 卸载知识源 | **新增**: `agent_id` |
| `memhop_tree_status` | 查看已挂载知识源 | **新增**: `agent_id` |
| `memhop_create_tree` | 创建逻辑知识树 | **新增**: `agent_id` |
| `memhop_list_trees` | 列出所有知识树 | **新增**: `agent_id` |
| `memhop_get_tree` | 获取单棵树详情 | **新增**: `agent_id` |
| `memhop_move_to_tree` | 将记忆移动到指定树 | **新增**: `agent_id` |
| `memhop_delete_tree` | 删除知识树 | **新增**: `agent_id` |

### 4.4 其他工具

所有工具均**新增 `agent_id` 必填参数**：
`memhop_stats`, `memhop_count`, `memhop_health`, `memhop_complete_plan`, `memhop_get_plan_tree`, `memhop_get_chat_history`, `memhop_plan_stats`, `memhop_list_schemas`, `memhop_list_entanglements`, `memhop_entanglement_detail`, `memhop_list_worldviews`, `memhop_worldview_detail`, `memhop_my_worldview`

---

## 五、上下文生命周期

```
三阶管理:
├── 活跃上下文 (ActiveSet, 内存, max=5)
│   ├── 当前对话直接匹配（余弦相似度 + 时间衰减）
│   ├── miss_streak≥5 或 idle>24h → 移入休眠池
│   └── 每个上下文累计 turn_count，>=5 轮自动压缩建树
│
├── 休眠上下文 (DormantPool, LMDB持久化, max=1000)
│   ├── 活跃集 miss 时自动检索
│   ├── 命中(余弦>0.65) → reactivate 到活跃集
│   ├── last_active > 7天 → 归档
│   └── 归档后不再 reactivate（仅保留 trace）
│
└── 三观模式 (WorldviewPattern, LMDB持久化)
    ├── Dream REM 阶段从纠缠事件涌现
    ├── 稳定度 > 0.7 → 注入 worldview_context
    ├── embedding 冲突检测 → cognitive_conflicts
    └── 通过 memhop_my_worldview 注入 LLM system prompt
```

---

## 六、接入要点

**agent_id 代替 agent_path**
- v0.13 BREAKING CHANGE：所有工具传 `agent_id` 而非 `agent_path`
- memhop 自动映射到 `{MEMHOP_BRAINS_DIR}/{agent_id}/memhop.db`
- 每个 agent 使用独立的记忆文件，完全隔离

**上下文过滤降失憶率**
- `memhop_recall` 传 `context_id` 指定范围
- 只返回该上下文内的记忆，大幅降低失憶率
- 无 `context_id` 时全局搜索（行为不变）

**Worldview 过滤降幻听率**
- `use_worldview_filter=true` 启用世界观冲突过滤
- 冲突检测支持 embedding 对比（自动）和 LLM 结果（手动）
- 通过 `llm_conflict_check` 参数传入应用层 LLM 的检测结果

**自动建树（可选）**
- `auto_create_tree=true`（默认）：上下文满 5 轮自动创建知识树
- `auto_compress=true`（默认）：上下文自动压缩
- 可通过 `llm_compressed_summary` 提供 LLM 生成的优质摘要
- `auto_create_tree=false` 完全关闭自动建树

**Dream 由 Agent 控制**
- memhop 不自动触发 dream，Agent 自主决定
- dream 时自动压缩所有待处理的上下文
- 建议每 50 轮或系统空闲时调一次
- 可通过 `llm_patterns` 和 `llm_contradictions` 传入 LLM 发现的模式

**世界观注入（降幻听率）**
- Agent 可在对话开始时调 `memhop_my_worldview`
- 将三观摘要注入 LLM system prompt
- recall 响应中的 `worldview_context` 和 `cognitive_conflicts` 可直接使用

**Agent 维护工作记忆**
- memhop 不保存 cortex（工作记忆），Agent 自己维护最近 N 轮
- 三块记忆区域：上下文（L1）、知识树（L2）、纠缠图（L0）
