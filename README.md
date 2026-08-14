<p align="center">
  <h1 align="center">MemHop</h1>
  <p align="center">
    <strong>Long-term memory for AI agents — an eight-layer cognitive memory database in a single embedded file. Pure Go, zero infrastructure.</strong>
  </p>
  <p align="center">
    <a href="README.zh.md">中文</a>
    &middot;
    <a href="https://qyiun666.github.io/meowagent.github.io/">Website</a>
    &middot;
    <a href="https://github.com/meowagent/meowagent">MeowAgent (coming soon)</a>
  </p>
</p>

<p align="center">
  <a href="https://github.com/qyiun666/MemHop/actions/workflows/workflow.yml"><img src="https://github.com/qyiun666/MemHop/actions/workflows/workflow.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/qyiun666/MemHop"><img src="https://pkg.go.dev/badge/github.com/qyiun666/MemHop.svg" alt="Go Reference"></a>
  <img src="https://img.shields.io/badge/go-1.26+-00ADD8.svg" alt="go">
  <img src="https://img.shields.io/badge/license-MIT%20OR%20Apache--2.0-blue.svg" alt="license">
</p>

<p align="center">
  <strong>Current: v1.2.0 (L5 plugin layer) · Latest stable tag: v1.0.1</strong>
</p>

---

MemHop is an **embedded long-term memory database for AI agents and LLM applications**, written in pure Go. It is not a vector database — it is a memory system modeled after how the human brain organizes knowledge, with identity, episodic recall, semantic compression, a knowledge graph, archival storage, and crystallized skills. One agent, one `.meh` file, zero infrastructure.

MemHop is an **agent-dedicated** memory database: each agent binds to exactly one `.meh` file, and a file-level exclusive lock guarantees a single instance per file (a second `Open` fails fast). It runs on **Linux, macOS, and Windows** with no cgo and no external services beyond your embedding/LLM endpoints.

