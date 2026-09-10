# MemHop Host Integration Guide (Go API)

> How to embed MemHop **directly as a Go module** (no MCP server) from your host
> process. Applies to **v1.6.3**. Module path `github.com/qyiun666/MemHop` — you
> only ever import the `api` package.

---

## 1. Integration shape

```
host process
 ├─ go.mod: require github.com/qyiun666/MemHop (or go.work replace → local checkout)
 ├─ import only github.com/qyiun666/MemHop/api (never internal/)
 ├─ one .meh file = many agent domains (isolated except the file-wide L3/L5 pools), addressed by Session(hexID)
 └─ external services:
      └─ ONE OpenAI-compatible LLM (turn distillation / Dream consolidation / Crystallize)
      └─ no embedding / vector service
```

### Hard contracts

| Contract | Meaning |
|---|---|
| **Single instance** | One `.meh` file is locked exclusively; a second `OpenMulti` on the same file fails. Every call runs through a `Session` bound to one agent domain. |
| **Serial calls** | Same-agent operations (Search / Update / Dream / write APIs) are serialized by the library's per-agent domain lock; different agents run in parallel on a `*MultiAgentDB`. The host needs no external queue. `Lock()`/`Unlock()` remain for host-critical sections around raw file access — they serialize **the default domain only** and panic on a closed DB (`Unlock` on a closed DB is a no-op). |
| **LLM on the write path** | `Update`, `Dream` and `Crystallize` call the LLM and fail when it is down (no silent degradation) — `Update` exactly once per turn. `Search` never calls it: a read cannot be blocked by the LLM. |
| **ID shape** | All external IDs are 16-char lowercase hex strings (xxhash64). Treat them as opaque: the library issues every id and a host only echoes it back — there is nothing to convert. `api.DefaultAgentID` names the implicit agent domain, and the turn topic id `Search` returns is what addresses that turn's L6 trajectory and the plan tree it opened. |
| **Timestamps** | Unix milliseconds everywhere; `<= 0` is `ErrInvalidQuery`. |

---

## 2. Prerequisites

| Dependency | Requirement | Example |
|---|---|---|
| Go 1.27+ | build requirement | — |
| LLM | OpenAI-compatible API | DeepSeek / OpenAI / any compatible endpoint |

That is the whole list. MemHop contacts no embedding / vector service and
needs no database, cache or server of its own: one writable file path plus one
LLM endpoint.

---

## 3. Add the dependency

```bash
go get github.com/qyiun666/MemHop@latest
```

```go
import "github.com/qyiun666/MemHop/api"
```

Everything you need — config, query/result types, layer models, error codes — is
re-exported from `api` as type aliases (see §9). No other import required.

---

## 4. Build the config (`MemHopConfig`)

`api.MemHopConfig` is the single assembly point. **Bold = required** (enforced by
`Validate()`).

### Top-level fields

| Field | Type | Meaning |
|---|---|---|
| **DBPath** | string | `.meh` path. Created on first open. |
| **LLM** | LlmConfig | see below. |
| Defaults | MemHopDefaults | Engine tuning; recommended `*api.DefaultMemHopDefaults` with selective overrides. |

### `LlmConfig` (build it by literal)

| Field | Required | Meaning |
|---|---|---|
| APIURL | ✅ | OpenAI-compatible endpoint URL. |
| APIKey | ✅ | API key (inject from env vars, never hardcode). |
| Model | ✅ | Model name. |
| TimeoutSecs | — | LLM call timeout. |
| MaxOutputTokens | — | Max output tokens. |

### `MemHopDefaults` — common overrides

`MemHopDefaults` exposes exactly three business knobs. Everything else
(consolidation prompt limits, decay lambdas, L1 edge thresholds) is
package-private in `internal/tuning.go` and no longer configurable — hosts
should not need to tune them; if you think you do, open an issue.

| Field | Default | Meaning |
|---|---|---|
| SceneDreamTopicThreshold | 24 | Once a scene's depth-1 topic count passes this, `Update` schedules that scene's Dream in the background. **0 disables the trigger** (relevant when building a partial literal). |
| DreamCompressMinTopics | 20 | Topics per scene before Dream will compress. |
| AgentIdleTTLMs | 3600000 | An agent domain whose context has been idle this long is freed from memory (it rebuilds from its records on next use). 0 disables the sweep. |

---

## 5. Open / Close

