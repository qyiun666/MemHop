# MemHop Core

> 6-layer brain-inspired memory engine SDK for AI Agents

[![Crates.io](https://img.shields.io/crates/v/memhop-core.svg)](https://crates.io/crates/memhop-core)
[![Documentation](https://docs.rs/memhop-core/badge.svg)](https://docs.rs/memhop-core)

## Overview

MemHop Core is a hypergraph-based associative memory engine designed for AI agents. It implements a 6-layer architecture inspired by human brain memory systems:

- **L0**: Role Profile (角色画像)
- **L1**: Entangled Hypergraph (纠缠超图)
- **L2**: Topic Graph (话题图)
- **L3**: Domain Hypergraph (领域超图)
- **L4**: Raw Archive (原文库)
- **L5**: Procedural Crystals (程序性晶体)

## Features

- **Triple-Channel Retrieval**: BM25 sparse + HNSW dense + E5 multilingual semantic
- **Brain-Inspired Architecture**: 6-layer memory hierarchy mimicking human memory
- **Emotional Memory**: Joy, Sadness, Anger, Fear, Surprise, Disgust support
- **Procedural Crystallization**: Extract patterns from memory chains
- **Knowledge Mounting**: Mount external knowledge sources (files, APIs, databases)
- **Session Management**: Topic activation/deactivation per session
- **Memory Consolidation**: Automatic memory organization and deduplication

## Quick Start

```rust
use memhop_core::{MemHopSDK, MemHopConfig, StoreBatch, StoreItem, RecallRequest};

fn main() -> memhop_core::Result<()> {
    // 1. Initialize SDK
    let config = MemHopConfig {
        model_path: Some("./models/multilingual-e5-small".to_string()),
        vector_dim: 384,
        ..Default::default()
    };
    MemHopSDK::init(config)?;

    // 2. Create Brain instance
    let mut brain = MemHopSDK::create_brain("./data/agent1", "agent1")?;

    // 3. Store memories
    brain.batch_store(StoreBatch {
        items: vec![StoreItem {
            text: "User prefers coffee over tea".to_string(),
            topic_label: Some("preference".to_string()),
            ..Default::default()
        }],
    })?;

    // 4. Recall memories
    let response = brain.recall(&RecallRequest {
        query: "What does the user drink?".to_string(),
        max_results: 5,
        ..Default::default()
    })?;

    for result in &response.results {
        println!("[{}] {} (score: {:.2})", result.layer, result.text, result.score);
    }

    Ok(())
}
```

## Feature Flags

| Feature | Description | Default |
|---------|-------------|---------|
| `candle` | Enable CandleEncoder for vector models | ❌ |
| `bench` | Benchmark support | ❌ |
| `bench-llm` | LLM integration testing (depends on bench) | ❌ |
| `llm-api` | LLM API calling support | ❌ |

## Dependencies

Add to your `Cargo.toml`:

```toml
[dependencies]
memhop-core = "0.25.1"

# With vector model support
memhop-core = { version = "0.25.1", features = ["candle"] }
```

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      MemHopSDK                              │
│  (Global singleton, shared encoder)                         │
├─────────────────────────────────────────────────────────────┤
│                        Brain                                │
│  ┌─────────┬─────────┬─────────┬─────────┬─────────┐       │
│  │   L0    │   L1    │   L2    │   L3    │   L4    │  L5   │
│  │ Profile │Hypergr. │ Topics  │ Domains │  Raw    │Crystal│
│  └─────────┴─────────┴─────────┴─────────┴─────────┴───────┘
│                           │                                 │
│  ┌────────────────────────┴────────────────────────┐       │
│  │              Query Engine                        │       │
│  │  BM25 + HNSW + E5 Triple-Channel Retrieval      │       │
│  └─────────────────────────────────────────────────┘       │
└─────────────────────────────────────────────────────────────┘
```

## License

All Rights Reserved. See [LICENSE](../LICENSE) for details.

## Links

- [Repository](https://github.com/meow-ai/memhop)
- [API Documentation](https://docs.rs/memhop-core)
- [Agent Integration Guide](../AGENT_INTEGRATION.md)
