# MemHop Host Integration Guide (Go API)

> How to embed MemHop **as a Go module** from your host
> process. Applies to **v1.6.6**. Module path `github.com/qyiun666/MemHop` — you
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
| **LLM on the write path** | `Update` and `Dream` call the LLM and fail when it is down (no silent degradation) — `Update` exactly once per turn, and a failed distillation writes no topic and leaves the turn open for a retry. `Search` never calls it: a read cannot be blocked by the LLM. |
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

`MemHopDefaults` exposes exactly three business knobs. Everything else lives with
the stage that reads it and is not configurable: the L1 decay lambdas and edge
similarity floor sit beside the Dream stages, the prompt output budgets beside the
LLM calls in `internal/cap/llmops`, the distillation sample limits in
`internal/cap/profile`. Hosts should not need to tune them; if you think you do,
open an issue.

| Field | Default | Meaning |
|---|---|---|
| SceneDreamTopicThreshold | 24 | Once a scene's depth-1 topic count passes this, `Update` schedules that scene's Dream in the background. **0 disables the trigger** (relevant when building a partial literal). |
| DreamCompressMinTopics | 20 | Topics per scene before Dream will compress. |
| AgentIdleTTLMs | 3600000 | An agent domain whose context has been idle this long is freed from memory (it rebuilds from its records on next use). 0 disables the sweep. The default domain and the shared L3 pool are never reclaimed. One fact is not rebuildable: the turn that was open. A domain reclaimed mid-round refuses that round's remaining writes and its close until the host opens a new turn — nothing lands silently on the fresh turn — and what the round had already appended stays stored under a topic no read names, since it never settled. Keep this longer than your longest round, or set 0. |
| ContentRetentionMs | 604800000 (7 days) | How long a turn's records (L4 content and L5 plan nodes) outlive it before a Dream sweeps them. 0 or less means the library default. There is no "keep everything" spelling: raise the window rather than turning the sweep off. The window is wall-clock, not round-count: records stamped older than it (a backfill, a test seed) are swept by the first Dream that runs, before any read gets a chance at them. |

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

The path is the host's to bound. `Open` creates the file it is handed, and there is no
sandbox and no name registry underneath it — so when a path, or the agent name a worker's
file is derived from, arrives out of a model's tool arguments, the host resolves it inside
its own directory before handing it in. `CompactTo` is the same kind of argument: an
arbitrary destination path.

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
  yours, and so is the destination this call writes to.

---

## 6. Core memory loop (every turn)

