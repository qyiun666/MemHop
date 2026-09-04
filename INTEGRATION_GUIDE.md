# MemHop Host Integration Guide (Go API)

> How to embed MemHop **directly as a Go module** (no MCP server) from your host
> process. Applies to **v1.6.0**. Module path `github.com/qyiun666/MemHop` — you
> only ever import the `api` package.

---

## 1. Integration shape

```
host process
 ├─ go.mod: require github.com/qyiun666/MemHop (or go.work replace → local checkout)
 ├─ import only github.com/qyiun666/MemHop/api (never internal/)
 ├─ one .meh file = many isolated agent domains, addressed by Session(hexID)
 └─ external services:
      └─ ONE OpenAI-compatible LLM (turn distillation / Dream consolidation / Crystallize)
      └─ no embedding / vector service (retired in v1.5.0)
```

### Hard contracts

| Contract | Meaning |
|---|---|
| **Single instance** | One `.meh` file is locked exclusively; a second `OpenMulti` on the same file fails. Every call runs through a `Session` bound to one agent domain. |
| **Serial calls** | Same-agent operations (Search / Update / Dream / write APIs) are serialized by the library's per-agent domain lock; different agents run in parallel on a `*MultiAgentDB`. The host needs no external queue. `Lock()`/`Unlock()` remain for host-critical sections around raw file access — they serialize **the default domain only** and panic on a closed DB (`Unlock` on a closed DB is a no-op). |
| **LLM on the write path** | `Update`, `Dream` and `Crystallize` call the LLM and fail when it is down (no silent degradation) — `Update` exactly once per turn. `Search` never calls it: a read cannot be blocked by the LLM. |
| **ID shape** | All external IDs are 16-char lowercase hex strings (xxhash64). Treat them as opaque: the library issues every id and a host only echoes it back — there is nothing to convert. `api.DefaultAgentID` names the implicit agent domain and `api.NewPlanID(name)` mints a plan id from a name you choose. |
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

### `LlmConfig` (exported since v1.2.7 — build it by literal)

| Field | Required | Meaning |
|---|---|---|
| APIURL | ✅ | OpenAI-compatible endpoint URL. |
| APIKey | ✅ | API key (inject from env vars, never hardcode). |
| Model | ✅ | Model name. |
| TimeoutSecs | — | LLM call timeout. |
| MaxOutputTokens | — | Max output tokens. |

### `MemHopDefaults` — common overrides (also exported since v1.2.7)

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

- `api.OpenMulti(cfg)` is the only entry point.
- `OpenMulti` mounts the built-in read-only capability cards (nothing written to `.meh`).
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

