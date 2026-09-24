# MemHop Host Integration Guide (Go API)

> How to embed MemHop **as a Go module** from your host
> process. Applies to **v1.6.6**. Module path `github.com/qyiun666/MemHop` — you
> only ever import the `api` package.

> This guide describes the surface as it is now: `api.Open` → `api.DB`, domains held
> as handles from `Primary` / `SubAgent` / `Agent` (the third takes the id
> `Session.AgentID` issued for a domain, and a host only ever round-trips it), no
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
 │   DB.SubAgent(llm, profile) for one created under it, and
 │   DB.Agent(llm, id) for one addressed by the id Session.AgentID hands out
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
| TimeoutSecs | 0 = not filled | Whole HTTP call budget in seconds; unfilled answers 120. A zero is never "no timeout" - that would hold a domain lock open on a dead endpoint. A filled window is honoured on the wire, and once: an endpoint slower than it comes back as `ErrLLM` after a single wait — not `ErrCancelled` (which would point the host at its own context), and not repeated per retry, because a slow endpoint is neither a 429 nor a 5xx. The same reason bounds the top: past 9223372036 s the seconds no longer fit a duration and a wrapped timeout reads as no timeout, so `Open` refuses it with `ErrConfig`. |
| MaxOutputTokens | 0 = not filled | Cap on one reply; unfilled answers 8192. The prompt budgets are computed against this ceiling, so leaving it unset is the normal case. |

### `MemHopDefaults` — common overrides

`MemHopDefaults` exposes four knobs. Because an unfilled one (0) takes the library default, a host that is happy with the defaults passes
`api.MemHopDefaults{}` and fills only what it changes — no need to reach for `DefaultMemHopDefaults` and copy it. Everything else lives with
the stage that reads it and is not configurable: the L1 decay lambdas and edge
similarity floor sit beside the Dream stages, the prompt output budgets beside the
LLM calls in `internal/cap/llmops`, the distillation sample limits in
`internal/cap/profile`. Hosts should not need to tune them; if you think you do,
open an issue. One vocabulary across the four: **0 means "not filled"** and the library
default answers; a negative is the explicit "switch this off" — except the retention
window, which has no off spelling at all: `Open` refuses a negative rather than folding it onto
the default, and refuses a window past the one the sweep can represent.

| Field | Default | Meaning |
|---|---|---|
| SceneDreamTopicThreshold | 24 | Once a scene's depth-1 topic count passes this, `Update` schedules that scene's Dream in the background. A negative disables the trigger. A refused pass changes nothing, so the count still passes next time and the scene is asked again — checked at every round close, and one consolidation call per scene per pass is its ceiling — a Dream already in flight for that scene absorbs the intervening closes (`TestDeclinedConsolidationAsksOncePerScenePerPass`). `DreamCompressMinTopics` is checked *before* the model is asked, so a scene under it spends nothing — but by default that floor (20) sits below this trigger (24), and every settled round adds a depth-1 row while the model keeps refusing, so raising the floor delays these asks rather than bounding them (it is also the number the consolidation prompt tells the model to converge toward). The one lever that bounds the spend is this field negative, then driving `Dream` on a cadence you choose. |
| DreamCompressMinTopics | 20 | Topics per scene before Dream will compress. It is also the number the model is told to converge toward, so a negative — no floor, merge as far as the model's own rules allow — is the aggressive spelling, not the conservative one. |
| AgentIdleTTLMs | 3600000 | An agent domain whose context has been idle this long is freed from memory (it rebuilds from its records on next use). A negative disables the sweep. The default domain and the shared L3 pool are never reclaimed. One fact is not rebuildable: the turn that was open. A domain reclaimed mid-round refuses that round's remaining writes and its close until the host opens a new turn — nothing lands silently on the fresh turn — and what the round had already appended stays stored under a topic no read names, since it never settled. Keep this longer than your longest round, or set a negative. |
| ContentRetentionMs | 604800000 (7 days) | How long a turn's records (L4 content and L5 plan nodes) outlive it before a Dream sweeps them. 0 means the library default; a negative is refused with `ErrConfig` instead of being folded onto it, because a host writing one is asking for a sweep that never runs. There is no "keep everything" spelling either: the longest window the sweep can measure is 9223372036854 ms (about 292 years), and past that the millisecond count overflows a `time.Duration` — the cutoff lands in the future and every record in the domain reads as expired — so `Open` refuses it too. The window is wall-clock, not round-count: records stamped older than it (a backfill, a test seed) are swept by the first Dream that runs, before any read gets a chance at them. A round that is still open is no exemption - the sweep reads the stamp, not the round's state; what a Dream never does is drop the open turn, so the pending close still settles it (its own pair is fresh, so it survives). **L3 is not a turn's record, and no window reaches it**: the knowledge graph is the file's shared pool on its own content clock, so the sweep that empties a domain leaves the project nodes a tool call reads — and their content — intact |

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
    api.MemHopDefaults{}, // tuning knobs: leave it empty for the library defaults, fill one to change one
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

**What a call answers after `Close`.** Once `Close` returns, every published call —
on `DB` and on every `Session` taken from it — answers `ErrClosed` (5002): the engine
refuses rather than read a released file, and nothing panics. So a host whose worker is
still driving a turn while the run ends gets a code it already branches on, not a
corruption error and not a crash. `Close` belongs to that set deliberately: a second
`Close` answers `ErrClosed` too, because it closed nothing — which is what lets
`defer lib.Close()` and an explicit end-of-run `Close` share one reading. Two accessors
keep answering normally because they read no engine state: `Session.AgentID` and
`DB.IsClosed`. `TestEveryCallAnswersErrClosedAfterClose` walks every published method on
that foot, and refuses to let its own table shrink as the surface grows.

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
  to be scanned to find it. `Primary()` returns its handle, and `AgentID()` on any handle
  renders the id of the domain it is bound to.
- `SubAgent(llm, profile)` creates the domain named `profile.Name` the first time and
  returns the same one every time after — the name is the domain's address, frozen at
  creation. `llm` is that domain's own endpoint, so a sub-agent can run on a different
  model. The profile is written only if the domain has none yet, which also finishes
  off a domain left half-created by a crash. `AgentType` is stamped, not taken: a
  domain created this way is a sub-agent.
  The name is also bounded: `MaxSubAgentNameBytes` (256) is the cap, measured in **bytes** — the
  registry stores it inside one record, so an unbounded key is an unbounded record. A host that builds
  worker names out of task titles should shorten against that constant rather than discover it in a
  refusal, since the name is the handle it will reopen the domain by.
- `Agent(llm, agentID)` reaches a domain by the id `Session.AgentID` issued for it: the
  same handle `SubAgent` returns for its name, re-pointed at `llm`. It creates nothing —
  an id this file never registered is refused with `ErrAgentNotFound`, so a mistyped or
  invented id cannot open an empty memory in a real domain's place. A host that wants to
  keep exactly one identifier per memory keeps this id rather than a name; the primary's
  own id is the implicit zero one and addresses the primary.
- **The id is scoped to its file.** Every file has its own primary and every primary is the
  zero domain, so the same 16 zeros name different memories in different `.meh` files. A
  host that deploys one file per agent keys across files by the path (path plus id at most);
  a map keyed on id alone would fold two agents' memories together.
