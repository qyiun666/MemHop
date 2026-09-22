// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Turn-keyed surface: one key's content writes and event read-back, its plan tree,
// and the retention window that empties it.

package api

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
)

// event builds one turn event as a host appends it: the kind is what makes it a
// record of what happened rather than a line of dialogue.
func event(eventType, payload string, ts int64) ArchiveInput {
	return ArchiveInput{Kind: KindEvent, EventType: eventType, Content: payload, CreatedAt: ts}
}

// onStep names the plan step an event belongs to.
func onStep(slot ArchiveInput, seq uint32) ArchiveInput {
	slot.NodeSeq = seq
	return slot
}

// eventsOf reads one topic's event track the only way the public surface allows: the
// same key with the Kind condition, in Seq order.
func eventsOf(t *testing.T, db *Session, topicID string) []ArchiveSlot {
	t.Helper()
	kind := KindEvent
	out, err := db.SearchL4(L4Query{TopicID: &topicID, Kind: &kind})
	if err != nil {
		t.Fatalf("read events of %s: %v", topicID, err)
	}
	return out
}

// utterancesOf reads one topic's dialogue back as the texts it holds, in Seq order —
// which is the order a closing call wrote them in, so a turn's question precedes its
// answer here.
func utterancesOf(t *testing.T, db *Session, topicID string) []string {
	t.Helper()
	kind := KindUtterance
	out, err := db.SearchL4(L4Query{TopicID: &topicID, Kind: &kind})
	if err != nil {
		t.Fatalf("read utterances of %s: %v", topicID, err)
	}
	texts := make([]string, len(out))
	for i, slot := range out {
		texts[i] = slot.Content
	}
	return texts
}

// Dream drops events past the 7-day retention window even when there is nothing
// to consolidate, and no delete API is exposed: a turn keeps the event still
// inside the window and loses the one outside it.
func TestSurfaceDreamPrunesExpiredEvents(t *testing.T) {
	db := openSurfaceDB(t)
	fresh := time.Now().Add(-time.Hour).UnixMilli()

	// One turn is filled before the next opens: a write reaches the turn the library
	// holds, and only the read below can tell the two turns' events apart.
	turnA := mustTurnKey(t, db)
	for _, ts := range []int64{100, fresh} {
		if _, err := db.AppendArchive(event("llm_request", "asked", ts)); err != nil {
			t.Fatalf("append event at %d: %v", ts, err)
		}
	}
	turnB := mustTurnKey(t, db)
	if _, err := db.AppendArchive(event("llm_request", "asked", 1_700_000_050_000)); err != nil {
		t.Fatalf("append B's event: %v", err)
	}

	if got := eventsOf(t, db, turnA); len(got) != 2 {
		t.Fatalf("both of A's events are inside the window: %+v", got)
	}

	// The two turns exist but hold no settled topics, so the consolidation stages
	// have nothing to chew and no LLM call is made; the prune stage is what this
	// exercises.
	if _, err := db.Dream(context.Background(), ""); err != nil {
		t.Fatalf("dream: %v", err)
	}
	got := eventsOf(t, db, turnA)
	if len(got) != 1 || got[0].CreatedAt != fresh {
		t.Fatalf("only A's fresh event survives: %+v", got)
	}
	if rest := eventsOf(t, db, turnB); len(rest) != 0 {
		t.Fatalf("B's expired event survives: %+v", rest)
	}
}

func TestSurfaceArchiveAppendAndRead(t *testing.T) {
	db := openSurfaceDB(t)
	turn := mustTurnKey(t, db)
	events := []ArchiveInput{
		event("llm_request", "user asks", 1_700_000_040_000),
		event("tool_call", "search", 1_700_000_040_100),
		event("llm_output", "replied", 1_700_000_040_200),
	}
	for _, ev := range events {
		if _, err := db.AppendArchive(ev); err != nil {
			t.Fatalf("append content: %v", err)
		}
	}
	if _, err := db.AppendArchive(ArchiveInput{Kind: KindEvent, Content: "no type"}); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("append invalid event: want ErrInvalidQuery, got %v", err)
	}
	got := eventsOf(t, db, turn)
	if len(got) != len(events) {
		t.Fatalf("read events: got %d, want %d", len(got), len(events))
	}
	for i, e := range got {
		want := events[i]
		// Slots 1 and 2 belong to the turn's dialogue, so the library starts a
		// topic's events at 3 and counts up from there.
		if e.Seq != uint64(i+3) {
			t.Fatalf("seq must ascend from the first free slot, got %d at %d", e.Seq, i)
		}
		// The read must hand back what was written, field for field: a facade
		// mapping that drops a column is otherwise invisible at this surface.
		if e.Kind != KindEvent || e.EventType != want.EventType || e.Content != want.Content ||
			e.CreatedAt != want.CreatedAt {
			t.Fatalf("event[%d] lost its body on the way back: got %+v want %+v", i, e, want)
		}
		if !isHexID(e.ID) || e.TopicID != turn {
			t.Fatalf("event[%d] ids: hash=%q context=%q", i, e.ID, e.TopicID)
		}
	}
}