No `ctx` parameter — the read path holds no cancellable LLM or network work — and **no retrieval cost**: no LLM, no embedding, no scoring; the hit scene's topics come straight from the L2Meta cache. A new scene starts out named `session:<id>` by the library; `UpdateScene(sceneID, ScenePatch{Name: &name})` is the host's one way to title it, and the title survives every later read (Search rewrites that same record to bump its counters, never the name). The only write is the scene record: its hit counters (feeding Dream's importance feedback) and its turn counter, which is what mints `NewTopicID`.

**`SearchResult` fields:**

| Field | Content | Host use |
|---|---|---|
| `Profile` | L0 profile snapshot | can go into the system prompt |
| `ProfileBrief` | bounded compact profile digest | light per-turn injection; fetch full `Profile` only when needed |
| `Scene` | the scene just read (`SceneID` / `SceneName` / `L3ID` / `TopicCount`) | keep `Scene.SceneID` — Update and later reads use it |
| `Topics` | the scene's depth-1 topics in user-timestamp order, each with `FusedKeywords` and `L4Refs` | **the memory injected into this turn's prompt**; follow `L4Refs` via `SearchL4(L4Query{IDs: ...})` for originals |
| `NewTopicID` | the topic this read opened for the turn about to run | hand it to `Update` and to `AppendTrajectory` — one turn, one id |

An unknown `SceneID` returns `ErrNotFound` (the library will not create a scene you asked to read); an empty one creates a scene and returns its id.

### 6.2 Turn end: `Update(TurnUpdate)`

```go
topicID, err := db.Update(api.TurnUpdate{
    SceneID:   sceneIDHex,    // must already exist (created by Search)
    TopicID:   topicIDHex,    // required: Search's NewTopicID — the topic this turn lives in
    UserText:  userRawText,   // required; a non-text user slot puts its media path/URL here
    UserTS:    userTS,        // Unix milliseconds, > 0
    UserType:  api.ContentText, // optional: image/video/document/audio/code/other
    AgentText: agentReply,    // required
    AgentTS:   agentTS,       // not earlier than UserTS
    AgentType: api.ContentText, // optional, same set as UserType
})
```

Both content types default to `ContentText` (their zero value), so a host that never touches them records exactly what it recorded before. This is the engine's only L4 write path, so it is also the only place a turn's content type gets declared — read it back through `ArchiveSlot.ContentType`, the `L4Query.Type` filter, or `SceneContext`'s `Messages[].Type`.

One call turns the exchange into its topic: two L4 archives (`RoleUser` + `RoleAgent`) plus keywords from a single distillation, and it returns the same 16-hex id it was given. Settling a topic id twice **rewrites** that turn instead of duplicating it (the archives hash from the topic id and their text), so retrying a timed-out `Update` is safe.

Key property: **distillation runs before any write.** A failed LLM call or an empty extraction errors out and leaves nothing behind — never a half-recorded turn. Unknown scene → `ErrNotFound`; malformed texts/timestamps → `ErrInvalidQuery`.

What may be settled is bounded: the `TopicID` has to be **a turn this scene opened** (the id `Search` minted for one of the turns the scene has counted so far). A Dream-fused topic, another scene's turn, or an id you built yourself is `ErrInvalidQuery` — before the LLM call, so a refusal leaves nothing behind. Replaying the current turn and settling an earlier turn that is still open both stay valid, which is what keeps an at-least-once write loop safe without letting a stale retry rewrite a turn that already settled.

### 6.3 Consolidation: `Dream(ctx, sceneID)`

```go
rep, err := db.Dream(ctx, "")      // empty sceneID sweeps every scene of the domain
// or db.Dream(ctx, sceneIDHex)    // one scene only
```

Usually **the host does not need to call it**: once a scene's depth-1 topic count passes `Defaults.SceneDreamTopicThreshold` (default 24), `Update` schedules that scene's Dream in the background (one in flight per scene).

Runs L2→L1→L0 compression / decay / profile distillation (several LLM calls, slow) — keep it in a goroutine or between turns.
Returns a structured `*DreamReport`: `ConsolidatedScenes / L2TopicsCompressed / L1NodesAdded|Removed / L1EdgesAdded|Removed / L0Updated` plus `Stages []DreamStage{Name, Status, DurationMs}` (status `ok | skipped | cancelled | error`). An empty report is not an error; a mid-pipeline failure returns the partial report with the error. After compression each scene keeps at most 20 depth-1 topics (`Consolidate` rule), which is the size bound on what a host reads back.

---

## 7. One turn, one topic

A turn is one user message plus one agent reply, and it is exactly one topic. `Update` stores both originals as L4 archives, so a turn's raw text stays recoverable through its `L4Refs` — nothing else is written there.

What happens *between* those two messages (tool calls, intermediate output, subagent results) is execution detail rather than conversation, and it belongs to the turn's L6 trajectory: append it under the same topic id with `AppendTrajectory(topicID, …)` (see §8 L6). That is where the retired N:N path now goes.

**Retired in v1.5.0:** `AppendL4Message` (append extra messages to a settled topic) and `RefineTopicKeywords` (re-distill that topic from all of them). They made the number of distillations per turn a host decision; one turn now costs exactly one LLM call, and its keyword track never goes stale relative to its own originals. L4 content types (`text`/`image`/`video`/`document`/`audio`/`code`/`other`) are declared on the write side by `Update`'s `user_type`/`agent_type` and reported back verbatim on the read side (`L4Query.Type` filter, `ArchiveSlot.ContentType`, `SceneContext`'s `Messages[].Type`); an undefined value is rejected with `ErrInvalidQuery` rather than stored. Dream's fused summary is the one archive whose type is fixed — `text`.

---

## 8. Layer API quick reference

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
| `db.SceneContext(sceneID) (*SceneContext, error)` | the scene's whole transcript (topics + their L4 originals) and **no write at all** — no turn opened, hit counters untouched; **use for session resume**. Unlike `Search` it flattens to depth 2, because a Dream-fused group keeps its originals on the children it sank, and this is the only read that brings them back: entries carry `Depth` and `ChildCount` so a fused parent (whose message is Dream's summary) can be told from the turns it grouped. `TopicCount` counts the entries returned, not the scene's depth-1 roots |
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
time, and re-importing a batch does not duplicate edges (edges stored before the
kind joined the id are recognised by the same semantic key). Unresolvable / self
/ invalid-kind entries land in `Errors`.

