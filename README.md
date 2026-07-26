<p align="center">
  <h1 align="center">MemHop</h1>
  <p align="center">
    <strong>Your agent remembers like a human — six-layer cognitive memory in a single embedded file.</strong>
  </p>
  <p align="center">
    <a href="README.zh.md">中文</a>
    &middot;
    <a href="https://qyiun666.github.io/meowagent.github.io/">Website</a>
    &middot;
    <a href="https://github.com/meowagent/meowagent">MeowAgent</a>
  </p>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/license-MIT%20OR%20Apache--2.0-blue.svg" alt="license">
  <img src="https://img.shields.io/badge/go-1.26+-00ADD8.svg" alt="go">
  <img src="https://img.shields.io/badge/test-passing-brightgreen.svg" alt="test">
</p>

---

MemHop is not a vector database. It is a memory system modeled after how the human brain organizes knowledge — with identity, episodic recall, semantic compression, skill acquisition, archival storage, and crystallized expertise. One agent, one `.meh` file, zero infrastructure.

MemHop is an **agent-dedicated** memory database: each agent binds to exactly one `.meh` file, and a file-level exclusive lock guarantees a single instance per file (a second `Open` fails fast). It runs on **Linux, macOS, and Windows** with no cgo and no external services beyond your embedding/LLM endpoints.