The host drives per turn: **turn start `Search` (read this session's memory and open the turn) → during the turn `AppendArchive` (record what happened) → turn end `Update` (record how the turn opened and ended, and distill the whole turn into the topic that read opened; the distilled track comes back with the call)**. Consolidation is scheduled by the engine once a scene's topic count passes the threshold; hosts may also call `Dream` explicitly. **One L2 scene = one host session, one turn = one topic** — and the library holds both ids: `Search` continues the domain's current scene and opens the next turn on it, so a host running one agent over one library names no ids and carries no keys across the write calls. The engine never guesses which scene to read.

### 6.1 Turn start: `Search(q)`

```go
res, err := db.Search(api.SearchQuery{
    SceneID:  sceneIDHex,  // empty = continue this domain's current scene (after a reopen it
                           // restores the one whose turn counter ran furthest); name a scene to
                           // scope this read to it instead
    NewScene: false,       // true = open a fresh scene — the only way to start a second
                           // conversation over the same domain
    L3ID:     graphIDHex,  // anchors a scene to an L3 project domain, and only a read that
                           // *creates* a scene takes it (NewScene, or the domain's first scene);
                           // handed in along with a scene this read continues it is refused
                           // (ErrInvalidQuery), not ignored
})
```

No `ctx` parameter — the read path holds no cancellable LLM or network work — and **no retrieval cost**: no LLM, no embedding, no scoring; the hit scene's topics come straight from the L2Meta cache. A new scene starts out named `session:<id>` by the library; `UpdateScene(sceneID, ScenePatch{Name: &name})` is the host's one way to title it, and the title survives every later read (Search rewrites that same record to advance its turn counter, never the name). The only write is that counter: it is what mints `NewTopicID`.

**`SearchResult` fields:**

| Field | Content | Host use |
|---|---|---|
| `Profile` | L0 profile snapshot | can go into the system prompt |
| `ProfileBrief` | bounded compact profile digest | light per-turn injection; fetch full `Profile` only when needed |
| `Scene` | the scene just read (`SceneID` / `SceneName` / `L3ID`) | read-side and corrections only — `SceneContext`, `UpdateScene`, `MergeScenes` take it; the write path never needs it, the library holds the open scene |
| `Topics` | the scene's depth-1 topics in user-timestamp order, each with its `FusedKeywords` | **the memory injected into this turn's prompt**; originals are addressed by a turn's own topic id — `SearchL4(L4Query{TopicID})` |
| `NewTopicID` | the topic this read opened for the turn about to run | the read/correction key for this turn's content (`SearchL4{TopicID}`, `DeleteTopic`); the write calls (`AppendArchive`, `Update`, the plan family) do **not** take it — the library holds the open turn |

An unknown `SceneID` returns `ErrNotFound` (the library will not create a scene you asked to read); an empty one continues the domain's current scene, creating one only when it holds none. `NewScene: true` skips all of that and opens a fresh scene.

### 6.2 During the turn: `AppendArchive(ArchiveInput)`

The host records what happens while the turn runs, one call per record, into the turn
`Search` opened — which turn that is belongs to the library, so this call names no ids
and what it writes cannot land on a turn the host is not working, nor under a key no
read ever lists. With no turn open the call is refused (`ErrInvalidQuery`, its message
contains `no turn is open`). The turn's dialogue itself — what the host was asked and
what it answered — is written by `Update` when the turn closes; `AppendArchive` is for
everything in between. `AppendArchive` returns the slot the record took and is the only
way content enters a topic.

```go
seq, err := db.AppendArchive(api.ArchiveInput{
    Kind:      api.KindUtterance, // or api.KindEvent
    Seq:       0,                 // 0 lets the library allocate a slot (Seq 1 and 2 are the
                                  // dialogue slots Update writes, so it allocates above them);
                                  // the slot taken is the return value
    Role:      api.RoleUser,      // utterance only: RoleUser / RoleAgent / RoleSystem
    ContentType: api.ContentText, // utterance only; a non-text slot carries its media path/URL in Content
    Content:   userRawText,       // required
    CreatedAt: userTS,            // Unix milliseconds — a seconds- or microsecond-scale stamp is refused
})
// An event names itself instead of taking a speaker, and may hang on a plan step:
seq, err = db.AppendArchive(api.ArchiveInput{
    Kind:      api.KindEvent,
    EventType: "tool_call",       // event only; free-form, no whitelist
    NodeSeq:   2,                 // optional: the ordinal PlanNodeAdd handed out for this turn
                                  // (0 = the event is bound to no step)
    Content:   `{"tool":"grep"}`,
    CreatedAt: time.Now().UnixMilli(),
})
```

`ID` and `TopicID` are ignored on the way in: the topic is the turn `Search` opened and
the record id follows from `(topic, Seq)`, which is what lets a host read a record back,
change one field and write it to the slot it came from. `Seq: 0`
allocates above every held slot, skipping 1 and 2, which are the dialogue's; because
allocating is the library choosing the slot, it confirms that slot is empty first, and
one whose record will not read back is refused with that read's own code rather than
overwritten. Naming a
held `Seq` **overwrites** it rather than erroring — across kinds too. That is the whole
replay story: a retried turn rewrites its slots instead of accumulating versions. What
a replay does not do is reclaim a slot it stopped filling, so a withdrawn line stays
until `DeleteTopic` or the retention window.

The refusals, all of them before anything is written (an append never creates a plan
step — only `PlanNodeAdd` does, and parentSeq 0 opens the turn's tree): an undefined `Kind`, empty `Content`,
`CreatedAt <= 0` or a `CreatedAt` in the seconds / microsecond band (the unit is Unix
milliseconds: a seconds-scale record is already older than the retention window, so the
next consolidation sweeps the turn's transcript, and a microsecond-scale one never
expires), an undefined `ContentType`, an event with no `EventType`, an utterance
carrying an `EventType` or a `NodeSeq`, an event whose `NodeSeq` names a step this turn
never created (`ErrInvalidQuery`, and nothing lands), the consolidation role `3` (the
library marks its own summaries with it), and content over budget — 4 KiB per event record, its name included, 64
KiB per utterance. Over budget is **refused, never truncated**: a shortened record reads
back exactly like a complete one.

### 6.3 Turn end: `Update(TurnEnd)`

```go
topic, err := db.Update(api.TurnEnd{
    Input:     userRawText,   // lands on the user's dialogue slot (Seq 1)
    Output:    agentReply,    // lands on the agent's dialogue slot (Seq 2)
    Outcome:   "resolved",    // recorded as one `turn_outcome` event for this call
    CreatedAt: time.Now().UnixMilli(), // Unix milliseconds — the host's to supply
})
// topic.FusedKeywords is the track the turn was distilled into — no second read needed
```

`Update` closes the turn `Search` opened and, in the same call, distills it. `Input` and
`Output` land on the two dialogue slots a reader looks for them on — so closing the same
turn again rewrites those two lines instead of accumulating versions — and `Outcome` is
appended as one `turn_outcome` event per call (a suspension and the resume that followed
it are two facts, not one line written twice). An empty field writes nothing; if all
three are empty the call is `ErrInvalidQuery`. Every record passes the same
`content.ValidateAppend` budget check as `AppendArchive`, and all of them are checked
before any is written.

It then reads the turn's utterances and runs exactly one LLM distillation, before the
topic is written, so a failed call or an empty extraction leaves no half-recorded topic —
while the records already on the turn (the host's appends, and `Update`'s own three) stay
where they are. A failed call leaves the turn open, so the host may close it again; a
replay rewrites the two dialogue slots rather than duplicating them. A turn whose
originals the retention window already reclaimed is refused (`ErrInvalidQuery`) without
reaching the LLM; with no turn open the call is `ErrInvalidQuery` and its message contains
`no turn is open`.

`Search` and `Update` are the pair that makes a turn: one turn is one `Search` plus one
(replayable) `Update`. Which scene and which turn this is belongs to the library — this
call names no ids — and the engine re-checks that the turn it closes is one the scene
actually counted, so a stale retry after a delete or merge is refused before the LLM call
with nothing written. That is what keeps an at-least-once write loop safe without letting
a stale retry rewrite a turn that already closed.

### 6.4 Consolidation: `Dream(ctx, sceneID)`

```go
rep, err := db.Dream(ctx, "")      // empty sceneID sweeps every scene of the domain
// or db.Dream(ctx, sceneIDHex)    // one scene only
```

Usually **the host does not need to call it**: once a scene's depth-1 topic count passes `Defaults.SceneDreamTopicThreshold` (default 24), `Update` schedules that scene's Dream in the background (one in flight per scene).

Runs L2→L1→L0 compression / decay / profile distillation (several LLM calls, slow) — keep it in a goroutine or between turns.
Returns a structured `*DreamReport`: `ConsolidatedScenes / L2TopicsCompressed / L1NodesAdded|Removed / L1EdgesAdded|Removed / L0Updated` plus `Stages []DreamStage{Name, Status, DurationMs}` (status `ok | skipped | cancelled | error`). Three of those figures are easy to misread: `L2TopicsCompressed` counts the topics sunk into fused groups, not the number of groups; `L1NodesAdded` counts the scene nodes the sync wrote, which includes an existing node re-stamped because its topic set moved, not only newly created ones; and `L1EdgesAdded` counts the co-occurrence edges created **or strengthened** by the pass. The two removal counters span both stages that remove — the stale rebuild and the decay — and count the edges each took with it as well as the nodes. An empty report is not an error; a mid-pipeline failure returns the partial report with the error. What a host reads back is bounded by convergence, not by a cap: passing the threshold schedules that scene's Dream, and Dream only merges the groups the model judges one — topics it never picked stay at depth 1.

### 6.5 Driving it from a decision-loop kernel

One `.meh` file, one decision loop, one agent domain is the shape this surface is built
for. Because the library holds which scene and which turn are open, the kernel carries no
bookkeeping of its own:

| The loop | The library |
|---|---|
| decides to start a round | `Search(SearchQuery{})` — nothing goes in, the scene's next turn comes open |
| runs its arms (model call, tool call, sandbox answer) | one `AppendArchive` per fact worth keeping, on the turn now open |
| ends the round with a status word | `Update(TurnEnd{Input, Output, Outcome, CreatedAt})` |
| plans the round step by step | `PlanNodeAdd` / `PlanNodeUpdate` / `PlanState`, all on the open turn |
| sleeps (a timer may fire mid-round) | `Dream` — it neither drops the turn the domain holds open nor sweeps what that turn has already written |

`Outcome` takes the kernel's own word for which arm ended the round: the engine stores it
verbatim and never branches on it, the same posture an event's `EventType` has. So no
status vocabulary is imposed on the kernel, and nothing translates on the way in.

Several agents are this shape repeated, not a wider shape. A worker the model recruits
through a tool call gets its own file, its own kernel and its own session handle, and
nothing in the table above changes for it: the library keeps no process-wide state to be
shared, so two such stacks running in one process neither see each other's turns nor reach
the same id. The one thing the host supplies is a bound on where a worker's file may go —
that path came out of a model (§5).

A decision-loop kernel's memory port has this shape: a recall before the model is consulted,
and a remember once at the round's terminal point on every exit arm. The two are not
symmetric — a kernel that works through tool calls recalls several times per round — so the
mapping is one read that opens the turn (`Search`, on the kernel's once-per-invocation hook,
not on the recall port), one pure read for every recall in between (`SceneContext("")` — no id
named, no turn consumed), and one close (`Update`). What an adapter holds is a session handle and the kernel's own name for how
the round ended. Nothing there is carried by the host's memory: the write calls take no id
at all. The one key a loop may hold is `NewTopicID` — the name the round-opening read returned —
and it is needed only to read this round's own event track while the round is still running
(`SearchL4{TopicID, Kind: event}`) or to retract it (`DeleteTopic`). The plan tree does not even
need that: `PlanState` reads the turn the library holds open. An event appended mid-round is
readable by that id before `Update` closes the turn.

What is left for the host is seven facts to know, not seven adapters to write:

- **A recall port turns "no scene yet" into an empty answer.** `SceneContext("")` refuses with
  `ErrNotFound` on a domain that has never been read — it does not create a scene for a read.
  That is the library's contract, but a decision-loop kernel reads memory *before* every model
  call and treats an error from that port as ending the whole invocation, so a first round
  would kill the agent that has nothing to remember yet. Check that one code and return no
  records; pass any other error up, where it is a real fault.

- **Closing one turn twice is a replay, not a continuation.** The two dialogue slots hold the
  pair the last close stated, while every ending stays as its own event —
  `TestTwoClosesOfOneTurnKeepBothEndingsAndTheLastDialogue` pins exactly that residue. So a
  round that suspends for input and resumes must not be closed twice on the same turn, and the
  mapping above is what keeps it straight: each invocation reads once (`Search`) and closes
  once (`Update`), so each arm is its own turn and keeps its own words. Nothing in the library
  merges the two arms — and the close is not where a turn's history accumulates, which is what
  `AppendArchive` is for.

- **Only `Update` and `Dream` talk to the model.** `Update` makes exactly one call, inside
  the domain lock; the read that opens a turn and every write inside it are deterministic.
  The background consolidation an `Update` may schedule also takes the domain lock, so a
  slow Dream holds up that domain's other calls — and never another domain's.
- **Milliseconds, everywhere.** Every timestamp on this surface is Unix milliseconds, and
  the write boundary refuses the seconds band (1e9–1e11) rather than storing a value the
  next consolidation would sweep away as expired. A kernel carrying `time.Time` or Unix
  seconds converts once where it fills the struct (`t.UnixMilli()`); that is the only
  conversion left on this path.
- **"The last N turns" counts topics, not records.** A turn is one topic on the scene's
  surface (`Search` and `SceneContext` hand them over in turn order), while
  `SearchL4{Limit}` caps *records*. A recall window of N turns therefore takes those N
  topics and then reads each one's content; no L4 query on this surface counts turns. What
  each of those reads costs is the library's scan of the whole domain, not of the window: on a
  300-turn domain (900 records) one `SceneContext("")` handed over all 300 topics in 1.4-2.2 ms over five runs,
  and a per-topic follow-up (`SearchL4{TopicID}`) took 43-64 microseconds each. So an adapter
  that looks up the ending of each recalled turn pays N x 50 us, and no additional read is needed
  to keep the recall cheap at that size.
- **A facade type that is *not* the engine's own is hiding something.** Most shapes here are
  aliases, so the struct filled at the call site is the struct the engine reads — a value
  crosses that boundary with no conversion step at all. The exceptions withhold a thing
  rather than rename one: an id, rendered as hex for the host (`TopicSlot`, `ArchiveSlot`), or
  a field the host has no right to write (`ProfileInput` is the four writable fields where
  `ProfileSlot` is the whole read shape; `PlanStep` carries no turn key, because the turn is
  the library's to remember).
- **Loops stay apart, including one started mid-round.** The open turn belongs to an agent
  domain and nothing about it is process-wide, so a second file — or a second domain of the
  same file — cannot write onto the first one's turn, and opening that second file *between*
  the first round's start and its close is ordinary: each close still settles the turn its
  own read opened (`api/surface_turn_test.go` pins both). Sub-agent domains are the separate
  facility for several domains sharing one file and one L3 knowledge graph; a second decision
  loop does not need one, it needs a second `Open`.

---

## 7. One turn, one topic

A turn is one topic: the id `Search` minted for it and now holds, and everything the host records
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

The 25 session methods split by audience:

- **Runtime/task face (18)** — the host drives these every turn and LLM tools bind to them: `Search` / `AppendArchive` / `Update` / `Dream` (the host-driven loop), `GetL0` / `UpdateL0`, `ListL1`, `ListScenes` / `SceneContext`, `GetL3` / `ListL3` / `ImportL3` / `QueryL3Nodes` / `QueryL3Subgraph`, `SearchL4`, `PlanNodeAdd` / `PlanNodeUpdate` / `PlanState`.
- **Assembly/admin face (7)** — host code at session boundaries and management channels only, never an LLM tool: `UpdateScene` / `RenameTopic` / `MergeScenes` / `DeleteScene` / `DeleteTopic`, `UpdateL3` / `DeleteL3`.

The file-level lifecycle and diagnostics sit on `api.DB` instead (7): `Primary` / `SubAgent`, then `Checkpoint` / `CompactTo` / `Close` / `IsClosed` / `Stats` (file size plus reachable record count across the file — the numbers a compaction decision is made from). There is no capability surface anywhere: the engine neither stores nor parses cards, so a host reads the events of a turn with `SearchL4{Kind: event}` and organizes them itself.

### L0 profile

```go
prof, err := db.GetL0()                       // *api.ProfileSlot — the full record
err = db.UpdateL0(&api.ProfileInput{Name: "..."})
```

`UpdateL0` takes a `ProfileInput`, which holds exactly the four fields a host
owns — `Name`, `Role`, `Personality`, `Preferences`. The rest of the stored
profile is not in that shape because it is not the host's to state: `EmotionState`
and `MBTI` are evolved by Dream, `UpdatedAtMs` is stamped by the library, and
`AgentType` is decided when the domain is created. A write inherits those two
distilled signals and `AgentType` from the record and stamps `UpdatedAtMs` itself,
so there is no need to `GetL0` and fill values back, and no read-only field can be
smuggled in — the compiler refuses. The distilled half refreshes
automatically with Dream: there is no standalone distill entry point.

`Personality` is the one field with two writers, and the one a write does *not*
inherit: Dream's distillation replaces it with the personality summary the model
derived from this domain's memories, so it reads back as whichever of the two ran
last. An `UpdateL0` that leaves it empty therefore clears the distilled summary,
and the next pass evolves it again — carry the value back from `GetL0` if you mean
to keep it. `Name`, `Role` and `Preferences` have the host as their only writer.

### L2 scenes

| Method | Meaning |
|---|---|
| `db.ListScenes(l3ID) ([]SceneSlot, error)` | scene list (`SceneID / SceneName / L3ID`); a non-empty `l3ID` keeps only the scenes anchored to that project domain, `""` lists all |
| `db.SceneContext(sceneID) (*SceneContext, error)` | the scene's whole transcript (topics + their L4 originals) and **no write at all** — no turn is opened; **use for session resume**, and for every recall a round makes after the one that opened its turn. An empty `sceneID` reads the scene this domain is working, so nothing has to be held to call it again. Unlike `Search` it flattens to depth 2, because a Dream-fused group keeps its originals on the children it sank, and this is the only read that brings them back. `ChildCount > 0` is what marks a fused group (its own single message carries role 3, the mark Dream puts on a fused group's summary); `Depth` only says whether the topic is still on the scene's surface — a group a later pass folded away sits at 2 level with the turns it summarizes. The entries returned are the whole count — roots and the sunk children this read alone brings back |
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
Deletion has one granularity: the whole graph. `QueryL3Subgraph`'s `edgeKinds`
narrows the walk to the kinds named and leaves the condition out when the list is
empty; a kind outside the six constants is refused with `ErrInvalidQuery`, because
the write boundary refuses to store one and an empty subgraph is the answer a host
reads back as "this graph holds no such edges".

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
    // Limit: 50,                // keep the tail of the read's order (<=0: every match)
})
```

`ArchiveInput` is what a write hands in: `Kind`, `Seq`, `ContentType`, `Role`, `EventType`,
`NodeSeq`, `CreatedAt`, `Content` — and no `ID` or `TopicID`. Which turn a record belongs to is
the library's to remember (`Search` minted it), so a record read back has nowhere to claim an
origin when it is appended again. `ArchiveSlot` is the same shape once stored: it carries
`Kind` (utterance / event), `Seq`, `ContentType`
(text/image/video/document/audio/code/other), `Role` (`RoleUser` / `RoleAgent` /
`RoleSystem`, plus `RoleDream` — the mark the library itself puts on a consolidated
summary, readable and refused on append),
`TopicID`, `CreatedAt` and `Content` — for media types `Content` is a path or URI,
not the binary.
Every query field is optional and the ones you set **AND** together — there are no
modes to choose between. The order is what a query spans: `Seq` within one topic,
`CreatedAt` across topics with the record id breaking ties, and `Limit` keeps the tail
of that order. So the reads a host
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
// A turn's events are L4 content of kind event, written into the turn Search
// opened — the host names no key.
_, err := db.AppendArchive(api.ArchiveInput{
    Kind:      api.KindEvent,
    EventType: "tool_call",   // any non-empty name the host chooses; no whitelist
    Content:   "tool name + arg summary", // 4 KiB covers the whole record, name included
    CreatedAt: time.Now().UnixMilli(),
})
// Seq and the owning topic are engine-assigned: what you append lands on the open
// turn, and that turn's plan nodes live under the same key.
// `NodeSeq` is what binds an event to a step — see the plan surface below;
// leave it 0 for a plain turn event.
```

