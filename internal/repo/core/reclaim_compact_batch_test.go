// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Compact rewrites the whole file, and its cost is the flush plus the remap each
// write does. Written one record at a time, 2000 records measured 5.5s here —
// minutes for a file of real size, on the one path an operator runs to get a
// file back under control. The bound is loose against machine variance and tight
// against that regression.
func TestCompactCopiesInBatches(t *testing.T) {
	const records = 2000
	dir := t.TempDir()
	engine, err := Create(filepath.Join(dir, "src.meh"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range records {
		if _, err := engine.WriteRecord(DefaultAgentID, RecL4Archive, uint64(i)+1, []byte("payload")); err != nil {
			t.Fatal(err)
		}
	}

	start := time.Now()
	if err := engine.Compact(filepath.Join(dir, "dst.meh")); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(filepath.Join(dir, "dst.meh"))
	if err != nil {
		t.Fatalf("open compacted file: %v", err)
	}
	defer reopened.Close()
	if got := liveCount(reopened); got != records {
		t.Fatalf("compacted file holds %d records, want %d", got, records)
	}
	for _, id := range []uint64{1, records / 2, records} {
		if _, _, err := reopened.ReadRecord(DefaultAgentID, id); err != nil {
			t.Fatalf("record %d unreadable after compact: %v", id, err)
		}
	}
	fmt.Printf("compact %d records: %v\n", records, elapsed)
	if elapsed > 2*time.Second {
		t.Fatalf("compacting %d records took %v: the copy is flushing and remapping per record again",
			records, elapsed)
	}
}