```go
cfg := &api.MemHopConfig{
    DBPath: "/data/agent.meh",
    LLM: api.LlmConfig{
        APIURL:          os.Getenv("LLM_URL"),
        APIKey:          os.Getenv("LLM_KEY"),
        Model:           os.Getenv("LLM_MODEL"),
        TimeoutSecs:     60,
        MaxOutputTokens: 8192,
    },
    Defaults: *api.DefaultMemHopDefaults,
}

dbm, err := api.OpenMulti(cfg)
if err != nil { /* ErrConfig / ErrInvalidMagic / ErrCorruption */ }
defer dbm.Close() // checkpoint snapshot + release mmap/file lock

// Multi-agent is the only mode: bind a session to a stable hex agent id.
agentID, err := dbm.CreateAgent("guide")
if err != nil { /* ... */ }
db, err := dbm.Session(agentID)
```

- `api.OpenMulti(cfg)` is the only entry point. The engine stores no capability records: capability cards live in the host's own directory (e.g. `plug/<package>/capability.json` next to the `.meh` file) — the host scans that directory and assembles its tool surface itself; nothing is injected at Open.
- Explicit flush: `db.Checkpoint()`.
- Space reclamation: `db.CompactTo(newPath)` writes a defragmented copy of the whole file (live records only, its own rebuilt index) and never touches the open one — `newPath` must not exist yet. Deletes are tombstones, so a domain that dropped scenes or graphs only gives bytes back here; the swap (Close → rename → Open) stays yours, which is why this call is Go-side and not an MCP tool.

---

## 6. Core memory loop (every turn)

