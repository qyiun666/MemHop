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
let response = brain.recall(&RecallRequest {
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
| `brain.recall(&request)` | 语义检索 | 基于查询检索相关记忆（引用传递） |
| `brain.consolidate()` | 记忆巩固 | 触发记忆整理和模式提取 |
| `brain.set_l0(catid, role_name, personality, values, worldview, traits)` | 设置画像 | 设置 Agent 角色画像（逐字段） |
| `brain.set_l0_from_profile(profile)` | 设置画像(结构体) | 通过 L0Profile 结构体设置画像 |
| `brain.set_l0_profile(catid, role_name, role, position, traits)` | 设置画像(局部更新) | 旧版 API，仅更新身份字段 |
| `brain.get_l0_profile()` | 获取画像 | 获取当前角色画像 |
| `brain.mount_shelf(dir_path, domain, domain_name)` | 挂载知识库 | 导入外部文档 |
| `brain.unmount_shelf(domain_id)` | 卸载知识库 | 移除已挂载的知识库 |
| `brain.list_shelf()` | 列出知识库 | 列出所有已挂载知识库 |
| `brain.activate_topic(session_id, topic_id, ttl_ms)` | 激活话题 | 提升话题在会话中的权重 |
| `brain.deactivate_topic(session_id, topic_id)` | 去激活话题 | 移除话题激活状态 |
| `brain.get_activated(session_id)` | 获取激活列表 | 获取指定会话的激活话题 ID 列表（返回 `Vec<String>`） |
| `brain.procedural_crystallize()` | 程序性结晶 | 从超边链中提取可复用模式 |
| `brain.list_crystals()` | 列出晶体 | 列出所有程序性晶体 |
| `brain.get_crystal(id)` | 获取晶体 | 获取单个晶体详情 |
| `brain.re_search(&req)` | 再搜索 | 带排除过滤的再搜索 |
| `brain.prewarm(&layers)` | 预热层 | 预热指定层，重建索引 |
| `brain.update_topic(topic_id, summary, keywords, extended_meta)` | 更新话题 | 更新话题元数据 |
| `brain.get_topic(id)` | 获取话题 | 获取话题详情 |
| `brain.organize_node(id)` | 组织节点 | 组织记忆节点 |
| `brain.storage_stats()` | 存储统计 | 获取各层存储使用率统计 |
| `brain.list_l3_paths()` | 列出 L3 路径 | 列出 L3 领域路径 |
| `brain.get_l4_raw(id)` | 获取 L4 原文 | 获取原始 L4 文档 |
| `brain.crystallize_l3(&req)` | L3 结晶化 | 从 L2 话题提炼知识写入 L3 知识超图 |
| `brain.emotional_feedback(&feedback)` | 情感反馈 | 根据情感类型调节记忆重要性 |
| `brain.get_emotion(memory_id)` | 获取情感 | 获取记忆的情感维度 |
| `brain.recall_by_emotion(&req)` | 情感检索 | 按情感类型检索记忆 |

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
| `valence` | Option<f64> | 否 | 效价参数 |
| `arousal` | Option<f64> | 否 | 唤醒度参数 |

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
pub fn recall(&mut self, req: &RecallRequest) -> Result<RecallResponse>
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
    recommended_crystals: Vec<ProceduralCrystal>, // 程序性晶体推荐
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
let response = brain.recall(&RecallRequest {
    query: "用户喜欢什么".to_string(),
    max_results: 5,
    ..Default::default()
})?;

// 带过滤条件
let response = brain.recall(&RecallRequest {
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

## consolidate — 记忆巩固

```rust
pub fn consolidate(&mut self) -> Result<ConsolidateReport>
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
    crystals_created: u32,      // 程序性结晶生成数
}
```

### 示例

```rust
let report = brain.consolidate()?;
println!("合并了 {} 个话题", report.topics_merged);
```

---

## L0 角色画像

```rust
// 设置画像（通过结构体）
brain.set_l0_from_profile(L0Profile {
    catid: Some("cat_001".to_string()),
    role_name: Some("小助手".to_string()),
    personality: vec!["友好".to_string(), "耐心".to_string()],
    values: vec!["用户至上".to_string()],
    ..Default::default()
})?;

// 获取画像
if let Some(profile) = brain.get_l0_profile() {
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
brain.activate_topic("session_1", "topic_123", 3600000)?; // 1小时

// 获取激活的话题
let activated = brain.get_activated("session_1");

// 去激活
brain.deactivate_topic("session_1", "topic_123")?;
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
3. **定期巩固**：定期调用 `consolidate()` 进行记忆整理
4. **会话管理**：使用 `activate_topic` 提升当前会话相关话题的权重

---

## crystallize_l3 — L3 结晶化

```rust
pub fn crystallize_l3(&mut self, req: &CrystallizeL3Request) -> Result<CrystallizeL3Report>
```

从 L2 话题提炼知识写入 L3 知识超图。meowAgent 使用 LLM 生成 summary 和 keywords，MemHop 负责创建 L3 domain 并更新 L2→L3 link。

### CrystallizeL3Request 参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `topic_id` | String | 是 | L2 话题 ID |
| `summary` | String | 是 | LLM 生成的知识摘要 |
| `keywords` | Vec<String> | 是 | LLM 生成的关键词列表 |
| `domain_name` | Option<String> | 否 | L3 domain 名称，默认使用 topic label |

### 返回值 CrystallizeL3Report

```rust
CrystallizeL3Report {
    domain_id: String,        // 创建的 L3 domain ID
    domain_name: String,      // domain 名称
    l3_nodes_created: u32,    // 创建的 L3 节点数
    topic_linked: bool,       // 是否成功链接 L2→L3
}
```

### 示例

```rust
use memhop_core::CrystallizeL3Request;

let report = brain.crystallize_l3(&CrystallizeL3Request {
    topic_id: "topic_123".to_string(),
    summary: "用户喜欢喝可乐，特别是百事可乐".to_string(),
    keywords: vec!["可乐".to_string(), "百事".to_string(), "饮品".to_string()],
    domain_name: Some("饮品偏好".to_string()),
})?;

println!("创建了 {} 个 L3 节点", report.l3_nodes_created);
```

### 调用时机

meowAgent 在以下场景调用 `crystallize_l3`：
1. L2 话题积累了足够的记忆（>5 条）
2. LLM 判断话题具有长期价值
3. 用户主动请求知识结晶

---

## 情感维度系统

### Emotion 枚举

```rust
pub enum Emotion {
    Joy,        // 快乐
    Sadness,    // 悲伤
    Anger,      // 愤怒
    Fear,       // 恐惧
    Surprise,   // 惊讶
    Disgust,    // 厌恶
    Neutral,    // 中性（无显著情感）
}
```

### emotional_feedback — 情感反馈

```rust
pub fn emotional_feedback(&mut self, feedback: &EmotionalFeedback) -> Result<()>
```

根据用户情感调节记忆权重。正面情感增强记忆保留，负面情感根据类型有不同影响。

#### EmotionalFeedback 参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `memory_id` | String | 是 | L1 节点 ID |
| `emotion` | Emotion | 是 | 情感类型 |
| `intensity` | f32 | 是 | 情感强度 (0.0-1.0) |
| `reason` | Option<String> | 否 | 情感原因 |

#### 重要性调整规则

| 情感类型 | 调整公式 | 说明 |
|---------|---------|------|
| Joy | importance += intensity * 0.15 | 快乐增强记忆 |
| Sadness | importance += intensity * 0.10 | 悲伤也增强记忆（负面情绪深刻） |
| Anger | importance += intensity * 0.05 | 愤怒轻微增强 |
| Fear | importance += intensity * 0.12 | 恐惧显著增强 |
| Surprise | importance += intensity * 0.08 | 惊讶轻微增强 |
| Disgust | importance -= intensity * 0.10 | 厌恶降低重要性 |

#### 示例

```rust
use memhop_core::{EmotionalFeedback, Emotion};

brain.emotional_feedback(&EmotionalFeedback {
    memory_id: "kn_123".to_string(),
    emotion: Emotion::Joy,
    intensity: 0.8,
    reason: Some("用户很开心".to_string()),
})?;
```

### get_emotion — 获取情感维度

```rust
pub fn get_emotion(&mut self, memory_id: &str) -> Result<EmotionalDimension>
```

获取单条记忆的情感维度。

#### 返回值 EmotionalDimension

```rust
EmotionalDimension {
    emotion: Emotion,      // 情感类型
    intensity: f32,        // 情感强度 (0.0-1.0)
    valence: f32,          // 效价 (-1.0 ~ 1.0)
    arousal: f32,          // 唤醒度 (0.0 ~ 1.0)
}
```

#### 示例

```rust
let emotion = brain.get_emotion("kn_123")?;
println!("情感: {:?}, 强度: {:.2}", emotion.emotion, emotion.intensity);
```

### recall_by_emotion — 按情感检索

```rust
pub fn recall_by_emotion(&mut self, req: &EmotionRecallRequest) -> Result<RecallResponse>
```

按情感类型检索记忆。

#### EmotionRecallRequest 参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `emotion` | Option<Emotion> | 否 | 按情感类型过滤，None 表示不过滤 |
| `min_intensity` | f32 | 否 | 最低情感强度，默认 0.0 |
| `time_decay_lambda` | Option<f32> | 否 | 时间衰减系数 |
| `max_results` | usize | 否 | 返回条数上限，默认 10 |

#### 示例

```rust
use memhop_core::{EmotionRecallRequest, Emotion};

// 检索所有快乐记忆
let response = brain.recall_by_emotion(&EmotionRecallRequest {
    emotion: Some(Emotion::Joy),
    min_intensity: 0.5,
    max_results: 10,
    ..Default::default()
})?;

for result in response.results {
    println!("{} (score: {:.2})", result.text, result.score);
}
```

---

## meowAgent 适配清单

| Stage | 归属 | MemHop API | meowAgent 负责 |
|-------|------|-----------|---------------|
| RecallStage | 混合 | `brain.recall(req)` 结果含 emotion 字段 | LLM query expansion、amygdala 情绪标注 |
| ExpressStage | MemHop | `brain.batch_store(batch)` 存储 valence/arousal | 格式化对话文本、LLM 生成 topic_label/llm_keywords/llm_compressed_summary |
| ReflectStage | MemHop | `brain.organize_node()` `brain.update_topic()` `brain.set_l0()` `brain.emotional_feedback()` | LLM 生成 summary/keywords/meta、LLM 检测情感 → 调用 emotional_feedback |
| CrystallizeStage | 混合 | `brain.crystallize_l3(req)` `brain.procedural_crystallize()` | LLM 生成 L3 摘要、决策何时结晶 |
| ThalamusStage | meowAgent | 无 | LLM query rewrite、**LLM 情感检测**（输出 Emotion+intensity → 传给 Express/Reflect） |
| PFC Stage | MemHop | `brain.activate_topic()` `brain.deactivate_topic()` | LLM 决策哪些 topic 该激活 |
| Dream/Consolidate | MemHop | `brain.consolidate()` | 无（完全由 MemHop 处理） |
| **情感检索** | MemHop | `brain.recall_by_emotion(req)` `brain.get_emotion(id)` | meowAgent 决策何时需要按情感检索 |
