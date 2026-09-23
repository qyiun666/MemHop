// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func eqIDs(t *testing.T, name string, got, want []uint64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
}

// A settled turn holds two utterances; a turn that recorded operations holds
// events above them. The two kinds must stay separable, and Seq alone must
// order both.
func TestL4IndexSeparatesKindsBySeqOrder(t *testing.T) {
	idx := NewL4Index()
	const topic = uint64(7)
	idx.Append(topic, core.SeqUser, 11, core.KindUtterance, 1000, 0)
	idx.Append(topic, core.SeqAgent, 12, core.KindUtterance, 1001, 0)
	idx.Append(topic, 3, 13, core.KindEvent, 1002, 0)
	idx.Append(topic, 4, 14, core.KindEvent, 1003, 0)

	eqIDs(t, "utterances", idx.IDs(topic, core.KindUtterance), []uint64{11, 12})
	eqIDs(t, "events", idx.IDs(topic, core.KindEvent), []uint64{13, 14})
	eqIDs(t, "all", idx.AllIDs(topic), []uint64{11, 12, 13, 14})
	if got := idx.IDs(99, core.KindUtterance); got != nil {
		t.Fatalf("unknown topic = %v, want nil", got)
	}
}

// An event names a plan step by ordinal, and the two records age separately: the step
// can be swept while the event that names it survives. The mirror is what the allocator
// reads to learn which ordinals are still spoken of, so the highest bound one has to be
// visible here — and a pruned record must stop reserving it.
func TestL4IndexTracksBoundOrdinals(t *testing.T) {
	idx := NewL4Index()
	const topic = uint64(7)
	idx.Append(topic, core.SeqUser, 11, core.KindUtterance, 1000, 0)
	idx.Append(topic, 3, 13, core.KindEvent, 1002, 2)
	idx.Append(topic, 4, 14, core.KindEvent, 1003, 5)

	if got := idx.MaxNodeSeq(topic); got != 5 {
		t.Fatalf("MaxNodeSeq = %d, want the highest ordinal an event names", got)
	}
	if got := idx.MaxNodeSeq(99); got != 0 {
		t.Fatalf("an unknown topic reserves %d, want nothing", got)
	}
	idx.RemoveIDs(topic, []uint64{14})
	if got := idx.MaxNodeSeq(topic); got != 2 {
		t.Fatalf("after pruning the binder MaxNodeSeq = %d, want the one still recorded", got)
	}
	idx.RemoveTopic(topic)
	if got := idx.MaxNodeSeq(topic); got != 0 {
		t.Fatalf("after the topic is gone MaxNodeSeq = %d, want nothing left reserved", got)
	}
}

// Events are appended while the turn runs and the utterances settle afterwards,
// so an utterance's Seq 1/2 must insert ahead of events already recorded rather
// than land at the tail and reorder the transcript.
func TestL4IndexInsertsAnEarlierSeq(t *testing.T) {
	idx := NewL4Index()
	const topic = uint64(7)
	idx.Append(topic, 3, 13, core.KindEvent, 1002, 0)
	idx.Append(topic, 4, 14, core.KindEvent, 1003, 0)
	idx.Append(topic, core.SeqUser, 11, core.KindUtterance, 1000, 0)
	idx.Append(topic, core.SeqAgent, 12, core.KindUtterance, 1001, 0)

	eqIDs(t, "all after settle", idx.AllIDs(topic), []uint64{11, 12, 13, 14})
	eqIDs(t, "events stay ordered", idx.IDs(topic, core.KindEvent), []uint64{13, 14})
}

// Re-writing a Seq is an in-place overwrite on the disk, so the mirror must
// replace the slot, not grow a second entry that names a record nobody reads.
func TestL4IndexAppendSameSeqReplaces(t *testing.T) {
	idx := NewL4Index()
	const topic = uint64(7)
	idx.Append(topic, core.SeqUser, 11, core.KindUtterance, 1000, 0)
	idx.Append(topic, core.SeqUser, 11, core.KindUtterance, 2000, 0)

	if got := idx.AllIDs(topic); len(got) != 1 || got[0] != 11 {
		t.Fatalf("re-writing Seq 1 grew the topic: %v", got)
	}
	if exp := idx.ExpiredBefore(1500); len(exp) != 0 {
		t.Fatalf("entry kept the stale timestamp: %v", exp)
	}
}

