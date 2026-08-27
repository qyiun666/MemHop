# MemHop Host Integration Guide (Go API)

> How to embed MemHop **directly as a Go module** (no MCP server) from your host
> process. Applies to **v1.3.4**. Module path `github.com/qyiun666/MemHop` — you
> only ever import the `api` package.

---

## 1. Integration shape

```
host process
 ├─ go.mod: require github.com/qyiun666/MemHop (or go.work replace → local checkout)
 ├─ import only github.com/qyiun666/MemHop/api (never internal/)
 ├─ one Agent = one *api.DB = one .meh file
 └─ external services:
      ├─ Ollama (embeddings via native HTTP /api/embed, no SDK)
      └─ OpenAI-compatible LLM (keyword extraction / Dream consolidation / Crystallize)
```

### Hard contracts

| Contract | Meaning |
|---|---|
| **Single instance** | One `.meh` file is locked exclusively; a second `*DB` on the same file fails. |
| **Serial calls** | Same-agent operations (Search / Update / Dream / write APIs) are serialized by the library's per-agent domain lock; different agents run in parallel on a `*MultiAgentDB`. The host needs no external queue. `Lock()`/`Unlock()` remain for host-critical sections around raw file access — they serialize **the default domain only** and panic on a closed DB (`Unlock` on a closed DB is a no-op). |
| **LLM is required** | Search / Update / RefineTopicKeywords do LLM keyword extraction and return an error when the LLM is down (no silent degradation). |
| **ID shape** | All external IDs are 16-char lowercase hex strings (xxhash64). Treat them as opaque; format uint64 ids with `fmt.Sprintf("%016x", n)` (MemHop does not re-export id helpers). |
| **Timestamps** | Unix milliseconds everywhere; `<= 0` is `ErrInvalidQuery`. |

---

## 2. Prerequisites

| Dependency | Requirement | Example |
|---|---|---|
| Go 1.27+ | build requirement | — |
| Ollama | running embedding endpoint | `http://localhost:11434` + `nomic-embed-text` (dim 768) |
| LLM | OpenAI-compatible API | DeepSeek / OpenAI / any compatible endpoint |

You can also bring your own encoder: implement the `api.Encoder` interface
(`Encode(text string) ([]float32, error)` + `IsAvailable() bool`) and inject it
via `api.OpenMultiWithEncoder`.

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
| **DBPath** | string | `.meh` path. Created on first open; on reopen the vector dim is checked. |
| **VectorDim** | int | Vector dimension, (0, 65535]. **Must match the Ollama model output** (`ErrVectorDimMismatch` otherwise; no migration). |
| **EncoderAddr** | string | Ollama HTTP address, e.g. `http://localhost:11434`. |
| **EmbedModel** | string | Embedding model name, e.g. `nomic-embed-text`. |
| EncoderTimeoutSecs | int | Encoder timeout (≥0; 0 = default). |
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

`MemHopDefaults` exposes only the three business knobs. Engine tuning constants
(RRF k, scene bonuses, decay lambdas, L1 activation limits, scoring thresholds)
are package-private in `internal/tuning.go` and no longer configurable — hosts
should not need to tune them; if you think you do, open an issue.

| Field | Default | Meaning |
|---|---|---|
| Capacity | 7 | Active-scene bound. When the active set reaches it, **Update** triggers a Dream on the oldest scene (pre-checked for compressibility). |
| SearchDreamContextThreshold | 30 | A Search triggers a scene Dream when the returned context exceeds this many topics. **0 disables the trigger** (relevant if you build a partial literal). |
| DreamCompressMinTopics | 20 | Topics per scene before Dream will compress. |

---

## 5. Open / Close

```go
cfg := &api.MemHopConfig{
    DBPath:      "/data/agent.meh",
    VectorDim:   768,
    EncoderAddr: "http://localhost:11434",
    EmbedModel:  "nomic-embed-text",
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
if err != nil { /* ErrConfig / ErrVectorDimMismatch / ErrCorruption */ }
defer dbm.Close() // checkpoint snapshot + close encoder + release mmap/file lock

// Multi-agent is the only mode: bind a session to a stable hex agent id.
agentID, err := dbm.CreateAgent("guide")
if err != nil { /* ... */ }
db, err := dbm.Session(agentID)
```

- `api.OpenMulti(cfg)` — default Ollama HTTP encoder.
- `api.OpenMultiWithEncoder(cfg, enc)` — custom encoder (mock / local model).
- `OpenMulti` mounts the built-in read-only capability cards (nothing written to `.meh`).
- Explicit flush: `db.Checkpoint()`.

---

## 6. Core memory loop (every turn)

Drive the loop per turn: **Search at turn start (recall + store) → Update at
turn end (archive reply) → Dream when idle (consolidate)**.

