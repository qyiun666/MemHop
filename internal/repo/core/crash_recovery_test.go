// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
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

// An append performed after reopening a checkpointed file must survive a
// subsequent crash even without a new checkpoint. Regression test for the
// trimTailSnapshot/nextOffset bug: the old snapshot was not actually
// truncated, so new records landed behind it and were lost on full scan.
func TestAppendAfterReopenWithTailSnapshotSurvivesCrash(t *testing.T) {
	p := tempPath(t, "tailsnap")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteRecord(RecL0Profile, 1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint(&IndexSnapshotData{SparseData: []byte("s1")}); err != nil {
		t.Fatal(err)
	}
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng2.WriteRecord(RecL2Topic, 2, []byte("two")); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash: no checkpoint, no normal Close.
	if err := eng2.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng3, err := Open(p)
	if err != nil {
		t.Fatalf("reopen after crash: %v", err)
	}
	defer eng3.Close(&IndexSnapshotData{})
	if _, data, err := eng3.ReadRecord(1); err != nil || string(data) != "one" {
		t.Fatalf("record 1: data=%q err=%v", data, err)
	}
	if _, data, err := eng3.ReadRecord(2); err != nil || string(data) != "two" {
		t.Fatalf("record 2 after crash: data=%q err=%v", data, err)
	}
}

// Multiple chained tail snapshots must all be dropped before the first
// append after reopen. Trimming only at the latest snapshot offset leaves
// older snapshots behind and recreates the same data-loss window.
func TestAppendAfterReopenWithMultipleTailSnapshotsSurvivesCrash(t *testing.T) {
	p := tempPath(t, "multisnap")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteRecord(RecL0Profile, 1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint(&IndexSnapshotData{SparseData: []byte("s1")}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint(&IndexSnapshotData{SparseData: []byte("s2")}); err != nil {
		t.Fatal(err)
	}
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng2.WriteRecord(RecL2Topic, 2, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := eng2.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng3, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng3.Close(&IndexSnapshotData{})
	for id, want := range map[uint64]string{1: "one", 2: "two"} {
		if _, data, err := eng3.ReadRecord(id); err != nil || string(data) != want {
			t.Fatalf("record %d: data=%q err=%v", id, data, err)
		}
	}
}

// Files written before RecordEnd existed store 0 in that header field. Open
// must reconstruct the record-area end across chained snapshots.
func TestAppendAfterReopenWithLegacyHeaderRecordEnd(t *testing.T) {
	p := tempPath(t, "legacyend")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteRecord(RecL0Profile, 1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint(&IndexSnapshotData{SparseData: []byte("s1")}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint(&IndexSnapshotData{SparseData: []byte("s2")}); err != nil {
		t.Fatal(err)
	}
	// Simulate a pre-RecordEnd file: clear the field in the active header
	// while preserving magic/version/CRC.
	h := eng.activeHeaderRef()
	h.RecordEnd = 0
	h.CRC32 = h.calculateCRC()
	if err := writeHeaderAt(eng.file, int64(eng.activeHeader)*HeaderSize, h.ToBytes()); err != nil {
		t.Fatal(err)
	}
	if err := eng.file.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng2.WriteRecord(RecL2Topic, 2, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := eng2.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng3, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng3.Close(&IndexSnapshotData{})
	for id, want := range map[uint64]string{1: "one", 2: "two"} {
		if _, data, err := eng3.ReadRecord(id); err != nil || string(data) != want {
			t.Fatalf("record %d: data=%q err=%v", id, data, err)
		}
	}
}

// Open must recover when exactly one A/B header is torn or corrupted; the
// dual-header design is only useful if a single bad slot does not block Open.
func TestOpenRecoversWhenOneHeaderCorrupt(t *testing.T) {
	for _, corruptOffset := range []int64{HeaderAOffset, HeaderBOffset} {
		t.Run(fmt.Sprintf("corrupt-header-at-%d", corruptOffset), func(t *testing.T) {
			p := tempPath(t, "onehdr")
			eng, err := Create(p, 768)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := eng.WriteRecord(RecL0Profile, 1, []byte("keep me")); err != nil {
				t.Fatal(err)
			}
			if err := eng.Checkpoint(&IndexSnapshotData{SparseData: []byte("s1")}); err != nil {
				t.Fatal(err)
			}
			if err := eng.CloseNoCheckpoint(); err != nil {
				t.Fatal(err)
			}

			f, err := os.OpenFile(p, os.O_WRONLY, 0644)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteAt([]byte{0, 0, 0, 0}, corruptOffset); err != nil {
				f.Close()
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}

			eng2, err := Open(p)
			if err != nil {
				t.Fatalf("Open with one corrupt header: %v", err)
			}
			if _, data, err := eng2.ReadRecord(1); err != nil || string(data) != "keep me" {
				eng2.CloseNoCheckpoint()
				t.Fatalf("record 1: data=%q err=%v", data, err)
			}
			if err := eng2.Close(&IndexSnapshotData{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Files with an unsupported format version must be rejected explicitly:
// 0x0004 is the legacy L5 plugin-slot format and 0x0005 the previous
// capability schema (neither has a migration path), 0x0007 a future
// version.
func TestHeaderVersionRejected(t *testing.T) {
	for _, v := range []uint16{0x0004, 0x0005, 0x0007} {
		t.Run(fmt.Sprintf("0x%04x", v), func(t *testing.T) {
			p := tempPath(t, "ver")
			eng, err := Create(p, 768)
			if err != nil {
				t.Fatal(err)
			}
			if err := eng.Close(&IndexSnapshotData{}); err != nil {
				t.Fatal(err)
			}
			// Rewrite both headers with the target version (valid CRC).
			h := NewFileHeader(768)
			h.Version = v
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
		})
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
