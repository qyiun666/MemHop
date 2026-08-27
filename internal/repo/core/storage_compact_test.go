// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"testing"
)

func TestCompact(t *testing.T) {
	p := tempPath(t, "compact_src")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close(&IndexSnapshotData{}) })
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("keep"))
	eng.WriteRecord(DefaultAgentID, RecL1SceneNode, 2, []byte("delete me"))
	eng.WriteRecord(DefaultAgentID, RecL2Topic, 3, []byte("also keep"))
	eng.WriteRecord(DefaultAgentID, RecL3GraphNode, 4, []byte("keep too"))
	eng.WriteRecord(DefaultAgentID, RecL4Archive, 5, []byte("remove"))
	eng.DeleteRecord(DefaultAgentID, 2)
	eng.DeleteRecord(DefaultAgentID, 5)
	// Checkpoint so original has a snapshot (fair comparison).
	eng.Checkpoint(&IndexSnapshotData{})

	compactPath := tempPath(t, "compact_dst")
	snap := &IndexSnapshotData{SparseByAgent: map[uint64][]byte{DefaultAgentID: []byte("sparse")}}
	if err := eng.Compact(compactPath, snap); err != nil {
		t.Fatal(err)
	}

	// Open compacted file.
	eng2, err := Open(compactPath)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close(&IndexSnapshotData{})
	if eng2.RecordCount() != 3 {
		t.Fatalf("compact count: %d", eng2.RecordCount())
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
	// The caller-provided snapshot must be carried into the compacted file.
	sd := eng2.SnapshotData()
	if sd == nil || string(sd.SparseByAgent[DefaultAgentID]) != "sparse" {
		t.Fatalf("compacted snapshot lost: %+v", sd)
	}
	// A nil snapshot is a caller bug, not a silent empty checkpoint.
	if err := eng.Compact(tempPath(t, "compact_nil"), nil); err == nil {
		t.Fatal("expected error for nil snapshot")
	}

	// Compacted file should be smaller (fewer records + no dead records).
	origSize := fileSize(t, p)
	compactSize := fileSize(t, compactPath)
	if compactSize >= origSize {
		t.Fatalf("compact not smaller: orig=%d compact=%d", origSize, compactSize)
	}
}