### 6.1 Turn start: `Search(ctx, q)`

```go
res, err := db.Search(ctx, api.SearchQuery{
    Text:      userRawText,            // this turn's raw user text, required
    Timestamp: time.Now().UnixMilli(), // Unix ms, required > 0
    // Optional routing (pick one; default is plain retrieval):
    // DirectedL2ID: &sceneIDHex,  // force-write into a specific scene
    // DirectedL3ID: &graphIDHex,  // only retrieve topics referencing that L3 graph
    // AutoCreate:   true,         // skip retrieval, create a fresh scene+topic
})
```

`ctx` cancels LLM keyword extraction, encoder calls and any internally
triggered Dream — pass a request-scoped context.

**`SearchResult` fields:**

| Field | Meaning | Host use |
|---|---|---|
| `Profile` | L0 profile snapshot (name/role/personality/preferences/lexicon/style/emotions) | can be spliced into the system prompt |
| `ProfileBrief` | Compact profile digest (name/role/top preferences/style/emotions, bounded) | lighter per-turn injection; full `Profile` only when needed |
| `Contexts` | Hit scene's context (`TopicSlot` list, depth ≤ 1) | **the memory to splice into this turn's LLM prompt** |
| `AssociatedContexts` | Activated scene topics (L1 hypergraph spreading activation) | optional extra memory |
| `NewTopicID` | ID of the topic created this round (16-hex); 0 = hit existing | feed to Update |

**Side effects (Search writes, it is not read-only):** LLM keyword extraction →
three-channel retrieval (BM25 + f32 vector + entity BK-Tree, RRF fusion) →
topic creation + centroid encoding + one L4 archive + L3 graph linking + scene
activation + scene usage count (folded into the scene record). An unavailable
encoder is an error.

### 6.2 Turn end: `Update`

```go
err := db.Update(topicID, agentReplyText, time.Now().UnixMilli())
// topicID: Search's NewTopicID, or an existing topic ID from Contexts (16-hex)
// nil means appended + indexed; a missing topic returns ErrNotFound
```

Appends a `Role=Agent` L4 archive → refreshes topic keywords and the BM25 index.
Calls the LLM for keyword extraction.

### 6.3 Idle time: `Dream(ctx, sceneID)`

```go
ok, err := db.Dream(ctx, "")      // "" = consolidate all active scenes
// or db.Dream(ctx, sceneIDHex)   // one scene only
// ok=false means nothing to consolidate, not an error
```

Runs the L2 → L1 → L0 pipeline (compression / decay / profile distillation;
multiple LLM calls — run it in a background goroutine or between turns).

---

## 7. N:N turns: `AppendL4Message` + `RefineTopicKeywords`

Standard turns are 1:1 (one user message, one reply). When the user sends
several messages and the agent replies once (or vice versa), run everything
through Search and each message becomes its own topic, breaking the turn. Use
`AppendL4Message` to append messages to **one existing topic**:

```go
// 1. First message creates the topic via Search (get topicID).
res, _ := db.Search(ctx, api.SearchQuery{Text: userMsg1, Timestamp: t1})
topicID := fmt.Sprintf("%016x", res.NewTopicID)

// 2. Remaining messages append to the same topic. role is a raw uint8:
//    0 = user, 1 = agent, 2 = system, 3 = dream (values > 3 rejected).
id1, err := db.AppendL4Message(topicID, userMsg2, t2, 0)   // user text
id2, err := db.AppendL4Message(topicID, agentMsg, t3, 1)   // agent text

// 3. Archive the final reply (AppendL4Message or Update).
db.Update(topicID, finalReply, t4)

// 4. N:N wrap-up: re-extract keywords from all L4 originals so the appended
//    messages become keyword-searchable. Cancellable via ctx.
if err := db.RefineTopicKeywords(ctx, topicID); err != nil { /* LLM failure etc. */ }
```

- `AppendL4Message(topicID, text, timestamp, role) (uint64, error)` — pure
  storage append: **no LLM, no keyword extraction** (usable when the LLM is
  down); the new id is appended to the topic's L4Refs. Returns the raw uint64
  id — format with `fmt.Sprintf("%016x", id)`.
- `RefineTopicKeywords(ctx, topicID) error` — guarded & idempotent: only runs
  when `L4Refs > 2` AND a user/agent keyword track is non-empty; otherwise a
  no-op returning nil. Merges all L4 originals in L4Refs order → LLM keywords →
  `FusedKeywords` (user/agent tracks cleared, timestamps preserved) → BM25
  rebuilt. Errors leave the topic untouched (extract-then-write).

---

## 8. Layer API quick reference

### L0 profile

```go
slot, err := db.GetL0()                       // *api.ProfileSlot
err = db.UpdateL0(&api.ProfileSlot{Name: "..."})
```

