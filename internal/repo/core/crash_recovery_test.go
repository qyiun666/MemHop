// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
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

// A crash mid-append leaves the file ending inside a frame. That residue is the
// tail of the log, so Open must cut it and keep appending from a clean end.
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

	// Half of a frame: the header claims more payload than the file holds.
	frame := EncodeRecord(DefaultAgentID, RecL2Topic, 0, 2, []byte("torn victim"))
	appendBytes(t, p, frame[:RecordHeaderSize+len("torn victim")/2])

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

	// A file ending inside the header itself recovers the same way.
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

// Two frame failures are two different accidents. A frame that does not fit the
// file is the log's own tail, cut short: everything the scan reached is intact and
// the residue is garbage. A frame whose bytes disagree with their checksum is
// whole — its header says exactly where the next frame begins — so only that one
// record is lost, and the records written after it are still addressable. Cutting
// the log at a checksum failure deletes them too, and the next checkpoint makes
// that permanent.
func TestChecksumFailedFrameKeepsTheRestOfTheLog(t *testing.T) {
	p := tempPath(t, "rot")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	middle, err := eng.WriteRecord(DefaultAgentID, RecL1SceneNode, 2, []byte("the damaged one"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteRecord(DefaultAgentID, RecL2Topic, 3, []byte("three")); err != nil {
		t.Fatal(err)
	}
	if err := eng.closeNoCheckpoint(); err != nil {
		t.Fatal(err)
	}
	flipByteAt(t, p, middle+RecordHeaderSize)
	before := fileSize(t, p)

	eng2, err := Open(p)
	if err != nil {
		t.Fatalf("a damaged record must not refuse the file: %v", err)
	}
	defer eng2.Close()
	if eng2.Contains(DefaultAgentID, 2) {
		t.Fatal("the checksum-failed record must stay out of the index")
	}
	for _, live := range []uint64{1, 3} {
		if !eng2.Contains(DefaultAgentID, live) {
			t.Fatalf("record %d was taken with the damaged one", live)
		}
	}
	if _, data, err := eng2.ReadRecord(DefaultAgentID, 3); err != nil || string(data) != "three" {
		t.Fatalf("record 3: data=%q err=%v", data, err)
	}
	// The scan walked the whole log, so the append point is past every frame
	// the file holds — including the damaged one, whose bytes stay until a
	// compaction rewrites the record area.
	if _, err := eng2.WriteRecord(DefaultAgentID, RecL2Topic, 4, []byte("four")); err != nil {
		t.Fatal(err)
	}
	if got := fileSize(t, p); got <= before {
		t.Fatalf("append after a skipped frame must grow the log from its real end: %d <= %d", got, before)
	}
}

// The undo half of a failed append. A write the kernel refuses cannot be provoked
// from a test, but what must be true afterwards can: the file is cut back to where
// the batch began, the caller's own cause is still what it reports, and the log
// keeps working from that point.
func TestUndoAppendCutsBackToTheBatchStart(t *testing.T) {
	p := tempPath(t, "undo")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	start := fileSize(t, p)
	// What a refused write leaves behind: a frame the file only partly holds.
	appendBytes(t, p, EncodeRecord(DefaultAgentID, RecL2Topic, 0, 2, []byte("half an attempt"))[:RecordHeaderSize+3])

	cause := common.NewError(common.ErrIO, "write record", io.ErrShortWrite)
	if err := eng.undoAppend(start, cause); !errors.Is(err, cause) {
		t.Fatalf("undo must report the failure that triggered it, got %v", err)
	}
	if got := fileSize(t, p); got != start {
		t.Fatalf("the refused batch is still in the file: size=%d want=%d", got, start)
	}
	if _, err := eng.WriteRecord(DefaultAgentID, RecL2Topic, 3, []byte("three")); err != nil {
		t.Fatalf("the engine must keep appending from the cut: %v", err)
	}
	if err := eng.closeNoCheckpoint(); err != nil {
		t.Fatal(err)
	}
	eng2, err := Open(p)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer eng2.Close()
	if eng2.Contains(DefaultAgentID, 2) || !eng2.Contains(DefaultAgentID, 1) || !eng2.Contains(DefaultAgentID, 3) {
		t.Fatal("records after the undone batch did not survive")
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

// Rotted payload and a rotted length field are two different accidents. A frame
// whose payload disagrees with its checksum still says exactly where the next frame
// begins; a frame whose own length bytes rotted says the wrong thing, so stepping
// over it by that length lands wherever but the next record. The scan then reads
// "this frame does not fit the file" — which is the torn-tail signal — and truncates
// from a point that is not the tail at all, deleting records that were never damaged
// and making the loss permanent at the next checkpoint.
func TestRottedLengthFieldKeepsTheRecordsAfterIt(t *testing.T) {
	p := tempPath(t, "rotlen")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	middle, err := eng.WriteRecord(DefaultAgentID, RecL1SceneNode, 2, []byte("the damaged one"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteRecord(DefaultAgentID, RecL2Topic, 3, []byte("three")); err != nil {
		t.Fatal(err)
	}
	if err := eng.closeNoCheckpoint(); err != nil {
		t.Fatal(err)
	}
	// Byte 2 of the frame is the low byte of its 4-byte length field.
	flipByteAt(t, p, middle+2)
	before := fileSize(t, p)

	eng2, err := Open(p)
	if err != nil {
		t.Fatalf("a rotted length must not refuse the file: %v", err)
	}
	defer eng2.Close()
	if eng2.Contains(DefaultAgentID, 2) {
		t.Fatal("the frame whose checksum failed must stay out of the index")
	}
	for _, live := range []uint64{1, 3} {
		if !eng2.Contains(DefaultAgentID, live) {
			t.Fatalf("record %d was taken with the damaged one", live)
		}
	}
	if _, data, err := eng2.ReadRecord(DefaultAgentID, 3); err != nil || string(data) != "three" {
		t.Fatalf("record 3: data=%q err=%v", data, err)
	}
	// The damaged frame's declared length is a lie of unknown direction, so the
	// scan may not move the append point by it: neither truncating the log nor
	// extending it with a sparse hole is a legal answer here.
	if got := fileSize(t, p); got != before {
		t.Fatalf("file resized by recovery of a rotted length: want %d, got %d", before, got)
	}
}

// A create that cannot take the exclusive lock has to stay away from the file:
// truncating before the lock is taken empties a database another instance is
// reading, and its holder then faults on a mapped page that no longer exists.
func TestCreateRefusesAFileAnotherInstanceHolds(t *testing.T) {
	p := tempPath(t, "create-lock")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("held")); err != nil {
		t.Fatal(err)
	}
	held := fileSize(t, p)

	if _, err := Create(p); err == nil {
		t.Fatal("create on a locked file must be refused")
	} else if !strings.Contains(err.Error(), "already open") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := fileSize(t, p); got != held {
		t.Fatalf("a refused create changed the file: want %d bytes, got %d", held, got)
	}
	if _, data, err := eng.ReadRecord(DefaultAgentID, 1); err != nil || string(data) != "held" {
		t.Fatalf("the holder lost its record: data=%q err=%v", data, err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
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

// flipByteAt rewrites one stored byte, which is what rot inside a frame's payload
// looks like to its checksum: the frame stays the size its header declares.
func flipByteAt(t *testing.T, path string, offset uint64) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b := make([]byte, 1)
	if _, err := f.ReadAt(b, int64(offset)); err != nil {
		t.Fatalf("read at %d: %v", offset, err)
	}
	b[0] ^= 0xFF
	if _, err := f.WriteAt(b, int64(offset)); err != nil {
		t.Fatalf("write at %d: %v", offset, err)
	}
}
