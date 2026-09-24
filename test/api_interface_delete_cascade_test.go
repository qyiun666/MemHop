// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The two corrections a host makes through L2 — "this turn never happened", "this whole
// session is wrong" — promise more than dropping a row. `DeleteTopic` is documented as taking
// the turn's originals *and its plan tree* with it, and as taking a fused group's sunk
// children along; the internal packages assert both, while the host-facing half of the suite
// only checked that the listing shrinks. So this measures what a host can actually observe:
// the file stops carrying those records, and a group nobody summarises any more does not
// leave its members addressable.

package test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

func TestInterfaceDeleteTopicTakesThePlanTreeToo(t *testing.T) {
	llm := newMockLLM(t)
	m := openMockDB(t, filepath.Join(t.TempDir(), "cascade.meh"), llm.srv.URL)
	db := newTestDB(t, m)
	t.Cleanup(func() { _ = m.Close() })
	now := time.Now().UnixMilli()

	res, err := db.Search(memhop.SearchQuery{NewScene: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	turn := res.NewTopicID
	root, err := db.PlanNodeAdd(0, "定位回归")
	if err != nil {
		t.Fatalf("PlanNodeAdd root: %v", err)
	}
	leaf, err := db.PlanNodeAdd(root, "改 fallback")
	if err != nil {
		t.Fatalf("PlanNodeAdd leaf: %v", err)
	}
	for _, e := range []struct {
		node  uint32
		text  string
		stamp int64
	}{{root, "grep the caller", now + 1}, {leaf, "patch the retry", now + 2}} {
		if _, err := db.AppendArchive(memhop.ArchiveInput{
			Kind: memhop.KindEvent, EventType: "tool_call", NodeSeq: e.node,
			ContentType: memhop.ContentText, Content: e.text, CreatedAt: e.stamp,
		}); err != nil {
			t.Fatalf("AppendArchive at step %d: %v", e.node, err)
		}
	}
	if _, err := turnOf(db, "所有权规则是什么", "靠移动语义", now+3); err != nil {
		t.Fatalf("settle: %v", err)
	}

	// While the turn exists, both halves are addressable: its events through the step they
	// name, and its tree through the plan read.
	topic := turn
	if hits, err := db.SearchL4(memhop.L4Query{TopicID: &topic, NodeSeq: root}); err != nil || len(hits) != 2 {
		t.Fatalf("the turn's step-bound events read back %d (err %v), want 2 — the root's subtree includes its child's event",
			len(hits), err)
	}
	if tree, err := db.PlanState(); err != nil || tree.TotalCount != 2 {
		t.Fatalf("the turn's plan tree reads %+v (err %v), want two steps", tree, err)
	}

	_, recordsBefore := counts(t, m)
	if err := db.DeleteTopic(turn); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	_, recordsAfter := counts(t, m)

	// The turn owned four content records (two originals, two step-bound events) and two plan
	// nodes. A cascade that stopped at the topic record would leave the plan nodes counted
	// here, still swept by every later Dream pass as though the turn existed.
	if dropped := recordsBefore - recordsAfter; dropped < 6 {
		t.Fatalf("DeleteTopic reclaimed %d records, want the 4 content records plus the 2 plan nodes", dropped)
	}
	if hits, err := db.SearchL4(memhop.L4Query{TopicID: &topic}); err != nil || len(hits) != 0 {
		t.Fatalf("the deleted turn still answers by topic: %+v (err %v)", hits, err)
	}
}

// A fused group is deleted as one thing: the summary row and every turn it swallowed.
func TestInterfaceDeleteTopicTakesAFusedGroupsSubtree(t *testing.T) {
	llm := newMockLLM(t)
	m := openMockDB(t, filepath.Join(t.TempDir(), "group.meh"), llm.srv.URL, noAutoDreamButCompress)
	db := newTestDB(t, m)
	t.Cleanup(func() { _ = m.Close() })

	stamp := int64(1_700_000_000_000)
	for i := 0; i < 4; i++ {
		if _, err := db.Search(memhop.SearchQuery{NewScene: i == 0}); err != nil {
			t.Fatalf("Search %d: %v", i, err)
		}
		stamp += 1000
		if _, err := turnOf(db, "巩固前的提问", "巩固前的回答", stamp); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}
	if _, err := db.Dream(context.Background(), ""); err != nil {
		t.Fatalf("Dream: %v", err)
	}
	ctx, err := db.SceneContext("")
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}
	var group *memhop.SceneContextTopic
	sunk := 0
	for i := range ctx.Topics {
		if ctx.Topics[i].ChildCount > 0 {
			group = &ctx.Topics[i]
		}
		if ctx.Topics[i].Depth > 1 {
			sunk++
		}
	}
	if group == nil {
		t.Fatalf("consolidation produced no group in %+v", ctx.Topics)
	}
	if sunk == 0 {
		t.Fatalf("no member rows sank under the group: %+v", ctx.Topics)
	}

	_, before := counts(t, m)
	if err := db.DeleteTopic(group.TopicID); err != nil {
		t.Fatalf("DeleteTopic(group): %v", err)
	}
	afterCtx, err := db.SceneContext("")
	if err != nil {
		t.Fatalf("SceneContext after: %v", err)
	}
	for _, row := range afterCtx.Topics {
		if row.TopicID == group.TopicID {
			t.Fatalf("the group row survived its own deletion: %+v", row)
		}
		// Every row the group summarised went with it: a sunk member whose group is gone has
		// no path back to a listing a host reads.
		if row.Depth > 1 {
			t.Fatalf("a member of the deleted group is still listed: %+v", row)
		}
	}
	if _, after := counts(t, m); after >= before {
		t.Fatalf("deleting the group reclaimed nothing: %d -> %d", before, after)
	}
}

// turnOf closes the turn the domain holds open, on a stamp the caller controls.
func turnOf(db *testDB, user, agent string, stamp int64) (string, error) {
	topic, err := db.Update(memhop.TurnEnd{Input: user, Output: agent, CreatedAt: stamp})
	if err != nil {
		return "", err
	}
	return topic.ID, nil
}

func counts(tb testing.TB, m *memhop.DB) (int64, int64) {
	tb.Helper()
	st, err := m.Stats()
	if err != nil {
		tb.Fatalf("Stats: %v", err)
	}
	return st.FileBytes, st.RecordCount
}

// Consolidation gives a scene rows the scene listing no longer names directly: the fused
// parent, and under it the turns it swallowed. A scene delete is only complete if the whole
// tree goes — so the check runs on a **compacted copy**, where nothing but live records
// remains and a surviving row cannot hide behind a tombstone or a cache.
func TestInterfaceDeleteSceneTakesItsFusedGroupWithIt(t *testing.T) {
	llm := newMockLLM(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "group_delete.meh")
	m := openMockDB(t, path, llm.srv.URL, noAutoDreamButCompress)
	db := newTestDB(t, m)

	stamp := int64(1_700_000_000_000)
	var sceneID string
	for i := 0; i < 4; i++ {
		res, err := db.Search(memhop.SearchQuery{NewScene: i == 0})
		if err != nil {
			t.Fatalf("Search %d: %v", i, err)
		}
		sceneID = res.Scene.SceneID
		stamp += 1000
		if _, err := turnOf(db, "融合前的提问", "融合前的回答", stamp); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}
	if _, err := db.Dream(context.Background(), ""); err != nil {
		t.Fatalf("Dream: %v", err)
	}
	ctx, err := db.SceneContext(sceneID)
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}
	var sunk, parents int
	for _, row := range ctx.Topics {
		if row.Depth > 1 {
			sunk++
		} else if row.ChildCount > 0 {
			parents++
		}
	}
	if sunk == 0 || parents == 0 {
		t.Fatalf("the pass built no group to delete: %+v", ctx.Topics)
	}

	if err := db.DeleteScene(sceneID); err != nil {
		t.Fatalf("DeleteScene: %v", err)
	}
	copyPath := filepath.Join(dir, "group_delete_copy.meh")
	if err := m.CompactTo(copyPath); err != nil {
		t.Fatalf("CompactTo: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened := openMockDB(t, copyPath, llm.srv.URL)
	defer reopened.Close()
	primary, err := reopened.Primary()
	if err != nil {
		t.Fatalf("Primary on the copy: %v", err)
	}

	liveBytes, liveRecords := counts(t, reopened)
	if liveRecords != 1 {
		t.Fatalf("the compacted copy still holds %d records after its only scene was deleted (want only the domain's profile); bytes=%d",
			liveRecords, liveBytes)
	}
	if scenes, err := primary.ListScenes(""); err != nil || len(scenes) != 0 {
		t.Fatalf("the copy lists scenes after the delete: %+v err %v", scenes, err)
	}
	if nodes, err := primary.ListL1(); err != nil || len(nodes) != 0 {
		t.Fatalf("the copy keeps a scene node for a deleted scene: %+v err %v", nodes, err)
	}
	kind := memhop.KindUtterance
	for _, row := range ctx.Topics {
		rows, err := primary.SearchL4(memhop.L4Query{TopicID: &row.TopicID, Kind: &kind})
		if err != nil || len(rows) != 0 {
			t.Fatalf("a %s-level turn %s of the deleted scene still answers %d rows (err %v) — its originals were left behind",
				map[bool]string{true: "sunk", false: "top"}[row.Depth > 1], row.TopicID, len(rows), err)
		}
	}
}
