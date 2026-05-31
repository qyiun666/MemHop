# MemHop v0.9.0

**脑启发记忆引擎。** Hopfield 模式补全 + HNSW 语义检索 + Hebbian 图学习。单进程 MCP Server，全机统一记忆底座。

## 🎯 第一梯队检索

| Benchmark | 指标 | 结果 |
|-----------|------|------|
| 合成检索 (BGE-M3, 200 docs) | NDCG@10 | **0.979** = 99.9% 余弦上限 |
| BEIR nfcorpus (300 docs) | NDCG@10 | **0.183** = 98.0% 余弦上限 |
| HNSW 检索延迟 | P50 | **< 1ms** |
| 检索延迟改善 (10K) | P99 | **541ms → 165ms (-70%)** |

> 检索管线无损 — HNSW 近似搜索质量紧贴纯余弦理论上限。延迟从 O(N) 降到 O(log N)。

## vs agentmemory

| 维度 | agentmemory | MemHop v0.9.0 |
|------|------------|---------------|
| 嵌入模型 | all-MiniLM-L6-v2 (384d) | **BGE-M3 (1024d)** |
| 检索 | BM25 + 向量 + 图谱 RRF | **HNSW + SparseIndex RRF** |
| 模式补全 | ❌ | **✅ Hopfield 收敛** |
| 图学习 | 静态共现 | **✅ Hebbian 动态边权** |
| 实时性 | SessionEnd 批量 | **✅ 逐轮实时** |
| 部署 | Node.js + SQLite | **Rust 单二进制 + LMDB** |
| 延迟 | 14ms | **< 1ms** |
| 多猫内存 | 共享 | **单进程 1×BGE-M3 (2GB)** |
| LongMemEval-S R@5 | 95.2% | 待跑 (`benchmarks/run_longmemeval.py`) |

## 安装

```bash
# MCP Server（二进制）
cargo build --release --features onnx,api-encoder
./target/release/memhop-mcp-server

# 作为 Rust 库
cargo add memhop
```

要求 Rust 1.85+。可选依赖：ONNX Runtime (BGE-M3 编码)、API encoder (OpenAI 兼容)。

## Quick Start

### MCP Server (推荐)

```bash
# 启动 MCP Server
MEMHOP_DB_PATH=/path/to/brain.db ./target/release/memhop-mcp-server

# 或在 Claude Desktop / Cursor 中配置
{
  "mcpServers": {
    "memhop": {
      "command": "/path/to/memhop-mcp-server",
      "env": { "MEMHOP_DB_PATH": "/path/to/brain.db" }
    }
  }
}
```

MCP Tools：`memhop_store` `memhop_recall` `memhop_mount_shelf` `memhop_knowledge_search` `memhop_forget` `memhop_update` `memhop_dream` `memhop_stats` `memhop_health`

### Rust Library

```rust
use memhop::{Brain, BrainConfig, PerceptionInput, RecallRequest, EmotionalState, Protection};

// Candle encoder is required. Set onnx_model_path to the model directory.
let config = BrainConfig {
    onnx_model_path: Some("models/bge-m3".into()),
    ..Default::default()
};
let mut brain = Brain::open("brain.db", config, None)?;

// 存储记忆
let out = brain.perceive(PerceptionInput {
    content: "今天学了 Rust ownership".into(),
    vector: vec![], // BGE-M3 编码器自动填充
    session_id: "chat-001".into(),
    ..Default::default()
})?;

// 检索 (Retrieval Mode — 纯语义)
let resp = brain.recall(&RecallRequest {
    query: "Rust 内存管理".into(),
    limit: 5,
    ..Default::default()
})?;

// 类脑联想 (Associative Mode — 情绪 + 图扩散)
let resp = brain.recall(&RecallRequest {
    query: "上次那个 Rust 的 bug".into(),
    mode: RecallMode::Associative,
    limit: 5,
    ..Default::default()
})?;

// 挂载知识库
brain.mount_shelf("/Users/me/books/rust-book", ShelfDomain::Book)?;
let results = brain.knowledge_search("ownership rules", "rust-book", 5)?;

// 记忆巩固
brain.dream()?;
```