- `Agents()` is that rule read the other way: it lists every domain in the file — id, name, and
  whether it is the primary — in id order, so a host that inherited a file or lost its own
  roster can discover what is in it rather than guess a name (and thereby create a second
  domain beside the real one). A tenant key that cannot be read stops the listing with that
  cause; a shorter list would be a wrong answer.
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
                           // restores the one a turn was opened in most recently; the counter only breaks
                           // ties among records written before that stamp); name a scene to
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
| `ProfileBrief` | the profile as a **bounded** digest: at most 6 lines, every free-text field capped at 160 runes and each preference value at 120 (a truncated one ends in `…`), the 5 lowest preference keys, in that order, and 2100 runes as the ceiling it never exceeds | light per-turn injection; fetch full `Profile` only when needed |
| `Scene` | the scene just read (`SceneID` / `SceneName` / `L3ID`) | read-side and corrections only — `SceneContext`, `UpdateScene`, `MergeScenes` take it; the write path never needs it, the library holds the open scene |
| `Topics` | the scene's depth-1 topics in user-timestamp order, each with its `FusedKeywords` | **the memory injected into this turn's prompt**; originals are addressed by a turn's own topic id — `SearchL4(L4Query{TopicID})` |
| `NewTopicID` | the topic this read opened for the turn about to run | the read/correction key for this turn's content (`SearchL4{TopicID}`, `DeleteTopic`); the write calls (`AppendArchive`, `Update`, the plan family) do **not** take it — the library holds the open turn |

An unknown `SceneID` returns `ErrNotFound` (the library will not create a scene you asked to read); an empty one continues the domain's current scene, creating one only when it holds none. `NewScene: true` skips all of that and opens a fresh scene. `SceneID` and `NewScene` are not sent together: one names a conversation to go on, the other asks for a different one, and the read is refused (`ErrInvalidQuery`) rather than answered by dropping a flag a host believes it set.

**One session is one conversation in progress.** The open turn belongs to the domain, not to the caller, so a `Search` from a second goroutine takes the turn the first was about to close — and the first's turn is left unsettled while its `Update` closes the other id. The domain lock keeps the file consistent either way (nothing corrupts, nothing interleaves on disk); what it does not do is multiplex turns. So run each concurrent worker on **its own domain** (`SubAgent` hands out a session per profile name), or serialize `Search → … → Update` on one handle. `TestConcurrentWorkersOnTheirOwnDomains` and `TestConcurrentReadsDuringWrites` cover both halves: six workers × four rounds on six domains, each listing exactly its own turns, and reads that keep running while a `Dream` and a new round rewrite the scene.

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
library marks its own summaries with it), and content over budget — `MaxEventPayloadBytes`
(4 KiB) per event record, its name included, and `MaxUtterancePayloadBytes` (64 KiB) per
utterance; both are exported, so a host chunks what it appends against the number rather than
a copy of this sentence. Over budget is **refused, never truncated**: a shortened record reads
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

**What the scene id scopes, and what it does not.** It picks which scenes get consolidated, and an id
this domain does not hold fails the call with `ErrNotFound` — a model echoing a stale scene id
out of its context is a normal event, and a clean no-op would read to the host as
"consolidated". The two retention prunes (L4 content, L5 plan nodes) run over the whole
domain either way, so a scoped pass's report still carries prune counts from scenes it never
named. (`TestInterfaceDreamScopesConsolidationButNotRetention`,
`TestInterfaceDreamRefusesASceneThatIsGone`.)

Usually **the host does not need to call it**: once a scene's depth-1 topic count passes `Defaults.SceneDreamTopicThreshold` (default 24), `Update` schedules that scene's Dream in the background (one in flight per scene).

Runs L2→L1→L0 compression / decay / profile distillation (several LLM calls, slow) — keep it in a goroutine or between turns.
Returns a structured `*DreamReport`: `L4RecordsPruned / L5NodesPruned / ConsolidatedScenes / L2TopicsCompressed / L1NodesAdded|Removed / L1EdgesAdded|Removed / L0Updated` plus `Stages []DreamStage{Name, Status, DurationMs}` (status `ok | skipped | cancelled | error`). Three of those figures are easy to misread: `L2TopicsCompressed` counts the topics sunk into fused groups, not the number of groups; `L1NodesAdded` counts the scene nodes the sync wrote, which includes an existing node re-stamped because its topic set moved, not only newly created ones; and `L1EdgesAdded` counts the co-occurrence edges created **or strengthened** by the pass. The two removal counters span both stages that remove — the stale rebuild and the decay — and count the edges each took with it as well as the nodes. An empty report is not an error; a mid-pipeline failure returns the partial report with the error. What a host reads back is bounded by convergence, not by a cap: passing the threshold schedules that scene's Dream, and Dream only merges the groups the model judges one — topics it never picked stay at depth 1.
The first two are the retention sweep (清单 5/6 里的「删除 7 天以上」): a host cannot rebuild them afterwards —
the records are gone, and the same pass writes other records, so no before/after diff of `Stats` separates them.

`Stages` arrives in the order the pass runs them, and the names are a closed set (`dream-stage-order`):

`l4_prune` → `l5_prune` → `l2_compress` → `index_rebuild` → `l1_nodes` → `l1_hyperedges` → `l1_rebuild` → `l1_decay` → `l0_distill`

The two prunes lead so a domain with nothing to consolidate still sheds expired content and plan nodes.
`l2_compress` is the model call that fuses groups; `index_rebuild` installs the rebuilt L2Meta before any
L1 stage reads it; `l1_nodes` and `l1_hyperedges` are the sync and the co-occurrence build, `l1_rebuild`
drops stale nodes, `l1_decay` fades and prunes, and `l0_distill` writes the profile pass. A stage the
pass never reached is **absent** from the list rather than marked `skipped`: `skipped` means the stage ran
and decided to do nothing, which is a different answer from a run cancelled on the way there.
(`TestInterfaceDreamReportsEveryStageInOrder`.)

### 6.5 Driving it from a decision-loop kernel

One `.meh` file, one decision loop, one agent domain is the shape this surface is built
for. Because the library holds which scene and which turn are open, the kernel carries no
bookkeeping of its own:

| The loop | The library |
|---|---|
| decides to start a round | `Search(SearchQuery{})` — nothing goes in, the scene's next turn comes open |
| runs its arms (model call, tool call, sandbox answer) | one `AppendArchive` per fact worth keeping, on the turn now open |
| ends the round with a status word | `Update(TurnEnd{Input, Output, Outcome, CreatedAt})` — each text field is optional and writes only its own record (a scheduled or resumed round may send `Output` alone), and one that sends none is refused |
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
that path came out of a model (§5). Two stacks can also be handed the same path by mistake, and
that is the one collision the library refuses on the host's behalf: `Open` of a file already held —
by this very process included — answers `ErrIO`, the holder is untouched, and the move is to pick
another path rather than read the file as damaged (`TestOpenRefusesAFileThisProcessAlreadyHolds`).
The same choice decides what a worker knows about the project: L3 is a pool **per file**, so a worker
opened on its own path starts from an empty graph, while one created as a sub-agent domain of the parent's
file inherits that graph with nothing to re-import (`TestKnowledgeGraphStaysInsideItsFile`). Which of the
two a worker is is the host's call — the engine will not guess it.

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
  The other half of that arrangement is what makes a mid-round recall safe: **the round in
  progress is not in the scene read at all.** A turn's topic row is created by the close, not by the
  open — `Search` only mints its id — so `SceneContext("")` lists settled rounds and nothing else. A loop
  that recalls four times before finishing never feeds its own half-written round back to the model, and
  what it recorded with `AppendArchive` arrives as that turn's own lines once `Update` settles it
  (`TestOpenTurnIsAbsentUntilItSettles`).

What is left for the host is eight facts to know, not eight adapters to write:

- **A domain that has never spoken answers a recall with nothing, not with an error.**
  `SceneContext("")` on such a domain returns an empty transcript with no scene named, and it
  still writes nothing — minting a scene stays what the read that opens a turn does. That is the
  shape a decision-loop kernel needs: it reads memory *before* every model call and treats an
  error from that port as ending the whole invocation, so the alternative was every host writing
  the same "ignore that one code" special case. A scene id you *name* that is not there remains
  `ErrNotFound`, and so does a read that genuinely failed.

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
- **Folding a scene read into prompt lines is four decisions, and they are stated here once.**
  `SceneContext("")` lists a scene's topics **flattened to depth ≤ 2 on purpose**: a consolidated
  group is a depth-1 topic whose summary rides as its own utterance marked `Role: api.RoleDream`,
  and the turns that group swallowed are its depth-2 children — this read is the only one that names
  those children, and the only place their originals come back. Both scene reads come off that same
  cache, so `Search`'s `Topics` and this read's depth-1 rows are the same rows in the same order
  (`TestInterfaceSceneReadsAgreeOnTheirSharedRows`) — a loop that reads the scene twice never meets two
  answers of one scene. So: (1) drop rows with `Depth > 1`,
  those are the rows a surface row already absorbed — usually an original turn, but sometimes an earlier group that a later pass folded away (it keeps its own summary and its children still name it, while what it said went into the newer summary); sinking takes one surface row down exactly one level per pass, so this read's own depth cap cannot hide a topic from a reader — what it lists is everything the scene still holds; (2) where `ChildCount > 0`, take that
  `RoleDream` line as the memory's text; (3) otherwise take the turn's own `RoleUser`/`RoleAgent`
  lines; (4) date a memory by the row's own `UserTimestamp` — for a turn that is when its user
  message arrived, for a group the earliest turn it swallowed. Steps (1) and (2) are what
  keep one fact from reaching the model twice: render the list as it arrives and a consolidated
  group shows up as its summary *and* again as every turn it replaced. The engine renders no prose
  — that is the host's — but which row *is* which is the library's to say, and it says it on the
  row: `Depth`, `ChildCount`, `Role`, and that row's own two bounds.
  Then the case that arrives by itself after a week: **content ages out while topic rows do not**.
  The retention window sweeps utterances and events, so a surface row eventually comes back with an
  empty `Messages` list — a group that lost its summary, or a plain turn that lost both of its
  lines. That is an expired end state, not a lost record and not an answer of "nothing here": what
  survived of that memory is the `Keywords` track sitting on the same row. Render those, or the host
  silently drops every memory a consolidation ever touched. They have to come from the row — the
  flattened listing carries no parent pointer, so no reader can walk down a subtree to find them
  (`TestFusedGroupAgesIntoKeywordTracksNotSilence`). Rule (4) reads the row's own bounds for the same
  reason: once the messages are gone there is nothing else left to date it by.
  One consequence worth naming: what `Update` writes when it closes a turn — the two utterances *and*
  the `turn_outcome` event — carries that one call's timestamp, so a row that lost its prose lost its
  outcome in the same sweep. A host's "did this round finish" field therefore comes back empty for
  exactly those rows, and the honest reading is "unknown", not "unfinished". Mid-round `AppendArchive`
  events date on their own clock, so an empty `Messages` list still does not mean the turn recorded nothing.

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
`Messages[].Type`); an undefined value is refused rather than stored. The speaker is an
`api.ArchiveRole`. Dream's fused summary is the one record whose type and role the library
fixes — `text`, role 3.

---

### 7.6 The vocabularies and how they travel

A tool schema has to promise a value set, and this surface spells two of its vocabularies as
words and four as numbers. Nothing here is implicit: adding a value changes this table
(`enum-wire-table`), and `api/surface_enums_test.go` fails until both guides say it too —
the check is row by row, so a table that dropped a value or a whole vocabulary is red even
though those words still appear somewhere else in the prose. What earns a row is being an
axis the host writes or filters on: `AgentTypePrimary` / `AgentTypeSub` report which domain a
profile belongs to, the library stamps them and no call sends them, so their two numbers are
not a vocabulary this surface promises anybody.

