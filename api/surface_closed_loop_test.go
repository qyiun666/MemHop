// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Closed-loop tests for the strict (no-fallback) write contracts: a call that
// returns an error must leave nothing behind, a call that succeeds must return
// what it stored, and every malformed request must surface as an error rather
// than as a silently degraded write. These run against the stub LLM only.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// openSurface opens a tenant session backed by the stub LLM.
func openSurface(t *testing.T) *Session {
	t.Helper()
	srv := stubLLM()
	t.Cleanup(srv.Close)
	_, sess := openMultiSession(t, surfaceConfig(t, srv.URL))
	return sess
}

// garbageLLM answers a well-formed chat completion whose content is prose,
// never JSON — the shape a model that cannot follow the output contract
// actually returns.
func garbageLLM(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-garbage", "object": "chat.completion", "created": 0, "model": "m",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "这是一段自然语言摘要，不是 JSON"},
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
}

// importGraph imports a small package-shaped knowledge batch and returns its
// graph id.
func importGraph(t *testing.T, sess *Session, mode L3ImportMode) (*L3ImportResult, string) {
	t.Helper()
	items := []L3ImportItem{
		{Title: "api", Domain: "proj/pkg", NodeType: "package", Content: "the facade",
			Keywords: []string{"facade"}, SourceRef: "api/",
			Related: []L3Relation{{Titles: []string{"internal"}, Kind: EdgeDependency}}},
		{Title: "internal", Domain: "proj/pkg", NodeType: "package", Content: "the composition root",
			Keywords: []string{"root"}, SourceRef: "internal/",
			Related: []L3Relation{{Titles: []string{"repo"}, Kind: EdgeDependency}}},
		{Title: "repo", Domain: "proj/pkg", NodeType: "package", Content: "the record layer",
			Keywords: []string{"repo"}, SourceRef: "internal/repo/"},
	}
	res, err := sess.ImportL3(items, mode)
	if err != nil {
		t.Fatalf("ImportL3: %v", err)
	}
	if len(res.GraphIDs) != 1 {
		t.Fatalf("want 1 graph, got %v", res.GraphIDs)
	}
	return res, res.GraphIDs[0]
}

func TestImportL3RejectsMalformedBatch(t *testing.T) {
	sess := openSurface(t)
	before, _ := sess.ListL3()

	bad := []struct {
		name string
		op   func() error
	}{
		{"empty batch", func() error { _, e := sess.ImportL3(nil, L3ImportOverwrite); return e }},
		{"empty mode", func() error {
			_, e := sess.ImportL3([]L3ImportItem{{Title: "a", Domain: "d"}}, L3ImportMode(""))
			return e
		}},
		{"unknown mode", func() error {
			_, e := sess.ImportL3([]L3ImportItem{{Title: "a", Domain: "d"}}, L3ImportMode("Append"))
			return e
		}},
		{"item without title", func() error {
			_, e := sess.ImportL3([]L3ImportItem{{Title: "ok", Domain: "d"}, {Domain: "d"}}, L3ImportOverwrite)
			return e
		}},
		{"item without domain", func() error {
			_, e := sess.ImportL3([]L3ImportItem{{Title: "ok", Domain: ""}}, L3ImportOverwrite)
			return e
		}},
	}
	for _, tc := range bad {
		if err := tc.op(); err == nil {
			t.Errorf("%s: want an error, got nil", tc.name)
		}
	}
	// none of them wrote anything
	after, _ := sess.ListL3()
	if len(after) != len(before) {
		t.Fatalf("a refused batch must create no graph: %d → %d graphs", len(before), len(after))
	}
	if sc := scenesOf(t, sess); sc != 0 {
		t.Fatalf("unexpected scenes: %d", sc)
	}
}

