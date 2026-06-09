<!-- badges -->
<p align="center">
  <a href="https://github.com/qyiun666/memhop/actions"><img src="https://img.shields.io/github/actions/workflow/status/qyiun666/memhop/ci.yml?branch=main&style=flat-square" alt="CI"></a>
  <a href="https://crates.io/crates/memhop-core"><img src="https://img.shields.io/crates/v/memhop-core?style=flat-square" alt="crates.io"></a>
  <a href="https://docs.rs/memhop-core"><img src="https://img.shields.io/docsrs/memhop-core?style=flat-square" alt="docs.rs"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT%20%2F%20Apache--2.0-blue?style=flat-square" alt="License"></a>
</p>

<!-- navigation -->
<p align="center">
  <a href="https://qyiun666.github.io/meowagent.github.io/">官网</a>
  ·
  <a href="#quick-start">Quick Start</a>
  ·
  <a href="https://docs.rs/memhop-core">API 文档</a>
  ·
  <a href="#benchmarks">Benchmarks</a>
  ·
  <a href="https://github.com/qyiun666/MeowAgent">MeowAgent</a>
  ·
  <a href="#contributing">Contributing</a>
</p>

---

<h1 align="center">MemHop</h1>

<p align="center">
  <strong>脑启发记忆引擎 SDK</strong><br>
  Hopfield 模式补全 · HNSW 语义检索 · Hebbian 图学习<br>
  为 AI Agent 打造的单进程嵌入式记忆底座
</p>

---

## 特性

**检索引擎**

- **O(1) 单条精准召回** — 非 Top-K 近似搜索，仿人脑瞬时回忆，基于 Hopfield 网络模式补全
- **BM25 + HNSW 双通道** — 稀疏检索（ngram 倒排索引 + BM25）始终可用，稠密向量检索（usearch HNSW）可选增强
- **可插拔双编码器** — 默认 NgramEncoder 零模型依赖；启用 `candle` feature 后加载 multilingual-e5-small 语义向量，EncoderRouter 自动路由稀疏/稠密双通道

**6 层记忆模型**

- **L0 角色画像** — Agent 人格与偏好持久化
- **L1 纠缠超图** — 核心记忆层，KnowledgeNode + Hyperedge + 超边链，Hebbian 动态边权学习
- **L2 话题图** — 话题聚类与关联发现
- **L3 领域超图** — 跨话题知识蒸馏，支持 L3 结晶化（`crystallize_l3`）
- **L4 原文库** — 原始对话/文档存档
- **L5 程序性晶体** — 链分析引擎，从历史记忆中提炼可复用操作流程

**记忆生命周期**

- **记忆激活系统** — Active / Latent / Dormant 三态管理，Active 子集驻留 HNSW（≤5000 节点），衰减公式 `score = importance × exp(-λt) + recall_bonus`
- **记忆巩固（Dream）** — 后台 consolidation 管线，自动执行话题反思、关键词精炼、边界检测
- **情感索引** — 多维度情感反馈（`emotional_feedback`），支持情感驱动召回（`recall_by_emotion`）
- **知识库挂载（Shelf）** — 通过 `mount_shelf` 将外部知识图谱注入 L3 层

**工程特性**

- **LMDB 持久化，零配置** — 每层独立 LMDB 环境，事务安全，无需外部数据库
- **HNSW 索引持久化** — usearch 原生序列化，启动时优先从缓存加载，避免全量重建
- **独立编码器服务** — memhop-encoder 独立进程，多 Agent 共享一个向量模型实例，通过 IPC 通信
- **全局编码器共享** — MemHopSDK 单例模式，同进程内多个 Brain 共享编码器，节省内存

## vs agentmemory

