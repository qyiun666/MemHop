# MemHop meowAgent 集成指南

## 快速开始

```rust
use memhop_core::{Brain, BrainConfig, Encoder, NgramEncoder, RecallRequest, StoreBatch, StoreItem, Layer};
use std::sync::Arc;

// 1. 创建 Brain
let encoder: Arc<Box<dyn Encoder>> = Arc::new(Box::new(NgramEncoder::new(1024)));
let config = BrainConfig {
    brains_dir: "./memhop_brains".to_string(),
    agent_id: "my_agent".to_string(),
};
let mut brain = Brain::open(config, encoder)?;

// 2. 存储记忆
let batch = StoreBatch {
    items: vec![StoreItem {
        text: "用户喜欢喝可乐".to_string(),
        source: "chat".to_string(),
        topic_label: Some("饮品偏好".to_string()),
        llm_keywords: Some(vec!["可乐".to_string()]),
        ..Default::default()
    }],
};
let report = brain.batch_store(batch)?;

// 3. 检索记忆
let response = brain.recall(RecallRequest {
    query: "用户喜欢什么饮料".to_string(),
    max_results: 10,
    target_layers: vec![Layer::L1, Layer::L2],
    ..Default::default()
})?;
```

---

## API 列表

| 方法 | 功能 | 说明 |
|------|------|------|
| `Brain::open(config, encoder)` | 创建 Brain | 打开或创建记忆引擎实例 |
| `brain.batch_store(batch)` | 批量存储 | 所有写入都通过此接口 |
| `brain.recall(request)` | 语义检索 | 基于查询检索相关记忆 |
| `brain.dream()` | 记忆巩固 | 触发记忆整理和模式提取 |
| `brain.set_l0(profile)` | 设置画像 | 设置 Agent 角色画像 |
| `brain.get_l0()` | 获取画像 | 获取当前角色画像 |
| `brain.mount_shelf(path, name, doc_type)` | 挂载知识库 | 导入外部文档 |
| `brain.unmount_shelf(domain_id)` | 卸载知识库 | 移除已挂载的知识库 |
| `brain.list_shelf()` | 列出知识库 | 列出所有已挂载知识库 |
| `brain.activate(session_id, topic_id, ttl_ms)` | 激活话题 | 提升话题在会话中的权重 |
| `brain.deactivate(session_id, topic_id)` | 去激活 | 移除话题激活状态 |
| `brain.get_activated(session_id)` | 获取激活列表 | 获取当前会话激活的话题 |
| `brain.crystallize()` | 程序性结晶 | 从超边链中提取可复用模式 |

---

## batch_store — 批量存储

```rust
pub fn batch_store(&mut self, batch: StoreBatch) -> Result<BatchReport>
```

### StoreItem 参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `text` | String | **是** | 原始文本 |
| `source` | String | 否 | 来源，默认 `"chat"` |
| `topic_label` | String | 推荐 | 话题标签 |
| `llm_keywords` | Vec<String> | 推荐 | 关键词列表 |
| `llm_compressed_summary` | String | 推荐 | LLM 生成的摘要 |
| `turn_id` | String | 否 | 对话轮次 ID |
| `session_id` | String | 否 | 会话 ID |
| `chain_parent_id` | String | 否 | 超边链前驱 ID |
| `chain_label` | String | 否 | 链标签：`correction`/`supplement`/`merge` |
| `domain_id` | String | 否 | 关联领域 ID |
| `importance` | f32 | 否 | 重要性权重 (0.0-1.0) |
| `valence` | f32 | 否 | 效价参数 |
| `arousal` | f32 | 否 | 唤醒度参数 |

### 返回值 BatchReport

```rust
BatchReport {
    l1_nodes_created: u32,      // 创建的 L1 节点数
    l1_hyperedges_created: u32, // 创建的 L1 超边数
    l2_topics_created: u32,     // 创建的 L2 话题数
    l3_nodes_created: u32,      // 创建的 L3 节点数
    l4_docs_stored: u32,        // 存储的 L4 文档数
    chains_created: u32,        // 创建的超边链数
    total_duration_us: u64,     // 执行耗时（微秒）
    l1_dedup_skipped: u32,      // 去重跳过的 L1 节点数
    engram_ids: HashMap<String, String>,  // 输入序号 → L1 ID
    l3_engram_ids: HashMap<String, String>, // 输入序号 → L3 ID
}
```

### 示例

```rust
let batch = StoreBatch {
    items: vec![
        StoreItem {
            text: "今天天气很好".to_string(),
            source: "chat".to_string(),
            topic_label: Some("天气".to_string()),
            ..Default::default()
        },
        StoreItem {
            text: "下午去了公园".to_string(),
            source: "chat".to_string(),
            chain_parent_id: Some(prev_id),  // 链接到上一条
            chain_label: Some("supplement".to_string()),
            ..Default::default()
        },
    ],
};
let report = brain.batch_store(batch)?;
```

---

## recall — 语义检索

```rust
pub fn recall(&mut self, request: RecallRequest) -> Result<RecallResponse>
```

### RecallRequest 参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `query` | String | 否 | 搜索文本 |
| `max_results` | usize | 否 | 返回条数上限，默认 10 |
| `target_layers` | Vec<Layer> | 否 | 目标层，默认 `[L1, L2]` |
| `spread_depth` | usize | 否 | 关联扩散深度，0=不扩散 |
| `topic_filter` | String | 否 | 话题过滤关键词 |
| `exclude_ids` | Vec<String> | 否 | 排除的节点 ID |
| `exclude_topic_ids` | Vec<String> | 否 | 排除的话题 ID |
| `l3_domain_id` | String | 否 | 限定 L3 领域 |
| `l2_topic_id` | String | 否 | 限定 L2 话题 |
| `session_id` | String | 否 | 限定会话 |
| `time_decay_lambda` | f32 | 否 | 时间衰减系数 |
| `time_range` | (i64, i64) | 否 | 毫秒时间戳范围 |