func TestImportL3ReadsBackEveryField(t *testing.T) {
	sess := openSurface(t)
	res, gid := importGraph(t, sess, L3ImportOverwrite)
	if len(res.CreatedIDs) != 3 || res.EdgesCreated != 2 {
		t.Fatalf("want 3 nodes / 2 edges, got %d / %d", len(res.CreatedIDs), res.EdgesCreated)
	}
	g, err := sess.GetL3(gid)
	if err != nil {
		t.Fatalf("GetL3: %v", err)
	}
	if g.Slot.Name != "proj/pkg" || len(g.Nodes) != 3 || len(g.Edges) != 2 {
		t.Fatalf("graph shape: %+v", g.Slot)
	}
	byTitle := map[string]HypergraphNode{}
	for _, n := range g.Nodes {
		byTitle[n.Title] = n
	}
	n := byTitle["api"]
	if n.NodeType != "package" || n.Content != "the facade" || n.SourceRef == nil || *n.SourceRef != "api/" {
		t.Fatalf("node fields did not round-trip: %+v", n)
	}
	if n.GraphID != gid || !strings.Contains(strings.Join(n.Keywords, ","), "facade") {
		t.Fatalf("node graph/keywords: %+v", n)
	}
	for _, e := range g.Edges {
		if len(e.NodeIDs) != 2 || e.Kind != EdgeDependency || e.GraphID != gid {
			t.Fatalf("unexpected edge: %+v", e)
		}
	}
	// the same batch re-imported is idempotent in every mode
	for _, mode := range []L3ImportMode{L3ImportSkip, L3ImportMerge, L3ImportOverwrite} {
		r, err := sess.ImportL3([]L3ImportItem{
			{Title: "api", Domain: "proj/pkg", NodeType: "package", Content: "the facade",
				Keywords: []string{"facade"}, SourceRef: "api/",
				Related: []L3Relation{{Titles: []string{"internal"}, Kind: EdgeDependency}}},
		}, mode)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if r.EdgesCreated != 0 {
			t.Fatalf("%s: re-declaring an existing edge created %d", mode, r.EdgesCreated)
		}
	}
	after, _ := sess.GetL3(gid)
	if len(after.Edges) != 2 {
		t.Fatalf("idempotent re-import changed the edge set: %d", len(after.Edges))
	}
}

func TestImportL3SkipRestoresEdgesOfADeletedNode(t *testing.T) {
	sess := openSurface(t)
	_, gid := importGraph(t, sess, L3ImportOverwrite)

	full, _ := sess.GetL3(gid)
	repoID := idOfTitle(full, "repo")
	if err := sess.DeleteL3Nodes(gid, []string{repoID}); err != nil {
		t.Fatalf("DeleteL3Nodes: %v", err)
	}
	deleted, _ := sess.GetL3(gid)
	if len(deleted.Nodes) != 2 || len(deleted.Edges) != 1 {
		t.Fatalf("cascade left %d nodes / %d edges, want 2 / 1", len(deleted.Nodes), len(deleted.Edges))
	}
	// Skip-mode re-import of the whole batch: the node returns, and so do the
	// edges other items declared onto it.
	importGraph(t, sess, L3ImportSkip)
	restored, _ := sess.GetL3(gid)
	if len(restored.Nodes) != 3 {
		t.Fatalf("want 3 nodes back, got %d", len(restored.Nodes))
	}
	if len(restored.Edges) != 2 {
		t.Fatalf("Skip re-import must restore both edges, got %d", len(restored.Edges))
	}
	for _, e := range restored.Edges {
		for _, id := range e.NodeIDs {
			if !hasNode(restored, id) {
				t.Fatalf("edge %s references a node that is not in the graph", e.IDHash)
			}
		}
	}
}