// TestSurfaceListScenesByProject verifies scenes anchored to an L3 domain are
// listed with hex ids once a session opening anchors them, and that two
// different L3 domains yield DISJOINT scene sets (the exclusion branch).
func TestSurfaceListScenesByProject(t *testing.T) {
	db := openSurfaceDB(t)
	l3A := l3Graph(t, db, "l3-proj-a")
	l3B := l3Graph(t, db, "l3-proj-b")
	if _, err := db.Search(SearchQuery{L3ID: l3A, NewScene: true}); err != nil {
		t.Fatalf("search A: %v", err)
	}
	if _, err := db.Search(SearchQuery{L3ID: l3B, NewScene: true}); err != nil {
		t.Fatalf("search B: %v", err)
	}
	scenesA, err := db.ListScenes(l3A)
	if err != nil {
		t.Fatalf("list by l3 A: %v", err)
	}
	if len(scenesA) == 0 {
		t.Fatal("ListScenes(A) should return the l3A-anchored scene")
	}
	for _, sc := range scenesA {
		if sc.L3ID != l3A {
			t.Fatalf("scene %s should be anchored to %s, got %s", sc.SceneID, l3A, sc.L3ID)
		}
		if !isHexID(sc.SceneID) {
			t.Fatalf("scene id %s should be 16 hex", sc.SceneID)
		}
	}
	scenesB, err := db.ListScenes(l3B)
	if err != nil {
		t.Fatalf("list by l3 B: %v", err)
	}
	if len(scenesB) == 0 {
		t.Fatal("ListScenes(B) should return the l3B-anchored scene")
	}
	sceneIDsB := make(map[string]struct{}, len(scenesB))
	for _, sc := range scenesB {
		sceneIDsB[sc.SceneID] = struct{}{}
	}
	for _, sc := range scenesA {
		if _, dup := sceneIDsB[sc.SceneID]; dup {
			t.Fatalf("scene %s leaked into both l3A and l3B lists", sc.SceneID)
		}
	}
}

// TestSurfaceUpdateSceneAnchor verifies the anchor correction path: re-anchoring
// a scene that already has a different domain is rejected (never a silent
// no-op), Force moves it, and an empty L3ID clears it.
func TestSurfaceUpdateSceneAnchor(t *testing.T) {
	db := openSurfaceDB(t)
	l3A := l3Graph(t, db, "cor-a")
	l3B := l3Graph(t, db, "cor-b")
	if _, err := db.Search(SearchQuery{L3ID: l3A}); err != nil {
		t.Fatalf("search: %v", err)
	}
	scenesA, err := db.ListScenes(l3A)
	if err != nil || len(scenesA) == 0 {
		t.Fatalf("expected an l3A-anchored scene: %+v err=%v", scenesA, err)
	}
	sceneID := scenesA[0].SceneID

	// Write-once: a non-force move to another domain is rejected and changes nothing.
	if _, err := db.UpdateScene(sceneID, ScenePatch{L3ID: &l3B}); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("non-force re-anchor: want ErrInvalidQuery, got %v", err)
	}
	if scenes, _ := db.ListScenes(l3A); len(scenes) != 1 {
		t.Fatalf("non-force set must not move the anchor: %+v", scenes)
	}
	// Force re-anchors the scene to l3B and hands the written scene back, so the
	// host reads its anchor off the reply instead of listing the domain.
	got, err := db.UpdateScene(sceneID, ScenePatch{L3ID: &l3B, Force: true})
	if err != nil {
		t.Fatalf("force re-anchor: %v", err)
	}
	if got.SceneID != sceneID || !isHexID(got.L3ID) {
		t.Fatalf("written scene = %+v, want the same hex id + a hex anchor", got)
	}
	if got.L3ID != l3B {
		t.Fatalf("anchor = %q, want %q", got.L3ID, l3B)
	}
	if scenes, _ := db.ListScenes(l3B); len(scenes) != 1 {
		t.Fatalf("force set must land in l3B: %+v", scenes)
	}
	// Clearing needs no Force: the scene simply becomes unanchored again.
	clearTo := ""
	if got, err = db.UpdateScene(sceneID, ScenePatch{L3ID: &clearTo}); err != nil {
		t.Fatalf("clear anchor: %v", err)
	} else if got.L3ID != "" {
		t.Fatalf("clear must drop the anchor, got %q", got.L3ID)
	}
	if scenes, _ := db.ListScenes(l3B); len(scenes) != 0 {
		t.Fatalf("clear must drop the anchor: %+v", scenes)
	}
}

