// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
)

// noAutoDreamButCompress keeps the background trigger off (a Dream scheduled mid-test would
// move the surface under the comparison) while letting the host-driven pass merge: the
// compress floor gates the model call, so leaving it at its default of 20 would answer "did
// nothing" for reasons unrelated to what is under test here.
func noAutoDreamButCompress(d *memhop.MemHopDefaults) {
	d.SceneDreamTopicThreshold = -1
	d.DreamCompressMinTopics = 2
}

// Consolidation is the one pass that rewrites what a scene looks like without deleting
// anything: the merged turns sink to depth 2 and a group row appears at depth 1, so the
// listing a host reads depends on a cache that Dream maintains incrementally. A cache that
// disagrees with the records shows up as a recall whose rows change when the process
// restarts - turns a host watched disappear, or a group loses the children it summarises.
// So the answer after a Dream has to be byte-identical to the answer after reopening the
// same file, where the caches are rebuilt from records.
func TestInterfaceConsolidationSurvivesReopen(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "consolidate.meh")
	m := openMockDB(t, path, llm.srv.URL, noAutoDreamButCompress)
	t.Cleanup(func() { _ = m.Close() })
	sess, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}

	var sceneID string
	stamp := int64(1_700_000_000_000)
	for i := 0; i < 8; i++ {
		res, err := sess.Search(memhop.SearchQuery{NewScene: i == 0})
		if err != nil {
			t.Fatalf("Search %d: %v", i, err)
		}
		sceneID = res.Scene.SceneID
		// Distinct stamps per round: the listing is ordered by them, and a tie would make
		// the order this test compares depend on nothing but luck.
		stamp += 1000
		if _, err := sess.Update(memhop.TurnEnd{Input: "巩固前的提问", Output: "巩固前的回答",
			Outcome: "answered", CreatedAt: stamp}); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}

	rep, err := sess.Dream(context.Background(), sceneID)
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if rep.L2TopicsCompressed == 0 {
		t.Fatalf("the pass compressed nothing, so the comparison below would be vacuous: %+v", rep)
	}

	dump := func(label string, s *memhop.Session) string {
		s2, err := s.SceneContext(sceneID)
		if err != nil {
			t.Fatalf("%s SceneContext: %v", label, err)
		}
		var groups, sunk int
		for _, row := range s2.Topics {
			if row.ChildCount > 0 {
				groups++
			}
			if row.Depth > 1 {
				sunk++
			}
			if len(row.Keywords) == 0 {
				t.Fatalf("%s: row %s has no keyword track", label, row.TopicID)
			}
		}
		if groups == 0 || sunk == 0 {
			t.Fatalf("%s: the consolidated state is missing from the listing (groups %d, sunk %d)", label, groups, sunk)
		}
		raw, err := json.Marshal(s2)
		if err != nil {
			t.Fatalf("%s marshal: %v", label, err)
		}
		return string(raw)
	}

	before := dump("before the reopen", sess)
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	m2 := openMockDB(t, path, llm.srv.URL, noAutoDreamButCompress)
	t.Cleanup(func() { _ = m2.Close() })
	sess2, err := m2.Primary()
	if err != nil {
		t.Fatalf("Primary after reopen: %v", err)
	}

	after := dump("after the reopen", sess2)
	if before != after {
		t.Fatalf("the scene read changed across a restart of the same file:\nbefore: %s\nafter:  %s", before, after)
	}
}
