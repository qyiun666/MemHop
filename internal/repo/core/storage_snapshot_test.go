// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"fmt"
	"testing"
	"time"
)

func TestOpenRecoversRecordsAfterSnapshot(t *testing.T) {
	p := tempPath(t, "crash")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close(&IndexSnapshotData{}) })
	// First batch, then checkpoint.
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("one"))
	eng.WriteRecord(DefaultAgentID, RecL1SceneNode, 2, []byte("two"))
	if err := eng.Checkpoint(&IndexSnapshotData{}); err != nil {
		t.Fatal(err)
	}
	// Second batch appended after the checkpoint (includes an overwrite).
	eng.WriteRecord(DefaultAgentID, RecL2Topic, 3, []byte("three"))
	eng.WriteRecord(DefaultAgentID, RecL2Topic, 1, []byte("one-updated"))
	// Simulate a crash: close without checkpoint.
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	// Both batches must be visible after reopen.
	if _, data, err := eng2.ReadRecord(DefaultAgentID, 2); err != nil || string(data) != "two" {
		t.Fatalf("record 2: data=%q err=%v", data, err)
	}
	if _, data, err := eng2.ReadRecord(DefaultAgentID, 3); err != nil || string(data) != "three" {
		t.Fatalf("record 3: data=%q err=%v", data, err)
	}
	// Later write for the same idHash wins.
	if _, data, err := eng2.ReadRecord(DefaultAgentID, 1); err != nil || string(data) != "one-updated" {
		t.Fatalf("record 1: data=%q err=%v", data, err)
	}
	if eng2.RecordCount() != 3 {
		t.Fatalf("recordCount: want 3, got %d", eng2.RecordCount())
	}
	// nextOffset must be past the recovered tail: a new write must not
	// clobber recovered records.
	if _, err := eng2.WriteRecord(DefaultAgentID, RecL4Archive, 4, []byte("four")); err != nil {
		t.Fatal(err)
	}
	if _, data, err := eng2.ReadRecord(DefaultAgentID, 3); err != nil || string(data) != "three" {
		t.Fatalf("record 3 after new write: data=%q err=%v", data, err)
	}
	if _, data, err := eng2.ReadRecord(DefaultAgentID, 4); err != nil || string(data) != "four" {
		t.Fatalf("record 4: data=%q err=%v", data, err)
	}
	eng2.Close(&IndexSnapshotData{})
}

func TestCloseNoCheckpointPreservesDiskState(t *testing.T) {
	p := tempPath(t, "nocp")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("a"))
	snap := &IndexSnapshotData{SparseByAgent: map[uint64][]byte{DefaultAgentID: []byte("sparse")}}
	if err := eng.Checkpoint(snap); err != nil {
		t.Fatal(err)
	}
	commitID := eng.activeHeaderRef().CommitID
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close(&IndexSnapshotData{})
	// Header must not have flipped and the snapshot must be intact.
	if got := eng2.activeHeaderRef().CommitID; got != commitID {
		t.Fatalf("commitID: want %d, got %d", commitID, got)
	}
	sd := eng2.SnapshotData()
	if sd == nil || string(sd.SparseByAgent[DefaultAgentID]) != "sparse" {
		t.Fatalf("snapshot lost: %+v", sd)
	}
	if _, data, err := eng2.ReadRecord(DefaultAgentID, 1); err != nil || string(data) != "a" {
		t.Fatalf("record 1: data=%q err=%v", data, err)
	}
}

func TestIndexCallbackMayReadRecord(t *testing.T) {
	p := tempPath(t, "iterlock")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	for i := range uint64(10) {
		eng.WriteRecord(DefaultAgentID, RecL0Profile, i, fmt.Appendf(nil, "v%d", i))
	}
	// Queue a writer during iteration. Under the old callback-based
	// implementation (fn invoked with RLock held) the waiting writer would
	// make fn's recursive RLock deadlock; the iterator copies the index
	// under RLock and yields lock-free, so engine methods stay callable.
	writerDone := make(chan struct{})
	first := true
	for idHash := range eng.Index(DefaultAgentID) {
		if first {
			first = false
			go func() {
				defer close(writerDone)
				eng.WriteRecord(DefaultAgentID, RecL0Profile, 999, []byte("queued"))
			}()
			time.Sleep(50 * time.Millisecond)
		}
		if _, _, err := eng.ReadRecord(DefaultAgentID, idHash); err != nil {
			t.Errorf("ReadRecord(%d): %v", idHash, err)
		}
	}
	<-writerDone
	if !eng.Contains(DefaultAgentID, 999) {
		t.Fatal("queued writer record missing")
	}
	// yield returning false stops iteration.
	count := 0
	for range eng.Index(DefaultAgentID) {
		count++
		if count >= 3 {
			break
		}
	}
	if count != 3 {
		t.Fatalf("early stop: want 3 yields, got %d", count)
	}
	eng.Close(&IndexSnapshotData{})
}