| vocabulary | on the wire | values (word the host reads ← what JSON carries) |
|---|---|---|
| a plan step's `status` | word | `in_progress`, `done`, `failed` |
| an import's `mode` | word | `skip`, `merge`, `overwrite` |
| `ArchiveKind` (`kind`) | number | `0` = utterance, `1` = event |
| `ArchiveRole` (`role`) | number | `0` user, `1` agent, `2` system, `3` dream — the last is the library's own mark, which `AppendArchive` refuses |
| `ContentType` (`content_type`, `type`) | number | `0` text, `1` image, `2` video, `3` document, `4` audio, `5` code, `255` other |
| `GraphEdgeKind` (a relation's `kind`) | number | `0` related, `1` causal, `2` part_of, `3` sequence, `4` dependency, `5` custom |

The four numeric ones print those words through `String()` (and refuse one value past the
set rather than reading it as "unset"), so the mapping between a model's word and the number
a call carries is one small table on the host side — and it is the host's, because the engine
does not branch on any of these names beyond validation.

### 7.7 Binding these as LLM tools

One table, one row per task-face method: the key names a model should be asked for, and what
comes back to put in the prompt. The keys are the JSON names of the facade's own shapes, so
`api/surface_tool_map_test.go` fails when a struct gains a field the table does not list, or
when an admin-face method wanders in here.

| tool name (suggested) | call | keys the model fills | what comes back for the prompt |
|---|---|---|---|
| `memory_search` | `Session.Search` | `scene_id`, `l3_id`, `new_scene` | profile + its brief, the scene, its settled topics, the topic id now open |
| `memory_record` | `Session.AppendArchive` | `kind`, `seq`, `content_type`, `role`, `event_type`, `node_seq`, `created_at`, `content` | the slot the record took |
| `memory_close_turn` | `Session.Update` | `input`, `output`, `outcome`, `created_at` — **at least one of the three text fields**, all-empty is refused | the turn's topic, its keyword track among its fields |
| `memory_dream` | `Session.Dream` | `scene_id` | the `DreamReport`: stages and counts |
| `memory_profile_get` | `Session.GetL0` | none | the whole profile |
| `memory_profile_update` | `Session.UpdateL0` | `name`, `role`, `personality`, `preferences` | nothing; the next read shows it |
| `memory_associations` | `Session.ListL1` | none | this domain's scene nodes with importance and the distilled signals |
| `memory_scenes` | `Session.ListScenes` | `l3_id` | the scene list, ordered by record id |
| `memory_scene_read` | `Session.SceneContext` | `scene_id` | the conversation of one scene, flattened two levels deep |
| `memory_graph_get` | `Session.GetL3` | `id` | one graph: slot, nodes, hyperedges |
| `memory_graph_list` | `Session.ListL3` | none | the graphs this file holds |
| `memory_graph_import` | `Session.ImportL3` | `items` (`title`, `domain`, `node_type`, `content`, `keywords`, `source_ref`, `related`, each with `titles`, `kind`), `mode` | which graphs were touched, what was created/updated/skipped, per-failure lines |
| `memory_nodes_query` | `Session.QueryL3Nodes` | `graph_id`, `ids`, `keyword`, `node_type`, `limit` | the matching nodes, ordered by id |
| `memory_subgraph` | `Session.QueryL3Subgraph` | `graph_id`, `start_node_id`, `max_depth`, `edge_kinds` | the reachable neighbourhood as nodes plus edges |
| `memory_archive_search` | `Session.SearchL4` | `keyword`, `start`, `end`, `ids`, `topic_id`, `type`, `kind`, `node_seq`, `limit` | the records that matched, in the read's own order |
| `plan_add_step` | `Session.PlanNodeAdd` | `parent_seq`, `title` | the ordinal that step is addressed by from now on |
| `plan_update_step` | `Session.PlanNodeUpdate` | `seq`, `title`, `status`, `summary` | nothing; a refused update leaves the tree untouched |
| `plan_state` | `Session.PlanState` | none | the open turn's forest, with `DoneCount`/`TotalCount` summed over every step; while no turn is open it answers `ErrInvalidQuery` — that is "nothing in progress", not a storage failure |

The table is checked against the shapes rather than trusted: `TestToolArgumentsDecodeIntoEveryInputShape`
decodes each tool's argument object straight into the input struct and requires every key to land — a
model's JSON becomes a call with no translation layer in between, and a renamed or missing key fails here
instead of in production.

Two of those keys are **the host's, even when the call is model-initiated** (`host-filled-keys`):
`created_at` and `seq`. A model asked for a millisecond instant will sometimes answer with
seconds or with nothing, and the library refuses both scales at the write boundary rather than
guessing a clock — so a host stamps these before the call and leaves `seq` at 0 to mean "the
next free slot". Everything else in the table a model may legitimately fill in.

Two things a host should not have to rediscover: the keys of a struct argument are its JSON
names (one scheme for the whole surface, and the enums' own values follow §7.6), and the eight
admin-face methods (`UpdateScene`, `RenameTopic`, `MergeScenes`, `DeleteScene`, `DeleteTopic`,
`UpdateL3`, `DeleteL3`, `AgentID`) are deliberately absent — a model that can destroy records
needs the host's approval path, not a tool schema.

## 8. Layer API quick reference

The 26 session methods split by audience:

- **Runtime/task face (18)** — the host drives these every turn and LLM tools bind to them: `Search` / `AppendArchive` / `Update` / `Dream` (the host-driven loop), `GetL0` / `UpdateL0`, `ListL1`, `ListScenes` / `SceneContext`, `GetL3` / `ListL3` / `ImportL3` / `QueryL3Nodes` / `QueryL3Subgraph`, `SearchL4`, `PlanNodeAdd` / `PlanNodeUpdate` / `PlanState`.
- **Assembly/admin face (8)** — host code at session boundaries and management channels only, never an LLM tool: `UpdateScene` / `RenameTopic` / `MergeScenes` / `DeleteScene` / `DeleteTopic`, `UpdateL3` / `DeleteL3`, `AgentID`.

The file-level lifecycle and diagnostics sit on `api.DB` instead (9): `Primary` / `SubAgent` / `Agent` / `Agents`, then `Checkpoint` / `CompactTo` / `Close` / `IsClosed` / `Stats` (file size plus reachable record count across the file — the numbers a compaction decision is made from). There is no capability surface anywhere: the engine neither stores nor parses cards, so a host reads the events of a turn with `SearchL4{Kind: event}` and organizes them itself.

### L0 profile

```go
prof, err := db.GetL0()                       // *api.ProfileSlot — the full record
err = db.UpdateL0(api.ProfileInput{Name: "..."})   // by value, like SubAgent
```

`UpdateL0` takes a `ProfileInput` **by value**, the way `SubAgent` takes it: a write
always names a profile, so there is no absent-profile state for a pointer to carry — only
`Open` takes `*ProfileInput`, because "no profile" there means "do not seed an existing
file". The type itself holds exactly the four fields a host
owns — `Name`, `Role`, `Personality`, `Preferences`. The rest of the stored
profile is not in that shape because it is not the host's to state: `EmotionState`
and `MBTI` are evolved by Dream, `UpdatedAtMs` is stamped by the library, and
`AgentType` is decided when the domain is created. A write inherits those two
distilled signals and `AgentType` from the record and stamps `UpdatedAtMs` itself,
so there is no need to `GetL0` and fill values back, and no read-only field can be
smuggled in — the compiler refuses. The distilled half refreshes
automatically with Dream: there is no standalone distill entry point.

`Name` is required, and on a **sub-agent** domain it is not a field this call can move: the name a
domain was created with is the tenant key `SubAgent` opens it by (held in the file's registry), while
`UpdateL0` reaches only the profile's own copy. Writing another spelling there would move one copy and
not the other — the old name still opening this memory, the new name opening a fresh empty domain
beside it, and `Agents()` calling the domain something its profile no longer says. That write is
therefore refused (`ErrInvalidQuery`) and nothing lands. The file's own primary domain is addressed by
`Primary()` and by no name, so its label stays free text that the roster follows
(`TestInterfaceSubAgentNameIsItsHandle` pins both halves).

One value on this surface exists in a Go read but not in a JSON encoding: the type word
`MBTI.type`. It is derived from the four axes on every read and never persisted (`mbti-hidden-derivation`),
so the axes stay the only fact on disk and the word can never drift from them. Put a profile into a
prompt through `ProfileBrief`, whose `mbti: ESFP` line is already rendered — do not re-derive the word
from the axes, and do not expect a marshalled `ProfileSlot` to carry it.

`Personality` is the one field with two writers, and the one a write does *not*
inherit: Dream's distillation replaces it with the personality summary the model
derived from this domain's memories, so it reads back as whichever of the two ran
last. An `UpdateL0` that leaves it empty therefore clears the distilled summary,
and the next pass evolves it again — carry the value back from `GetL0` if you mean
to keep it. `Name`, `Role` and `Preferences` have the host as their only writer — and that half is written
**whole, not merged**: naming one preference drops the rest, omitting `Role` clears it, and a nil
table means the same as an empty one. That is the price of being able to delete anything at all —
a per-key merge would leave a preference with no way to go away — so changing one entry means
reading the profile, editing the map, and writing it back (`TestUpdateL0WritesTheHostHalfWhole`
pins the replacement and the read-merge-write path). The distilled half is inherited either way,
so no host write can fade it.

### L1 scene associations (what Dream thinks relates to what)

```go
nodes, err := db.ListL1()   // []api.SceneNodeView — read-only: there is no write surface
```

`ListL1` is the only access, and it is read-only: **Dream is the single writer** of this layer.
A node exists per scene that has settled turns; its `importance` starts at `1.0` and only falls,
and the edges between nodes are built from the keyword tracks of the turns those scenes hold.
The table is that shape's key list, field for field (`l1-node-fields`):

| Field | What it answers |
|---|---|
| `id` / `scene_id` | the node and the scene it stands for; one node per scene |
| `topic_ids` | which topics the **last Dream sync** found under the scene — a snapshot, not a live listing: a topic deleted since still appears until the next pass rebuilds it |
| `edge_ids` | the co-occurrence edges this node sits on. There is **no read for an edge itself**: the only thing an id tells you is that two nodes sharing one were judged related by Dream |
| `importance` | how strong the trace still is, in `(0,1]`, decaying since the last time the memory mattered |
| `valence` / `arousal` | the emotional reading Dream distilled onto it, each in `[0,1]` — **0 is a reading** ("extremely negative" / "completely calm"), not "unmeasured"; the neutral point is `0.5` and distance from it is the strength. These are two of the three axes a profile carries: the node record has no dominance, so a node's emotion is not a smaller `ProfileSlot.EmotionState` |
| `emotion_set` | whether any pass ever stamped those two, which is the only way to tell a distilled `(0,0)` from a never-measured node |
| `created_at` / `updated_at` | milliseconds. `updated_at` is the **decay clock**: it moves when the scene's topic set changes, when a pass fades the node, and when a distillation stamps values that actually differ — so it answers "when did this memory last matter", not "when was it written" |

What a host can and cannot do here: nothing is writable (no L0-style input shape exists for it),
no edge or node can be deleted by hand, and forgetting happens only inside `Dream` — a domain that
stops being written to still needs a pass to shrink. A node whose scene is deleted goes with it
(`TestDeleteSceneLeavesNoOrphansInReadableLayers`), an undreamed domain answers `[]` rather than
nothing (`TestListL1OnAnUndreamedDomainIsEmptyNotNil`), the list comes back in id order on every
call (`TestListL1SortsByIDHash`), and a node that exists but will not read back fails the call
instead of being skipped (`TestListL1ReportsUnreadableNode`). Fading is measured in wall-clock
hours and composes across passes, so two short intervals and one long one arrive at the same
importance (`TestNodeDecayComposesAcrossPasses`). The whole of it is walked on the host's own
surface, no build tag and no quota: empty before the first `Dream`, one node per settled
conversation, an edge shared between two conversations that repeat the same keywords, and a
deleted topic still listed until the next pass rebuilds the snapshot
(`TestInterfaceDreamBuildsTheAssociationLayer`).

### L2 scenes

| Method | Meaning |
|---|---|
| `db.ListScenes(l3ID) ([]SceneSlot, error)` | scene list (`SceneID / SceneName / L3ID`); a non-empty `l3ID` keeps only the scenes anchored to that project domain, `""` lists all. The order is record-id ascending, so the same file answers the same listing on every call — it is a repeatable answer, not a relevance ranking |
| `db.SceneContext(sceneID) (*SceneContext, error)` | the scene's whole transcript (topics + their L4 originals) and **no write at all** — no turn is opened; **use for session resume**, and for every recall a round makes after the one that opened its turn. An empty `sceneID` reads the scene this domain is working, so nothing has to be held to call it again. Unlike `Search` it flattens to depth 2, because a Dream-fused group keeps its originals on the children it sank, and this is the only read that brings them back. Each row also carries its own `UserTimestamp`/`AgentTimestamp` — the bounds of that turn, or of the group it belongs to — which is the only date left once retention has swept its messages. `ChildCount > 0` is what marks a fused group (its own single message carries role 3, the mark Dream puts on a fused group's summary); `Depth` only says whether the topic is still on the scene's surface — a group a later pass folded away sits at 2 level with the turns it summarizes. Rows arrive in a fixed order — `UserTimestamp`, then shallower-first (a fused group carries the timestamp of the first turn it swallowed, so ties are the normal case), then the topic id — so a host reads the listing linearly and never re-sorts it. The entries returned are the whole count — roots and the sunk children this read alone brings back|
| `db.UpdateScene(sceneID, api.ScenePatch{Name, L3ID, Force}) (SceneSlot, error)` | title it (`Name`), anchor it to an L3 project domain (`L3ID`), or clear the anchor (`L3ID: &""`); nil fields keep their stored value, and the **written scene comes back**. Moving a scene that already has one to a **different** domain needs `Force: true` — without it the call is refused (`ErrInvalidQuery`) rather than quietly losing the old anchor; clearing needs no Force because it is reversible. A patch that changes nothing — an empty one, or values the scene already holds — **appends nothing**: the file is append-only, so the confirm-without-listing use this call documents would otherwise cost bytes per look, and a retried patch per retry |
| `db.RenameTopic(topicID, name) (TopicSlot, error)` | the one host-authored label a topic carries: the engine derives nothing into it, so consolidating or merging rewrites the record around the name. Visible to the next `Search` / `SceneContext` immediately, not at the next consolidation. An empty name is refused — topics are born unnamed, so `""` is the absence of a label, not one — an unknown topic is `ErrNotFound` and nothing is created for it, and renaming to the name it already carries writes nothing |
| `db.MergeScenes(primaryID, []secondaryIDs) error` | fold conversations into one: each secondary's topics are retargeted under the primary and its scene record goes. **The survivor keeps its own title**, and an L3 anchor is carried rather than lost: a survivor that
names no project domain takes over the anchor the swallowed scenes had (so the merged conversation
stays listed under that domain), a survivor that names one keeps its own claim, and when the swallowed
scenes disagree about the domain the call is refused (`ErrInvalidQuery`) with nothing destroyed. A
label a host wrote on a turn survives the retarget. Each swallowed scene's L1 node is dropped on the spot, and the survivor's node is rebuilt over the turns it gained by the next `Dream`; the domain is moved onto the primary, so a turn of a swallowed scene can no longer be closed (`TestInterfaceMemoryDeletionTakesItsNodeWithIt` pins the node and label halves) |
| `db.DeleteTopic(topicID) error` | delete a topic subtree + its L4 archives + its L5 plan tree + indexes; the subtree is the topics whose `parent_id` points into it, so deleting a fused group takes every turn it summarised, and deleting a turn takes the tree keyed on it with it (`TestInterfaceDeleteTopicTakesThePlanTreeToo`, `TestInterfaceDeleteTopicTakesAFusedGroupsSubtree`) (memory correction) |
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
members + kind). What is deduped is that set, not the phrasing: the same fact
restated from another member, with its titles in another order, and again after a
restart, is a no-op — neither a second edge nor an error
(`TestInterfaceRelationIdentityIgnoresOrderAndAnchor`). Unresolvable / self /
invalid-kind entries land in `Errors`.

`GraphIDs` is what closes the loop: a graph id is `hash(Domain)` and no other
public call renders that derivation, so `ImportL3` reports it directly —
`SearchQuery.L3ID` / `UpdateScene` need that id.

**Copying a graph into another file.** The pool is per file, so a worker that opens its own
`.meh` starts with no project knowledge, and the copy is all public surface: `GetL3` hands back
the nodes (with their titles) and the hyperedges (as member ids), so a host writes one
`L3ImportItem` per node and hangs each edge under its lowest member as that item's `Related` —
a hyperedge is an unordered set, so neither which member anchors it nor the order its titles arrive in loses anything. Two facts are worth
writing down: a graph's id derives from its label, so importing under the same `Domain` lands
the copy on the **same id** in the other file; and `SourceRef` is a plain string on both sides
(empty means "no positional reference", which is exactly what the encoded answer leaves out), so
what a read returns can be written back without dereferencing anything.
`TestInterfaceGraphCopyBetweenLibraries` is that recipe with assertions — every node field, the
edge sets keyed by kind plus sorted titles, and a replay of the same copy still at three edges.

`GetL3` / `ListL3` / `QueryL3Nodes` / `QueryL3Subgraph` / `UpdateL3` / `DeleteL3`.

**Renaming, and what a rename does not move.** `UpdateL3(id, label)` changes a graph's label
and answers with the whole graph; an empty label is refused rather than erasing the one that
addresses it. The new label has to be free — a domain label is how `ImportL3` routes a batch, so
renaming onto a label another graph carries is `ErrInvalidQuery` instead of an ambiguous domain.
Renaming onto the label the graph already carries **writes nothing**: it succeeds and leaves the
graph's `UpdatedAt` where it was, because that clock answers "when did this graph's content
change" and a no-op has no answer to give — which is also what makes a replayed rename converge
instead of making an untouched graph look freshly edited. What never
moves is the **id**: from the rename on it is no longer `hash(label)`, so a scene's anchor and
every read argument keep the id `ImportL3` / `ListL3` handed over — the library never asks a
host to derive one. Both labels still route to that graph while the rename stands (the new one
by the label on its record, the label it was created under by the id derived from it), so
re-importing under either extends it instead of starting a twin
(`TestUpdateL3RenameSurvivesReimport`, and at the host face `TestInterfaceGraphRenameKeepsItsIdAndBothLabelsRoute`). Updating a *node* is the same import path: a node is
addressed by graph plus title, so re-import that title with `merge` or `overwrite`.

Deletion has one granularity: the whole graph. `QueryL3Subgraph`'s `edgeKinds`
narrows the walk to the kinds named and leaves the condition out when the list is
empty; a kind outside the six constants is refused with `ErrInvalidQuery`, because
the write boundary refuses to store one and an empty subgraph is the answer a host
reads back as "this graph holds no such edges".

`QueryL3Subgraph`'s `max_depth` counts hops from the start node, and a non-positive
value sets no bound — the walk answers with the whole reachable component. That is the
same reading every `limit` on this surface carries (`L3NodeQuery` and `L4Query` include
`limit: 0` = no cap), so a tool schema writes one rule for zero, not two. Cyclic edges are
safe: a node is visited once, so the walk stops at the component rather than spinning. The result is self-describing at every bound: an edge is reported only when
all of its members are inside the neighbourhood the walk reached, so a host never gets a relation whose
end it cannot name without a second call (a hyperedge crossing the bound is left out whole).

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
    // Start: t0, End: t1,       // created within [t0, t1] (ms); 0 leaves that bound unset
    // IDs: []string{...},       // by archive id (one id = one record)
    // TopicID: &topicHex,       // only this topic's archives
    // NodeSeq: 2,               // only records attributed to this step or any step under it
    //                             // (needs TopicID: a step is addressed inside a turn)
    // Type: &api.ContentImage,  // only this content type
    // Limit: 50,                // keep the tail of the read's order (<=0: every match)
})
```

`Start` and `End` are milliseconds, and they are checked with the same ruler as a write's
`CreatedAt`: a seconds-scale or microsecond-scale bound is refused (`ErrInvalidQuery`),
because such a bound is never the window it names — as `Start`, seconds sit below every
stamp and let everything through; as `End`, they exclude everything. A refusal is a worse
looking answer than a wrong row set and a much better one for a host.

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
(`0` = a root). An ordinal is issued above everything that still names one — the surviving
steps and this turn's surviving events bound to a step — so a turn's numbering can carry a gap:
an ordinal an event still speaks of is never handed to a new step (the two age separately, and a
step swept by the retention window can leave an event naming it). There is no plan id and no path
string to mint, the plan calls name no topic id (they act on the turn `Search` opened), and
`PlanState()` is how a tree comes back, and it reads the turn `Search` opened: with no turn open it
refuses with `ErrInvalidQuery`, which a caller should read as an empty answer rather than a
broken library.

| Call | Meaning |
|---|---|
| `seq, err := db.PlanNodeAdd(0, title)` | open this turn's tree by creating its first root step, and take back the ordinal that step is addressed by from here on. A turn's tree starts with no steps, so this is also how a plan first appears under a turn; there is no separate "create the tree" call |
| `seq, err := db.PlanNodeAdd(parentSeq, title)` | add one step to the tree and get its ordinal. `parentSeq` `0` hangs it at the top level, so this is also how a second root joins the forest; any other value must name a step this tree already holds — `PlanNodeAdd` under an unknown parent is `ErrNotFound` and grows nothing. A step is created `in_progress`, so no status is asked for here; a title may be left empty and filled in later, and the view falls back to the ordinal until the host names the step |
| `err := db.PlanNodeUpdate(api.PlanStep{Seq: seq, Status: api.PlanStatusDone, Summary: s})` | restate one step: its `Status` plus the node's own `Title`/`Summary`. `Status` is stated every time (there is no "leave it as it was" spelling) while a blank `Title`/`Summary` keeps what the node holds, so updating a step never rewinds its title. A parent's folded `Summary` is the one text the engine owns and re-derives: once every direct child of a Done parent is terminal it is folded from theirs, and it keeps following the branch — a step planned under a parent that had already closed, or a settled child finished with different words, re-folds it. State a `Summary` on the parent yourself and it becomes your text: no rollup rewrites it. A step reaching a terminal status records `FinishedAt`; restating a settled step as `in_progress` re-opens it and drops that timestamp. Once every direct child of a parent whose status is done reaches a terminal status itself, that parent's summary folds up from its children's. A status outside `in_progress` / `done` / `failed`, or an ordinal this turn never created (`ErrNotFound`), is refused **before the node is touched** and leaves the tree exactly as it was. This call writes no content |
| `tree, err := db.PlanState()` | read the forest view (`PlanTree.Roots` + `DoneCount` / `TotalCount`, which are summed over every step of every tree; every `PlanNodeView` carries `Seq` / `ParentSeq` / `Status` / `Summary` / `Children`) — also the restart recovery path |
| `db.AppendArchive(ev)` with a non-zero `ev.NodeSeq` | record a step event against one step of this turn's tree. The step has to exist already: an ordinal nobody created refuses the whole record (`ErrInvalidQuery`) and stores nothing, because an event naming a step the plan never holds is the plan and the record disagreeing — the tree is `PlanNodeAdd`'s to build, and a mistyped ordinal cannot quietly open a second one. `EventType` is **the host's own name for the step**, on this path exactly as on a bare turn event — the engine never branches on it (the name comes back verbatim through `SearchL4`) and only refuses an empty one. Convention names for readers: `plan_step`, `llm_request`, `llm_output`, `tool_call`, `tool_result`, `subagent_spawn`, `subagent_done`, `context_inject`, `ask_user`, `user_reply` |

Status has three values and one string encoding each: `api.PlanStatusInProgress`
(`in_progress`), `api.PlanStatusDone` (`done`), `api.PlanStatusFailed` (`failed`). The
engine keeps no "planned but not started" state — a step exists because the host created
it, and it exists in progress.

**What one plan context costs, measured.** Two reads carry a turn's whole plan into a
prompt: `PlanState()` for the tree, and one `SearchL4{TopicID, Kind: &event}` for the
step-bound events (`NodeSeq` on each row is the attribution). Against the offline stub on an
Apple M2 (`go test ./test/ -bench BenchmarkEngine -benchtime=100x`), that pair is
**≈97–99 µs**, and one answer for a 21-step tree with an event per step is **8 268 bytes** of
JSON — about 0.4 KB per step, counted on the wire format, not on the text a host renders from
it. The engine truncates nothing: `PlanState` returns the whole forest, and the only
compression it performs is the semantic one — a parent's summary folding once all of its
direct children reach a terminal status (and re-folds when that branch changes underneath it, while a summary the host wrote itself is left alone), and a step reading as itself plus its subtree. The
token budget therefore stays the host's, exactly as on the recall path, and a budget-shaped
read is not added before a host is shown needing one.

The plan write surface sits on `api.Session`'s 18 task-face methods: a tree is built on
the turn `Search` opened for the domain, so these calls name no topic id.

The all-zero key `0000000000000000` is reserved (it is the value a record leaves its key
unset with) — `Search` never opens a turn on it, and the read entries that do take a
topic id (`SearchL4{TopicID}`, `RenameTopic`, `DeleteTopic`) reject it, and a `SearchL4{IDs}` list
that carries it is refused rather than answered one row short. `SearchL4{TopicID}` also refuses a **scene** id:
the scene key and the turn key come out of the same `Search` result, and reading one where the other belongs
would answer empty — which a host reads as a round that recorded nothing. An id naming no record at all is still
an empty answer, because a turn that is still open has content before it has a topic record.

---

## 9. Exported types (v1.6.6)

| Kind | Names | Use |
|---|---|---|
| entry & handles | **`Open`** → `*DB`, then `DB.Primary()` / `DB.SubAgent(llm, profile)` / `DB.Agent(llm, id)` → `*Session` | three ways in; a domain is a handle, and `Session.AgentID` is the id `DB.Agent` takes back |
| config | **`LlmConfig`** / `MemHopDefaults` + `DefaultMemHopDefaults` | the endpoint and tuning arguments `Open` takes |
| input shapes | **`ProfileInput`** / `SearchQuery` / `TurnEnd` / `ScenePatch` / `L3ImportItem` / `L3Relation` / `L3ImportMode` / `L3NodeQuery` / `L4Query` / `PlanStep` / `ArchiveInput` (the L4 write shape; a read returns `ArchiveSlot`) | inputs; `ProfileInput` is the only profile a host may write, and of its four fields only `Name` is required |
| response DTOs | `AgentInfo` / `ProfileSlot` / `SceneNodeView` / `SceneSlot` / `TopicSlot` / `SceneContext` / `SceneContextTopic` / `SceneMessage` / `SearchResult` / `DreamReport` + `DreamStage` / `HypergraphSlot` / `HypergraphNode` / `HypergraphEdge` / `L3Graph` / `L3Subgraph` / `L3ImportResult` / `PlanTree` / `PlanNodeView` / `DreamReport` / `DreamStage` | every id field is a 16-char hex string, and every one of them was issued by the library |
| enums | `GraphEdgeKind` / `ContentType` / `ArchiveKind` / `ArchiveRole` / `PlanStatus` / `AgentTypePrimary` + `AgentTypeSub` | the vocabulary a call is written in |
| file diagnostics | **`DBStats`** (`FileBytes` / `RecordCount`) | what `DB.Stats()` answers. Not two views of one number: `FileBytes` is space, `RecordCount` is the live set that must survive a rewrite, and the two are never subtracted from each other. What a `CompactTo` gives back is `FileBytes` before against `FileBytes` after — measured 19 700 → 17 827 bytes on the same 47 live records — and the rewrite is a fixed point: run it again on a file with nothing dead left and it comes out no larger (18 830 → 18 680), which `TestInterfaceCompactedCopyAnswersIdentically` holds on both sides |
| errors | `Code` + the `Err*` constants, read with `CodeOf(err)` | the numeric code behind an error string |

Enum constants are exported too: `L3ImportSkip` / `L3ImportMerge` / `L3ImportOverwrite`,
`EdgeRelated`…`EdgeCustom`, `ContentText`…`ContentOther`, `KindUtterance` /
`KindEvent`, `RoleUser` / `RoleAgent` / `RoleSystem` / `RoleDream`,
`PlanStatusInProgress` / `PlanStatusDone` / `PlanStatusFailed`.

Nothing in this list converts an id: there is no `FormatID` / `ParseID` pair and no
numeric id in any signature, because a host echoes back the hex strings it was given
and builds none itself. There is no capability type either — the card format, its
file layout and its activation are the host's own assets.


> A record's kind is `api.KindUtterance` / `api.KindEvent`. The speaker of an utterance is
> `api.ArchiveRole`, and the three a host may declare on an appended one are
> `api.RoleUser` / `RoleAgent` / `RoleSystem`. The fourth value, `api.RoleDream`, is the
> library's own mark on a consolidated summary: naming it is how a host tells that summary
> apart from a turn's own two lines when it renders a prompt, and `AppendArchive` still
> refuses it, so a host cannot write a record that reads as consolidated. A name is not a
> write grant — the boundary is. `role` is that named type on every shape carrying it
> (`ArchiveInput`, `ArchiveSlot`, `SceneMessage`), so a constant lands with no conversion,
> and `String()` gives the word a tool schema promises the model.
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
and `ErrLLM`. `ErrAgentNotFound` is what `DB.Agent` answers for an id this file never
registered - the one place a host hands the library an agent id, and it refuses rather
than opening an empty domain. `api.NewError(code, message)` builds one of these errors for a caller
that refuses on its own terms — a tool layer turning down an argument outside a
vocabulary, say — so that refusal carries the same code the library's own refusals do.
Numbers are never reused: `1002` and `9001` are retired and will not be reissued.

---

## 11. Minimal runnable skeleton

The loop below is also run inside this repository, round by round against a fake model with no quota and
no network: `TestInterfaceRoundFlowRunsEndToEnd` (plan a round, record each step's work against
that step, close the round, spawn a second domain mid-loop, run a second round), and
`TestInterfaceMemoryPortServesSeveralAgentsOnOneFileAndOneFileEach` for the shape §11.1 describes — a worker on a second `.meh`, with
a second process refused by the file's own lock. So a clone can check the sequence without
building the two other projects.

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "strings"
    "time"

    "github.com/qyiun666/MemHop/api"
)

func main() {
    llm := api.LlmConfig{
        APIURL: os.Getenv("LLM_URL"),
        APIKey: os.Getenv("LLM_KEY"),
        Model:  os.Getenv("LLM_MODEL"),
    }
    lib, err := api.Open(
        os.Getenv("MEH_PATH"), // /data/agent.meh
        llm,
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

    // One identifier per memory, if the host wants one: the library issues each domain's
    // id, DB.Agents lists the domains this file holds, and DB.Agent opens one again by that
    // id (an id is scoped to its file - every file's primary is the same zero id).
    roster, err := lib.Agents()
    if err != nil { log.Fatal(err) }
    _ = roster // each AgentInfo: ID (round-trip only), Name (what SubAgent takes), Primary
    again, err := lib.Agent(llm, db.AgentID())
    if err != nil { log.Fatal(err) }
    _ = again // the same domain, now on this endpoint

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

    // Per loop iteration, before asking the model again: the plan back as context.
    // PlanState reads the turn the library holds open and hands back the whole
    // forest — each parent's Summary already folded from its children once they all
    // reach a terminal status, plus the two rollups no single row can give you.
    // The engine renders nothing: turning this into the lines a model reads is the
    // host's own format, and it is what the next call's prompt carries.
    tree, err := db.PlanState()
    if err != nil {
        log.Fatal(err)
    }
    var planText strings.Builder
    fmt.Fprintf(&planText, "plan: %d/%d steps done\n", tree.DoneCount, tree.TotalCount)
    var steps func(nodes []api.PlanNodeView, depth int)
    steps = func(nodes []api.PlanNodeView, depth int) {
        for _, n := range nodes {
            fmt.Fprintf(&planText, "%s- #%d %s [%s]\n",
                strings.Repeat("  ", depth), n.Seq, n.Title, n.Status)
            steps(n.Children, depth+1)
        }
    }
    steps(tree.Roots, 0)
    promptContext := res.ProfileBrief + "\n" + planText.String()
    fmt.Println(promptContext) // the block this turn sends ahead of the model's next call

    // Per turn: end — Update writes the turn's input and output onto the dialogue
    // slots, records the outcome as one event, and distills the turn into its keywords.
    if _, err := db.Update(api.TurnEnd{Input: "user raw message", Output: "agent reply",
        CreatedAt: time.Now().UnixMilli()}); err != nil { log.Fatal(err) }

    // Idle / scheduled (usually unnecessary: Update schedules consolidation
    // once a scene's topic count passes the threshold).
    if _, err := db.Dream(context.Background(), ""); err != nil {
        log.Fatal(err)
    }

    // The model asked for a worker: the host's answer is one more Open, on a path of its own
    // choosing. A second domain in THIS file would share its L3 graph; a second file shares
    // nothing, so the choice is about what the two agents are allowed to know in common.
    workerLib, err := api.Open(
        strings.TrimSuffix(os.Getenv("MEH_PATH"), ".meh") + ".worker.meh",
        llm, api.DefaultMemHopDefaults,
        &api.ProfileInput{Name: "researcher", Role: "sub-agent"})
    if err != nil { log.Fatal(err) }
    defer workerLib.Close()
    helper, err := workerLib.Primary()
    if err != nil { log.Fatal(err) }

    // The same four calls, on the other handle: the loop is not agent-shaped.
    if _, err := helper.Search(api.SearchQuery{}); err != nil { log.Fatal(err) }
    if _, err := helper.AppendArchive(api.ArchiveInput{Kind: api.KindEvent,
        ContentType: api.ContentText, EventType: "tool_call",
        Content: "delegated work", CreatedAt: time.Now().UnixMilli()}); err != nil { log.Fatal(err) }
    if _, err := helper.Update(api.TurnEnd{Input: "what is in this project",
        Output: "a memory engine", Outcome: "answered",
        CreatedAt: time.Now().UnixMilli()}); err != nil { log.Fatal(err) }

    // Each memory answers for its own agent, and neither listing carries the other's turn.
    for _, lib := range []*api.DB{lib, workerLib} {
        session, err := lib.Primary()
        if err != nil { log.Fatal(err) }
        ctx, err := session.SceneContext("")
        if err != nil { log.Fatal(err) }
        fmt.Printf("%s holds %d turn(s)\n", session.AgentID(), len(ctx.Topics))
    }
}
```