Usually maintained by Dream automatically; manual writes only when forced.

### L2 scenes

| Method | Meaning |
|---|---|
| `db.ListScenes() ([]SceneSlot, error)` | scene list (`SceneID / SceneName / TopicCount`) |
| `db.SceneContext(sceneID) (*SceneContext, error)` | full scene view (topics + L4 messages) — **use for session resume** |
| `db.MergeScenes(primaryID, []secondaryIDs) error` | merge scenes |
| `db.ActiveSceneIDs() []string` | currently active scene IDs |
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
}}, api.L3ImportMerge)                 // Skip / Merge / Overwrite
// returns CreatedIDs / UpdatedIDs / SkippedCount / Errors
```

`GetL3 / ListL3 / QueryL3Nodes / QueryL3Subgraph / UpdateL3 / DeleteL3`.
Search automatically links matching graphs onto new topics (`L3Refs`) — this is
what makes `DirectedL3ID` scoping work.

### L4 archive search (historical originals)

```go
arcs, err := db.SearchL4(api.L4Query{
    Keyword: "keyword",        // mode 1: content substring
    // Start: t0, End: t1,    // mode 2: time range (ms)
    // IDs: []string{...},    // mode 3: by id
    // TopicID: &topicHex,    // filter: only this topic's archives
})
```

`ArchiveSlot` fields: `ContentType` (text/image/video/document/audio/code),
`Role` (0=user/1=agent/2=system/3=dream), `ContextID`, `CreatedAt`, `Content`,
`Metadata`. Single record: `db.GetArchive(id)`.

### L5 capabilities (register tools/skills to the LLM)

| Method | Meaning |
|---|---|
| `db.ListCapabilities(CapabilityListQuery{Status, Type, Keyword})` | list capability cards |
| `db.ImportCapability(path)` | import a memhop-capability/v3 JSON file |
| `db.GetCapability(id)` / `db.DeleteCapability(id)` | read / delete |
| `db.UpdateCapability(id, CapabilityPatch{...})` | partial update (built-ins rejected) |
| `db.ActivateCapability(id)` | draft → active |
| `db.RecordCapabilityUsage(id, success)` | usage feedback |

> The built-in toolbox (19 cards: 13 manual API manuals + 6 atomic cards) is mounted at Open and served by `ListCapabilities` (read-only, never persisted to `.meh`); manual cards use `type: "api"` with `ref: "api:MethodName"` — call them directly on `*api.DB`. Resources are tool declarations (`name/desc/input/output` mirror the host tool spec; `input` is a JSON Schema string), so hosts project them with a pure field copy.

### L6 trajectory + crystallization (v1.2.7 additions)

```go
// Record key operations (tool calls / sub-tasks / decisions).
err := db.AppendTrajectory(sessionIDHex, api.TrajectorySlot{
    EventType: "tool_call",   // turn_start / tool_call / tool_result /
                              // subagent_spawn / subagent_done / context_inject /
                              // llm_request / llm_output / turn_end
    Payload:   "tool name + arg summary", // truncated to 4KB
    // L4Ref: &archiveIDHex,  // optionally reference a dialogue archive
    Timestamp: time.Now().UnixMilli(),
})
// Seq / SessionID are engine-assigned; don't set them.

// Decide whether a session is worth crystallizing:
stats, err := db.TrajectoryStats(sessionIDHex)
// stats.Steps      — total events
// stats.ToolUsage  — EventType → count (map)
// stats.LastAppendAt — last event timestamp (Unix ms)