// TestSurfaceReservedTopicID locks the all-zero guard wherever a host still names a
// turn. Nothing in the library ever mints that key, while it is exactly what an
// unfilled one decodes to — so a read that answered it would be read back as "this
// turn holds nothing", and a correction asked for it would hang on an address nobody
// can name again. The five writes on the open turn carry no key at all: which turn is
// open is the library's memory of the last Search, and what they refuse is a domain
// holding no turn (TestTurnWritesRefuseWhenNoTurnIsOpen).
func TestSurfaceReservedTopicID(t *testing.T) {
	db := openSurfaceDB(t)
	zero := "0000000000000000"
	turn := mustTurnKey(t, db)
	now := time.Now().UnixMilli()
	for i := 0; i < 3; i++ {
		if _, err := db.AppendArchive(event("llm_request", "asked", now)); err != nil {
			t.Fatal(err)
		}
	}
	kind := KindEvent
	for name, call := range map[string]func() error{
		"SearchL4":     func() error { _, err := db.SearchL4(L4Query{TopicID: &zero, Kind: &kind}); return err },
		"DeleteTopic":  func() error { return db.DeleteTopic(zero) },
		"RenameTopic":  func() error { _, err := db.RenameTopic(zero, "x"); return err },
		"SearchL4Node": func() error { _, err := db.SearchL4(L4Query{TopicID: &zero, NodeSeq: 1}); return err },
	} {
		if err := call(); CodeOf(err) != ErrInvalidQuery {
			t.Fatalf("%s(zero topic id): err=%v, want ErrInvalidQuery", name, err)
		}
	}
	if got := eventsOf(t, db, turn); len(got) != 3 {
		t.Fatalf("bare turn events must survive: %d", len(got))
	}
}