---


### 11.1 A second agent, on a memory of its own

When the model decides to delegate, the host's answer to that tool call is one more `api.Open`:
a worker on its own `.meh`, opened exactly like the main one. Nothing in the loop above changes
for it — the same four calls drive either handle, so a host carries no per-agent type, no id
table and no second code path (the skeleton in §11 ends by doing this). What differs is only
isolation. A second **domain inside one file** shares that file's L3 knowledge graph — one
project's facts, read by every agent in that file — while keeping scenes, originals and profiles
apart; a second **file** shares nothing at all, not even the graph, and costs one more exclusive
lock. Choose by whether the two agents are supposed to know the same project.

Two mechanical rules a host meets the first time it moves a file: a live `.meh` holds its
exclusive lock until the handle is closed (close before renaming, compacting away, or deleting),
and a path that already exists is reopened rather than recreated — `api.Open` on an existing
file keeps every domain it holds, and the profile argument is consulted only when the file is
not there yet.

### The same shape behind a framework's memory port

An agent framework usually owns its own two-method port — open a round per invocation, recall before
every model call, hand the invocation's facts back once — and the integrator writes the adapter.
`TestInterfaceMemoryPortAdapterHoldsNoIds` is that adapter, executable in this repository: its whole
state is one `*Session` handle, `begin()` is the `Search` that opens the round and keeps the
`ProfileBrief` that read hands back, `recall()` is pure (`SceneContext` plus `PlanState`, never a
second `Search` — four recalls still settle one round), and `remember()` is one `Update` carrying
the framework's own outcome word. Two claims it pins are easy to write wrong from the reference
alone: `PlanState` answers `ErrInvalidQuery` while no turn is open, which means "nothing in
progress" and must not abort the invocation; and an over-budget write comes back as an error with
the round still open, so the retry lands on that round instead of minting a new one.
`TestInterfaceMemoryPortServesSeveralAgentsOnOneFileAndOneFileEach` extends the same recipe to the shape a
running host has several agents in: a sub-agent is a domain of the same handle (its adapter still
holds one `*Session`, and its recall never shows the parent's rows), a task spawned per job gets a
file of its own, and reading what that worker left behind means reopening the path — a second
instance is refused with `ErrIO` saying "another instance", which is the pair a host retries on,
since `ErrIO` alone cannot tell a busy file from a broken disk.

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
   a turn's topic is stamped with the earliest and latest timestamps among its **utterances** — an event recorded mid-round does not widen those bounds.
   Two clocks, one rule: the content you append and the round you close carry **your** millisecond stamp, and the library never
   substitutes one for a missing `CreatedAt` — that value is what the retention window measures, so guessing it on a caller's
   behalf would silently expire or immortalise someone's transcript. What the library derives instead (a plan step's own
   timestamps, a profile's `UpdatedAtMs`, a graph's content clock) it stamps itself, on the same millisecond scale.