The host drives per turn: **turn start `Search` (read this session's memory and open the turn) → turn end `Update` (settle the whole turn into the topic that read opened)**. Consolidation is scheduled by the engine once a scene's topic count passes the threshold; hosts may also call `Dream` explicitly. **One L2 scene = one host session, one turn = one topic** — the host decides which scene to read and never mints an id itself; the engine never guesses.

### 6.1 Turn start: `Search(q)`

```go
res, err := db.Search(api.SearchQuery{
    SceneID: sceneIDHex,  // empty = ask the library for a fresh scene (first turn of a session)
    L3ID:    graphIDHex,  // optional: anchors a *newly created* scene to an L3 project domain
})
```

No `ctx` parameter — the read path holds no cancellable LLM or network work — and **no retrieval cost**: no LLM, no embedding, no scoring; the hit scene's topics come straight from the L2Meta cache. A new scene starts out named `session:<id>` by the library; `UpdateScene(sceneID, ScenePatch{Name: &name})` is the host's one way to title it, and the title survives every later read (Search rewrites that same record to advance its turn counter, never the name). The only write is that counter: it is what mints `NewTopicID`.

**`SearchResult` fields:**

| Field | Content | Host use |
|---|---|---|
| `Profile` | L0 profile snapshot | can go into the system prompt |
| `ProfileBrief` | bounded compact profile digest | light per-turn injection; fetch full `Profile` only when needed |
| `Scene` | the scene just read (`SceneID` / `SceneName` / `L3ID` / `TopicCount`) | keep `Scene.SceneID` — Update and later reads use it |
| `Topics` | the scene's depth-1 topics in user-timestamp order, each with its `FusedKeywords` | **the memory injected into this turn's prompt**; originals are addressed by a turn's own topic id — `SearchL4(L4Query{TopicID})` |
| `NewTopicID` | the topic this read opened for the turn about to run | hand it to `AppendArchive` and to `Update` — one turn, one id |

An unknown `SceneID` returns `ErrNotFound` (the library will not create a scene you asked to read); an empty one creates a scene and returns its id.

### 6.2 During the turn: `AppendArchive(topicID, ArchiveSlot)`

The host records the turn itself, one call per record, under the topic id `Search`
issued. `AppendArchive` is the only way content enters a topic.

```go
err := db.AppendArchive(topicIDHex, api.ArchiveSlot{
    Kind:      api.KindUtterance, // or api.KindEvent
    Seq:       1,                 // 0 lets the library allocate the slot
    Role:      api.RoleUser,      // utterance only: RoleUser / RoleAgent / RoleSystem
    ContentType: api.ContentText, // utterance only; a non-text slot carries its media path/URL in Content
    Content:   userRawText,       // required
    CreatedAt: userTS,            // Unix milliseconds, > 0
})
// An event names itself instead of taking a speaker, and may hang on a plan step:
err = db.AppendArchive(topicIDHex, api.ArchiveSlot{
    Kind:      api.KindEvent,
    EventType: "tool_call",       // event only; free-form, no whitelist
    NodePath:  "1.1",             // optional: creates that step as pending
    Content:   `{"tool":"grep"}`,
    CreatedAt: time.Now().UnixMilli(),
})
```

`IDHash` and `ContextID` are ignored on the way in: the topic comes from the
argument and the record id follows from `(topic, Seq)`, which is what lets a host
read a record back, change one field and write it to the slot it came from. `Seq: 0`
allocates above every held slot, skipping 1 and 2, which are the dialogue's; naming a
held `Seq` **overwrites** it rather than erroring — across kinds too. That is the whole
replay story: a retried turn rewrites its slots instead of accumulating versions. What
a replay does not do is reclaim a slot it stopped filling, so a withdrawn line stays
until `DeleteTopic` or the retention window.

The refusals, all of them before anything is written (including before a `NodePath`
creates a step): an undefined `Kind`, empty `Content`, `CreatedAt <= 0`, an undefined
`ContentType`, an event with no `EventType`, an utterance carrying an `EventType` or a
`NodePath`, the consolidation role `3` (the library marks its own summaries with it),
and content over budget — 4 KiB per event, 64 KiB per utterance. Over budget is
**refused, never truncated**: a shortened record reads back exactly like a complete one.

### 6.3 Turn end: `Update(sceneID, topicID)`

```go
err := db.Update(sceneIDHex, topicIDHex)
```

One distillation over the utterances that topic holds produces its keywords, and
`Update` writes no content of its own: exactly one LLM call per turn, and it runs
before the topic is written, so a failed call or an empty extraction leaves no
half-recorded topic — while the records the host appended earlier stay where it put
them. Unknown scene → `ErrNotFound`; a `topicID` that is not a turn of that scene, or
a topic with no content left to distill → `ErrInvalidQuery`, the latter without
reaching the LLM at all. Settling the same topic twice re-derives its track from what
the topic holds now, so retrying a timed-out `Update` is safe.

What may be settled is bounded: the `TopicID` has to be **a turn this scene opened** (the id `Search` minted for one of the turns the scene has counted so far). A Dream-fused topic, another scene's turn, or an id you built yourself is `ErrInvalidQuery` — before the LLM call, so a refusal leaves nothing behind. Replaying the current turn and settling an earlier turn that is still open both stay valid, which is what keeps an at-least-once write loop safe without letting a stale retry rewrite a turn that already settled.

### 6.4 Consolidation: `Dream(ctx, sceneID)`

```go
rep, err := db.Dream(ctx, "")      // empty sceneID sweeps every scene of the domain
// or db.Dream(ctx, sceneIDHex)    // one scene only
```

Usually **the host does not need to call it**: once a scene's depth-1 topic count passes `Defaults.SceneDreamTopicThreshold` (default 24), `Update` schedules that scene's Dream in the background (one in flight per scene).

Runs L2→L1→L0 compression / decay / profile distillation (several LLM calls, slow) — keep it in a goroutine or between turns.
Returns a structured `*DreamReport`: `ConsolidatedScenes / L2TopicsCompressed / L1NodesAdded|Removed / L1EdgesAdded|Removed / L0Updated` plus `Stages []DreamStage{Name, Status, DurationMs}` (status `ok | skipped | cancelled | error`). An empty report is not an error; a mid-pipeline failure returns the partial report with the error. After compression each scene keeps at most 20 depth-1 topics (`Consolidate` rule), which is the size bound on what a host reads back.

---

## 7. One turn, one topic

A turn is one topic: the id `Search` minted for it, and everything the host records
about it lives under that id. Dialogue originals and operation events are the same
kind of record differing only by `Kind`, so a turn can hold as many of either as the
host recorded, and one `Update` distills them into one keyword track.

What happens *while* the turn runs (tool calls, intermediate output, subagent results)
is not conversation, so it goes in as `Kind: api.KindEvent` beside the utterances —
under the same key, and out of the transcript reads: `SceneContext` and
`SearchL4(L4Query{TopicID, Kind: &KindUtterance})` show what was said,
`SearchL4{..., Kind: &KindEvent}` shows what happened, and `Crystallize` reads only
the events.

The write side owns the axes: content type (`text`/`image`/`video`/`document`/`audio`/
`code`/`other`) and speaker are declared per record by `AppendArchive` and reported
back verbatim (`ArchiveSlot.ContentType`, `L4Query.Type`, `SceneContext`'s
`Messages[].Type`); an undefined value is refused rather than stored. Dream's fused
summary is the one record whose type and role the library fixes — `text`, role 3.

---

## 8. Layer API quick reference

The 25 session methods split by audience:

- **Runtime/task face (18)** — the host drives these every turn and LLM tools bind to them: `Search` / `AppendArchive` / `Update` / `Dream` (the host-driven loop), `GetL0` / `UpdateL0`, `ListScenes` / `SceneContext`, `GetL3` / `ListL3` / `ImportL3` / `QueryL3Nodes` / `QueryL3Subgraph`, `SearchL4`, `ListTrajectorySessions` / `Crystallize`, `PlanCommit` / `PlanState`.
- **Assembly/admin face (7, plus all of `MultiAgentDB`)** — host code at session boundaries and management channels only, never an LLM tool: `UpdateScene` / `MergeScenes` / `DeleteScene` / `DeleteTopic`, `UpdateL3` / `DeleteL3` / `DeleteL3Nodes`. The capability format left the method surface entirely: `ParseCapabilityPackage` / `ValidateCapabilityCard` are package-level functions (§8 L5).

### L0 profile

```go
slot, err := db.GetL0()                       // *api.ProfileSlot
err = db.UpdateL0(&api.ProfileSlot{Name: "..."})
```

`UpdateL0` writes the host-owned half — `Name`, `Role`, `Personality`,
`Preferences` — and nothing else: `EmotionState` and `MBTI` are kept from the
stored profile because only Dream evolves them, and `UpdatedAtMs` is stamped by
the library. There is therefore no need to `GetL0` and fill values back; passing
those three fields changes nothing. The distilled half refreshes automatically
with Dream: there is no standalone distill entry point.

### L2 scenes

| Method | Meaning |
|---|---|
| `db.ListScenes(l3ID) ([]SceneSlot, error)` | scene list (`SceneID / SceneName / TopicCount`); a non-empty `l3ID` keeps only the scenes anchored to that project domain, `""` lists all |
| `db.SceneContext(sceneID) (*SceneContext, error)` | the scene's whole transcript (topics + their L4 originals) and **no write at all** — no turn is opened; **use for session resume**. Unlike `Search` it flattens to depth 2, because a Dream-fused group keeps its originals on the children it sank, and this is the only read that brings them back: entries carry `Depth` and `ChildCount` so a fused parent (whose message is Dream's summary) can be told from the turns it grouped. `TopicCount` counts the entries returned, not the scene's depth-1 roots |
| `db.UpdateScene(sceneID, api.ScenePatch{Name, L3ID, Force}) (SceneSlot, error)` | title it (`Name`), anchor it to an L3 project domain (`L3ID`), or clear the anchor (`L3ID: &""`); nil fields keep their stored value, and the **written scene comes back** |
| `db.MergeScenes(primaryID, []secondaryIDs) error` | merge scenes |
| `db.DeleteTopic(topicID) error` | delete a topic subtree + its L4 archives + indexes; prunes parent `ChildrenIDs` (memory correction) |
| `db.DeleteScene(sceneID) error` | delete a scene + all topics/archives + L1 node; `ErrNotFound` if missing (memory correction) |

