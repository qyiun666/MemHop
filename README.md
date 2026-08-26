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
  <strong>Current: v1.3.4 · Latest stable tag: v1.3.4</strong>
</p>

---

MemHop is an **embedded long-term memory database for AI agents and LLM applications**, written in pure Go. It is not a vector database — it is a memory system modeled after how the human brain organizes knowledge, with identity, episodic recall, semantic compression, a knowledge graph, archival storage, and crystallized skills. One agent, one `.meh` file, zero infrastructure.

MemHop is an **agent-dedicated** memory database: each agent binds to exactly one `.meh` file, and a file-level exclusive lock guarantees a single instance per file (a second `Open` fails fast). It runs on **Linux, macOS, and Windows** with no cgo and no external services beyond your embedding/LLM endpoints.

Built as the brain memory of [MeowAgent](https://github.com/meowagent/meowagent) (coming soon), MemHop works as an embedded organ rather than a standalone service. No server to run, no configuration to manage — just open a file and your agent has memory.

> **Our stance on agent memory.** Memory should not be an afterthought bolted on with a vector database plugin or a plain-text log dumped into a context window. An agent without internalised memory is just a stateless function pretending to be intelligent. MemHop exists because we believe memory must be *cognitive* — structured, compressed, consolidated, and forgotten the way a human brain does — and *embedded* — living inside the agent process itself, not behind a network call. One file, zero infrastructure, a mind that grows with every conversation.

## Features

- **Seven-Layer Architecture** — L0 Profile → L1 Engram → L2 Context → L3 Knowledge → L4 Archive → L5 Crystal → L6 Trajectory, with Dream consolidation
- **Three-Channel RRF Retrieval** — BM25 (gse CJK) + f32 vector + fuzzy entity/term matching (entity index auto-fed from indexed topic terms), fused via Reciprocal Rank Fusion (k=60)
- **V2 Storage** — `.meh` format (`FormatVersion=0x0008`) with A/B dual headers, per-record CRC32 + torn-write truncation recovery, mmap zero-copy, snapshot/checkpoint. Record frames carry an 8-byte `agent_id` (26-byte header) and the engine indexes every record by `(agent, idHash)` domain. **Not compatible with `0x0007` (or older) `.meh` data files** — they are rejected at Open with no migration path
- **Multi-Agent Domains** — `OpenMulti` + `CreateAgent(name)` / `Session(agentID)` / `ListAgents` / `DeleteAgent`: many agents share one `.meh` file with fully isolated per-agent domains (indices, active scenes, Dream pipelines, domain locks); same-agent operations serialize, different agents run in parallel; idle domains reclaim memory on access cadence (`Defaults.AgentIdleTTLMs`) while their records stay on disk. Single-agent hosts keep using `Open` unchanged (default domain)
- **L1 Scene Hypergraph + Spreading Activation** — Dream creates co-occurrence hyperedges between scenes whose topic keyword sets overlap (Jaccard ≥ `L1EdgeMinSimilarity`); Search association walks the graph from the hit scene, propagating activation (× edge weight × dampening per hop) and returns the top associated scenes' topics as `AssociatedContexts` — real cross-scene associative recall ("联想记忆"), with edge weights decayed and pruned by the Dream pipeline
- **Dream Pipeline** — five stages over L0–L2: L2 compress → L1 rebuild → L1 decay → L0 profile → L0 distill (emotion/MBTI)
- **L3 Knowledge Graph** — Multiple independent hypergraphs with node/edge import, CRUD, keyword/type lookup and BFS subgraph queries
- **Single Instance by Design** — one agent = one `.meh` file, enforced by a cross-platform file lock (linux/darwin/windows)
- **Minimal & Embeddable** — 4 direct Go deps (xxhash, gse, go-openai, go-sdk); Ollama is accessed through its plain HTTP API, no Ollama SDK dependency, `sync.RWMutex` + `atomic.Pointer`, zero infrastructure
- **MCP Server** — `cmd/memhop-mcp` exposes the full public API as 32 MCP tools over multi-tenant HTTP (SSE + streamable-http, official `modelcontextprotocol/go-sdk`): one process serves many hosts through one shared `.meh` file, each tenant isolated by URL path `/mcp/<tenant-id>` into its own agent domain (stable agentID per tenant name, `os.Root`-anchored db-dir)
- **Single Agent, Single File** — one agent = one `.meh` file by default, no server process, no background daemon; opt into multi-agent sharing with `OpenMulti`

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


> **Concurrency contract.** Same-agent operations (Search / Update / Dream / write APIs) are serialized by the library's per-agent domain lock; different agents run in parallel on a `*MultiAgentDB`, so the host needs no external queue. `*DB` is the single-agent handle bound to the default domain. The file's exclusive lock still allows only one process per `.meh` file; `Lock()`/`Unlock()` remain available for host-critical sections and panic on a closed DB.

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
| L6 Trajectory | `AppendTrajectory` · `ReadTrajectory` · `TrajectoryStats` · `DeleteTrajectory` · `Crystallize` |

### Built-in L5 Capabilities

The root `capabilities/` directory ships a ready-to-use capability toolbox (`memhop-capability/v3`), embedded into the library at build time — **19 cards in two groups**: MemHop's own API manuals (13 cards: guide, search, update, dream, trajectory, crystallize, capability-import, profile, scene, archive, capability, knowledge, refine — covering every public API except `Open`/`Close`/`Dream`/`Update`/`Search` and L5 reads) and atomic capability cards a harness/agent is expected to have (file read/write/edit, command execution, file search, web search). Manual cards reference the Go API directly (`type: "api"`, `ref: "api:MethodName"`) — the host calls the methods on `*api.DB` with no MCP layer involved. **Resources are tool declarations**: `name/desc/input/output` mirror the host tool spec (e.g. meowire `ToolSpec`) field-for-field, so a host projects them with a pure field copy and zero format conversion. **Zero config, zero writes**: `ListCapabilities` / `GetCapability` serve the built-in toolbox directly (same status/type/keyword filters as stored records), so the host LLM can fetch and consult it. Built-ins are read-only, never persisted to the `.meh` file, dedupe by ID against stored same-name records (stored wins), and are NOT attached to `Search` responses — retrieval returns stored matches only.

## Architecture

```
Layer   Name             Human Parallel          Mechanism
─────   ──────────────   ───────────────────     ─────────────────────────────────────────────
 L6     Trajectory       Procedural log          Host-appended operation events; crystallized into L5 capability drafts
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
## Testing & Benchmarks

MemHop's test suite exercises only the public `api` surface — exactly the calls a host (e.g. MeowAgent) makes — and asserts the engine's own memory structures, not external answerability judges.

### Integration tests (`test/`, build tag `integration`)

- **Memory loop** (`TestCoreCycleSearchUpdateDream`): N Search+Update cycles ingested the way a real host does, with **periodic L0/L1/L4 consistency checks** every few turns — L0 profile readable, L1 scene graph present (`ListScenes`/`SceneContext`), L4 holding the raw utterance verbatim. After Dream consolidation the scene must still expose consolidated topics and retrieval must surface the stored facts.
- **Keyword fidelity & persistence** (`TestKeywordFidelity`/`TestKeywordPersistence`/`TestDreamCompressionFidelity`): keywords extracted from a dialogue utterance faithfully carry its meaning, survive noise cycles, and stay faithful across Dream compression.
- **API contracts** (`TestInterface*`), **e2e flows** (`TestE2E*`), **keyword-extraction robustness** (`TestExtractKeywordsLongInputRealLLM`/`TestSearchLongInputNeverFails`).

### Benchmarks (`go test -tags integration -bench .`)

All benchmarks drive the real api loop (real encoder + real LLM, no external judge):

| Benchmark | Measures |
|-----------|----------|
| `BenchmarkMemoryLoop` | steady-state Search+Update memory loop with the engine's **auto-triggered Dream** (a scene's depth-1 context exceeding the 30-topic threshold) and periodic L0/L1 verification |
| `BenchmarkSearchAutoCreate` / `BenchmarkSearchRetrieve` | first-write vs retrieval Search latency |
| `BenchmarkUpdate` | agent-reply append latency |
| `BenchmarkDreamConsolidation` | full Dream pipeline latency |
| `BenchmarkSearchLatency` | retrieval latency distribution (min/p50/p95/max) |

### Why no external dataset benchmark?

Public memory benchmarks (LoCoMo, LongMemEval) evaluate "retrieval → LLM-judged answerability" — a different question than what MemHop's layered design asserts (L0 profile distillation, L1 scene-graph coherence, L2 compression semantics, L4 verbatim archival). LongMemEval, the closest fit (multi-session user-assistant chats, ~500 QA), needs 115K–1.5M tokens per question and is not a practical continuous-integration target. MemHop therefore verifies its memory structures directly through the api loop instead of chasing a generic QA score.

## Project Structure

```
api/                         ← Public facade: DB handle (open/search/update/dream/l0–l6) + multi-agent facade (openmulti/session/agents) + type aliases/constructors
internal/                    ← Business assembly: config / db / defaults / l0 / l2 / l3 / l3query /
                               l4 / l5 / l6 / agents / agentctx / search / update / dream / scenefind / llm_client / llm_ops / encoder
internal/repo/               ← Data layer: l0layer–l6layer + agentlayer (record read/write, vectors)
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
| v1.4.0 | 2026-08-26 | Multi-agent memory database | one `.meh` file carries many isolated agent domains: record frames gain `agent_id` (26-byte header), engine indexes and snapshots (0x02) are per-agent, tenant registry records map names to stable crypto/rand agentIDs · `api.OpenMulti` / `AgentSession` / `CreateAgent` / `ListAgents` / `DeleteAgent`; `Open` stays zero-change for single-agent hosts (default domain) · business layer rebuilt around per-agent `agentContext` with domain locks (same-agent serial, cross-agent parallel), idle-domain memory reclamation and scoped Dream pipelines · L7 trajectory layer renumbered to **L6** (cognitive layers converge to L0–L6) · MCP registry shares one `MultiAgentDB` (one `<db-dir>/memhop.meh`), `os.Root`-anchored db-dir · duplicate structs/conversion layers removed (`topicSlotJSON`, `topicToL2Meta`, single-value slice wrappers) · Go 1.23–1.26 stdlib modernization (`iter.Seq2`, `unique.Make`, `os.Root`) · zero new dependencies · **breaking**: `.meh` files with `FormatVersion <= 0x0007` are rejected at Open, no migration; promoted `internal.DB` methods on `api.DB` now carry an `agentID` parameter (facade methods unchanged), `Lock()` panics on a closed DB |
| v1.3.4 | 2026-08-26 | L5 tool-declaration isomorphism | `memhop-capability` format v3: `ResourceRef` renamed `description` → `desc` and gained `input` (JSON Schema string) / `output` — the tool-declaration fields now mirror the host tool spec shape (meowire `ToolSpec`) exactly, so hosts project capabilities with a pure field copy and zero format conversion · `WorkflowStep` gained `args` — action chains carry step parameters officially (no private config formats) · crystallize prompt emits the v3 shape (`type`/`resources` instead of `kind`/`manifest`) · `validateCapabilityImport` now requires resource names and validates `input` as JSON · **breaking**: v2 cards are rejected at import (format must be `memhop-capability/v3`); stored capability records written by earlier versions lose `desc/input/output` on read · built-in capability toolbox (`capabilities/*.json`) fully rewritten to v3 with real JSON Schemas |
| v1.3.3 | 2026-08-26 | Retrieval scoring normalization + defaults slimdown | vector floor fixed from overriding every other signal to lifting only below-threshold scenes (floor = threshold + cosine×0.5): real-signal ordering (RRF + keyword overlap + bonuses) wins, semantic fallback preserved · `MemHopDefaults` slimmed from 24 fields to 3 business knobs (`Capacity` / `DreamCompressMinTopics` / `SearchDreamContextThreshold`); 4 dead fields (`MaxResults` / `DefaultTimeoutSecs` / `DefaultMaxOutputTokens` / `MaxDepth`) removed and 16 tuning constants moved to package-private `internal/tuning.go` · `TopScene` / `SpreadingActivation` / `applySceneBonuses` / `rrfFuse` signatures dropped the defaults parameter · **breaking**: hosts referencing removed fields must clean up · no format change (stays `0x0007`) · MCP tool set unchanged (32) ·
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
