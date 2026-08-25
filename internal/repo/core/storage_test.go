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
	"time"
)

// helper: temp path for a .meh file
func tempPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name+".meh")
}

func TestCreateWriteRead(t *testing.T) {
	p := tempPath(t, "cwr")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close(&IndexSnapshotData{}) })
	data := []byte("hello storage engine")
	offset, err := eng.WriteRecord(RecL0Profile, 12345, data)
	if err != nil {
		t.Fatal(err)
	}
	if offset != DataStart {
		t.Fatalf("expected offset %d, got %d", DataStart, offset)
	}
	rt, got, err := eng.ReadRecord(12345)
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
	_, _, err = eng.ReadRecord(99999)
	if err == nil {
		t.Fatal("expected error for missing record")
	}
}

func TestABHeaderSwitch(t *testing.T) {
	p := tempPath(t, "ab")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close(&IndexSnapshotData{}) })
	eng.WriteRecord(RecL1SceneNode, 1, []byte("a"))
	snap := &IndexSnapshotData{SparseData: []byte("s")}
	for range 4 {
		if err := eng.Checkpoint(snap); err != nil {
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
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(RecL2Topic, 100, []byte("checkpoint data"))
	snap := &IndexSnapshotData{
		SparseData:  []byte("sparse"),
		L3IndexData: []byte("l3"),
	}
	if err := eng.Checkpoint(snap); err != nil {
		t.Fatal(err)
	}
	eng.Close(snap)

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close(&IndexSnapshotData{})
	if eng2.RecordCount() != 1 {
		t.Fatalf("recordCount: want 1, got %d", eng2.RecordCount())
	}
	rt, data, err := eng2.ReadRecord(100)
	if err != nil {
		t.Fatal(err)
	}
	if rt != RecL2Topic {
		t.Fatalf("rt: want %d, got %d", RecL2Topic, rt)
	}
	if string(data) != "checkpoint data" {
		t.Fatalf("data: %q", data)
	}
	// Verify snapshot data survived.
	sd := eng2.SnapshotData()
	if sd == nil {
		t.Fatal("snapshot data is nil")
	}
	if string(sd.SparseData) != "sparse" {
		t.Fatalf("sparse: %q", sd.SparseData)
	}
}

func TestDeleteRecord(t *testing.T) {
	p := tempPath(t, "del")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close(&IndexSnapshotData{}) })
	eng.WriteRecord(RecL0Profile, 1, []byte("first"))
	eng.WriteRecord(RecL1SceneNode, 2, []byte("second"))
	eng.WriteRecord(RecL2Topic, 3, []byte("third"))

	ok, err := eng.DeleteRecord(2)
	if err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	_, _, err = eng.ReadRecord(2)
	if err == nil {
		t.Fatal("expected not found after delete")
	}
	if eng.RecordCount() != 2 {
		t.Fatalf("count: %d", eng.RecordCount())
	}
	// Others still readable.
	if _, _, err := eng.ReadRecord(1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := eng.ReadRecord(3); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteRecordBatch(t *testing.T) {
	p := tempPath(t, "del_batch")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close(&IndexSnapshotData{}) })
	eng.WriteRecord(RecL0Profile, 1, []byte("first"))
	eng.WriteRecord(RecL1SceneNode, 2, []byte("second"))
	eng.WriteRecord(RecL2Topic, 3, []byte("third"))
	eng.WriteRecord(RecL2Scene, 4, []byte("scene"))

	// Mixed existing/missing ids: missing ones are skipped without affecting the result
	n, err := eng.DeleteRecordBatch([]uint64{1, 2, 99})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 deleted, got %d", n)
	}
	if eng.RecordCount() != 2 {
		t.Fatalf("count: %d", eng.RecordCount())
	}
	// Deleted ones unreadable, remaining ones readable
	for _, id := range []uint64{1, 2} {
		if _, _, err := eng.ReadRecord(id); err == nil {
			t.Errorf("record %d should be deleted", id)
		}
	}
	if _, _, err := eng.ReadRecord(3); err != nil {
		t.Error("record 3 should survive")
	}
	if _, _, err := eng.ReadRecord(4); err != nil {
		t.Error("record 4 should survive")
	}
	// All missing returns 0
	n, err = eng.DeleteRecordBatch([]uint64{99})
	if err != nil || n != 0 {
		t.Fatalf("missing-only batch: n=%d err=%v", n, err)
	}
}