Renaming and anchoring are one call: `UpdateScene` reads the scene once,
applies the fields you named, writes it back and hands you the stored scene — so
confirming an anchor never costs a `ListScenes` sweep. Blank title is
`ErrInvalidQuery`, unknown scene is `ErrNotFound`, and an anchor must name a
project domain that exists.
Anchoring is write-once — pointing a scene that already has a **different**
domain at a new one is rejected (`ErrInvalidQuery`) unless `Force` is set;
clearing an anchor never needs `Force`, because the scene simply becomes
unanchored again.

### L3 knowledge graphs (stable facts: people / projects / preferences)

The graph pool is **file-wide**: every agent domain of the file shares one L3
pool. Knowledge one agent imports is visible to — and anchorable by — every
other agent, and deleting an agent never deletes the pool. Scenes, archives
and profiles stay domain-local.

```go
res, err := db.ImportL3([]api.L3ImportItem{{
    Title:    "Alice's project",      // node title, required
    Domain:   "project",
    NodeType: "project",              // person / project / preference ...
    Content:  "Alice is building MemHop",
    Keywords: []string{"Alice", "MemHop"},
    SourceRef: "docs/alice.md:1",     // positional reference (optional)
    Related:  []api.L3Relation{{Titles: []string{"Alice", "Projects"}, Kind: api.EdgePartOf}},
    // one relation = one hyperedge over the item plus every title (1 title = a binary relation)
}}, api.L3ImportMerge)                 // Skip / Merge / Overwrite
// returns GraphIDs / CreatedIDs / UpdatedIDs / SkippedCount / EdgesCreated / Errors
```