| 维度 | agentmemory | MemHop v0.25.0 |
|------|------------|----------------|
| 嵌入模型 | all-MiniLM-L6-v2 (384d) | **multilingual-e5-small (384d)** |
| 检索 | BM25 + 向量 + 图谱 RRF | **HNSW + SparseIndex BM25 RRF** |
| 模式补全 | ❌ | **✅ Hopfield 收敛** |
| 图学习 | 静态共现 | **✅ Hebbian 动态边权** |
| 记忆激活 | 全量加载 | **✅ Active/Latent/Dormant 三态** |
| 情感索引 | ❌ | **✅ 多维度情感反馈 + 情感召回** |
| 实时性 | SessionEnd 批量 | **✅ 逐轮实时写入** |
| 部署 | Node.js + SQLite | **Rust SDK + LMDB** |
| 延迟 | ~14ms | **< 1ms** |

## 安装

### Cargo 依赖

```toml
[dependencies]
# 基础版（仅 BM25 稀疏检索，零模型依赖）
memhop-core = "0.25"

# 完整版（BM25 + HNSW 语义向量，需要模型文件）
memhop-core = { version = "0.25", features = ["candle"] }
```

> 要求 Rust 1.85+（edition 2024）。使用 `candle` feature 需要系统有 C++ 编译器（macOS 自带 clang++，Linux 需 `g++`，Windows 需 MSVC）。

### 独立编码器服务（可选）

如果你的多个 Agent 需要共享向量模型，可以运行独立的 memhop-encoder 进程：

```bash
# 仅 NgramEncoder（无模型依赖）
memhop-encoder --dim 1024

# 加载语义向量模型（需要 candle feature）
memhop-encoder --model-path ./models/multilingual-e5-small
```

客户端通过 `EncoderClient` 连接：

```rust
use memhop_encoder_client::EncoderClient;
use memhop_core::encoder::Encoder;

let client = EncoderClient::connect("/tmp/memhop-encoder.sock")?;
let output = client.encode("你好世界");
```

## Quick Start

### 最简示例

```rust
use memhop_core::{MemHopSDK, MemHopConfig, StoreBatch, StoreItem, RecallRequest};

fn main() -> memhop_core::Result<()> {
    // 1. 初始化 SDK（全局一次性）
    MemHopSDK::init(MemHopConfig::default())?;

    // 2. 创建 Brain
    let mut brain = MemHopSDK::create_brain("./data", "my_agent")?;

    // 3. 存储记忆
    brain.batch_store(StoreBatch {
        items: vec![
            StoreItem { text: "用户喜欢 Rust 和猫".into(), ..Default::default() },
            StoreItem { text: "明天下午3点开会".into(), ..Default::default() },
        ],
    })?;

    // 4. 检索记忆
    let results = brain.recall(&RecallRequest {
        query: "用户有什么爱好".into(),
        ..Default::default()
    })?;

    for r in &results.results {
        println!("[{}] {}", r.score, r.text);
    }
    Ok(())
}
```

### 使用语义向量模型

```rust
use memhop_core::{MemHopSDK, MemHopConfig};

fn main() -> memhop_core::Result<()> {
    let config = MemHopConfig {
        model_path: Some("./models/multilingual-e5-small".to_string()),
        vector_dim: 384,
        ..Default::default()
    };
    MemHopSDK::init(config)?;

    let mut brain = MemHopSDK::create_brain("./data/agent1", "agent1")?;
    // Brain 现在同时支持 BM25 稀疏检索 + HNSW 语义向量检索
    Ok(())
}
```

### 多 Agent 共享编码器

```rust
use memhop_core::{MemHopSDK, MemHopConfig};

fn main() -> memhop_core::Result<()> {
    // 初始化一次，所有 Brain 共享同一个编码器实例
    MemHopSDK::init(MemHopConfig {
        model_path: Some("./models/multilingual-e5-small".to_string()),
        vector_dim: 384,
        ..Default::default()
    })?;

    let mut agent_a = MemHopSDK::create_brain("./data/agent_a", "agent_a")?;
    let mut agent_b = MemHopSDK::create_brain("./data/agent_b", "agent_b")?;
    // 各自独立的 LMDB 存储，共享编码器内存
    Ok(())
}
```

### 测试环境（非全局实例）

