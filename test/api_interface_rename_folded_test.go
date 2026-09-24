// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"context"
	"path/filepath"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
)

// Renaming is a whole-record rewrite, and the read shape reports no parent id — a fused parent
// is recognised only by `ChildCount`, the number of rows naming it. That makes the failure mode
// of a careless rename visible in exactly two ways, and both are checked here: the renamed turn
// surfacing again at depth 1 (so the group's summary and its original sit side by side), and the
// parent's child count dropping (so the host is told one fewer turn belongs to that group than
// the consolidation actually folded).
//
// The name must also survive a reopen: a rename that only reached the cache would look right
// until the process restarts.
func TestInterfaceRenamingAFoldedTurnKeepsItFolded(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "rename_folded.meh")
	m := openMockDB(t, path, llm.srv.URL, noAutoDreamButCompress)
	sess, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	first, err := sess.Search(memhop.SearchQuery{NewScene: true})
	if err != nil {
		t.Fatalf("open a scene: %v", err)
	}
	scene := first.Scene.SceneID
	var turns []string
	for i := 0; i < 2; i++ {
		if _, err := sess.Search(memhop.SearchQuery{}); err != nil {
			t.Fatalf("open turn %d: %v", i, err)
		}
		id, err := turn(sess, "同一件事的前半", "同一件事的后半")
		if err != nil {
			t.Fatalf("close turn %d: %v", i, err)
		}
		turns = append(turns, id)
	}
	if _, err := sess.Dream(context.Background(), ""); err != nil {
		t.Fatalf("Dream: %v", err)
	}

	before, err := sess.SceneContext(scene)
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}
	var parent string
	sunk := -1
	children := 0
	for i, row := range before.Topics {
		if row.Depth == 1 && row.ChildCount > 0 {
			parent, children = row.TopicID, row.ChildCount
		}
		if row.Depth > 1 {
			sunk = i
		}
	}
	if parent == "" || children != 2 || sunk < 0 {
		t.Fatalf("the fixture did not fold two turns under one group (parent=%q children=%d sunk=%d): %+v",
			parent, children, sunk, before.Topics)
	}
	victim := before.Topics[sunk].TopicID
	keywords := len(before.Topics[sunk].Keywords)

	if _, err := sess.RenameTopic(victim, "被改名的那一轮"); err != nil {
		t.Fatalf("RenameTopic: %v", err)
	}
	after, err := sess.SceneContext(scene)
	if err != nil {
		t.Fatalf("SceneContext after the rename: %v", err)
	}
	for i := range after.Topics {
		row := after.Topics[i]
		switch {
		case row.TopicID == victim:
			if row.Depth != before.Topics[sunk].Depth {
				t.Fatalf("the rename moved a folded turn from depth %d to %d — its own originals now sit beside "+
					"the group that replaced them: %+v", before.Topics[sunk].Depth, row.Depth, row)
			}
			if row.Name != "被改名的那一轮" {
				t.Fatalf("the rename did not name the row it was asked to: %+v", row)
			}
			if len(row.Keywords) != keywords {
				t.Fatalf("the rename disturbed the keyword track: %d before, %d after (%+v)",
					keywords, len(row.Keywords), row)
			}
		case row.TopicID == parent:
			if row.ChildCount != children {
				t.Fatalf("the group now claims %d children, was %d — the rename dropped the parent link it "+
					"was rewritten from: %+v", row.ChildCount, children, row)
			}
		}
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened := openMockDB(t, path, llm.srv.URL, noAutoDreamButCompress)
	t.Cleanup(func() { _ = reopened.Close() })
	sess2, err := reopened.Primary()
	if err != nil {
		t.Fatalf("Primary after reopen: %v", err)
	}
	back, err := sess2.SceneContext(scene)
	if err != nil {
		t.Fatalf("SceneContext after reopen: %v", err)
	}
	for _, row := range back.Topics {
		if row.TopicID == victim && row.Name == "" {
			t.Fatal("the rename was not in the file: the name is back to unnamed after a reopen — only a cache held it")
		}
	}
}