// MaxSeq spans kinds, which is what keeps an allocated event off the two slots
// the turn's originals will claim.
func TestL4IndexMaxSeqSpansKinds(t *testing.T) {
	idx := NewL4Index()
	const topic = uint64(7)
	if got := idx.MaxSeq(topic); got != 0 {
		t.Fatalf("unknown topic MaxSeq = %d, want 0", got)
	}
	idx.Append(topic, core.SeqUser, 11, core.KindUtterance, 1000, 0)
	idx.Append(topic, core.SeqAgent, 12, core.KindUtterance, 1001, 0)
	if got := idx.MaxSeq(topic); got != core.LastUtteranceSeq {
		t.Fatalf("MaxSeq over two utterances = %d, want %d", got, core.LastUtteranceSeq)
	}
	idx.Append(topic, 5, 15, core.KindEvent, 1002, 0)
	if got := idx.MaxSeq(topic); got != 5 {
		t.Fatalf("MaxSeq = %d, want 5", got)
	}
}

// Expiry reports without mutating: the caller deletes the records first and only
// then mirrors the removal, so a failed delete cannot leave the index naming a
// live record as gone.
func TestL4IndexExpiredBeforeReadsOnly(t *testing.T) {
	idx := NewL4Index()
	idx.Append(1, core.SeqUser, 11, core.KindUtterance, 1000, 0)
	idx.Append(1, 3, 12, core.KindEvent, 900, 0)
	idx.Append(1, 4, 13, core.KindEvent, 5000, 0)
	idx.Append(2, core.SeqUser, 21, core.KindUtterance, 100, 0)

	expired := idx.ExpiredBefore(2000)
	eqIDs(t, "topic 1 expired", expired[1], []uint64{11, 12})
	eqIDs(t, "topic 2 expired", expired[2], []uint64{21})
	if len(expired) != 2 {
		t.Fatalf("ExpiredBefore touched other topics: %v", expired)
	}
	eqIDs(t, "index unchanged after report", idx.AllIDs(1), []uint64{11, 12, 13})

	idx.RemoveIDs(1, []uint64{11, 12})
	eqIDs(t, "after RemoveIDs", idx.AllIDs(1), []uint64{13})
	idx.RemoveIDs(1, []uint64{11})
	eqIDs(t, "removing an absent id", idx.AllIDs(1), []uint64{13})
	idx.RemoveTopic(1)
	if got := idx.AllIDs(1); got != nil {
		t.Fatalf("RemoveTopic left %v", got)
	}
}

// Emptying a topic by removal must drop the topic itself, or a later read
// addresses an empty entry as if the topic still held content.
func TestL4IndexRemoveAllIDsDropsTopic(t *testing.T) {
	idx := NewL4Index()
	idx.Append(1, core.SeqUser, 11, core.KindUtterance, 1000, 0)
	idx.RemoveIDs(1, []uint64{11})
	if got := idx.AllIDs(1); got != nil {
		t.Fatalf("emptied topic still listed: %v", got)
	}
	// AllIDs answers nil for an emptied topic and for one the index never held,
	// so the entry itself has to be checked: a leftover empty slice would let a
	// later read address the topic as if it still held content.
	if len(idx.byTopic) != 0 {
		t.Fatalf("emptied topic still holds an index entry: %v", idx.byTopic)
	}
}

func TestBuildL4FromEngineIndexesBothKinds(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "l4.meh"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	const topic = uint64(7)
	for _, slot := range []core.ArchiveSlot{
		{IDHash: core.HashContent(topic, core.SeqUser), Kind: core.KindUtterance, Seq: core.SeqUser, TopicID: topic, CreatedAt: 1000},
		{IDHash: core.HashContent(topic, 3), Kind: core.KindEvent, Seq: 3, TopicID: topic, CreatedAt: 1100},
	} {
		rec := slot
		if err := core.WriteArchiveSlot(engine, core.DefaultAgentID, rec.IDHash, &rec); err != nil {
			t.Fatal(err)
		}
	}

	idx := BuildL4FromEngine(engine, core.DefaultAgentID)
	if got := idx.MaxSeq(topic); got != 3 {
		t.Fatalf("rebuilt MaxSeq = %d, want 3", got)
	}
	if got := idx.IDs(topic, core.KindUtterance); len(got) != 1 || got[0] != core.HashContent(topic, core.SeqUser) {
		t.Fatalf("rebuilt utterances = %v", got)
	}
	if got := idx.IDs(topic, core.KindEvent); len(got) != 1 {
		t.Fatalf("rebuilt events = %v", got)
	}
}
