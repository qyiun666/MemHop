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

// The all-zero key is the unset value of every record's owning id, so no turn
// ever settled content under it. Naming it as a filter is a host saying it holds
// no key at all — it has to be refused like every other entry that takes a turn
// key, not answered as an empty list that reads like a turn with nothing in it.
func TestSearchL4RefusesReservedZeroTopic(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	zero := "0000000000000000"
	if _, err := db.SearchL4(core.DefaultAgentID, L4Query{TopicID: &zero}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("want ErrInvalidQuery for the reserved topic key, got %v", err)
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

// One turn's events are a plan step at a time for the host: the step's ordinal
// filters the attribution down to what belongs to it, inside the turn that owns
// them — and a step's read covers its whole branch, which is only knowable from
// the tree, so the steps have to have been created there.
func TestSearchL4ByNodeSeq(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	topic := common.HashID("turn-tree")
	topicHex := common.FormatHash(topic)

	// The branch this read must walk: step 1 → 2 → 3, and a second top-level step
	// with no link to it.
	root := add(t, db, topicHex, 0, "一步")
	child := add(t, db, topicHex, root, "子")
	grand := add(t, db, topicHex, child, "孙")
	sibling := add(t, db, topicHex, 0, "另起的")

	appendEvent := func(slotSeq uint64, nodeSeq uint32) uint64 {
		t.Helper()
		slot := core.ArchiveSlot{
			Kind: core.KindEvent, EventType: "tool_call", Content: "work",
			CreatedAt: int64(2000 + slotSeq), Seq: slotSeq, NodeSeq: nodeSeq,
		}
		if err := db.AppendArchive(core.DefaultAgentID, topicHex, slot); err != nil {
			t.Fatalf("append on step %d: %v", nodeSeq, err)
		}
		return core.HashContent(topic, slotSeq)
	}
	own := appendEvent(3, root)
	kid := appendEvent(4, child)
	leaf := appendEvent(5, grand)
	other := appendEvent(6, sibling)
	// A record no step did: it belongs to the turn and to no step's read.
	appendEvent(7, 0)

	evKind := core.KindEvent
	want := func(nodeSeq uint32, ids ...uint64) {
		t.Helper()
		out, err := db.SearchL4(core.DefaultAgentID,
			L4Query{TopicID: &topicHex, Kind: &evKind, NodeSeq: nodeSeq})
		if err != nil {
			t.Fatalf("step %d read: %v", nodeSeq, err)
		}
		if len(out) != len(ids) {
			t.Fatalf("step %d: want %d events, got %+v", nodeSeq, len(ids), out)
		}
		for i, id := range ids {
			if out[i].IDHash != id {
				t.Fatalf("step %d entry %d: want %x, got %x", nodeSeq, i, id, out[i].IDHash)
			}
		}
	}
	// A step's work includes what its sub-steps did: once a step is split, the
	// events land on the children, and a read answering only for the parent's own
	// line would report a step that did one thing when it did three.
	want(root, own, kid, leaf)
	want(child, kid, leaf)
	want(grand, leaf)
	// An unrelated step of the same turn keeps its own records and nothing else —
	// with ordinals there is no shared digit prefix to tell apart from a parent.
	want(sibling, other)

	// A step that was never created selects no records: the filter answers with
	// an empty read rather than inventing a branch to justify itself.
	if got, err := db.SearchL4(core.DefaultAgentID,
		L4Query{TopicID: &topicHex, Kind: &evKind, NodeSeq: 99}); err != nil || len(got) != 0 {
		t.Fatalf("an unknown step = %+v / %v, want no records", got, err)
	}
}

// A step address means nothing outside the turn holding its records, and a read
// that took one without a topic would sweep the whole domain.
func TestNodeSeqFilterNeedsTopicID(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	if _, err := db.SearchL4(core.DefaultAgentID, L4Query{NodeSeq: 1}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("a step filter with no turn: want ErrInvalidQuery, got %v", err)
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

// A filter set to a value outside the vocabulary matches nothing, and an empty list
// is exactly what "this turn holds none" looks like. The append boundary refuses
// those same values, so a read may not answer them with a shorter list.
func TestSearchL4RefusesUndefinedFilterValues(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	mustScene(t, engine, 5, "工作")

	unknownKind := core.ArchiveKind(99)
	unknownType := core.ContentType(99)
	if _, err := db.SearchL4(core.DefaultAgentID, L4Query{Kind: &unknownKind}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("an undefined kind must be refused, got %v", err)
	}
	if _, err := db.SearchL4(core.DefaultAgentID, L4Query{Type: &unknownType}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("an undefined content type must be refused, got %v", err)
	}
}