```rust
use memhop_core::{MemHopInstance, MemHopConfig};

fn main() -> memhop_core::Result<()> {
    // MemHopInstance 不污染全局状态，适合测试和多配置场景
    let instance = MemHopInstance::new(MemHopConfig::default())?;
    let mut brain = instance.create_brain("/tmp/test_brain", "test_agent")?;
    Ok(())
}
```

## 核心 API

### MemHopSDK（全局单例）

| 方法 | 说明 |
|------|------|
| `MemHopSDK::init(config)` | 初始化 SDK（进程级一次性） |
| `MemHopSDK::create_brain(dir, agent_id)` | 创建 Brain 实例（使用全局编码器） |
| `MemHopSDK::get_encoder()` | 获取全局编码器引用 |
| `MemHopSDK::is_initialized()` | 检查是否已初始化 |
| `MemHopSDK::init(MemHopConfig::from_env())` | 从 `MEMHOP_MODEL_PATH` 环境变量初始化 |

### MemHopInstance（非全局，测试友好）

| 方法 | 说明 |
|------|------|
| `MemHopInstance::new(config)` | 创建独立实例（不影响全局状态） |
| `instance.create_brain(dir, agent_id)` | 使用该实例的编码器创建 Brain |
| `instance.encoder()` | 获取该实例的编码器 |

### Brain

| 方法 | 说明 |
|------|------|
| `batch_store(batch)` | 批量存储记忆（唯一写入接口） |
| `recall(req)` | 检索记忆（BM25 + HNSW RRF 融合） |
| `consolidate()` | 记忆巩固（dream 管线：话题反思、关键词精炼） |
| `mount_shelf(dir, domain, name)` | 挂载外部知识库到 L3 |
| `crystallize_l3(req)` | L3 结晶化（从历史记忆中提炼操作流程） |
| `emotional_feedback(feedback)` | 多维度情感反馈 |
| `recall_by_emotion(req)` | 情感驱动召回 |
| `storage_stats()` | 各层存储统计信息 |

## 架构

### Workspace 结构

```
memhop/
├── memhop-core/           SDK 核心库（lib）
├── memhop-encoder/        独立编码器服务（bin）
├── memhop-encoder-client/ IPC 客户端库（lib）
└── memhop-protocol/       共享 IPC 协议定义（lib）
```

### memhop-core 模块

```
memhop-core/src/
├── sdk.rs              SDK 入口（MemHopSDK + MemHopInstance + MemHopConfig）
├── brain/              顶层 API（6 层记忆模型统一入口）
├── encoder/            编码器（NgramEncoder + CandleEncoder + EncoderRouter）
├── index.rs            HNSW 向量索引（usearch）+ SparseIndex（BM25 倒排索引）
├── activation/         记忆激活管理器（Active / Latent / Dormant 三态）
├── hypergraph/         L1 纠缠超图 + Hebbian 边权学习
├── topic_graph/        L2 话题标准图
├── domain_graph/       L3 领域超图
├── raw_archive/        L4 原文库
├── procedural/         L5 程序性结晶 — 链分析引擎
├── profile/            L0 角色画像
├── lmdb/               LMDB 持久化层（各层独立环境）
├── dream/              记忆巩固管线（consolidate 实现）
├── recall/             检索管线
├── batch_store.rs      批量存储（唯一写入接口）
├── query_engine.rs     按层检索引擎
├── organize/           记忆组织（话题反思、关键词精炼、边界检测）
├── shelf/              知识库挂载（L3 领域图扩展）
├── session/            会话上下文管理（纯内存）
├── splitter.rs         长文本分段
├── engram.rs           数据模型（KnowledgeNode, Hyperedge, Topic, ...）
└── types.rs            配置 + 请求/响应类型
```

### 数据流

```
用户输入
  │
  ├─→ Encoder（NgramEncoder / CandleEncoder / EncoderRouter）
  │     ├── sparse: HashMap<String, f32>  → SparseIndex (BM25)
  │     └── dense:  Vec<f16>              → HnswIndex (usearch HNSW)
  │
  ├─→ batch_store() → LMDB 持久化（各层独立事务）
  │
  └─→ recall()
        ├── Stage 1: SparseIndex BM25 粗筛 → 候选集
        ├── Stage 2: HnswIndex HNSW 精排 → Top-K
        └── RRF 融合 → 最终排序
```

