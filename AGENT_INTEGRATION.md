# MemHop v0.23.0 — meowAgent 集成指南

## 架构概览

```
┌─────────────────────────────────────────────────────────────┐
│                      meowAgent (Rust 进程)                   │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │              memhop-core (核心记忆引擎)                │   │
│  │  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐ │   │
│  │  │  L0     │  │  L1     │  │  L2     │  │  L3     │ │   │
│  │  │ 角色画像 │  │ 纠缠超图 │  │ 话题图  │  │ 领域图  │ │   │
│  │  └─────────┘  └─────────┘  └─────────┘  └─────────┘ │   │
│  │  ┌─────────┐  ┌─────────┐  ┌─────────┐             │   │
│  │  │  L4     │  │  L5     │  │ Sparse  │             │   │
│  │  │ 原文库  │  │ 晶体库  │  │ Index   │             │   │
│  │  └─────────┘  └─────────┘  └─────────┘             │   │
│  └─────────────────────────────────────────────────────┘   │
│                          │                                   │
│                          ▼                                   │
│  ┌─────────────────────────────────────────────────────┐   │
│  │           memhop-encoder-client (IPC 客户端)          │   │
│  └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                          │
                          │ Unix Domain Socket + bincode
                          ▼
┌─────────────────────────────────────────────────────────────┐
│              memhop-encoder (独立编码器服务)                   │
│              设备级共享，所有 Agent 共用                        │
└─────────────────────────────────────────────────────────────┘
```

---

## 快速开始

### 1. 添加依赖

```toml
[dependencies]
memhop-core = "0.23"
memhop-encoder-client = "0.23"
```

### 2. 基本使用

```rust
use memhop_core::{Brain, BrainConfig, Encoder, NgramEncoder, RecallRequest, StoreBatch, StoreItem};
use memhop_encoder_client::EncoderClient;
use std::sync::Arc;

// 1. 创建编码器（本地 NgramEncoder 或远程 EncoderClient）
let encoder: Arc<Box<dyn Encoder>> = Arc::new(Box::new(NgramEncoder::new(1024)));

// 2. 创建 Brain 实例
let config = BrainConfig {
    brains_dir: "./memhop_brains".to_string(),
    agent_id: "my_agent".to_string(),
};
let mut brain = Brain::open(config, encoder)?;

// 3. 批量写入记忆
let batch = StoreBatch {
    items: vec![
        StoreItem {
            text: "用户喜欢喝可乐".to_string(),
            source: "chat".to_string(),
            turn_id: Some("turn_1".to_string()),
            session_id: Some("session_1".to_string()),
            topic_label: Some("饮品偏好".to_string()),
            llm_keywords: Some(vec!["可乐".to_string(), "偏好".to_string()]),
            llm_compressed_summary: Some("用户告知她喜欢可乐".to_string()),
            ..Default::default()
        },
    ],
};
let report = brain.batch_store(batch)?;
println!("存储完成: {:?}", report);

// 4. 检索记忆
let request = RecallRequest {
    query: "用户喜欢什么饮料".to_string(),
    max_results: 10,
    target_layers: vec![Layer::L1, Layer::L2],
    ..Default::default()
};
let response = brain.recall(request)?;
println!("检索到 {} 条结果", response.results.len());
```

### 3. 使用远程编码器

```rust
use memhop_encoder_client::EncoderClient;

// 连接到共享的编码器服务
let encoder_client = EncoderClient::connect("/tmp/memhop-encoder.sock")?;

// 创建使用远程编码器的 Brain
let encoder: Arc<Box<dyn Encoder>> = Arc::new(Box::new(encoder_client));
let brain = Brain::open(config, encoder)?;
```

---

## 核心 API

### Brain::open

创建或打开 Brain 实例。

```rust
pub fn open(config: BrainConfig, encoder: Arc<Box<dyn Encoder>>) -> Result<Self>
```

