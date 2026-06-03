# MemHop v0.14 — meowAgent 集成指南

## 一、概述

MemHop v0.14 是一个完全重新设计的记忆引擎，基于 **1×L1 超图 + N×L2 话题图 + M×L3 领域超图 + L4 原文库** 的四层架构。

### v0.13 → v0.14 核心变化

| 变化 | v0.13 | v0.14 |
|------|-------|-------|
| 编码器 | ONNX 必选 + Ngram | 纯 Ngram + BM25 |
| 检索 | HNSW + Hopfield + BM25 | 纯 BM25 |
| 数据模型 | Engram / Tree / Entanglement 等 | Hyperedge / Topic / KnowledgeNode / RawDocument |
| 存储 | 1 个 LMDB × 9 子库 | 4 个独立 LMDB 环境 |
| 写入 | 逐条 memhop_store | memhop_batch_store（批量） |
| 层 | 3 层（Cortex/Hippocampus/Neocortex） | 4 层（L1 超图/L2 话题/L3 领域/L4 原文） |
| 事件链 | 无 | 超边链（Hyperedge Chain） |
| 跨平台 | macOS 优先 | macOS / Linux / Windows |
| 外部依赖 | ort + tokenizers + reqwest | 无 |

## 二、启动

### 环境变量

```bash
# 以前需要的（已删除）
# MEMHOP_ONNX_MODEL=...
# MEMHOP_RERANKER_MODEL=...

# 现在只需要（可选）
MEMHOP_BRAINS_DIR=/path/to/brains   # 默认 ./memhop_brains
MEMHOP_SOCKET=/tmp/memhop.sock      # 默认 /tmp/memhop.sock
```

### 启动命令

```bash
memhop-mcp-server
# 输出: memhop-mcp-server v0.14.0 listening on /tmp/memhop.sock
```

启动时间：**<100ms**（无 ONNX 加载）

## 三、MCP 工具

### 3.1 memhop_batch_store（核心，P0）

替代 v0.13 的逐条 memhop_store。**所有写入都通过此工具**。

**请求：**

```json
{
  "jsonrpc": "2.0",
  "method": "memhop_batch_store",
  "params": {
    "agent_id": "cat_1",
    "items": [{
      "text": "user: 她喜欢喝可乐 | assistant: 了解",
      "turn_id": "session_1_T5",
      "session_id": "session_1",
      "source": "chat",
      "topic_label": "饮品偏好",
      "llm_keywords": ["可乐", "偏好"],
      "llm_compressed_summary": "用户告知她喜欢可乐",
      "chain_parent_id": null,
      "chain_label": null,
      "valence": 0.7,
      "arousal": 0.3,
      "domain_id": null
    }, {
      "text": "user: 不对，他喜欢喝雪碧 | assistant: 已更新",
      "turn_id": "session_1_T6",
      "session_id": "session_1",
      "source": "chat",
      "topic_label": "饮品偏好",
      "llm_keywords": ["雪碧", "纠正"],
      "llm_compressed_summary": "用户纠正：他喜欢雪碧",
      "chain_parent_id": "he_1749123456789_xxxx",
      "chain_label": "correction",
      "domain_id": null
    }]
  },
  "id": 1
}
```