The track reads back with `SearchL4(L4Query{TopicID: &topicID, Kind: &event})`, in Seq
order, and it is addressed **by turn key only**: nothing returns an event handle on
write because no public call takes one, and Dream drops content older than the
retention window. Of an event you hand in, `EventType`, `NodeSeq`, `Content` and
`CreatedAt` are used as given; the library assigns `Seq` and the owning topic and
forces `ContentType` to `text` with no speaker — a thing that happened has neither.
An event over 4 KiB — name and body together — is refused: a shortened event would read back exactly like a
complete one.

What those events become is the host's decision and the engine takes no part in it.
There is no capability surface: the library stores no cards, parses no card format and
offers no crystallization call, so a host that distills its own action log does that
with its own prompt, its own dedupe and its own file layout. Nor is there a call that
enumerates the turns holding events — `Search` issued those keys, and the host that
appended under them still has them.

### L5 plan tree (Go host surface)

A plan belongs to the turn that opened it: **the library keys the tree on that turn's
topic id** — the one `Search` minted and now holds — and the same key addresses the
turn's content in L4. A step is addressed by a **per-turn
ordinal** (`Seq`, a `uint32` the library hands out from 1 and the host only ever echoes
back: the create calls return it), and `ParentSeq` says which step it hangs under
(`0` = a root). There is no plan id and no path string to mint, the plan calls name no
topic id (they act on the turn `Search` opened), and `PlanState()` is how a tree comes
back.

