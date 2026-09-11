# MemHop Host Integration Guide (Go API)

> How to embed MemHop **directly as a Go module** (no MCP server) from your host
> process. Applies to **v1.6.4**. Module path `github.com/qyiun666/MemHop` — you
> only ever import the `api` package.

> This guide describes the surface as it is now: `api.Open` → `api.DB`, domains held
> as handles from `Primary` / `SubAgent` (no agent id crosses the boundary), no
> capability surface, and no trajectory-session enumeration — a host reads a turn's
> events with `SearchL4{TopicID, Kind: event}`. The method lists below are the ones
> `api/surface_public_test.go` pins; `go doc
> github.com/qyiun666/MemHop/api.Session` stays the authoritative per-method text,
> because `internal` is not published and that command is the only documentation of a
> promoted method.

---

## 1. Integration shape

```
host process
 ├─ go.mod: require github.com/qyiun666/MemHop (or go.work replace → local checkout)
 ├─ import only github.com/qyiun666/MemHop/api (never internal/)
 ├─ one .meh file = many agent domains (isolated except the file-wide L3 pool), each reached
 │   through a handle: DB.Primary() for the domain the file was opened on,
 │   DB.SubAgent(llm, profile) for one created under it — no agent id ever crosses this line
 └─ external services:
      └─ ONE OpenAI-compatible LLM (turn distillation / Dream consolidation)
      └─ no embedding / vector service
```

### Hard contracts

| Contract | Meaning |
|---|---|
| **Single instance** | One `.meh` file is locked exclusively; a second `api.Open` on the same file fails. Every call runs through a `Session` bound to one agent domain. |
| **Serial calls** | Same-agent operations (Search / Update / Dream / write APIs) are serialized by the library's per-agent domain lock — the LLM call runs inside that lock, so one slow response holds its own domain and no other. Different agents run in parallel. The host needs no external queue, and there is no raw-file access path left for it to guard. |
| **LLM on the write path** | `Update` and `Dream` call the LLM and fail when it is down (no silent degradation) — `Update` exactly once per turn, and a failed distillation writes nothing. `Search` never calls it: a read cannot be blocked by the LLM. |
| **ID shape** | All external IDs are 16-char lowercase hex strings (xxhash64). Treat them as opaque: the library issues every id and a host only echoes it back — there is nothing to convert. An agent domain is never named by an id at this boundary: you hold the `*Session` the library gave you. The turn topic id `Search` returns is what addresses that turn's L4 content and the plan tree it opened. |
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

## 4. Build the arguments (`LlmConfig`, `MemHopDefaults`)

`Open` takes the endpoint, the tuning knobs and the primary profile as separate
arguments, so there is no config object to assemble. **Bold = required** (the
endpoint is checked before the path is touched).

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
lib, err := api.Open(
    "/data/agent.meh",     // path
    api.LlmConfig{         // endpoint: required, validated before the path is touched
        APIURL:          os.Getenv("LLM_URL"),
        APIKey:          os.Getenv("LLM_KEY"),
        Model:           os.Getenv("LLM_MODEL"),
        TimeoutSecs:     60,
        MaxOutputTokens: 8192,
    },
    api.DefaultMemHopDefaults, // tuning knobs; copy it to change one open
    &api.ProfileInput{Name: "guide", Role: "assistant"}, // required for a new file
)
if err != nil { /* ErrConfig / ErrInvalidQuery / ErrInvalidMagic / ErrCorruption */ }
defer lib.Close() // checkpoint snapshot + release mmap/file lock

