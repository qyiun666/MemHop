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
  <img src="https://img.shields.io/badge/go-1.27+-00ADD8.svg" alt="go">
  <img src="https://img.shields.io/badge/license-MIT%20OR%20Apache--2.0-blue.svg" alt="license">
</p>

<p align="center">
  <strong>Current: v1.6.0 · Latest stable tag: v1.6.0</strong>
</p>

---

MemHop is an **embedded long-term memory database for AI agents and LLM applications**, written in pure Go. It is not a vector database — it is a memory system modeled after how the human brain organizes knowledge, with identity, episodic recall, semantic compression, a knowledge graph, archival storage, and crystallized skills. One agent, one `.meh` file, zero infrastructure.

MemHop is an **agent-dedicated** memory database: each agent binds to exactly one `.meh` file, and a file-level exclusive lock guarantees a single instance per file (a second `Open` fails fast). It runs on **Linux, macOS, and Windows** with no cgo and no external service beyond your LLM endpoint.

Built as the brain memory of [MeowAgent](https://github.com/meowagent/meowagent) (coming soon), MemHop works as an embedded organ rather than a standalone service. No server to run, no configuration to manage — just open a file and your agent has memory.

> **Our stance on agent memory.** Memory should not be an afterthought bolted on with a vector database plugin or a plain-text log dumped into a context window. An agent without internalised memory is just a stateless function pretending to be intelligent. MemHop exists because we believe memory must be *cognitive* — structured, compressed, consolidated, and forgotten the way a human brain does — and *embedded* — living inside the agent process itself, not behind a network call. One file, zero infrastructure, a mind that grows with every conversation.

## Features

- **Seven-Layer Architecture** — L0 Profile → L1 Engram → L2 Context → L3 Knowledge → L4 Archive → L5 Crystal → L6 Trajectory, with Dream consolidation
- **Scene-is-the-session memory loop** — one L2 scene = one host session. `Search` reads that scene's depth-1 topic set straight from the in-memory cache (zero LLM, zero embedding, no scoring) *and opens the turn*: it hands back the topic id the turn will live in. `Update` settles the whole finished turn into that id (user text + agent text + both timestamps → one distillation into the topic's keywords), and L6 trajectory events bind to the same id. The scene's `FusedKeywords` set *is* the context a host injects
- **V2 Storage** — `.meh` format (`FormatVersion=0x0009`) with A/B dual headers, per-record CRC32 + torn-write truncation recovery, mmap zero-copy, snapshot/checkpoint. Record frames carry an 8-byte `agent_id` (26-byte header) and the engine indexes every record by `(agent, idHash)` domain. **Not compatible with `0x0008` (or older) `.meh` data files** — they are rejected at Open with no migration path
- **Multi-Agent Domains** — `OpenMulti` + `CreateAgent(name)` / `Session(agentID)` / `ListAgents` / `DeleteAgent`: many agents share one `.meh` file with fully isolated per-agent domains (caches, Dream pipelines, domain locks); same-agent operations serialize, different agents run in parallel; idle domains reclaim memory on access cadence (`Defaults.AgentIdleTTLMs`) while their records stay on disk. Multi-agent is the only mode — every operation runs through a per-domain session
- **L1 Scene Hypergraph** — Dream creates co-occurrence hyperedges between scenes whose keyword sets overlap (Jaccard ≥ `L1EdgeMinSimilarity`) and decays/prunes them over time; query-time spreading activation retired with the scoring subsystem, so L1 is maintained by Dream for explicit graph queries and future association
- **Dream Pipeline** — consolidation over L0–L2 plus L6 retention pruning: L2 compress → index rebuild → L1 nodes/hyperedges rebuild → L1 decay → L0 distill (emotion/MBTI) → L6 prune (drops trajectory events older than 7 days); returns a per-stage `DreamReport`
- **L3 Knowledge Graph** — multiple independent hypergraphs with node import carrying positional source refs and relation edges (an edge is its members plus its kind, so one node pair can hold several relations), graph and node-level deletion, keyword/type/id lookup that ANDs together, and BFS subgraph queries
- **Single Instance by Design** — one `.meh` file has exactly one owner: a cross-platform exclusive lock (linux/darwin/windows) makes a second open fail fast, and the embedded path runs with no server process and no background daemon
- **Minimal & Embeddable** — 4 direct Go deps (xxhash, go-openai, go-sdk, golang.org/x/sys) — **the engine contacts no embedding / vector service at all**, and there is no dimension to declare in the config; `sync.RWMutex` + `atomic.Pointer`, zero infrastructure
- **MCP Server** — `cmd/memhop-mcp` exposes 27 of the 34 public session methods as MCP tools over multi-tenant HTTP (SSE + streamable-http, official `modelcontextprotocol/go-sdk`): one process serves many hosts through one shared `.meh` file, each tenant isolated by URL path `/mcp/<tenant-id>` into its own agent domain (stable agentID per tenant name, `os.Root`-anchored db-dir, and `memhop_capability_import` paths anchored to `--capability-dir`, defaulting to the db-dir). Go-only by design: the L6 plan write/read surface (`PlanCommit`/`PlanState`/`PlanReplace`/`SyncPlanTree`), memory correction (`DeleteTopic`/`DeleteScene`/`DeleteL3Nodes`) and file maintenance (`CompactTo`, whose argument is an output path) — those need a host that owns session state or chooses where a file is written

## Quick Start

> Full integration guide (config, all layer APIs, N:N turns, pitfalls):
> [INTEGRATION_GUIDE.md](INTEGRATION_GUIDE.md) · 中文: [INTEGRATION_GUIDE.zh.md](INTEGRATION_GUIDE.zh.md)