func TestCompact(t *testing.T) {
	p := tempPath(t, "compact_src")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close(&IndexSnapshotData{}) })
	eng.WriteRecord(RecL0Profile, 1, []byte("keep"))
	eng.WriteRecord(RecL1SceneNode, 2, []byte("delete me"))
	eng.WriteRecord(RecL2Topic, 3, []byte("also keep"))
	eng.WriteRecord(RecL3GraphNode, 4, []byte("keep too"))
	eng.WriteRecord(RecL4Archive, 5, []byte("remove"))
	eng.DeleteRecord(2)
	eng.DeleteRecord(5)
	// Checkpoint so original has a snapshot (fair comparison).
	eng.Checkpoint(&IndexSnapshotData{})

	compactPath := tempPath(t, "compact_dst")
	snap := &IndexSnapshotData{SparseData: []byte("sparse")}
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
	_, data, err := eng2.ReadRecord(1)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("data: %q", data)
	}
	_, _, err = eng2.ReadRecord(2)
	if err == nil {
		t.Fatal("expected not found in compacted file")
	}
	_, data3, err := eng2.ReadRecord(3)
	if err != nil || string(data3) != "also keep" {
		t.Fatal("record 3 missing or wrong")
	}
	// The caller-provided snapshot must be carried into the compacted file.
	sd := eng2.SnapshotData()
	if sd == nil || string(sd.SparseData) != "sparse" {
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

func TestConcurrentReadWrite(t *testing.T) {
	p := tempPath(t, "conc")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close(&IndexSnapshotData{}) })
	// Seed some records.
	for i := range uint64(100) {
		eng.WriteRecord(RecL0Profile, i, fmt.Appendf(nil, "seed-%d", i))
	}
	var wg sync.WaitGroup
	// Concurrent readers.
	for range 5 {
		wg.Go(func() {
			for i := range uint64(100) {
				eng.ReadRecord(i)
			}
		})
	}
	// Concurrent writers (base 1,2,3 to avoid overlapping with seed 0-99).
	for g := 1; g <= 3; g++ {
		wg.Add(1)
		go func(base uint64) {
			defer wg.Done()
			for i := range uint64(50) {
				eng.WriteRecord(RecL2Topic, base*1000+i, fmt.Appendf(nil, "w-%d", i))
			}
		}(uint64(g))
	}
	wg.Wait()
	// Verify all seed records intact.
	for i := range uint64(100) {
		if !eng.Contains(i) {
			t.Fatalf("seed record %d missing", i)
		}
	}
	if eng.RecordCount() != 100+3*50 {
		t.Fatalf("count: %d", eng.RecordCount())
	}
}

func TestWriteRecordBatch(t *testing.T) {
	p := tempPath(t, "batch")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close(&IndexSnapshotData{}) })
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
		rt, data, err := eng.ReadRecord(rec.IDHash)
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
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close(&IndexSnapshotData{}) })
	if eng.FileSize() != DataStart {
		t.Fatalf("initial size: %d", eng.FileSize())
	}
}

func TestContainsAndIndex(t *testing.T) {
	p := tempPath(t, "iter")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close(&IndexSnapshotData{}) })
	eng.WriteRecord(RecL0Profile, 42, []byte("x"))
	if !eng.Contains(42) {
		t.Fatal("should contain 42")
	}
	if eng.Contains(99) {
		t.Fatal("should not contain 99")
	}
	count := 0
	for range eng.Index() {
		count++
	}
	if count != 1 {
		t.Fatalf("iter count: %d", count)
	}
}

