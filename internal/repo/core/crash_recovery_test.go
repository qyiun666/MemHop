// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"os"
	"strings"
	"testing"
)

// A delete must survive a crash (no checkpoint): the tombstone is replayed
// on Open instead of the deleted record silently resurrecting.
func TestTombstoneReplayAfterCrash(t *testing.T) {
	p := tempPath(t, "tomb")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("one"))
	eng.WriteRecord(DefaultAgentID, RecL1SceneNode, 2, []byte("two"))
	if ok, err := eng.DeleteRecord(DefaultAgentID, 1); err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	// Simulate a crash: close without checkpoint.
	if err := eng.closeNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	if eng2.Contains(DefaultAgentID, 1) {
		t.Fatal("deleted record resurrected after reopen")
	}
	if !eng2.Contains(DefaultAgentID, 2) {
		t.Fatal("live record lost after reopen")
	}
	if liveCount(eng2) != 1 {
		t.Fatalf("recordCount: want 1, got %d", liveCount(eng2))
	}
}

// A delete after a checkpoint must override the snapshotted index entry.
func TestTombstoneReplayOverridesSnapshot(t *testing.T) {
	p := tempPath(t, "tombsnap")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("one"))
	eng.WriteRecord(DefaultAgentID, RecL1SceneNode, 2, []byte("two"))
	if err := eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if ok, err := eng.DeleteRecord(DefaultAgentID, 1); err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	if err := eng.closeNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	if eng2.Contains(DefaultAgentID, 1) {
		t.Fatal("tombstone did not override snapshot entry")
	}
	if !eng2.Contains(DefaultAgentID, 2) {
		t.Fatal("live record lost after reopen")
	}
}

// A torn tail frame (crash mid-append) must be truncated on Open, not fail it.
func TestTornTailFrameTruncatedOnOpen(t *testing.T) {
	p := tempPath(t, "torn")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("keep me"))
	if err := eng.closeNoCheckpoint(); err != nil {
		t.Fatal(err)
	}
	cleanSize := fileSize(t, p)

	// Append a full frame with a flipped data byte (CRC mismatch) — the
	// classic torn write.
	frame := EncodeRecord(DefaultAgentID, RecL2Topic, 0, 2, []byte("torn victim"))
	frame[len(frame)-1] ^= 0xFF
	appendBytes(t, p, frame)

	eng2, err := Open(p)
	if err != nil {
		t.Fatalf("open after torn write: %v", err)
	}
	if _, data, err := eng2.ReadRecord(DefaultAgentID, 1); err != nil || string(data) != "keep me" {
		t.Fatalf("record 1: data=%q err=%v", data, err)
	}
	if eng2.Contains(DefaultAgentID, 2) {
		t.Fatal("torn frame must not be indexed")
	}
	// New appends must land on the clean tail, not after the residue.
	if _, err := eng2.WriteRecord(DefaultAgentID, RecL2Topic, 3, []byte("after")); err != nil {
		t.Fatal(err)
	}
	if err := eng2.closeNoCheckpoint(); err != nil {
		t.Fatal(err)
	}
	if got := fileSize(t, p); got != cleanSize+int64(RecordHeaderSize+len("after")) {
		t.Fatalf("residue not truncated: size=%d cleanSize=%d", got, cleanSize)
	}

	// A partially written frame (file ends mid-header) recovers the same way.
	appendBytes(t, p, []byte{0xAB, 0xCD, 0xEF})
	eng3, err := Open(p)
	if err != nil {
		t.Fatalf("open after partial frame: %v", err)
	}
	defer eng3.Close()
	if !eng3.Contains(DefaultAgentID, 1) || !eng3.Contains(DefaultAgentID, 3) {
		t.Fatal("live records lost after partial-frame recovery")
	}
}

// A crash between writing the snapshot blob and flipping the header leaves an
// orphan blob at the tail; Open must recover instead of failing forever.
func TestOrphanSnapshotBlobTruncatedOnOpen(t *testing.T) {
	p := tempPath(t, "orphan")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("one"))
	eng.WriteRecord(DefaultAgentID, RecL1SceneNode, 2, []byte("two"))
	if err := eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	committedOff := eng.activeHeaderRef().SnapshotOffset
	if err := eng.closeNoCheckpoint(); err != nil {
		t.Fatal(err)
	}
	// Simulate the crash window: snapshot blob synced, header never flipped.
	blob := BuildSnapshot(map[uint64]map[uint64]uint64{DefaultAgentID: {1: DataStart}})
	appendBytes(t, p, blob)

	eng2, err := Open(p)
	if err != nil {
		t.Fatalf("open with orphan snapshot blob: %v", err)
	}
	defer eng2.Close()
	if !eng2.Contains(DefaultAgentID, 1) || !eng2.Contains(DefaultAgentID, 2) {
		t.Fatal("records lost after orphan blob recovery")
	}
	// The committed snapshot is still the active one: the orphan appended
	// behind it was recognised and truncated rather than adopted. Adopting it
	// would move this offset and lose record 2, which its index never named.
	if got := eng2.activeHeaderRef().SnapshotOffset; got != committedOff {
		t.Fatalf("active snapshot moved: want %d, got %d", committedOff, got)
	}
}

// One agent binds one database: a second instance must be rejected.
func TestSecondInstanceRejectedByLock(t *testing.T) {
	p := tempPath(t, "lock")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p); err == nil {
		t.Fatal("second instance must be rejected while first holds the lock")
	} else if !strings.Contains(err.Error(), "already open") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	// After Close the lock is released and Open succeeds.
	eng2, err := Open(p)
	if err != nil {
		t.Fatalf("open after close: %v", err)
	}
	eng2.Close()
}

// appendBytes appends raw bytes to the file, simulating crash residue.
func appendBytes(t *testing.T, path string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