```go
import (
    "context"
    "log"
    "os"
    "time"

    memhop "github.com/qyiun666/MemHop/api"
)

dbm, err := memhop.OpenMulti(&memhop.MemHopConfig{
    DBPath: "agent.meh", // the whole database; no server, no dimension to declare
    LLM: memhop.LlmConfig{ // required, validated at Open (powers Update's distillation)
        APIURL: "https://api.openai.com/v1",
        APIKey: os.Getenv("OPENAI_API_KEY"),
        Model:  "gpt-4o-mini",
    },
    Defaults: *memhop.DefaultMemHopDefaults,
})
if err != nil {
    log.Fatal(err)
}
defer dbm.Close()

// One .meh file carries isolated domains. CreateAgent returns a stable
// 16-char hex ID; Session binds every call to that domain.
agentID, err := dbm.CreateAgent("my-agent")
if err != nil {
    log.Fatal(err)
}
sess, err := dbm.Session(agentID)
if err != nil {
    log.Fatal(err)
}

// Read memory = read one scene (a scene IS a host session), which also
// opens the turn about to run. Empty SceneID → the library creates a scene
// and returns its id (L3ID optionally anchors it to a project domain); a
// non-empty SceneID must already exist, otherwise ErrNotFound. The read costs
// no LLM, no embedding and no scoring; NewTopicID is the topic this turn
// lives in.
res, err := sess.Search(memhop.SearchQuery{})
if err != nil {
    log.Fatal(err)
}
sceneID := res.Scene.SceneID
for _, topic := range res.Topics { // this session's depth-1 set = the context
    _ = topic.FusedKeywords
}

// End of turn: settle the whole exchange into the topic Search opened. Both
// originals become L4 archives and one distillation produces the turn
// topic's keywords. Replaying the same TopicID rewrites that turn instead of
// duplicating it, so a timed-out Update is safe to retry.
topicID, err := sess.Update(memhop.TurnUpdate{
    SceneID:   sceneID,
    TopicID:   res.NewTopicID,
    UserText:  "What did we discuss yesterday?",
    UserTS:    time.Now().UnixMilli(),
    AgentText: "Agent: ...",
    AgentTS:   time.Now().UnixMilli(),
})
if err != nil {
    log.Fatal(err)
}

// While the turn runs, its trajectory events bind to that same topic id.
_ = sess.AppendTrajectory(topicID, "", memhop.TrajectorySlot{
    EventType: "tool_call",
    Payload:   `{"tool":"grep"}`,
    Timestamp: time.Now().UnixMilli(),
})

// Dream consolidation (L0-L2); an empty sceneID sweeps every scene of the
// domain. Update already schedules it in the background once a scene's
// topic count passes the threshold, so hosts rarely call it.
report, err := sess.Dream(context.Background(), "")
```


> **Concurrency contract.** Same-agent operations (Search / Update / Dream / write APIs) are serialized by the library's per-agent domain lock; different agents run in parallel on a `*MultiAgentDB`, so the host needs no external queue. `*memhop.Session` carries no cross-domain state beyond its bound id. The file's exclusive lock still allows only one process per `.meh` file; `MultiAgentDB` exposes no locking API (removed in v1.5.0): the domain lock is the library's, and a host critical section needs its own.

Prerequisites: Go 1.27+ and an OpenAI-compatible LLM endpoint (`Config.LLM` is required) — no embedding / vector service needed

### API Overview

| Group | Methods |
|-------|---------|
| Core loop | `Search(q)` · `Update(TurnUpdate) → topicID` · `Dream(ctx, sceneID)` |
| L0 Profile | `GetL0` · `UpdateL0` |
| L2 Context | `ListScenes([l3ID])` · `UpdateScene(id, {Name, L3ID, Force})` · `SceneContext` · `MergeScenes` · `DeleteTopic` · `DeleteScene` |
| L3 Knowledge | `GetL3` · `ListL3` · `ImportL3` (returns the graph ids it wrote) · `UpdateL3` · `DeleteL3` · `DeleteL3Nodes` (Go only) · `QueryL3Nodes` · `QueryL3Subgraph` |
| L4 Archive | `SearchL4(q)` — one read surface; keyword (case-insensitive), time range, ids, topic and content type are conditions, not modes; `Limit` keeps the newest matches |
| L5 Capability | `ImportCapability` · `UpdateCapability` · `DeleteCapability` · `ListCapabilities([IDs])` · `ActivateCapability` · `RecordCapabilityUsage` |
| L6 Trajectory | `AppendTrajectory(key, [nodePath])` · `ReadTrajectory(key)` · `ListTrajectorySessions` · `Crystallize(key)` — a turn's events key on its topic id (7-day auto-retention, no delete API) |
| L6 Plan tree | `PlanCommit` · `PlanState` · `PlanReplace` · `SyncPlanTree`, ids from `api.NewPlanID(name)` (Go API only, not in the MCP tool set) |
| DB handle | `OpenMulti` · `CreateAgent` · `ListAgents` · `DeleteAgent` · `Session(id)` · `Checkpoint` · `CompactTo(newPath)` (defragmented copy; Go only) · `Close` · `IsClosed` · `api.DefaultAgentID` |

### Built-in L5 Capabilities

The root `capabilities/` directory ships **six built-in capability cards** (`memhop-capability/v3`, embedded into the library at build time, English): `memhop-guide` (the loop split — Search/Update/Dream and trajectory recording run host-side and must never be manual LLM calls — plus an index of the other five) and five LLM-callable manuals (knowledge, scene, archive, profile, capability). Cards document the Go API (`type: "api"`, `ref: "api:MethodName"`) — the host calls the methods directly, no MCP layer involved. **Resources are tool declarations**: `name/desc/input/output` mirror the host tool spec (e.g. meowire `ToolSpec`) field-for-field, so a host projects them with a pure field copy and zero format conversion. **Tiered injection**: `ListCapabilities` serves the toolbox read-only (same filters as stored records, never persisted to the `.meh` file, deduped by ID against stored same-name records — stored wins, NOT attached to `Search` responses); inject only a one-line-per-card index (`id + name + summary + trigger`) plus the guide, and fetch full parameter schemas on demand via `ListCapabilities(CapabilityListQuery{IDs: []string{id}})`.

## Architecture

```
Layer   Name             Human Parallel          Mechanism
─────   ──────────────   ───────────────────     ─────────────────────────────────────────────
 L6     Trajectory       Procedural log          Host-appended operation events; crystallized into L5 capability drafts
 L5     Crystal          Muscle memory           Reusable capability packages (skills · MCP · tools · prompts · services)
 L4     Archive          Long-term memory        Raw dialogue logs & historical records
 L3     Knowledge        Semantic memory         Multi-source hypergraph knowledge base
 L2     Context          Working memory          Compressed topic structures (4 depth levels)
 L1     Engram           Scene hypergraph        Scene nodes + keyword-overlap hyperedges; maintained by Dream for explicit graph queries
 L0     Profile          Identity                Agent personality, preferences & language habits
```