func TestUpdateL3RenameSurvivesReimport(t *testing.T) {
	sess := openSurface(t)
	_, gid := importGraph(t, sess, L3ImportOverwrite)
	renamed, err := sess.UpdateL3(gid, ptr("proj/renamed"))
	if err != nil || renamed.Slot.Name != "proj/renamed" || renamed.Slot.IDHash != gid {
		t.Fatalf("UpdateL3: %+v err=%v", renamed.Slot, err)
	}
	// Importing under the ORIGINAL domain name resolves to the same graph and
	// must not overwrite the host's label.
	importGraph(t, sess, L3ImportOverwrite)
	after, err := sess.GetL3(gid)
	if err != nil {
		t.Fatalf("GetL3: %v", err)
	}
	if after.Slot.Name != "proj/renamed" {
		t.Fatalf("a re-import undid the rename: %q", after.Slot.Name)
	}
	if len(after.Nodes) != 3 {
		t.Fatalf("the re-import should extend the same graph, nodes=%d", len(after.Nodes))
	}
	// importing under the NEW name extends the same graph too
	if _, err := sess.ImportL3([]L3ImportItem{{Title: "extra", Domain: "proj/renamed", NodeType: "package"}},
		L3ImportOverwrite); err != nil {
		t.Fatalf("import under the renamed domain: %v", err)
	}
	l3, _ := sess.ListL3()
	if len(l3) != 1 {
		t.Fatalf("renamed domain must not start a second graph: %d graphs", len(l3))
	}
}

func TestQueryL3NodesRefusesUnknownGraphAndBadIds(t *testing.T) {
	sess := openSurface(t)
	_, gid := importGraph(t, sess, L3ImportOverwrite)
	if _, err := sess.QueryL3Nodes(L3NodeQuery{GraphID: gid, IDs: []string{"not-hex"}}); err == nil {
		t.Fatal("an unparsable node id must be an error, not an empty result")
	}
	if out, err := sess.QueryL3Nodes(L3NodeQuery{GraphID: gid, IDs: []string{"0000000000000001"}}); err != nil || len(out) != 0 {
		t.Fatalf("a well-formed but unknown id should match nothing without erroring: %d %v", len(out), err)
	}
	if err := sess.DeleteL3(gid); err != nil {
		t.Fatalf("DeleteL3: %v", err)
	}
	// every L3 read now agrees that the graph is gone
	if _, err := sess.GetL3(gid); err == nil {
		t.Fatal("GetL3 on a deleted graph must error")
	}
	if _, err := sess.QueryL3Nodes(L3NodeQuery{GraphID: gid}); err == nil {
		t.Fatal("QueryL3Nodes on a deleted graph must error, not return empty")
	}
	if err := sess.DeleteL3Nodes(gid, []string{"0000000000000001"}); err == nil {
		t.Fatal("DeleteL3Nodes on a deleted graph must error")
	}
}

