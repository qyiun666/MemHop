// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// writeSlot fabricates one content record the way the library would have written
// it: a Seq of its own, and the id that (topic, Seq) hashes to. A hand-invented id
// names a record no slot addresses, and a repeated Seq now means one slot being
// rewritten, so a fixture that ignores both stops describing the store.
func writeSlot(t *testing.T, engine *core.StorageEngine, topicID, seq uint64,
	kind core.ArchiveKind, content string, createdAt int64, ctype core.ContentType) core.ArchiveSlot {
	t.Helper()
	arc := core.ArchiveSlot{
		IDHash: core.HashContent(topicID, seq), Kind: kind, Seq: seq,
		ContentType: ctype, TopicID: topicID, Content: content, CreatedAt: createdAt,
	}
	if err := core.WriteArchiveSlot(engine, core.DefaultAgentID, arc.IDHash, &arc); err != nil {
		t.Fatalf("write slot %d: %v", seq, err)
	}
	return arc
}

// TestSearchL4ByID reads archives by ID; a missing ID is simply skipped.
func TestSearchL4ByID(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	topicHash := common.HashID("topic1")
	a1 := writeSlot(t, engine, topicHash, core.SeqUser, core.KindUtterance, "hello", 1000, core.ContentText)

	got, err := db.SearchL4(core.DefaultAgentID, L4Query{IDs: []string{common.FormatHash(a1.IDHash)}})
	if err != nil {
		t.Fatalf("SearchL4 by id: %v", err)
	}
	if len(got) != 1 || got[0].Content != "hello" || got[0].TopicID != topicHash {
		t.Fatalf("unexpected archive: %+v", got)
	}

	miss, err := db.SearchL4(core.DefaultAgentID, L4Query{IDs: []string{common.FormatHash(12345)}})
	if err != nil || len(miss) != 0 {
		t.Fatalf("missing id: want empty result, got %v / %v", miss, err)
	}
	if _, err := db.SearchL4(core.DefaultAgentID, L4Query{IDs: []string{"nothex"}}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("malformed id: want ErrInvalidQuery, got %v", err)
	}
}

// TestSearchL4TopicOnly pins the read a host needs after a turn: naming only
// the topic (or only the content type) must resolve that turn's originals
// instead of falling through to an empty result.
func TestSearchL4TopicOnly(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	t1, t2 := common.HashID("only1"), common.HashID("only2")
	o1 := writeSlot(t, engine, t1, core.SeqUser, core.KindUtterance, "u", 1000, core.ContentText)
	o2 := writeSlot(t, engine, t1, core.SeqAgent, core.KindUtterance, "a", 1001, core.ContentText)
	writeSlot(t, engine, t2, core.SeqUser, core.KindUtterance, "other", 1002, core.ContentText)

	t1Hex := common.FormatHash(t1)
	got, err := db.SearchL4(core.DefaultAgentID, L4Query{TopicID: &t1Hex})
	if err != nil {
		t.Fatalf("topic-only: %v", err)
	}
	if len(got) != 2 || got[0].IDHash != o1.IDHash || got[1].IDHash != o2.IDHash {
		t.Fatalf("topic-only: want the two slots of t1 in Seq order, got %+v", got)
	}

	all, err := db.SearchL4(core.DefaultAgentID, L4Query{})
	if err != nil || len(all) != 3 {
		t.Fatalf("empty query: want every content record, got %d / %v", len(all), err)
	}
}

// writeEvent records one operation event in the topic's own Seq space, optionally
// attributed to a plan step — the axis the content read filters on.
func writeEvent(t *testing.T, engine *core.StorageEngine, topicID, seq uint64,
	nodePath, text string) core.ArchiveSlot {
	t.Helper()
	arc := core.ArchiveSlot{
		IDHash: core.HashContent(topicID, seq), Kind: core.KindEvent, Seq: seq,
		TopicID: topicID, NodePath: nodePath, EventType: "tool_call",
		Content: text, CreatedAt: int64(2000 + seq),
	}
	if err := core.WriteArchiveSlot(engine, core.DefaultAgentID, arc.IDHash, &arc); err != nil {
		t.Fatalf("write event %d: %v", seq, err)
	}
	return arc
}

