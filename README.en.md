<!-- language switcher -->
<p align="center">
  <a href="README.md">中文</a> | <strong>English</strong>
</p>

<!-- badges -->
<p align="center">
  <a href="https://crates.io/crates/memhop-core"><img src="https://img.shields.io/crates/v/memhop-core?style=flat-square" alt="crates.io"></a>
  <a href="https://docs.rs/memhop-core"><img src="https://img.shields.io/docsrs/memhop-core?style=flat-square" alt="docs.rs"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT%20%2F%20Apache--2.0-blue?style=flat-square" alt="License"></a>
</p>

<!-- navigation -->
<p align="center">
  <a href="https://qyiun666.github.io/meowagent.github.io/">Website</a>
  ·
  <a href="#quick-start">Quick Start</a>
  ·
  <a href="https://docs.rs/memhop-core">API Docs</a>
  ·
  <a href="#benchmarks">Benchmarks</a>
  ·
  <a href="https://github.com/qyiun666/MeowAgent">MeowAgent</a>
  ·
  <a href="#reporting-issues">Reporting Issues</a>
</p>

---

<h1 align="center">MemHop</h1>

<p align="center">
  <strong>Brain-Inspired Memory Engine SDK</strong><br>
  Hopfield Pattern Completion · HNSW Semantic Retrieval · Hebbian Graph Learning<br>
  Single-process embedded memory foundation for AI Agents
</p>

---

## Features

**Retrieval Engine**

- **Sublinear Single-Item Recall** — Not Top-K approximate search; mimics human instant recall via Hopfield network pattern completion
- **BM25 + HNSW Dual-Channel** — Sparse retrieval (ngram inverted index + BM25) always available; dense vector retrieval (usearch HNSW) optional enhancement
- **Pluggable Dual Encoders** — Default NgramEncoder with zero model dependency; enable `candle` feature to load multilingual-e5-small semantic vectors with EncoderRouter auto-routing sparse/dense channels

**6-Layer Memory Model**

- **L0 Profile** — Agent persona and preference persistence
- **L1 Entangled Hypergraph** — Core memory layer: KnowledgeNode + Hyperedge + hyperedge chains, Hebbian dynamic edge weight learning
- **L2 Topic Graph** — Topic clustering and association discovery
- **L3 Domain Hypergraph** — Cross-topic knowledge distillation, supports L3 crystallization (`crystallize_l3`)
- **L4 Raw Archive** — Original conversation/document archival
- **L5 Procedural Crystals** — Chain analysis engine, distills reusable operational procedures from historical memories

**Memory Lifecycle**

- **Memory Activation System** — Active / Latent / Dormant tri-state management; Active subset resides in HNSW (≤5000 nodes); decay formula: `score = importance × exp(-λt) + recall_bonus`
- **Memory Consolidation (Dream)** — Background consolidation pipeline: automatic topic reflection, keyword refinement, boundary detection
- **Emotional Indexing** — Multi-dimensional emotional feedback (`emotional_feedback`), emotion-driven recall (`recall_by_emotion`)
- **Knowledge Shelf Mounting** — Inject external knowledge graphs into L3 via `mount_shelf`

**Engineering**

- **LMDB Persistence, Zero Config** — Independent LMDB environment per layer, transaction-safe, no external database required
- **HNSW Index Persistence** — usearch native serialization; loads from cache on startup, avoids full rebuild
- **Standalone Encoder Service** — memhop-encoder runs as a separate process; multiple Agents share one vector model instance via IPC
- **Global Encoder Sharing** — MemHopSDK singleton pattern; multiple Brains in the same process share the encoder, saving memory

## vs agentmemory

| Dimension | agentmemory | MemHop v0.25.1 |
|-----------|------------|----------------|
| Embedding Model | all-MiniLM-L6-v2 (384d) | **multilingual-e5-small (384d)** |
| Retrieval | BM25 + Vector + Graph RRF | **HNSW + SparseIndex BM25 RRF** |
| Pattern Completion | ❌ | **✅ Hopfield Convergence** |
| Graph Learning | Static Co-occurrence | **✅ Hebbian Dynamic Edge Weights** |
| Memory Activation | Full Load | **✅ Active/Latent/Dormant Tri-State** |
| Emotional Indexing | ❌ | **✅ Multi-Dimensional Emotional Feedback + Recall** |
| Real-time | SessionEnd Batch | **✅ Per-Turn Real-Time** |
| Deployment | Node.js + SQLite | **Rust SDK + LMDB** |
| Latency | ~14ms | **< 1ms** |

## Installation

### Cargo Dependency

```toml
[dependencies]
# Basic (BM25 sparse retrieval only, zero model dependency)
memhop-core = "0.25"

# Full (BM25 + HNSW semantic vectors, requires model files)
memhop-core = { version = "0.25", features = ["candle"] }
```

