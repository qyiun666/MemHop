// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests: exercise the public API surface through
// api.OpenMulti against a mock OpenAI-compatible LLM server. No external
// services required; run with `go test ./test/...`.

package test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/api"
)

func TestInterfaceTurnEventsAndCrystallize(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	// The content key is a turn's topic id — minted by Search and never typed by
	// hand.
	session := openTurn(t, db, sceneID)
	if err := turn(db.Session, sceneID, session, "读一下 a.go 并改掉拼写", "已读取 a.go 并改掉拼写"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	ts := time.Now().UnixMilli()

	if err := db.AppendArchive(session, api.ArchiveSlot{
		Kind: api.KindEvent, EventType: "tool_call", Content: `{"tool":"read_file","file":"a.go"}`, CreatedAt: ts,
	}); err != nil {
		t.Fatalf("AppendArchive: %v", err)
	}
	// The key has to be a turn the library actually opened, or the rest of this
	// test would only prove that a made-up id round-trips.
	if surface, err := db.Search(api.SearchQuery{SceneID: sceneID}); err != nil ||
		!slices.ContainsFunc(surface.Topics, func(topic api.TopicSlot) bool { return topic.ID == session }) {
		t.Fatalf("key %s is not a topic of scene %s: %+v err %v", session, sceneID, surface.Topics, err)
	}
	if err := db.AppendArchive(session, api.ArchiveSlot{
		Kind: api.KindEvent, EventType: "tool_result", Content: "file content", CreatedAt: ts + 500,
	}); err != nil {
		t.Fatalf("AppendArchive #2: %v", err)
	}
	kind := api.KindEvent
	events, err := db.SearchL4(api.L4Query{TopicID: &session, Kind: &kind})
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	// Slots 1 and 2 are reserved for the turn's dialogue, so the library allocates
	// a topic's first event at 3.
	if len(events) != 2 || events[0].Seq != 3 || events[1].Seq != 4 {
		t.Fatalf("want 2 events with seq 3,4: %+v", events)
	}

	// Crystallize returns candidates against a host-supplied catalog; the
	// engine stores nothing, so the candidates are all there is. The mock
	// speaks the prompt's contract: reuse/merge name an existing card,
	// create carries a full v4 card.
	existing := []api.CapabilityImport{{
		Name: "已有能力", Summary: "已有", Trigger: "已有触发",
		Resources: []api.ResourceRef{{Type: api.CapabilityMCP, Name: "old_tool"}},
	}}
	out, err := db.Crystallize(context.Background(), session, existing)
	if err != nil {
		t.Fatalf("Crystallize: %v", err)
	}
	if len(out.Capabilities) != 3 {
		t.Fatalf("want 3 candidates (reuse/merge/create): %+v", out)
	}
	byAction := map[string]api.CrystallizeCapability{}
	for _, c := range out.Capabilities {
		byAction[c.Action] = c
	}
	if reuse := byAction["reuse"]; reuse.ReuseID != "已有能力" {
		t.Fatalf("reuse candidate must name the existing card: %+v", reuse)
	}
	if merge := byAction["merge"]; merge.ReuseID != "已有能力" || merge.Capability.Name != "已有能力" {
		t.Fatalf("merge candidate mismatch: %+v", merge)
	}
	if create := byAction["create"]; create.Capability.Name != "重构流程" ||
		len(create.Capability.Resources) != 1 || create.Capability.Resources[0].Name != "read_file" {
		t.Fatalf("create candidate mismatch: %+v", create)
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

	// The step is planned first, then the event logged against it: a plan node is
	// only ever created by a declaration.
	mustDeclare(t, db, turnID, api.PlanStep{NodePath: "1.1", Status: "in_progress"})
	if err := db.AppendArchive(turnID, api.ArchiveSlot{
		Kind: api.KindEvent, EventType: "tool_call", NodePath: "1.1",
		Content: `{"tool":"bash","cmd":"go test"}`, CreatedAt: ts,
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	before := llm.calls["keywords"]
	if err := turn(db.Session, sceneID, turnID, "跑一下测试", "go test ./... 全绿"); err != nil {
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
	// Dialogue took slots 1 and 2, so the event logged before them landed at 3.
	if evs[0].Seq != 3 || evs[0].NodePath != "1.1" || evs[0].EventType != "tool_call" {
		t.Fatalf("event = %+v, want seq 3 bound to 1.1", evs[0])
	}
	if only, err := db.SearchL4(api.L4Query{TopicID: &turnID, Kind: &uttered}); err != nil || len(only) != 2 {
		t.Fatalf("utterance read = %+v err=%v, want the two originals", only, err)
	}
	if all, err := db.SearchL4(api.L4Query{TopicID: &turnID}); err != nil || len(all) != 3 {
		t.Fatalf("unfiltered topic read = %+v err=%v, want all three records", all, err)
	}

	tree, err := db.PlanState(turnID)
	if err != nil {
		t.Fatalf("plan state: %v", err)
	}
	if tree.TotalCount != 2 {
		t.Fatalf("plan tree = %+v, want the step 1.1 and the parent the path created", tree)
	}
}