`Related` targets resolve by title within the same graph and may appear later
in the batch (two-phase import). A hyperedge is identified by its member nodes
**plus its kind**, so one node pair can hold `related` and `part_of` at the same
time, and re-importing a batch does not duplicate edges (deduped on sorted
members + kind). Unresolvable / self / invalid-kind entries land in `Errors`.

`GraphIDs` is what closes the loop: a graph id is `hash(Domain)` and no other
public call renders that derivation, so `ImportL3` reports it directly —
`SearchQuery.L3ID` / `UpdateScene` need that id.

`GetL3 / ListL3 / QueryL3Nodes / QueryL3Subgraph / UpdateL3 / DeleteL3 /`
`DeleteL3Nodes`.

`QueryL3Nodes` filters AND together (`IDs` / `Keyword` / `NodeType`), so naming
only `GraphID` lists that graph's nodes and `Keyword` is case-insensitive — like
the L4 keyword filter. `DeleteL3` removes the graph with every node and edge;
`DeleteL3Nodes(graphID, nodeIDs)` removes specific nodes and cascades the
hyperedges touching them (Go-only, like the other memory-correction calls), so
correcting one wrong fact no longer means rebuilding the graph and losing the
edges bound to it. An id that names no node of that graph is refused and nothing
is deleted.

Every L3 id the library issued names exactly one record type: `GetL3(nodeID)`,
`UpdateL3(nodeID, …)` or `UpdateScene(sceneID, ScenePatch{L3ID: &nodeID})`
answer `ErrNotFound` rather than reading — or writing — across kinds.

L2↔L3 is one relation, held by the scene: `SceneSlot.L3ID` anchors a session to
a project domain (many scenes may share one graph). It is set when the scene is
created (`SearchQuery.L3ID`) or later via `UpdateScene`, and
`ListScenes(l3ID)` reads the domain back. Topics carry no graph references.

### L4 archive search (historical originals)

```go
arcs, err := db.SearchL4(api.L4Query{
    Keyword: "keyword",          // case-insensitive substring of Content
    // Start: t0, End: t1,       // created within [t0, t1] (ms)
    // IDs: []string{...},       // by archive id (one id = one record)
    // TopicID: &topicHex,       // only this topic's archives
    // Type: &api.ContentImage,  // only this content type
    // Limit: 50,                // keep the newest N matches (<=0: every match)
})
```

