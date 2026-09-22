// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

// helper: temp path for a .meh file
// liveCount reads the number of live records off the index under the lock the
// index itself needs: the engine keeps no counter beside it.
func liveCount(e *StorageEngine) uint32 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return uint32(e.totalRecordsLocked())
}

func tempPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name+".meh")
}

func TestCreateWriteRead(t *testing.T) {
	p := tempPath(t, "cwr")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	data := []byte("hello storage engine")
	offset, err := eng.WriteRecord(DefaultAgentID, RecL0Profile, 12345, data)
	if err != nil {
		t.Fatal(err)
	}
	if offset != DataStart {
		t.Fatalf("expected offset %d, got %d", DataStart, offset)
	}
	rt, got, err := eng.ReadRecord(DefaultAgentID, 12345)
	if err != nil {
		t.Fatal(err)
	}
	if rt != RecL0Profile {
		t.Fatalf("record type: want %d, got %d", RecL0Profile, rt)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("data mismatch: %q vs %q", got, data)
	}
	// NotFound
	_, _, err = eng.ReadRecord(DefaultAgentID, 99999)
	if err == nil {
		t.Fatal("expected error for missing record")
	}
}

func TestABHeaderSwitch(t *testing.T) {
	p := tempPath(t, "ab")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	eng.WriteRecord(DefaultAgentID, RecL1SceneNode, 1, []byte("a"))
	for range 4 {
		if err := eng.Checkpoint(); err != nil {
			t.Fatal(err)
		}
	}
	// After 4 checkpoints, commitID should be 4 and active header should alternate.
	active := eng.activeHeaderRef()
	if active.CommitID != 4 {
		t.Fatalf("commitID: want 4, got %d", active.CommitID)
	}
	if eng.activeHeader != 0 { // even number → back to A
		t.Fatalf("activeHeader: want 0, got %d", eng.activeHeader)
	}
}

