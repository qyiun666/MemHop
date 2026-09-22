// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

func writeContent(t *testing.T, engine *core.StorageEngine, idx *index.L4Index, topicID, seq uint64, kind core.ArchiveKind, text string, createdAt int64) uint64 {
	t.Helper()
	arc := &core.ArchiveSlot{
		TopicID: topicID, Seq: seq, Kind: kind, ContentType: core.ContentText,
		Content: text, CreatedAt: createdAt,
	}
	if err := AppendArchiveL4(engine, core.DefaultAgentID, idx, arc); err != nil {
		t.Fatalf("append %s/%d: %v", kind, seq, text)
	}
	return arc.IDHash
}

// The whole point of a positional id: re-writing a topic's Seq lands on the same
// record. A replayed turn therefore converges without the library ever holding a
// list of what that turn superseded.
func TestAppendArchiveL4SameSeqOverwritesInPlace(t *testing.T) {
	engine := tempEngine(t)
	idx := index.NewL4Index()
	topic := uint64(7)

	first := writeContent(t, engine, idx, topic, core.SeqUser, core.KindUtterance, "旧的说法", 1000)
	second := writeContent(t, engine, idx, topic, core.SeqUser, core.KindUtterance, "新的说法", 2000)
	if first != second {
		t.Fatalf("the same (topic, Seq) must derive one id: %d vs %d", first, second)
	}
	if n := len(core.CollectAllArchives(engine, core.DefaultAgentID)); n != 1 {
		t.Fatalf("live records = %d, want one: a rewrite must not leave a second version", n)
	}
	if got := idx.AllIDs(topic); len(got) != 1 || got[0] != second {
		t.Fatalf("index grew on a rewrite: %v", got)
	}

	got, err := QueryArchivesL4(engine, core.DefaultAgentID, ArchiveQuery{TopicID: &topic, Index: idx})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Content != "新的说法" {
		t.Fatalf("superseded text still readable: %+v", got)
	}
	// The mirror followed the record's new timestamp, so an ageing sweep reads
	// the rewrite rather than the slot's first write.
	if exp := idx.ExpiredBefore(1500); len(exp) != 0 {
		t.Fatalf("index kept the pre-rewrite timestamp: %v", exp)
	}
}

// Kind is a condition like any other, and it has to hold on every read route —
// including the by-ID fast path, which would otherwise hand an event back to a
// caller that asked for utterances.
func TestQueryArchivesL4HonoursKindOnEveryRoute(t *testing.T) {
	engine := tempEngine(t)
	idx := index.NewL4Index()
	topic := uint64(7)
	utter := writeContent(t, engine, idx, topic, core.SeqUser, core.KindUtterance, "原文", 1000)
	ev := writeContent(t, engine, idx, topic, 3, core.KindEvent, "发生了什么", 1100)

	kind := core.KindUtterance
	for name, q := range map[string]ArchiveQuery{
		"by id":          {IDs: []uint64{utter, ev}, Kind: &kind},
		"by topic":       {TopicID: &topic, Kind: &kind, Index: idx},
		"by scan":        {Kind: &kind, Keyword: ""},
		"by time":        {Start: 1000, End: 2000, Kind: &kind},
		"no kind at all": {TopicID: &topic, Index: idx},
	} {
		got, err := QueryArchivesL4(engine, core.DefaultAgentID, q)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := 1
		if name == "no kind at all" {
			want = 2
		}
		if len(got) != want {
			t.Fatalf("%s selected %+v, want %d record(s)", name, got, want)
		}
		if want == 1 && got[0].IDHash != utter {
			t.Fatalf("%s returned %x, want the utterance %x", name, got[0].IDHash, utter)
		}
	}
}

// A topic's records come back in Seq order even when their timestamps say
// otherwise: Seq is the one total order of a topic's content, and it is what
// makes a transcript read question-first.
func TestQueryArchivesL4OrdersBySeqNotTimestamp(t *testing.T) {
	engine := tempEngine(t)
	idx := index.NewL4Index()
	topic := uint64(7)
	writeContent(t, engine, idx, topic, 3, core.KindEvent, "c", 100)
	writeContent(t, engine, idx, topic, core.SeqUser, core.KindUtterance, "a", 900)
	writeContent(t, engine, idx, topic, core.SeqAgent, core.KindUtterance, "b", 900)

	got, err := QueryArchivesL4(engine, core.DefaultAgentID, ArchiveQuery{TopicID: &topic, Index: idx})
	if err != nil {
		t.Fatal(err)
	}
	var seqs []uint64
	for _, arc := range got {
		seqs = append(seqs, arc.Seq)
	}
	if len(seqs) != 3 || seqs[0] != 1 || seqs[1] != 2 || seqs[2] != 3 {
		t.Fatalf("Seq order lost: %v", seqs)
	}

	// Limit keeps the newest matches, which with Seq as the order means the
	// highest slots — the events, not the dialogue.
	kind := core.KindEvent
	capped, err := QueryArchivesL4(engine, core.DefaultAgentID, ArchiveQuery{Kind: &kind, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 1 || capped[0].Content != "c" {
		t.Fatalf("limit kept %+v", capped)
	}
}

// A record the index names but the engine cannot read is mirror drift and must
// be reported: a transcript missing one utterance reads exactly like a complete
// one. An empty query is not that case — it selects nothing, it does not fail.
func TestQueryArchivesL4TopicIndexDriftIsAnError(t *testing.T) {
	engine := tempEngine(t)
	idx := index.NewL4Index()
	topic := uint64(7)
	id := writeContent(t, engine, idx, topic, core.SeqUser, core.KindUtterance, "原文", 1000)
	if _, err := engine.DeleteRecordBatch(core.DefaultAgentID, []uint64{id}); err != nil {
		t.Fatal(err)
	}
	if _, err := QueryArchivesL4(engine, core.DefaultAgentID, ArchiveQuery{TopicID: &topic, Index: idx}); common.CodeOf(err) != common.ErrIO {
		t.Fatalf("index naming a missing record = %v, want ErrIO", err)
	}
}

// The domain-wide route answers the same promise as the indexed one: a record it
// cannot return is reported. Otherwise a content search quietly hands back fewer
// lines than the domain holds, and the caller has no way to tell that from a turn
// that said less.
func TestQueryArchivesL4ScanReportsUnreadableRecord(t *testing.T) {
	engine := tempEngine(t)
	idx := index.NewL4Index()
	writeContent(t, engine, idx, 7, core.SeqUser, core.KindUtterance, "能读的", 1000)
	damaged := writeContent(t, engine, idx, 8, core.SeqUser, core.KindUtterance, "读不动的", 1100)
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL4Archive, damaged, []byte(`{"id":`)); err != nil {
		t.Fatalf("make the record unreadable: %v", err)
	}
	kind := core.KindUtterance
	if _, err := QueryArchivesL4(engine, core.DefaultAgentID, ArchiveQuery{Kind: &kind}); common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("the scan must report the record it cannot return, got %v", err)
	}
}

