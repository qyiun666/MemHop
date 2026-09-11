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
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := eng.closeNoCheckpoint(); err != nil {
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
	if err := eng2.closeNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng3, err := Open(p)
	if err != nil {
		t.Fatalf("reopen after crash: %v", err)
	}
	defer eng3.Close()
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
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := eng.closeNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng2.WriteRecord(DefaultAgentID, RecL2Topic, 2, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := eng2.closeNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng3, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng3.Close()
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
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint(); err != nil {
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
	if err := eng.closeNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng2.WriteRecord(DefaultAgentID, RecL2Topic, 2, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := eng2.closeNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng3, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng3.Close()
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
			eng, err := Create(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("keep me")); err != nil {
				t.Fatal(err)
			}
			if err := eng.Checkpoint(); err != nil {
				t.Fatal(err)
			}
			if err := eng.closeNoCheckpoint(); err != nil {
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
				eng2.closeNoCheckpoint()
				t.Fatalf("record 1: data=%q err=%v", data, err)
			}
			if err := eng2.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Files with an unsupported format version must be rejected explicitly at
// Open: 0x0012 (a domain's identity on its L0 profile, a topic name its host
// writes) is the only accepted version — older layouts and future ones have no
// migration path.
// The rejection names both versions it saw and wanted, so this asserts the pair
// per case: "an error mentioning a version" would also pass on an unrelated
// failure, and a stale FormatVersion would otherwise go unnoticed. The list runs
// one past the current version so a bump that forgets to move the boundary fails
// here rather than silently accepting whatever comes next.
func TestHeaderVersionRejected(t *testing.T) {
	for _, v := range []uint16{0x0004, 0x0005, 0x0006, 0x0007, 0x0008, 0x0009, 0x000A, 0x000B, 0x000C, 0x000D, 0x000E, 0x000F, 0x0010, 0x0011, 0x0013} {
		t.Run(fmt.Sprintf("0x%04x", v), func(t *testing.T) {
			p := tempPath(t, "ver")
			eng, err := Create(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := eng.Close(); err != nil {
				t.Fatal(err)
			}
			// Rewrite both headers with the target version (valid CRC).
			h := NewFileHeader()
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

			want := fmt.Sprintf("unsupported file format version 0x%04x (expected 0x%04x)", v, FormatVersion)
			if _, err := Open(p); err == nil {
				t.Fatal("expected version error")
			} else if !strings.Contains(err.Error(), want) {
				t.Fatalf("want %q, got %v", want, err)
			}
		})
	}
}

// A snapshot blob with an unsupported version must be rejected explicitly,
// while the version this build writes parses.
func TestSnapshotVersionRejected(t *testing.T) {
	blob := BuildSnapshot(map[uint64]map[uint64]uint64{DefaultAgentID: {1: DataStart}})
	if blob[4] != SnapshotVersion {
		t.Fatalf("blob carries 0x%02x, want the current 0x%02x", blob[4], SnapshotVersion)
	}
	if _, err := ParseSnapshot(blob); err != nil {
		t.Fatalf("the version this build writes must parse: %v", err)
	}
	blob[4] = 0x7F // tamper version, then fix the CRC
	crc := crc32.ChecksumIEEE(blob[:len(blob)-4])
	binary.LittleEndian.PutUint32(blob[len(blob)-4:], crc)
	if _, err := ParseSnapshot(blob); err == nil {
		t.Fatal("expected snapshot version error")
	} else if common.CodeOf(err) != common.ErrCorruption {
		t.Fatalf("unexpected error: %v", err)
	}
}