func TestCheckpointReopen(t *testing.T) {
	p := tempPath(t, "ckp")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(DefaultAgentID, RecL2Topic, 100, []byte("checkpoint data"))
	if err := eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	eng.Close()

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	if liveCount(eng2) != 1 {
		t.Fatalf("recordCount: want 1, got %d", liveCount(eng2))
	}
	rt, data, err := eng2.ReadRecord(DefaultAgentID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if rt != RecL2Topic {
		t.Fatalf("rt: want %d, got %d", RecL2Topic, rt)
	}
	if string(data) != "checkpoint data" {
		t.Fatalf("data: %q", data)
	}
	// The index came back from the checkpoint's snapshot, not from a full scan:
	// a snapshot Open cannot read is cleared, leaving the header pointing at
	// nothing.
	if eng2.activeHeaderRef().SnapshotOffset == 0 {
		t.Fatal("snapshot was not consumed; Open fell back to a full scan")
	}
}

func TestDeleteRecord(t *testing.T) {
	p := tempPath(t, "del")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("first"))
	eng.WriteRecord(DefaultAgentID, RecL1SceneNode, 2, []byte("second"))
	eng.WriteRecord(DefaultAgentID, RecL2Topic, 3, []byte("third"))

	ok, err := eng.DeleteRecord(DefaultAgentID, 2)
	if err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	_, _, err = eng.ReadRecord(DefaultAgentID, 2)
	if err == nil {
		t.Fatal("expected not found after delete")
	}
	if liveCount(eng) != 2 {
		t.Fatalf("count: %d", liveCount(eng))
	}
	// Others still readable.
	if _, _, err := eng.ReadRecord(DefaultAgentID, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := eng.ReadRecord(DefaultAgentID, 3); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteRecordBatch(t *testing.T) {
	p := tempPath(t, "del_batch")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("first"))
	eng.WriteRecord(DefaultAgentID, RecL1SceneNode, 2, []byte("second"))
	eng.WriteRecord(DefaultAgentID, RecL2Topic, 3, []byte("third"))
	eng.WriteRecord(DefaultAgentID, RecL2Scene, 4, []byte("scene"))

	// Mixed existing/missing ids: missing ones are skipped without affecting the result
	n, err := eng.DeleteRecordBatch(DefaultAgentID, []uint64{1, 2, 99})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 deleted, got %d", n)
	}
	if liveCount(eng) != 2 {
		t.Fatalf("count: %d", liveCount(eng))
	}
	// Deleted ones unreadable, remaining ones readable
	for _, id := range []uint64{1, 2} {
		if _, _, err := eng.ReadRecord(DefaultAgentID, id); err == nil {
			t.Errorf("record %d should be deleted", id)
		}
	}
	if _, _, err := eng.ReadRecord(DefaultAgentID, 3); err != nil {
		t.Error("record 3 should survive")
	}
	if _, _, err := eng.ReadRecord(DefaultAgentID, 4); err != nil {
		t.Error("record 4 should survive")
	}
	// All missing returns 0
	n, err = eng.DeleteRecordBatch(DefaultAgentID, []uint64{99})
	if err != nil || n != 0 {
		t.Fatalf("missing-only batch: n=%d err=%v", n, err)
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	p := tempPath(t, "conc")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	// Seed some records.
	for i := range uint64(100) {
		eng.WriteRecord(DefaultAgentID, RecL0Profile, i, fmt.Appendf(nil, "seed-%d", i))
	}
	var wg sync.WaitGroup
	// Concurrent readers.
	for range 5 {
		wg.Go(func() {
			for i := range uint64(100) {
				eng.ReadRecord(DefaultAgentID, i)
			}
		})
	}
	// Concurrent writers (base 1,2,3 to avoid overlapping with seed 0-99).
	for g := 1; g <= 3; g++ {
		wg.Add(1)
		go func(base uint64) {
			defer wg.Done()
			for i := range uint64(50) {
				eng.WriteRecord(DefaultAgentID, RecL2Topic, base*1000+i, fmt.Appendf(nil, "w-%d", i))
			}
		}(uint64(g))
	}
	wg.Wait()
	// Verify all seed records intact.
	for i := range uint64(100) {
		if !eng.Contains(DefaultAgentID, i) {
			t.Fatalf("seed record %d missing", i)
		}
	}
	if liveCount(eng) != 100+3*50 {
		t.Fatalf("count: %d", liveCount(eng))
	}
}

func TestWriteRecordBatch(t *testing.T) {
	p := tempPath(t, "batch")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	records := []RecordEntry{
		{RecordType: RecL0Profile, IDHash: 10, Data: []byte("ten")},
		{RecordType: RecL1SceneNode, IDHash: 20, Data: []byte("twenty")},
		{RecordType: RecL2Topic, IDHash: 30, Data: []byte("thirty")},
	}
	offsets, err := eng.WriteRecordBatch(records)
	if err != nil {
		t.Fatal(err)
	}
	if len(offsets) != 3 {
		t.Fatalf("offsets len: %d", len(offsets))
	}
	for _, rec := range records {
		rt, data, err := eng.ReadRecord(DefaultAgentID, rec.IDHash)
		if err != nil {
			t.Fatal(err)
		}
		if rt != rec.RecordType || string(data) != string(rec.Data) {
			t.Fatalf("mismatch for %d", rec.IDHash)
		}
	}
}

func TestFileSize(t *testing.T) {
	p := tempPath(t, "size")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	if eng.mappedSize() != DataStart {
		t.Fatalf("initial size: %d", eng.mappedSize())
	}
}

func TestContainsAndIndex(t *testing.T) {
	p := tempPath(t, "iter")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	eng.WriteRecord(DefaultAgentID, RecL0Profile, 42, []byte("x"))
	if !eng.Contains(DefaultAgentID, 42) {
		t.Fatal("should contain 42")
	}
	if eng.Contains(DefaultAgentID, 99) {
		t.Fatal("should not contain 99")
	}
	count := 0
	for range eng.IndexByType(DefaultAgentID, RecL0Profile) {
		count++
	}
	if count != 1 {
		t.Fatalf("iter count: %d", count)
	}
}

// The closed-engine contract: every operation answers with the ErrClosed code
// — read, write, batch delete, checkpoint, a second Close — and the snapshot
// iterators yield nothing.
func TestClosedEngineAnswersErrClosed(t *testing.T) {
	p := tempPath(t, "closed")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteRecord(DefaultAgentID, RecL2Topic, 1, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := eng.ReadRecord(DefaultAgentID, 1); common.CodeOf(err) != common.ErrClosed {
		t.Fatalf("ReadRecord after Close: %v", err)
	}
	if _, err := eng.WriteRecord(DefaultAgentID, RecL2Topic, 2, []byte("y")); common.CodeOf(err) != common.ErrClosed {
		t.Fatalf("WriteRecord after Close: %v", err)
	}
	if _, err := eng.DeleteRecordBatch(DefaultAgentID, []uint64{1}); common.CodeOf(err) != common.ErrClosed {
		t.Fatalf("DeleteRecordBatch after Close: %v", err)
	}
	if err := eng.Checkpoint(); common.CodeOf(err) != common.ErrClosed {
		t.Fatalf("Checkpoint after Close: %v", err)
	}
	if err := eng.Close(); common.CodeOf(err) != common.ErrClosed {
		t.Fatalf("second Close: %v", err)
	}
	for range eng.IndexByType(DefaultAgentID, RecL2Topic) {
		t.Fatal("a closed engine yields ids")
	}
	for range eng.IterAgents() {
		t.Fatal("a closed engine yields agents")
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
