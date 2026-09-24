// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Dream takes a scene id, and a host in a tool loop reaches for exactly that: the model
// names a scene out of its context, and the id may be one the library has already lost.
// The scope also has a boundary worth stating out loud — consolidation honours it, the two
// retention prunes do not, because content and plan nodes age on their own clocks and a
// host that had to sweep every scene one by one would never finish the domain.

package test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
)

func TestInterfaceDreamRefusesASceneThatIsGone(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	settleTurn(t, db, sceneID, "用户要求重构代码", "好的,我来重构这段代码")

	if err := db.DeleteScene(sceneID); err != nil {
		t.Fatalf("DeleteScene: %v", err)
	}
	rep, err := db.Dream(context.Background(), sceneID)
	if memhop.CodeOf(err) != memhop.ErrNotFound {
		t.Fatalf("Dream over a deleted scene = %v (code %d, report %+v), want ErrNotFound",
			err, memhop.CodeOf(err), rep)
	}
	// The unscoped pass over the same domain still answers: the scene being gone is not
	// the domain being broken.
	if _, err := db.Dream(context.Background(), ""); err != nil {
		t.Fatalf("the domain's own Dream after the deleted scene: %v", err)
	}
}

func TestInterfaceDreamScopesConsolidationButNotRetention(t *testing.T) {
	llm := newMockLLM(t)
	m := openMockDB(t, filepath.Join(t.TempDir(), "scope.meh"), llm.srv.URL,
		func(d *internal.MemHopDefaults) {
			d.DreamCompressMinTopics = 2
			// 60s: settling the turns ahead of the pass costs LLM round trips, and a
			// 1s window let a slow runner's own turns cross it before the Dream ran.
			d.ContentRetentionMs = 60000
			d.SceneDreamTopicThreshold = -1
		})
	db := newTestDB(t, m)
	defer db.Close()

	scoped := openSession(t, db)
	other := openSession(t, db)
	for _, scene := range []string{scoped, other} {
		settleTurn(t, db, scene, "用户要求重构代码", "好的,我来重构这段代码")
		settleTurn(t, db, scene, "继续重构第二个模块", "第二个模块也补上测试")
	}

	// One aged record, written into the scene this pass does not name. A day-old
	// millisecond stamp is already past the window, so no sleep is needed.
	if _, err := db.Search(memhop.SearchQuery{SceneID: other}); err != nil {
		t.Fatalf("open a turn on the other scene: %v", err)
	}
	old := time.Now().Add(-24 * time.Hour).UnixMilli()
	stamped, err := db.AppendArchive(memhop.ArchiveInput{
		Kind: memhop.KindEvent, ContentType: memhop.ContentText, EventType: "tool_call",
		CreatedAt: old, Content: "a record past the window in the other scene",
	})
	if err != nil {
		t.Fatalf("AppendArchive: %v", err)
	}
	if _, err := db.Update(memhop.TurnEnd{Input: "顺便记一笔", Output: "记下了",
		Outcome: "done", CreatedAt: old}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	rep, err := db.Dream(context.Background(), scoped)
	if err != nil {
		t.Fatalf("scoped Dream: %v", err)
	}
	if rep.ConsolidatedScenes != 1 {
		t.Fatalf("a Dream naming one scene consolidated %d scenes, want 1", rep.ConsolidatedScenes)
	}

	// The scene it was told about fused; the other one still shows both turns.
	scopedSurface, err := db.SceneContext(scoped)
	if err != nil {
		t.Fatalf("SceneContext(scoped): %v", err)
	}
	otherSurface, err := db.SceneContext(other)
	if err != nil {
		t.Fatalf("SceneContext(other): %v", err)
	}
	// A fused group is the one surface row with children under it, so "did this pass fuse
	// anything" is read off ChildCount rather than off a row count — the scene read lists a
	// group and the turns it swallowed, so both scenes show three rows either way.
	fused := func(topics []memhop.SceneContextTopic) int {
		n := 0
		for _, row := range topics {
			if row.Depth == 1 && row.ChildCount > 0 {
				n++
			}
		}
		return n
	}
	if rep.L2TopicsCompressed != 2 || fused(scopedSurface.Topics) != 1 || fused(otherSurface.Topics) != 0 {
		t.Fatalf("a Dream scoped to one scene fused %d topics, %d group(s) on the named scene and %d on the "+
			"untouched one, want 2 topics and 1 group on the named scene only",
			rep.L2TopicsCompressed, fused(scopedSurface.Topics), fused(otherSurface.Topics))
	}

	// The retention sweep did not honour the scope: the aged record in the other scene is
	// gone, which is what the report's prune stages counted.
	left, err := db.SearchL4(memhop.L4Query{Keyword: "past the window"})
	if err != nil {
		t.Fatalf("SearchL4: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("the aged record in the untouched scene survived a Dream scoped elsewhere: %+v", left)
	}
	pruned := false
	for _, stage := range rep.Stages {
		if strings.HasPrefix(stage.Name, "l4_prune") && stage.Status == "ok" {
			pruned = true
		}
	}
	if !pruned {
		t.Fatalf("a Dream scoped to one scene reported no L4 prune stage: %+v", rep.Stages)
	}
	if stamped < 3 {
		t.Fatalf("the aged record took slot %d, want one above the two dialogue slots", stamped)
	}
}
