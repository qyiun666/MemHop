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
  <img src="https://img.shields.io/badge/go-1.25+-00ADD8.svg" alt="go">
  <img src="https://img.shields.io/badge/test-passing-brightgreen.svg" alt="test">
</p>

---

MemHop is not a vector database. It is a memory system modeled after how the human brain organizes knowledge — with identity, episodic recall, semantic compression, skill acquisition, archival storage, and crystallized expertise. One agent, one `.meh` file, zero infrastructure.

Built as the brain memory of [MeowAgent](https://github.com/meowagent/meowagent), MemHop works as an embedded organ rather than a standalone service. No server to run, no configuration to manage — just open a file and your agent has memory.

> **Our stance on agent memory.** Memory should not be an afterthought bolted on with a vector database plugin or a plain-text log dumped into a context window. An agent without internalised memory is just a stateless function pretending to be intelligent. MemHop exists because we believe memory must be *cognitive* — structured, compressed, consolidated, and forgotten the way a human brain does — and *embedded* — living inside the agent process itself, not behind a network call. One file, zero infrastructure, a mind that grows with every conversation.

## Features

- **Six-Layer Architecture** — L0 Profile → L1 Engram → L2 Context → L3 Knowledge → L4 Archive → L5 Crystal, with Dream consolidation
- **Three-Channel RRF** — BM25 (gse CJK) + f16 vector + entity fuzzy matching, fused via Reciprocal Rank Fusion (k=60)
- **V2 Storage** — `.meh` format with A/B dual headers, CRC32 integrity, mmap zero-copy, snapshot/checkpoint
- **Dream Pipeline** — L3 distill → L2 compress → L1 rebuild/decay → L0 profile → language habits → L5 crystallize
- **L3 Knowledge Graph** — Multi-hypergraph with community detection (clique + Louvain), BFS, adjacency caching
- **Minimal & Embeddable** — Only 2 direct Go deps (xxhash, gse), `sync.RWMutex` + `atomic.Pointer`, zero infrastructure

## Quick Start

```go
import "memhop"

db, err := memhop.Open(&memhop.Config{
    DBPath:      "agent.meh",
    VectorDim:   768,
    EncoderAddr: "http://127.0.0.1:11434",
    EmbedModel:  "nomic-embed-text",
    LLM: memhop.LlmConfig{
        APIURL: "https://api.openai.com/v1",
        APIKey: os.Getenv("OPENAI_API_KEY"),
        Model:  "gpt-4o-mini",
    },
})
if err != nil {
    log.Fatal(err)
}
defer db.Close()

// Search
results, _ := db.Search(memhop.SearchQuery{Text: "What did we discuss?", MaxResults: 10})

// Store
db.BatchStore(memhop.StoreBatch{Items: []memhop.StoreItem{{Content: "User: ...\nAgent: ..."}}})

// Dream consolidation
report, _ := db.Dream(nil)
```

Prerequisites: Go 1.25+, Ollama (`ollama pull nomic-embed-text`)

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

The Dream cycle is an automatic memory consolidation process inspired by how the human brain processes experiences during sleep:

1. **L3 Distillation** — Extract structured knowledge via LLM
2. **L2 Compression** — Demote old contexts, merge topics
3. **L1 Rebuild & Decay** — Rebuild hypergraph, decay episodic importance
4. **L0 Profile Rebuild** — Regenerate agent profile with emotion/MBTI patterns
5. **Language Habit Learning** — Discover vocabulary and style patterns
6. **L5 Crystallization** — Extract reusable procedures

### Search

MemHop uses **three-channel retrieval fusion** (BM25 + vector + entity) with RRF:

| Channel | Method                                                                   |
| ------- | ------------------------------------------------------------------------ |
| BM25    | Keyword matching via inverted index (gse CJK tokenization)       |
| Vector  | Semantic similarity with f16 half-precision via Ollama HTTP `/api/embed` |
| Entity  | Fuzzy entity name matching for knowledge graph queries                   |

Post-fusion: additive scene bonuses for active/recent sessions, then L1 association expansion + L5 crystal matching + L0 profile assembly.

## Benchmarks

Tested on [LOCOMO10](https://github.com/snap-research/LOCOMO) (ACL 2024) — 419 turns stored, 199 QA queries across 5 categories:

| Metric | Result |
|--------|--------|
| Recall@1 | **100.0%** |
| Recall@5 | **98.5%** |
| P95 Latency | **1.24s** |

### Comparison

| Dimension | MemHop | agentmemory | SimpleMem | TencentDB Agent Memory |
|-----------|--------|-------------|-----------|------------------------|
| Deploy | Embedded .meh | npm + SQLite | pip + FAISS | Plugin + SQLite |
| Architecture | L0–L5 cognitive + RRF | 4-layer + Hook auto-capture | 3-stage semantic compression | 4-tier semantic pyramid |
| Retrieval | BM25 + f16 vector + entity | BM25 + vector + KG + RRF | FAISS + BM25 hybrid | Hierarchical semantic search |
| Retrieval Rate | **98.5% R@5** ¹ | 95.2% recall ² | F1 0.613 ³ | — ⁴ |
| Language | **Go** | TypeScript | Python | TypeScript |
| Stars | — | 23K+ | 3.7K+ | 4.5K+ |

¹ LOCOMO10 Recall@5 · ² LongMemEval-S Recall · ³ LOCOMO multimodal F1 · ⁴ Reports 61% token reduction, no standard retrieval rate published

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
| MemHop | [github.com/qyiun666/memhop](https://github.com/qyiun666/memhop) |
| MeowDesk | [github.com/qyiun666/MeowDesk](https://github.com/qyiun666/MeowDesk) |
| Website | [qyiun666.github.io/meowagent.github.io](https://qyiun666.github.io/meowagent.github.io/) |
| Email | qyiun666@163.com |

## License

MIT OR Apache-2.0