> Requires Rust 1.85+ (edition 2024). The `candle` feature requires a C++ compiler (macOS: clang++ built-in, Linux: `g++`, Windows: MSVC).

### Standalone Encoder Service (Optional)

If multiple Agents need to share a vector model, run the standalone memhop-encoder process:

```bash
# NgramEncoder only (no model dependency)
memhop-encoder --dim 1024

# Load semantic vector model (requires candle feature)
memhop-encoder --model-path ./models/multilingual-e5-small
```

Connect from a client using `EncoderClient`:

```rust
use memhop_encoder_client::EncoderClient;
use memhop_core::encoder::Encoder;

let client = EncoderClient::connect("/tmp/memhop-encoder.sock")?;
let output = client.encode("Hello world");
```

## Quick Start

### Minimal Example

```rust
use memhop_core::{MemHopSDK, MemHopConfig, StoreBatch, StoreItem, RecallRequest};

fn main() -> memhop_core::Result<()> {
    // 1. Initialize SDK (one-time, process-wide)
    MemHopSDK::init(MemHopConfig::default())?;

    // 2. Create Brain
    let mut brain = MemHopSDK::create_brain("./data", "my_agent")?;

    // 3. Store memories
    brain.batch_store(StoreBatch {
        items: vec![
            StoreItem { text: "User loves Rust and cats".into(), ..Default::default() },
            StoreItem { text: "Meeting tomorrow at 3pm".into(), ..Default::default() },
        ],
    })?;

    // 4. Retrieve memories
    let results = brain.recall(&RecallRequest {
        query: "What are the user's hobbies".into(),
        ..Default::default()
    })?;

    for r in &results.results {
        println!("[{}] {}", r.score, r.text);
    }
    Ok(())
}
```

### With Semantic Vector Model

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
    // Brain now supports both BM25 sparse + HNSW semantic vector retrieval
    Ok(())
}
```

### Multi-Agent Shared Encoder

```rust
use memhop_core::{MemHopSDK, MemHopConfig};

fn main() -> memhop_core::Result<()> {
    // Initialize once; all Brains share the same encoder instance
    MemHopSDK::init(MemHopConfig {
        model_path: Some("./models/multilingual-e5-small".to_string()),
        vector_dim: 384,
        ..Default::default()
    })?;

    let mut agent_a = MemHopSDK::create_brain("./data/agent_a", "agent_a")?;
    let mut agent_b = MemHopSDK::create_brain("./data/agent_b", "agent_b")?;
    // Independent LMDB storage, shared encoder memory
    Ok(())
}
```

### Testing (Non-Global Instance)

```rust
use memhop_core::{MemHopInstance, MemHopConfig};

fn main() -> memhop_core::Result<()> {
    // MemHopInstance does not pollute global state — ideal for tests
    let instance = MemHopInstance::new(MemHopConfig::default())?;
    let mut brain = instance.create_brain("/tmp/test_brain", "test_agent")?;
    Ok(())
}
```

## Core API

### MemHopSDK (Global Singleton)

| Method | Description |
|--------|-------------|
| `MemHopSDK::init(config)` | Initialize SDK (one-time, process-wide) |
| `MemHopSDK::create_brain(dir, agent_id)` | Create Brain instance (uses global encoder) |
| `MemHopSDK::get_encoder()` | Get global encoder reference |
| `MemHopSDK::is_initialized()` | Check if initialized |
| `MemHopSDK::init(MemHopConfig::from_env())` | Initialize from `MEMHOP_MODEL_PATH` env var |

### MemHopInstance (Non-Global, Test-Friendly)

| Method | Description |
|--------|-------------|
| `MemHopInstance::new(config)` | Create independent instance (does not affect global state) |
| `instance.create_brain(dir, agent_id)` | Create Brain using this instance's encoder |
| `instance.encoder()` | Get this instance's encoder |

### Brain

| Method | Description |
|--------|-------------|
| `batch_store(batch)` | External input batch store (Dream = internal maintenance) |
| `recall(req)` | Retrieve memories (BM25 + HNSW RRF fusion) |
| `consolidate()` | Memory consolidation (dream pipeline: topic reflection, keyword refinement) |
| `mount_shelf(dir, domain, name)` | Mount external knowledge graph into L3 |
| `crystallize_l3(req)` | L3 crystallization (distill operational procedures from history) |
| `emotional_feedback(feedback)` | Multi-dimensional emotional feedback |
| `recall_by_emotion(req)` | Emotion-driven recall |
| `storage_stats()` | Per-layer storage statistics |

## Architecture

### Workspace Structure

```
memhop/
├── memhop-core/           SDK core library (lib)
├── memhop-encoder/        Standalone encoder service (bin)
├── memhop-encoder-client/ IPC client library (lib)
└── memhop-protocol/       Shared IPC protocol definitions (lib)
```

### memhop-core Modules

```
memhop-core/src/
├── sdk.rs              SDK entry (MemHopSDK + MemHopInstance + MemHopConfig)
├── brain/              Top-level API (unified 6-layer memory model entry)
├── encoder/            Encoders (NgramEncoder + CandleEncoder + EncoderRouter)
├── index.rs            HNSW vector index (usearch) + SparseIndex (BM25 inverted index)
├── activation/         Memory activation manager (Active / Latent / Dormant tri-state)
├── hypergraph/         L1 entangled hypergraph + Hebbian edge weight learning
├── topic_graph/        L2 topic graph
├── domain_graph/       L3 domain hypergraph
├── raw_archive/        L4 raw archive
├── procedural/         L5 procedural crystals — chain analysis engine
├── profile/            L0 persona profile
├── lmdb/               LMDB persistence layer (independent env per layer)
├── dream/              Memory consolidation pipeline (consolidate implementation)
├── recall/             Retrieval pipeline
├── batch_store.rs      External input batch store
├── query_engine.rs     Per-layer retrieval engine
├── organize/           Memory organization (topic reflection, keyword refinement, boundary detection)
├── shelf/              Knowledge shelf mounting (L3 domain graph extension)
├── session/            Session context management (in-memory only)
├── splitter.rs         Long text segmentation
├── engram.rs           Data models (KnowledgeNode, Hyperedge, Topic, ...)
└── types.rs            Config + request/response types
```

### Data Flow

```
User Input
  │
  ├─→ Encoder (NgramEncoder / CandleEncoder / EncoderRouter)
  │     ├── sparse: HashMap<String, f32>  → SparseIndex (BM25)
  │     └── dense:  Vec<f16>              → HnswIndex (usearch HNSW)
  │
  ├─→ batch_store() → LMDB persistence (independent transactions per layer)
  │
  └─→ recall()
        ├── Stage 1: SparseIndex BM25 coarse screening → candidates
        ├── Stage 2: HnswIndex HNSW fine ranking → Top-K
        └── RRF fusion → final ranking