Built as the brain memory of [MeowAgent](https://github.com/meowagent/meowagent) (coming soon), MemHop works as an embedded organ rather than a standalone service. No server to run, no configuration to manage — just open a file and your agent has memory.

> **Our stance on agent memory.** Memory should not be an afterthought bolted on with a vector database plugin or a plain-text log dumped into a context window. An agent without internalised memory is just a stateless function pretending to be intelligent. MemHop exists because we believe memory must be *cognitive* — structured, compressed, consolidated, and forgotten the way a human brain does — and *embedded* — living inside the agent process itself, not behind a network call. One file, zero infrastructure, a mind that grows with every conversation.

## Features

- **Eight-Layer Architecture** — L0 Profile → L1 Engram → L2 Context → L3 Knowledge → L4 Archive → L5 Crystal → L6 Scene Usage → L7 Trajectory, with Dream consolidation
- **Three-Channel RRF** — BM25 (gse CJK) + f32 vector + entity fuzzy matching, fused via Reciprocal Rank Fusion (k=60)
- **V2 Storage** — `.meh` format (`FormatVersion=0x0004`) with A/B dual headers, per-record CRC32 + torn-write truncation recovery, mmap zero-copy, snapshot/checkpoint. **Not compatible with v1 `.meh` data files** (JSON serialization switched to native numbers)
- **Dream Pipeline** — five stages over L0–L2: L2 compress → L1 rebuild → L1 decay → L0 profile → L0 distill (emotion/MBTI)
- **L3 Knowledge Graph** — Multi-hypergraph with community detection (clique + Louvain), BFS, adjacency caching
- **Single Instance by Design** — one agent = one `.meh` file, enforced by a cross-platform file lock (linux/darwin/windows)
- **Minimal & Embeddable** — 4 direct Go deps (xxhash, gse, ollama, go-openai), `sync.RWMutex` + `atomic.Pointer`, zero infrastructure

## Quick Start

```go
import (
    "context"
    "os"
    "time"

    memhop "github.com/qyiun666/MemHop/internal"
    "github.com/qyiun666/MemHop/internal/sub"
    "github.com/qyiun666/MemHop/internal/sub/common"
)

db, err := memhop.Open(&sub.MemHopConfig{
    DBPath:      "agent.meh",
    VectorDim:   1024,
    EncoderAddr: "http://127.0.0.1:11434",
    EmbedModel:  "qllama/bge-m3:q4_k_m",
    LLM: sub.LlmConfig{ // required: validated at Open
        APIURL: "https://api.openai.com/v1",
        APIKey: os.Getenv("OPENAI_API_KEY"),
        Model:  "gpt-4o-mini",
    },
    Defaults: *sub.DefaultMemHopDefaults,
})
if err != nil {
    log.Fatal(err)
}
defer db.Close()

// Search — three routes: AutoCreate (skip retrieval, new scene+topic),
// DirectedL2ID (append to a specific scene), or default three-channel retrieval.
// Timestamp is required: Unix milliseconds of the message.
res, err := db.Search(sub.SearchQuery{
    Text:      "What did we discuss?",
    Timestamp: time.Now().UnixMilli(),
})
if err != nil {
    log.Fatal(err)
}

// Append the agent reply to the topic created by Search.
// Update takes the topic ID as a hex string (common.FormatHash).
topicID := common.FormatHash(res.NewTopicID)
_ = db.Update(topicID, "Agent: ...", time.Now().UnixMilli())

// Dream consolidation over active scenes (L0-L2)
ok, err := db.Dream(context.Background())
```

Prerequisites: Go 1.26+, Ollama (`ollama pull qllama/bge-m3:q4_k_m`), an OpenAI-compatible LLM endpoint (`Config.LLM` is required)

### API Overview

| Group | Methods |
|-------|---------|
| Core loop | `Search` · `Update` · `Dream` · `Checkpoint` · `Close` |
| L0 Profile | `GetL0` · `UpdateL0` |
| L2 Context | `ListScenes` · `MergeScenes` |
| L3 Knowledge | `GetL3` · `ListL3` · `ImportL3` · `UpdateL3` · `DeleteL3` · `QueryL3Nodes` · `QueryL3Subgraph` |
| L4 Archive | `SearchL4` · `GetArchive` |
| L5 Plugin | `ImportPlugin` · `GetPlugin` · `DeletePlugin` · `ListPlugins` · `Crystallize` |

## Architecture

```
Layer   Name             Human Parallel          Mechanism
─────   ──────────────   ───────────────────     ─────────────────────────────────────────────
 L7     Trajectory       Procedural log          Host-appended operation events; crystallized into L5 plugins
 L6     Scene Usage      Retrieval feedback      Per-scene search hit counters feeding L1 decay
 L5     Crystal          Muscle memory           Reusable capability packages (skills · MCP · tools · prompts · services)
 L4     Archive          Long-term memory        Raw dialogue logs & historical records
 L3     Knowledge        Semantic memory         Multi-source hypergraph knowledge base
 L2     Context          Working memory          Compressed topic structures (4 depth levels)
 L1     Engram           Associative hypergraph  Hypergraph skeleton linking L2 contexts
 L0     Profile          Identity                Agent personality, preferences & language habits
```

### Dream Pipeline

The Dream cycle is an automatic memory consolidation process inspired by how the human brain processes experiences during sleep. It operates on **L0–L2 only** (L3 distillation and L5 crystallization are out of scope by design) and runs five stages:

1. **L2 Compression** — LLM groups and merges related topics, one goroutine per active scene, demotes stale contexts
2. **L1 Rebuild** — Rebuild the hypergraph skeleton linking L2 contexts (search indexes rebuilt in the same pass)
3. **L1 Decay** — Decay episodic importance, prune weak nodes/edges
4. **L0 Profile** — Regenerate the agent profile from consolidated memory
5. **L0 Distill** — Distill emotion/MBTI patterns (always runs; skipped automatically when no L1 samples exist)

`Dream(ctx) (bool, error)` takes the write lock for the whole cycle, returns success immediately when no scenes are active, and honors `ctx` cancellation between stages.

### Search

`Search` dispatches to one of three routes: `AutoCreate` (skip retrieval, create a fresh scene+topic), `DirectedL2ID` (append to a specific scene), or the default retrieval route (optionally scoped by `DirectedL3ID`). The retrieval route uses **three-channel fusion** (BM25 + vector + entity) with RRF:

| Channel | Method                                                              |
| ------- | ------------------------------------------------------------------- |
| BM25    | Keyword matching via inverted index (gse CJK tokenization)          |
| Vector  | Semantic similarity with f32 single-precision via Ollama HTTP embed |
| Entity  | Fuzzy entity name matching for knowledge graph queries              |

Post-fusion: keyword-overlap scoring, additive scene bonuses for active/recent scenes, then L1 association expansion + L5 plugin matching + L0 profile assembly.

## Benchmarks

### LoCoMo Retrieval Recall (v1.1.0)

Long-term conversational memory recall on [LoCoMo](https://github.com/snap-research/locomo) (ACL 2024), evaluated at the **retrieval layer only** (no answer generation): each QA is searched against the ingested `.meh` memory, and an LLM judge decides whether the returned context alone is enough to answer.

| Scope | Sessions | Turns | QA | Recall (answerable) | Entity hit |
|-------|----------|-------|-----|---------------------|------------|
| 3 conversations (conv-26/30/41) | 70 | 1,451 | 497 | 0.531 (264/497) | 0.945 |
| 1 conversation (conv-26) | 19 | 419 | 199 | 0.709 (141/199) | 0.883 |

- **Recall** covers all five LoCoMo categories, including 22.5% adversarial questions whose correct behavior is abstention (an unanswerable context is a correct outcome), so it is a conservative lower bound; answerable categories 1–4 estimate to ~0.69.
- **Entity hit** is a model-free metric: the fraction of QAs whose answer tokens appear in the retrieved context.
- `Search` returns the context to the host (e.g. MeowAgent) as the generation context; retrieval is not answer generation.

Reproduce:

```bash
# 1 conversation
go test -tags integration ./test/ -run '^$' -bench BenchmarkLocomoRecall -benchtime 1x
# 3 conversations
MEMHOP_LOCOMO_ITEMS=3 go test -tags integration ./test/ -run '^$' -bench BenchmarkLocomoRecall -benchtime 1x
```

Analysis and competitor positioning: [docs/benchmarks/locomo_recall_analysis.md](docs/benchmarks/locomo_recall_analysis.md)

## Project Structure

```
internal/                     ← Assembly layer: DB facade (open, search, update, dream, l0–l5)
internal/sub/                 ← Business assembly: config / db / defaults / search / update /
                                dream / scenefind / llm_client / llm_ops / encoder
internal/sub/repo/            ← Data layer: open + l0layer–l5layer (record read/write, vectors)
internal/sub/repo/index/      ← Index layer: sparse (BM25) / l1_reverse / l2meta / l3_index /
                                entity / rebuild / tokenizer (gse)
internal/sub/repo/core/       ← .meh engine: engine / frame / header / snapshot / reclaim /
                                record / model / mmap / filelock
internal/sub/common/          ← Bottom-level utils: bktree / cosine / enum / errors / hash /
                                sliceutil / strutil / vec
test/                         ← Integration tests (build tag: integration)
benches/fixtures/             ← Benchmark datasets (locomo10, locomo_smoke, longmemeval_smoke)
```

Dependency direction is strictly one-way: `internal → sub → repo → core`, with `common` at the bottom (no references to any other internal package).

## Development

```bash
go build ./...                          # Build
go vet ./...                            # Static analysis
go test ./internal/...                  # Unit tests (no external services)
go test -tags integration ./test/...    # Integration tests (requires Ollama + LLM key)
```

Integration tests run against real services (Ollama encoder + an OpenAI-compatible LLM). Configure the LLM via environment variables `MEMHOP_TEST_LLM_KEY` / `MEMHOP_TEST_LLM_URL` / `MEMHOP_TEST_LLM_MODEL` (defaults to the DeepSeek endpoint when only the key is set), or via `test/testsupport/key_config.json`.

## Changelog

| Version | Date | Highlight | Core Changes |
|---------|------|-----------|--------------|
| v1.2.0 | 2026-08-14 | L5 plugin layer | L5 action chains → plugin slots (PluginSlot + structured five-section manifest: skills / MCPs / tools / prompts / services) · path-only import via `ImportPlugin`, hand-written create/update removed · Crystallize dispatches plugins by type from L7 trajectories · `SearchResult.Crystals` → `Plugins` · eight-layer architecture (L0–L7) docs |
| v1.1.0 | 2026-07-27 ~ 08.11 | Architecture refactor | Layered `internal` rewrite (assembly → sub → repo → core/index/common) · f16 → f32 single-precision vectors · topic centroid vector retrieval · `BatchStore` removed · `Dream(ctx)` narrowed to `(bool, error)` · `.meh` format `0x0004`, incompatible with v1 data · integration tests rebuilt against the new internal API |
| v1.0.0 | 2026-07-26 | First stable release | Go rewrite with six-layer cognitive architecture, V2 .meh storage, BM25+vector+entity RRF search, Dream consolidation pipeline, L3 hypergraph with community detection. |
| v0.54–v0.58 | 2026-07-16 ~ 07-23 | Go Rewrite | v0.58: Unified RRF — additive scene bonuses, three-channel fusion, L6 removed, atomic.Pointer · v0.57: Dream narrowed to L0+L1+L2, LLM hardening, L5 Write API, SkipDistill · v0.55: Stability — IVF removed, panic→error, crash recovery, L5 write pipeline · v0.54: Go foundation — 4-layer arch, V2 .meh storage, 2 deps, log/slog |
| v0.18–v0.63 | 2026-05-31 ~ 07-10 | Rust | V2 append-only `.meh` with snapshot/checkpoint · BM25 + IVF hybrid retrieval · L3 hypergraph DSL, community detection (clique + Louvain), BFS/caching · Full Dream pipeline: L3 distill → L2 compress → L1 decay → L0 rebuild → L5 crystallize · FFI (cdylib), MCP Server, gRPC/Unix Socket encoder |
| v0.6–v0.17 | 2026-05-20 ~ 05-25 | Rust Early | Pure Rust single crate (dropped Python bindings) · LMDB to custom `.meh` storage migration · 4-layer to 6-layer cognitive architecture evolution · MCP Server integration · HNSW vector index (replaced brute-force) |
| v0.1–v0.5 | 2026-05-19 ~ 05-24 | Python | Hopfield associative memory network · LMDB embedded storage, `pip install` one-click · O(1) associative recall with confidence scoring · BrainLoop self-circulating agent loop · Proved "living memory" concept |

## Links

| | |
|---|---|
| MeowAgent | [github.com/meowagent/meowagent](https://github.com/meowagent/meowagent) — coming soon |
| MemHop | [github.com/qyiun666/MemHop](https://github.com/qyiun666/MemHop) |
| MeowDesk | [github.com/qyiun666/MeowDesk](https://github.com/qyiun666/MeowDesk) — coming soon |
| Website | [qyiun666.github.io/meowagent.github.io](https://qyiun666.github.io/meowagent.github.io/) |
| Email | qyiun666@163.com |

<p align="center">⭐️ <a href="https://github.com/qyiun666/MemHop">Star MemHop on GitHub</a> — your support keeps us building!</p>

## License

MIT OR Apache-2.0
