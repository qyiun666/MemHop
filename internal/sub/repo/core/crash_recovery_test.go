// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/sub/common"
)

// A delete must survive a crash (no checkpoint): the tombstone is replayed
// on Open instead of the deleted record silently resurrecting.
func TestTombstoneReplayAfterCrash(t *testing.T) {
	p := tempPath(t, "tomb")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(RecL0Profile, 1, []byte("one"))
	eng.WriteRecord(RecL1SceneNode, 2, []byte("two"))
	if ok, err := eng.DeleteRecord(1); err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	// Simulate a crash: close without checkpoint.
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close(&IndexSnapshotData{})
	if eng2.Contains(1) {
		t.Fatal("deleted record resurrected after reopen")
	}
	if !eng2.Contains(2) {
		t.Fatal("live record lost after reopen")
	}
	if eng2.RecordCount() != 1 {
		t.Fatalf("recordCount: want 1, got %d", eng2.RecordCount())
	}
}

// A delete after a checkpoint must override the snapshotted index entry.
func TestTombstoneReplayOverridesSnapshot(t *testing.T) {
	p := tempPath(t, "tombsnap")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(RecL0Profile, 1, []byte("one"))
	eng.WriteRecord(RecL1SceneNode, 2, []byte("two"))
	if err := eng.Checkpoint(&IndexSnapshotData{}); err != nil {
		t.Fatal(err)
	}
	if ok, err := eng.DeleteRecord(1); err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close(&IndexSnapshotData{})
	if eng2.Contains(1) {
		t.Fatal("tombstone did not override snapshot entry")
	}
	if !eng2.Contains(2) {
		t.Fatal("live record lost after reopen")
	}
}

// A torn tail frame (crash mid-append) must be truncated on Open, not fail it.
func TestTornTailFrameTruncatedOnOpen(t *testing.T) {
	p := tempPath(t, "torn")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(RecL0Profile, 1, []byte("keep me"))
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}
	cleanSize := fileSize(t, p)

	// Append a full frame with a flipped data byte (CRC mismatch) — the
	// classic torn write.
	frame := EncodeRecord(RecL2Topic, 0, 2, []byte("torn victim"))
	frame[len(frame)-1] ^= 0xFF
	appendBytes(t, p, frame)

	eng2, err := Open(p)
	if err != nil {
		t.Fatalf("open after torn write: %v", err)
	}
	if _, data, err := eng2.ReadRecord(1); err != nil || string(data) != "keep me" {
		t.Fatalf("record 1: data=%q err=%v", data, err)
	}
	if eng2.Contains(2) {
		t.Fatal("torn frame must not be indexed")
	}
	// New appends must land on the clean tail, not after the residue.
	if _, err := eng2.WriteRecord(RecL2Topic, 3, []byte("after")); err != nil {
		t.Fatal(err)
	}
	if err := eng2.CloseNoCheckpoint(); err != nil {
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
	defer eng3.Close(&IndexSnapshotData{})
	if !eng3.Contains(1) || !eng3.Contains(3) {
		t.Fatal("live records lost after partial-frame recovery")
	}
}

// A crash between writing the snapshot blob and flipping the header leaves an
// orphan blob at the tail; Open must recover instead of failing forever.
func TestOrphanSnapshotBlobTruncatedOnOpen(t *testing.T) {
	p := tempPath(t, "orphan")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(RecL0Profile, 1, []byte("one"))
	eng.WriteRecord(RecL1SceneNode, 2, []byte("two"))
	if err := eng.Checkpoint(&IndexSnapshotData{SparseData: []byte("s1")}); err != nil {
		t.Fatal(err)
	}
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}
	// Simulate the crash window: snapshot blob synced, header never flipped.
	blob, err := BuildSnapshot(map[uint64]uint64{1: DataStart}, &IndexSnapshotData{SparseData: []byte("s2")})
	if err != nil {
		t.Fatal(err)
	}
	appendBytes(t, p, blob)

	eng2, err := Open(p)
	if err != nil {
		t.Fatalf("open with orphan snapshot blob: %v", err)
	}
	defer eng2.Close(&IndexSnapshotData{})
	if !eng2.Contains(1) || !eng2.Contains(2) {
		t.Fatal("records lost after orphan blob recovery")
	}
	// The committed snapshot (s1) must still be the active one.
	sd := eng2.SnapshotData()
	if sd == nil || string(sd.SparseData) != "s1" {
		t.Fatalf("active snapshot wrong: %+v", sd)
	}
}

// One agent binds one database: a second instance must be rejected.
func TestSecondInstanceRejectedByLock(t *testing.T) {
	p := tempPath(t, "lock")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p); err == nil {
		t.Fatal("second instance must be rejected while first holds the lock")
	} else if !strings.Contains(err.Error(), "already open") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := eng.Close(&IndexSnapshotData{}); err != nil {
		t.Fatal(err)
	}
	// After Close the lock is released and Open succeeds.
	eng2, err := Open(p)
	if err != nil {
		t.Fatalf("open after close: %v", err)
	}
	eng2.Close(&IndexSnapshotData{})
}

// A file with an unsupported format version must be rejected explicitly.
func TestHeaderVersionRejected(t *testing.T) {
	p := tempPath(t, "ver")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(&IndexSnapshotData{}); err != nil {
		t.Fatal(err)
	}
	// Rewrite both headers with a future version (valid CRC).
	h := NewFileHeader(768)
	h.Version = 0x0005
	buf := h.ToBytes()
	f, err := os.OpenFile(p, os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(buf[:], HeaderAOffset); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(buf[:], HeaderBOffset); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, err := Open(p); err == nil {
		t.Fatal("expected version error")
	} else if !strings.Contains(err.Error(), "version") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A snapshot blob with an unsupported version must be rejected explicitly.
func TestSnapshotVersionRejected(t *testing.T) {
	blob, err := BuildSnapshot(map[uint64]uint64{1: DataStart}, &IndexSnapshotData{})
	if err != nil {
		t.Fatal(err)
	}
	blob[4] = 0x7F // tamper version, then fix the CRC
	crc := crc32.ChecksumIEEE(blob[:len(blob)-4])
	binary.LittleEndian.PutUint32(blob[len(blob)-4:], crc)
	if _, _, err := ParseSnapshot(blob); err == nil {
		t.Fatal("expected snapshot version error")
	} else if common.CodeOf(err) != common.ErrCorruption {
		t.Fatalf("unexpected error: %v", err)
	}
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