### Dream Pipeline

The Dream cycle is an automatic consolidation pass inspired by how sleep processes the day's experiences. It acts on **L0–L2 only** (L3 distillation and L5 crystallization are out of scope) plus L6 retention pruning:

1. **L2 compression** — the LLM groups related topics per scene; each target scene runs in its own goroutine, sinking merged topics under a new depth-1 fused node
2. **L1 rebuild** — scene nodes are synced from L2, the L2Meta topic cache is rebuilt in the same scan, and keyword-overlap hyperedges are created or refreshed
3. **L1 decay** — scene importance and edge weights decay over time, weak nodes are pruned
4. **L0 profile** — the agent profile is rebuilt from consolidated memory
5. **L0 distill** — emotion/MBTI signals are distilled (always runs; skipped when the L1 sample set is empty)

Trigger: once a scene's depth-1 topic count passes `Defaults.SceneDreamTopicThreshold` (default 24), `Update` schedules that scene's Dream in the background; hosts may also call it. `Dream(ctx, sceneID) (*DreamReport, error)` holds the domain lock for the whole cycle, sweeps every scene of the domain when `sceneID` is empty (scenes below `DreamCompressMinTopics` are skipped) and honours `ctx` cancellation between stages.

### Read & write path

**There is no scored retrieval.** A scene is a host session, so the engine never guesses which scene a message belongs to:

| Path | What it does | Cost |
|------|--------------|------|
| `Search(SearchQuery{SceneID, L3ID})` | empty `SceneID` → create a scene (named by the library) and return its id; otherwise → the scene's depth-1 topics (user-timestamp order) plus the L0 profile — and `NewTopicID`, the topic this read opens for the coming turn | in-memory read (L2Meta), zero LLM / embedding / scoring; the only write is the scene record (hit counters + turn counter) |
| `Update(TurnUpdate{SceneID, TopicID, ...})` | settles one finished turn into the topic Search opened: two L4 archives plus a topic whose keywords come from a single distillation | exactly one LLM call per turn; distillation runs before any write, so a failure leaves no trace. Same `TopicID` = rewrite, never duplicate |

What a host injects as context is the keyword set of that scene's depth-1 topics; to read a turn's original text, follow the topic's `L4Refs` through `SearchL4`, or use `SceneContext`. Dream keeps the injected size bounded (`Consolidate` requires at most 20 topics per scene after compression).

Removed along with retrieval: three-channel RRF scoring, L1 spreading activation (`AssociatedContexts`), topic centroids and the embedding dependency, the `AutoCreate` / `DirectedL2ID` / `DirectedL3ID` routes, and topic-level `L3Refs` (L2↔L3 now lives solely on the scene anchor `SceneSlot.L3ID`).


## Testing & Benchmarks

MemHop's test suite exercises only the public `api` surface — exactly the calls a host (e.g. MeowAgent) makes — and asserts the engine's own memory structures, not external answerability judges.

### Integration tests (`test/`, build tag `integration`)

- **Memory loop** (`TestCoreCycleUpdateDream`): N turns settled into one scene the way a real host does, with **periodic L0/L2/L4 consistency checks** every few turns — L0 profile readable, the scene read non-empty, L4 holding the raw utterance verbatim. After Dream consolidation the scene surface must shrink while every fact stays recoverable from L4.
- **Keyword fidelity & persistence** (`TestKeywordFidelity`/`TestKeywordPersistence`/`TestDreamCompressionFidelity`): the keywords distilled from a turn faithfully carry its meaning, survive noise turns, and stay faithful across Dream compression.
- **API contracts** (`TestInterface*`: reads make zero LLM calls, writes cost exactly one distillation per turn, unknown scenes are rejected, checkpoints survive a restart), **e2e flows** (`TestE2E*`), **long-input robustness** (`TestExtractKeywordsLongInputRealLLM`/`TestUpdateLongTurnNeverFails`).

### Benchmarks (`go test -tags integration -bench .`)

All benchmarks drive the real api loop (real LLM, no external judge):

| Benchmark | Measures |
|-----------|----------|
| `BenchmarkMemoryLoop` | steady-state Search+Update loop including the engine's **auto-scheduled Dream** (a scene's depth-1 topic count passing the threshold) and periodic L0/L2 verification |
| `BenchmarkUpdateTurn` | one-turn settle latency (one distillation + topic + two L4 writes) |
| `BenchmarkSceneRead` / `BenchmarkSceneReadLatency` | scene-read throughput and latency distribution (min/p50/p95/max) |
| `BenchmarkAppendL4` | pure storage append latency (no LLM) |
| `BenchmarkDreamConsolidation` | full Dream pipeline latency |


### Why no external dataset benchmark?

Public memory benchmarks (LoCoMo, LongMemEval) evaluate "retrieval → LLM-judged answerability" — a different question than what MemHop's layered design asserts (L0 profile distillation, L1 scene-graph coherence, L2 compression semantics, L4 verbatim archival). LongMemEval, the closest fit (multi-session user-assistant chats, ~500 QA), needs 115K–1.5M tokens per question and is not a practical continuous-integration target. MemHop therefore verifies its memory structures directly through the api loop instead of chasing a generic QA score.

## Project Structure

```
api/                         ← Public facade: openmulti (entry + tenant management) / session (the only
                               business handle, hex-id surface) / types / mapping / ids / errors / exports
internal/                    ← Business assembly: config / db / session / defaults / tuning /
                               l0 / l2 / l3 / l3query / l4 / l5 / l6 / l6_plan / agents / agentctx /
                               search / update / dream / plancache / llm_client / llm_ops / models / exports
internal/repo/               ← Data layer: l0layer–l6layer + agentlayer (record read/write)
internal/repo/index/         ← Index layer: l2meta / rebuild (single-pass scan) /
                               traj (the L6 turn shape)
internal/repo/core/          ← .meh engine: engine / frame / header / snapshot / reclaim /
                               record / model / mmap / filelock
internal/common/             ← Bottom-layer utilities: enum / errors / hash /
                               sliceutil / strutil / timeutil
test/                         ← Integration tests (build tag: integration)
benches/fixtures/             ← Benchmark datasets (locomo10, locomo_smoke, longmemeval_smoke)
```

