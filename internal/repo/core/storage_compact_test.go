// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

func TestCompact(t *testing.T) {
	p := tempPath(t, "compact_src")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("keep"))
	eng.WriteRecord(DefaultAgentID, RecL1SceneNode, 2, []byte("delete me"))
	eng.WriteRecord(DefaultAgentID, RecL2Topic, 3, []byte("also keep"))
	eng.WriteRecord(DefaultAgentID, RecL3GraphNode, 4, []byte("keep too"))
	eng.WriteRecord(DefaultAgentID, RecL4Archive, 5, []byte("remove"))
	eng.DeleteRecord(DefaultAgentID, 2)
	eng.DeleteRecord(DefaultAgentID, 5)
	// Checkpoint so original has a snapshot (fair comparison).
	eng.Checkpoint()

	compactPath := tempPath(t, "compact_dst")
	if err := eng.Compact(compactPath); err != nil {
		t.Fatal(err)
	}

	// Open compacted file.
	eng2, err := Open(compactPath)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	if liveCount(eng2) != 3 {
		t.Fatalf("compact count: %d", liveCount(eng2))
	}
	_, data, err := eng2.ReadRecord(DefaultAgentID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("data: %q", data)
	}
	_, _, err = eng2.ReadRecord(DefaultAgentID, 2)
	if err == nil {
		t.Fatal("expected not found in compacted file")
	}
	_, data3, err := eng2.ReadRecord(DefaultAgentID, 3)
	if err != nil || string(data3) != "also keep" {
		t.Fatal("record 3 missing or wrong")
	}
	// The compacted file carries its own snapshot, so opening it restores the
	// index from that snapshot rather than scanning the whole log.
	if eng2.activeHeaderRef().SnapshotOffset == 0 {
		t.Fatal("compacted file has no snapshot")
	}

	// Compacted file should be smaller (fewer records + no dead records).
	origSize := fileSize(t, p)
	compactSize := fileSize(t, compactPath)
	if compactSize >= origSize {
		t.Fatalf("compact not smaller: orig=%d compact=%d", origSize, compactSize)
	}
}

// A compaction refuses to rewrite a file it cannot read whole, and the refusal is
// the only place a host learns which record is damaged: the engine has no read face
// that shows one. A checksum failure and a frame that does not fit the file are
// different repairs, so the read's own code is what comes back.
func TestCompactRefusalNamesTheRecordItCannotRead(t *testing.T) {
	p := tempPath(t, "compact_rot")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	victim, err := eng.WriteRecord(DefaultAgentID, RecL1SceneNode, 7, []byte("this one rots"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteRecord(DefaultAgentID, RecL2Topic, 8, []byte("fine")); err != nil {
		t.Fatal(err)
	}
	flipByteAt(t, p, victim+RecordHeaderSize)

	err = eng.Compact(tempPath(t, "compact_dst"))
	if err == nil {
		t.Fatal("a compaction over a record it cannot read must refuse")
	}
	if common.CodeOf(err) != common.ErrCRCMismatch {
		t.Fatalf("want the read's own classification, got %v", err)
	}
	if !strings.Contains(err.Error(), common.FormatHash(7)) {
		t.Fatalf("the refusal must name the record, got %v", err)
	}
}