### 返回值 RecallResponse

```rust
RecallResponse {
    results: Vec<RecallResult>,  // 检索结果
    total_count: usize,          // 结果总数
    l0_profile: Option<L0Profile>, // L0 角色画像
    confidence: Option<f32>,     // 置信度
    activated_topics: Vec<ActivatedTopicInfo>, // 激活的话题
}

RecallResult {
    layer: Layer,                // 来源层：L1/L2/L3/L4
    id: String,                  // 节点 ID
    text: String,                // 文本内容
    score: f32,                  // 相关性分数
    topic_label: Option<String>, // 话题标签
    created_at: i64,             // 创建时间戳（毫秒）
    version: u64,                // 版本号
}
```

### 示例

```rust
// 基本检索
let response = brain.recall(RecallRequest {
    query: "用户喜欢什么".to_string(),
    max_results: 5,
    ..Default::default()
})?;

// 带过滤条件
let response = brain.recall(RecallRequest {
    query: "Python".to_string(),
    target_layers: vec![Layer::L1, Layer::L4],
    topic_filter: Some("编程".to_string()),
    session_id: Some("session_1".to_string()),
    ..Default::default()
})?;

for result in response.results {
    println!("[{}] {} (score: {:.2})", result.layer, result.text, result.score);
}
```

---

## dream — 记忆巩固

```rust
pub fn dream(&mut self) -> Result<ConsolidateReport>
```

定期调用以整理记忆、合并相似话题、提取模式。

### 返回值 ConsolidateReport

```rust
ConsolidateReport {
    chains_consolidated: u32,   // 超边链合并数
    topics_merged: u32,         // 话题合并数
    topics_reflected: u32,      // 话题反思数
    duration_ms: u64,           // 执行耗时（毫秒）
    vitality_decayed: u32,      // 活力衰减数
    schemas_emerged: u32,       // 模式涌现数
    l0_updated: bool,           // L0 是否更新
    plans_consolidated: u32,    // 计划合并数
}
```

### 示例

```rust
let report = brain.dream()?;
println!("合并了 {} 个话题", report.topics_merged);
```

---

## L0 角色画像

```rust
// 设置画像
brain.set_l0(L0Profile {
    catid: Some("cat_001".to_string()),
    role_name: Some("小助手".to_string()),
    personality: vec!["友好".to_string(), "耐心".to_string()],
    values: vec!["用户至上".to_string()],
    ..Default::default()
})?;

// 获取画像
if let Some(profile) = brain.get_l0() {
    println!("角色: {:?}", profile.role_name);
}
```

### L0Profile 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `catid` | String | 不可修改的唯一标识符 |
| `role_name` | String | 可修改的名称 |
| `personality` | Vec<String> | 性格特征 |
| `values` | Vec<String> | 价值观 |
| `worldview` | Vec<String> | 世界观 |
| `role` | String | 角色类型 |
| `position` | String | 定位 |
| `traits` | HashMap<String, String> | 其他特征 |

---

## 知识库管理

```rust
// 挂载知识库
let meta = brain.mount_shelf(
    "/path/to/docs",
    "技术文档",
    "markdown"
)?;

// 列出已挂载
for shelf in brain.list_shelf() {
    println!("{}: {} ({} chunks)", shelf.id, shelf.path, shelf.chunk_count);
}

// 卸载
brain.unmount_shelf(&meta.id)?;
```

---

## 会话管理

```rust
// 激活话题（提升检索权重）
brain.activate("session_1", "topic_123", 3600000)?; // 1小时

// 获取激活的话题
let activated = brain.get_activated("session_1");

// 去激活
brain.deactivate("session_1", "topic_123")?;
```

---

## 编码器选择

### NgramEncoder（本地，无需外部服务）

```rust
let encoder: Arc<Box<dyn Encoder>> = Arc::new(Box::new(NgramEncoder::new(1024)));
```

### EncoderClient（远程，需要启动编码器服务）

```bash
# 启动编码器服务
memhop-encoder --socket /tmp/memhop-encoder.sock
```

```rust
use memhop_encoder_client::EncoderClient;

let encoder_client = EncoderClient::connect("/tmp/memhop-encoder.sock")?;
let encoder: Arc<Box<dyn Encoder>> = Arc::new(Box::new(encoder_client));
```

---

## 错误处理

```rust
use memhop_core::{MemHopError, Result};

match brain.recall(request) {
    Ok(response) => { /* 处理响应 */ }
    Err(MemHopError::Storage(e)) => { /* 存储错误，可重试 */ }
    Err(MemHopError::Encoding(e)) => { /* 编码错误 */ }
    Err(MemHopError::Validation(e)) => { /* 验证错误 */ }
    Err(e) => { /* 其他错误 */ }
}
```

---

## 最佳实践

1. **批量写入**：始终使用 `batch_store`，不要逐条写入
2. **话题标签**：为每条记忆提供 `topic_label`，提升检索质量
3. **定期巩固**：定期调用 `dream()` 进行记忆整理
4. **会话管理**：使用 `activate` 提升当前会话相关话题的权重
