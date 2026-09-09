// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestArchiveIndexAppendAndHashes(t *testing.T) {
	idx := NewArchiveIndex()
	idx.Append(7, 100, 2000)
	idx.Append(7, 101, 1000)
	idx.Append(8, 200, 1500)

	if got := idx.Hashes(7); !slices.Equal(got, []uint64{101, 100}) {
		t.Fatalf("want CreatedAt ascending, got %v", got)
	}
	if got := idx.Hashes(8); !slices.Equal(got, []uint64{200}) {
		t.Fatalf("other topic must not bleed in: %v", got)
	}
	if idx.Hashes(9) != nil {
		t.Fatal("an unknown topic reads as empty, not as an error")
	}
}

// An id hashes its content, so settling the same turn twice with the same texts
// resolves to the same id — and a doubled entry would render that utterance
// twice.
func TestArchiveIndexAppendIsIdempotent(t *testing.T) {
	idx := NewArchiveIndex()
	id := common.HashID("dup")
	idx.Append(7, id, 1000)
	idx.Append(7, id, 1000)
	if got := idx.Hashes(7); len(got) != 1 {
		t.Fatalf("re-appending an id must not list it twice: %v", got)
	}
}

func TestArchiveIndexRemove(t *testing.T) {
	idx := NewArchiveIndex()
	idx.Append(7, 100, 1000)
	idx.Append(7, 101, 2000)

	if n := idx.RemoveIDs(7, []uint64{100}); n != 1 {
		t.Fatalf("RemoveIDs = %d, want 1", n)
	}
	if got := idx.Hashes(7); !slices.Equal(got, []uint64{101}) {
		t.Fatalf("after RemoveIDs: %v", got)
	}
	if n := idx.RemoveIDs(7, []uint64{999}); n != 0 {
		t.Fatalf("removing an unlisted id = %d, want 0", n)
	}
	if n := idx.RemoveIDs(7, []uint64{101}); n != 1 {
		t.Fatalf("RemoveIDs last = %d, want 1", n)
	}
	if got := idx.Hashes(7); got != nil {
		t.Fatalf("an emptied topic must drop out, got %v", got)
	}

	idx.Append(7, 110, 1000)
	idx.Append(8, 120, 1000)
	idx.RemoveTopic(7)
	if idx.Hashes(7) != nil {
		t.Fatal("RemoveTopic must drop the whole turn")
	}
	if idx.Hashes(8) == nil {
		t.Fatal("RemoveTopic must not touch another turn")
	}
}

// A domain reopened from disk must see every archive of every topic, or a
// settled turn reads back with nothing said in it.
func TestBuildArchiveFromEngineRestoresTopics(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "arch.meh"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := engine.Close(nil); err != nil {
			t.Errorf("close: %v", err)
		}
	}()

	write := func(topicID uint64, content string, at int64) {
		t.Helper()
		id := common.HashID(content)
		slot := &core.ArchiveSlot{IDHash: id, ContextID: topicID, Content: content, CreatedAt: at}
		if err := core.WriteArchiveSlot(engine, core.DefaultAgentID, id, slot); err != nil {
			t.Fatalf("write archive: %v", err)
		}
	}
	write(7, "a", 2000)
	write(7, "b", 1000)
	write(8, "c", 3000)

	idx := BuildArchiveFromEngine(engine, core.DefaultAgentID)
	if got := idx.Hashes(7); !slices.Equal(got, []uint64{common.HashID("b"), common.HashID("a")}) {
		t.Fatalf("rebuilt order: %v", got)
	}
	if got := idx.Hashes(8); !slices.Equal(got, []uint64{common.HashID("c")}) {
		t.Fatalf("rebuilt second topic: %v", got)
	}
}
