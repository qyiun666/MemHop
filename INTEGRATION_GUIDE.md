# MemHop Host Integration Guide (Go API)

> How to embed MemHop **directly as a Go module** (no MCP server) from your host
> process. Applies to **v1.5.0**. Module path `github.com/qyiun666/MemHop` — you
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
| **LLM on the write path** | `Update`, `RefineTopicKeywords`, `Dream` and `Crystallize` call the LLM and fail when it is down (no silent degradation). `Search` never calls it — a read cannot be blocked by the LLM. |
| **ID shape** | All external IDs are 16-char lowercase hex strings (xxhash64). Treat them as opaque: every response id feeds back as-is, and `api.FormatID` / `api.ParseID` / `api.FormatAgentID` / `api.ParseAgentID` exist for the rare case a host must convert. |
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

---

## 6. Core memory loop (every turn)

The host drives per turn: **turn start `Search` (read this session's memory) → turn end `Update` (settle the whole turn)**. Consolidation is scheduled by the engine once a scene's topic count passes the threshold; hosts may also call `Dream` explicitly. **One L2 scene = one host session** — the host decides which scene to read; the engine never guesses.

### 6.1 Turn start: `Search(q)`

```go
res, err := db.Search(api.SearchQuery{
    SceneID:   sceneIDHex,  // empty = ask the library for a fresh scene (first turn of a session)
    L3ID:      graphIDHex,  // optional: anchors a *newly created* scene to an L3 project domain
    SceneName: "chat-42",   // optional: name for a newly created scene (else "session:<id>")
})
```

No `ctx` parameter — the read path holds no cancellable LLM or network work — and **no retrieval cost**: no LLM, no embedding, no scoring; the hit scene's topics come straight from the L2Meta cache. The only side effect is the scene's hit counter (which feeds Dream's importance feedback).

**`SearchResult` fields:**

| Field | Content | Host use |
|---|---|---|
| `Profile` | L0 profile snapshot | can go into the system prompt |
| `ProfileBrief` | bounded compact profile digest | light per-turn injection; fetch full `Profile` only when needed |
| `Scene` | the scene just read (`SceneID` / `SceneName` / `L3ID` / `TopicCount`) | keep `Scene.SceneID` — Update and later reads use it |
| `Topics` | the scene's depth-1 topics in user-timestamp order, each with `FusedKeywords` and `L4Refs` | **the memory injected into this turn's prompt**; follow `L4Refs` via `GetArchive`/`SearchL4` for originals |

An unknown `SceneID` returns `ErrNotFound` (the library will not create a scene you asked to read); an empty one creates a scene and returns its id.

### 6.2 Turn end: `Update(TurnUpdate)`

```go
topicID, err := db.Update(api.TurnUpdate{
    SceneID:   sceneIDHex,   // must already exist (created by Search)
    UserText:  userRawText,  // required
    UserTS:    userTS,       // Unix milliseconds, > 0
    AgentText: agentReply,   // required
    AgentTS:   agentTS,      // not earlier than UserTS
})
```

One call turns the exchange into **one depth-1 topic**: two L4 archives (`RoleUser` + `RoleAgent`) plus keywords from a single distillation, returning the 16-hex topic id (hand it to `AppendL4Message` for this turn's intermediate messages).

Key property: **distillation runs before any write.** A failed LLM call or an empty extraction errors out and leaves nothing behind — never a half-recorded turn. Unknown scene → `ErrNotFound`; malformed texts/timestamps → `ErrInvalidQuery`.

### 6.3 Consolidation: `Dream(ctx, sceneID)`

```go
rep, err := db.Dream(ctx, "")      // empty sceneID sweeps every scene of the domain
// or db.Dream(ctx, sceneIDHex)    // one scene only
```

Usually **the host does not need to call it**: once a scene's depth-1 topic count passes `Defaults.SceneDreamTopicThreshold` (default 24), `Update` schedules that scene's Dream in the background (one in flight per scene).

Runs L2→L1→L0 compression / decay / profile distillation (several LLM calls, slow) — keep it in a goroutine or between turns.
Returns a structured `*DreamReport`: `ConsolidatedScenes / L2TopicsCompressed / L1NodesAdded|Removed / L1EdgesAdded|Removed / L0Updated` plus `Stages []DreamStage{Name, Status, DurationMs}` (status `ok | skipped | cancelled | error`). An empty report is not an error; a mid-pipeline failure returns the partial report with the error. After compression each scene keeps at most 20 depth-1 topics (`Consolidate` rule), which is the size bound on what a host reads back.

---

## 7. N:N turns: `AppendL4Message` + `RefineTopicKeywords`

A standard turn is 1:1 (one user message, one reply). When the user sends several messages and the agent answers once (or vice versa), settle the turn's outermost pair with `Update` and append every intermediate message to **that same topic** — the exchange then stays one record in memory.

```go
// 1. The turn's outermost pair goes through Update, giving the topic id.
topicID, err := db.Update(api.TurnUpdate{
    SceneID: sceneIDHex, UserText: userMsg1, UserTS: t1,
    AgentText: finalReply, AgentTS: t4,
})

// 2. Intermediate messages append to the same topic. role is a bare uint8:
//    0 = user, 1 = agent, 2 = system, 3 = dream (>3 rejected).
//    contentType picks the record class: text-like (text/document/code) store
//    the original text in Content; media (image/audio/video) store a path or URI.
id1, err := db.AppendL4Message(topicID, userMsg2, t2, 0, api.ContentText)
id2, err := db.AppendL4Message(topicID, "tool output...", t3, 1, api.ContentText)
id3, err := db.AppendL4Message(topicID, "img://shot.png", t3+1, 0, api.ContentImage)

// 3. Re-distill from all originals so the appended messages enter the context.
if err := db.RefineTopicKeywords(ctx, topicID); err != nil { /* LLM failure etc. */ }
```

- `AppendL4Message(topicID, text, timestamp, role, contentType) (string, error)` — pure storage append: **no keyword extraction, no LLM** (works with the LLM down); the new id is appended to the topic's L4Refs and returned as 16-hex. Content contract: `text`/`document`/`code` carry the original; `image`/`audio`/`video` carry a path/URI (mime/size/sha256 in `Metadata`). Undefined values rejected. A missing topic returns `ErrNotFound` — no orphan archive.
- `RefineTopicKeywords(ctx, topicID) error` — concatenates every L4 original in `L4Refs` order → LLM extraction → overwrites the topic's single `FusedKeywords` (**timestamps untouched**, Dream grouping relies on them). Since v1.5.0 there is **no idempotence guard**: every call costs one LLM round trip, and deciding when to call is the host's business (an empty `L4Refs` is a no-op). An empty extraction is refused so the track is never wiped.

---


## 8. Layer API quick reference

### L0 profile

```go
slot, err := db.GetL0()                       // *api.ProfileSlot
err = db.UpdateL0(&api.ProfileSlot{Name: "..."})
err = db.DistillL0(ctx)                       // runs only Dream's emotion/MBTI stage
```

Usually maintained by Dream automatically; manual writes only when forced.
`DistillL0` is the lightweight refresh entry (no-op when the domain has no
profile samples).

### L2 scenes

| Method | Meaning |
|---|---|
| `db.ListScenes() ([]SceneSlot, error)` | scene list (`SceneID / SceneName / TopicCount`) |
| `db.SceneContext(sceneID) (*SceneContext, error)` | full scene view (topics + L4 messages) — **use for session resume** |
| `db.MergeScenes(primaryID, []secondaryIDs) error` | merge scenes |
| `db.ListScenesByL3(l3ID) []SceneSlot` | scenes anchored to one L3 project domain (= host sessions) |
| `db.DeleteTopic(topicID) error` | delete a topic subtree + its L4 archives + indexes; prunes parent `ChildrenIDs` (memory correction) |
| `db.DeleteScene(sceneID) error` | delete a scene + all topics/archives + L1 node; `ErrNotFound` if missing (memory correction) |

### L3 knowledge graphs (stable facts: people / projects / preferences)

```go
res, err := db.ImportL3([]api.L3ImportItem{{
    Title:    "Alice's project",      // node title, required
    Domain:   "project",
    NodeType: "project",              // person / project / preference ...
    Content:  "Alice is building MemHop",
    Keywords: []string{"Alice", "MemHop"},
    SourceRef: "docs/alice.md:1",     // positional reference (optional)
    Related:  []api.L3Relation{{Title: "Alice", Kind: api.EdgePartOf}}, // hyperedges (optional)
}}, api.L3ImportMerge)                 // Skip / Merge / Overwrite
// returns CreatedIDs / UpdatedIDs / SkippedCount / EdgesCreated / Errors
```

`Related` targets resolve by title within the same graph and may appear later
in the batch (two-phase import); re-importing the same batch does not
duplicate edges; unresolvable / self / invalid-kind entries land in `Errors`.

`GetL3 / ListL3 / QueryL3Nodes / QueryL3Subgraph / UpdateL3 / DeleteL3`.

L2↔L3 is one relation, held by the scene: `SceneSlot.L3ID` anchors a session to
a project domain (many scenes may share one graph). It is set when the scene is
created (`SearchQuery.L3ID`) or later via `SetSceneL3ID`, and `ListScenesByL3`
reads the domain back. Topics carry no graph references.

### L4 archive search (historical originals)

```go
arcs, err := db.SearchL4(api.L4Query{
    Keyword: "keyword",        // mode 1: content substring
    // Start: t0, End: t1,    // mode 2: time range (ms)
    // IDs: []string{...},    // mode 3: by id
    // TopicID: &topicHex,    // filter: only this topic's archives
    // Type: &api.ContentImage, // filter: only this content type
})
```

`ArchiveSlot` fields: `ContentType` (text/image/video/document/audio/code),
`Role` (0=user/1=agent/2=system/3=dream), `ContextID`, `CreatedAt`, `Content`,
`Metadata` — for media types `Content` is a path or URI, not the binary.
Single record: `db.GetArchive(id)`.

### L5 capabilities (register tools/skills to the LLM)

| Method | Meaning |
|---|---|
| `db.ListCapabilities(CapabilityListQuery{Status, Type, Keyword})` | list capability cards |
| `db.ImportCapability(path)` | import a memhop-capability/v3 JSON file |
| `db.GetCapability(id)` / `db.DeleteCapability(id)` | read / delete |
| `db.UpdateCapability(id, CapabilityPatch{...})` | partial update (built-ins rejected) |
| `db.ActivateCapability(id)` | draft → active |
| `db.RecordCapabilityUsage(id, success)` | usage feedback |

> The built-in toolbox (6 English cards: `memhop-guide` + 5 LLM-callable manuals) is mounted at Open and served by `ListCapabilities` (read-only, never persisted to `.meh`); manual cards use `type: "api"` with `ref: "api:MethodName"` — call them directly on the api facade. Inject only the one-line index (`id + name + summary + trigger`) plus the guide, and fetch parameter details on demand via `GetCapability(id)`. Resources are tool declarations (`name/desc/input/output` mirror the host tool spec; `input` is a JSON Schema string), so hosts project them with a pure field copy.

### L6 trajectory + crystallization (v1.2.7 additions)

```go
// One trajectory per agent turn: search starts the turn, update ends it —
// derive a fresh turn ID each turn (e.g. hash of session + turn number).
err := db.AppendTrajectory(turnIDHex, api.TrajectorySlot{
    EventType: "tool_call",   // classifies each step of the turn:
                              // llm_request / llm_output / tool_call / tool_result /
                              // subagent_spawn / subagent_done / context_inject /
                              // ask_user / user_reply (free-form; no whitelist)
    Payload:   "tool name + arg summary", // truncated to 4KB
    // TopicID: topicIDHex,  // L2 topic the turn resolves to (search hit or
                              // update's new topic); crystallize folds sibling
                              // turns of the same topic into one prompt
    Timestamp: time.Now().UnixMilli(),
})
// Seq / SessionID are engine-assigned; don't set them.

// L6 → L5: distill the turn's trajectory into capability drafts. Turns
// carrying an L2 TopicID aggregate their sibling turns first (capped at
// 128KB payload, oldest dropped).
res, err := db.Crystallize(ctx, turnIDHex)
// res.CreatedIDs / ReusedIDs / MergedIDs / Errors
// res.Details — per-candidate disposition: []CrystallizeDetail{
//   {Name, Action: "create|reuse|merge|skip", CapabilityID, Reason}}
// Activate drafts with ActivateCapability afterwards.

// Enumerate turns (e.g. to pick crystallize candidates).
sessions, err := db.ListTrajectorySessions()
// sessions[i] = TrajectorySessionSummary{SessionID hex (one per turn), Steps, LastAppendAt}
```

`ReadTrajectory(turnID)` reads events in Seq order. Retention is internal:
Dream drops events older than 7 days (L6 is a process index; durable
products live in L4/L5) — there is no delete API.

---

## 9. Exported types (v1.5.0)

| Kind | Names | Use |
|---|---|---|
| config | `MemHopConfig` / **`LlmConfig`** / `MemHopDefaults` + `DefaultMemHopDefaults` | the whole assembly surface |
| input aliases | `SearchQuery` / `TurnUpdate` / `L3ImportItem` / `L3Relation` / `L3ImportMode` / `L3ImportResult` / `L3NodeQuery` / `L4Query` / `CapabilityListQuery` / `CapabilityPatch` / `CapabilityImport` / `SceneContext` / `SceneMessage` / `TrajectorySessionSummary` / `CrystallizeResult` / `CrystallizeDetail` / `DreamReport` / `DreamStage` / `ResourceRef` / `Workflow` | inputs & id-free results (all string IDs are hex) |
| response DTOs | `ProfileSlot` / `SceneSlot` / `TopicSlot` / `SearchResult` / `HypergraphSlot` / `HypergraphNode` / `HypergraphEdge` / `HypergraphSource` / `L3Graph` / `L3Subgraph` / `ArchiveSlot` / `Capability` / `TrajectorySlot` | every ID field is a 16-hex string (v1.4.1) |
| id helpers | **`FormatID`** / **`ParseID`** / **`FormatAgentID`** / **`ParseAgentID`** (new in v1.4.1) | hex ⇄ uint64 when a host must convert |
| enums | `GraphEdgeKind` / `CapabilityType` / `CapabilityStatus` / `CapabilityOrigin` / `ContentType` / `PlanStatus` | enum aliases |

Enum constants are exported too: `L3ImportSkip/Merge/Overwrite`,
`CapabilityMCP/Skill/API/Composite`, `CapabilityDraft/Active/Deprecated`,
`CapabilityOrigin*`, `EdgeRelated...EdgeCustom`,
`ContentText/Image/Video/Document/Audio/Code/Other`.

> `AppendL4Message` takes `role` as a bare `uint8`; the exported constants are
> `api.RoleUser` / `RoleAgent` / `RoleSystem` / `RoleDream` (values 0-3).
> Plan trees use `api.NodeTypeEvent` / `NodeTypePlan`, the numeric read-side
> `api.Status*` codes and the string write-side `api.PlanStatus*` values.

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

## 11. Minimal runnable skeleton (v1.5.0 signatures)

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

    // One host session = one scene. The first turn asks for a scene with an
    // empty SceneID and keeps the returned id.
    opened, err := db.Search(api.SearchQuery{SceneName: "chat-1"})
    if err != nil { log.Fatal(err) }
    sceneID := opened.Scene.SceneID

    // Per turn: start — read this session's memory (zero LLM) and splice it in.
    res, err := db.Search(api.SearchQuery{SceneID: sceneID})
    if err != nil { log.Fatal(err) }
    _ = res // Profile/ProfileBrief + Topics (FusedKeywords per topic)

    // Per turn: end — settle the whole exchange, get the turn's topic id back.
    userTS := time.Now().UnixMilli()
    topicID, err := db.Update(api.TurnUpdate{
        SceneID:   sceneID,
        UserText:  "user raw message",
        UserTS:    userTS,
        AgentText: "agent reply",
        AgentTS:   time.Now().UnixMilli(),
    })
    if err != nil { log.Fatal(err) }
    _ = topicID // feed AppendL4Message to keep adding this turn's messages

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
   feed back as-is, and `api.FormatID` / `api.ParseID` cover rare conversions.
5. **`Search` writes no memory content**: its only side effect is the scene hit
   counter. To read originals use `SceneContext` / `SearchL4` / `GetArchive`;
   to re-derive keywords use `RefineTopicKeywords`.
   Replaying an `Update` with the same `SceneID` + both timestamps is
   idempotent: the topic id is derived from that triple and the archives from
   the topic id, so a retried settle overwrites instead of duplicating.
6. **One file, many agent domains**: since v1.4 all tenants live inside one
   `.meh` file (`OpenMulti` → `CreateAgent(name)` → `Session(hexID)`), fully
   isolated per domain; legacy files (`FormatVersion < 0x0009`) cannot be
   opened or migrated.
7. **Built-in capability cards are read-only**: `UpdateCapability` rejects them.
8. **Trajectories auto-expire**: Dream drops events older than 7 days;
   the external surface is append + query only (`AppendTrajectory` /
   `ReadTrajectory` / `ListTrajectorySessions`) — no delete API.
9. **The host owns the scene id**: `Update` accepts only an existing scene
   (obtained from `Search` → `Scene.SceneID`). The library never creates a
   scene behind a settle, and Dream never merges scenes — merging is the
   explicit `MergeScenes`, which deletes the merged-away records and thereby
   invalidates any scene id the host still holds.
10. **`SceneDreamTopicThreshold` defaults to 24**: a partial `MemHopDefaults`
    literal leaves it 0, which **disables** automatic consolidation — assign
    `*api.DefaultMemHopDefaults` first, then override. Context size stays
    bounded only because Dream compresses each scene to ≤20 topics, so
    switching it off lets the injected context grow without limit.