`GraphIDs` is what closes the loop: a graph id is `hash(Domain)` and no other
public call renders that derivation, so without it a host could only find its
imported graph again by listing the domain and matching names — and
`SearchQuery.L3ID` / `UpdateScene` need the id.

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

`ArchiveSlot` fields: `ContentType` (text/image/video/document/audio/code),
`Role` (`RoleUser`/`RoleAgent` written by `Update`, `RoleDream` by Dream),
`ContextID`, `CreatedAt`, `Content` — for media types `Content` is a path or
URI, not the binary.
Every field is optional and the ones you set **AND** together — there are no
modes to choose between — and the result is sorted by `CreatedAt`. So the read
a host wants after a turn is one call: `SearchL4(L4Query{TopicID: &topicID})`
returns exactly that turn's originals; `L4Query{IDs: []string{id}}` replaces
the old single-record getter (a missing id yields an empty slice, a malformed
one `ErrInvalidQuery`); an empty query returns the domain's whole archive set —
read a time range or add `Limit` before doing that on a large domain, since the
result is every original the file holds.

### L5 capabilities (register tools/skills to the LLM)

| Method | Meaning |
|---|---|
| `db.ListCapabilities(CapabilityListQuery{IDs, Status, Type, Keyword})` | list capability cards; conditions AND, so `IDs: []string{id}` reads one card |
| `db.ImportCapability(path)` | import a memhop-capability/v3 JSON file (or a directory holding `capability.json`); the Go surface takes any path the host can name, and imported cards land **active** (a crystallized card lands draft) |
| `db.DeleteCapability(id)` | delete |
| `db.UpdateCapability(id, CapabilityPatch{...})` | partial update (built-ins rejected) |
| `db.ActivateCapability(id)` | draft → active |
| `db.RecordCapabilityUsage(id, success)` | usage feedback |

> The built-in toolbox (6 English cards: `memhop-guide` + 5 LLM-callable manuals) is mounted at Open and served by `ListCapabilities` (read-only, never persisted to `.meh`); manual cards use `type: "api"` with `ref: "api:MethodName"` — call them directly on the api facade. Inject only the one-line index (`id + name + summary + trigger`) plus the guide, and fetch parameter details on demand via `ListCapabilities(CapabilityListQuery{IDs: []string{id}})`. Resources are tool declarations (`name/desc/input/output` mirror the host tool spec; `input` is a JSON Schema string), so hosts project them with a pure field copy.

### L6 trajectory + crystallization (v1.2.7 additions)

```go
// One trajectory per agent turn: the key is the NewTopicID Search returned
// for this turn — the host no longer derives turn keys itself.
err := db.AppendTrajectory(turnIDHex, "", api.TrajectorySlot{
    EventType: "tool_call",   // classifies each step of the turn:
                              // llm_request / llm_output / tool_call / tool_result /
                              // subagent_spawn / subagent_done / context_inject /
                              // ask_user / user_reply (free-form; no whitelist)
    Payload:   "tool name + arg summary", // 4KB budget, over-budget is refused
    Timestamp: time.Now().UnixMilli(),
})
// Seq, SessionID and the event's TopicID are engine-assigned: the key you
// append under IS the turn's topic id, so don't set them.
// The second argument is the plan node path — see the plan surface below;
// pass "" for a plain turn event.

// L6 → L5: distill one turn's trajectory into capability drafts (capped at
// 128KB payload, oldest events dropped). Pass the plan id instead of a topic
// id to crystallize everything a plan tree bound together.
res, err := db.Crystallize(ctx, turnIDHex)
// res.CreatedIDs / ReusedIDs / MergedIDs / Errors
// res.Details — per-candidate disposition: []CrystallizeDetail{
//   {Name, Action: "create|reuse|merge|skip", CapabilityID, Reason}}
// Activate drafts with ActivateCapability afterwards.

// Enumerate turns (e.g. to pick crystallize candidates).
sessions, err := db.ListTrajectorySessions()
// sessions[i] = TrajectorySessionSummary{SessionID hex (the turn's topic id), Steps, LastAppendAt}
```