| Call | Meaning |
|---|---|
| `seq, err := db.PlanNodeAdd(0, title)` | open this turn's tree by creating its first root step, and take back the ordinal that step is addressed by from here on. A turn's tree starts with no steps, so this is also how a plan first appears under a turn; there is no separate "create the tree" call |
| `seq, err := db.PlanNodeAdd(parentSeq, title)` | add one step to the tree and get its ordinal. `parentSeq` `0` hangs it at the top level, so this is also how a second root joins the forest; any other value must name a step this tree already holds — `PlanNodeAdd` under an unknown parent is `ErrNotFound` and grows nothing. A step is created `in_progress`, so no status is asked for here; a title may be left empty and filled in later, and the view falls back to the ordinal until the host names the step |
| `err := db.PlanNodeUpdate(api.PlanStep{Seq: seq, Status: api.PlanStatusDone, Summary: s})` | restate one step: its `Status` plus the node's own `Title`/`Summary`. `Status` is stated every time (there is no "leave it as it was" spelling) while a blank `Title`/`Summary` keeps what the node holds, so updating a step never rewinds its title or erases a folded summary. A step reaching a terminal status records `FinishedAt`; restating a settled step as `in_progress` re-opens it and drops that timestamp. Once every direct child of a `Done` parent is itself terminal, the parent's summary folds up from its children's. A status outside `in_progress` / `done` / `failed`, or an ordinal this turn never created (`ErrNotFound`), is refused **before the node is touched** and leaves the tree exactly as it was. This call writes no content |
| `tree, err := db.PlanState()` | read the forest view (`PlanTree.Roots` + `DoneCount` / `TotalCount`, which are summed over every step of every tree; every `PlanNodeView` carries `Seq` / `ParentSeq` / `Status` / `Summary` / `Children`) — also the restart recovery path |
| `db.AppendArchive(ev)` with a non-zero `ev.NodeSeq` | record a step event against one step of this turn's tree. The step has to exist already: an ordinal nobody created refuses the whole record (`ErrInvalidQuery`) and stores nothing, because an event naming a step the plan never holds is the plan and the record disagreeing — the tree is `PlanNodeAdd`'s to build, and a mistyped ordinal cannot quietly open a second one. `EventType` is **the host's own name for the step**, on this path exactly as on a bare turn event — the engine never branches on it (the name comes back verbatim through `SearchL4`) and only refuses an empty one. Convention names for readers: `plan_step`, `llm_request`, `llm_output`, `tool_call`, `tool_result`, `subagent_spawn`, `subagent_done`, `context_inject`, `ask_user`, `user_reply` |