## 核心特性

### 双模式检索
- **Retrieval Mode**：HNSW → cosine sort → [Cross-Encoder] → 纯质量，对标 FAISS
- **Associative Mode**：HNSW → Hopfield spread → 情绪/ngram boost → 类脑联想

### 三层架构
```
L0 Cortex (VecDeque, 7 entries)     → 工作记忆，当前会话
L1 Hippocampus (LMDB, ~500 entries) → 暂存区，高保真
L2 Hopfield + EntangleGraph         → 长期记忆，模式补全 + 图关联扩散
```

### 知识库挂载
```
MeowAgent 传入路径 → MemHop 自动切片 → BGE-M3 编码 → HNSW 索引
支持: code (AST) / doc (heading) / book (chapter) / paper / custom
```

### Dream 记忆巩固
- NREM：遗忘弱记忆 + 合并相似模式
- REM：跨域关联发现 + 矛盾检测 + Schema 命名
- LLM 可选注入 (不在检索热路径)

### Plan-Gated Retrieval (PGT)
四层计划门控检索 (L0-L3)：计划内 ngram → 图 BFS → 时序 → 全局回退

### EntangleGraph
四种边类型：Semantic / Temporal / Manual / CrossTree。Hebbian 学习：频繁共召回自动增强边权。竞争扩散激活 + 横向抑制。

### 编码器策略
```
api-encoder (OpenAI) > ONNX BGE-M3 (1024d) > NgramEncoder (fallback)
单进程加载一份 BGE-M3 (2GB)，所有猫共享
```

### 隐私过滤
`memhop_store` 自动剥离 API key、secret、`<private>` 标签

## 部署架构

```
全机一个 memhop-mcp-server 进程
├── BGE-M3 ONNX (1×2GB)
├── Brain(cat_a) → LMDB: /data/cats/A/
├── Brain(cat_b) → LMDB: /data/cats/B/
├── Shelf(rust-book) → HNSW-only 知识索引
└── Dream Scheduler → 轮询所有 Brain
```

## 架构

```
memhop (lib crate, v0.9.0)
├── brain.rs         顶层 API (Retrieval/Associative 双模式)
├── hnsw.rs          HNSW 图搜索索引 (719 行, O(log N))
├── hopfield.rs      Modern Hopfield Network (模式补全)
├── activation.rs    竞争扩散 + 情绪对齐
├── shelf.rs         知识库挂载 (334 行)
├── encoder/
│   ├── hybrid.rs    Ngram+ONNX 融合编码
│   ├── onnx.rs      BGE-M3 ONNX 编码器
│   ├── api.rs       OpenAI 兼容 API 编码器
│   ├── ngram.rs     N-gram 哈希编码 (fallback)
│   └── reranker.rs  Cross-Encoder 精排 (147 行)
├── storage.rs       LMDB 持久化层
├── index.rs         SparseIndex (ngram 倒排)
├── unified_graph.rs EntangleGraph + Hebbian 学习
├── dream.rs         六阶段记忆巩固
├── plan_gate.rs     Plan-Gated Retrieval
└── types.rs         配置 + 请求/响应类型

memhop-mcp-server (binary crate, v0.9.0)
└── main.rs          MCP JSON-RPC (多数据库路径 + health + 隐私过滤)
```

## Benchmark

```bash
# 构建
cargo build --release --features onnx

# 单元测试 (144+ tests)
cargo test --workspace

# 延迟 benchmark
./target/release/latency_bench --scales 1000,5000,10000,50000

# 质量 benchmark (需 Python 环境)
pip install sentence-transformers numpy
python3 -c "
from sentence_transformers import SentenceTransformer
# ... 生成 BGE-M3 编码的测试数据 ...
"
./target/release/quality_bench --input /tmp/input.json --output /tmp/output.json --mode retrieval

# LongMemEval-S (对标 agentmemory)
python3 benchmarks/run_longmemeval.py 500
```

## 测试

```bash
cargo test --workspace
# 99 lib tests + 30 integration + 15 plan_integration = 144 total
```

## License

All Rights Reserved. Copyright (c) 2026 MemHop Contributors.