`ArchiveSlot` carries `Kind` (utterance / event), `Seq`, `ContentType`
(text/image/video/document/audio/code/other), `Role` (`RoleUser` / `RoleAgent` /
`RoleSystem`; the library's own consolidation role is not a public constant),
`ContextID`, `CreatedAt` and `Content` — for media types `Content` is a path or URI,
not the binary.
Every query field is optional and the ones you set **AND** together — there are no
modes to choose between — and the result is sorted by `Seq`. So the reads a host
wants after a turn are one call each: `SearchL4(L4Query{TopicID: &topicID, Kind:
&utterance})` gives what was said, the same with `Kind: &event` gives what happened,
and without `Kind` both; `L4Query{IDs: []string{id}}` gets a single record back by
id (a missing id yields an empty slice, a malformed one `ErrInvalidQuery`). An empty
query returns the domain's whole content set — bound it with a time range or `Limit`
on a large domain.

### L5 capabilities (directory-as-capability — the host owns the files)

The engine **stores no capability records**. The single source of truth is the host's own capability directory (e.g. `plug/<package>/capability.json` next to the `.meh` file): the host scans it, projects cards into its tool surface, and restart-picks-up changes; activating a draft is promoting its file. The library keeps the format itself, exported as package-level functions:

| Function | Meaning |
|---|---|
| `api.ParseCapabilityPackage(data, source)` | parse a `memhop-capability/v4` document (one file = one package, 1..N cards) into `[]CapabilityImport`, validating the whole package |
| `api.ValidateCapabilityCard(card)` | check one card against the same contract (name, summary, resources, action chains) |

> One card = a name + any number of function entries (`resources`, no card-level type); each entry self-describes its launch (`type: mcp|skill|api|composite` + `ref`/`config`), purpose (`desc`) and usage (`input`/`output`), mirroring the host tool spec field-for-field — hosts project them with a pure field copy. A composite entry carries its action chain in `config` as `{"steps":[{"tool":"...","args":{...}}]}` (every step needs a non-empty `tool`). For the LLM-facing block, the capability package's `PromptCard` renders one card: name, version, summary, trigger, then per-resource launch/description/input/output/steps — no `id:`/`package:`/`usage:` lines, since the host directory, not a stored record, is the card's identity.

### Turn events + crystallization

```go
// A turn's events are L4 content of kind event, under the NewTopicID Search
// returned for this turn — the host never derives a turn key itself.
err := db.AppendArchive(turnIDHex, api.ArchiveSlot{
    Kind:      api.KindEvent,
    EventType: "tool_call",   // names the step:
                              // llm_request / llm_output / tool_call / tool_result /
                              // subagent_spawn / subagent_done / context_inject /
                              // ask_user / user_reply (free-form; no whitelist)
    Content:   "tool name + arg summary", // 4 KiB budget, over-budget is refused
    CreatedAt: time.Now().UnixMilli(),
})
// Seq and the owning topic are engine-assigned: the key you append under IS the
// turn's topic id, and the plan nodes that turn opened live under the same key.
// `NodePath` is what binds an event to a step — see the plan surface below;
// leave it empty for a plain turn event.

// L6 → capability candidates: distill one turn's trajectory against the
// host's current catalog (capped at 128KB payload, oldest events dropped).
res, err := db.Crystallize(ctx, turnIDHex, existingCards)
// existingCards []api.CapabilityImport — the host's current catalog, read
// from its own directory (empty on first run).
// res.Capabilities — []CrystallizeCapability: {Action: "create|reuse|merge",
// ReuseID (the existing card's NAME, not a hex id)} + the card
// payload. The engine writes nothing: validate, dedupe against your
// directory and persist drafts (e.g. plug/draft/) yourself — a file
// promotion activates.

// Enumerate turns (e.g. to pick crystallize candidates).
sessions, err := db.ListTrajectorySessions()
// sessions[i] = TrajectorySessionSummary{SessionID hex (the turn's topic id), Events, LastAppendAt}
```

A turn's event track reads back with `SearchL4(L4Query{TopicID: &topicID, Kind:
&event})` in Seq order. The track is addressed **by turn key only**: nothing returns an
event handle on write, because no public call takes one, and Dream drops content
older than the retention window. Of an event you hand in, `EventType`, `NodePath`,
`Content` and `CreatedAt` are used as given; the library assigns `Seq` and the owning
topic and forces `ContentType` to `text` with no speaker — a thing that happened has
neither. Content over 4 KiB is refused: a shortened event would read back exactly
like a complete one.

`ListTrajectorySessions` enumerates the turns that hold events, so a host can find
crystallization candidates without remembering which ids it logged.

### L6 plan tree (Go host surface)

A plan tree belongs to the turn that opened it: **the L6 key is that turn's
topic id** — the one `Search` hands back — and it addresses the turn's nodes, while
the same key addresses the turn's content in L4. The host assigns each node a **dotted `NodePath`** (`"1"`,
`"1.2.1"`) and holds nothing else: there is no plan id to mint, and
`PlanState(topicID)` is how a tree comes back.

| Call | Meaning |
|---|---|
| `db.AppendArchive(topicID, ev)` with a non-empty `ev.NodePath` | record a step event against that node, creating the node chain as pending if missing — this is also how a step is added. `NodePath` **shapes the tree itself**: every missing ancestor segment is created as pending, so a typo in a path opens a second tree, and L6 exposes no node-delete call (a stale tree is reclaimed by the retention window once the turn that opened it falls out of it). `EventType` is **the host's own name for the step**, on this path exactly as on a bare turn event — the engine never branches on it (it comes back through `SearchL4` and into the Crystallize prompt verbatim) and only refuses an empty one. Convention names for readers: `plan_step`, `llm_request`, `llm_output`, `tool_call`, `tool_result`, `subagent_spawn`, `subagent_done`, `context_inject`, `ask_user`, `user_reply` |
| `db.PlanCommit(topicID, nodePath, ev, api.PlanStep{Title: "research", Type: "step", Status: api.PlanStatusDone, Summary: s})` | commit one step: advance the node's status, append its event, and roll `done` children's summaries up into their parent (a parent turns `done` only when the host commits it). A `nodePath` missing along the dotted path is created as pending — this is how a step is added. A blank `Title`/`Type`/`Summary` keeps what the node already holds; an unknown `Status` is refused before the tree moves |
| `db.PlanState(topicID)` | read the forest view (`PlanTree.Roots` + `DoneCount` / `TotalCount`) — also the restart recovery path |

`0000000000000000` is reserved (it is the value a record leaves its key unset
with) and every L6 entry rejects it — reads included.

---

## 9. Exported types (v1.6.3)

| Kind | Names | Use |
|---|---|---|
| config | `MemHopConfig` / **`LlmConfig`** / `MemHopDefaults` + `DefaultMemHopDefaults` | the whole assembly surface |
| input aliases | `SearchQuery` / `ScenePatch` / `L3ImportItem` / `L3Relation` / `L3ImportMode` / `L3ImportResult` / `L3NodeQuery` / `L4Query` / `SceneContext` / `SceneMessage` / `TrajectorySessionSummary` / `DreamReport` / `DreamStage` / `CapabilityImport` / `CapabilityPackageDoc` / `CrystallizeOutput` / `CrystallizeCapability` / `ResourceRef` | inputs & id-free results (all string IDs are hex) |
| response DTOs | `ProfileSlot` / `SceneSlot` / `TopicSlot` / `SearchResult` / `HypergraphSlot` / `HypergraphNode` / `HypergraphEdge` / `HypergraphSource` / `L3Graph` / `L3Subgraph` / `ArchiveSlot` (write and read) | every ID field is a 16-hex string |
| id surface | **`DefaultAgentID`** (the implicit domain) | the library issues every id — turn topics included; a host echoes them back and converts nothing |
| enums | `GraphEdgeKind` / `CapabilityType` / `ContentType` / `PlanStatus` | enum aliases |

Enum constants are exported too: `L3ImportSkip/Merge/Overwrite`,
`CapabilityMCP/Skill/API/Composite`, `EdgeRelated...EdgeCustom`,
`ContentText/Image/Video/Document/Audio/Code/Other`. The capability format
survived the record layer's retirement as package-level surface:
`CapabilityFormatV4` + `ParseCapabilityPackage` / `ValidateCapabilityCard`.

> A record's kind is `api.KindUtterance` / `api.KindEvent`. L4 `role` is a bare
> `uint8`, and the three a host may declare on an appended utterance are
> `api.RoleUser` / `RoleAgent` / `RoleSystem`. The fourth value, 3, is the library's
> own mark on a consolidated summary: it is deliberately not exported and
> `AppendArchive` refuses it, so a host cannot write a record that reads as
> consolidated.
> A plan node's status is only ever a string — the `api.PlanStatus*` constants — since
> the engine assigns a node's state separately from the events bound to it.

---

## 10. Errors

All MemHop errors carry a numeric code: `api.CodeOf(err)` returns it (0 for
non-MemHop errors). Check with the exported constants:

```go
if api.CodeOf(err) == api.ErrNotFound { ... }
```

Codes: `ErrConfig`, `ErrInvalidQuery`, `ErrNotFound`,
`ErrIO`, `ErrClosed`, `ErrInvalidMagic`, `ErrCRCMismatch`, `ErrCorruption`,
`ErrSerialization`, `ErrDeserialization`, `ErrLLM`, `ErrAgentNotFound` (agentID not registered or deleted).
Numbers are never reused: `1002` and `9001` are retired and will not be reissued.

---

## 11. Minimal runnable skeleton

```go
package main

