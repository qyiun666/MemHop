// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// CompactTo is the host's only way back from tombstone-only deletes: it must
// free space, keep every live record, and never be able to destroy a file.
package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestCompactToWritesLiveRecordsOnly(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.meh")
	engine, err := core.Create(live)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close(nil) })
	db := newTestDB(t, engine)
	db.config.DBPath = live

	writeTopic(t, engine, core.DefaultAgentID, newTopic(11, 1, 100, []string{"keep"}))
	writeTopic(t, engine, core.DefaultAgentID, newTopic(12, 1, 200, []string{"drop"}))
	if _, err := engine.DeleteRecordBatch(core.DefaultAgentID, []uint64{12}); err != nil {
		t.Fatal(err)
	}

	// An empty target, the open file (also spelled relatively) and a directory
	// are all refusals: compaction writes a copy or nothing.
	t.Chdir(dir)
	for _, bad := range []string{"", live, filepath.Join(".", filepath.Base(live)), dir} {
		if err := db.CompactTo(bad); common.CodeOf(err) != common.ErrInvalidQuery {
			t.Fatalf("CompactTo(%q): %v, want ErrInvalidQuery", bad, err)
		}
	}

	out := filepath.Join(dir, "compact.meh")
	if err := db.CompactTo(out); err != nil {
		t.Fatalf("CompactTo: %v", err)
	}
	if err := db.CompactTo(out); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("CompactTo over an existing copy: %v", err)
	}
	if liveSize, outSize := fileSize(t, live), fileSize(t, out); outSize >= liveSize {
		t.Fatalf("copy did not reclaim the tombstoned space: live=%d compact=%d", liveSize, outSize)
	}

	reopened, err := core.Open(out)
	if err != nil {
		t.Fatalf("open compact copy: %v", err)
	}
	t.Cleanup(func() { reopened.Close(nil) })
	if n := countRecords(reopened, core.DefaultAgentID, core.RecL2Topic); n != 1 {
		t.Fatalf("compact copy holds %d topics, want only the live one", n)
	}
	kept, err := core.ReadTopicSlot(reopened, core.DefaultAgentID, 11)
	if err != nil || kept.FusedKeywords[0] != "keep" {
		t.Fatalf("live record did not survive compaction: %+v (%v)", kept, err)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}
