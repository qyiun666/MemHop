<p align="center">
  <h1 align="center">MemHop</h1>
  <p align="center">
    <strong>Long-term memory for AI agents — a seven-layer cognitive memory database in a single embedded file. Pure Go, zero infrastructure.</strong>
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
  <strong>Current: v1.3.2 · Latest stable tag: v1.3.2</strong>
</p>

---

MemHop is an **embedded long-term memory database for AI agents and LLM applications**, written in pure Go. It is not a vector database — it is a memory system modeled after how the human brain organizes knowledge, with identity, episodic recall, semantic compression, a knowledge graph, archival storage, and crystallized skills. One agent, one `.meh` file, zero infrastructure.

MemHop is an **agent-dedicated** memory database: each agent binds to exactly one `.meh` file, and a file-level exclusive lock guarantees a single instance per file (a second `Open` fails fast). It runs on **Linux, macOS, and Windows** with no cgo and no external services beyond your embedding/LLM endpoints.

Built as the brain memory of [MeowAgent](https://github.com/meowagent/meowagent) (coming soon), MemHop works as an embedded organ rather than a standalone service. No server to run, no configuration to manage — just open a file and your agent has memory.

> **Our stance on agent memory.** Memory should not be an afterthought bolted on with a vector database plugin or a plain-text log dumped into a context window. An agent without internalised memory is just a stateless function pretending to be intelligent. MemHop exists because we believe memory must be *cognitive* — structured, compressed, consolidated, and forgotten the way a human brain does — and *embedded* — living inside the agent process itself, not behind a network call. One file, zero infrastructure, a mind that grows with every conversation.

## Features

- **Seven-Layer Architecture** — L0 Profile → L1 Engram → L2 Context → L3 Knowledge → L4 Archive → L5 Crystal → L7 Trajectory (L6 scene usage folded into L2), with Dream consolidation
- **Three-Channel RRF Retrieval** — BM25 (gse CJK) + f32 vector + fuzzy entity/term matching (entity index auto-fed from indexed topic terms), fused via Reciprocal Rank Fusion (k=60)
- **V2 Storage** — `.meh` format (`FormatVersion=0x0007`) with A/B dual headers, per-record CRC32 + torn-write truncation recovery, mmap zero-copy, snapshot/checkpoint. **Not compatible with v1 `.meh` data files** (JSON serialization switched to native numbers); 0x0005 introduced the 0x0F Capability record, 0x0006 re-designed the capability payload as the v2 mcp/skill/composite resource-wrapper model, 0x0007 folded the L6 scene-usage record into the L2 scene slot, removed the L1 reverse index from the snapshot and added real L1 hyperedge creation — files with 0x0006 (or older) are rejected at Open with no migration path
- **L1 Scene Hypergraph + Spreading Activation** — Dream creates co-occurrence hyperedges between scenes whose topic keyword sets overlap (Jaccard ≥ `L1EdgeMinSimilarity`); Search association walks the graph from the hit scene, propagating activation (× edge weight × dampening per hop) and returns the top associated scenes' topics as `AssociatedContexts` — real cross-scene associative recall ("联想记忆"), with edge weights decayed and pruned by the Dream pipeline
- **Dream Pipeline** — five stages over L0–L2: L2 compress → L1 rebuild → L1 decay → L0 profile → L0 distill (emotion/MBTI)
- **L3 Knowledge Graph** — Multiple independent hypergraphs with node/edge import, CRUD, keyword/type lookup and BFS subgraph queries
- **Single Instance by Design** — one agent = one `.meh` file, enforced by a cross-platform file lock (linux/darwin/windows)
- **Minimal & Embeddable** — 4 direct Go deps (xxhash, gse, go-openai, go-sdk); Ollama is accessed through its plain HTTP API, no Ollama SDK dependency, `sync.RWMutex` + `atomic.Pointer`, zero infrastructure
- **MCP Server** — `cmd/memhop-mcp` exposes the full public API as 32 MCP tools over multi-tenant HTTP (SSE + streamable-http, official `modelcontextprotocol/go-sdk`): one process serves many hosts, each isolated by URL path `/mcp/<tenant-id>` into its own `.meh` file
- **Single Agent, Single File** — one agent = one `.meh` file, no server process, no background daemon

## Quick Start

> Full integration guide (config, all layer APIs, N:N turns, pitfalls):
> [INTEGRATION_GUIDE.md](INTEGRATION_GUIDE.md) · 中文: [INTEGRATION_GUIDE.zh.md](INTEGRATION_GUIDE.zh.md)

```go
import (
    "context"
    "fmt"
    "log"
    "os"
    "time"

    memhop "github.com/qyiun666/MemHop/api"
)

db, err := memhop.Open(&memhop.MemHopConfig{
    DBPath:      "agent.meh",
    VectorDim:   1024,
    EncoderAddr: "http://127.0.0.1:11434",
    EmbedModel:  "qllama/bge-m3:q4_k_m",
    LLM: memhop.LlmConfig{ // required: validated at Open
        APIURL: "https://api.openai.com/v1",
        APIKey: os.Getenv("OPENAI_API_KEY"),
        Model:  "gpt-4o-mini",
    },
    Defaults: *memhop.DefaultMemHopDefaults,
})
if err != nil {
    log.Fatal(err)
}
defer db.Close()

// Search — three routes: AutoCreate (skip retrieval, new scene+topic),
// DirectedL2ID (append to a specific scene), or default three-channel retrieval.
// Timestamp is required: Unix milliseconds of the message. ctx cancels LLM
// keyword extraction, encoder calls and any internally triggered Dream.
res, err := db.Search(ctx, memhop.SearchQuery{
    Text:      "What did we discuss?",
    Timestamp: time.Now().UnixMilli(),
})
if err != nil {
    log.Fatal(err)
}

// Append the agent reply to the topic created by Search.
// Update takes the topic ID as a 16-char hex string (NewTopicID is uint64).
topicID := fmt.Sprintf("%016x", res.NewTopicID)
if err = db.Update(topicID, "Agent: ...", time.Now().UnixMilli()); err != nil {
    log.Fatal(err)
}

// Dream consolidation over active scenes (L0-L2); sceneID "" = all active scenes.
ok, err := db.Dream(context.Background(), "")
```


> **Concurrency contract.** A `*DB` is a single-agent handle. The host must serialize Search / Update / Dream / write APIs on one DB instance. The MCP server keeps the same contract per tenant: one tenant = one `.meh` file, serialized through its own `*DB`.

Prerequisites: Go 1.26+, Ollama (`ollama pull qllama/bge-m3:q4_k_m`), an OpenAI-compatible LLM endpoint (`Config.LLM` is required)

### API Overview

| Group | Methods |
|-------|---------|
| Core loop | `Search(ctx, q)` · `Update` · `Dream(ctx)` · `Checkpoint` · `Close` |
| L0 Profile | `GetL0` · `UpdateL0` |
| L2 Context | `ListScenes` · `SceneContext` · `ActiveSceneIDs` · `MergeScenes` · `DeleteTopic` · `DeleteScene` · `RefineTopicKeywords(ctx, id)` |
| L3 Knowledge | `GetL3` · `ListL3` · `ImportL3` · `UpdateL3` · `DeleteL3` · `QueryL3Nodes` · `QueryL3Subgraph` |
| L4 Archive | `SearchL4` · `GetArchive` · `AppendL4Message` |
| L5 Capability | `ImportCapability` · `GetCapability` · `UpdateCapability` · `DeleteCapability` · `ListCapabilities` · `ActivateCapability` · `RecordCapabilityUsage` |
| L7 Trajectory | `AppendTrajectory` · `ReadTrajectory` · `TrajectoryStats` · `DeleteTrajectory` · `Crystallize` |

### Built-in L5 Capabilities

The root `capabilities/` directory ships a ready-to-use capability toolbox (`memhop-capability/v2`), embedded into the library at build time — **19 cards in two groups**: MemHop's own API manuals (`manual`, 13 cards: guide, search, update, dream, trajectory, crystallize, capability-import, profile, scene, archive, capability, knowledge, refine — covering every public API except `Open`/`Close`/`Dream`/`Update`/`Search` and L5 reads) and atomic capability cards a harness/agent is expected to have (`atomic`: file read/write/edit, command execution, file search, web search). Manual cards reference the Go API directly (`type: "api"`, `ref: "api:MethodName"`) — the host calls the methods on `*api.DB` with no MCP layer involved. **Zero config, zero writes**: `ListCapabilities` / `GetCapability` serve the built-in toolbox directly (same status/type/keyword filters as stored records), so the host LLM can fetch and consult it. Built-ins are read-only, never persisted to the `.meh` file, dedupe by ID against stored same-name records (stored wins), and are NOT attached to `Search` responses — retrieval returns stored matches only.

## Architecture

```
Layer   Name             Human Parallel          Mechanism
─────   ──────────────   ───────────────────     ─────────────────────────────────────────────
 L7     Trajectory       Procedural log          Host-appended operation events; crystallized into L5 capability drafts
 L5     Crystal          Muscle memory           Reusable capability packages (skills · MCP · tools · prompts · services)
 L4     Archive          Long-term memory        Raw dialogue logs & historical records
 L3     Knowledge        Semantic memory         Multi-source hypergraph knowledge base
 L2     Context          Working memory          Compressed topic structures (4 depth levels)
 L1     Engram           Scene hypergraph        Scene nodes + keyword-overlap hyperedges; activation spreads here during Search association
 L0     Profile          Identity                Agent personality, preferences & language habits
```

### Dream Pipeline

The Dream cycle is an automatic memory consolidation process inspired by how the human brain processes experiences during sleep. It operates on **L0–L2 only** (L3 distillation and L5 crystallization are out of scope by design) and runs five stages:

1. **L2 Compression** — LLM groups and merges related topics, one goroutine per active scene, demotes stale contexts
2. **L1 Rebuild** — Sync L1 scene nodes from L2, rebuild search indexes, and create/refresh keyword-overlap hyperedges between scenes in the same pass
3. **L1 Decay** — Decay scene importance and edge weights, prune weak nodes
4. **L0 Profile** — Regenerate the agent profile from consolidated memory
5. **L0 Distill** — Distill emotion/MBTI patterns (always runs; skipped automatically when no L1 samples exist)

`Dream(ctx) (bool, error)` takes the write lock for the whole cycle, returns success immediately when no scenes are active, and honors `ctx` cancellation between stages.

### Search

`Search` dispatches to one of three routes: `AutoCreate` (skip retrieval, create a fresh scene+topic), `DirectedL2ID` (append to a specific scene), or the default retrieval route (optionally scoped by `DirectedL3ID`). The retrieval route uses **three-channel RRF fusion** (BM25 + vector + entity fuzzy terms):

| Channel | Method                                                              |
| ------- | ------------------------------------------------------------------- |
| BM25    | Keyword matching via inverted index (gse CJK tokenization)          |
| Vector  | Semantic similarity with f32 single-precision via Ollama HTTP embed |
| Entity  | Fuzzy term/entity matching over indexed topic terms (BK-Tree, edit distance ≤ 2) |

Post-fusion: keyword-overlap scoring, additive scene bonuses for active/recent scenes, then L1 spreading activation (cross-scene associative recall over the scene hypergraph) + L5 capability matching + L0 profile assembly.


`SearchResult` returns `Contexts` (the hit scene's depth-≤1 topics, each carrying `L4Refs`) and `AssociatedContexts` (topics from the L1-associated scene); hosts pull the L4 original text via `SceneContext` or `SearchL4`.

When `Search` creates a topic it also matches relevant L3 knowledge nodes and writes their graph IDs into `TopicSlot.L3Refs`; `DirectedL3ID` filters topics on these refs.
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
api/                         ← Public facade: DB handle (open/search/update/dream/l0–l5/l7) + type aliases/constructors
internal/                    ← Business assembly: config / db / defaults / l0 / l2 / l3 / l3query /
                               l4 / l5 / l7 / search / update / dream / scenefind / llm_client / llm_ops / encoder
internal/repo/               ← Data layer: l0layer–l5layer + l7layer (record read/write, vectors)
internal/repo/index/         ← Index layer: sparse (BM25) / l1_reverse / l2meta / l3_index /
                               entity / rebuild / tokenizer (gse)
internal/repo/core/          ← .meh engine: engine / frame / header / snapshot / reclaim /
                               record / model / mmap / filelock
internal/common/             ← Bottom-level utils: bktree / cosine / enum / errors / hash /
                               sliceutil / strutil / vec
test/                         ← Integration tests (build tag: integration)
benches/fixtures/             ← Benchmark datasets (locomo10, locomo_smoke, longmemeval_smoke)
```

Dependency direction is strictly one-way: `api → internal → repo → core`, with `common` at the bottom (no references to any other internal package).


> Note: `docs/` and `AGENTS.md` are intentionally kept local-only (see `.gitignore`), so links under `docs/` may not resolve in a public clone.

### LLM Call Cost Model

- **Hot path** (`Search` + `Update`): one small keyword-extraction call each, capped at 512 output tokens. Typical cost is low; latency is the more visible factor.
- **Dream**: one consolidation call per active scene with at least 20 topics (active-scene set bounded by `Capacity`, default 7), plus one distill call with at most 200 ranked L1 samples (up to 20 keywords each). Output caps: 8192 / 2048 tokens.
- **Crystallize**: one explicit, host-triggered call per session trajectory.
- Use a small/fast chat model (e.g. a local Ollama model or a cheap API model) for the configured LLM when latency and cost matter; keyword extraction does not need a frontier model.

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
| v1.3.2 | 2026-08-26 | API fixes: async Dream + deletion + Update simplification | Search/Update no longer block on an internally triggered Dream (background goroutine, per-scene in-flight dedup, Close cancels a pending Dream) · new `DeleteTopic` (subtree closure + L4 + indexes + parent ChildrenIDs pruning) and `DeleteScene` (scene + all topics + archives + L1 node + active set) for memory correction · `Update` returns `error` instead of `(bool, error)` · `SearchResult.ProfileBrief` — compact profile digest (name/role/top preferences/style/emotions, bounded) · no format change (stays `0x0007`) · MCP tool set unchanged (32) ·
| v1.3.0 | 2026-08-26 | L1 scene hypergraph + spreading-activation association | Dream creates real `RecL1Hyperedge` co-occurrence edges between scenes (keyword-overlap Jaccard ≥ `L1EdgeMinSimilarity`); Search `AssociatedContexts` replaced the no-op same-scene listing with a graph walk (activation × edge weight × dampening per hop, ≤ `L1EdgeMaxHops`, top `L1AssocMaxScenes` other scenes) · L6 scene-usage record removed — hit counters folded into the L2 `SceneSlot` (`HitCount`/`LastHitAt`) · `L1ReverseIndex` (incl. snapshot field) and 4 dead L1 functions removed; association is now a pure storage-level graph read · `.meh` format bumped to `0x0007` — 0x0006 files are rejected at Open, no migration · new defaults: `L1EdgeMinSimilarity` (0.15), `L1EdgeMaxHops` (2), `L1ActivationDampening` (0.5), `L1ActivationThreshold` (0.05), `L1AssocMaxScenes` (3)
| v1.2.7 | 2026-08-25 | Host alignment + bilingual integration guides | `Search(ctx, q)` and `RefineTopicKeywords(ctx, id)` accept a context (cancels LLM extraction, encoder calls, internally triggered Dream) · `api` exports `LlmConfig` / `MemHopDefaults` / `TopicSlot` / `ResourceRef` / `CrystallizeDetail` / `TrajectoryStats` · new `TrajectoryStats` (per-session L7 stats) + `memhop_trajectory_stats` MCP tool (31 → 32 tools) · `CrystallizeResult.Details` — per-candidate create/reuse/merge/skip disposition · `AppendL4Message` (pure L4 append, no LLM) · active-scene capacity: Update triggers a Dream on the oldest scene at Capacity with a compressibility pre-check; `SearchDreamContextThreshold` zero-value guard · bilingual integration guides added at repo root (`INTEGRATION_GUIDE.md` / `INTEGRATION_GUIDE.zh.md`) |
| v1.2.5 | 2026-08-20 | MCP server rewritten | `cmd/memhop-mcp` fully rewritten against the `api` facade (v1.2.4 removed it): all 31 MCP tools map 1:1 to `api.DB` methods · multi-tenant HTTP — SSE + streamable-http (2025-03-26 spec, stateless), each tenant isolated by URL path `/mcp/<tenant-id>` into its own `.meh` file, lazy-open registry with a first-open mutex · all tool outputs serialize record IDs as 16-char hex strings (uint64 JSON numbers lose precision in JS/TS hosts) · tenant-ID whitelist + path-traversal rejection (defense in depth) · LLM credentials via env vars only (no CLI flag) · go-sdk v1.7.0 back as a direct dep (3 → 4) · offline tests for config/registry/tools/streamable + multi-tenant SSE smoke · codebase cleanup: dropped redundant enum JSON helpers (default `~uint8` JSON behavior is identical), `CodeOf` migrated to Go 1.26 `errors.AsType`, scalar cosine loop (2.7× faster at 1024 dims), deleted the `internal/repo/open.go` forwarding layer (17 funcs + 8 aliases; internal calls core/index directly), removed the `ParseID→FormatHash` round-trip in Update |
| v1.2.4 | 2026-08-19 | api/ facade + internal/ flattening | Public Go API moved from the root package to `github.com/qyiun666/MemHop/api` (root `memhop.go`/`types.go` removed) · `internal/sub/` flattened into `internal/` (`package sub` → `package internal`), `internal/sub/repo` → `internal/repo`, `internal/sub/common` → `internal/common` · `cmd/memhop-mcp` removed (rewritten in v1.2.5) · build config (Makefile fmt, pre-commit hook, CI gofmt) updated · breaking change: hosts importing the root package must switch to `/api` |
| v1.2.3 | 2026-08-18 | MCP compatibility fixes + DSH integration + retrieval quality | MCP tool schemas fixed (no-arg tools no longer emit `properties: null`, breaking strict clients) · all tool outputs render record IDs as 16-char hex strings (uint64 JSON numbers lose precision in JS/TS hosts, breaking `new_topic_id` round-trips) · new `--transport streamable-http` (2025-03-26 spec, stateless multi-tenant; supported by DSH's dsh-mcp-client) · DeepSeek Harness integration guide + agent instructions (`docs/dsh/`) · streamable-http smoke test · keyword-extraction prompt overhauled (semantic completeness + colloquial variants + phrases) + Search returns all relevance-ordered topics (scene-context truncation removed), LoCoMo recall 0.392 → 0.668, entity_hit 0.284 → 0.877 |
| v1.2.1 | 2026-08-16 | MCP server + L5 capability layer | New `cmd/memhop-mcp` binary: multi-tenant SSE MCP server (official go-sdk v1.7.0) mapping the full public API to 28 tools (search/update/dream/checkpoint/status, profile, scenes, knowledge, archive, capabilities, trajectory/crystallize) · tenant path isolation `/mcp/<tenant-id>` · graceful shutdown persists via snapshot · offline SSE smoke tests (`make test-mcp`) · usage docs under `docs/mcp/` (local) · L5 plugin layer refactored into the capability layer (`memhop-capability/v1`: manual/atomic/composite kinds, draft→active lifecycle via `ActivateCapability`, fingerprint dedup, Crystallize emits create/reuse/merge candidates) · built-in capability toolbox (`capabilities/`, embedded, read-only, attached at Open) · `Update` returns `(bool, error)` · `.meh` format bumped to `0x0005` — 0x0004 files (v1.2.0 plugin records) are rejected at Open, no migration · encoder health check requires a 2xx HEAD on the endpoint root (no fallback) · active scenes bounded by `Capacity` (default 7, oldest evicted from Dream targets) · `RecordEnd` header field + A/B header damage recovery |
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
| Meowire | [github.com/qyiun666/meowire](https://github.com/qyiun666/meowire) |
| MeowDesk | [github.com/qyiun666/MeowDesk](https://github.com/qyiun666/MeowDesk) — coming soon |
| Website | [qyiun666.github.io/meowagent.github.io](https://qyiun666.github.io/meowagent.github.io/) |
| Email | qyiun666@163.com |

<p align="center">⭐️ <a href="https://github.com/qyiun666/MemHop">Star MemHop on GitHub</a> — your support keeps us building!</p>

## License

MIT OR Apache-2.0