// Domains come back as handles, never as ids. `db` below is always one of them:
// a *api.Session, and every business call in this guide runs on it.
db, err := lib.Primary()                        // the domain the file was opened on
worker, err := lib.SubAgent(workerLLM, api.ProfileInput{Name: "worker"}) // by name
```

`api.Open` is the only entry point, and what it does depends on the file and on the
primary domain's profile:

| file | profile for the primary | argument | result |
|---|---|---|---|
| there | there | anything | **succeeds, the argument is ignored** — the file's own profile is the source of truth |
| there | none | nothing | `ErrConfig` |
| there | none | given | validated, then written → succeeds |
| absent | — | nothing | `ErrConfig`, **and no file is left behind** |
| absent | — | given | validated, then the file is created and seeded → succeeds |

- The primary is the implicit zero domain, so a file holds exactly one and nothing has
  to be scanned to find it. `Primary()` returns its handle; a host never sees an agent
  id at all.
- `SubAgent(llm, profile)` creates the domain named `profile.Name` the first time and
  returns the same one every time after — the name is the domain's address, frozen at
  creation. `llm` is that domain's own endpoint, so a sub-agent can run on a different
  model. The profile is written only if the domain has none yet, which also finishes
  off a domain left half-created by a crash. `AgentType` is stamped, not taken: a
  domain created this way is a sub-agent.
- Both refusals happen before anything touches the filesystem, so a refused `Open`
  leaves no file behind for the next attempt to trip over.
- Explicit flush: `lib.Checkpoint()`.
- Space reclamation: `lib.CompactTo(newPath)` writes a defragmented copy of the whole
  file (live records only, its own rebuilt index) and never touches the open one —
  `newPath` must not exist yet. Deletes are tombstones, so a domain that dropped
  scenes or graphs only gives bytes back here; the swap (Close → rename → Open) stays
  yours, which is why this call is Go-side and not an MCP tool.

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
| `Scene` | the scene just read (`SceneID` / `SceneName` / `L3ID`) | keep `Scene.SceneID` — Update and later reads use it |
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
    NodeSeq:   2,                 // optional: the ordinal PlanCreate/PlanNodeAdd handed out for this turn
                                  // (0 = the event is bound to no step)
    Content:   `{"tool":"grep"}`,
    CreatedAt: time.Now().UnixMilli(),
})
```

`IDHash` and `TopicID` are ignored on the way in: the topic comes from the
argument and the record id follows from `(topic, Seq)`, which is what lets a host
read a record back, change one field and write it to the slot it came from. `Seq: 0`
allocates above every held slot, skipping 1 and 2, which are the dialogue's; naming a
held `Seq` **overwrites** it rather than erroring — across kinds too. That is the whole
replay story: a retried turn rewrites its slots instead of accumulating versions. What
a replay does not do is reclaim a slot it stopped filling, so a withdrawn line stays
until `DeleteTopic` or the retention window.

The refusals, all of them before anything is written (an append never creates a plan
step — only `PlanCreate` and `PlanNodeAdd` do): an undefined `Kind`, empty `Content`,
`CreatedAt <= 0`, an undefined `ContentType`, an event with no `EventType`, an utterance
carrying an `EventType` or a `NodeSeq`, an event whose `NodeSeq` names a step this turn
never created (`ErrInvalidQuery`, and nothing lands), the consolidation role `3` (the
library marks its own summaries with it), and content over budget — 4 KiB per event, 64
KiB per utterance. Over budget is **refused, never truncated**: a shortened record reads
back exactly like a complete one.

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
Returns a structured `*DreamReport`: `ConsolidatedScenes / L2TopicsCompressed / L1NodesAdded|Removed / L1EdgesAdded|Removed / L0Updated` plus `Stages []DreamStage{Name, Status, DurationMs}` (status `ok | skipped | cancelled | error`). An empty report is not an error; a mid-pipeline failure returns the partial report with the error. What a host reads back is bounded by convergence, not by a cap: passing the threshold schedules that scene's Dream, and Dream only merges the groups the model judges one — topics it never picked stay at depth 1.

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
`SearchL4{..., Kind: &KindEvent}` shows what happened. What a host turns those
events into is its own business: the engine stores no capability cards and offers no
crystallization call.

The write side owns the axes: content type (`text`/`image`/`video`/`document`/`audio`/
`code`/`other`) and speaker are declared per record by `AppendArchive` and reported
back verbatim (`ArchiveSlot.ContentType`, `L4Query.Type`, `SceneContext`'s
`Messages[].Type`); an undefined value is refused rather than stored. Dream's fused
summary is the one record whose type and role the library fixes — `text`, role 3.

---

## 8. Layer API quick reference

The 26 session methods split by audience:

- **Runtime/task face (19)** — the host drives these every turn and LLM tools bind to them: `Search` / `AppendArchive` / `Update` / `Dream` (the host-driven loop), `GetL0` / `UpdateL0`, `ListL1`, `ListScenes` / `SceneContext`, `GetL3` / `ListL3` / `ImportL3` / `QueryL3Nodes` / `QueryL3Subgraph`, `SearchL4`, `PlanCreate` / `PlanNodeAdd` / `PlanNodeUpdate` / `PlanState`.
- **Assembly/admin face (7)** — host code at session boundaries and management channels only, never an LLM tool: `UpdateScene` / `RenameTopic` / `MergeScenes` / `DeleteScene` / `DeleteTopic`, `UpdateL3` / `DeleteL3`.

The file-level lifecycle sits on `api.DB` instead (6): `Primary` / `SubAgent`, then `Checkpoint` / `CompactTo` / `Close` / `IsClosed`. There is no capability surface anywhere: the engine neither stores nor parses cards, so a host reads the events of a turn with `SearchL4{Kind: event}` and organizes them itself.

### L0 profile

```go
prof, err := db.GetL0()                       // *api.ProfileSlot — the full record
err = db.UpdateL0(&api.ProfileInput{Name: "..."})
```

`UpdateL0` takes a `ProfileInput`, which holds exactly the four fields a host
owns — `Name`, `Role`, `Personality`, `Preferences`. The rest of the stored
profile is not in that shape because it is not the host's to state: `EmotionState`
and `MBTI` are evolved by Dream, `UpdatedAtMs` is stamped by the library, and
`AgentType` is decided when the domain is created. A write inherits all four from
the record, so there is no need to `GetL0` and fill values back, and no read-only
field can be smuggled in — the compiler refuses. The distilled half refreshes
automatically with Dream: there is no standalone distill entry point.

### L2 scenes

| Method | Meaning |
|---|---|
| `db.ListScenes(l3ID) ([]SceneSlot, error)` | scene list (`SceneID / SceneName / L3ID`); a non-empty `l3ID` keeps only the scenes anchored to that project domain, `""` lists all |
| `db.SceneContext(sceneID) (*SceneContext, error)` | the scene's whole transcript (topics + their L4 originals) and **no write at all** — no turn is opened; **use for session resume**. Unlike `Search` it flattens to depth 2, because a Dream-fused group keeps its originals on the children it sank, and this is the only read that brings them back: entries carry `Depth` and `ChildCount` so a fused parent (whose message is Dream's summary) can be told from the turns it grouped. The entries returned are the whole count — roots and the sunk children this read alone brings back |
| `db.UpdateScene(sceneID, api.ScenePatch{Name, L3ID, Force}) (SceneSlot, error)` | title it (`Name`), anchor it to an L3 project domain (`L3ID`), or clear the anchor (`L3ID: &""`); nil fields keep their stored value, and the **written scene comes back** |
| `db.MergeScenes(primaryID, []secondaryIDs) error` | merge scenes |
| `db.DeleteTopic(topicID) error` | delete a topic subtree + its L4 archives + indexes; the subtree is the topics whose `parent_id` points into it (memory correction) |
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

`GetL3` / `ListL3` / `QueryL3Nodes` / `QueryL3Subgraph` / `UpdateL3` / `DeleteL3`.
Deletion has one granularity: the whole graph.