`ReadTrajectory(key)` reads events in Seq order. The log is append-only and
addressed **by key only**: nothing returns or takes an event id, because no
public call consumes one — the read is the whole turn (or the whole plan), and
Dream drops events older than the retention window (L6 is a process index;
durable products live in L4/L5). Of an event you hand in, `EventType`,
`Payload`, `Timestamp` and `FinishedAt` are used as given; `Seq`, the session
id, the plan-node fields (`NodeType`/`PlanID`/`ParentID`/`NodePath`/`Status`/
`Summary`/`PlanType`) are assigned or cleared by the library, and a `Payload`
over 4 KiB is refused — a shortened event would read back exactly like a complete one.

### L6 plan tree (Go host surface)

One L6 key space carries both turns and the host's task tree. The library
issues the plan id, so the host names the plan and gets a stable token back:

```go
planID := api.NewPlanID("cat-42")     // deterministic 16-hex; naming it again
                                      // after a restart recovers the same tree
```

| Call | Meaning |
|---|---|
| `db.SyncPlanTree(planID, root *PlanNode)` | push the authoritative whole tree: adds/updates nodes by `NodePath`, deletes vanished nodes with their bound events, emits no `plan_step`. A blank `Title`/`Type`/`Status`/`Summary` inherits the stored value, so a partial snapshot never rewinds a finished step |
| `db.AppendTrajectory(planID, nodePath, ev)` | record a step event against that node (creating the node chain as pending if missing). `nodePath` is **dotted** (`"1"`, `"1.2.1"`) and must sit under its parent's path; `EventType` must come from the plan vocabulary: `plan_step`, `llm_request`, `llm_output`, `tool_call`, `tool_result`, `subagent_spawn`, `subagent_done`, `context_inject`, `ask_user`, `user_reply` (a bare turn event takes any `EventType` the host names) |
| `db.PlanCommit(planID, nodePath, ev, api.PlanStatusDone, summary)` | advance a node's status and append its step event; `done` children's summaries roll up into their parent |
| `db.PlanState(planID)` | read the forest view (`PlanTree.Roots` + `DoneCount` / `TotalCount`) — this is also how a host recovers its tree after a restart |
| `db.PlanReplace(planID, rootTitle)` | wipe the tree and (optionally) seed a fresh titled root, keeping the id |

`0000000000000000` is reserved (it is the bare-event sentinel) and every plan
entry rejects it.

---

## 9. Exported types (v1.6.0)

| Kind | Names | Use |
|---|---|---|
| config | `MemHopConfig` / **`LlmConfig`** / `MemHopDefaults` + `DefaultMemHopDefaults` | the whole assembly surface |
| input aliases | `SearchQuery` / `TurnUpdate` / `ScenePatch` / `L3ImportItem` / `L3Relation` / `L3ImportMode` / `L3ImportResult` / `L3NodeQuery` / `L4Query` / `CapabilityListQuery` / `CapabilityPatch` / `SceneContext` / `SceneMessage` / `TrajectorySessionSummary` / `CrystallizeResult` / `CrystallizeDetail` / `DreamReport` / `DreamStage` / `ResourceRef` / `Workflow` | inputs & id-free results (all string IDs are hex) |
| response DTOs | `ProfileSlot` / `SceneSlot` / `TopicSlot` / `SearchResult` / `HypergraphSlot` / `HypergraphNode` / `HypergraphEdge` / `HypergraphSource` / `L3Graph` / `L3Subgraph` / `ArchiveSlot` / `Capability` / `TrajectorySlot` | every ID field is a 16-hex string (v1.4.1) |
| id surface | **`DefaultAgentID`** (the implicit domain) / **`NewPlanID(name)`** (mint a plan id) | the library issues ids; a host echoes them back and converts nothing |
| enums | `GraphEdgeKind` / `CapabilityType` / `CapabilityStatus` / `CapabilityOrigin` / `ContentType` / `PlanStatus` | enum aliases |

Enum constants are exported too: `L3ImportSkip/Merge/Overwrite`,
`CapabilityMCP/Skill/API/Composite`, `CapabilityDraft/Active/Deprecated`,
`CapabilityOrigin*`, `EdgeRelated...EdgeCustom`,
`ContentText/Image/Video/Document/Audio/Code/Other`.

