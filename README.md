<p align="center">
  <h1 align="center">MemHop</h1>
  <p align="center">
    <strong>Long-term memory for AI agents — a six-layer cognitive memory database in a single embedded file. Pure Go, zero infrastructure.</strong>
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
  <strong>Current: v1.6.5</strong>
</p>

---

MemHop is an **embedded long-term memory database for AI agents and LLM applications**, written in pure Go. It is not a vector database — it is a memory system modeled after how the human brain organizes knowledge, with identity, episodic recall, semantic compression, a knowledge graph and archival storage. One agent, one `.meh` file, zero infrastructure.

MemHop is an **agent-dedicated** memory database: each agent binds to exactly one `.meh` file, and a file-level exclusive lock guarantees a single instance per file (a second `Open` fails fast). It runs on **Linux, macOS, and Windows** with no cgo and no external service beyond your LLM endpoint.

Built as the brain memory of [MeowAgent](https://github.com/meowagent/meowagent) (coming soon), MemHop works as an embedded organ rather than a standalone service. No server to run, no configuration to manage — just open a file and your agent has memory.

> **Our stance on agent memory.** Memory should not be an afterthought bolted on with a vector database plugin or a plain-text log dumped into a context window. An agent without internalised memory is just a stateless function pretending to be intelligent. MemHop exists because we believe memory must be *cognitive* — structured, compressed, consolidated, and forgotten the way a human brain does — and *embedded* — living inside the agent process itself, not behind a network call. One file, zero infrastructure, a mind that grows with every conversation.

## Features

- **Six-Layer Architecture** — L0 Profile → L1 Engram → L2 Context → L3 Knowledge → L4 Archive → L5 Plan, with Dream consolidation
- **Scene-is-the-session memory loop** — one L2 scene = one host session. `Search` reads that scene's depth-1 topic set straight from the in-memory cache (zero LLM, zero embedding, no scoring) *and opens the turn*, and from then on the library is what holds both ids — the write calls name no scene and no topic. The host records the turn itself: `AppendArchive` writes what happened while it ran (dialogue originals and operation events being L4 content that differs only by `Kind`), and `Update` closes the turn with how it opened and how it ended, distilling its utterances into that topic's keywords in exactly one LLM call. Everything a turn holds lives under that one id, and the task tree it opened is L5. The scene's `FusedKeywords` set *is* the context a host injects
- **V2 Storage** — `.meh` format (`FormatVersion=0x0012`) with A/B dual headers, per-record CRC32 + torn-write truncation recovery, mmap zero-copy, snapshot/checkpoint. Record frames carry an 8-byte `agent_id` (26-byte header) and the engine indexes every record by `(agent, idHash)` domain. The L3 knowledge graph lives in the file-wide reserved shared domain, which holds nothing else. **Only `0x0012` opens** — files at `0x0011` or older are rejected, with no migration path: an older file's profiles carry no `agent_type`, so every domain in it decodes as the primary agent — not one wrong value somewhere but the same wrong value in every domain at once, against a rule that a file has exactly one
- **Multi-Agent Domains** — `Open(path, llm, defaults, profile)` → a DB handle whose domains are reached as handles, never as ids: `Primary()` for the domain the file was opened on, `SubAgent(llm, profile)` for one created under it and addressed by its name. Many agents share one `.meh` file with fully isolated per-agent domains (caches, Dream pipelines, domain locks); same-agent operations serialize, different agents run in parallel; idle domains reclaim memory on access cadence (`Defaults.AgentIdleTTLMs`) while their records stay on disk. One exception (below): the L3 knowledge graph is file-wide shared
- **L1 Scene Hypergraph** — Dream creates co-occurrence hyperedges between scenes whose keyword sets overlap (Jaccard ≥ 0.15, a floor the engine fixes rather than a knob) and decays/prunes them over time; an edge is weighted by the similarity it was born from and fades from there — it strengthens only when an endpoint's turn list came out different — a turn lost counting as much as one gained — so re-measuring a pair whose lists are unchanged, even after the same turns are re-distilled into different keywords, never undoes the fade. L1 is maintained by Dream for explicit graph queries and future association — reads never score or spread activation
- **Dream Pipeline** — consolidation over L0–L2 plus retention on both content and plan: L2 compress → index rebuild → L1 nodes/hyperedges rebuild → L1 decay → L0 distill (emotion/MBTI), with `l4_prune` (drops a topic's content past the retention window — seven days by default, `Defaults.ContentRetentionMs`) and `l5_prune` (drops plan nodes past it, exempting a tree still in flight) on every pass; returns a per-stage `DreamReport`
- **L3 Knowledge Graph** — multiple independent hypergraphs with node import carrying positional source refs and relation edges (an edge is its members plus its kind, so one node pair can hold several relations), graph deletion, keyword/type/id lookup that ANDs together, and BFS subgraph queries. The graph pool is **file-wide**: every agent domain of the file shares one L3 pool (project knowledge is imported once, visible to all), and the pool's lifetime is the file's, not any one domain's
- **Single Instance by Design** — one `.meh` file has exactly one owner: a cross-platform exclusive lock (linux/darwin/windows) makes a second open fail fast, and the embedded path runs with no server process and no background daemon
- **Minimal & Embeddable** — 3 direct Go deps (xxhash, go-openai, golang.org/x/sys) — **the engine contacts no embedding / vector service at all**, and there is no dimension to declare in the config; `sync.RWMutex` + `atomic.Pointer`, zero infrastructure

## Quick Start

> Full integration guide (config, all layer APIs, turns and trajectories, pitfalls):
> [INTEGRATION_GUIDE.md](INTEGRATION_GUIDE.md) · 中文: [INTEGRATION_GUIDE.zh.md](INTEGRATION_GUIDE.zh.md)

```go
import (
    "context"
    "log"
    "os"
    "time"

    memhop "github.com/qyiun666/MemHop/api"
)

db, err := memhop.Open(
    "agent.meh", // the whole database; no server, no dimension to declare
    memhop.LlmConfig{ // required, validated before the path is touched
        APIURL: "https://api.openai.com/v1",
        APIKey: os.Getenv("OPENAI_API_KEY"),
        Model:  "gpt-4o-mini",
    },
    memhop.DefaultMemHopDefaults,
    // Required for a file that is not there yet: opening one has to know whose
    // memory it is. An existing file keeps the primary it already has.
    &memhop.ProfileInput{Name: "my-agent", Role: "assistant"},
)
if err != nil {
    log.Fatal(err)
}
defer db.Close()

// One .meh file carries isolated domains, and they come back as handles rather
// than as ids. Primary is the domain the file was opened on; SubAgent creates
// (or returns) one addressed by name, optionally on its own LLM endpoint.
sess, err := db.Primary()
if err != nil {
    log.Fatal(err)
}

// Read memory = read one scene (a scene IS a host session), which also
// opens the turn about to run. An empty SceneID continues the domain's current
// scene — after a reopen it restores the one whose turn counter ran furthest —
// so a host running one agent over one library names no scene at all;
// NewScene: true is the only way to start a fresh conversation. Naming a
// SceneID scopes this read to it and it must already exist, otherwise
// ErrNotFound (L3ID anchors only a read that creates a scene). The read costs
// no LLM, no embedding and no scoring; NewTopicID is the topic this turn
// lives in.
res, err := sess.Search(memhop.SearchQuery{})
if err != nil {
    log.Fatal(err)
}
for _, topic := range res.Topics { // this session's depth-1 set = the context
    _ = topic.FusedKeywords
}

// While the turn runs, the host records what happened into the turn Search
// opened — which turn that is the library remembers, so this call names no ids
// and with no turn open it is refused. Dialogue and events are the same kind of
// record: they differ only by Kind. Each call returns the slot its record took.
_, _ = sess.AppendArchive(memhop.ArchiveInput{
    Kind:      memhop.KindEvent,
    EventType: "tool_call",
    Content:   `{"tool":"grep"}`,
    CreatedAt: time.Now().UnixMilli(),
})

// End of turn: Input and Output land on the dialogue slots a reader looks for
// them on (Seq 1 and 2), so closing the same turn again rewrites those two
// lines instead of accumulating; Outcome is the host's own word for how the
// round ended, stored as one event per call. The turn's utterances are then
// distilled into its keyword track, returned with the topic.
topic, err := sess.Update(memhop.TurnEnd{
    Input:     "What did we discuss yesterday?",
    Output:    "Agent: ...",
    Outcome:   "resolved",
    CreatedAt: time.Now().UnixMilli(),
})
if err != nil {
    log.Fatal(err)
}
_ = topic.FusedKeywords

// Dream consolidation (L0-L2); an empty sceneID sweeps every scene of the
// domain. Update already schedules it in the background once a scene's
// topic count passes the threshold, so hosts rarely call it.
report, err := sess.Dream(context.Background(), "")
```


> **Concurrency contract.** Same-agent operations (Search / Update / Dream / write APIs) are serialized by the library's per-agent domain lock; different agents run in parallel on a `*DB`, so the host needs no external queue. `*memhop.Session` carries no cross-domain state beyond its bound domain — the scene and turn it is working are held by the domain, not by the caller. The file's exclusive lock still allows only one process per `.meh` file; `DB` exposes no locking API: the domain lock is the library's, and a host critical section needs its own. Nothing about the open turn is process-wide, so one more agent is one more `Open` — including one made between another round's start and its close, which leaves each round's keys and content apart.

Prerequisites: Go 1.27+ and an OpenAI-compatible LLM endpoint (configured through `api.LlmConfig`, which `api.Open` takes) — no embedding / vector service needed

### API Overview

| Group | Methods |
|-------|---------|
| Core loop | `Search(q) → opens the turn` · `PlanNodeAdd(parentSeq, title) → seq` (parentSeq 0 opens the turn's tree) / `PlanNodeUpdate(PlanStep{Seq, Status, …})` (the plan comes first, one step at a time) · `AppendArchive(ArchiveInput{...}) → seq` · `Update(TurnEnd{Input, Output, Outcome, CreatedAt}) → topic` · `Dream(ctx, sceneID)` — none of the turn writes names a scene or a topic id: `Search` opens the turn they all key on |
| L0 Profile | `GetL0` · `UpdateL0` |
| L1 Engram (read-only) | `ListL1() → []SceneNodeView` — every scene node of the domain in a stable order, ids as hex. Dream builds the nodes and the co-occurrence edges between them and is the only writer, so there is no L1 write call. `Importance` / `Valence` / `Arousal` are what consolidation computed, and `EmotionSet` says whether any pass ever stamped the two signals (0 is a legal reading, so the values alone cannot); `EdgeIDs` has no read of its own — two nodes sharing one are a pair Dream judged related |
| L2 Context | `ListScenes([l3ID])` · `UpdateScene(id, {Name, L3ID, Force})` · `RenameTopic(topicID, name)` · `SceneContext(sceneID, or "" for the domain's current scene)` · `MergeScenes` · `DeleteTopic` · `DeleteScene` |
| L3 Knowledge | `GetL3` · `ListL3` · `ImportL3` (reports every graph the batch resolved a domain into, including one it added nothing new to) · `UpdateL3` · `DeleteL3` · `QueryL3Nodes` · `QueryL3Subgraph` (its `edgeKinds` narrows the walk; an undefined kind is refused, not answered with an empty subgraph) |
| L4 Archive | `AppendArchive(ArchiveInput{Kind, Seq, Role, ContentType, EventType, NodeSeq, Content, CreatedAt}) → seq` is the only way content enters a topic, and the topic it enters is the turn `Search` opened: the call names no ids, so a mistyped or invented key is not something a host can pass, and with no turn open it is refused (`ErrInvalidQuery`) rather than landing content under a key no read ever lists, `Seq: 0` allocates and the slot taken comes back, and naming a held slot rewrites it. An event's `NodeSeq` has to name a step this turn created (`0` binds it to nothing). `SearchL4(q)` is the one read over both kinds: keyword (case-insensitive), time range, ids, topic, `Kind` (utterance / event), `NodeSeq` (one plan step **and every step under it**, which means nothing outside its turn; `0` leaves the condition unset) and content type are conditions, not modes — an unset `Kind` selects both; `Limit` keeps the tail of the read's order (slot order inside one topic, record-time order across topics) |
| Turn events (L4, kind `event`) | A turn's events are L4 content of kind `event` under the topic id Search issued for it (swept past the retention window — seven days by default, configurable; no delete API); read them with `SearchL4(L4Query{TopicID, Kind: &KindEvent})`, append them with `AppendArchive`. A topic's first event is Seq 3, because slots 1 and 2 belong to its dialogue |
| L5 Plan tree | `PlanNodeAdd(parentSeq, title) → seq` · `PlanNodeUpdate(PlanStep{Seq, Status, Title, Summary})` · `PlanState()` — the tree is what L5 itself records: one node per record, addressed by the turn **plus a step ordinal the library hands out** (`1, 2, 3 …` inside that turn — issued above everything that still names one: this turn's live steps **and** the highest ordinal its surviving events bound to a step, since a step and its events share one address while the two age separately. Not permanently unique — a sweep frees a swept step's ordinal once the events naming it go too — and a turn's numbering can therefore carry a gap, so a host reads no density into it and treats an ordinal it held before, on a turn older than the retention window, as a new step's address); the turn is the one `Search` opened, so these calls name no id, which makes `PlanState()` and `SearchL4{TopicID, Kind}` one key across two stores, and `SearchL4{TopicID, NodeSeq}` reads back the work of one step and every step under it. A node exists only because something created it: a `parentSeq` of 0 opens the turn's tree, one naming a step the tree does not hold is refused rather than answered by growing one, and `PlanNodeUpdate` restates a step that is already there — `Status` every time (`in_progress` / `done` / `failed`, there is no "leave it" spelling), `Title`/`Summary` kept when left blank. A created step starts `in_progress`, so the model has no "planned but not started" state. A plan write stores no content: a step's events are L4 records the host appends itself |
| DB handle | `Open(path, llm, defaults, profile)` · `Primary()` · `SubAgent(llm, profile)` · `Checkpoint` · `CompactTo(newPath)` (defragmented copy; the destination is the caller's to constrain) · `Stats` (file bytes and live record count — the numbers a compaction decision is made from) · `Close` · `IsClosed` |

## Architecture

```
Layer   Name             Human Parallel          Mechanism
─────   ──────────────   ───────────────────     ─────────────────────────────────────────────
 L5     Plan             Task tree               One node per step, keyed by the turn that opened it; expired trees are swept by Dream
 L4     Archive            Turn content            A turn's dialogue originals and operation events (Kind), addressed by (topic, Seq); retention window (seven days by default) — the keyword track is what outlives it
 L3     Knowledge        Semantic memory         Multi-source hypergraph knowledge base
 L2     Context          Working memory          Topics on a scene's surface, plus turns Dream folded under a fused group (one sink per topic)
 L1     Engram           Scene hypergraph        Scene nodes + keyword-overlap hyperedges; maintained by Dream for explicit graph queries
 L0     Profile          Identity                Agent personality, preferences & language habits
```

### Dream Pipeline

The Dream cycle is an automatic consolidation pass inspired by how sleep processes the day's experiences. It acts on **L0–L2 only** (L3 distillation is out of scope) plus retention pruning over L4 content and L5 plan nodes:

1. **L2 compression** — the LLM groups related topics per scene; each target scene runs in its own goroutine under a bounded fan-out (the whole pass holds the domain lock, so the scene count is not the concurrency), sinking merged topics under a new depth-1 fused node
2. **L1 rebuild** — scene nodes are synced from L2, the L2Meta topic cache is rebuilt in the same scan, and keyword-overlap hyperedges are created or refreshed
3. **L1 decay** — scene importance and edge weights decay over time, weak nodes are pruned
4. **L0 distill** — emotion/MBTI and a personality summary are distilled from the ranked L1 samples and merged into the stored profile: `Name`, `Role` and `Preferences` are left alone, and `Personality` is the one host-written field Dream also refines, so a host write that omits it clears the refined value until the next pass. The per-node emotions that same reply carries are backfilled onto the L1 nodes that have never been stamped — a node already carrying a reading keeps it, including a legal all-zero one; the stage is skipped when there are no samples to read

Trigger: once a scene's depth-1 topic count passes `Defaults.SceneDreamTopicThreshold` (default 24), `Update` schedules that scene's Dream in the background, one pass in flight per scene; hosts may also call it. `Dream(ctx, sceneID) (*DreamReport, error)` holds the domain lock for the whole cycle, sweeps every scene of the domain when `sceneID` is empty (scenes below `DreamCompressMinTopics` are skipped) and honours `ctx` cancellation between stages.

### Read & write path

**There is no scored retrieval.** A scene is a host session, so the engine never guesses which scene a message belongs to:

| Path | What it does | Cost |
|------|--------------|------|
| `Search(SearchQuery{SceneID, L3ID, NewScene})` | empty `SceneID` → continue the domain's current scene (after a reopen it restores from the records, the one whose turn counter ran furthest), creating one only when the domain holds none; `NewScene: true` → a fresh scene, the only way to start a second conversation over one domain; a named `SceneID` → that scene's depth-1 topics (user-timestamp order) plus the L0 profile — and `NewTopicID`, the topic this read opens for the coming turn | in-memory read (L2Meta), zero LLM / embedding / scoring; the only write is the scene record (its turn counter) |
| `AppendArchive(ArchiveInput{Kind, ...}) → seq` | the turn's only content write, and what it writes into is the turn `Search` opened — which turn that is the library remembers, so this call names no ids and a mistyped or invented topic id is not even something a host can pass; with no turn open it is refused instead of landing content under a key no read ever lists. An utterance declares who spoke and what the content is; an event names itself and may hang on a plan step. `Seq: 0` allocates above the two dialogue slots, and the slot taken comes back | zero LLM; a refused record stores nothing, and an event naming a step the tree does not hold is refused rather than answered by creating one; budgets are 4 KiB per event record, name included, and 64 KiB per utterance, refused rather than truncated |
| `Update(TurnEnd{Input, Output, Outcome, CreatedAt}) → topic` | closes that turn: `Input` and `Output` land on its two dialogue slots (`Seq` 1 and 2), so re-closing a turn rewrites those lines instead of accumulating versions, and `Outcome` — the host's own word for which arm ended the round, which the engine never branches on — goes in as one `turn_outcome` event per call. It then distills the turn's utterances into its keyword track and returns the topic as stored, the track among its fields | exactly one LLM call per turn, and it runs before the topic is written, so a failure leaves no topic. A turn whose content the retention window already reclaimed is refused with `ErrInvalidQuery` without reaching the LLM, and so is this call when no turn is open |

What a host injects as context is the keyword set of that scene's depth-1 topics; to read a turn's original text, address L4 by that turn's topic id — `SearchL4(L4Query{TopicID, Kind: &KindUtterance})` — or use `SceneContext`, which already carries the messages. Content is bounded: past the retention window (seven days by default) a topic keeps its keyword track and its `Messages` come back empty or with a gap in `Seq`, which is a legal end state rather than a failed read. The injected size is kept in check by Dream converging each scene towards `DreamCompressMinTopics` (default 20) — a target the consolidation pass aims at, not a bound a scene is held to, so leaving automatic consolidation on is what keeps the context from growing without limit.

Removed along with retrieval: three-channel RRF scoring, L1 spreading activation (`AssociatedContexts`), topic centroids and the embedding dependency, the `AutoCreate` / `DirectedL2ID` / `DirectedL3ID` routes, and topic-level `L3Refs` (L2↔L3 now lives solely on the scene anchor `SceneSlot.L3ID`).


## Testing & Benchmarks

MemHop's test suite exercises only the public `api` surface — exactly the calls a host (e.g. MeowAgent) makes — and asserts the engine's own memory structures, not external answerability judges.

### Integration tests (`test/`, build tag `integration`)

- **Memory loop** (`TestCoreCycleUpdateDream`): N turns settled into one scene the way a real host does, with **periodic L0/L2/L4 consistency checks** every few turns — L0 profile readable, the scene read non-empty, L4 holding the raw utterance verbatim. After Dream consolidation the scene surface must shrink while every fact stays recoverable from L4. Inside one run this is a same-turn check: L4 content carries a 7-day window, so what Dream leaves behind long-term is the fused keyword track, not the text.
- **Keyword fidelity & persistence** (`TestKeywordFidelity`/`TestKeywordPersistence`/`TestDreamCompressionFidelity`): the keywords distilled from a turn faithfully carry its meaning, survive noise turns, and stay faithful across Dream compression.
- **API contracts** (`TestInterface*`: reads make zero LLM calls, writes cost exactly one distillation per turn, unknown scenes are rejected, checkpoints survive a restart), **e2e flows** (`TestE2E*`), **long-input robustness** (`TestExtractKeywordsLongInputRealLLM`/`TestUpdateLongTurnSettles`).

### Benchmarks (`go test -tags integration -bench .`)

All benchmarks drive the real api loop (real LLM, no external judge):

| Benchmark | Measures |
|-----------|----------|
| `BenchmarkMemoryLoop` | steady-state Search+Update loop including the engine's **auto-scheduled Dream** (a scene's depth-1 topic count passing the threshold) and periodic L0/L2 verification |
| `BenchmarkUpdateTurn` | one turn end to end: the read that opens it, then the close (the two dialogue writes, one distillation, the topic write) |
| `BenchmarkSceneRead` / `BenchmarkSceneReadLatency` | scene-read throughput and latency distribution (min/p50/p95/max) |
| `BenchmarkDreamConsolidation` | full Dream pipeline latency |


### Why no external dataset benchmark?

Public memory benchmarks (LoCoMo, LongMemEval) evaluate "retrieval → LLM-judged answerability" — a different question than what MemHop's layered design asserts (L0 profile distillation, L1 scene-graph coherence, L2 compression semantics, L4 verbatim archival). LongMemEval, the closest fit (multi-session user-assistant chats, ~500 QA), needs 115K–1.5M tokens per question and is not a practical continuous-integration target. MemHop therefore verifies its memory structures directly through the api loop instead of chasing a generic QA score.

## Project Structure

```
api/                         ← Public facade: open (the one entry) / session (the only
                               business handle, hex-id surface) / types / mapping / errors / exports
internal/ (root)             ← Big methods + composition root: config / db / session / models /
                               exports + agents / l0…l5 / l3query / search / update / dream
internal/scene|turn|dream|graph|content|plan
                             ← Layer-3 small methods, one package per cognitive face: none of them
                               takes the domain lock and none imports a sibling
internal/domain              ← Per-agent state: Context (domain lock, the three caches, OpCtx),
                               plan cache, L2Meta mirror maintenance
internal/config              ← Config types: the LLM endpoint and the tuning defaults a host supplies,
                               plus the bundle the assembly layer builds out of them
internal/llm                 ← OpenAI-compatible transport: Chat + the truncation-escalating retry
internal/cap/                ← Layer-4 capabilities, identity-neutral, dependencies injected:
                               engram / llmops / profile / knowledge
internal/repo/               ← Data layer: l0layer–l5layer + agentlayer (record read/write)
internal/repo/index/         ← Index layer: l2meta / rebuild (single-pass scan) /
                               l4 (the content each topic owns)
internal/repo/core/          ← .meh engine: engine / frame / header / snapshot / reclaim /
                               record / model / mmap / filelock
internal/common/             ← Bottom-layer utilities: enum / errors / hash / sliceutil / timeutil
test/                         ← Integration tests (build tag: integration): the offline host-face
                               suite and the real-LLM half
benches/fixtures/             ← Benchmark datasets (locomo10, locomo_smoke, longmemeval_smoke)
```

Dependency direction is strictly one-way: `api → internal → repo → core`, with `common` at the bottom (no references to any other internal package).


> Note: `docs/` and `AGENTS.md` are intentionally kept local-only (see `.gitignore`), so links under `docs/` may not resolve in a public clone.

### LLM Call Cost Model

- **Read path** (`Search`): **zero LLM, zero embedding** — served from the L2Meta cache alone.
- **Write path** (`Update`): exactly one keyword distillation per turn (both originals fed together), a 512-token output cap escalating on truncation, then one format-constrained retry — a reply that still will not parse is `ErrLLM` and that turn writes nothing.
- **Dream**: one consolidation call per scene reaching the topic floor (`DreamCompressMinTopics`, default 20), plus one distill call with at most 200 ranked L1 samples (up to 20 keywords each). Output caps: 8192 / 2048 tokens.
- Use a small/fast chat model (a cheap API model or a local OpenAI-compatible endpoint) for the configured LLM when latency and cost matter; keyword distillation does not need a frontier model.
- **The bill is per closing call, not per round.** Two closes of one turn are two distillations and still one topic — the dialogue slots keep the last pair of words while both endings stay as events on its track. A round that suspends for input and resumes is read and closed once per invocation, so it is two turns, not one closed twice.

## Development

```bash
go build ./...                          # Build
go vet ./...                            # Static analysis
go test ./internal/...                  # Unit tests (no external services)
go test -tags integration ./test/...    # Integration tests (requires an LLM key)
make check-guides                       # Compile the runnable skeleton both guides embed
```

Integration tests run against a real LLM (the engine needs no embedding service). Configure the LLM via environment variables `MEMHOP_TEST_LLM_KEY` / `MEMHOP_TEST_LLM_URL` / `MEMHOP_TEST_LLM_MODEL` (defaults to the DeepSeek endpoint when only the key is set), or via `test/testsupport/key_config.json`.

## Changelog

Each row keeps at most five highlights; the full numbered log lives in [CHANGELOG.md](CHANGELOG.md).

| Version | Date | Highlight | Core Changes |
|---------|------|-----------|--------------|
| v1.6.6 | 2026-09-23 | A turn closes in one call: the domain holds its own scene and turn, and the write shape carries no address |1. **One `Update(TurnEnd{Input,Output,Outcome,CreatedAt})` closes a round** - `Settle` is gone; Input/Output overwrite the turn topic's Seq 1/2, Outcome appends per call as a `turn_outcome` event. The domain itself holds the scene and turn keys: `Search` continues the current scene and opens the turn, and `AppendArchive`'s write shape (`ArchiveInput`) carries no address - a turn the host is not in cannot be written.<br>2. **A domain can be held by one identifier**: `Session.AgentID` reads the domain's library-issued id, `DB.Agent(llm, id)` opens that same domain back (it creates nothing - an unknown or non-hex id is refused), and `DB.Agents()` lists every domain the file holds.<br>3. **A parent's folded summary is derived text**: the plan node marks folded summaries (`summary_folded`), so they re-derive when the branch changes while host-written text is never rewritten.<br>4. **Fixed a real defect: merging swallowed a conversation's project membership** - an unanchored scene absorbing an anchored one dropped it from the project listing; the survivor now adopts the swallowed scene's anchor, and conflicting anchors refuse the whole merge before any deletion.<br>5. **Fixed a real defect found by CI on Linux: same-millisecond round opens let the scene id hash decide which conversation a reopen resumes** - `last_used_at` stamps are now strictly increasing inside the domain.|
| v1.6.5 | 2026-09-22 | The MCP surface is retired: the Go module is the only way in |1. **`cmd/memhop-mcp` deleted whole** (25 tools, multi-tenant HTTP over SSE and streamable-http, the tenant registry and the `--tenants` list that bounded how many domains one file may grow). 2. **Deps 4 → 3**: it was the only reader of `modelcontextprotocol/go-sdk`, so `go mod tidy` also drops the 7 indirect deps it carried. 3. **Go surface unchanged**: `Session` 25 + `DB` 7, with `api.CodeOf` kept as the host's seam for reading an error code. 4. `make build-mcp`/`test-mcp` are gone and the CI/hook package lists lose `cmd`. 5. Every "Go-only" phrasing becomes a constraint on the caller (`CompactTo`'s argument is an arbitrary write path; the host pins the destination), and one attribution is fixed: `SceneContextTopic.messages` is empty in JSON because of `omitempty`, not because a tool chose it. Disk format `0x0012` unchanged|
| v1.6.4 | 2026-09-11 | The surface is rebuilt around `Open`: domains come back as handles, and an id never crosses the boundary; the package audit that followed put each method where it belongs and deleted what nothing read |1. **The entry point is `Open(path, llm, defaults, profile)` → `*api.DB`**, decided by what the file holds - both refusals happen before the filesystem is touched - and domains come back as handles: `Primary()` / `SubAgent(llm, profile)`; the host never holds or echoes an agent id.<br>2. **L0 gains `AgentType`** (0 = primary, 1 = sub), stamped at creation so profile edits cannot move a domain between the two; format `0x0011` → `0x0012`, older files refused at Open with no migration.<br>3. **Retired whole**: the capability surface (`Crystallize`, `ParseCapabilityPackage` / `ValidateCapabilityCard`), `ListTrajectorySessions`, `DeleteAgent` and its delete chain - surface 27 + 8 → 26 + 6.<br>4. **Public surface converged (breaking)**: a topic stores no child list (`children_ids` gone, the closure is computed from `parent_id`), profile writes take `api.ProfileInput` (four host fields, `Name` required), two pure-`len` keys deleted, a graph's `updated_at` becomes its content clock.<br>5. **The review rounds that followed swept listing order, memory quality and the kernel's destruction paths** - among them an `O_TRUNC` that cleared a file another process still held; every finding is in the CHANGELOG.|
| v1.6.3 | 2026-09-10 | Content is L4's only story: a turn's dialogue and events share one layer, L5 keeps only the plan tree, `Update` only distills |1. **Records merged into L4**: `ArchiveSlot` gains `Kind`/`Seq`/`EventType`/`NodeSeq` and the archive id is positional (derived from the turn's topic and the slot ordinal), so rewriting one slot overwrites in place - the replay-diff tombstone machinery is gone.<br>2. **L6 is plan nodes only**: `core.PlanNode` on frame `0x0F`; `TrajectorySlot`, `NodeType*` and `PlanNodeRef` deleted. Files at `0x000E` and older are refused at Open, no migration.<br>3. **`AppendArchive` is the only content write** (validation before any write, over-budget refused rather than truncated) and **`Update` writes no content** - it renders the topic's utterances and distills once; a failed distillation leaves the host's records exactly where it put them.<br>4. **Surface 26 → 25**: `AppendTrajectory` and `ReadTrajectory` deleted (the read is `SearchL4{TopicID, Kind}`), `AppendArchive` added.<br>5. **The plan layer re-shaped**: a turn's tree is written one step at a time (`PlanCreate`/`PlanNodeAdd`/`PlanNodeUpdate`), a step is addressed by a per-turn ordinal, and an event naming a step the tree does not hold is refused instead of growing a branch.|
| v1.6.2 | 2026-09-07 | Plan-event naming belongs to the host (vocabulary retired) |1. **`planEventTypes` and the `plan.ValidateEvent` wrapper deleted**: a plan-bound event (`nodePath` non-empty) takes `EventType` exactly as a bare turn event does — any non-empty name the host chooses, stored verbatim. The constraint did not earn its keep: the engine never branches on `EventType` (every non-test reference in the repo is that one lookup, `ReadTrajectory` echoing the field, and one `fmt.Fprintf` line in the Crystallize prompt), so refusing writes was its only behavior, and the cost sat entirely on the host — a rejected event name silently thins that turn's trajectory, which is how sandbox-adjudication prompts fell out of the audit log<br>2. **One validation point remains**: `trajectory.ValidateEvent` (non-empty `EventType` + `Timestamp > 0` + payload ≤ 4 KB). `AppendTrajectory`'s plan branch and `PlanCommit` still call it **before** `EnsureNode` / `UpdateNodeLocked`, so a refused write leaves the tree and the event log exactly as they were<br>3. **Delivery surfaces now agree**: MCP's `memhop_trajectory_append` already documented `event_type` as host-chosen, and the plan write surface is not on the MCP tool set at all — this round aligns the Go face with the wording the shipped surface already used; 24 tools unchanged<br>4. **That round added no method and touched no layout**: `event_type` is a JSON string inside the record, so neither tightening nor loosening changes the frame. (The method count and format version moved again in later, unreleased rounds — see the v1.6.3 row). **The change relaxes**, so hosts gain an auditable event without touching call sites<br>5. Tests: `TestPlanEventVocabularyRejectsUnknown` becomes `TestPlanEventNamesAreHostOwned` (a host-named plan event is accepted, its name is not rewritten, and an empty `EventType` is still refused before the node chain is built); three refusal assertions across `api` and `test` now trigger on an empty `EventType`. Decision record: `notes/implemented/simplification/2026-09-07-plan-event-vocabulary-retirement.md`|
| v1.6.1 | 2026-09-06 | Surface re-converged (34→32); builtin manual cards deleted; public face split by audience; L5 record layer retired (directory-as-capability) |1. **The L5 record layer retired (directory-as-capability)**: the engine no longer stores capability cards - the host's own `plug/<pkg>/capability.json` directory is the single source of truth. Five session methods and eight types went with it (public face 32 → 27).<br>2. **`ActivateCapability` deleted** (activation folds into `UpdateCapability`) and **`PlanReplace` deleted** (`SyncPlanTree(planID, nil)` wipes the tree): one capability, one implementation.<br>3. **Builtin manual cards deleted**: the nine in-memory "how to call the library" cards existed only to be skipped or to duplicate tool descriptions - a capability pool holds only LLM-triggerable units.<br>4. **Public face split by audience** (task face vs assembly/admin face) - the grouping that still governs the surface.<br>5. **Format `0x000B` → `0x000C`**: older files are refused at Open, no migration.|
| v1.6.0 | 2026-09-04 | No-fallback interfaces; file-wide L3/L5 shared pools; unified capability card + `plug/` auto-injection |1. **The v1.5.0 tag was cut before its API surface was complete**; this release ships the finished line, with ids minted only by the library (`api.DefaultAgentID` names the implicit domain, `api.NewPlanID(name)` mints plan ids).<br>2. **Merged surface**: `SetSceneName`+`SetSceneL3ID` → `UpdateScene(id, ScenePatch)`, `PlanAppend` → `AppendTrajectory`, `ListScenesByL3` → `ListScenes(l3ID)`; nine one-off methods deleted (34 + 8).<br>3. **Interfaces refuse instead of falling back**: unparseable LLM keyword output is `ErrLLM` and the turn writes nothing (the gse/tokenizer fallback is gone, dependencies 5 → 4); over-budget payloads are rejected, not truncated.<br>4. **The L3 knowledge graph is a file-wide shared pool**: one graph for every agent domain, records in the reserved shared domain, scene anchor validation reads the pool.<br>5. **L5 unified card + file-wide capability pool + `plug/` auto-injection**; format 0x0009 → 0x000B, older files rejected at Open.|
| v1.5.0 | 2026-09-01 | L2 re-shape: a scene IS a host session, and the library owns the turn id |1. **`Search` reads the scene AND opens the turn**: input is `{scene_id, l3_id}` (both optional); the result carries the scene, its depth-1 topics and the topic id minted for the turn about to run. Every read-path LLM/embedding/scoring call is gone.<br>2. **`Update` settles the whole turn into that id**: one distillation runs before any write - a failed LLM call leaves nothing behind; replaying an id overwrites the turn in place.<br>3. **N:N append surface deleted** (`AppendL4Message`, `RefineTopicKeywords`): a turn's L4 originals are exactly its two texts; what happens between them belongs to the turn's trajectory.<br>4. **Single keyword track**: only `fused_keywords` remains - Dream compression, L1 hyperedges and host injection all read that one track.<br>5. **Retrieval subsystem deleted** (three-channel scenefind, RRF, scene bonuses, centroids, `Encoder`): the engine contacts no embedding service.|
| v1.4.2 | 2026-08-31 | L6 plan tree + L2 directory anchor |1. L6 carries a task tree: `TrajectorySlot.NodeType` splits turn events from plan nodes, node ids derived stably by `HashPlanNode(planID, nodePath)` under a `plan:` namespace, events bound via `PlanNodeRef`<br>2. three-form surface `PlanAppend` / `PlanCommit` / `PlanState` plus `PlanReplace` (re-plan, keeps planID), `SyncPlanTree` (whole-tree snapshot diff, emits no `plan_step`), `ListPlans` (restart recovery)<br>3. **Model A fold**: a parent turns `done` only on an explicit host commit; after each commit the `done` children's summaries roll up bottom-up in numeric `NodePath` order without clobbering a host-written parent summary<br>4. L2 scene → L3 directory anchor (N:1): `SceneSlot.L3ID`, optional `SearchQuery.L3ID` pre-filter with backfill on hit, `ListScenesByL3`, `SetSceneL3ID(sceneID, l3ID, force)` write-once unless correcting or clearing<br>5. hardening: `0000000000000000` is the reserved bare-event `PlanID` and rejected by all five plan entry points (`PlanReplace` on it used to delete every turn event of the domain); Dream's plan exemption narrowed to plans active inside the 7-day window, so abandoned plans no longer accumulate forever|
| v1.4.1 | 2026-08-28 | Type-contract cleanup: hex-ID DTOs, L0 profile v2, L3 hypergraph activation |1. api response DTOs are real structs — every ID field leaves as a 16-char hex string (`SearchResult.NewTopicID`, `AppendL4Message`, `AgentID()` included) with new `api.FormatID` / `api.ParseID` helpers<br>2. L0 profile v2 (`FormatVersion 0x0009`): field ownership (Name/Role/Preferences host-exclusive, Personality host-seeded + Dream-distilled), typed `EmotionState`/`MBTI` distillation signals, dead `lexicon`/`style_traits` removed<br>3. L3 import gains `source_ref` (positional reference) and `related` (same-graph hyperedges resolved by title, two-phase forward references, idempotent re-import; `edges_created` result field, `L3Relation` type exported)<br>4. L6 one-trajectory-per-turn: SessionID is a turn key (search opens, update closes), events carry `TopicID` for cross-turn crystallization, external surface trimmed to append/query (`TrajectoryStats` / `DeleteTrajectory` / `PruneTrajectory` removed, 33 → 31 tools), Dream `l6_prune` auto-drops events older than 7 days<br>5. **breaking**: `.meh` files with `FormatVersion != 0x0009` (i.e. ≤ 0x0008) are rejected at Open, no migration|
| v1.4.0 | 2026-08-26 | Multi-agent memory database |1. one `.meh` file carries many isolated agent domains: record frames gain `agent_id` (26-byte header), engine indexes and snapshots (0x02) are per-agent, tenant registry records map names to stable crypto/rand agentIDs<br>2. `api.OpenMulti` / `AgentSession` / `CreateAgent` / `ListAgents` / `DeleteAgent`; `Open` stays zero-change for single-agent hosts (default domain)<br>3. business layer rebuilt around per-agent `agentContext` with domain locks (same-agent serial, cross-agent parallel), idle-domain memory reclamation and scoped Dream pipelines<br>4. L7 trajectory layer renumbered to **L6** (cognitive layers converge to L0–L6)<br>5. **breaking**: `.meh` files with `FormatVersion <= 0x0007` are rejected at Open, no migration; promoted `internal.DB` methods on `api.DB` now carry an `agentID` parameter (facade methods unchanged), `Lock()` panics on a closed DB|
| v1.3.4 | 2026-08-26 | L5 tool-declaration isomorphism |1. `memhop-capability` format v3: `ResourceRef` renamed `description` → `desc` and gained `input` (JSON Schema string) / `output` — the tool-declaration fields now mirror the host tool spec shape (meowire `ToolSpec`) exactly, so hosts project capabilities with a pure field copy and zero format conversion<br>2. `WorkflowStep` gained `args` — action chains carry step parameters officially (no private config formats)<br>3. **breaking**: v2 cards are rejected at import (format must be `memhop-capability/v3`); stored capability records written by earlier versions lose `desc/input/output` on read|
| v1.3.3 | 2026-08-26 | Retrieval scoring normalization + defaults slimdown |1. vector floor fixed from overriding every other signal to lifting only below-threshold scenes (floor = threshold + cosine×0.5): real-signal ordering (RRF + keyword overlap + bonuses) wins, semantic fallback preserved<br>2. `MemHopDefaults` slimmed from 24 fields to 3 business knobs (`Capacity` / `DreamCompressMinTopics` / `SearchDreamContextThreshold`); 4 dead fields (`MaxResults` / `DefaultTimeoutSecs` / `DefaultMaxOutputTokens` / `MaxDepth`) removed and 16 tuning constants moved to package-private `internal/tuning.go`<br>3. **breaking**: hosts referencing removed fields must clean up|
| v1.3.2 | 2026-08-26 | API fixes: async Dream + deletion + Update simplification |1. Search/Update no longer block on an internally triggered Dream (background goroutine, per-scene in-flight dedup, Close cancels a pending Dream)<br>2. new `DeleteTopic` (subtree closure + L4 + indexes + parent ChildrenIDs pruning) and `DeleteScene` (scene + all topics + archives + L1 node + active set) for memory correction<br>3. `Update` returns `error` instead of `(bool, error)`<br>4. `SearchResult.ProfileBrief` — compact profile digest (name/role/top preferences/style/emotions, bounded)|
| v1.3.0 | 2026-08-26 | L1 scene hypergraph + spreading-activation association | 1. Dream creates real `RecL1Hyperedge` co-occurrence edges between scenes (keyword-overlap Jaccard ≥ `L1EdgeMinSimilarity`); Search `AssociatedContexts` replaced the no-op same-scene listing with a graph walk (activation × edge weight × dampening per hop, ≤ `L1EdgeMaxHops`, top `L1AssocMaxScenes` other scenes)<br>2. L6 scene-usage record removed — hit counters folded into the L2 `SceneSlot` (`HitCount`/`LastHitAt`)<br>3. `L1ReverseIndex` (incl. snapshot field) and 4 dead L1 functions removed; association is now a pure storage-level graph read<br>4. `.meh` format bumped to `0x0007` — 0x0006 files are rejected at Open, no migration<br>5. new defaults: `L1EdgeMinSimilarity` (0.15), `L1EdgeMaxHops` (2), `L1ActivationDampening` (0.5), `L1ActivationThreshold` (0.05), `L1AssocMaxScenes` (3)
| v1.2.7 | 2026-08-25 | Host alignment + bilingual integration guides |1. `Search(ctx, q)` and `RefineTopicKeywords(ctx, id)` accept a context (cancels LLM extraction, encoder calls, internally triggered Dream)<br>2. `api` exports `LlmConfig` / `MemHopDefaults` / `TopicSlot` / `ResourceRef` / `CrystallizeDetail` / `TrajectoryStats`<br>3. `AppendL4Message` (pure L4 append, no LLM)<br>4. active-scene capacity: Update triggers a Dream on the oldest scene at Capacity with a compressibility pre-check; `SearchDreamContextThreshold` zero-value guard<br>5. bilingual integration guides added at repo root (`INTEGRATION_GUIDE.md` / `INTEGRATION_GUIDE.zh.md`)|
| v1.2.5 | 2026-08-20 | MCP server rewritten |1. `cmd/memhop-mcp` fully rewritten against the `api` facade (v1.2.4 removed it): all 31 MCP tools map 1:1 to `api.DB` methods<br>2. multi-tenant HTTP — SSE + streamable-http (2025-03-26 spec, stateless), each tenant isolated by URL path `/mcp/<tenant-id>` into its own `.meh` file, lazy-open registry with a first-open mutex<br>3. all tool outputs serialize record IDs as 16-char hex strings (uint64 JSON numbers lose precision in JS/TS hosts)<br>4. go-sdk v1.7.0 back as a direct dep (3 → 4)|
| v1.2.4 | 2026-08-19 | api/ facade + internal/ flattening | 1. Public Go API moved from the root package to `github.com/qyiun666/MemHop/api` (root `memhop.go`/`types.go` removed)<br>2. `internal/sub/` flattened into `internal/` (`package sub` → `package internal`), `internal/sub/repo` → `internal/repo`, `internal/sub/common` → `internal/common`<br>3. `cmd/memhop-mcp` removed (rewritten in v1.2.5)<br>4. build config (Makefile fmt, pre-commit hook, CI gofmt) updated<br>5. breaking change: hosts importing the root package must switch to `/api` |
| v1.2.3 | 2026-08-18 | MCP compatibility fixes + DSH integration + retrieval quality |1. MCP tool schemas fixed (no-arg tools no longer emit `properties: null`, breaking strict clients)<br>2. all tool outputs render record IDs as 16-char hex strings (uint64 JSON numbers lose precision in JS/TS hosts, breaking `new_topic_id` round-trips)<br>3. new `--transport streamable-http` (2025-03-26 spec, stateless multi-tenant; supported by DSH's dsh-mcp-client)<br>4. keyword-extraction prompt overhauled (semantic completeness + colloquial variants + phrases) + Search returns all relevance-ordered topics (scene-context truncation removed), LoCoMo recall 0.392 → 0.668, entity_hit 0.284 → 0.877|
| v1.2.1 | 2026-08-16 | MCP server + L5 capability layer |1. New `cmd/memhop-mcp` binary: multi-tenant SSE MCP server (official go-sdk v1.7.0) mapping the full public API to 28 tools (search/update/dream/checkpoint/status, profile, scenes, knowledge, archive, capabilities, trajectory/crystallize)<br>2. tenant path isolation `/mcp/<tenant-id>`<br>3. L5 plugin layer refactored into the capability layer (`memhop-capability/v1`: manual/atomic/composite kinds, draft→active lifecycle via `ActivateCapability`, fingerprint dedup, Crystallize emits create/reuse/merge candidates)<br>4. `.meh` format bumped to `0x0005` — 0x0004 files (v1.2.0 plugin records) are rejected at Open, no migration<br>5. `RecordEnd` header field + A/B header damage recovery|
| v1.2.0 | 2026-08-14 | L5 plugin layer | 1. L5 action chains → plugin slots (PluginSlot + structured five-section manifest: skills / MCPs / tools / prompts / services)<br>2. path-only import via `ImportPlugin`, hand-written create/update removed<br>3. Crystallize dispatches plugins by type from L7 trajectories<br>4. `SearchResult.Crystals` → `Plugins`<br>5. eight-layer architecture (L0–L7) docs |
| v1.1.0 | 2026-07-27 ~ 08.11 | Architecture refactor |1. Layered `internal` rewrite (assembly → sub → repo → core/index/common)<br>2. f16 → f32 single-precision vectors<br>3. topic centroid vector retrieval<br>4. `.meh` format `0x0004`, incompatible with v1 data|
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