| 参数 | 类型 | 说明 |
|------|------|------|
| `config.brains_dir` | String | 数据存储目录 |
| `config.agent_id` | String | Agent 标识 |
| `encoder` | Arc<Box<dyn Encoder>> | 编码器实例 |

---

### Brain::batch_store

批量写入记忆。**所有写入都通过此接口**。

```rust
pub fn batch_store(&mut self, batch: StoreBatch) -> Result<BatchReport>
```

#### StoreItem 字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `text` | String | **是** | 原始文本 |
| `source` | String | 否 | 来源，默认 `"chat"` |
| `turn_id` | Option<String> | 否 | 对话轮次 ID |
| `session_id` | Option<String> | 否 | 会话 ID |
| `topic_label` | Option<String> | 推荐 | 话题标签 |
| `llm_keywords` | Option<Vec<String>> | 推荐 | 关键词 |
| `llm_compressed_summary` | Option<String> | 推荐 | 摘要 |
| `chain_parent_id` | Option<String> | 否 | 超边链前驱 ID |
| `chain_label` | Option<String> | 否 | 链标签：`correction`/`supplement`/`merge` |
| `domain_id` | Option<String> | 否 | 关联领域 ID |
| `importance` | Option<f32> | 否 | 重要性权重 |
| `valence` | Option<f32> | 否 | 效价参数（情感维度） |
| `arousal` | Option<f32> | 否 | 唤醒度参数（情感维度） |

#### BatchReport 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `l1_nodes_created` | u32 | 创建的 L1 节点数 |
| `l1_hyperedges_created` | u32 | 创建的 L1 超边数 |
| `l2_topics_created` | u32 | 创建的 L2 话题数 |
| `l3_nodes_created` | u32 | 创建的 L3 节点数 |
| `l4_docs_stored` | u32 | 存储的 L4 文档数 |
| `chains_created` | u32 | 创建的超边链数 |
| `total_duration_us` | u64 | 执行耗时（微秒） |
| `l1_dedup_skipped` | u32 | 去重跳过的 L1 节点数 |
| `engram_ids` | HashMap<usize, String> | 输入序号 → L1 节点 ID 映射 |
| `l3_engram_ids` | HashMap<usize, String> | 输入序号 → L3 节点 ID 映射 |

---

### Brain::recall

语义检索记忆。

```rust
pub fn recall(&mut self, request: RecallRequest) -> Result<RecallResponse>
```

#### RecallRequest 字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `query` | String | 否 | 搜索文本，为空返回空结果 |
| `max_results` | usize | 否 | 返回条数上限，默认 10 |
| `target_layers` | Vec<Layer> | 否 | 目标层，默认 `[L1, L2]` |
| `spread_depth` | usize | 否 | 关联扩散深度，0=不扩散 |
| `topic_filter` | Option<String> | 否 | 话题过滤关键词 |
| `exclude_ids` | Vec<String> | 否 | 排除的节点/文档 ID |
| `exclude_topic_ids` | Vec<String> | 否 | 排除的话题 ID |
| `l3_domain_id` | Option<String> | 否 | 限定 L3 领域 |
| `l2_topic_id` | Option<String> | 否 | 限定 L2 话题 |
| `session_id` | Option<String> | 否 | 限定会话 |
| `time_decay_lambda` | f32 | 否 | 时间衰减系数 |
| `time_range` | Option<(i64, i64)> | 否 | 毫秒时间戳范围 `(start, end)` |

#### RecallResponse 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `results` | Vec<RecallResult> | 检索结果列表 |
| `total_count` | usize | 结果总数 |
| `l0_profile` | Option<L0Profile> | L0 角色画像（可选） |
| `confidence` | Option<f32> | 置信度（可选） |
| `activated_topics` | Vec<ActivatedTopicInfo> | 激活的话题列表 |