import (
    "context"
    "log"
    "os"
    "time"

    "github.com/qyiun666/MemHop/api"
)

func main() {
    dbm, err := api.OpenMulti(&api.MemHopConfig{
        DBPath: os.Getenv("MEH_PATH"), // /data/agent.meh
        LLM: api.LlmConfig{
            APIURL: os.Getenv("LLM_URL"),
            APIKey: os.Getenv("LLM_KEY"),
            Model:  os.Getenv("LLM_MODEL"),
        },
        Defaults: *api.DefaultMemHopDefaults,
    })
    if err != nil { log.Fatal(err) }
    defer dbm.Close()

    // Multi-agent is the only mode: bind every call to one agent domain.
    agentID, err := dbm.CreateAgent("guide-agent")
    if err != nil { log.Fatal(err) }
    db, err := dbm.Session(agentID)
    if err != nil { log.Fatal(err) }

    // One host session = one scene. The first read asks for a scene with an
    // empty SceneID and keeps the returned id.
    opened, err := db.Search(api.SearchQuery{})
    if err != nil { log.Fatal(err) }
    sceneID := opened.Scene.SceneID

    // Per turn: start — read this session's memory (zero LLM), splice the
    // topics in, and keep NewTopicID: it is where this turn will settle.
    res, err := db.Search(api.SearchQuery{SceneID: sceneID})
    if err != nil { log.Fatal(err) }
    _ = res // Profile/ProfileBrief + Topics (FusedKeywords per topic)

    // Per turn: record what was said and what happened, under that topic id.
    topicID := res.NewTopicID
    userTS := time.Now().UnixMilli()
    _ = db.AppendArchive(topicID, api.ArchiveSlot{Kind: api.KindUtterance, Seq: 1,
        Role: api.RoleUser, Content: "user raw message", CreatedAt: userTS})
    _ = db.AppendArchive(topicID, api.ArchiveSlot{Kind: api.KindEvent,
        EventType: "tool_call", Content: "grep ...", CreatedAt: userTS + 1})
    _ = db.AppendArchive(topicID, api.ArchiveSlot{Kind: api.KindUtterance, Seq: 2,
        Role: api.RoleAgent, Content: "agent reply", CreatedAt: time.Now().UnixMilli()})

    // Per turn: end — distill that topic's utterances into its keywords.
    if err := db.Update(sceneID, topicID); err != nil { log.Fatal(err) }

    // Idle / scheduled (usually unnecessary: Update schedules consolidation
    // once a scene's topic count passes the threshold).
    if _, err := db.Dream(context.Background(), ""); err != nil {
        log.Fatal(err)
    }
}
```

---


## 12. Pitfalls

1. **The LLM only affects `Update` and `Dream`**: `Search` and `AppendArchive` make
   zero LLM calls, so recording and reading can never be blocked by it. `Update`
   distils once per turn and, on failure, returns an error having written no topic —
   the records you appended earlier stay. Hosts should retry a failed settle.
2. **No embedding service, no dimension to declare**: the two header bytes at
   offset 6 are reserved. The format version is `0x000E`: the L3 knowledge
   graph lives in the reserved shared domain (`core.SharedPoolAgentID`); no
   migration runs — `0x000D` and older files are rejected at Open, because they
   store a turn's events in a record type that no longer exists and its archives
   under text-derived ids, neither of which the current rules can address.
3. **Timestamps in Unix ms**, `<= 0` → `ErrInvalidQuery` on every record you append;
   a turn's topic is stamped with the earliest and latest of its content.
4. **IDs are opaque 16-hex strings**: never splice/truncate them; response ids
   feed back as-is; the facade exposes no hex ⇄ integer bridge.
5. **`Search` writes no memory content**: it opens one turn (bumping the
   scene's hit and turn counters) and creates no topic record, so an abandoned
   turn leaves nothing behind. To read originals use `SceneContext` /
   `SearchL4`.
   Replaying an append with the same `(TopicID, Seq)` is idempotent: the record
   hashes from that pair, so a retry rewrites it instead of duplicating — and a
   slot the replay stops filling is not reclaimed.
6. **One file, many agent domains**: all tenants live inside one
   `.meh` file (`OpenMulti` → `CreateAgent(name)` → `Session(hexID)`), fully
   isolated per domain except the file-wide L3 pool; legacy files
   (`FormatVersion < 0x000E`) cannot be opened or migrated.
7. **Content and plans auto-expire**: Dream drops a topic's content older than 7
   days and plan nodes older than 7 days (a tree still in flight is exempt);
   `DeleteTopic` / `DeleteScene` are the explicit corrections. Past the window a
   topic keeps its keyword track and its `Messages` come back empty or with gaps in
   `Seq` — a legal end state, not a failed read. Everything is keyed by the turn's
   topic id, so append before `Update` closes the turn (the id is already in hand
   from `Search`) and never invent one.
8. **The library owns the turn id**: `Update` accepts only an existing scene
   (`Search` → `Scene.SceneID`) and a topic id that read issued — a turn cannot
   be settled without first being opened. The library never creates a scene
   behind a settle, and Dream never merges scenes — merging is the explicit
   `MergeScenes`, which deletes the merged-away records and thereby invalidates
   any scene id the host still holds.
   Each `Search` opens exactly one turn: a host that reads a scene twice and
   settles once simply skips a turn number — gaps cost nothing, and no read
   ever reissues an id already given out.
9. **`SceneDreamTopicThreshold` defaults to 24**: a partial `MemHopDefaults`
    literal leaves it 0, which **disables** automatic consolidation — assign
    `*api.DefaultMemHopDefaults` first, then override. Context size stays
    bounded only because Dream compresses each scene to ≤20 topics, so
    switching it off lets the injected context grow without limit.