// TestSurfaceAppendArchivePlanBranch pins the split write surface: the plan write
// face creates a tree one step at a time and AppendArchive writes content — both a
// bare turn event (no NodeSeq) and an event bound to one created step. The two turns
// are worked one after the other: a write reaches the turn the library holds, so the
// bare one is finished before the plan tree opens.
func TestSurfaceAppendArchivePlanBranch(t *testing.T) {
	db := openSurfaceDB(t)
	now := time.Now().UnixMilli()

	// A turn that only logged plain events owns no tree: a bare event references
	// no step, so nothing about it is a plan.
	mustTurnKey(t, db)
	if _, err := db.AppendArchive(event("llm_request", "asked", now)); err != nil {
		t.Fatalf("bare turn event: %v", err)
	}
	if bare, err := db.PlanState(); err != nil || bare.TotalCount != 0 {
		t.Fatalf("bare turn events invented a plan: %+v err=%v", bare, err)
	}

	planTurn := mustTurnKey(t, db)
	// An event cannot open a step. Creating the step first is the whole point: a
	// step the plan does not hold is the host's plan and its record disagreeing.
	if _, err := db.AppendArchive(onStep(event("tool_call", "p", now+1), 2)); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("event on a step that does not exist: want ErrInvalidQuery, got %v", err)
	}
	if empty, err := db.PlanState(); err != nil || empty.TotalCount != 0 {
		t.Fatalf("the refused event built a step: %+v err=%v", empty, err)
	}

	root, err := db.PlanNodeAdd(0, "父")
	if err != nil {
		t.Fatal(err)
	}
	child, err := db.PlanNodeAdd(root, "子")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendArchive(onStep(event("tool_call", "p", now+1), child)); err != nil {
		t.Fatalf("step-bound event: %v", err)
	}
	evs := eventsOf(t, db, planTurn)
	if len(evs) != 1 || evs[0].TopicID != planTurn || evs[0].NodeSeq != child {
		t.Fatalf("plan key: %+v", evs)
	}
	// The plan branch takes the host's own event name exactly as the bare path
	// does, and refuses only an empty one.
	if _, err := db.AppendArchive(onStep(event("sandbox_ask", "asked", now+2), root)); err != nil {
		t.Fatalf("host-named plan event: %v", err)
	}
	if _, err := db.AppendArchive(onStep(ArchiveInput{Kind: KindEvent, Content: "x", CreatedAt: now + 3}, root)); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("empty plan event type: want ErrInvalidQuery, got %v", err)
	}
	// An ordinal no step of this turn holds can never name one, so the same rule
	// refuses it — and nothing about the tree changes on the way out. An integer
	// address has no malformed spelling to catch: a step either exists or does not.
	if _, err := db.AppendArchive(onStep(event("x", "p", now+4), 77)); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("an ordinal nobody created: want ErrInvalidQuery, got %v", err)
	}
	if still, err := db.PlanState(); err != nil || still.TotalCount != 2 {
		t.Fatalf("a refused event changed the tree: %+v err=%v", still, err)
	}
	// An event that claims the dialogue track while naming a step is refused: the
	// two kinds do not share axes.
	if _, err := db.AppendArchive(onStep(ArchiveInput{
		Kind: KindUtterance, Role: RoleUser, EventType: "tool_call", Content: "x", CreatedAt: now + 5,
	}, root)); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("utterance wearing an event: want ErrInvalidQuery, got %v", err)
	}
	// A bound event carries the step it landed on, and the id it comes back with is
	// a library-issued hex token the host never builds.
	bound := eventsOf(t, db, planTurn)
	if len(bound) != 2 {
		t.Fatalf("plan-bound events: %+v", bound)
	}
	if bound[0].NodeSeq == 0 || bound[0].NodeSeq == bound[1].NodeSeq {
		t.Fatalf("plan-bound event lost its step attribution: %+v", bound)
	}
	if !isHexID(bound[1].ID) {
		t.Fatalf("event id is not a library hex token: %q", bound[1].ID)
	}
}

// TestSurfaceIDContract locks the host-facing id surface: the library issues
// every id, so the facade exposes no integer-to-hex bridge, and the turn key a
// plan is addressed by is one of them — hex-rendered, library-minted, and never
// the reserved all-zero token.
func TestSurfaceIDContract(t *testing.T) {
	llm := stubLLM()
	t.Cleanup(llm.Close)
	m, sess := openSurfaceSession(t, llm.URL)
	defer m.Close()
	res, err := sess.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !isHexID(res.NewTopicID) || res.NewTopicID == "0000000000000000" {
		t.Fatalf("turn topic %q is not a library-minted non-zero hex token", res.NewTopicID)
	}
}

// TestSurfaceDreamUnknownScene: naming a scene that does not exist is an
// error, not a zero-valued report that looks like a successful no-op.
func TestSurfaceDreamUnknownScene(t *testing.T) {
	db := openSurfaceDB(t)
	ghost := common.FormatHash(common.HashID("no-such-scene"))
	rep, err := db.Dream(context.Background(), ghost)
	if CodeOf(err) != ErrNotFound {
		t.Fatalf("dream unknown scene: rep=%v want ErrNotFound, got %v", rep, err)
	}
	// Dreaming an empty domain (no scene named) stays a clean no-op.
	if rep, err := db.Dream(context.Background(), ""); err != nil || rep == nil {
		t.Fatalf("dream empty domain: rep=%v err=%v", rep, err)
	}
}