#### RecallResult 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `layer` | Layer | 来源层 |
| `id` | String | 节点/文档 ID |
| `text` | String | 文本内容 |
| `score` | f32 | 相关性分数 |
| `topic_label` | Option<String> | 话题标签 |
| `created_at` | i64 | 创建时间戳（毫秒） |
| `version` | u64 | 版本号 |

---

### Brain::dream

触发记忆巩固（梦境模拟）。

```rust
pub fn dream(&mut self) -> Result<ConsolidateReport>
```

#### ConsolidateReport 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `chains_consolidated` | u32 | 超边链合并数 |
| `topics_merged` | u32 | 话题合并数 |
| `topics_reflected` | u32 | 话题反思数 |
| `duration_ms` | u64 | 执行耗时（毫秒） |
| `vitality_decayed` | u32 | 活力衰减数 |
| `schemas_emerged` | u32 | 模式涌现数 |
| `l0_updated` | bool | L0 是否更新 |
| `plans_consolidated` | u32 | 计划合并数 |

---

### Brain::mount_shelf / unmount_shelf / list_shelf

知识库管理接口。

```rust
// 挂载知识库
pub fn mount_shelf(&mut self, path: &str, name: &str, doc_type: &str) -> Result<ShelfMeta>

// 卸载知识库
pub fn unmount_shelf(&mut self, domain_id: &str) -> Result<()>

// 列出已挂载知识库
pub fn list_shelf(&self) -> Vec<ShelfMeta>
```

#### ShelfMeta 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | String | 领域 ID |
| `path` | String | 挂载路径 |
| `doc_type` | String | 文档类型 |
| `chunk_count` | usize | 分块数量 |
| `mounted_at` | i64 | 挂载时间戳（毫秒） |

---

### Brain::set_l0 / get_l0

角色画像管理。

```rust
// 设置 L0 角色画像
pub fn set_l0(&mut self, profile: L0Profile) -> Result<()>

// 获取 L0 角色画像
pub fn get_l0(&self) -> Option<L0Profile>
```

#### L0Profile 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `catid` | Option<String> | 不可修改的唯一标识符 |
| `role_name` | Option<String> | 可修改的名称 |
| `personality` | Vec<String> | 性格特征列表 |
| `values` | Vec<String> | 价值观列表 |
| `worldview` | Vec<String> | 世界观列表 |
| `role` | Option<String> | 角色类型 |
| `position` | Option<String> | 定位 |
| `traits` | HashMap<String, String> | 其他特征键值对 |

---

### Brain::activate / deactivate / get_activated / feedback

会话管理接口。

```rust
// 激活话题
pub fn activate(&mut self, session_id: &str, topic_id: &str, ttl_ms: i64) -> Result<()>

// 去激活话题
pub fn deactivate(&mut self, session_id: &str, topic_id: &str) -> Result<()>

// 获取当前激活的话题列表
pub fn get_activated(&self, session_id: &str) -> Vec<ActivationEntry>

// 检索结果反馈
pub fn feedback(&mut self, session_id: &str, result_ids: &[&str], relevant: bool) -> Result<()>
```

#### ActivationEntry 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `topic_id` | String | 话题 ID |
| `activated_at` | i64 | 激活时间戳（毫秒） |
| `ttl_ms` | i64 | 激活有效期（毫秒） |
| `last_hit_at` | i64 | 最后命中时间戳（毫秒） |

---

### Brain::crystallize

触发程序性结晶。

```rust
pub fn crystallize(&mut self) -> Result<CrystallizeReport>
```

#### CrystallizeReport 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `crystals_created` | u32 | 本次生成的晶体数 |
| `chains_analyzed` | u32 | 分析的链总数 |
| `duration_ms` | u64 | 执行耗时（毫秒） |

---

## 记忆激活系统 (v0.23.0)

v0.23.0 引入了三级记忆激活状态：