// One turn's events are a plan step at a time for the host: NodePath filters the
// attribution down to the step it belongs to, inside the turn that owns them.
func TestSearchL4ByNodePath(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	topic := common.HashID("turn-tree")
	writeSlot(t, engine, topic, core.SeqUser, core.KindUtterance, "u", 1000, core.ContentText)
	own := writeEvent(t, engine, topic, 3, "1", "the step's own line")
	child := writeEvent(t, engine, topic, 4, "1.1", "cargo build")
	grand := writeEvent(t, engine, topic, 5, "1.1.1", "cargo build --release")
	writeEvent(t, engine, topic, 6, "30", "a sibling that merely shares the digit")
	writeEvent(t, engine, topic, 7, "", "unattributed")

	topicHex := common.FormatHash(topic)
	evKind := core.KindEvent
	want := func(nodePath string, ids ...uint64) {
		t.Helper()
		out, err := db.SearchL4(core.DefaultAgentID,
			L4Query{TopicID: &topicHex, Kind: &evKind, NodePath: nodePath})
		if err != nil {
			t.Fatalf("node-path %q read: %v", nodePath, err)
		}
		if len(out) != len(ids) {
			t.Fatalf("node-path %q: want %d events, got %+v", nodePath, len(ids), out)
		}
		for i, id := range ids {
			if out[i].IDHash != id {
				t.Fatalf("node-path %q entry %d: want %x, got %x", nodePath, i, id, out[i].IDHash)
			}
		}
	}
	// A step's work includes what its sub-steps did: once "1" is split, the
	// events land on the children, and a read that answered only for the parent's
	// own line would report a step that did one thing when it did three.
	want("1", own.IDHash, child.IDHash, grand.IDHash)
	want("1.1", child.IDHash, grand.IDHash)
	want("1.1.1", grand.IDHash)
	// Segment boundary, not string prefix: "3" is not the parent of "30", so it
	// matches nothing here.
	want("3")
}

// A step address means nothing outside the turn holding its records, and a read
// that took one without a topic would sweep the whole domain.
func TestNodePathFilterNeedsTopicID(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	if _, err := db.SearchL4(core.DefaultAgentID, L4Query{NodePath: "1.1"}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("node path without a topic: want ErrInvalidQuery, got %v", err)
	}
	topicHex := common.FormatHash(common.HashID("shape"))
	if _, err := db.SearchL4(core.DefaultAgentID,
		L4Query{TopicID: &topicHex, NodePath: "1..2"}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("malformed node path: want ErrInvalidQuery, got %v", err)
	}
}