4. **IDs are opaque 16-hex strings**: never splice/truncate them; response ids
   feed back as-is; the facade exposes no hex ⇄ integer bridge.
5. **`Search` writes no memory content**: it opens one turn (advancing the
   scene's turn counter) and creates no topic record, so a round that never
   settles stays out of the scene read. That is not the same as leaving nothing
   behind: whatever the round already appended survives under that turn's id —
   which no read names once the domain moves on — until the retention window
   sweeps it. Closing with `Update` is what makes a round's content reachable, so
   abandoning a round that recorded something costs space, not nothing.
   To read originals use `SceneContext` /
   `SearchL4`.
   Replaying an append to the same slot is idempotent: the record
   hashes from the open turn and its `Seq`, so a retry rewrites it instead of duplicating — and a
   slot the replay stops filling is not reclaimed.
6. **One file, many agent domains**: all tenants live inside one `.meh` file —
   `api.Open` settles the domain the file was opened on, `DB.SubAgent(llm,
   profile)` creates or returns one under it by name, and `DB.Agent(llm, id)` returns the
   same domain by the id `Session.AgentID` issued (an id is scoped to its own file, since
   every file's primary is the zero one) — fully isolated per domain
   except the file-wide L3 pool; legacy files (`FormatVersion < 0x0012`) cannot be
   opened or migrated.
7. **Content and plans auto-expire**: Dream drops a topic's content past the
   retention window (seven days by default, configurable via
   `Defaults.ContentRetentionMs`) and plan nodes past it. A tree is exempt from
   that sweep only while it is BOTH still in flight and was acted on inside the
   window - an abandoned in-flight tree sweeps like anything else, which is exactly
   what keeps L5 bounded; inside a tree that is not exempt, every step is measured
   on its own clock, so a plan one late update touched loses only its stale steps;
   `DeleteTopic` / `DeleteScene` are the explicit corrections. Past the window a
   topic keeps its keyword track and its `Messages` come back **empty**: one `Update` stamps a
   turn's two originals together, so they age as a pair. Gaps in `Seq` are the shape of the
   *event* track (`SearchL4{TopicID, Kind: event}`), where every record carries its own moment
   and a survivor keeps the number it was written at — nothing is ever renumbered, on either
   read (`TestSweepKeepsEverySurvivorAtItsOwnSeq`); a host that addressed an utterance slot of
   its own can see a gap in `Messages` too. Either outcome is a legal end state, not a failed read. A fused group's summary ages from
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
9. **A knob left at 0 is a knob you did not fill**: `MemHopDefaults` is read that way
    everywhere, exactly as `LlmConfig`'s two budgets already were, so a partial literal
    cannot silently switch automatic consolidation off any more. Turning a knob off takes
    the explicit negative spelling — the retention window being the one exception, where a negative is refused at
    `Open` rather than read as "off" — and for `DreamCompressMinTopics` a negative is the
    *lossy* direction, since that number is what the consolidation prompt asks the scene
    to converge toward. Context size is held in check only by Dream converging each scene
    towards `DreamCompressMinTopics` (default 20) — a target, not a ceiling a scene is kept
    under — so disabling automatic consolidation lets the injected context grow without
    limit.