```

## Platform Compatibility

| Platform | Status | Notes |
|----------|--------|-------|
| macOS (Apple Silicon / Intel) | ✅ Full Support | Native clang++ |
| Linux (x86_64 / aarch64) | ✅ Full Support | Requires `g++` (usearch build dependency) |
| Windows (x86_64) | ✅ Full Support | IPC encoder communicates via TCP localhost |

> On Windows, the IPC encoder uses TCP 127.0.0.1 (not Unix Socket). SDK core functionality is identical across all platforms.

## Benchmarks

MemHop includes 9 benchmark suites covering retrieval latency, throughput, and end-to-end performance:

```bash
# Run all benchmarks (requires bench feature)
cargo bench --workspace --features bench

# Run specific benchmarks
cargo bench --bench retrieval_bench --features bench    # Retrieval latency
cargo bench --bench functional_bench --features bench   # Functional benchmarks
cargo bench --bench agent_e2e_bench --features bench    # Agent end-to-end
cargo bench --bench longmemeval_bench --features bench  # LongMemEval evaluation

# Benchmarks requiring LLM API
cargo bench --bench llm_integration_bench --features "bench,bench-llm,llm-api"
```

Key metrics (Apple M-series, 1000-node scale):

| Operation | Latency |
|-----------|---------|
| Single recall (BM25 only) | < 1ms |
| Single recall (BM25 + HNSW) | < 3ms |
| batch_store (10 items) | < 5ms |
| HNSW cosine_search (top-10) | < 0.5ms |

## Testing

```bash
# Full test suite
cargo test --workspace

# memhop-core only
cargo test -p memhop-core

# With candle feature
cargo test -p memhop-core --features candle
```

## Ecosystem

MemHop is the memory foundation of the Meow ecosystem:

| Project | Description | Link |
|---------|-------------|------|
| **MeowAgent** | AI Agent framework with MemHop as embedded memory engine | [GitHub](https://github.com/qyiun666/MeowAgent) |
| **MeowDesk** | Desktop companion app (Tauri + Rust) | Coming soon |
| **memhop-encoder** | Standalone encoder service for shared vector model | This repo |

## Reporting Issues

Bug reports and feature requests: please open a [GitHub Issue](https://github.com/qyiun666/memhop/issues).

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

## Links

- **Website:** https://qyiun666.github.io/meowagent.github.io/
- **MeowAgent:** https://github.com/qyiun666/MeowAgent
- **MeowDesk:** Desktop companion app (Tauri + Rust, coming soon)
- **Email:** qyiun666@163.com

## License

Licensed under either of [MIT license](LICENSE-MIT) or [Apache License, Version 2.0](LICENSE-APACHE) at your option.

Unless you explicitly state otherwise, any contribution intentionally submitted for inclusion in this crate by you, as defined in the Apache-2.0 license, shall be dual licensed as above, without any additional terms or conditions.