`QueryL3Nodes` filters AND together (`IDs` / `Keyword` / `NodeType`), so naming
only `GraphID` lists that graph's nodes and `Keyword` is case-insensitive — like
the L4 keyword filter. Every L3 read comes back sorted by id (its nodes, its
edges, the graph slots of `ListL3`) and `Limit` keeps the first N of that order,
so the same query gives the same list in the same order every time. `DeleteL3`
removes the graph with every node and edge in it, then clears the scene anchors
that named it in every domain of the file.

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
    // NodeSeq: 2,               // only records attributed to this step or any step under it
    //                             // (needs TopicID: a step is addressed inside a turn)
    // Type: &api.ContentImage,  // only this content type
    // Limit: 50,                // keep the newest N matches (<=0: every match)
})
```

`ArchiveSlot` carries `Kind` (utterance / event), `Seq`, `ContentType`
(text/image/video/document/audio/code/other), `Role` (`RoleUser` / `RoleAgent` /
`RoleSystem`; the library's own consolidation role is not a public constant),
`TopicID`, `CreatedAt` and `Content` — for media types `Content` is a path or URI,
not the binary.
Every query field is optional and the ones you set **AND** together — there are no
modes to choose between — and the result is sorted by `Seq`. So the reads a host
wants after a turn are one call each: `SearchL4(L4Query{TopicID: &topicID, Kind:
&utterance})` gives what was said, the same with `Kind: &event` gives what happened,
and without `Kind` both; adding `NodeSeq: 2` cuts that event track down to the records
attributed to one plan step and every step under it, so a step's work reads back without
pulling the whole turn; `L4Query{IDs: []string{id}}` gets a single record back by
id (a missing id yields an empty slice, a malformed one `ErrInvalidQuery`). An empty
query returns the domain's whole content set — bound it with a time range or `Limit`
on a large domain.

### Turn events (what a turn did, beside what it said)

```go
// A turn's events are L4 content of kind event, under the NewTopicID Search
// returned for this turn — the host never derives a turn key itself.
err := db.AppendArchive(turnIDHex, api.ArchiveSlot{
    Kind:      api.KindEvent,
    EventType: "tool_call",   // any non-empty name the host chooses; no whitelist
    Content:   "tool name + arg summary", // 4 KiB budget, over-budget is refused
    CreatedAt: time.Now().UnixMilli(),
})
// Seq and the owning topic are engine-assigned: the key you append under IS the
// turn's topic id, and the plan nodes that turn opened live under the same key.
// `NodeSeq` is what binds an event to a step — see the plan surface below;
// leave it 0 for a plain turn event.
```

The track reads back with `SearchL4(L4Query{TopicID: &topicID, Kind: &event})`, in Seq
order, and it is addressed **by turn key only**: nothing returns an event handle on
write because no public call takes one, and Dream drops content older than the
retention window. Of an event you hand in, `EventType`, `NodeSeq`, `Content` and
`CreatedAt` are used as given; the library assigns `Seq` and the owning topic and
forces `ContentType` to `text` with no speaker — a thing that happened has neither.
Payload over 4 KiB is refused: a shortened event would read back exactly like a
complete one.

What those events become is the host's decision and the engine takes no part in it.
There is no capability surface: the library stores no cards, parses no card format and
offers no crystallization call, so a host that distills its own action log does that
with its own prompt, its own dedupe and its own file layout. Nor is there a call that
enumerates the turns holding events — `Search` issued those keys, and the host that
appended under them still has them.

### L5 plan tree (Go host surface)

A plan belongs to the turn that opened it: **the L5 key is that turn's
topic id** — the one `Search` hands back — and it addresses the turn's nodes, while
the same key addresses the turn's content in L4. A step is addressed by a **per-turn
ordinal** (`Seq`, a `uint32` the library hands out from 1 and the host only ever echoes
back: the create calls return it), and `ParentSeq` says which step it hangs under
(`0` = a root). There is no plan id and no path string to mint, and
`PlanState(topicID)` is how a tree comes back.

| Call | Meaning |
|---|---|
| `seq, err := db.PlanCreate(topicID, title)` | open this turn's tree by creating its first step, and take back the ordinal that step is addressed by from here on. A turn's tree starts with no steps, so this is also how a plan first appears under a turn |
| `seq, err := db.PlanNodeAdd(topicID, parentSeq, title)` | add one step to the tree and get its ordinal. `parentSeq` `0` hangs it at the top level, so this is also how a second root joins the forest; any other value must name a step this tree already holds — `PlanNodeAdd` under an unknown parent is `ErrNotFound` and grows nothing. A step is created `in_progress`, so no status is asked for here; a title may be left empty and filled in later, and the view falls back to the ordinal until the host names the step |
| `err := db.PlanNodeUpdate(topicID, api.PlanStep{Seq: seq, Status: api.PlanStatusDone, Summary: s})` | restate one step: its `Status` plus the node's own `Title`/`Summary`. `Status` is stated every time (there is no "leave it as it was" spelling) while a blank `Title`/`Summary` keeps what the node holds, so updating a step never rewinds its title or erases a folded summary. A step reaching a terminal status records `FinishedAt`; restating a settled step as `in_progress` re-opens it and drops that timestamp. Once every direct child of a `Done` parent is itself terminal, the parent's summary folds up from its children's. A status outside `in_progress` / `done` / `failed`, or an ordinal this turn never created (`ErrNotFound`), is refused **before the node is touched** and leaves the tree exactly as it was. This call writes no content |
| `tree, err := db.PlanState(topicID)` | read the forest view (`PlanTree.Roots` + `DoneCount` / `TotalCount`, which are summed over every step of every tree; every `PlanNodeView` carries `Seq` / `ParentSeq` / `Status` / `Summary` / `Children`) — also the restart recovery path |
| `db.AppendArchive(topicID, ev)` with a non-zero `ev.NodeSeq` | record a step event against one step of this turn's tree. The step has to exist already: an ordinal nobody created refuses the whole record (`ErrInvalidQuery`) and stores nothing, because an event naming a step the plan never holds is the plan and the record disagreeing — the tree is `PlanCreate` / `PlanNodeAdd`'s to build, and a mistyped ordinal cannot quietly open a second one. `EventType` is **the host's own name for the step**, on this path exactly as on a bare turn event — the engine never branches on it (the name comes back verbatim through `SearchL4`) and only refuses an empty one. Convention names for readers: `plan_step`, `llm_request`, `llm_output`, `tool_call`, `tool_result`, `subagent_spawn`, `subagent_done`, `context_inject`, `ask_user`, `user_reply` |

Status has three values and one string encoding each: `api.PlanStatusInProgress`
(`in_progress`), `api.PlanStatusDone` (`done`), `api.PlanStatusFailed` (`failed`). The
engine keeps no "planned but not started" state — a step exists because the host created
it, and it exists in progress.

The plan write surface is Go-only: `api.Session`'s 19 task-face methods include it, but
the MCP tool face exposes no plan call, because a tree has to be built by a caller that
holds the turn it belongs to.

`0000000000000000` is reserved (it is the value a record leaves its key unset
with) and every L5 entry rejects it — reads included.

---

## 9. Exported types (v1.6.4)

| Kind | Names | Use |
|---|---|---|
| entry & handles | **`Open`** → `*DB`, then `DB.Primary()` / `DB.SubAgent(llm, profile)` → `*Session` | the only ways in; an agent domain is held as a handle, never named by an id |
| config | **`LlmConfig`** / `MemHopDefaults` + `DefaultMemHopDefaults` | the endpoint and tuning arguments `Open` takes |
| input shapes | **`ProfileInput`** / `SearchQuery` / `ScenePatch` / `L3ImportItem` / `L3Relation` / `L3ImportMode` / `L3NodeQuery` / `L4Query` / `PlanStep` / `ArchiveSlot` (also a result) | inputs; `ProfileInput` is the only profile a host may write, and of its four fields only `Name` is required |
| response DTOs | `ProfileSlot` / `SceneNodeView` / `SceneSlot` / `TopicSlot` / `SceneContext` / `SceneContextTopic` / `SceneMessage` / `SearchResult` / `HypergraphSlot` / `HypergraphNode` / `HypergraphEdge` / `L3Graph` / `L3Subgraph` / `L3ImportResult` / `PlanTree` / `PlanNodeView` / `DreamReport` / `DreamStage` | every id field is a 16-char hex string, and every one of them was issued by the library |
| enums | `GraphEdgeKind` / `ContentType` / `ArchiveKind` / `PlanStatus` / `AgentTypePrimary` + `AgentTypeSub` | the vocabulary a call is written in |
| errors | `Code` + the `Err*` constants, read with `CodeOf(err)` | the numeric code behind an error string |

Enum constants are exported too: `L3ImportSkip` / `Merge` / `Overwrite`,
`EdgeRelated`…`EdgeCustom`, `ContentText`…`ContentOther`, `KindUtterance` /
`KindEvent`, `RoleUser` / `RoleAgent` / `RoleSystem`,
`PlanStatusInProgress` / `PlanStatusDone` / `PlanStatusFailed`.

Nothing in this list converts an id: there is no `FormatID` / `ParseID` pair and no
numeric id in any signature, because a host echoes back the hex strings it was given
and builds none itself. There is no capability type either — the card format, its
file layout and its activation are the host's own assets.


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
    lib, err := api.Open(
        os.Getenv("MEH_PATH"), // /data/agent.meh
        api.LlmConfig{
            APIURL: os.Getenv("LLM_URL"),
            APIKey: os.Getenv("LLM_KEY"),
            Model:  os.Getenv("LLM_MODEL"),
        },
        api.DefaultMemHopDefaults,
        // Only consulted when the file is not there yet.
        &api.ProfileInput{Name: "guide-agent", Role: "assistant"},
    )
    if err != nil { log.Fatal(err) }
    defer lib.Close()

    // The file's primary domain. Sub-agent domains would come from
    // lib.SubAgent(llm, api.ProfileInput{Name: ...}) the same way.
    db, err := lib.Primary()
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

    // The plan comes first: a step exists because it was created here, one call
    // per step, and an event can only be attributed to a step the tree already
    // holds. The create calls hand back the ordinal that step is addressed by.
    fix, err := db.PlanCreate(topicID, "locate the regression")
    if err != nil { log.Fatal(err) }
    leaf, err := db.PlanNodeAdd(topicID, fix, "fix")
    if err != nil { log.Fatal(err) }
    _ = db.AppendArchive(topicID, api.ArchiveSlot{Kind: api.KindEvent,
        EventType: "tool_call", NodeSeq: leaf,
        Content: "grep ...", CreatedAt: userTS + 1})
    _ = db.AppendArchive(topicID, api.ArchiveSlot{Kind: api.KindUtterance, Seq: 2,
        Role: api.RoleAgent, Content: "agent reply", CreatedAt: time.Now().UnixMilli()})
    _ = db.PlanNodeUpdate(topicID, api.PlanStep{Seq: leaf, Status: api.PlanStatusDone,
        Summary: "…"})

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
   offset 6 are reserved. The format version is `0x0012`: the L3 knowledge
   graph lives in the reserved shared domain (`core.SharedPoolAgentID`), a
   topic carries the `name` its host gave it, and a domain's identity
   (`agent_type`) is stored on its own profile. No migration runs — `0x0011`
   and older files are rejected at Open, and the reason is not a field this
   reader would rather not decode: a pre-`0x0012` profile carries no
   `agent_type` at all, so **every** domain in such a file arrives reading as
   the primary agent while the current rules hold that one file has exactly
   one. Loosen that and the file opens with its domain identities
   simultaneously wrong everywhere. Older files also carry their own smaller
   divergences — a plan node addressed by a dotted path string rather than an
   ordinal, statuses under a numbering where `0` meant pending and `2` meant
   done, events attributed to steps no declaration ever made, an archive's
   owning topic under a `context_id` key, ids derived from `l1:` / `l4:`
   namespaces — and none of it can be addressed under the current rules.
3. **Timestamps in Unix ms**, `<= 0` → `ErrInvalidQuery` on every record you append;
   a turn's topic is stamped with the earliest and latest of its content.
4. **IDs are opaque 16-hex strings**: never splice/truncate them; response ids
   feed back as-is; the facade exposes no hex ⇄ integer bridge.
5. **`Search` writes no memory content**: it opens one turn (advancing the
   scene's turn counter) and creates no topic record, so an abandoned
   turn leaves nothing behind. To read originals use `SceneContext` /
   `SearchL4`.
   Replaying an append with the same `(TopicID, Seq)` is idempotent: the record
   hashes from that pair, so a retry rewrites it instead of duplicating — and a
   slot the replay stops filling is not reclaimed.
6. **One file, many agent domains**: all tenants live inside one `.meh` file —
   `api.Open` settles the domain the file was opened on, and `DB.SubAgent(llm,
   profile)` creates or returns one under it by name — fully isolated per domain
   except the file-wide L3 pool; legacy files (`FormatVersion < 0x0012`) cannot be
   opened or migrated.
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
    `api.DefaultMemHopDefaults` first, then override. Context size stays
    bounded only because Dream compresses each scene to ≤20 topics, so
    switching it off lets the injected context grow without limit.
