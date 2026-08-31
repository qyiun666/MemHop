// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestTrajIndexAppendAndQuery(t *testing.T) {
	idx := NewTrajIndex()
	idx.Append(7, 1, 101, 100, 0)
	idx.Append(7, 2, 102, 200, 55)
	idx.Append(8, 1, 103, 300, 55)

	if seq, ok := idx.MaxSeq(7); !ok || seq != 2 {
		t.Fatalf("MaxSeq(7) = %d %v, want 2 true", seq, ok)
	}
	if _, ok := idx.MaxSeq(9); ok {
		t.Fatal("unknown turn must report ok=false")
	}
	if hashes := idx.EventHashes(7); len(hashes) != 2 || hashes[0] != 101 || hashes[1] != 102 {
		t.Fatalf("EventHashes(7) = %v", hashes)
	}
	if hashes := idx.EventHashes(9); hashes != nil {
		t.Fatalf("EventHashes(9) = %v, want nil", hashes)
	}
	if topic := idx.TopicEvents(55); len(topic) != 2 || topic[0] != 102 || topic[1] != 103 {
		t.Fatalf("TopicEvents(55) = %v", topic)
	}
	if topic := idx.TopicEvents(1); topic != nil {
		t.Fatalf("TopicEvents(1) = %v, want nil", topic)
	}

	if got := idx.RemoveBefore(250); len(got) != 2 || got[0] != 101 || got[1] != 102 {
		t.Fatalf("RemoveBefore = %v, want 101,102 (ts 100,200 < 250)", got)
	}
	if _, ok := idx.MaxSeq(7); ok {
		t.Fatal("emptied turn must be dropped from the index")
	}
	if seq, _ := idx.MaxSeq(8); seq != 1 {
		t.Fatal("ts 300 must survive prune")
	}

	idx.Append(7, 3, 104, 400, 0)
	if got := idx.RemoveBefore(350); len(got) != 1 || got[0] != 103 {
		t.Fatalf("RemoveBefore(350) = %v, want 103 (ts 300 < 350)", got)
	}
	if _, ok := idx.MaxSeq(8); ok {
		t.Fatal("emptied turn 8 must be dropped from the index")
	}
	if seq, _ := idx.MaxSeq(7); seq != 3 {
		t.Fatal("turn 7 must keep event 104")
	}
	sums := idx.Summaries()
	if len(sums) != 1 || sums[0].SessionID != 7 || sums[0].Steps != 1 || sums[0].LastAt != 400 {
		t.Fatalf("Summaries = %+v, want only turn 7", sums)
	}
}

func TestTrajIndexRemoveSession(t *testing.T) {
	idx := NewTrajIndex()
	idx.Append(7, 1, 101, 100, 55)
	idx.Append(7, 2, 102, 200, 0)
	idx.Append(8, 1, 103, 300, 55)

	got := idx.RemoveSession(7)
	if len(got) != 2 || got[0] != 101 || got[1] != 102 {
		t.Fatalf("RemoveSession(7) = %v, want [101 102]", got)
	}
	if _, ok := idx.MaxSeq(7); ok {
		t.Fatal("removed turn must report ok=false")
	}
	if hashes := idx.EventHashes(7); hashes != nil {
		t.Fatalf("EventHashes(7) = %v, want nil", hashes)
	}
	if topic := idx.TopicEvents(55); len(topic) != 1 || topic[0] != 103 {
		t.Fatalf("TopicEvents(55) = %v, want only the surviving turn's event", topic)
	}
	if sums := idx.Summaries(); len(sums) != 1 || sums[0].SessionID != 8 {
		t.Fatalf("Summaries = %+v, want only turn 8", sums)
	}
	// Removing the same (or an unknown) turn again is a no-op.
	if got := idx.RemoveSession(7); got != nil {
		t.Fatalf("second RemoveSession = %v, want nil", got)
	}
}

func TestBuildTrajFromEngineRestoresTurns(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "traj.meh"), 768)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close(&core.IndexSnapshotData{})
	const agent = core.DefaultAgentID
	for _, ev := range []core.TrajectorySlot{
		{SessionID: 7, Seq: 1, EventType: "llm_request", Payload: "a", Timestamp: 100},
		{SessionID: 7, Seq: 2, EventType: "tool_call", Payload: "b", Timestamp: 200, TopicID: 55},
		{SessionID: 8, Seq: 1, EventType: "llm_output", Payload: "c", Timestamp: 300, TopicID: 55},
	} {
		if err := repoAppendTraj(engine, agent, ev); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	idx := BuildTrajFromEngine(engine, agent)
	if seq, ok := idx.MaxSeq(7); !ok || seq != 2 {
		t.Fatalf("MaxSeq(7) = %d %v, want 2 true", seq, ok)
	}
	if topic := idx.TopicEvents(55); len(topic) != 2 {
		t.Fatalf("TopicEvents(55) = %v, want 2 ids", topic)
	}
	if sums := idx.Summaries(); len(sums) != 2 {
		t.Fatalf("Summaries = %+v, want 2 turns", sums)
	}
}

// repoAppendTraj mirrors repo.AppendTrajectory's id derivation; the index
// package must not import the repo layer.
func repoAppendTraj(engine *core.StorageEngine, agentID uint64, ev core.TrajectorySlot) error {
	ev.IDHash = common.HashID(fmt.Sprintf("%d:%d", ev.SessionID, ev.Seq))
	return core.WriteTrajectorySlot(engine, agentID, ev.IDHash, &ev)
}