**响应：**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "l1_nodes_created": 2,
    "l1_hyperedges_created": 1,
    "l2_topics_created": 1,
    "l3_nodes_created": 0,
    "l4_docs_stored": 2,
    "chains_created": 1,
    "total_duration_us": 1234
  }
}
```

**参数说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `items[].text` | string | 是 | 原始对话文本 |
| `items[].turn_id` | string | 否 | 对话轮次 ID |
| `items[].session_id` | string | 否 | 会话 ID |
| `items[].source` | string | 是 | 来源："chat" / "system" / "knowledge" |
| `items[].topic_label` | string | 推荐 | meowAgent 的话题分类标签 |
| `items[].llm_keywords` | string[] | 推荐 | meowAgent 提取的关键词 |
| `items[].llm_compressed_summary` | string | 推荐 | meowAgent 生成的摘要 |
| `items[].chain_parent_id` | string | 否 | 超边链：前一个事件的 hyperedge_id |
| `items[].chain_label` | string | 否 | 超边链标签："correction" / "supplement" / "merge" |
| `items[].valence` | float | 否 | 情绪效价 -1.0~1.0 |
| `items[].arousal` | float | 否 | 情绪唤醒度 0.0~1.0 |
| `items[].domain_id` | string | 否 | 关联的 L3 知识领域 ID |

**内部处理流程：**
1. `text` → L4 原文库（原始文本存储）
2. `text` → L1 超图节点 + 超边关联
3. `topic_label` + `llm_keywords` → L2 话题更新
4. `domain_id` → L3 领域更新
5. `chain_parent_id` → 超边链创建

### 3.2 memhop_health（不变）

```json
// 请求
{"jsonrpc":"2.0","method":"memhop_health","params":{"agent_id":"cat_1"},"id":1}
// 响应
{"jsonrpc":"2.0","id":1,"result":{"status":"ok","version":"0.14.0"}}
```

### 3.3 memhop_consolidate（新增）

```json
// 请求
{"jsonrpc":"2.0","method":"memhop_consolidate","params":{"agent_id":"cat_1"},"id":1}
// 响应
{"jsonrpc":"2.0","id":1,"result":{"chains_consolidated":0,"topics_merged":0,"duration_ms":0}}
```

触发记忆巩固。当前版本返回全零报告，完整实现将在后续迭代中补充。

### 3.4 已删除的工具

以下工具在 v0.14 中已删除，不要再调用：

| 工具 | 替代方案 |
|------|----------|
| memhop_dream | 暂未实现，后续补充 |
| memhop_reflect | 不再需要，batch_store 时传入 llm_compressed_summary |
| memhop_mount/unmount/list_trees | L3 领域通过 domain_id 管理 |
| memhop_create/delete/get_tree | 不再需要 |
| memhop_move_to_tree | 不再需要 |
| memhop_complete/get/plan_stats | 不再需要 |
| memhop_get_chat_history | 待实现按层查询 |
| memhop_list/compress_context | 不再需要 |
| memhop_list_entanglements | 超边链信息附在 recall 结果中 |
| memhop_list_worldviews | 待实现 L1 查询 |
| memhop_list_schemas | 不再需要 |
| memhop_knowledge_search | 待实现 L3 查询 |

## 四、meowAgent 适配要点

### 4.1 必须改的

#### 替换逐条 store 为批量 store

**旧代码（v0.13）：**
```rust
for (topic_label, segment) in topics {
    let req = StoreRequest { text: segment, topic_label: Some(topic_label), ... };
    shared.organs.memory.store(req).await?;
}
```

**新代码（v0.14）：**
```rust
let mut items = Vec::new();
for (topic_label, segment) in topics {
    items.push(StoreItem {
        text: segment,
        topic_label: Some(topic_label),
        llm_keywords: keywords.clone(),
        llm_compressed_summary: summary.clone(),
        turn_id: Some(turn_id.clone()),
        session_id: Some(session_id.clone()),
        source: "chat".to_string(),
        ..Default::default()
    });
}
// 一次 RPC 完成
let report = memhop.batch_store(items).await?;
```

#### 更新 MemoryOrgan trait

```rust
#[async_trait]
pub trait MemoryOrgan: Send + Sync {
    async fn batch_store(&self, items: Vec<StoreItem>) -> Result<BatchReport>;
    async fn recall(&self, query: &str) -> Result<Vec<MemoryEntry>>;
}
```

#### 更新 StoreItem 类型

参考上面的 `StoreItem` 结构体。`StoreItem` 已从 `StoreRequest`（v0.13）简化，移除了不再需要的字段（`vector`, `kind`, `tree`, `match_threshold`, `context_half_life` 等）。

### 4.2 推荐做的事

1. **继续提供 topic_label**：你在 topic_splitter 中已经用 LLM 做了话题分类，直接传入 batch_store，这是 L2 话题的输入
2. **继续提供 llm_keywords**：用于 L2 关键词索引
3. **继续提供 llm_compressed_summary**：用于 L2 摘要
4. **纠正场景使用 chain_parent_id**：当用户纠正记忆时（"可乐→雪碧"），传入 chain_parent_id 建立超边链

### 4.3 仍然可用的

| meowAgent 侧 | 状态 |
|-------------|------|
| topic_splitter（LLM 话题分割） | ✅ 继续使用 |
| llm_keywords 生成 | ✅ 继续使用 |
| llm_compressed_summary 生成 | ✅ 继续使用 |
| delta_queue 持久化 | ✅ 改用 batch_store |
| crystallize（技能结晶） | ✅ 改用 domain_id |
| session manager | ✅ 不变 |

## 五、性能对比

| 指标 | v0.13 | v0.14 | 说明 |
|------|-------|-------|------|
| 启动时间 | 30-120s（ONNX 加载） | <100ms | 纯 ngram |
| 存储 / 100K 条 | ~800MB | ~200MB | 去除 f16 密集向量 + 无压缩 |
| 内存占用 | ~500MB | ~50MB | BM25 稀疏索引 |
| 写入 / 100 条 | ~300ms（100 次 IPC） | ~5ms（1 次 IPC） | batch_store |
| 跨平台 | macOS 优先 | macOS/Linux/Windows | 零 C 依赖 |

## 六、数据格式

v0.14 数据库格式不兼容 v0.13。如需迁移请联系 MemHop 团队。

数据库文件布局（每个 agent 一个目录）：
```
<brains_dir>/<agent_id>/
├── l1_hypergraph.db/    # L1 超图
├── l2_topics.db/        # L2 话题
├── l3_domains.db/       # L3 领域
└── l4_raw.db/           # L4 原文
```
