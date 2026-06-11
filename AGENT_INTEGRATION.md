# MemHop SDK 集成指南

> MemHop v0.25.1 — 6 层仿人脑记忆引擎 SDK（三重检索 + L3 领域图 + Dream v2）

---

## 📦 分发方式

MemHop 提供两种分发形式:

### 1. Rust SDK (本文档)
- **适用**: Rust项目,需要完整功能
- **获取**: `cargo add memhop-core` 或 Git依赖
- **特点**: 50+ API,类型安全,IDE完美支持
- **源码**: BSL-1.1 License,可查看但生产需授权

### 2. Binary Crate (闭源分发)
- **适用**: meowagent等内部项目,保护源码
- **获取**: 私有Cargo registry
- **特点**: meowagent开发者看不到MemHop源码(只有.rlib)
- **配置**: 见 [docs/integration/QUICKSTART-CLOSED-SOURCE.md](docs/integration/QUICKSTART-CLOSED-SOURCE.md)

---

## 目录

- [1. 快速开始](#1-快速开始)
- [2. 依赖配置](#2-依赖配置)
- [3. 向量模型配置](#3-向量模型配置)
- [4. SDK 初始化](#4-sdk-初始化)
- [5. API 参考](#5-api-参考)
- [6. 完整示例](#6-完整示例)
- [7. 最佳实践](#7-最佳实践)
- [8. 故障排除](#8-故障排除)

---

## 1. 快速开始

### 最简示例（3 行代码）

```rust
use memhop_core::{MemHopSDK, MemHopConfig, StoreBatch, StoreItem, RecallRequest};

// 1. 初始化 SDK
MemHopSDK::init(MemHopConfig::default())?;

// 2. 创建 Brain
let mut brain = MemHopSDK::create_brain("./data", "my_agent")?;

// 3. 存储 + 检索
brain.batch_store(StoreBatch {
    items: vec![StoreItem { text: "Hello World".into(), ..Default::default() }]
})?;

let results = brain.recall(&RecallRequest { query: "Hello".into(), ..Default::default() })?;
```

---

## 2. 依赖配置

### 2.1 添加依赖

在你的 `Cargo.toml` 中添加：

```toml
[dependencies]
# 方式一：本地路径（开发时）
memhop-core = { path = "../memhop/memhop-core" }

# 方式二：Git 依赖（推荐）
memhop-core = { git = "https://github.com/your-org/memhop.git", branch = "main" }

# 方式三：启用向量模型（可选）
memhop-core = { git = "https://github.com/your-org/memhop.git", features = ["candle"] }
```

### 2.2 Feature Flags

| Feature | 说明 | 默认 |
|---------|------|------|
| `candle` | 启用 CandleEncoder 向量模型 | ❌ |
| `bench` | 基准测试支持 | ❌ |
| `bench-llm` | LLM 集成测试支持（依赖 bench） | ❌ |
| `llm-api` | LLM API 调用支持 | ❌ |

### 2.3 项目结构示例

```
your-project/
├── Cargo.toml
├── src/
│   └── main.rs
├── data/                    # Brain 数据目录（自动创建）
│   └── agent1/
│       └── agent_brain.db   # redb 单文件存储（包含所有层数据）
└── models/                  # 向量模型目录（可选）
    └── multilingual-e5-small/
        ├── config.json
        ├── tokenizer.json
        └── model.safetensors
```

---

## 3. 向量模型配置

### 3.1 模型说明

向量模型 `multilingual-e5-small` 是 MemHop 的**必选组件**，随项目一起分发。

| 模型 | 路径 | 说明 |
|------|------|------|
| multilingual-e5-small | `./models/multilingual-e5-small` | 384 维向量，支持中英文语义检索 |

### 3.2 模型路径

模型路径已在项目中预配置，位于 `./models/multilingual-e5-small`：

```
models/
└── multilingual-e5-small/
    ├── config.json
    ├── tokenizer.json
    └── model.safetensors
```

### 3.3 配置方式

```rust
// 方式一：代码中指定（推荐）
let config = MemHopConfig {
    model_path: Some("./models/multilingual-e5-small".to_string()),
    vector_dim: 384,
    ..Default::default()
};

// 方式二：环境变量
// export MEMHOP_MODEL_PATH=./models/multilingual-e5-small
let config = MemHopConfig::from_env();
```

**注意**: `model_path` 是必填项，必须指定向量模型路径才能使用完整的语义检索功能。

### 3.4 环境变量

| 变量 | 说明 | 示例 |
|------|------|------|
| `MEMHOP_MODEL_PATH` | 向量模型路径 | `./models/multilingual-e5-small` |

---

## 4. SDK 初始化

### 4.1 基本初始化

```rust
use memhop_core::{MemHopSDK, MemHopConfig};

// 使用默认配置（仅 NgramEncoder）
MemHopSDK::init(MemHopConfig::default())?;

// 使用向量模型
let config = MemHopConfig {
    model_path: Some("./models/multilingual-e5-small".to_string()),
    vector_dim: 384,
    ..Default::default()
};
MemHopSDK::init(config)?;
```

### 4.2 MemHopConfig 参数

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `model_path` | `Option<String>` | `None` | **必填** 向量模型路径 |
| `vector_dim` | `usize` | `384` | 向量维度 |

### 4.3 进程级单例

SDK 使用 `OnceLock` 实现全局单例，同一进程内所有 Brain 实例共享同一个编码器：

```rust
// 主进程初始化一次
MemHopSDK::init(config)?;

// 所有 Brain 实例共享编码器
let brain1 = MemHopSDK::create_brain("./data/cat1", "cat1")?;
let brain2 = MemHopSDK::create_brain("./data/cat2", "cat2")?;
// brain1 和 brain2 共享同一个编码器实例
```

### 4.4 检查初始化状态

```rust
if MemHopSDK::is_initialized() {
    println!("SDK 已初始化");
}

// 获取当前配置
if let Some(config) = MemHopSDK::get_config() {
    println!("模型路径: {:?}", config.model_path);
}
```

---

## 5. API 参考

### 5.1 MemHopSDK

| 方法 | 说明 |
|------|------|
| `MemHopSDK::init(config)` | 初始化 SDK（全局一次性） |
| `MemHopSDK::create_brain(dir, agent_id)` | 创建 Brain 实例 |
| `MemHopSDK::get_encoder()` | 获取全局编码器 |
| `MemHopSDK::is_initialized()` | 检查是否已初始化 |
| `MemHopSDK::get_config()` | 获取当前配置 |

### 5.2 Brain 核心 API

#### 创建实例

```rust
pub fn open(config: BrainConfig, encoder: Arc<Box<dyn Encoder>>) -> Result<Self>
pub fn set_e5_encoder(&mut self, encoder: Arc<Box<dyn Encoder>>)
```

| 参数 | 类型 | 说明 |
|------|------|------|
| `config` | BrainConfig | 包含 brains_dir 和 agent_id |
| `encoder` | Arc<Box<dyn Encoder>> | 编码器实例（Ngram/Candle） |

- `set_e5_encoder()`: 设置 E5 编码器（IPC 连接 memhop-encoder 服务），启用三重检索第三通道

#### 记忆存储

```rust
pub fn batch_store(&mut self, batch: StoreBatch) -> Result<BatchReport>
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `text` | String | ✅ | 原始文本 |
| `source` | String | ❌ | 来源，默认 `"chat"` |
| `topic_label` | String | 推荐 | 话题标签 |
| `llm_keywords` | Vec<String> | 推荐 | 关键词列表 |
| `llm_compressed_summary` | String | 推荐 | LLM 摘要 |
| `turn_id` | String | ❌ | 对话轮次 ID |
| `session_id` | String | ❌ | 会话 ID |
| `chain_parent_id` | String | ❌ | 超边链前驱 ID |
| `chain_label` | String | ❌ | 链标签 |
| `domain_id` | String | ❌ | 关联领域 ID |
| `importance` | f32 | ❌ | 重要度评分 (0.0-1.0)，默认 0.5 |
| `valence` | Option<f64> | ❌ | 效价参数 |
| `arousal` | Option<f64> | ❌ | 唤醒度参数 |

#### 记忆检索

```rust
pub fn recall(&mut self, req: &RecallRequest) -> Result<RecallResponse>
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `query` | String | ✅ | 搜索文本 |
| `max_results` | usize | ❌ | 返回条数上限，默认 10 |
| `target_layers` | Vec<Layer> | ❌ | 目标层，默认 `[L1, L2]` |
| `spread_depth` | usize | ❌ | 关联扩散深度 |
| `topic_filter` | String | ❌ | 话题过滤关键词 |
| `exclude_ids` | Vec<String> | ❌ | 排除的节点 ID |
| `exclude_topic_ids` | Vec<String> | ❌ | 排除的话题标签 |
| `l3_domain_id` | String | ❌ | 限定 L3 领域 |
| `l2_topic_id` | String | ❌ | 限定 L2 话题 |
| `session_id` | String | ❌ | 限定会话 |
| `time_decay_lambda` | f32 | ❌ | 时间衰减系数 |
| `active_l3_domains` | Option<Vec<String>> | ❌ | 激活的 L3 领域列表 |

**RecallResponse** 新增字段:

| 字段 | 类型 | 说明 |
|------|------|------|
| `prefetch` | Vec<PrefetchHint> | 预测性记忆提示（超边扩散） |
| `recommended_crystals` | Vec<ProceduralCrystal> | 匹配查询的程序性晶体推荐 |
| `confidence` | Option<f32> | 检索置信度 |
| `activated_topics` | Vec<ActivatedTopicInfo> | 当前激活话题列表 |

**PrefetchHint** 字段:

| 字段 | 类型 | 说明 |
|------|------|------|
| `node_id` | String | 预测的目标节点 ID |
| `text` | String | 文本（懒加载，协议层填充） |
| `prediction_score` | f32 | 预测评分 |
| `reason` | PrefetchReason | 预测原因 |

**PrefetchReason** 枚举:

- `HyperedgeSpread` — L1 超边扩散
- `SameTopicHighFreq` — 同话题高频节点
- `EmotionalResonance` — 情感共鸣
- `CrossDomainBridge` — 跨域桥接
- `TemporalProximity` — 时间邻近

#### 记忆巩固

```rust
pub fn consolidate(&mut self) -> Result<ConsolidateReport>
```

定期调用以整理记忆、合并相似话题、提取模式。

#### L0 角色画像

```rust
// 设置画像
pub fn set_l0_from_profile(&mut self, profile: &L0Profile) -> Result<()>
pub fn set_l0(
    &mut self,
    catid: Option<String>,
    role_name: Option<String>,
    personality: Vec<String>,
    values: Vec<String>,
    worldview: Vec<String>,
    traits: HashMap<String, String>,
) -> Result<()>

// 获取画像
pub fn get_l0_profile(&mut self) -> Result<Option<L0Profile>>
```

#### 知识库管理

```rust
// 文件型知识库挂载
pub fn mount_shelf(&mut self, dir_path, domain, domain_name) -> Result<ShelfMeta>
pub fn unmount_shelf(&mut self, domain_id) -> Result<()>
pub fn list_shelf(&mut self) -> Result<Vec<ShelfMeta>>

// 非文件型知识源挂载（API JSON、数据库导出、手动录入等）
pub fn mount_source(&mut self, input: MountSourceInput) -> Result<ShelfMeta>
```

**MountSourceInput 参数**:

| 参数 | 类型 | 说明 |
|------|------|------|
| `domain_name` | String | 知识域名称（必填） |
| `domain` | String | 文档类型（如 "api", "database"） |
| `kind` | SourceKind | 来源类型（ApiJson, DatabaseExport, ManualEntry 等） |
| `items` | Vec<MountSourceItem> | 知识条目列表（必填，非空） |

#### 会话管理

```rust
pub fn activate_topic(&mut self, session_id, topic_id, ttl_ms)
pub fn deactivate_topic(&mut self, session_id, topic_id)
pub fn get_activated(&self, session_id) -> Vec<String>
```

#### L3 结晶化

```rust
pub fn crystallize_l3(&mut self, req: &CrystallizeL3Request) -> Result<CrystallizeL3Report>
```

#### 情感系统

```rust
pub fn emotional_feedback(&mut self, feedback: &EmotionalFeedback) -> Result<()>
pub fn get_emotion(&mut self, memory_id) -> Result<EmotionalDimension>
pub fn recall_by_emotion(&mut self, req: &EmotionRecallRequest) -> Result<RecallResponse>
```

#### 程序性晶体

```rust
pub fn procedural_crystallize(&mut self) -> Result<CrystallizeReport>
pub fn store_crystal(&mut self, crystal: &ProceduralCrystal) -> Result<()>
pub fn get_crystal(&mut self, id: &str) -> Result<Option<ProceduralCrystal>>
pub fn list_crystals(&mut self) -> Result<Vec<ProceduralCrystal>>
pub fn get_crystals_by_keyword(&mut self, keyword: &str) -> Result<Vec<ProceduralCrystal>>
pub fn update_crystal_usage(&mut self, crystal_id: &str, success: bool) -> Result<()>
```

- `procedural_crystallize()`: 运行程序性结晶管线，从记忆链中提取模式
- `store_crystal()` / `get_crystal()` / `list_crystals()`: 程序性晶体 CRUD
- `get_crystals_by_keyword()`: 按关键词匹配晶体（`trigger_keywords` 子串匹配）
- `update_crystal_usage()`: 更新晶体使用反馈（EMA 更新 success_rate）

#### 话题管理

```rust
pub fn list_topics(&mut self) -> Result<Vec<Topic>>
pub fn get_topic(&mut self, topic_id: &str) -> Result<Option<Topic>>
pub fn update_topic(&mut self, topic_id, summary, keywords, extended_meta) -> Result<()>
```

#### L4 文档查询

```rust
pub fn get_l4_raw(&mut self, doc_id: &str) -> Result<Option<RawDocument>>
pub fn get_l4_by_session(&mut self, session_id: &str) -> Result<Vec<RawDocument>>
pub fn get_l4_by_topic(&mut self, topic_id: &str) -> Result<Vec<RawDocument>>
pub fn l4_doc_count(&self) -> usize
```

#### L3 领域查询

```rust
pub fn list_l3_paths(&mut self) -> Result<Vec<L3PathInfo>>
```

#### 记忆组织

```rust
pub fn organize_node(&mut self, node_id: &str) -> Result<()>
```

对指定节点执行话题反思和关键词精炼。

#### 再搜索

```rust
pub fn re_search(&mut self, req: &RecallRequest) -> Result<RecallResponse>
```

#### 配置与统计

```rust
pub fn config(&self) -> &BrainConfig
pub fn storage_stats(&self) -> Vec<StorageLayerInfo>
pub fn prewarm(&mut self, layers: &[String]) -> Result<HashMap<String, PrewarmLayerResult>>
```

#### 激活话题查询

```rust
pub fn get_activated_topics(&mut self) -> Vec<ActivatedTopicInfo>
```

---

### 5.3 新架构特性 (v0.25.1)

#### 三重检索 (Triple Retrieval)

MemHop v0.25.1 支持三重检索通道，提升语义匹配精度：

| 通道 | 索引 | 编码器 | 说明 |
|------|------|--------|------|
| 1. BM25 稀疏 | SparseIndexV2 | NgramEncoder | 关键词精确匹配 |
| 2. HNSW 稠密 | HNSW (usearch) | NgramEncoder | 语义向量近似检索 |
| 3. E5 稠密 | HNSW_E5 (usearch) | memhop-encoder (IPC) | 多语言语义检索 |

启用三重检索需要启动独立的 `memhop-encoder` 服务并通过 `set_e5_encoder()` 将 IPC 客户端注入 Brain。

```rust
// 启动编码器服务（独立进程）
// cargo run --bin memhop-encoder -- --socket /tmp/memhop-encoder.sock

// 客户端注入
use memhop_encoder_client::EncoderClient;
let e5_encoder = EncoderClient::connect("/tmp/memhop-encoder.sock")?;
brain.set_e5_encoder(Arc::new(Box::new(e5_encoder)));
```

#### 预测性预取 (Prefetch)

`recall()` 返回的 `RecallResponse` 包含 `prefetch` 字段，通过 L1 超边 BFS 扩散生成预测性记忆提示。三条检索路径均支持：

- `cascade_recall`（默认级联模式）
- `associative_recall`（关联扩散模式，`spread_depth > 0`）
- `recall_by_emotion`（情感检索）
- `re_search` / `execute`（多目标层检索）

#### L3 领域激活与策略

`RecallRequest.active_l3_domains` 支持选择性激活 L3 领域：
- `Some([id1, id2])` — 仅在这些 L3 中搜索
- `Some([])` — 跳过 L3 搜索
- `None` — 搜索所有已挂载 L3（默认）

**L3HyperedgeStrategy** 枚举控制知识库挂载时的超边构建策略：

| 策略 | 适用场景 | 行为 |
|------|---------|------|
| `Sequential` | 书籍/文档/通用 | Association + Evolution 超边 |
| `Structural` | 代码仓库 | 基于目录结构 + 引用关系 |
| `Citation` | 论文/文献 | 仅 Association 超边 |

挂载时自动根据 `ShelfDomain` 类型选择策略：

```rust
// Code 类型自动使用 Structural 策略
let meta = brain.mount_shelf("/path/to/src", ShelfDomain::Code, "my-code")?;

// Book 类型使用 Sequential 策略
let meta = brain.mount_shelf("/path/to/docs", ShelfDomain::Book, "my-docs")?;
```

#### Crystal 使用反馈

程序性晶体支持使用反馈闭环：

```rust
// 晶体被 Agent 成功采用后回调
brain.update_crystal_usage(&crystal.id, true)?;  // success=true

// 晶体被拒绝
brain.update_crystal_usage(&crystal.id, false)?;  // success=false
```

内部使用 EMA（指数移动平均，α=0.1）更新 `success_rate`，`cascade_recall` 自动在每次检索后调用 `update_crystal_usage`。

#### 记忆激活管理 (v0.23.0)

`ActivationManager` 实现三级记忆状态管理：Active / Latent / Dormant。

**激活分数计算**：
- 公式：`score = base_importance × exp(-λ × hours_since_last_access)`
- recall 命中后自动加权（默认 +0.3）

**状态转换阈值**：

| 转换 | 阈值 | 说明 |
|------|------|------|
| Active → Latent | 0.2 | 激活分数低于阈值 |
| Latent → Dormant | 0.05 | 激活分数低于阈值 |
| 高重要性保底 | 0.8 / 0.95 | importance 高于此值不降级 |

**配置示例**：

```rust
use memhop_core::ActivationConfig;

let config = ActivationConfig {
    active_capacity: 5000,    // 活跃态容量上限
    active_threshold: 0.2,    // Active → Latent 阈值
    dormant_threshold: 0.05,  // Latent → Dormant 阈值
    decay_lambda: 0.001,      // 衰减系数（约 29 天半衰期）
    recall_bonus: 0.3,        // recall 命中奖励
    ..Default::default()
};
```

#### 记忆再巩固 (v1.0)

`ReconsolidationManager` 实现记忆再巩固机制，在 recall 后更新记忆的元数据。

**触发时机**：每次 `recall()` 调用后自动触发。

**更新内容**：
- 更新 `last_accessed_at` 时间戳
- 计算新的激活分数
- 根据访问频率调整 importance

---

## 6. 完整示例

### 6.1 基本使用

```rust
use memhop_core::{
    MemHopSDK, MemHopConfig, Brain, StoreBatch, StoreItem,
    RecallRequest, Layer, L0Profile,
};

fn main() -> memhop_core::Result<()> {
    // 1. 初始化 SDK
    let config = MemHopConfig {
        model_path: Some("./models/multilingual-e5-small".to_string()),
        vector_dim: 384,
        ..Default::default()
    };
    MemHopSDK::init(config)?;

    // 2. 创建 Brain
    let mut brain = MemHopSDK::create_brain("./data/agent1", "agent1")?;

    // 3. 设置角色画像
    brain.set_l0_from_profile(&L0Profile {
        catid: Some("cat_001".to_string()),
        role_name: Some("小助手".to_string()),
        personality: vec!["友好".to_string(), "耐心".to_string()],
        ..Default::default()
    })?;

    // 4. 存储记忆
    let batch = StoreBatch {
        items: vec![
            StoreItem {
                text: "用户喜欢喝可乐".to_string(),
                source: "chat".to_string(),
                topic_label: Some("饮品偏好".to_string()),
                llm_keywords: Some(vec!["可乐".to_string(), "饮品".to_string()]),
                ..Default::default()
            },
            StoreItem {
                text: "用户今天心情很好".to_string(),
                source: "chat".to_string(),
                topic_label: Some("心情".to_string()),
                ..Default::default()
            },
        ],
    };
    let report = brain.batch_store(batch)?;
    println!("存储完成: L1={}, L2={}", report.l1_nodes_created, report.l2_topics_created);

    // 5. 检索记忆
    let response = brain.recall(&RecallRequest {
        query: "用户喜欢什么饮料".to_string(),
        max_results: 5,
        target_layers: vec![Layer::L1, Layer::L2],
        ..Default::default()
    })?;

    for result in &response.results {
        println!("[{}] {} (score: {:.2})", result.layer, result.text, result.score);
    }

    // 6. 记忆巩固
    let consolidate_report = brain.consolidate()?;
    println!("巩固完成: 合并 {} 个话题", consolidate_report.topics_merged);

    Ok(())
}
```

### 6.2 多 Agent 共享编码器

```rust
use memhop_core::{MemHopSDK, MemHopConfig};

fn main() -> memhop_core::Result<()> {
    // 初始化一次
    MemHopSDK::init(MemHopConfig {
        model_path: Some("./models/multilingual-e5-small".to_string()),
        ..Default::default()
    })?;

    // 创建多个 Brain（共享编码器）
    let mut cat1 = MemHopSDK::create_brain("./data/cat1", "cat1")?;
    let mut cat2 = MemHopSDK::create_brain("./data/cat2", "cat2")?;

    // 各自独立存储
    cat1.batch_store(StoreBatch {
        items: vec![StoreItem { text: "Cat1 的记忆".into(), ..Default::default() }]
    })?;

    cat2.batch_store(StoreBatch {
        items: vec![StoreItem { text: "Cat2 的记忆".into(), ..Default::default() }]
    })?;

    Ok(())
}
```

### 6.3 MemHopInstance（非全局实例）

用于测试或需要多配置的场景：

```rust
use memhop_core::{MemHopInstance, MemHopConfig};

// 创建独立实例（不影响全局 SDK）
let config = MemHopConfig {
    model_path: Some("./models/multilingual-e5-small".to_string()),
    vector_dim: 384,
    ..Default::default()
};
let instance = MemHopInstance::new(config)?;

// 使用实例创建 Brain
let mut brain = instance.create_brain("./data/test", "test-agent")?;

// 正常使用 Brain API
brain.batch_store(StoreBatch {
    items: vec![StoreItem { text: "测试记忆".into(), ..Default::default() }]
})?;
```

### 6.4 情感系统

```rust
use memhop_core::{EmotionalFeedback, Emotion, EmotionRecallRequest};

// 存储记忆
let report = brain.batch_store(StoreBatch {
    items: vec![StoreItem { text: "今天很开心".into(), ..Default::default() }]
})?;
let memory_id = report.engram_ids["0"].clone();

// 情感反馈
brain.emotional_feedback(&EmotionalFeedback {
    memory_id: memory_id.clone(),
    emotion: Emotion::Joy,
    intensity: 0.9,
    reason: Some("用户表达了快乐".to_string()),
})?;

// 按情感检索
let response = brain.recall_by_emotion(&EmotionRecallRequest {
    emotion: Some(Emotion::Joy),
    min_intensity: 0.5,
    max_results: 10,
    ..Default::default()
})?;
```

### 6.4 知识库挂载

```rust
// 挂载知识库
let meta = brain.mount_shelf(
    "/path/to/docs",
    "技术文档",
    "markdown"
)?;
println!("挂载成功: {} ({} chunks)", meta.id, meta.chunk_count);

// 列出已挂载
for shelf in brain.list_shelf()? {
    println!("{}: {}", shelf.id, shelf.path);
}

// 卸载
brain.unmount_shelf(&meta.id)?;
```

---

## 7. 最佳实践

### 7.1 初始化

- ✅ 在程序启动时调用 `MemHopSDK::init()` 一次
- ✅ 使用环境变量配置模型路径，便于部署
- ❌ 不要在循环中重复初始化

### 7.2 存储

- ✅ 始终使用 `batch_store` 批量写入
- ✅ 为每条记忆提供 `topic_label` 和 `llm_keywords`
- ❌ 不要逐条调用 `batch_store`

### 7.3 检索

- ✅ 使用 `target_layers` 限定检索范围
- ✅ 使用 `session_id` 隔离不同会话
- ✅ 定期调用 `consolidate()` 整理记忆

### 7.4 错误处理

```rust
use memhop_core::MemHopError;

match brain.recall(request) {
    Ok(response) => { /* 处理响应 */ }
    Err(MemHopError::Storage(e)) => { /* 存储错误，可重试 */ }
    Err(MemHopError::Encode(e)) => { /* 编码错误 */ }
    Err(MemHopError::InvalidArgument(e)) => { /* 验证错误，检查参数 */ }
    Err(MemHopError::NotFound(e)) => { /* 资源不存在 */ }
    Err(e) => { /* 其他错误 */ }
}
```

---

## 8. 故障排除

### 8.1 编译错误

**问题**: `candle` feature 未启用
```
error[E0433]: unresolved import `memhop_core::CandleEncoder`
```

**解决**: 在 `Cargo.toml` 中启用 feature
```toml
memhop-core = { ..., features = ["candle"] }
```

### 8.2 运行时错误

**问题**: 模型加载失败
```
[MemHopSDK] Failed to load CandleEncoder: No such file or directory
```

**解决**: 检查模型路径是否正确
```bash
ls -la ./models/multilingual-e5-small/
# 应包含 config.json, tokenizer.json, model.safetensors
```

**问题**: SDK 未初始化
```
MemHopSDK not initialized. Call MemHopSDK::init() first.
```

**解决**: 在创建 Brain 前调用 `MemHopSDK::init()`

### 8.3 性能问题

**问题**: 首次检索慢
```
[memhop] WARNING: L1 first open took 500ms (10000 nodes)
```

**解决**: 使用 `prewarm()` 预热
```rust
brain.prewarm(&["L1".to_string(), "L2".to_string()])?;
```

### 8.4 内存问题

**问题**: 内存占用过高

**解决**: 
1. 不使用向量模型时，设置 `model_path: None`
2. 使用 `consolidate()` 定期整理记忆
3. 监控 `storage_stats()` 检查数据量

---

## 附录

### A. 完整配置示例

```rust
let config = MemHopConfig {
    model_path: Some("./models/multilingual-e5-small".to_string()),
    vector_dim: 384,
};
```

### B. 环境变量参考

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `MEMHOP_MODEL_PATH` | 向量模型路径 | 无 |

### C. 错误码参考

| 错误类型 | 说明 | 处理建议 |
|---------|------|---------|
| `Storage` | 存储层错误 | 检查磁盘空间和权限 |
| `Encode` | 编码错误 | 检查模型文件完整性 |
| `InvalidArgument` | 参数验证失败 | 检查输入参数范围 |
| `NotFound` | 资源不存在 | 检查 ID 是否正确 |
| `Internal` | 内部错误 | 联系开发者 |
