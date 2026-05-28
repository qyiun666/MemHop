# MeowAgent 适配 MemHop v0.9.1 — 开发任务

**日期**: 2026-05-28  
**依赖**: MemHop v0.9.1（turn 级存储 API）  

---

## 背景

MemHop v0.9.1 改为 turn 级独立存储。MeowAgent 需要在 perceive 前拆分多话题、管理 turn_id/session_id、消费 Schema。

---

## 改动

### 1. 单轮多话题拆分

用户一句话包含多个独立话题时，在调用 `memhop.store` 前拆分：

```python
# MeowAgent perceive hook
def perceive(text: str, turn_id: str, session_id: str):
    # LLM 快速判断是否有多个话题（一次 cheap call）
    topics = llm.split_topics(text)
    # 示例: "JWT 过期了，顺便 logo 改蓝，CI 也挂了"
    # → ["JWT token 过期", "logo 颜色改成蓝色", "CI pipeline 挂了"]

    for i, (topic_text, topic_label) in enumerate(topics):
        memhop.store(
            content=topic_text,
            turn_id=turn_id,
            segment_index=i,
            topic_label=topic_label,  # "jwt"/"logo"/"ci"
            session_id=session_id,
        )

    # 原文存外部存储
    db.put(turn_id, text)
```

### 2. turn_id / session_id 管理

| 字段 | 谁生成 | 规则 |
|------|--------|------|
| `session_id` | MeowAgent | 每次新会话新建 UUID |
| `turn_id` | MeowAgent | 每轮递增 `{session_id}_T{n}` |
| `segment_index` | MeowAgent | 同一轮拆分后 0,1,2... |

### 3. Recall 消费

```python
resp = memhop.recall("JWT 过期怎么修？")

# resp.hit_turns: 命中的具体 turn
for hit in resp.hit_turns:
    full_text = db.get(hit.turn_id)  # 从外部存储拉完整原文
    prompt += f"[{hit.session_id} 第{hit.turn_index}轮] score={hit.score}\n{full_text}\n"

# resp.aggregated_sessions: 按 session 聚合，用于找"整场对话线索"
for sess in resp.aggregated_sessions:
    all_turns = db.get_session_turns(sess.session_id)
    prompt += f"[会话摘要] {sess.session_id}: {len(all_turns)}轮\n"
```

### 4. Schema 消费

Dream 生成的 Schema 通过 `memhop.stats()` 或新的 `memhop.list_schemas()` 接口获取：

```python
schemas = memhop.list_schemas(session_id="...")
# → [
#   { id: "schema_jwt", summary: "修 JWT bug, 方案 RSA SHA256", turns: [1,3,5,...,19] },
#   { id: "schema_logo", summary: "改 logo 为蓝色", turns: [2,4,6,...,18] },
# ]
```

---

## 新增 MCP Tool 需求（对 MemHop）

| Tool | 说明 |
|------|------|
| `memhop_list_schemas(session_id?)` | 列出 Schema，可按 session 过滤 |

---

## 不变

- MCP 协议不变
- 猫管理逻辑不变
- CodeGraph / KnowledgeBase 独立不改