func TestSceneAnchorAgreesWithTheGraphSurface(t *testing.T) {
	sess := openSurface(t)
	_, gid := importGraph(t, sess, L3ImportOverwrite)

	sr, err := sess.Search(SearchQuery{L3ID: gid})
	if err != nil {
		t.Fatalf("Search anchoring a new scene: %v", err)
	}
	if sr.Scene.L3ID != gid {
		t.Fatalf("new scene should carry the anchor, got %q", sr.Scene.L3ID)
	}
	// the same anchor on an existing scene is a request conflict, not a no-op
	if _, err := sess.Search(SearchQuery{SceneID: sr.Scene.SceneID, L3ID: gid}); err == nil {
		t.Fatal("Search must refuse an L3ID for an existing scene instead of ignoring it")
	}
	// an anchor naming a graph that does not exist is refused on creation too
	if _, err := sess.Search(SearchQuery{L3ID: "ffffffffffffffff"}); err == nil {
		t.Fatal("Search must refuse an unknown anchor graph")
	}
	if got, err := sess.ListScenes(gid); err != nil || len(got) != 1 {
		t.Fatalf("ListScenes(gid): %d scenes err=%v", len(got), err)
	}
	cleared, err := sess.UpdateScene(sr.Scene.SceneID, ScenePatch{L3ID: ptr("")})
	if err != nil || cleared.L3ID != "" {
		t.Fatalf("clearing the anchor: %+v err=%v", cleared, err)
	}
	if got, _ := sess.ListScenes(gid); len(got) != 0 {
		t.Fatalf("scene kept its anchor in the l3 listing: %d", len(got))
	}
	// the whole graph going away leaves no dangling anchor behind
	if err := sess.DeleteL3(gid); err != nil {
		t.Fatalf("DeleteL3: %v", err)
	}
	rest, err := sess.ListScenes(gid)
	if err != nil {
		t.Fatalf("ListScenes on a deleted graph: %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("a deleted graph still lists %d scenes", len(rest))
	}
}

func TestPlanCommitRejectedLeavesTreeUntouched(t *testing.T) {
	sess := openSurface(t)
	pid := NewPlanID("atomic-audit")
	root := &PlanNode{NodePath: "1", Title: "root", Type: "task", Status: "in_progress",
		Children: []PlanNode{{NodePath: "1.1", Title: "leaf", Type: "task", Status: "pending"}}}
	if err := sess.SyncPlanTree(pid, root); err != nil {
		t.Fatalf("SyncPlanTree: %v", err)
	}
	before, err := sess.PlanState(pid)
	if err != nil {
		t.Fatal(err)
	}

	rejected := []struct {
		name string
		ev   TrajectorySlot
	}{
		{"no timestamp", TrajectorySlot{EventType: "plan_step"}},
		{"no event type", TrajectorySlot{Timestamp: 7}},
		{"event type outside the plan vocabulary", TrajectorySlot{EventType: "whatever", Timestamp: 7}},
		{"payload over budget", TrajectorySlot{EventType: "plan_step", Timestamp: 7,
			Payload: strings.Repeat("x", 5*1024)}},
	}
	for _, tc := range rejected {
		if err := sess.PlanCommit(pid, "1.1", tc.ev, "done", "should-not-stick"); err == nil {
			t.Fatalf("%s: want an error", tc.name)
		}
		after, err := sess.PlanState(pid)
		if err != nil {
			t.Fatalf("%s: PlanState: %v", tc.name, err)
		}
		if render(after.Roots) != render(before.Roots) {
			t.Fatalf("%s: a refused commit moved the tree\n before %s\n after  %s",
				tc.name, render(before.Roots), render(after.Roots))
		}
		if evs, err := sess.ReadTrajectory(pid); err != nil || len(evs) != 0 {
			t.Fatalf("%s: a refused commit stored %d events (err=%v)", tc.name, len(evs), err)
		}
	}

	if err := sess.PlanCommit(pid, "1.1", TrajectorySlot{EventType: "plan_step", Timestamp: 7}, "done", "leaf done"); err != nil {
		t.Fatalf("valid commit: %v", err)
	}
	after, _ := sess.PlanState(pid)
	if after.DoneCount != before.DoneCount+1 {
		t.Fatalf("valid commit must advance the tree: %d → %d", before.DoneCount, after.DoneCount)
	}
	evs, err := sess.ReadTrajectory(pid)
	if err != nil || len(evs) != 1 {
		t.Fatalf("want 1 event, got %d err=%v", len(evs), err)
	}
	// an event bound to a step reads back attributed to that step
	if evs[0].NodePath != "1.1" || evs[0].PlanID != pid || evs[0].SessionID != pid {
		t.Fatalf("event not attributed to its step: %+v", evs[0])
	}
}

func TestAppendTrajectoryRefusesAndStoresNothing(t *testing.T) {
	sess := openSurface(t)
	key := NewPlanID("unused") + "" // any 16-hex key works for a bare turn event
	turn := mustTurnKey(t, sess)
	over := strings.Repeat("字", 3000)
	if err := sess.AppendTrajectory(turn, "", TrajectorySlot{EventType: "x", Timestamp: 1, Payload: over}); err == nil {
		t.Fatal("an over-budget payload must be refused")
	}
	if evs, err := sess.ReadTrajectory(turn); err != nil || len(evs) != 0 {
		t.Fatalf("a refused append stored %d events (err=%v)", len(evs), err)
	}
	// exactly at the budget is accepted
	if err := sess.AppendTrajectory(turn, "", TrajectorySlot{
		EventType: "x", Timestamp: 1,
		Payload: strings.Repeat("a", 4*1024)}); err != nil {
		t.Fatalf("payload at the budget limit: %v", err)
	}
	if err := sess.AppendTrajectory(key, "1", TrajectorySlot{EventType: "nope", Timestamp: 1}); err == nil {
		t.Fatal("a plan-bound event must be validated against the plan vocabulary")
	}
}

func TestUpdateFailsLoudlyWhenTheLLMCannotExtract(t *testing.T) {
	srv := garbageLLM(t)
	t.Cleanup(srv.Close)
	_, sess := openMultiSession(t, surfaceConfig(t, srv.URL))
	sr, err := sess.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	_, err = sess.Update(TurnUpdate{
		SceneID: sr.Scene.SceneID, TopicID: sr.NewTopicID,
		UserText: "我们聊聊 Rust 的所有权", UserTS: 1,
		AgentText: "所有权规则保证了内存安全", AgentTS: 2,
	})
	if err == nil {
		t.Fatal("Update must fail when keyword extraction degrades, not settle a turn with fake keywords")
	}
	// nothing settled: the scene still has no topics, and no archive exists
	again, err := sess.Search(SearchQuery{SceneID: sr.Scene.SceneID})
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if len(again.Topics) != 0 {
		t.Fatalf("a failed Update settled %d topics", len(again.Topics))
	}
	if arcs, err := sess.SearchL4(L4Query{}); err != nil || len(arcs) != 0 {
		t.Fatalf("a failed Update archived %d originals (err=%v)", len(arcs), err)
	}
}

// ---- small helpers ----

func ptr[T any](v T) *T { return &v }

func idOfTitle(g *L3Graph, title string) string {
	for _, n := range g.Nodes {
		if n.Title == title {
			return n.IDHash
		}
	}
	return ""
}

func hasNode(g *L3Graph, id string) bool {
	for _, n := range g.Nodes {
		if n.IDHash == id {
			return true
		}
	}
	return false
}

func scenesOf(t *testing.T, sess *Session) int {
	t.Helper()
	scenes, err := sess.ListScenes("")
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	return len(scenes)
}

func mustTurnKey(t *testing.T, sess *Session) string {
	t.Helper()
	sr, err := sess.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	return sr.NewTopicID
}

func render(ns []PlanNodeView) string {
	var b strings.Builder
	for _, n := range ns {
		b.WriteString(n.NodePath + "=" + n.Status + "/" + n.Summary + " ")
		b.WriteString(render(n.Children))
	}
	return b.String()
}

func TestImportL3HyperedgeStaysOneEdge(t *testing.T) {
	sess := openSurface(t)
	res, err := sess.ImportL3([]L3ImportItem{
		{Title: "auth-module", Domain: "proj/arch", NodeType: "module", Content: "the whole",
			Related: []L3Relation{{Titles: []string{"login", "token", "session"}, Kind: EdgePartOf}}},
		{Title: "login", Domain: "proj/arch", NodeType: "file", Content: "l"},
		{Title: "token", Domain: "proj/arch", NodeType: "file", Content: "t"},
		{Title: "session", Domain: "proj/arch", NodeType: "file", Content: "s"},
	}, L3ImportOverwrite)
	if err != nil {
		t.Fatalf("ImportL3: %v", err)
	}
	if res.EdgesCreated != 1 || len(res.Errors) != 0 {
		t.Fatalf("want 1 edge / no errors, got %d %+v", res.EdgesCreated, res.Errors)
	}
	g, err := sess.GetL3(res.GraphIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Edges) != 1 || len(g.Edges[0].NodeIDs) != 4 {
		t.Fatalf("the n-node fact must survive as ONE edge over 4 members: %+v", g.Edges)
	}
	for _, id := range g.Edges[0].NodeIDs {
		if !isHexID(id) {
			t.Fatalf("edge member id not hex: %q", id)
		}
	}

	// a relation naming nothing, or a member twice, is refused per relation
	bad, err := sess.ImportL3([]L3ImportItem{
		{Title: "auth-module", Domain: "proj/arch", Content: "the whole", Related: []L3Relation{
			{Titles: nil}, {Titles: []string{"login", "login"}}, {Titles: []string{"ghost"}}}},
	}, L3ImportOverwrite)
	if err != nil {
		t.Fatalf("per-item relation errors must not fail the call: %v", err)
	}
	if len(bad.Errors) != 3 || bad.EdgesCreated != 0 {
		t.Fatalf("want 3 relation errors and no edge, got %+v / %d", bad.Errors, bad.EdgesCreated)
	}
}