Status has three values and one string encoding each: `api.PlanStatusInProgress`
(`in_progress`), `api.PlanStatusDone` (`done`), `api.PlanStatusFailed` (`failed`). The
engine keeps no "planned but not started" state — a step exists because the host created
it, and it exists in progress.

The plan write surface sits on `api.Session`'s 18 task-face methods: a tree is built on
the turn `Search` opened for the domain, so these calls name no topic id.

The all-zero key `0000000000000000` is reserved (it is the value a record leaves its key
unset with) — `Search` never opens a turn on it, and the read entries that do take a
topic id (`SearchL4{TopicID}`, `RenameTopic`, `DeleteTopic`) reject it.

---

## 9. Exported types (v1.6.6)

| Kind | Names | Use |
|---|---|---|
| entry & handles | **`Open`** → `*DB`, then `DB.Primary()` / `DB.SubAgent(llm, profile)` → `*Session` | the only ways in; an agent domain is held as a handle, never named by an id |
| config | **`LlmConfig`** / `MemHopDefaults` + `DefaultMemHopDefaults` | the endpoint and tuning arguments `Open` takes |
| input shapes | **`ProfileInput`** / `SearchQuery` / `TurnEnd` / `ScenePatch` / `L3ImportItem` / `L3Relation` / `L3ImportMode` / `L3NodeQuery` / `L4Query` / `PlanStep` / `ArchiveInput` (the L4 write shape; a read returns `ArchiveSlot`) | inputs; `ProfileInput` is the only profile a host may write, and of its four fields only `Name` is required |
| response DTOs | `ProfileSlot` / `SceneNodeView` / `SceneSlot` / `TopicSlot` / `SceneContext` / `SceneContextTopic` / `SceneMessage` / `SearchResult` / `HypergraphSlot` / `HypergraphNode` / `HypergraphEdge` / `L3Graph` / `L3Subgraph` / `L3ImportResult` / `PlanTree` / `PlanNodeView` / `DreamReport` / `DreamStage` | every id field is a 16-char hex string, and every one of them was issued by the library |
| enums | `GraphEdgeKind` / `ContentType` / `ArchiveKind` / `PlanStatus` / `AgentTypePrimary` + `AgentTypeSub` | the vocabulary a call is written in |
| errors | `Code` + the `Err*` constants, read with `CodeOf(err)` | the numeric code behind an error string |

