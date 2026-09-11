// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"os"
	"testing"
)

// TestTrimTailSnapshotOnWrite verifies the write-path invariant: the first
// write after a checkpoint drops the tail snapshot, so snapshots never
// accumulate across checkpoint+write cycles.
func TestTrimTailSnapshotOnWrite(t *testing.T) {
	p := tempPath(t, "trim_tail")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("first"))
	if err := eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	eng.Checkpoint() // second snapshot piles up at the tail
	sizeWithSnaps := eng.FileSize()

	// The first write after checkpoints must drop all tail snapshots.
	eng.WriteRecord(DefaultAgentID, RecL2Topic, 2, []byte("second"))
	sizeAfterWrite := eng.FileSize()
	if sizeAfterWrite >= sizeWithSnaps {
		t.Fatalf("tail snapshots not trimmed on write: with=%d after=%d",
			sizeWithSnaps, sizeAfterWrite)
	}
	// Sanity: file is now exactly the two record frames.
	if want := uint64(DataStart) + 2*uint64(RecordHeaderSize) + uint64(len("first")+len("second")); sizeAfterWrite != want {
		t.Fatalf("file size after trim: got %d, want %d", sizeAfterWrite, want)
	}
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	if eng2.RecordCount() != 2 {
		t.Fatalf("recordCount: want 2, got %d", eng2.RecordCount())
	}
	if _, data, err := eng2.ReadRecord(DefaultAgentID, 2); err != nil || string(data) != "second" {
		t.Fatalf("record 2: got %q err=%v", data, err)
	}
}

// TestOpenAfterTrimTruncateWindow simulates a crash inside trimTailSnapshot:
// the tail is truncated but the null header is not written yet, so the active
// header points at a snapshot that is no longer there and Open must fall back
// to a full scan.
func TestOpenAfterTrimTruncateWindow(t *testing.T) {
	p := tempPath(t, "trim_window")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("first"))
	eng.WriteRecord(DefaultAgentID, RecL2Topic, 2, []byte("second"))
	if err := eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	snapOff := int64(eng.activeHeaderRef().SnapshotOffset)
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}
	// Simulate the crash: file truncated to the data-region end, header intact.
	if err := os.Truncate(p, snapOff); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatalf("open in reclaim truncate window: %v", err)
	}
	defer eng2.Close()
	if eng2.RecordCount() != 2 {
		t.Fatalf("recordCount: want 2, got %d", eng2.RecordCount())
	}
	if _, data, err := eng2.ReadRecord(DefaultAgentID, 1); err != nil || string(data) != "first" {
		t.Fatalf("record 1: got %q err=%v", data, err)
	}
}

// TestOpenFallsBackToFullScanOnCorruptSnapshot verifies that a corrupted
// snapshot blob no longer makes the file unopenable: Open falls back to a
// full scan and truncates the residue.
func TestOpenFallsBackToFullScanOnCorruptSnapshot(t *testing.T) {
	p := tempPath(t, "corrupt_snap")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("first"))
	eng.WriteRecord(DefaultAgentID, RecL2Topic, 2, []byte("second"))
	if err := eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	snapOff := int64(eng.activeHeaderRef().SnapshotOffset)
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}
	// Flip one byte inside the snapshot blob.
	f, err := os.OpenFile(p, os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if _, err := f.ReadAt(buf, snapOff); err != nil {
		f.Close()
		t.Fatal(err)
	}
	buf[0] ^= 0xFF
	if _, err := f.WriteAt(buf, snapOff); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatalf("open with corrupt snapshot: %v", err)
	}
	defer eng2.Close()
	if eng2.RecordCount() != 2 {
		t.Fatalf("recordCount: want 2, got %d", eng2.RecordCount())
	}
	if _, data, err := eng2.ReadRecord(DefaultAgentID, 2); err != nil || string(data) != "second" {
		t.Fatalf("record 2: got %q err=%v", data, err)
	}
	// The snapshot residue must have been truncated away on open.
	if off := eng2.activeHeaderRef().SnapshotOffset; off != 0 {
		t.Fatalf("snapshot residue not truncated: header still points at %d", off)
	}
}