// TestSearchL4TopicFilter three modes combined with TopicID filtering.
func TestSearchL4TopicFilter(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	t1, t2 := common.HashID("t1"), common.HashID("t2")
	a1 := writeSlot(t, engine, t1, core.SeqUser, core.KindUtterance, "rust 所有权", 1000, core.ContentText)
	a2 := writeSlot(t, engine, t1, core.SeqAgent, core.KindUtterance, "生命周期", 2000, core.ContentText)
	a3 := writeSlot(t, engine, t2, core.SeqUser, core.KindUtterance, "rust 生态", 3000, core.ContentText)
	t1Hex, t2Hex := common.FormatHash(t1), common.FormatHash(t2)

	// Keyword + TopicID: a1 hits (a3 belongs to t2 and is excluded).
	out, err := db.SearchL4(core.DefaultAgentID, L4Query{Keyword: "rust", TopicID: &t1Hex})
	if err != nil {
		t.Fatalf("SearchL4 keyword+topic: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != a1.IDHash {
		t.Fatalf("keyword+topic: want [a1], got %v", out)
	}

	// Time range + TopicID: a1 only (Start must be > 0; 0 means unset).
	out, err = db.SearchL4(core.DefaultAgentID, L4Query{Start: 500, End: 1500, TopicID: &t1Hex})
	if err != nil {
		t.Fatalf("SearchL4 range+topic: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != a1.IDHash {
		t.Fatalf("range+topic: want [a1], got %v", out)
	}

	// IDs mode + TopicID: only a3.
	out, err = db.SearchL4(core.DefaultAgentID, L4Query{
		IDs:     []string{common.FormatHash(a1.IDHash), common.FormatHash(a2.IDHash), common.FormatHash(a3.IDHash)},
		TopicID: &t2Hex})
	if err != nil {
		t.Fatalf("SearchL4 ids+topic: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != a3.IDHash {
		t.Fatalf("ids+topic: want [a3], got %v", out)
	}

	// Invalid TopicID errors.
	if _, err := db.SearchL4(core.DefaultAgentID, L4Query{Keyword: "rust", TopicID: new("nothex")}); err == nil {
		t.Fatal("want error for invalid topic id")
	}
}

// TestSearchL4TypeFilter: the optional content-type filter narrows results
// within the query modes.
func TestSearchL4TypeFilter(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	topic := common.HashID("types")
	writeSlot(t, engine, topic, core.SeqUser, core.KindUtterance, "文字内容", 2000, core.ContentText)
	image := writeSlot(t, engine, topic, core.SeqAgent, core.KindUtterance, "img://cat.png", 3000, core.ContentImage)

	img := core.ContentImage
	got, err := db.SearchL4(core.DefaultAgentID, L4Query{Start: 1000, End: 4000, Type: &img})
	if err != nil {
		t.Fatalf("SearchL4: %v", err)
	}
	if len(got) != 1 || got[0].IDHash != image.IDHash {
		t.Fatalf("type filter: %+v, want only the image slot", got)
	}
}

// Kind is a condition like any other, and a topic's two kinds are now stored
// together — so an unfiltered read of a topic has to return both, and each Kind
// has to cut the other away on every read route.
func TestSearchL4KindCondition(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	topic := common.HashID("mixed")
	utter := writeSlot(t, engine, topic, core.SeqUser, core.KindUtterance, "说了什么", 1000, core.ContentText)
	ev := writeSlot(t, engine, topic, core.LastUtteranceSeq+1, core.KindEvent, "发生了什么", 1001, core.ContentText)
	ev2 := writeSlot(t, engine, topic, core.LastUtteranceSeq+2, core.KindEvent, "又发生了什么", 1002, core.ContentText)
	topicHex := common.FormatHash(topic)

	utterance, event := core.KindUtterance, core.KindEvent
	cases := []struct {
		name string
		q    L4Query
		want []core.ArchiveSlot
	}{
		{"unset kind, by topic", L4Query{TopicID: &topicHex}, []core.ArchiveSlot{utter, ev, ev2}},
		{"utterance only", L4Query{TopicID: &topicHex, Kind: &utterance}, []core.ArchiveSlot{utter}},
		{"events only", L4Query{TopicID: &topicHex, Kind: &event}, []core.ArchiveSlot{ev, ev2}},
		{"unset kind, scanned", L4Query{}, []core.ArchiveSlot{utter, ev, ev2}},
		{"events, scanned", L4Query{Kind: &event}, []core.ArchiveSlot{ev, ev2}},
		{"events, by id", L4Query{IDs: []string{
			common.FormatHash(utter.IDHash), common.FormatHash(ev.IDHash)}, Kind: &event},
			[]core.ArchiveSlot{ev}},
	}
	for _, tc := range cases {
		got, err := db.SearchL4(core.DefaultAgentID, tc.q)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("%s = %+v, want %d record(s)", tc.name, got, len(tc.want))
		}
		for i := range got {
			if got[i].IDHash != tc.want[i].IDHash {
				t.Fatalf("%s order/selection = %+v, want %+v", tc.name, got, tc.want)
			}
		}
	}
}

// The archive keyword is matched case-insensitively, like the L3 node keyword:
// a host that types "RUST" into either layer gets the same set back. Limit caps
// the result from the newest end, because the read is ordered oldest first.
func TestSearchL4KeywordCaseAndLimit(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	topic := common.HashID("case")
	writeSlot(t, engine, topic, 1, core.KindUtterance, "Rust 所有权", 1000, core.ContentText)
	fresh := writeSlot(t, engine, topic, 2, core.KindUtterance, "rust 生态", 2000, core.ContentText)
	writeSlot(t, engine, topic, 3, core.KindUtterance, "go 并发", 3000, core.ContentText)

	for _, kw := range []string{"RUST", "rust", "Rust"} {
		got, err := db.SearchL4(core.DefaultAgentID, L4Query{Keyword: kw})
		if err != nil {
			t.Fatalf("keyword %q: %v", kw, err)
		}
		if len(got) != 2 {
			t.Fatalf("keyword %q: want both rust archives, got %+v", kw, got)
		}
	}

	capped, err := db.SearchL4(core.DefaultAgentID, L4Query{Keyword: "rust", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 1 || capped[0].IDHash != fresh.IDHash {
		t.Fatalf("limit 1: want the newest match only, got %+v", capped)
	}
	if all, err := db.SearchL4(core.DefaultAgentID, L4Query{Limit: 2}); err != nil || len(all) != 2 || all[0].IDHash != fresh.IDHash {
		t.Fatalf("limit over an unfiltered read: %+v / %v", all, err)
	}
}
