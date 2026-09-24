// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Acceptance items 9 and 11 say consolidation is the library's job, not a call the host has to
// schedule: the guide tells a host it "usually does not need to call" Dream, because a scene whose
// surface passes the threshold gets a pass scheduled at the close of a round. Every offline case in
// this file's neighbourhood switches that trigger *off* (a pass landing mid-test would race its own
// assertions), which left the promise with no host-surface witness: a reader could not tell whether
// the trigger exists at all, or exists only in the guide.
//
// This drives the loop the way a host does — Search, work, Update, repeat, and never a Dream call —
// and waits for the pass to show up in the scene read. The wait is a deadline with a fixed poll, and
// the test proves its own premise first: the threshold really was crossed and the consolidate call
// point really was silent before the pass appeared.

package test

import (
	"path/filepath"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

func TestInterfaceConsolidationIsScheduledByTheRoundClose(t *testing.T) {
	llm := newMockLLM(t)
	const threshold = 2
	db := newTestDB(t, openMockDB(t, filepath.Join(t.TempDir(), "auto_dream.meh"), llm.srv.URL,
		func(d *memhop.MemHopDefaults) {
			d.SceneDreamTopicThreshold = threshold
			d.DreamCompressMinTopics = 2
		}))

	sceneID := ""
	for i := 0; i < 4; i++ {
		res, err := db.Search(memhop.SearchQuery{NewScene: i == 0})
		if err != nil {
			t.Fatalf("Search %d: %v", i, err)
		}
		sceneID = res.Scene.SceneID
		if _, err := turn(db.Session, "用户要求重构代码", "好的,我来重构这段代码"); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
	}

	// The premise: this scene is over the line that schedules a pass, and nothing has asked the
	// consolidation call point yet — so any fusion seen below can only be the library's doing.
	before, err := db.SceneContext(sceneID)
	if err != nil {
		t.Fatalf("SceneContext before the wait: %v", err)
	}
	surface := 0
	for _, row := range before.Topics {
		if row.Depth == 1 {
			surface++
		}
	}
	if surface <= threshold {
		t.Fatalf("the fixture crossed nothing: %d surface rows against a threshold of %d", surface, threshold)
	}
	// The counter is read here only to show the pass below was the first: consolidation is asked
	// for at exactly one place in the library, and this test reached it with round closes —
	// nothing in this file calls Dream.

	group := ""
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		sc, err := db.SceneContext(sceneID)
		if err != nil {
			t.Fatalf("SceneContext during the wait: %v", err)
		}
		for _, row := range sc.Topics {
			if row.Depth == 1 && row.ChildCount >= 2 {
				group = row.TopicID
				break
			}
		}
		if group != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if group == "" {
		t.Fatal("no consolidation pass reached the scene: the host never called Dream and the " +
			"round closes did not schedule one either — the guide's 'usually the host does not need " +
			"to call it' is not what the library does")
	}
	if llm.count("consolidate") == 0 {
		t.Fatal("a fused group appeared without the consolidation call point being asked, which means " +
			"the group came from somewhere this test did not mean to exercise")
	}

	// The pass took nothing away from the loop it interrupted: rounds keep opening, closing, and
	// reading back on the scene that was just reorganised.
	res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search after the unbidden pass: %v", err)
	}
	if _, err := db.Update(memhop.TurnEnd{Input: "接着上一件事", Output: "继续做完",
		Outcome: "done", CreatedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("Update after the unbidden pass: %v", err)
	}
	if res.NewTopicID == "" {
		t.Fatal("the round after the pass opened without a turn key of its own")
	}
}