Built as the brain memory of [MeowAgent](https://github.com/meowagent/meowagent), MemHop works as an embedded organ rather than a standalone service. No server to run, no configuration to manage — just open a file and your agent has memory.

> **Our stance on agent memory.** Memory should not be an afterthought bolted on with a vector database plugin or a plain-text log dumped into a context window. An agent without internalised memory is just a stateless function pretending to be intelligent. MemHop exists because we believe memory must be *cognitive* — structured, compressed, consolidated, and forgotten the way a human brain does — and *embedded* — living inside the agent process itself, not behind a network call. One file, zero infrastructure, a mind that grows with every conversation.

## Features

- **Six-Layer Architecture** — L0 Profile → L1 Engram → L2 Context → L3 Knowledge → L4 Archive → L5 Crystal, with Dream consolidation
- **Three-Channel RRF** — BM25 (gse CJK) + f16 vector + entity fuzzy matching, fused via Reciprocal Rank Fusion (k=60)
- **V2 Storage** — `.meh` format with A/B dual headers, per-record CRC32 + torn-write truncation recovery, mmap zero-copy, snapshot/checkpoint
- **Dream Pipeline** — five stages over L0–L2: L2 compress → L1 rebuild → L1 decay → L0 profile → L0 distill (emotion/MBTI)
- **L3 Knowledge Graph** — Multi-hypergraph with community detection (clique + Louvain), BFS, adjacency caching
- **Single Instance by Design** — one agent = one `.meh` file, enforced by a cross-platform file lock (linux/darwin/windows)
- **Minimal & Embeddable** — 4 direct Go deps (xxhash, gse, ollama, go-openai), `sync.RWMutex` + `atomic.Pointer`, zero infrastructure

## Quick Start

```go
import (
    "context"
    "time"

    memhop "github.com/qyiun666/MemHop/api"
)

db, err := memhop.Open(&memhop.Config{
    DBPath:      "agent.meh",
    VectorDim:   768,
    EncoderAddr: "http://127.0.0.1:11434",
    EmbedModel:  "nomic-embed-text",
    LLM: memhop.LlmConfig{ // required: validated at Open
        APIURL: "https://api.openai.com/v1",
        APIKey: os.Getenv("OPENAI_API_KEY"),
        Model:  "gpt-4o-mini",
    },
})
if err != nil {
    log.Fatal(err)
}
defer db.Close()

// Search (Timestamp is required: Unix milliseconds of the message)
results, _ := db.Search(memhop.SearchQuery{
    Text:       "What did we discuss?",
    Timestamp:  time.Now().UnixMilli(),
    MaxResults: 10,
})

// Append the agent reply to the topic created by Search
_ = db.Update(results.NewTopicID, "Agent: ...", time.Now().UnixMilli())

// Batch store (Keywords are required per item)
db.BatchStore(memhop.StoreBatch{Items: []memhop.StoreItem{{
    Content:  "User: ...\nAgent: ...",
    Keywords: []string{"project", "deadline"},
}}})

// Dream consolidation (L0-L2)
report, _ := db.Dream(context.Background(), nil)
```

Prerequisites: Go 1.26+, Ollama (`ollama pull nomic-embed-text`), an OpenAI-compatible LLM endpoint (`Config.LLM` is required)

## Architecture

```
Layer   Name             Human Parallel          Mechanism
─────   ──────────────   ───────────────────     ─────────────────────────────────────────────
 L5     Crystal          Muscle memory           Crystallized procedures & reusable skills
 L4     Archive          Long-term memory        Raw dialogue logs & historical records
 L3     Knowledge        Semantic memory         Multi-source hypergraph knowledge base
 L2     Context          Working memory          Compressed topic structures (4 depth levels)
 L1     Engram           Associative hypergraph  Hypergraph skeleton linking L2 contexts
 L0     Profile          Identity                Agent personality, preferences & language habits
```

### Dream Pipeline

The Dream cycle is an automatic memory consolidation process inspired by how the human brain processes experiences during sleep. It operates on **L0–L2 only** (L3 distillation and L5 crystallization are out of scope by design) and runs five stages:

1. **L2 Compression** — LLM groups and merges related topics, demotes stale contexts
2. **L1 Rebuild** — Rebuild the hypergraph skeleton linking L2 contexts
3. **L1 Decay** — Decay episodic importance, prune weak nodes/edges
4. **L0 Profile** — Regenerate the agent profile from consolidated memory
5. **L0 Distill** — Distill emotion/MBTI patterns (optional, `SkipDistill`)

Each Dream call makes at most three outbound LLM requests. `Dream(ctx, opts)` serializes concurrent calls (the second returns an error) and honors `ctx` cancellation between stages.

### Search

MemHop uses **three-channel retrieval fusion** (BM25 + vector + entity) with RRF:

| Channel | Method                                                                   |
| ------- | ------------------------------------------------------------------------ |
| BM25    | Keyword matching via inverted index (gse CJK tokenization)       |
| Vector  | Semantic similarity with f16 half-precision via Ollama HTTP `/api/embed` |
| Entity  | Fuzzy entity name matching for knowledge graph queries                   |

Post-fusion: additive scene bonuses for active/recent sessions, then L1 association expansion + L5 crystal matching + L0 profile assembly.

## Benchmarks

Tested on [LOCOMO10](https://github.com/snap-research/LOCOMO) (ACL 2024) — 419 turns stored, 199 QA queries across 5 categories (Single/Multi/Open/Temporal/Abs all at 100%):

| Metric | Result |
|--------|--------|
| Recall@1 | **100.0%** (199/199) |
| Recall@3 | **100.0%** (199/199) |
| Recall@5 | **100.0%** (199/199) |
| P50 / P95 Latency | 1.76s / 3.97s ¹ |
| Engine-side search latency | P50 ≈ 15ms (offline MockEncoder benchmark) |

¹ End-to-end latency is dominated by embedding encode (Apple M2, Ollama bge-m3 running CPU-only); the engine's BM25 + vector + entity three-channel search itself takes single-digit milliseconds.

Reproduce locally (requires Ollama + the LOCOMO10 dataset under `test/`):

```bash
go test -tags integration ./test/ -run TestLocomo10Recall -v
```

### Comparison (2026 memory systems)

| System | GitHub Stars | LOCOMO | LongMemEval | Recall@5 | P95 Latency | Deploy | Language |
|--------|-------------|--------|-------------|----------|-------------|--------|----------|
| **MemHop** | — | — | — | **100%** ² | 3.97s ¹ | Embedded .meh | **Go** |
| ZeroMemory | ~200 | 96.1% | — | — | — | Embedded | — |
| MemoryLake | ~500 | 94.03% | — | — | — | SaaS/OSS | Python |
| Zep/Graphiti | ~5k | 94.7%\* | 90.2% | — | 0.63s | Go core | Go/Python |
| Mem0 2026 | ~51k | 92.5% | 93.4% | — | 1.44s | SaaS/OSS | Python |
| Hindsight | ~800 | 92.0% | 94.6% | — | — | OSS/MCP | Python |
| EverMemOS | ~300 | 92.32% | — | — | — | OSS | Python |
| ByteRover | ~100 | 92.2% | 92.8% | — | 1.6s | SaaS | — |
| Dakera | ~500 | 87.8% | — | — | — | Self-host | Rust+Go SDK |
| MemMachine | ~1.5k | 84.87% | — | — | — | OSS | Python |
| Cognee | ~28k | 80.3% | — | — | — | OSS | Python |
| Letta | ~13k | — | — | — | — | OSS | Python |
| agentmemory | ~20k | — | — | 95.2% | — | Embedded TS | TypeScript |
| MemPalace | ~41k\* | — | — | 96.6% | — | Local | JS/TS |
| engram | ~150 | — | — | — | — | Embedded Go | Go |
| OMEGA | ~300 | — | — | — | <50ms | Local MCP | Python |
| LangMem | ~500 | 58.1% | — | — | — | Embedded | Python |

² LOCOMO10-subset retrieval-only recall, NOT directly comparable with end-to-end QA Accuracy (the LOCOMO column) · \* Zep LOCOMO is self-reported; MemPalace star count is disputed (bot inflation)

## Project Structure

```
api/                              ← Public API (Open, Search, BatchStore, Dream, L0-L5)
internal/
├── common/
│   ├── config/                   ← Configuration
│   ├── hash/                     ← xxhash
│   ├── mherrors/                 ← Error types
│   ├── numeric/                  ← f16, cosine
│   ├── strutil/                  ← String utils
│   └── timeutil/                 ← Time utils
├── core/
│   ├── index/                    ← L1 reverse, L2 meta, L3, sparse, entity, tokenizer, vector
│   ├── model/                    ← profile, hypergraph, scene_node, archive, enums
│   ├── record/                   ← L0, L4, L5, graph, topic
│   └── storage/                  ← V2 .meh engine (header, mmap, compact, snapshot)
└── query/
    ├── crud/                     ← L0-L5 CRUD
    ├── dream/                    ← Dream pipeline (compress, emotion, l0_distill, l0_form, l1_decay, l1_rebuild, llm, pipeline)
    ├── encoder/                  ← Ollama HTTP embedding client
    ├── graph/                    ← L3 graph (bfs, community, dsl, mutate, store, subgraph)
    ├── health/                   ← Encoder health check
    ├── importx/                  ← Document import
    ├── search/                   ← RRF search (orchestrator, pipeline, rrf, search)
    ├── session/                  ← Session management
    └── write/                    ← Batch store + update
```

## Development

```bash
go build ./api/... ./internal/...          # Build
go test ./api/... ./internal/...           # Unit tests
go test ./test/...                         # Integration tests (requires Ollama)
go vet ./...                               # Static analysis
```

## Changelog

| Version | Date | Highlight | Core Changes |
|---------|------|-----------|--------------|
| v0.54–v0.58 | 2026-07-16 ~ 07-23 | Go Rewrite | v0.58: Unified RRF — additive scene bonuses, three-channel fusion, L6 removed, atomic.Pointer · v0.57: Dream narrowed to L0+L1+L2, LLM hardening, L5 Write API, SkipDistill · v0.55: Stability — IVF removed, panic→error, crash recovery, L5 write pipeline · v0.54: Go foundation — 4-layer arch, V2 .meh storage, 2 deps, log/slog |
| v0.18–v0.63 | 2026-05-31 ~ 07-10 | Rust | V2 append-only `.meh` with snapshot/checkpoint · BM25 + IVF hybrid retrieval · L3 hypergraph DSL, community detection (clique + Louvain), BFS/caching · Full Dream pipeline: L3 distill → L2 compress → L1 decay → L0 rebuild → L5 crystallize · FFI (cdylib), MCP Server, gRPC/Unix Socket encoder |
| v0.6–v0.17 | 2026-05-20 ~ 05-25 | Rust Early | Pure Rust single crate (dropped Python bindings) · LMDB to custom `.meh` storage migration · 4-layer to 6-layer cognitive architecture evolution · MCP Server integration · HNSW vector index (replaced brute-force) |
| v0.1–v0.5 | 2026-05-19 ~ 05-24 | Python | Hopfield associative memory network · LMDB embedded storage, `pip install` one-click · O(1) associative recall with confidence scoring · BrainLoop self-circulating agent loop · Proved "living memory" concept |

## Links

| | |
|---|---|
| MeowAgent | [github.com/meowagent/meowagent](https://github.com/meowagent/meowagent) |
| MemHop | [github.com/qyiun666/MemHop](https://github.com/qyiun666/MemHop) |
| MeowDesk | [github.com/qyiun666/MeowDesk](https://github.com/qyiun666/MeowDesk) |
| Website | [qyiun666.github.io/meowagent.github.io](https://qyiun666.github.io/meowagent.github.io/) |
| Email | qyiun666@163.com |

## License

MIT OR Apache-2.0
