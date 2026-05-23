# MemHop

嵌入式联想记忆引擎。O(1) 召回，单文件 LMDB 存储，零配置，零网络依赖。

## 安装

```bash
pip install memhop
```

Python 3.10+，预编译 wheel，macOS/Linux/Windows。

## Quick Start

```python
import memhop

with memhop.open("brain.db") as db:
    # 写入记忆
    id = db.remember("今天吃了豆浆油条", meta={"tags": ["早餐"], "session_id": "s01"})

    # O(1) 联想召回
    m = db.recall("早餐吃了什么")
    print(m.text)       # "今天吃了豆浆油条"
    print(m.confidence) # 0.94
```

## 核心 API

### 读写

| 方法 | 说明 |
|------|------|
| `remember(text, meta?)` | 写入记忆，返回记忆 ID |
| `remember_batch(items)` | 批量写入（去重+快 3x） |
| `recall(cue)` | O(1) 联想召回，最佳匹配 |
| `recall_topk(cue, k=5)` | Top-K 召回 |
| `fuse_recall(cues, weights?)` | 多 cue 融合召回 |
| `fuse_recall_topk(cues, k, weights?)` | 多 cue Top-K |
| `search(filters, limit?)` | 按 meta 字段精确过滤 |
| `recent(limit=5)` | 最近写入的记忆 |
| `forget(id)` | 删除 |
| `update(id, text?, meta?)` | 更新 |

### 链路

| 方法 | 说明 |
|------|------|
| `link_to(from_id, to_id, type)` | 创建记忆之间连接 |
| `links_of(id)` | 出链 → `[{to, type}]` |
| `links_to(id)` | 入链 ← `[{from, type}]` |

### 场景门控 v0.4.0+

自动根据对话场景缩小召回范围，无需手动传 scope：

| 方法 | 说明 |
|------|------|
| `set_gating(enabled)` | 启用/禁用 |
| `set_gating_threshold(t)` | 余弦相似度阈值（默认 0.6） |
| `reset_scene()` | 清除当前场景锚定 |

场景路由（v0.5.3）引入 `recent_turn_summary`，在 `remember` 时自动累积滚动摘要，`recall` 时结合 query + recent 上下文做联合匹配。连续 miss ≥ 3 自动切场景。

### 纠缠扩散 v0.5.3

```python
with memhop.open("brain.db") as db:
    results = db.spread_activation("God Object", max_hops=2)
    # → [{id: "...", activation: 0.85}, {id: "...", activation: 0.42}]
```

从 Hopfield 召回种子 → BFS 遍历 `link_to` 连接 → 返回激活量降序列表。衰减系数 0.5/跳。

### 可塑性

| 方法 | 说明 |
|------|------|
| `recall_with_plasticity(cue)` | 召回 + 吸引子漂移 |
| `enable_plasticity(bool)` | 启用/禁用 |
| `get_memory_stats(id)` | 访问统计 |
| `trigger_decay()` | 强制执行衰减 |

### 引擎信息

| 方法 | 说明 |
|------|------|
| `stats` | 引擎统计（总数/活跃/平均重要性/Layer分布） |
| `count` | 记忆总数 |
| `entity_graph()` | 纠缠图（Layer 1 → entity 链路） |
| `knowledge_tree()` | 知识树 |
| `episode_thread(session_id?)` | 会话线程 |
| `memories_by_layer(layer?)` | 按 Layer 分组 |
| `purge_before(datetime)` | 清理旧记忆 |

## 核心特性

- **O(1) 召回**: Modern Hopfield Network，与记忆总量无关
- **单文件**: LMDB 持久化，`brain.db` 即走即拷
- **零模型默认**: 字符 n-gram 哈希编码，无需下载任何模型
- **三层记忆**: entity（纠缠图）/ knowledge（知识树）/ episode（原文）
- **场景门控**: 自动按 session/tree/anchor 过滤候选集
- **纠缠扩散**: 沿连接图 BFS 激活，类似认知联想
- **无 Calibrator**: Dream Mode 纯本地统计，不发起任何网络请求（v0.5.3）

## License

All Rights Reserved. Copyright (c) 2026 MemHop Contributors.
