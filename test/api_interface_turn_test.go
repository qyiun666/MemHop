// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests: exercise the public API surface through
// api.Open against a mock OpenAI-compatible LLM server. No external
// services required; run with `go test ./test/...`.

package test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/api"
)

func TestInterfaceTurnEvents(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	// The content key is a turn's topic id — minted by Search and never typed by
	// hand. It is also the only turn these writes can reach: AppendArchive names
	// nothing, it goes where the open turn is.
	session := openTurn(t, db, sceneID)
	ts := time.Now().UnixMilli()

	if _, err := db.AppendArchive(api.ArchiveInput{
		Kind: api.KindEvent, EventType: "tool_call", Content: `{"tool":"read_file","file":"a.go"}`, CreatedAt: ts,
	}); err != nil {
		t.Fatalf("AppendArchive: %v", err)
	}
	if _, err := db.AppendArchive(api.ArchiveInput{
		Kind: api.KindEvent, EventType: "tool_result", Content: "file content", CreatedAt: ts + 500,
	}); err != nil {
		t.Fatalf("AppendArchive #2: %v", err)
	}
	if _, err := turn(db.Session, "读一下 a.go 并改掉拼写", "已读取 a.go 并改掉拼写"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	// The key has to be a turn the library actually opened, or the rest of this
	// test would only prove that a made-up id round-trips. Read after the close:
	// this Search opens the next turn, and every write of this one is already in.
	if surface, err := db.Search(api.SearchQuery{SceneID: sceneID}); err != nil ||
		!slices.ContainsFunc(surface.Topics, func(topic api.TopicSlot) bool { return topic.ID == session }) {
		t.Fatalf("key %s is not a topic of scene %s: %+v err %v", session, sceneID, surface.Topics, err)
	}
	kind := api.KindEvent
	events, err := db.SearchL4(api.L4Query{TopicID: &session, Kind: &kind})
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	// Slots 1 and 2 are reserved for the turn's dialogue — allocation skips them even
	// while they are still empty, which is what puts these two events at 3 and 4 even
	// though the dialogue was written after them. Each event comes back as it went in:
	// its own type, its own payload, and the timestamp the host stamped it with.
	if len(events) != 2 || events[0].Seq != 3 || events[1].Seq != 4 {
		t.Fatalf("want 2 events with seq 3,4: %+v", events)
	}
	if events[0].EventType != "tool_call" || events[0].Content != `{"tool":"read_file","file":"a.go"}` || events[0].CreatedAt != ts {
		t.Fatalf("first event = %+v, want the tool_call as appended at %d", events[0], ts)
	}
	if events[1].EventType != "tool_result" || events[1].Content != "file content" || events[1].CreatedAt != ts+500 {
		t.Fatalf("second event = %+v, want the tool_result as appended at %d", events[1], ts+500)
	}
}

// One turn, one key: what the turn said and what it did are the same topic's
// content, and no read confuses them — the scene context shows only the dialogue,
// the kind-filtered read only the event, the plan view only the step the event
// named, and the one distillation sees exactly the dialogue.
func TestInterfaceTurnContentSharesOneKey(t *testing.T) {
	db, llm := openTestDB(t)
	sceneID := openSession(t, db)
	turnID := openTurn(t, db, sceneID)
	ts := time.Now().UnixMilli()

	// The step is created first, then the event logged against it: a plan node is
	// only ever created by the plan write surface. Both belong to the open turn.
	step := mustCreate(t, db, 0, "")
	if _, err := db.AppendArchive(api.ArchiveInput{
		Kind: api.KindEvent, EventType: "tool_call", NodeSeq: step,
		Content: `{"tool":"bash","cmd":"go test"}`, CreatedAt: ts,
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	before := llm.calls["keywords"]
	if _, err := turn(db.Session, "跑一下测试", "go test ./... 全绿"); err != nil {
		t.Fatalf("settle turn: %v", err)
	}
	if got := llm.calls["keywords"] - before; got != 1 {
		t.Fatalf("settling cost %d distillations, want 1", got)
	}

	ctx, err := db.SceneContext(sceneID)
	if err != nil {
		t.Fatalf("scene context: %v", err)
	}
	if len(ctx.Topics) != 1 {
		t.Fatalf("scene surface = %+v, want the one turn", ctx.Topics)
	}
	msgs := ctx.Topics[0].Messages
	if len(msgs) != 2 || msgs[0].Seq != 1 || msgs[1].Seq != 2 ||
		msgs[0].Role != api.RoleUser || msgs[1].Role != api.RoleAgent {
		t.Fatalf("dialogue read = %+v, want the two originals on slots 1 and 2", msgs)
	}
	for _, m := range msgs {
		if m.Content == `{"tool":"bash","cmd":"go test"}` {
			t.Fatalf("the event leaked into the transcript: %+v", msgs)
		}
	}

	event := api.KindEvent
	uttered := api.KindUtterance
	evs, err := db.SearchL4(api.L4Query{TopicID: &turnID, Kind: &event})
	if err != nil || len(evs) != 1 {
		t.Fatalf("event read = %+v err=%v", evs, err)
	}
	// Dialogue owns slots 1 and 2 whoever writes them first, so the event logged
	// before the close still landed at 3.
	if evs[0].Seq != 3 || evs[0].NodeSeq != step || evs[0].EventType != "tool_call" {
		t.Fatalf("event = %+v, want seq 3 bound to step %d", evs[0], step)
	}
	if only, err := db.SearchL4(api.L4Query{TopicID: &turnID, Kind: &uttered}); err != nil || len(only) != 2 {
		t.Fatalf("utterance read = %+v err=%v, want the two originals", only, err)
	}
	if all, err := db.SearchL4(api.L4Query{TopicID: &turnID}); err != nil || len(all) != 3 {
		t.Fatalf("unfiltered topic read = %+v err=%v, want all three records", all, err)
	}

	// The turn this closed is still the domain's open one, so the plan read reaches
	// its tree without naming it.
	tree, err := db.PlanState()
	if err != nil {
		t.Fatalf("plan state: %v", err)
	}
	// The tree holds the one step that was created: binding an event to a step
	// adds no parent above it, and nothing implies one any more.
	if tree.TotalCount != 1 || tree.Roots[0].Seq != step {
		t.Fatalf("plan tree = %+v, want just the step the event bound to", tree)
	}
}

// A turn's two sides are each optional and a close that carries neither is refused: the
// keyword track distills out of dialogue, so an empty one is not settled into a topic with
// nothing in it. A close that states only how the round ended (the outcome event) is the
// same case — the events a round recorded while it ran are real, but they are not a
// transcript, and the turn stays open for the host to close it with one.
func TestInterfaceUpdateNeedsAtLeastOneSideOfTheDialogue(t *testing.T) {
	db, mock := openTestDB(t)
	sceneID := openSession(t, db)

	openTurn(t, db, sceneID)
	_, err := db.Update(api.TurnEnd{CreatedAt: time.Now().UnixMilli()})
	if api.CodeOf(err) != api.ErrInvalidQuery {
		t.Fatalf("a close with nothing in it: want ErrInvalidQuery, got %v", err)
	}
	// The refusal names what the host left out rather than blaming the turn's content:
	// the two questions have different answers, and a host reads this one to fix its call.
	if !strings.Contains(err.Error(), "an input, an output or an outcome") {
		t.Fatalf("a close with nothing in it answered %q, want it to say which fields are missing", err)
	}
	before := mock.calls["keywords"]
	// The refused close left the turn open, so the host can still finish it properly.
	if _, err := db.Update(api.TurnEnd{Input: "只有刺激", Output: "只有回复", CreatedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("closing after the refusal: %v", err)
	}
	if mock.calls["keywords"] != before+1 {
		t.Fatalf("the distill calls moved from %d to %d, want exactly one after the refusal", before, mock.calls["keywords"])
	}

	// One side alone is a turn that happened: what landed is that one line, on the slot
	// its side owns, and the track still distills.
	for _, tc := range []struct {
		name    string
		in, out string
		wantSeq uint64
	}{
		{"a round with no stimulus", "", "后台任务自己跑完了", 2},
		{"a round with nothing to say", "用户问了但没有回答", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := openTurn(t, db, sceneID)
			ts := time.Now().UnixMilli()
			if _, err := db.Update(api.TurnEnd{Input: tc.in, Output: tc.out, CreatedAt: ts}); err != nil {
				t.Fatalf("close: %v", err)
			}
			kind := api.KindUtterance
			lines, err := db.SearchL4(api.L4Query{TopicID: &key, Kind: &kind})
			if err != nil || len(lines) != 1 {
				t.Fatalf("the turn's dialogue = %+v err %v, want one line", lines, err)
			}
			if lines[0].Seq != tc.wantSeq || lines[0].Content != tc.in+tc.out {
				t.Fatalf("the line landed on %+v, want Seq %d holding the side that was sent", lines[0], tc.wantSeq)
			}
			if _, err := db.Update(api.TurnEnd{Outcome: "suspended", CreatedAt: ts + 1}); err != nil {
				t.Fatalf("re-closing the same turn with only an outcome: %v", err)
			}
		})
	}
}