func TestDeleteTopicArchivesTakesBothKinds(t *testing.T) {
	engine := tempEngine(t)
	idx := index.NewL4Index()
	writeContent(t, engine, idx, 7, core.SeqUser, core.KindUtterance, "a", 1000)
	writeContent(t, engine, idx, 7, 3, core.KindEvent, "e", 1100)
	writeContent(t, engine, idx, 8, core.SeqUser, core.KindUtterance, "keep", 1000)

	if err := DeleteTopicArchives(engine, core.DefaultAgentID, idx, []uint64{7}); err != nil {
		t.Fatal(err)
	}
	if n := len(core.CollectAllArchives(engine, core.DefaultAgentID)); n != 1 {
		t.Fatalf("live records = %d, want the other topic only", n)
	}
	if got := idx.AllIDs(7); got != nil {
		t.Fatalf("index still credits the deleted topic with %v", got)
	}
	// A topic with nothing indexed costs no write.
	if err := DeleteTopicArchives(engine, core.DefaultAgentID, idx, []uint64{99}); err != nil {
		t.Fatalf("empty topic delete: %v", err)
	}
}

func TestDropExpiredArchivesMirrorsAfterTheDisk(t *testing.T) {
	engine := tempEngine(t)
	idx := index.NewL4Index()
	topic := uint64(7)
	old := writeContent(t, engine, idx, topic, core.SeqUser, core.KindUtterance, "旧的", 1000)
	fresh := writeContent(t, engine, idx, topic, 3, core.KindEvent, "新的", 5000)

	n, err := DropExpiredArchives(engine, core.DefaultAgentID, idx, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("dropped %d, want 1", n)
	}
	if _, err := core.ReadArchiveSlot(engine, core.DefaultAgentID, old); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("expired record still readable: %v", err)
	}
	if got := idx.AllIDs(topic); len(got) != 1 || got[0] != fresh {
		t.Fatalf("mirror kept the dropped record: %v", got)
	}
	if n, err := DropExpiredArchives(engine, core.DefaultAgentID, idx, 2000); n != 0 || err != nil {
		t.Fatalf("second sweep should be a no-op, got %d/%v", n, err)
	}
}

// A Seq is a slot inside one turn, so a read spanning turns cannot order by it.
// Ordering by Seq did two damages at once: a host asking for its newest content
// got the turn with the most slots — an old but long turn beating a short fresh
// one — and records tied on Seq came back in whatever order the record scan
// visited them, so the same query twice could return different subsets of
// itself.
func TestDomainWideL4ReadOrdersByTimeAndKeepsNewest(t *testing.T) {
	engine := tempEngine(t)
	idx := index.NewL4Index()
	const longOld = uint64(11)
	const shortNew = uint64(12)
	const tiedNew = uint64(13)
	for seq := uint64(1); seq <= 3; seq++ {
		writeContent(t, engine, idx, longOld, seq, core.KindEvent, "旧轮的槽位", int64(1000+seq))
	}
	writeContent(t, engine, idx, shortNew, 1, core.KindEvent, "新轮", 5000)
	tiedA := writeContent(t, engine, idx, shortNew, 2, core.KindEvent, "同刻之一", 6000)
	tiedB := writeContent(t, engine, idx, tiedNew, 1, core.KindEvent, "同刻之二", 6000)

	got, err := QueryArchivesL4(engine, core.DefaultAgentID, ArchiveQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("limit 2 returned %d records", len(got))
	}
	for _, arc := range got {
		if arc.TopicID != shortNew && arc.TopicID != tiedNew {
			t.Fatalf("the newest two are not the newest: %+v", got)
		}
	}
	// A tie on one instant has exactly one order, and it is the record id.
	first, second := min(tiedA, tiedB), max(tiedA, tiedB)
	if got[0].IDHash != first || got[1].IDHash != second {
		t.Fatalf("a tie must break by id, got %x then %x", got[0].IDHash, got[1].IDHash)
	}

	// A turn's own slot order survives: its events come back 1,2,3 rather than
	// shuffled by id or timestamp.
	scoped := longOld
	older, err := QueryArchivesL4(engine, core.DefaultAgentID, ArchiveQuery{TopicID: &scoped})
	if err != nil {
		t.Fatal(err)
	}
	if len(older) != 3 || older[0].Seq != 1 || older[1].Seq != 2 || older[2].Seq != 3 {
		t.Fatalf("a turn lost its slot order: %+v", older)
	}
}