Dependency direction is strictly one-way: `api → internal → repo → core`, with `common` at the bottom (no references to any other internal package).


> Note: `docs/` and `AGENTS.md` are intentionally kept local-only (see `.gitignore`), so links under `docs/` may not resolve in a public clone.

### LLM Call Cost Model

- **Read path** (`Search`): **zero LLM, zero embedding** — served from the L2Meta cache alone.
- **Write path** (`Update`): exactly one keyword distillation per turn (both originals fed together), 512-token output cap escalating on truncation, heuristic tokenisation on parse failure.
- **Dream**: one consolidation call per scene reaching the topic floor (`DreamCompressMinTopics`, default 20), plus one distill call with at most 200 ranked L1 samples (up to 20 keywords each). Output caps: 8192 / 2048 tokens.
- **Crystallize**: one explicit, host-triggered call per turn trajectory; turns sharing an L2 topic fold into one prompt (capped at 128KB of payload, oldest dropped).
- Use a small/fast chat model (a cheap API model or a local OpenAI-compatible endpoint) for the configured LLM when latency and cost matter; keyword distillation does not need a frontier model.

## Development

```bash
go build ./...                          # Build
go vet ./...                            # Static analysis
go test ./internal/...                  # Unit tests (no external services)
go test -tags integration ./test/...    # Integration tests (requires an LLM key)
```

Integration tests run against a real LLM (the engine needs no embedding service). Configure the LLM via environment variables `MEMHOP_TEST_LLM_KEY` / `MEMHOP_TEST_LLM_URL` / `MEMHOP_TEST_LLM_MODEL` (defaults to the DeepSeek endpoint when only the key is set), or via `test/testsupport/key_config.json`.

## Changelog