## 平台兼容

| 平台 | 状态 | 说明 |
|------|------|------|
| macOS (Apple Silicon / Intel) | ✅ 完整支持 | 原生 clang++ |
| Linux (x86_64 / aarch64) | ✅ 完整支持 | 需要 `g++`（usearch 编译依赖） |
| Windows (x86_64) | ✅ 完整支持 | IPC 编码器通过 TCP localhost 通信 |

> Windows 上 IPC 编码器使用 TCP 127.0.0.1 通信（非 Unix Socket），SDK 核心功能全平台一致。

## Benchmarks

MemHop 内置 9 个基准测试套件，覆盖检索延迟、吞吐量、端到端性能：

```bash
# 运行全部基准（需要 bench feature）
cargo bench --workspace --features bench

# 运行特定基准
cargo bench --bench retrieval_bench --features bench    # 检索延迟
cargo bench --bench functional_bench --features bench   # 功能基准
cargo bench --bench agent_e2e_bench --features bench    # Agent 端到端
cargo bench --bench longmemeval_bench --features bench  # LongMemEval 评估

# 需要 LLM API 的基准
cargo bench --bench llm_integration_bench --features "bench,bench-llm,llm-api"
```

关键指标（Apple M 系列，1000 节点规模）：

| 操作 | 延迟 |
|------|------|
| 单条 recall（BM25 only） | < 1ms |
| 单条 recall（BM25 + HNSW） | < 3ms |
| batch_store（10 条） | < 5ms |
| HNSW cosine_search（top-10） | < 0.5ms |

## 测试

```bash
# 全量测试
cargo test --workspace

# 仅 memhop-core
cargo test -p memhop-core

# 包含 candle feature
cargo test -p memhop-core --features candle
```

## 生态

MemHop 是 Meow 生态的记忆底座：

| 项目 | 说明 | 链接 |
|------|------|------|
| **MeowAgent** | AI Agent 框架，内嵌 MemHop 作为记忆引擎 | [GitHub](https://github.com/qyiun666/MeowAgent) |
| **MeowDesk** | 桌面陪伴应用（Tauri + Rust） | 敬请期待 |
| **memhop-encoder** | 独立编码器服务，多 Agent 共享向量模型 | 本仓库 |

## Contributing

欢迎贡献！在提交 PR 之前：

1. Fork 本仓库并创建 feature 分支
2. 确保 `cargo test --workspace` 通过
3. 如果修改了公共 API，请更新文档注释
4. 提交 PR 并描述你的改动

Bug 报告和功能请求请直接提交 [Issue](https://github.com/qyiun666/memhop/issues)。

## Sponsor

If MemHop powers your agent's memory, consider sponsoring to support ongoing development. Your sponsorship covers compute costs, benchmark infrastructure, and open-source maintenance.

| Tier | Monthly | What You Get |
|------|---------|--------------|
| Kitten 🐱 | $1 | Heartfelt thanks + name on Sponsor Wall |
| Tabby 🐾 | $5 | Early feature access + priority issue triage |
| Siamese 🐈 | $10 | Monthly dev updates + private Discord channel |
| Maine Coon 🦁 | $25 | Priority roadmap input + beta testing access |
| Sphinx 👑 | $100 | Direct line with maintainer + sponsor logo in README |

**[Sponsor on GitHub](https://github.com/sponsors/qyiun666)**

## 链接

- **官网：** https://qyiun666.github.io/meowagent.github.io/
- **MeowAgent：** https://github.com/qyiun666/MeowAgent
- **MeowDesk：** 桌面陪伴应用（Tauri + Rust，敬请期待）
- **邮箱：** qyiun666@163.com

## License

Licensed under either of [MIT license](LICENSE-MIT) or [Apache License, Version 2.0](LICENSE-APACHE) at your option.

Unless you explicitly state otherwise, any contribution intentionally submitted for inclusion in this crate by you, as defined in the Apache-2.0 license, shall be dual licensed as above, without any additional terms or conditions.