func TestOpenRecoversRecordsAfterSnapshot(t *testing.T) {
	p := tempPath(t, "crash")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close(&IndexSnapshotData{}) })
	// First batch, then checkpoint.
	eng.WriteRecord(RecL0Profile, 1, []byte("one"))
	eng.WriteRecord(RecL1SceneNode, 2, []byte("two"))
	if err := eng.Checkpoint(&IndexSnapshotData{}); err != nil {
		t.Fatal(err)
	}
	// Second batch appended after the checkpoint (includes an overwrite).
	eng.WriteRecord(RecL2Topic, 3, []byte("three"))
	eng.WriteRecord(RecL2Topic, 1, []byte("one-updated"))
	// Simulate a crash: close without checkpoint.
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	// Both batches must be visible after reopen.
	if _, data, err := eng2.ReadRecord(2); err != nil || string(data) != "two" {
		t.Fatalf("record 2: data=%q err=%v", data, err)
	}
	if _, data, err := eng2.ReadRecord(3); err != nil || string(data) != "three" {
		t.Fatalf("record 3: data=%q err=%v", data, err)
	}
	// Later write for the same idHash wins.
	if _, data, err := eng2.ReadRecord(1); err != nil || string(data) != "one-updated" {
		t.Fatalf("record 1: data=%q err=%v", data, err)
	}
	if eng2.RecordCount() != 3 {
		t.Fatalf("recordCount: want 3, got %d", eng2.RecordCount())
	}
	// nextOffset must be past the recovered tail: a new write must not
	// clobber recovered records.
	if _, err := eng2.WriteRecord(RecL4Archive, 4, []byte("four")); err != nil {
		t.Fatal(err)
	}
	if _, data, err := eng2.ReadRecord(3); err != nil || string(data) != "three" {
		t.Fatalf("record 3 after new write: data=%q err=%v", data, err)
	}
	if _, data, err := eng2.ReadRecord(4); err != nil || string(data) != "four" {
		t.Fatalf("record 4: data=%q err=%v", data, err)
	}
	eng2.Close(&IndexSnapshotData{})
}

func TestCloseNoCheckpointPreservesDiskState(t *testing.T) {
	p := tempPath(t, "nocp")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	eng.WriteRecord(RecL0Profile, 1, []byte("a"))
	snap := &IndexSnapshotData{SparseData: []byte("sparse")}
	if err := eng.Checkpoint(snap); err != nil {
		t.Fatal(err)
	}
	commitID := eng.activeHeaderRef().CommitID
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	eng2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close(&IndexSnapshotData{})
	// Header must not have flipped and the snapshot must be intact.
	if got := eng2.activeHeaderRef().CommitID; got != commitID {
		t.Fatalf("commitID: want %d, got %d", commitID, got)
	}
	sd := eng2.SnapshotData()
	if sd == nil || string(sd.SparseData) != "sparse" {
		t.Fatalf("snapshot lost: %+v", sd)
	}
	if _, data, err := eng2.ReadRecord(1); err != nil || string(data) != "a" {
		t.Fatalf("record 1: data=%q err=%v", data, err)
	}
}

func TestIndexCallbackMayReadRecord(t *testing.T) {
	p := tempPath(t, "iterlock")
	eng, err := Create(p, 768)
	if err != nil {
		t.Fatal(err)
	}
	for i := range uint64(10) {
		eng.WriteRecord(RecL0Profile, i, fmt.Appendf(nil, "v%d", i))
	}
	// Queue a writer during iteration. Under the old callback-based
	// implementation (fn invoked with RLock held) the waiting writer would
	// make fn's recursive RLock deadlock; the iterator copies the index
	// under RLock and yields lock-free, so engine methods stay callable.
	writerDone := make(chan struct{})
	first := true
	for idHash := range eng.Index() {
		if first {
			first = false
			go func() {
				defer close(writerDone)
				eng.WriteRecord(RecL0Profile, 999, []byte("queued"))
			}()
			time.Sleep(50 * time.Millisecond)
		}
		if _, _, err := eng.ReadRecord(idHash); err != nil {
			t.Errorf("ReadRecord(%d): %v", idHash, err)
		}
	}
	<-writerDone
	if !eng.Contains(999) {
		t.Fatal("queued writer record missing")
	}
	// yield returning false stops iteration.
	count := 0
	for range eng.Index() {
		count++
		if count >= 3 {
			break
		}
	}
	if count != 3 {
		t.Fatalf("early stop: want 3 yields, got %d", count)
	}
	eng.Close(&IndexSnapshotData{})
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