// A host runs one decision loop per library, and the open turn is the library's own
// memory now. So the memory has to be per agent domain, not per file and not per
// process: three loops drive three domains here — the primary and a sub-agent sharing
// one file, plus a second file in the same process. Each opens a turn, records one
// line of its own in it, and closes it; the read-back says whether any of them wrote
// onto another's turn.
func TestEachDomainHoldsItsOwnTurn(t *testing.T) {
	llm := stubLLM()
	t.Cleanup(llm.Close)

	dbA, sub := openSurfaceSession(t, llm.URL)
	t.Cleanup(func() { _ = dbA.Close() })
	primary, err := dbA.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	dbB, otherFile := openSurfaceSession(t, llm.URL)
	t.Cleanup(func() { _ = dbB.Close() })

	hosts := []struct {
		sess *Session
		word string
	}{
		{primary, "primary"},
		{sub, "sub"},
		{otherFile, "other-file"},
	}

	turns := make([]string, len(hosts))
	for i, h := range hosts {
		res, err := h.sess.Search(SearchQuery{})
		if err != nil {
			t.Fatalf("%s Search: %v", h.word, err)
		}
		turns[i] = res.NewTopicID
		if _, err := h.sess.AppendArchive(event("tool_call", h.word, turnStamp)); err != nil {
			t.Fatalf("%s AppendArchive: %v", h.word, err)
		}
	}
	for i, h := range hosts {
		topic, err := h.sess.Update(TurnEnd{
			Input: h.word + " in", Output: h.word + " out", CreatedAt: turnStamp,
		})
		if err != nil {
			t.Fatalf("%s Update: %v", h.word, err)
		}
		if topic.ID != turns[i] {
			t.Fatalf("%s closed topic %s, want the turn its own Search opened (%s)",
				h.word, topic.ID, turns[i])
		}
	}
	for i, h := range hosts {
		if got := utterancesOf(t, h.sess, turns[i]); !slices.Equal(got,
			[]string{h.word + " in", h.word + " out"}) {
			t.Fatalf("%s's turn holds %v, want only its own two originals", h.word, got)
		}
		if evs := eventsOf(t, h.sess, turns[i]); len(evs) != 1 || evs[0].Content != h.word {
			t.Fatalf("%s's event track: %+v", h.word, evs)
		}
	}
}

// The host's own decision to run a second agent lands here: mid-round — after the first
// library opened its turn and while that turn is still open — a second file is opened and a
// whole round is run through it. Nothing about that disturbs the first round: the turn the
// first library opened is still the one its close settles, its own events stay its own, and
// the read that follows continues its own scene. This is the shape of "one library per
// agent" being safe to grow at runtime rather than only at start-up.
func TestSecondLibraryOpenedMidRound(t *testing.T) {
	llm := stubLLM()
	t.Cleanup(llm.Close)
	db1, first := openSurfaceSession(t, llm.URL)
	t.Cleanup(func() { _ = db1.Close() })

	opened, err := first.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("first round's Search: %v", err)
	}
	if _, err := first.AppendArchive(event("subagent_spawn", "go look at the second repo", turnStamp)); err != nil {
		t.Fatalf("spawn event: %v", err)
	}

	db2, second := openSurfaceSession(t, llm.URL)
	t.Cleanup(func() { _ = db2.Close() })
	if _, err := second.Search(SearchQuery{}); err != nil {
		t.Fatalf("second library's Search: %v", err)
	}
	if _, err := second.Update(TurnEnd{
		Input: "what does the second repo do", Output: "it stores memory", CreatedAt: turnStamp,
	}); err != nil {
		t.Fatalf("second library's Update: %v", err)
	}

	if _, err := first.AppendArchive(event("subagent_done", "it reported back", turnStamp)); err != nil {
		t.Fatalf("done event: %v", err)
	}
	closed, err := first.Update(TurnEnd{
		Input: "spawn an agent to look", Output: "it looked", CreatedAt: turnStamp,
	})
	if err != nil {
		t.Fatalf("first round's Update: %v", err)
	}
	if closed.ID != opened.NewTopicID {
		t.Fatalf("the first round closed topic %s, want the turn its own Search opened (%s)",
			closed.ID, opened.NewTopicID)
	}
	evs := eventsOf(t, first, opened.NewTopicID)
	if len(evs) != 2 || evs[0].Content != "go look at the second repo" || evs[1].Content != "it reported back" {
		t.Fatalf("the first turn's event track: %+v", evs)
	}
	if got := utterancesOf(t, first, opened.NewTopicID); !slices.Equal(got,
		[]string{"spawn an agent to look", "it looked"}) {
		t.Fatalf("the first turn's dialogue: %q", got)
	}
	next, err := first.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("third Search: %v", err)
	}
	if next.Scene.SceneID != opened.Scene.SceneID {
		t.Fatalf("the first library left its conversation: scene %s, want the one it was on (%s)",
			next.Scene.SceneID, opened.Scene.SceneID)
	}
	if next.NewTopicID == opened.NewTopicID {
		t.Fatalf("the read after the close reopened the turn it just settled: %s", next.NewTopicID)
	}
}
