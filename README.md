<p align="center">
  <h1 align="center">MemHop</h1>
  <p align="center">
    <strong>Your agent remembers like a human.</strong>
  </p>
  <p align="center">
    A memory database purpose-built for AI agents, implementing a seven-layer cognitive architecture (L0–L6) in a single embedded file.
  </p>
  <p align="center">
    <a href="https://qyiun666.github.io/meowagent.github.io/">Website</a>
    &middot;
    <a href="API.md">API Reference</a>
    &middot;
    <a href="https://github.com/meowagent/meowagent">MeowAgent</a>
  </p>
</p>

<p align="center">
  <img src="https://img.shields.io/crates/v/memhop" alt="crates.io">
  <img src="https://img.shields.io/crates/l/memhop" alt="license">
  <img src="https://img.shields.io/github/actions/workflow/status/meowagent/memhop/ci.yml?label=build" alt="build">
  <img src="https://img.shields.io/badge/rust-1.75%2B-orange" alt="rust">
</p>

---

MemHop is not a vector database. It is a memory system modeled after how the human brain organizes knowledge — with identity, episodic recall, semantic compression, skill acquisition, archival storage, and crystallized expertise. One agent, one `.meh` file, zero infrastructure.

Built as the brain memory of [MeowAgent](https://github.com/meowagent/meowagent), MemHop works as an embedded organ rather than a standalone service. No server to run, no configuration to manage — just open a file and your agent has memory.

```c
/* 4 functions. JSON in, JSON out. Works from C, Python, Go, and anything with a C ABI. */

void* db   = memhop_open("{\"db_path\":\"agent.meh\",\"vector_dim\":768}");
char* res  = memhop_execute(db, "{\"command\":\"search\",\"dialogue\":\"What did we talk about?\"}");
              memhop_free_string(res);
              memhop_close(db);
```

## Quick Start

### FFI — Any Language

The FFI exposes 4 `extern "C"` functions that dispatch 13 JSON commands. Download pre-built binaries from [Releases](../../releases).

```python
# Python
import ctypes, json

lib = ctypes.CDLL("./libmemhop.dylib")
lib.memhop_open.restype = ctypes.c_void_p
lib.memhop_execute.restype = ctypes.c_char_p

db = lib.memhop_open(json.dumps({"db_path": "agent.meh", "vector_dim": 768}).encode())

result = lib.memhop_execute(db, json.dumps({
    "command": "search",
    "dialogue": "What did we discuss yesterday?",
    "context_limit": 10
}).encode())

print(json.loads(result))
lib.memhop_close(db)
```

### Rust

```rust
use memhop::{MemHop, MemHopConfig, SearchQuery, UpdateRequest};

let mut db = MemHop::open(MemHopConfig::new("agent.meh".into(), 768))?;

// 1. Retrieve relevant contexts (vector retrieval requires the grpc-encoder feature)
let results = db.search_context(SearchQuery {
    dialogue: "What did we discuss yesterday?".into(),
    l2_id: None,
    context_id: None,
    l3_id: None,
    context_limit: 10,
    auto_create: 0,
    min_score: 0.0,
    source: Default::default(),
})?;

for ctx in &results.contexts {
    println!("[{:.2}] {}", ctx.retrieval_score, ctx.title);
}

// 2. Append a new turn to the top context
if let Some(ctx) = results.contexts.first() {
    db.update_memory(UpdateRequest {
        topic_id: ctx.id.clone(),
        dialogue_text: "We discussed Rust lifetimes.".into(),
        summary: None,
        action_chain: None,
        instant_distill: false,
        source: Default::default(),
    })?;
}

// 3. Run Dream consolidation (uses the LLM configured in MemHopConfig)
let report = db.dream(None)?;
println!("dream stages: {:?}", report.stages);

// 4. Graceful shutdown
db.close()?;
```

## Architecture

MemHop models memory as seven cognitive layers, each corresponding to a distinct brain function. Memories flow between layers during the Dream consolidation cycle, just as the human brain consolidates experiences during sleep.

```
Layer   Name             Human Parallel        Mechanism
─────   ──────────────   ───────────────────   ─────────────────────────────────────────────
 L6     PathwayWeight    Procedural memory     Weighted action pathways & habit reinforcement
 L5     Crystal          Muscle memory         Crystallized procedures & reusable skills
 L4     Archive          Long-term memory      Raw dialogue logs & historical records
 L3     Knowledge        Semantic memory       Multi-source hypergraph knowledge base
 L2     Context          Working memory        Compressed topic structures (4 depth levels)
 L1     Engram           Associative hypergraph  Hypergraph skeleton linking L2 contexts via typed hyperedges (CoOccurrence/Causal/Semantic/Temporal/Hierarchical/Sequence); episodic decay with emotional modulation
 L0     Profile          Identity              Agent personality, preferences & language habits
```

Memories enter at L1/L2 as raw conversation, then flow downward during Dream cycles: L2 contexts compress and merge, L3 extracts structured knowledge, L4 archives the originals, and L5 distills reusable skills. The result is a memory system that grows more organized and insightful over time — without manual intervention.

### Knowledge Graph (L3)

L3 stores structured knowledge as **multiple independent hypergraphs** — not flat embeddings, but typed nodes connected by labeled, weighted edges. Knowledge can be distilled from conversations (Dream pipeline), imported from documents and file paths, or created programmatically.

Each hypergraph is a self-contained knowledge domain. When you search, MemHop returns **L3 previews** — lightweight summaries of relevant knowledge graphs (title, key nodes, keywords) — so the agent can decide which graphs to explore in depth without loading full structures.

```
search("how does photosynthesis work?")
  │
  ├─ L2 Contexts ────── compressed conversation matches
  ├─ L3 Previews ────── lightweight knowledge summaries
  │    ├─ "Biology > Plant Science" (12 nodes, keywords: chlorophyll, Calvin cycle...)
  │    └─ "Chemistry > Organic Reactions" (8 nodes, keywords: carbon fixation...)
  └─ Archives ───────── raw dialogue references
```

## Dream Pipeline

The Dream cycle is MemHop's most distinctive feature — an automatic memory consolidation process inspired by how the human brain processes experiences during sleep. Each cycle runs seven stages in sequence:

```
Dream Cycle
    │
    ├─ 1. L3 Distillation        Extract structured knowledge from conversations via LLM
    ├─ 2. L2 Compression         Demote old contexts through 4 depth levels, merge topics
    ├─ 3. L1 Rebuild & Decay     Rebuild hypergraph (remove dangling nodes/edges); decay episodic importance over time
    ├─ 4. L0 Profile Rebuild     Regenerate agent profile from accumulated knowledge
    ├─ 5. Language Habit Learn   Discover user's vocabulary, style traits, emotion patterns
    ├─ 6. L5 Crystallization     Extract reusable procedures from action chain patterns
    └─ 7. L6 Pathway Decay       Apply time-decay to procedural pathway weights and prune stale habits
```

Emotional salience modulates memory persistence: high-arousal, high-valence memories decay slower than neutral ones — the same mechanism that makes emotional experiences more memorable in humans.

Dream requires an OpenAI-compatible LLM endpoint for the distillation stages. Without an LLM, MemHop falls back to heuristic consolidation and remains fully functional for search and update operations.

## Search

MemHop uses **two-channel retrieval fusion** (BM25 + vector) and then optionally applies a cross-encoder reranker to surface the most relevant memories:

| Channel | Weight | Method |
|---------|--------|--------|
| BM25 | 0.45 | Keyword matching via inverted index with CJK tokenization |
| Vector | 0.55 | Semantic similarity with f16 half-precision SIMD (AVX2 / NEON) |
| Reranker | — | Optional cross-encoder rerank over the fused candidate set (`enable_reranker: true` by default) |

Search routes through four paths depending on the query parameters — `auto_create` for new conversation tracking, `context_id` for targeted recall within a topic, `l3_id` for knowledge graph exploration, and the default full two-channel retrieval for general memory search. This design ensures searches are fast and precise without scanning the entire database in normal workflows.

## .meh File Format

MemHop uses a custom binary format purpose-built for memory storage:

```
┌─────────────────────────────────┐
│  Header A  (4 KB)               │  ← Active header (CRC32 checksummed)
│  Header B  (4 KB)               │  ← Backup header (crash recovery)
├─────────────────────────────────┤
│  Page 0    (4 KB)               │  ← B-tree root, index pages
│  Page 1    (4 KB)               │
│  ...                            │
│  Page N    (4 KB)               │  ← Data pages (context, engram, archive...)
├─────────────────────────────────┤
│  Free List                      │  ← Available page tracking
│  Journal (WAL)                  │  ← Write-ahead transaction log
└─────────────────────────────────┘
```

A/B dual headers with CRC32 checksums and a WAL journal provide crash safety. The database is memory-mapped for zero-copy reads and grows automatically when pages are exhausted (500 pages / 2 MB per extension). All vector data uses f16 half-precision floats for 2x memory efficiency.

## Platform Support

| Platform | Binary | CI |
|----------|--------|----|
| macOS Universal (Intel + Apple Silicon) | `libmemhop-universal.dylib` | `create-universal` |
| macOS Apple Silicon | `libmemhop.dylib` | `build-macos-arm` |
| macOS Intel | `libmemhop.dylib` | `build-macos-x86` |
| Linux x86_64 | `libmemhop.so` | `build-linux` |
| Windows x86_64 | `memhop.dll` | `build-windows` |

## Development

```bash
cargo build --release     # Build library + cdylib
cargo test                # Run test suite

# Full test including LLM Dream pipeline
MEMHOP_LLM_API_KEY=sk-xxx cargo test -- --include-ignored --nocapture
```

## Changelog

| Version Range | Date | Highlights |
|---|---|---|
| **v0.42.0 – v0.47.0** | 2026-06-14 ~ 2026-06-25 | SQLite-grade embedded DB refactor; `graph_query` / `delete` FFI commands; OpenAI-compatible LLM config; L3 retrieval optimization + adjacency cache + reverse index |
| **v0.30.0 – v0.41.0** | 2026-06-14 | Dedicated memory DB with `.meh` format; six-layer cognitive architecture (L0–L5); L2-centric search/update model; Dream consolidation pipeline; BM25 + HNSW dual-channel retrieval |
| **v0.23.0 – v0.25.x** | 2026-06-08 ~ 2026-06-10 | Architecture redesign; usearch replacing fast-hnsw; cross-platform transport layer; 6-layer decomposition + triple retrieval; L3 domain graph + Dream v2 |
| **v0.18.0 – v0.19.0** | 2026-06-05 ~ 2026-06-07 | Architecture optimization + `catid` field; single-instance validation; stateless request-level architecture; 22 MCP interfaces |
| **v0.12.0 – v0.14.x** | 2026-05-31 ~ 2026-06-04 | Brain-inspired memory architecture; knowledge tree + entangled events; stateless refactor; multi-agent isolation; 4-layer hypergraph memory engine |
| **v0.6.0 – v0.11.0** | 2026-05-25 ~ 2026-05-29 | Pure Rust rewrite (Python removed); Brain three-layer memory architecture; Plan-level memory; HNSW dual-mode recall; Unified Memory Architecture |
| **v0.1.0 – v0.5.x** | 2026-05-19 ~ 2026-05-24 | Hopfield network core engine; Rust + pyo3 embedded engine; BrainLoop self-cycling agent; dual-model calibration architecture |

For detailed release notes, see [GitHub Releases](../../releases).

## Contributing

Have a bug report or feature idea? [Open an issue](../../issues).

## Contact

| | |
|---|---|
| Email | qyiun666@163.com |
| MeowAgent | [github.com/meowagent/meowagent](https://github.com/meowagent/meowagent) |
| Website | [qyiun666.github.io/meowagent.github.io](https://qyiun666.github.io/meowagent.github.io/) |

## License

Licensed under either of [Apache License, Version 2.0](LICENSE-APACHE) or [MIT License](LICENSE-MIT) at your option.