> L4 `role` is a bare `uint8`; the exported constants are
> `api.RoleUser` / `RoleAgent` / `RoleSystem` / `RoleDream` (values 0-3).
> Plan trees use `api.NodeTypeEvent` / `NodeTypePlan`, the numeric read-side
> `api.PlanStatus*` string values (a plan node's status is only ever a string; the L6 event record carries no status field).

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
Numbers are never reused: `1002` (vector-dimension mismatch) and `9001`
(encoder) were retired with the retrieval subsystem.

---

## 11. Minimal runnable skeleton (v1.6.0 signatures)

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

    // Per turn: end — settle the whole exchange into that topic id.
    userTS := time.Now().UnixMilli()
    topicID, err := db.Update(api.TurnUpdate{
        SceneID:   sceneID,
        TopicID:   res.NewTopicID,
        UserText:  "user raw message",
        UserTS:    userTS,
        AgentText: "agent reply",
        AgentTS:   time.Now().UnixMilli(),
    })
    if err != nil { log.Fatal(err) }
    // Everything the turn did in between belongs to that same topic:
    _ = db.AppendTrajectory(topicID, "", api.TrajectorySlot{
        EventType: "tool_call", Payload: "grep ...", Timestamp: userTS + 1,
    })

    // Idle / scheduled (usually unnecessary: Update schedules consolidation
    // once a scene's topic count passes the threshold).
    if _, err := db.Dream(context.Background(), ""); err != nil {
        log.Fatal(err)
    }
}
```

---


## 12. Pitfalls

1. **The LLM only affects the write path**: `Search` makes zero LLM calls, so
   reads can never be blocked by it. `Update` distils once per turn and, on
   failure, returns an error having written nothing — no half-recorded turn.
   Hosts should retry a failed settle.
2. **No embedding service, no dimension to declare**: the two header bytes at
   offset 6 held the vector dimension until v1.5.0 and are now reserved — files
   written by v1.4.x open unchanged. The format version is still `0x0009`: the
   single keyword track is folded in at decode time, so no migration runs and
   none is needed. Headers older than `0x0009` remain rejected.
3. **Timestamps in Unix ms**, `<= 0` → `ErrInvalidQuery`; the agent timestamp
   must not precede the user timestamp.
4. **IDs are opaque 16-hex strings**: never splice/truncate them; response ids
   feed back as-is; the facade exposes no hex ⇄ integer bridge.
5. **`Search` writes no memory content**: it opens one turn (bumping the
   scene's hit and turn counters) and creates no topic record, so an abandoned
   turn leaves nothing behind. To read originals use `SceneContext` /
   `SearchL4`.
   Replaying an `Update` with the same `TopicID` is idempotent: the topic is
   that id and its archives hash from it, so a retried settle overwrites
   instead of duplicating.
6. **One file, many agent domains**: since v1.4 all tenants live inside one
   `.meh` file (`OpenMulti` → `CreateAgent(name)` → `Session(hexID)`), fully
   isolated per domain; legacy files (`FormatVersion < 0x0009`) cannot be
   opened or migrated.
7. **Built-in capability cards are read-only**: `UpdateCapability` rejects them.
8. **Trajectories auto-expire**: Dream drops events older than 7 days;
   the external surface is append + query only (`AppendTrajectory` /
   `ReadTrajectory` / `ListTrajectorySessions`) — no delete API. A turn's
   trajectory is keyed by its topic id, so append before `Update` settles the
   turn (the id is already in hand from `Search`) and never invent one.
9. **The library owns the turn id**: `Update` accepts only an existing scene
   (`Search` → `Scene.SceneID`) and a topic id that read issued — a turn cannot
   be settled without first being opened. The library never creates a scene
   behind a settle, and Dream never merges scenes — merging is the explicit
   `MergeScenes`, which deletes the merged-away records and thereby invalidates
   any scene id the host still holds.
   Each `Search` opens exactly one turn: a host that reads a scene twice and
   settles once simply skips a turn number — gaps cost nothing, and no read
   ever reissues an id already given out.
10. **`SceneDreamTopicThreshold` defaults to 24**: a partial `MemHopDefaults`
    literal leaves it 0, which **disables** automatic consolidation — assign
    `*api.DefaultMemHopDefaults` first, then override. Context size stays
    bounded only because Dream compresses each scene to ≤20 topics, so
    switching it off lets the injected context grow without limit.