| 状态 | 说明 | 检索权重 |
|------|------|--------|
| **Active** | 当前活跃记忆 | 1.0 |
| **Latent** | 潜伏记忆（近期使用） | 0.5-0.8 |
| **Dormant** | 休眠记忆（长期未用） | 0.1-0.3 |

激活分数计算：
- 指数衰减：`score = base_score * exp(-lambda * age_hours)`
- Recall 奖励：每次被检索命中，分数增加 0.1

---

## HNSW 向量索引 (v0.23.0)

v0.23.0 将 HNSW 向量索引从 usearch (C++) 替换为 fast-hnsw (纯 Rust)，实现：

- **零 C++ 依赖**：编译无需 C++ 工具链
- **跨平台兼容**：Windows/mac/Linux 开箱即用
- **性能保持**：O(log N) 近似搜索，支持 Cosine/L2/InnerProduct 度量
- **F16 量化**：内存减半，精度损失可忽略

### 技术细节

| 特性 | 说明 |
|------|------|
| 最大节点数 | 100,000 (默认) |
| 连接数 (M) | 16-32 (根据数据规模自适应) |
| 构建扩展 | 128-512 |
| 搜索扩展 | 64-256 |
| 序列化 | bincode，支持增量保存 |

### 配置

```rust
use memhop_core::MemHopHnswConfig;

// 自动根据数据规模调整配置
let config = MemHopHnswConfig::for_scale(node_count);

// 或手动指定
let config = MemHopHnswConfig {
    connectivity: 16,
    expansion_add: 128,
    expansion_search: 64,
};
```

---

## 编码器配置

### 本地 NgramEncoder

适用于：
- 快速启动，无需外部服务
- 纯文本 BM25 检索
- 开发和测试环境

```rust
let encoder = NgramEncoder::new(1024);
```

### 远程 EncoderClient

适用于：
- 生产环境，设备级共享编码器
- 需要高质量语义向量（BERT 模型）
- 多 Agent 共享同一编码器服务

```bash
# 启动编码器服务
memhop-encoder --socket /tmp/memhop-encoder.sock
```

```rust
let encoder_client = EncoderClient::connect("/tmp/memhop-encoder.sock")?;
let encoder: Arc<Box<dyn Encoder>> = Arc::new(Box::new(encoder_client));
```

---

## 错误处理

```rust
use memhop_core::{MemHopError, Result};

match brain.recall(request) {
    Ok(response) => { /* 处理响应 */ }
    Err(MemHopError::Storage(e)) => { /* 存储错误 */ }
    Err(MemHopError::Encoding(e)) => { /* 编码错误 */ }
    Err(MemHopError::Validation(e)) => { /* 验证错误 */ }
    Err(e) => { /* 其他错误 */ }
}
```

---

## 最佳实践

1. **批量写入**：始终使用 `batch_store`，不要逐条写入
2. **话题标签**：为每条记忆提供 `topic_label`，提升检索质量
3. **定期巩固**：定期调用 `dream()` 进行记忆巩固
4. **会话管理**：使用 `activate` 提升当前会话相关话题的权重
5. **错误重试**：存储失败时，建议重试 3 次，间隔 100ms

---

## 版本历史

- **v0.23.0** (2026-06-08)
  - 架构重构：memhop-mcp-server 移除，MemHop 完全嵌入 meowAgent
  - 新增记忆激活系统（Active/Latent/Dormant）
  - SparseIndexV2：forward 索引从内存移到 LMDB
  - 编码器 IPC 化：Unix Domain Socket + bincode 协议
  - **HNSW 索引优化**：usearch 替换为 fast-hnsw（纯 Rust，无 C++ 依赖，跨平台部署更简单，支持 stable Rust）

- **v0.20.0** (2026-05-30)
  - 新增 `memhop_set_l0` 完整版接口
  - 新增 `memhop_get_topic` 查询单个话题
  - 新增存储使用率统计

- **v0.19.0** (2026-05-28)
  - 新增 LRU 缓存管理
  - 新增存储使用率监控