| Version | Date | Highlight | Core Changes |
|---------|------|-----------|--------------|
| v1.6.0 | 2026-09-04 | No-fallback interfaces; the finished v1.5.0 surface | The v1.5.0 tag was cut before its API surface was complete — hosts requiring it hit `undefined: api.DefaultAgentID / api.NewPlanID / api.ScenePatch`. This release ships the finished line. 1. **Library-minted ids**: `api.DefaultAgentID` names the implicit domain and `api.NewPlanID(name)` mints plan ids; the four `api/ids.go` Format/Parse bridges are gone<br>2. **Merged surface**: `SetSceneName`+`SetSceneL3ID` → `UpdateScene(id, ScenePatch)`, `PlanAppend` → `AppendTrajectory(key, nodePath, ev)`, `ListScenesByL3` → `ListScenes(l3ID)`; `Lock`/`Unlock`/`Session.Checkpoint`/`IsClosed`/`AgentID`/`DistillL0`/`ListPlans`/`GetArchive`/`GetCapability` deleted (34 + 8 methods)<br>3. **Interfaces refuse instead of falling back**: unparseable LLM keyword output is `ErrLLM` and the turn writes nothing (gse/tokenizer fallback deleted, direct dependencies 5 → 4); a rejected `PlanCommit` leaves the tree and events untouched; over-budget trajectory payloads are rejected, not truncated; `DeleteCapability`/`DeleteAgent`/`MergeScenes` report unknown ids instead of silent no-ops; reads surface transient errors instead of skipping records<br>4. **Graph import closed loop**: skipped re-imports rebuild their edges, existing graph slots are reused so renames survive re-import, batches validate before writing, `L3Relation.Titles` declares true N-ary hyperedges, `UpdateL3` name collisions are rejected with deterministic re-export routing, deleting a graph detaches scene anchors, `SyncPlanTree` branch deletion mirrors into the trajectory index<br>5. **Format stays 0x0009**, MCP tool set unchanged (31); every fix re-verified by importing this repo into its own L3 hypergraph — full record in CHANGELOG |
| v1.5.0 | 2026-09-01 | L2 re-shape: a scene IS a host session, and the library owns the turn id | 1. **`Search` reads the scene AND opens the turn**: input `{scene_id, l3_id}`, both optional — an empty `scene_id` mints a scene (library-named `session:<id>`; the `scene_name` input is gone — the host titles a scene with `UpdateScene`), a non-empty unknown one is `ErrNotFound`, and `l3_id` only anchors a newly created scene. The result is `{profile, profile_brief, scene, topics, new_topic_id}`: the scene record, its depth-1 topics in turn order (the context to inject) and the topic id this read minted for the turn about to run, `hash("turn:" + scene:turn_seq)` from a new per-scene `turn_seq` counter. `contexts`/`associated_contexts`/`auto_create`/`directed_l2_id`/`directed_l3_id`, the `ctx` parameter and every read-path LLM/embedding/scoring call are gone. That counter write is the read's only write and is now load-bearing: a failed write fails the read instead of reissuing a possibly duplicate id<br>2. **`Update` settles the whole turn into that id**: `TurnUpdate{scene_id, topic_id, user_text, user_ts, user_type, agent_text, agent_ts, agent_type}` returns the same id; one distillation runs before any write, so a failed LLM call leaves nothing behind; a missing / zero / non-hex `topic_id` is `ErrInvalidQuery`; replaying one id overwrites the turn and tombstones the two archives it supersedes. Both timestamps are now ordering fields only, not identity inputs<br>3. **N:N append surface deleted**: `AppendL4Message` (many messages into one topic) and `RefineTopicKeywords` (re-distill one topic from all originals) are gone — a turn's L4 originals are exactly its two texts, and what happens between them belongs to the turn's L6 trajectory. L4 content types are declared on the way in — `user_type`/`agent_type` (zero value = `text`, a non-text slot carries its media path or URL as the text) — and `SceneMessage.type` reports them back<br>4. **L6 trajectory keyed by the turn's topic id**: `AppendTrajectory` / `ReadTrajectory` / `Crystallize` take that id and the event's `topic_id` is stamped from the key, so host-minted turn keys disappear; plan-bound events keep using the plan id, and the cross-turn fold (with `TrajIndex.TopicEvents`) is deleted — a plan id is now the cross-turn aggregation unit<br>5. **Single keyword track**: `user_keywords`/`agent_keywords`/`centroid_page_ref`/`l3_refs` removed, only `fused_keywords` remains (same on-disk field name); Dream compression, L1 hyperedges and host injection all read that one track, no summary field — originals stay in L4<br>6. **Scene ids are minted by the library**: `NewSceneSlot(sceneID, name)` no longer hashes the name; `CreateSceneL2WithID` reuses an existing scene idempotently; the `timestamp:text` auto-naming path disappears<br>7. **Retrieval subsystem deleted**: `internal/cap/scenefind` (three channels, RRF, scene bonuses, L1 spreading activation), topic centroids, `RecVecCentroid`, `Encoder`/`HttpEncoder`/`OpenMultiWithEncoder` and the encoder config — **the engine contacts no embedding service**<br>8. **`VectorDim` deleted** from the config surface (`CheckVectorDim`/`ErrVectorDimMismatch`/MCP `--vector-dim` with it); the two header bytes at offset 6 are reserved, format stays `0x0009`<br>9. **Dead index island deleted**: the orphaned BM25 / entity / BK-tree / L3-index code (L3 node search always scanned records), two Levenshtein implementations and zero-caller `common.FormatIDs`<br>10. **Consolidation trigger re-axled**: `activeScenes`/`Capacity` plus `ActiveSceneIDs`/`HasActiveScenes` removed; `Update` schedules a scene's Dream once its depth-1 count passes `SceneDreamTopicThreshold` (default 24); `Dream(ctx, "")` sweeps every scene of the domain<br>11. **No format bump**: `TopicSlot.UnmarshalJSON` folds an old file's two tracks into `fused_keywords` at decode time, and `turn_seq` is additive (scenes written before this release decode to 0 and open turn 1 on the first read). Turn topics derive their id under a `"turn:"` namespace disjoint from Dream's fused-node ids; the unused `ComputeTopicIDForText` and the dead `SceneNode.VectorPageRef` are removed<br>12. Scene merging no longer happens inside Dream (it would delete a sceneID the host still holds); `MergeScenes` stays an explicit host API. **Delivery**: MCP `memhop_search` loses `scene_name` and returns `new_topic_id`, `memhop_update` requires `topic_id`, the trajectory tools key on that id, `memhop_status` reports `scene_count`, `memhop_scene_active_list` is deleted while `memhop_scene_rename` is added (30 → 31 tools); **L0 profile field ownership is enforced by the library** — `UpdateL0` writes only the host's four fields, inherits the Dream-evolved emotion state and MBTI from the stored profile and stamps `updated_at_ms` itself, so `memhop_profile_update` is a plain forward and Go hosts get the same guarantee; `SyncPlanTree` inherits a blank `Title`/`PlanType`/`Status`/`Summary` instead of rewinding the node, a failed Dream merge group rolls back what it wrote, and a transient read failure is never reported as "record not found"; the DSH plugin surface (`dsh/`, `dsh-adapter/`) is retired in this same release, so the delivery is the library plus the MCP server<br>13. **Public surface consolidated to what a host actually reaches** (audited against `api/` alone — the MCP tool set is not a reason to keep a method): Session 43 → 33 methods, `MultiAgentDB` 9 → 7; the post-release interface audit below added `DeleteL3Nodes` and `CompactTo`, so the surface is 34 + 8. Deleted with their implementation chains: `Lock`/`Unlock` (the default domain's mutex exposed to hosts), `Session.Checkpoint`/`IsClosed`/`AgentID` (duplicates of the DB handle), `DistillL0` (a Dream stage, not an entry point), `ListPlans` + `plan.Summarize` + `PlanCache.All` (recovery is `PlanState` on a known id), `GetArchive` and `GetCapability` (both are one condition of the list query), `MergeScenes`' dead repo overwrite twin `OverwriteSceneL3ID`, and the un-consumed `api.CapabilityImport` alias. Merged: `ListScenesByL3` → `ListScenes(l3ID)`, `SetSceneName`+`SetSceneL3ID` → `UpdateScene(id, ScenePatch{Name, L3ID, Force})` (one read-modify-write), `PlanAppend` → `AppendTrajectory(key, nodePath, ev)` (empty nodePath = the bare turn event). **The library issues every id**: the four `Format*`/`Parse*` bridges are gone — `api.DefaultAgentID` names the implicit domain and `api.NewPlanID(name)` mints a plan id under a `plan:` namespace, so a host echoes ids and never builds one; `internal.FormatAgentID`/`ParseAgentID` collapsed into the one `FormatID`/`ParseID` pair. A fourth was caught by a new reflection guard: `UpdateScene` had been merged without a facade override, so embedding leaked `core.SceneSlot` to hosts with uint64 ids — the facade now maps it and hands back the **written scene** (confirming an anchor no longer costs a `ListScenes` sweep), and `api/surface_public_test.go` rejects any host-visible uint64 id field. `api.NewPlanID` borrows away from the reserved 0, so every id the library issues is one it accepts. Three silent failures fixed: `SearchL4` with only `TopicID` or only `Type` returned nothing (the L4 selector is now a set of AND-ed conditions, so one call pulls a turn's originals); re-anchoring an anchored scene answered `nil` while changing nothing (now `ErrInvalidQuery` unless `Force`, and the target domain must exist — also enforced when `Search` anchors a new scene); `Dream` on an unknown scene id returned a zero report with no error (now `ErrNotFound`)<br>14. **Post-release per-layer interface audit** (every finding re-checked against source, plus the public surface imported into this repo's own L3 hypergraph and queried): two core-layer defects that turned a wrong id into silent data loss are fixed — **typed record reads now check the frame's record type** (`GetL3(nodeID)` and friends answer `ErrNotFound` instead of decoding a foreign slot, which is what let `UpdateL3` rename a node record into a graph slot), and **a hyperedge's identity includes its kind** (an edge pair carrying both `related` and `part_of` used to collapse to whichever kind was written last; re-imports dedupe on sorted members + kind, so edges already stored under the pair-only hash are not duplicated). The public surface got more honest, in the host's favour: the twelve contract-heavy methods that reached hosts by embedding alone are now **declared with doc comments on the facade** (`go doc api.Session` showed 22 of 34 before), `ImportL3` reports `graph_ids` for the graphs it wrote (a graph id is `hash(Domain)` and no other public call renders that derivation), and two new entries exist: `DeleteL3Nodes` (node-level deletion, cascading its hyperedges) and `MultiAgentDB.CompactTo` (the way back from tombstone-only deletes). Semantics tightened or made consistent: `Update` may only settle **a turn this scene opened** (writing a Dream-fused topic, another scene's turn, or a host-invented id is refused, while replays and out-of-order settles stay valid); `QueryL3Nodes` conditions AND together instead of a priority switch that silently ignored two of them; `SearchL4`'s keyword is case-insensitive like the L3 filter and gains `Limit` (newest matches), with the MCP tool capped at 50 by default; `SceneContext`'s depth ≤ 2 flattening is documented as deliberate — it is the only read that brings back the originals Dream sank. Dead public fields removed: `HypergraphNode.importance`, `HypergraphEdge.weight/label`, `ArchiveSlot.metadata` and the `RoleSystem` constant (no write path anywhere in the engine; the record fields stay so older files decode), and `plan_type` is now cleared on event writes since the record contract makes it node-only. Security and delivery: `memhop_capability_import` paths are anchored through `os.Root` on `--capability-dir` (default `--db-dir`) and escapes are refused; `memhop_dream`'s `scene_id` is optional so MCP hosts can reach domain-wide consolidation at all; the import tool description and the `ListL3` card each claimed something the code does not return and now tell the truth; each card's summary maps its Go method names to the `memhop_*` tools an MCP client actually calls; and the ~70-line `idsToHex` rewrite in the MCP server, a no-op since the api DTOs render every id as hex (four of its fifteen keys named fields no public DTO has), is deleted. New drift guards tie hand-written vocabularies to the engine enums they enumerate. **Format version stays 0x0009**: this round changes derivation and validation, not the record layout. |
| v1.4.2 | 2026-08-31 | L6 plan tree + L2 directory anchor | 1. L6 carries a task tree: `TrajectorySlot.NodeType` splits turn events from plan nodes, node ids derived stably by `HashPlanNode(planID, nodePath)` under a `plan:` namespace, events bound via `PlanNodeRef`<br>2. three-form surface `PlanAppend` / `PlanCommit` / `PlanState` plus `PlanReplace` (re-plan, keeps planID), `SyncPlanTree` (whole-tree snapshot diff, emits no `plan_step`), `ListPlans` (restart recovery)<br>3. **Model A fold**: a parent turns `done` only on an explicit host commit; after each commit the `done` children's summaries roll up bottom-up in numeric `NodePath` order without clobbering a host-written parent summary<br>4. `PlanTree.Roots` is a **forest** (one root per top-level step; orphaned nodes surface as roots instead of vanishing)<br>5. L2 scene → L3 directory anchor (N:1): `SceneSlot.L3ID`, optional `SearchQuery.L3ID` pre-filter with backfill on hit, `ListScenesByL3`, `SetSceneL3ID(sceneID, l3ID, force)` write-once unless correcting or clearing<br>6. per-domain `planCache` so `PlanState`/`ListPlans`/rollup stop scanning the engine per call<br>7. api constants exported: `Role*`, `NodeType*`, numeric `Status*` (read-side), string `PlanStatus*` + `PlanStatus` type (write/query side); fifth status `running` added<br>8. write surface forced authoritative: all plan-node fields and `Seq` are overwritten on every append path, `EventType` restricted for plan events<br>9. hardening: `0000000000000000` is the reserved bare-event `PlanID` and rejected by all five plan entry points (`PlanReplace` on it used to delete every turn event of the domain); Dream's plan exemption narrowed to plans active inside the 7-day window, so abandoned plans no longer accumulate forever<br>10. no format change (stays `0x0009`, additive JSON fields, v1.4.1 files open as-is), MCP tool set unchanged (31) — the plan surface is **Go module only** this release |
| v1.4.1 | 2026-08-28 | Type-contract cleanup: hex-ID DTOs, L0 profile v2, L3 hypergraph activation | 1. api response DTOs are real structs — every ID field leaves as a 16-char hex string (`SearchResult.NewTopicID`, `AppendL4Message`, `AgentID()` included) with new `api.FormatID` / `api.ParseID` helpers<br>2. L0 profile v2 (`FormatVersion 0x0009`): field ownership (Name/Role/Preferences host-exclusive, Personality host-seeded + Dream-distilled), typed `EmotionState`/`MBTI` distillation signals, dead `lexicon`/`style_traits` removed<br>3. zero in-library hex round-trips (repo-layer ID params are uint64, centroid hash via `HashBytes`)<br>4. L3 import gains `source_ref` (positional reference) and `related` (same-graph hyperedges resolved by title, two-phase forward references, idempotent re-import; `edges_created` result field, `L3Relation` type exported)<br>5. `AppendL4Message` gains `contentType` (Content* constants exported; text/document/code carry the original text, image/audio/video carry a path/URI with mime/size/sha256 in Metadata), `L4Query.Type` filter and MCP `archive_search` `content_type` param<br>6. L6 one-trajectory-per-turn: SessionID is a turn key (search opens, update closes), events carry `TopicID` for cross-turn crystallization, external surface trimmed to append/query (`TrajectoryStats` / `DeleteTrajectory` / `PruneTrajectory` removed, 33 → 31 tools), Dream `l6_prune` auto-drops events older than 7 days<br>7. distill/consolidate LLM parsing gains a format-constrained retry<br>8. **breaking**: `.meh` files with `FormatVersion != 0x0009` (i.e. ≤ 0x0008) are rejected at Open, no migration |
| v1.4.0 | 2026-08-26 | Multi-agent memory database | 1. one `.meh` file carries many isolated agent domains: record frames gain `agent_id` (26-byte header), engine indexes and snapshots (0x02) are per-agent, tenant registry records map names to stable crypto/rand agentIDs<br>2. `api.OpenMulti` / `AgentSession` / `CreateAgent` / `ListAgents` / `DeleteAgent`; `Open` stays zero-change for single-agent hosts (default domain)<br>3. business layer rebuilt around per-agent `agentContext` with domain locks (same-agent serial, cross-agent parallel), idle-domain memory reclamation and scoped Dream pipelines<br>4. L7 trajectory layer renumbered to **L6** (cognitive layers converge to L0–L6)<br>5. MCP registry shares one `MultiAgentDB` (one `<db-dir>/memhop.meh`), `os.Root`-anchored db-dir<br>6. duplicate structs/conversion layers removed (`topicSlotJSON`, `topicToL2Meta`, single-value slice wrappers)<br>7. Go 1.23–1.26 stdlib modernization (`iter.Seq2`, `unique.Make`, `os.Root`)<br>8. zero new dependencies<br>9. **breaking**: `.meh` files with `FormatVersion <= 0x0007` are rejected at Open, no migration; promoted `internal.DB` methods on `api.DB` now carry an `agentID` parameter (facade methods unchanged), `Lock()` panics on a closed DB |
| v1.3.4 | 2026-08-26 | L5 tool-declaration isomorphism | 1. `memhop-capability` format v3: `ResourceRef` renamed `description` → `desc` and gained `input` (JSON Schema string) / `output` — the tool-declaration fields now mirror the host tool spec shape (meowire `ToolSpec`) exactly, so hosts project capabilities with a pure field copy and zero format conversion<br>2. `WorkflowStep` gained `args` — action chains carry step parameters officially (no private config formats)<br>3. crystallize prompt emits the v3 shape (`type`/`resources` instead of `kind`/`manifest`)<br>4. `validateCapabilityImport` now requires resource names and validates `input` as JSON<br>5. **breaking**: v2 cards are rejected at import (format must be `memhop-capability/v3`); stored capability records written by earlier versions lose `desc/input/output` on read<br>6. built-in capability toolbox (`capabilities/*.json`) fully rewritten to v3 with real JSON Schemas |
| v1.3.3 | 2026-08-26 | Retrieval scoring normalization + defaults slimdown | 1. vector floor fixed from overriding every other signal to lifting only below-threshold scenes (floor = threshold + cosine×0.5): real-signal ordering (RRF + keyword overlap + bonuses) wins, semantic fallback preserved<br>2. `MemHopDefaults` slimmed from 24 fields to 3 business knobs (`Capacity` / `DreamCompressMinTopics` / `SearchDreamContextThreshold`); 4 dead fields (`MaxResults` / `DefaultTimeoutSecs` / `DefaultMaxOutputTokens` / `MaxDepth`) removed and 16 tuning constants moved to package-private `internal/tuning.go`<br>3. `TopScene` / `SpreadingActivation` / `applySceneBonuses` / `rrfFuse` signatures dropped the defaults parameter<br>4. **breaking**: hosts referencing removed fields must clean up<br>5. no format change (stays `0x0007`)<br>6. MCP tool set unchanged (32)
| v1.3.2 | 2026-08-26 | API fixes: async Dream + deletion + Update simplification | 1. Search/Update no longer block on an internally triggered Dream (background goroutine, per-scene in-flight dedup, Close cancels a pending Dream)<br>2. new `DeleteTopic` (subtree closure + L4 + indexes + parent ChildrenIDs pruning) and `DeleteScene` (scene + all topics + archives + L1 node + active set) for memory correction<br>3. `Update` returns `error` instead of `(bool, error)`<br>4. `SearchResult.ProfileBrief` — compact profile digest (name/role/top preferences/style/emotions, bounded)<br>5. no format change (stays `0x0007`)<br>6. MCP tool set unchanged (32)
| v1.3.0 | 2026-08-26 | L1 scene hypergraph + spreading-activation association | 1. Dream creates real `RecL1Hyperedge` co-occurrence edges between scenes (keyword-overlap Jaccard ≥ `L1EdgeMinSimilarity`); Search `AssociatedContexts` replaced the no-op same-scene listing with a graph walk (activation × edge weight × dampening per hop, ≤ `L1EdgeMaxHops`, top `L1AssocMaxScenes` other scenes)<br>2. L6 scene-usage record removed — hit counters folded into the L2 `SceneSlot` (`HitCount`/`LastHitAt`)<br>3. `L1ReverseIndex` (incl. snapshot field) and 4 dead L1 functions removed; association is now a pure storage-level graph read<br>4. `.meh` format bumped to `0x0007` — 0x0006 files are rejected at Open, no migration<br>5. new defaults: `L1EdgeMinSimilarity` (0.15), `L1EdgeMaxHops` (2), `L1ActivationDampening` (0.5), `L1ActivationThreshold` (0.05), `L1AssocMaxScenes` (3)
| v1.2.7 | 2026-08-25 | Host alignment + bilingual integration guides | 1. `Search(ctx, q)` and `RefineTopicKeywords(ctx, id)` accept a context (cancels LLM extraction, encoder calls, internally triggered Dream)<br>2. `api` exports `LlmConfig` / `MemHopDefaults` / `TopicSlot` / `ResourceRef` / `CrystallizeDetail` / `TrajectoryStats`<br>3. new `TrajectoryStats` (per-session L7 stats) + `memhop_trajectory_stats` MCP tool (31 → 32 tools)<br>4. `CrystallizeResult.Details` — per-candidate create/reuse/merge/skip disposition<br>5. `AppendL4Message` (pure L4 append, no LLM)<br>6. active-scene capacity: Update triggers a Dream on the oldest scene at Capacity with a compressibility pre-check; `SearchDreamContextThreshold` zero-value guard<br>7. bilingual integration guides added at repo root (`INTEGRATION_GUIDE.md` / `INTEGRATION_GUIDE.zh.md`) |
| v1.2.5 | 2026-08-20 | MCP server rewritten | 1. `cmd/memhop-mcp` fully rewritten against the `api` facade (v1.2.4 removed it): all 31 MCP tools map 1:1 to `api.DB` methods<br>2. multi-tenant HTTP — SSE + streamable-http (2025-03-26 spec, stateless), each tenant isolated by URL path `/mcp/<tenant-id>` into its own `.meh` file, lazy-open registry with a first-open mutex<br>3. all tool outputs serialize record IDs as 16-char hex strings (uint64 JSON numbers lose precision in JS/TS hosts)<br>4. tenant-ID whitelist + path-traversal rejection (defense in depth)<br>5. LLM credentials via env vars only (no CLI flag)<br>6. go-sdk v1.7.0 back as a direct dep (3 → 4)<br>7. offline tests for config/registry/tools/streamable + multi-tenant SSE smoke<br>8. codebase cleanup: dropped redundant enum JSON helpers (default `~uint8` JSON behavior is identical), `CodeOf` migrated to Go 1.26 `errors.AsType`, scalar cosine loop (2.7× faster at 1024 dims), deleted the `internal/repo/open.go` forwarding layer (17 funcs + 8 aliases; internal calls core/index directly), removed the `ParseID→FormatHash` round-trip in Update |
| v1.2.4 | 2026-08-19 | api/ facade + internal/ flattening | 1. Public Go API moved from the root package to `github.com/qyiun666/MemHop/api` (root `memhop.go`/`types.go` removed)<br>2. `internal/sub/` flattened into `internal/` (`package sub` → `package internal`), `internal/sub/repo` → `internal/repo`, `internal/sub/common` → `internal/common`<br>3. `cmd/memhop-mcp` removed (rewritten in v1.2.5)<br>4. build config (Makefile fmt, pre-commit hook, CI gofmt) updated<br>5. breaking change: hosts importing the root package must switch to `/api` |
| v1.2.3 | 2026-08-18 | MCP compatibility fixes + DSH integration + retrieval quality | 1. MCP tool schemas fixed (no-arg tools no longer emit `properties: null`, breaking strict clients)<br>2. all tool outputs render record IDs as 16-char hex strings (uint64 JSON numbers lose precision in JS/TS hosts, breaking `new_topic_id` round-trips)<br>3. new `--transport streamable-http` (2025-03-26 spec, stateless multi-tenant; supported by DSH's dsh-mcp-client)<br>4. DeepSeek Harness integration guide + agent instructions (`docs/dsh/`)<br>5. streamable-http smoke test<br>6. keyword-extraction prompt overhauled (semantic completeness + colloquial variants + phrases) + Search returns all relevance-ordered topics (scene-context truncation removed), LoCoMo recall 0.392 → 0.668, entity_hit 0.284 → 0.877 |
| v1.2.1 | 2026-08-16 | MCP server + L5 capability layer | 1. New `cmd/memhop-mcp` binary: multi-tenant SSE MCP server (official go-sdk v1.7.0) mapping the full public API to 28 tools (search/update/dream/checkpoint/status, profile, scenes, knowledge, archive, capabilities, trajectory/crystallize)<br>2. tenant path isolation `/mcp/<tenant-id>`<br>3. graceful shutdown persists via snapshot<br>4. offline SSE smoke tests (`make test-mcp`)<br>5. usage docs under `docs/mcp/` (local)<br>6. L5 plugin layer refactored into the capability layer (`memhop-capability/v1`: manual/atomic/composite kinds, draft→active lifecycle via `ActivateCapability`, fingerprint dedup, Crystallize emits create/reuse/merge candidates)<br>7. built-in capability toolbox (`capabilities/`, embedded, read-only, attached at Open)<br>8. `Update` returns `(bool, error)`<br>9. `.meh` format bumped to `0x0005` — 0x0004 files (v1.2.0 plugin records) are rejected at Open, no migration<br>10. encoder health check requires a 2xx HEAD on the endpoint root (no fallback)<br>11. active scenes bounded by `Capacity` (default 7, oldest evicted from Dream targets)<br>12. `RecordEnd` header field + A/B header damage recovery |
| v1.2.0 | 2026-08-14 | L5 plugin layer | 1. L5 action chains → plugin slots (PluginSlot + structured five-section manifest: skills / MCPs / tools / prompts / services)<br>2. path-only import via `ImportPlugin`, hand-written create/update removed<br>3. Crystallize dispatches plugins by type from L7 trajectories<br>4. `SearchResult.Crystals` → `Plugins`<br>5. eight-layer architecture (L0–L7) docs |
| v1.1.0 | 2026-07-27 ~ 08.11 | Architecture refactor | 1. Layered `internal` rewrite (assembly → sub → repo → core/index/common)<br>2. f16 → f32 single-precision vectors<br>3. topic centroid vector retrieval<br>4. `BatchStore` removed<br>5. `Dream(ctx)` narrowed to `(bool, error)`<br>6. `.meh` format `0x0004`, incompatible with v1 data<br>7. integration tests rebuilt against the new internal API |
| v1.0.0 | 2026-07-26 | First stable release | Go rewrite with six-layer cognitive architecture, V2 .meh storage, BM25+vector+entity RRF search, Dream consolidation pipeline, L3 hypergraph with community detection. |
| v0.54–v0.58 | 2026-07-16 ~ 07-23 | Go Rewrite | 1. v0.58: Unified RRF — additive scene bonuses, three-channel fusion, L6 removed, atomic.Pointer<br>2. v0.57: Dream narrowed to L0+L1+L2, LLM hardening, L5 Write API, SkipDistill<br>3. v0.55: Stability — IVF removed, panic→error, crash recovery, L5 write pipeline<br>4. v0.54: Go foundation — 4-layer arch, V2 .meh storage, 2 deps, log/slog |
| v0.18–v0.63 | 2026-05-31 ~ 07-10 | Rust | 1. V2 append-only `.meh` with snapshot/checkpoint<br>2. BM25 + IVF hybrid retrieval<br>3. L3 hypergraph DSL, community detection (clique + Louvain), BFS/caching<br>4. Full Dream pipeline: L3 distill → L2 compress → L1 decay → L0 rebuild → L5 crystallize<br>5. FFI (cdylib), MCP Server, gRPC/Unix Socket encoder |
| v0.6–v0.17 | 2026-05-20 ~ 05-25 | Rust Early | 1. Pure Rust single crate (dropped Python bindings)<br>2. LMDB to custom `.meh` storage migration<br>3. 4-layer to 6-layer cognitive architecture evolution<br>4. MCP Server integration<br>5. HNSW vector index (replaced brute-force) |
| v0.1–v0.5 | 2026-05-19 ~ 05-24 | Python | 1. Hopfield associative memory network<br>2. LMDB embedded storage, `pip install` one-click<br>3. O(1) associative recall with confidence scoring<br>4. BrainLoop self-circulating agent loop<br>5. Proved "living memory" concept |

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