Enum constants are exported too: `L3ImportSkip` / `Merge` / `Overwrite`,
`EdgeRelated`…`EdgeCustom`, `ContentText`…`ContentOther`, `KindUtterance` /
`KindEvent`, `RoleUser` / `RoleAgent` / `RoleSystem` / `RoleDream`,
`PlanStatusInProgress` / `PlanStatusDone` / `PlanStatusFailed`.

Nothing in this list converts an id: there is no `FormatID` / `ParseID` pair and no
numeric id in any signature, because a host echoes back the hex strings it was given
and builds none itself. There is no capability type either — the card format, its
file layout and its activation are the host's own assets.


> A record's kind is `api.KindUtterance` / `api.KindEvent`. L4 `role` is a bare
> `uint8`, and the three a host may declare on an appended utterance are
> `api.RoleUser` / `RoleAgent` / `RoleSystem`. The fourth value, `api.RoleDream`, is the
> library's own mark on a consolidated summary: naming it is how a host tells that summary
> apart from a turn's own two lines when it renders a prompt, and `AppendArchive` still
> refuses it, so a host cannot write a record that reads as consolidated. A name is not a
> write grant — the boundary is.
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
`ErrSerialization`, `ErrDeserialization`, `ErrCancelled` (the caller's own context
ended before the work did — a cancelled `Dream`, an LLM call abandoned mid-retry)
and `ErrLLM`. `ErrAgentNotFound` is exported but no public call produces it: domains
come back as handles, so there is no host-supplied agent id that could be
unregistered. `api.NewError(code, message)` builds one of these errors for a caller
that refuses on its own terms — a tool layer turning down an argument outside a
vocabulary, say — so that refusal carries the same code the library's own refusals do.
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

    // One host session = one scene, and the library holds it: an empty SearchQuery
    // continues the domain's current scene (creating one on first use) and opens the
    // next turn. NewScene: true starts a second conversation instead. A host running
    // one agent over one library carries no id across the write calls.
    res, err := db.Search(api.SearchQuery{})
    if err != nil { log.Fatal(err) }
    _ = res // Profile/ProfileBrief + Topics (FusedKeywords per topic)

    // The plan comes first: a step exists because it was created here, one call
    // per step, and an event can only be attributed to a step the tree already
    // holds. The create calls hand back the ordinal that step is addressed by.
    userTS := time.Now().UnixMilli()
    fix, err := db.PlanNodeAdd(0, "locate the regression")
    if err != nil { log.Fatal(err) }
    leaf, err := db.PlanNodeAdd(fix, "fix")
    if err != nil { log.Fatal(err) }

    // Per turn: record what happened while it ran, into the turn Search opened.
    // What was said (input / output) is supplied when the turn closes.
    _, _ = db.AppendArchive(api.ArchiveInput{Kind: api.KindEvent,
        EventType: "tool_call", NodeSeq: leaf,
        Content: "grep ...", CreatedAt: userTS + 1})
    _ = db.PlanNodeUpdate(api.PlanStep{Seq: leaf, Status: api.PlanStatusDone,
        Summary: "…"})

    // Per turn: end — Update writes the turn's input and output onto the dialogue
    // slots, records the outcome as one event, and distills the turn into its keywords.
    if _, err := db.Update(api.TurnEnd{Input: "user raw message", Output: "agent reply",
        CreatedAt: time.Now().UnixMilli()}); err != nil { log.Fatal(err) }

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
   the records already on the turn stay. A failed `Update` leaves the turn open, so the
   host can close it again.
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
   Replaying an append to the same slot is idempotent: the record
   hashes from the open turn and its `Seq`, so a retry rewrites it instead of duplicating — and a
   slot the replay stops filling is not reclaimed.
