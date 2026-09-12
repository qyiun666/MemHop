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
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	// First batch, then checkpoint.
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("one"))
	eng.WriteRecord(DefaultAgentID, RecL1SceneNode, 2, []byte("two"))
	if err := eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	// Second batch appended after the checkpoint (includes an overwrite).
	eng.WriteRecord(DefaultAgentID, RecL2Topic, 3, []byte("three"))
	eng.WriteRecord(DefaultAgentID, RecL2Topic, 1, []byte("one-updated"))
	// Simulate a crash: close without checkpoint.
	if err := eng.closeNoCheckpoint(); err != nil {
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
	if liveCount(eng2) != 3 {
		t.Fatalf("recordCount: want 3, got %d", liveCount(eng2))
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
	eng2.Close()
}

func TestCloseNoCheckpointPreservesDiskState(t *testing.T) {
	p := tempPath(t, "nocp")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("a"))
	if err := eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	hdr := eng.activeHeaderRef()
	commitID, snapOff, snapLen := hdr.CommitID, hdr.SnapshotOffset, hdr.SnapshotLength
	if err := eng.closeNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	// The header must not have flipped, and the snapshot must still be where
	// that header says it is: an unreadable snapshot is cleared and replaced by
	// a full scan, so a surviving pointer means Open consumed it.
	hdr2 := eng2.activeHeaderRef()
	if hdr2.CommitID != commitID {
		t.Fatalf("commitID: want %d, got %d", commitID, hdr2.CommitID)
	}
	if hdr2.SnapshotOffset != snapOff || hdr2.SnapshotLength != snapLen {
		t.Fatalf("snapshot pointer moved: want off=%d len=%d, got off=%d len=%d",
			snapOff, snapLen, hdr2.SnapshotOffset, hdr2.SnapshotLength)
	}
	if _, data, err := eng2.ReadRecord(DefaultAgentID, 1); err != nil || string(data) != "a" {
		t.Fatalf("record 1: data=%q err=%v", data, err)
	}
}

// Snapshot iteration has to stay callable: a scan that yielded with the read lock
// held would deadlock the moment the body called another engine method, because a
// waiting writer blocks further RLocks. The id list is copied under the lock and the
// iteration runs lock-free, which is what every CollectAll* read path relies on.
func TestIndexCallbackMayReadRecord(t *testing.T) {
	p := tempPath(t, "iterlock")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	for i := range uint64(10) {
		eng.WriteRecord(DefaultAgentID, RecL0Profile, i, fmt.Appendf(nil, "v%d", i))
	}
	// Queue a writer while the iteration is in progress, then keep calling engine
	// methods from inside it.
	writerDone := make(chan struct{})
	first := true
	for idHash := range eng.IndexByType(DefaultAgentID, RecL0Profile) {
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
	// Breaking out of the range stops iteration.
	count := 0
	for range eng.IndexByType(DefaultAgentID, RecL0Profile) {
		count++
		if count >= 3 {
			break
		}
	}
	if count != 3 {
		t.Fatalf("early stop: want 3 yields, got %d", count)
	}
	eng.Close()
}
