// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"os"
	"testing"
)

// TestCheckpointReclaim verifies that CheckpointReclaim collapses the file
// to a single snapshot without losing any records.
func TestCheckpointReclaim(t *testing.T) {
	p := tempPath(t, "reclaim")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("first"))
	eng.WriteRecord(DefaultAgentID, RecL2Topic, 2, []byte("second"))
	snap := &IndexSnapshotData{SparseByAgent: map[uint64][]byte{DefaultAgentID: []byte("sparse")}}
	// Consecutive checkpoints without writes pile up snapshots at the tail.
	for i := range ReclaimMinSnapshots {
		if err := eng.Checkpoint(snap); err != nil {
			t.Fatalf("checkpoint %d: %v", i, err)
		}
	}
	sizeBefore := eng.FileSize()

	got, err := eng.CheckpointReclaim(snap)
	if err != nil {
		t.Fatal(err)
	}
	if got != snap {
		t.Error("CheckpointReclaim must return the snapshot it wrote")
	}
	sizeAfter := eng.FileSize()
	if sizeAfter >= sizeBefore {
		t.Fatalf("reclaim did not shrink file: before=%d after=%d", sizeBefore, sizeAfter)
	}
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close(&IndexSnapshotData{})
	if eng2.RecordCount() != 2 {
		t.Fatalf("recordCount: want 2, got %d", eng2.RecordCount())
	}
	for id, want := range map[uint64]string{1: "first", 2: "second"} {
		_, data, err := eng2.ReadRecord(DefaultAgentID, id)
		if err != nil || string(data) != want {
			t.Errorf("record %d: got %q err=%v, want %q", id, data, err, want)
		}
	}
	if sd := eng2.SnapshotData(); sd == nil || string(sd.SparseByAgent[DefaultAgentID]) != "sparse" {
		t.Fatalf("reclaimed snapshot lost: %+v", sd)
	}
}

// TestCheckpointReclaimSkipsFewSnapshots verifies that Reclaim returns early
// without touching the file when the snapshot count is below the threshold.
func TestCheckpointReclaimSkipsFewSnapshots(t *testing.T) {
	p := tempPath(t, "reclaim_skip")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("first"))
	snap := &IndexSnapshotData{SparseByAgent: map[uint64][]byte{DefaultAgentID: []byte("sparse")}}
	for i := range ReclaimMinSnapshots - 1 {
		if err := eng.Checkpoint(snap); err != nil {
			t.Fatalf("checkpoint %d: %v", i, err)
		}
	}
	sizeBefore := eng.FileSize()

	got, err := eng.CheckpointReclaim(snap)
	if err != nil {
		t.Fatal(err)
	}
	if got != snap {
		t.Error("skipped reclaim must still return the snapshot")
	}
	if sizeAfter := eng.FileSize(); sizeAfter != sizeBefore {
		t.Fatalf("file changed below threshold: before=%d after=%d", sizeBefore, sizeAfter)
	}
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close(&IndexSnapshotData{})
	if eng2.RecordCount() != 1 {
		t.Fatalf("recordCount: want 1, got %d", eng2.RecordCount())
	}
}

// TestTrimTailSnapshotOnWrite verifies the write-path invariant: the first
// write after a checkpoint drops the tail snapshot, so snapshots never
// accumulate across checkpoint+write cycles.
func TestTrimTailSnapshotOnWrite(t *testing.T) {
	p := tempPath(t, "trim_tail")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("first"))
	snap := &IndexSnapshotData{SparseByAgent: map[uint64][]byte{DefaultAgentID: []byte("sparse")}}
	if err := eng.Checkpoint(snap); err != nil {
		t.Fatal(err)
	}
	eng.Checkpoint(snap) // second snapshot piles up at the tail
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
	defer eng2.Close(&IndexSnapshotData{})
	if eng2.RecordCount() != 2 {
		t.Fatalf("recordCount: want 2, got %d", eng2.RecordCount())
	}
	if _, data, err := eng2.ReadRecord(DefaultAgentID, 2); err != nil || string(data) != "second" {
		t.Fatalf("record 2: got %q err=%v", data, err)
	}
}

// TestOpenAfterReclaimTruncateWindow simulates a crash after the reclaim
// truncate but before the null header write: the active header points at the
// deleted snapshot, and Open must fall back to a full scan.
func TestOpenAfterReclaimTruncateWindow(t *testing.T) {
	p := tempPath(t, "reclaim_window")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("first"))
	eng.WriteRecord(DefaultAgentID, RecL2Topic, 2, []byte("second"))
	if err := eng.Checkpoint(&IndexSnapshotData{SparseByAgent: map[uint64][]byte{DefaultAgentID: []byte("sparse")}}); err != nil {
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
	defer eng2.Close(&IndexSnapshotData{})
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
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("first"))
	eng.WriteRecord(DefaultAgentID, RecL2Topic, 2, []byte("second"))
	if err := eng.Checkpoint(&IndexSnapshotData{SparseByAgent: map[uint64][]byte{DefaultAgentID: []byte("sparse")}}); err != nil {
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
	defer eng2.Close(&IndexSnapshotData{})
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