6. **One file, many agent domains**: all tenants live inside one `.meh` file —
   `api.Open` settles the domain the file was opened on, and `DB.SubAgent(llm,
   profile)` creates or returns one under it by name — fully isolated per domain
   except the file-wide L3 pool; legacy files (`FormatVersion < 0x0012`) cannot be
   opened or migrated.
7. **Content and plans auto-expire**: Dream drops a topic's content past the
   retention window (seven days by default, configurable via
   `Defaults.ContentRetentionMs`) and plan nodes past it (a tree still in flight is exempt);
   `DeleteTopic` / `DeleteScene` are the explicit corrections. Past the window a
   topic keeps its keyword track and its `Messages` come back empty or with gaps in
   `Seq` — a legal end state, not a failed read. A fused group's summary ages from
   the pass that wrote it, not from the turns it replaced, so it can outlive their
   originals: a parent may still carry Dream's own text after its children's have
   been swept. Everything is keyed by the turn the library holds
   open, so append during the turn and let `Update` close it — there is no id to invent
   or carry.
8. **The library holds the scene and the open turn**: `Search` continues the domain's
   scene and opens the next turn; the write calls (`Update`, `AppendArchive`, the plan
   family) act on that turn and name no ids. With no turn open — never `Search`ed, or the
   turn or its scene was deleted (`DeleteTopic`/`DeleteScene`) or merged away
   (`MergeScenes`) — they return `ErrInvalidQuery` with a message containing
   `no turn is open`. The library never creates a scene behind a write, and Dream never
   merges scenes — merging is the explicit
   `MergeScenes`, which deletes the merged-away records and thereby invalidates
   any scene id the host still holds. Close the turn before merging: a turn `Search`
   opened and `Update` never closed has no topic record yet, so there is nothing
   for the merge to retarget — its id names a scene that is gone, and the turn can
   never be closed afterwards.
   Each `Search` opens exactly one turn: a host that reads a scene twice and
   updates once simply skips a turn number — gaps cost nothing, and no read
   ever reissues an id already given out.
9. **`SceneDreamTopicThreshold` defaults to 24**: a partial `MemHopDefaults`
    literal leaves it 0, which **disables** automatic consolidation — assign
    `api.DefaultMemHopDefaults` first, then override. Context size is held in
    check only by Dream converging each scene towards `DreamCompressMinTopics`
    (default 20) — a target a pass aims at, not a ceiling a scene is kept under
    — so switching automatic consolidation off lets the injected context grow
    without limit.