// L6 → L5: distill the trajectory into capability drafts.
res, err := db.Crystallize(ctx, sessionIDHex)
// res.CreatedIDs / ReusedIDs / MergedIDs / Errors
// res.Details — per-candidate disposition: []CrystallizeDetail{
//   {Name, Action: "create|reuse|merge|skip", CapabilityID, Reason}}
// Activate drafts with ActivateCapability afterwards.
```

`ReadTrajectory(sessionID)` reads events in Seq order; `DeleteTrajectory(sessionID)`
cleans up (trajectories are short-lived — the host owns cleanup).

---

## 9. Exported types (v1.2.7)

| Alias | Source | Use |
|---|---|---|
| `MemHopConfig` / `Encoder` | internal | config + custom encoder contract |
| **`LlmConfig`** | internal (new in v1.2.7) | build `MemHopConfig.LLM` by literal |
| **`MemHopDefaults`** | internal (new in v1.2.7) | name the defaults type instead of copying |
| `SearchQuery` / `SearchResult` | internal | search input/output |
| `SceneContext` / `SceneSlot` | internal / core | scene views |
| `L3Graph` / `L3ImportItem` / `L3ImportMode` / `L3ImportResult` / `L3NodeQuery` / `L3Subgraph` | internal | knowledge graph |
| `L4Query` / `ArchiveSlot` | internal / core | archive search |
| `Capability` / `CapabilityListQuery` / `CapabilityPatch` / `CrystallizeResult` / **`CrystallizeDetail`** | core / internal | capabilities |
| **`TrajectoryStats`** / `TrajectorySlot` | internal / core | trajectory + stats |
| **`TopicSlot`** | core (new in v1.2.7) | element of `SearchResult.Contexts` |
| **`ResourceRef`** | core (new in v1.2.7) | element of `Capability.Resources` |
| `ProfileSlot` / `HypergraphSlot` / `HypergraphNode` / `GraphEdgeKind` | core | models |

Enum constants are exported too: `L3ImportSkip/Merge/Overwrite`,
`CapabilityMCP/Skill/API/Composite`, `CapabilityDraft/Active/Deprecated`,
`CapabilityOrigin*`, `EdgeRelated...EdgeCustom`.

> Note: `Role*` constants are **not** re-exported — `AppendL4Message` takes a
> raw `uint8` (0=user, 1=agent, 2=system, 3=dream).

---

## 10. Errors

All MemHop errors carry a numeric code: `api.CodeOf(err)` returns it (0 for
non-MemHop errors). Check with the exported constants:

```go
if api.CodeOf(err) == api.ErrNotFound { ... }
```

Codes: `ErrConfig`, `ErrVectorDimMismatch`, `ErrInvalidQuery`, `ErrNotFound`,
`ErrIO`, `ErrClosed`, `ErrInvalidMagic`, `ErrCRCMismatch`, `ErrCorruption`,
`ErrSerialization`, `ErrDeserialization`, `ErrEncoder`, `ErrLLM`, `ErrAgentNotFound` (agentID not registered or deleted).

---

## 11. Minimal runnable skeleton (v1.2.7 signatures)

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "time"

    "github.com/qyiun666/MemHop/api"
)

func main() {
    dbm, err := api.OpenMulti(&api.MemHopConfig{
        DBPath:      os.Getenv("MEH_PATH"),        // /data/agent.meh
        VectorDim:   768,
        EncoderAddr: os.Getenv("OLLAMA_URL"),      // http://localhost:11434
        EmbedModel:  "nomic-embed-text",
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

    // Per turn: start
    res, err := db.Search(context.Background(), api.SearchQuery{
        Text:      "user raw message",
        Timestamp: time.Now().UnixMilli(),
    })
    if err != nil { log.Fatal(err) }
    _ = res // Profile + Contexts → splice into prompt

    // Per turn: end. NewTopicID is a uint64 — format it to 16-hex first
    // (0 means the turn hit an existing topic; pick its ID from Contexts).
    topicID := fmt.Sprintf("%016x", res.NewTopicID)
    if err := db.Update(topicID, "agent reply", time.Now().UnixMilli()); err != nil {
        log.Fatal(err)
    }

    // Idle / scheduled
    if _, err := db.Dream(context.Background(), ""); err != nil {
        log.Fatal(err)
    }
}
```

---

## 12. Pitfalls

1. **LLM down = memory loop down**: Search/Update/RefineTopicKeywords do not
   degrade. Have an LLM availability fallback before integrating.
2. **Encoder dimension is locked**: `VectorDim` mismatch → Open fails
   (`ErrVectorDimMismatch`), no migration of old files: headers older than
   format `0x0008` are rejected outright at Open.
3. **Timestamps in Unix ms**, `<= 0` → `ErrInvalidQuery`.
4. **IDs are opaque 16-hex strings**: never splice/truncate them; generate them
   with `fmt.Sprintf("%016x", n)` and validate with `strconv` when needed.
5. **Search is a write**: to read history without creating memory, use
   `SceneContext` / `SearchL4`.
6. **One file, many agent domains**: since v1.4 all tenants live inside one
   `.meh` file (`OpenMulti` → `CreateAgent(name)` → `Session(hexID)`), fully
   isolated per domain; legacy single-agent files (`FormatVersion < 0x0008`)
   cannot be opened or migrated.
7. **Built-in capability cards are read-only**: `UpdateCapability` rejects them.
8. **Trajectories are short-lived**: the host owns cleanup via
   `DeleteTrajectory`; MemHop never auto-deletes.
9. **Capacity semantics (v1.2.7)**: the active set is unbounded; reaching
   `Capacity` makes Update trigger a Dream on the oldest scene — Dream is
   skipped when the scene is below `DreamCompressMinTopics` (pre-checked).
10. **`SearchDreamContextThreshold` default 30**: a partial `MemHopDefaults`
    literal leaves it 0, which **disables** the Search-triggered Dream — assign
    `*api.DefaultMemHopDefaults` first, then override.
