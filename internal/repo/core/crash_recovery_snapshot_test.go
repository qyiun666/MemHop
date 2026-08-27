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
	if _, err := eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint(&IndexSnapshotData{SparseByAgent: map[uint64][]byte{DefaultAgentID: []byte("s1")}}); err != nil {
		t.Fatal(err)
	}
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng2.WriteRecord(DefaultAgentID, RecL2Topic, 2, []byte("two")); err != nil {
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
	if _, data, err := eng3.ReadRecord(DefaultAgentID, 1); err != nil || string(data) != "one" {
		t.Fatalf("record 1: data=%q err=%v", data, err)
	}
	if _, data, err := eng3.ReadRecord(DefaultAgentID, 2); err != nil || string(data) != "two" {
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
	if _, err := eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint(&IndexSnapshotData{SparseByAgent: map[uint64][]byte{DefaultAgentID: []byte("s1")}}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint(&IndexSnapshotData{SparseByAgent: map[uint64][]byte{DefaultAgentID: []byte("s2")}}); err != nil {
		t.Fatal(err)
	}
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng2.WriteRecord(DefaultAgentID, RecL2Topic, 2, []byte("two")); err != nil {
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
		if _, data, err := eng3.ReadRecord(DefaultAgentID, id); err != nil || string(data) != want {
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
	if _, err := eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint(&IndexSnapshotData{SparseByAgent: map[uint64][]byte{DefaultAgentID: []byte("s1")}}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint(&IndexSnapshotData{SparseByAgent: map[uint64][]byte{DefaultAgentID: []byte("s2")}}); err != nil {
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
	if _, err := eng2.WriteRecord(DefaultAgentID, RecL2Topic, 2, []byte("two")); err != nil {
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
		if _, data, err := eng3.ReadRecord(DefaultAgentID, id); err != nil || string(data) != want {
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
			if _, err := eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("keep me")); err != nil {
				t.Fatal(err)
			}
			if err := eng.Checkpoint(&IndexSnapshotData{SparseByAgent: map[uint64][]byte{DefaultAgentID: []byte("s1")}}); err != nil {
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
			if _, data, err := eng2.ReadRecord(DefaultAgentID, 1); err != nil || string(data) != "keep me" {
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
// 0x0004 is the legacy L5 plugin-slot format, 0x0005/0x0006 the previous
// capability schemas, 0x0007 the single-agent frame format (none has a
// migration path), 0x0009 a future version.
func TestHeaderVersionRejected(t *testing.T) {
	for _, v := range []uint16{0x0004, 0x0005, 0x0006, 0x0007, 0x0009} {
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
	blob, err := BuildSnapshot(map[uint64]map[uint64]uint64{DefaultAgentID: {1: DataStart}}, &IndexSnapshotData{})
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
