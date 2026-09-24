// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Acceptance item 2: the host may read L1, and Dream is the one that writes it. The layer
// had mechanics tests deep inside the packages but no case on the host's own surface, so
// nothing said out loud what a host actually sees: an empty listing before the first
// consolidation, one node per conversation after it, an edge between two conversations that
// talk about the same thing, and what the node's `topic_ids` promise — a snapshot of the
// last sync, not a live listing.

package test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
)

func TestInterfaceDreamBuildsTheAssociationLayer(t *testing.T) {
	llm := newMockLLM(t)
	db := newTestDB(t, openMockDB(t, filepath.Join(t.TempDir(), "l1.meh"), llm.srv.URL))

	if nodes, err := db.ListL1(); err != nil || len(nodes) != 0 {
		t.Fatalf("a domain that never consolidated answered %+v (err %v), want an empty list", nodes, err)
	}

	settle := func(scene bool, turns int) string {
		var sceneID string
		for i := 0; i < turns; i++ {
			res, err := db.Search(memhop.SearchQuery{NewScene: scene && i == 0})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			sceneID = res.Scene.SceneID
			if _, err := turn(db.Session, "rust 的所有权规则是什么", "靠移动语义保证内存安全"); err != nil {
				t.Fatalf("turn: %v", err)
			}
		}
		return sceneID
	}
	first := settle(true, 2)
	settle(true, 2)

	if _, err := db.Dream(context.Background(), ""); err != nil {
		t.Fatalf("Dream: %v", err)
	}
	nodes, err := db.ListL1()
	if err != nil {
		t.Fatalf("ListL1 after Dream: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("Dream built %+v, want one node per settled conversation", nodes)
	}
	byScene := map[string]memhop.SceneNodeView{}
	for _, n := range nodes {
		byScene[n.SceneID] = n
	}
	firstNode, ok := byScene[first]
	if !ok {
		t.Fatalf("no node names the first conversation, got %+v", nodes)
	}
	if len(firstNode.TopicIDs) != 2 {
		t.Fatalf("the node lists %+v, want the two turns its scene settled", firstNode.TopicIDs)
	}
	// A node synced minutes ago is at (near) full strength: the counter starts at 1 and the
	// only thing that moves it is decay, which the same pass already ran once. So the value
	// is just under 1, and nothing about it says "this trace is fading".
	if firstNode.Importance <= 0.99 || firstNode.Importance > 1.0 {
		t.Fatalf("a just-synced node reports importance %v, want 1 minus one small decay", firstNode.Importance)
	}
	// Both conversations settled the same keywords, so Dream should judge them related:
	// the edge is the whole reason the layer exists, and an id is readable only as "these
	// two nodes share it".
	if len(firstNode.EdgeIDs) == 0 {
		t.Fatalf("the first node names no edge: %+v", firstNode)
	}
	other := byScene[nodes[1].SceneID]
	if len(other.EdgeIDs) == 0 || other.EdgeIDs[0] != firstNode.EdgeIDs[0] {
		t.Fatalf("the two nodes do not share an edge: %+v vs %+v", firstNode, other)
	}

	// The snapshot claim: deleting a topic leaves the node's list as the last sync wrote
	// it, and the next pass rebuilds it.
	if err := db.DeleteTopic(firstNode.TopicIDs[0]); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	afterDelete, err := db.ListL1()
	if err != nil {
		t.Fatalf("ListL1 after delete: %v", err)
	}
	if got := nodeFor(t, afterDelete, first).TopicIDs; len(got) != 2 || got[0] != firstNode.TopicIDs[0] {
		t.Fatalf("the listing changed under the host's feet: %+v, want the last sync's snapshot", got)
	}
	if _, err := db.Dream(context.Background(), ""); err != nil {
		t.Fatalf("second Dream: %v", err)
	}
	third, err := db.ListL1()
	if err != nil {
		t.Fatalf("ListL1 after the second Dream: %v", err)
	}
	if got := nodeFor(t, third, first).TopicIDs; len(got) != 1 {
		t.Fatalf("the next pass did not rebuild the snapshot: %+v", got)
	}
	if len(third) != 2 {
		t.Fatalf("a second Dream minted extra nodes: %+v", third)
	}
}

func nodeFor(t *testing.T, nodes []memhop.SceneNodeView, sceneID string) memhop.SceneNodeView {
	t.Helper()
	for _, n := range nodes {
		if n.SceneID == sceneID {
			return n
		}
	}
	raw, _ := json.Marshal(nodes)
	t.Fatalf("no node for scene %s in %s", sceneID, raw)
	return memhop.SceneNodeView{}
}

// The same layer has to disappear with the memory it was drawn from, and the host only ever
// sees that through ListL1: DeleteScene drops the scene's node right away, and MergeScenes
// drops the swallowed scene's node while the surviving scene keeps its own name, its anchor
// and the turns it gained. What a merge or a delete does NOT do is lose a label the host
// wrote: a named turn comes back named through a consolidation pass, because those rewrites
// move the tree around the name rather than over it.
func TestInterfaceMemoryDeletionTakesItsNodeWithIt(t *testing.T) {
	llm := newMockLLM(t)
	db := newTestDB(t, openMockDB(t, filepath.Join(t.TempDir(), "l1_delete.meh"), llm.srv.URL,
		func(d *memhop.MemHopDefaults) { d.DreamCompressMinTopics = 2 }))

	graph, err := db.ImportL3([]memhop.L3ImportItem{{
		Title: "memhop", Domain: "引擎", NodeType: "concept", Content: "记忆引擎",
	}}, memhop.L3ImportSkip)
	if err != nil || len(graph.GraphIDs) != 1 {
		t.Fatalf("ImportL3: %+v err %v", graph, err)
	}
	graphID := graph.GraphIDs[0]

	// Two conversations on the same subject, so Dream links them; one is anchored to the
	// project graph and titled, the other is titled too — the merge has to pick a winner.
	settle := func(newScene bool, anchor string, turns int) (string, string) {
		var sceneID, namedTopic string
		for i := 0; i < turns; i++ {
			res, err := db.Search(memhop.SearchQuery{NewScene: newScene && i == 0, L3ID: func() string {
				if i == 0 {
					return anchor
				}
				return ""
			}()})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			sceneID = res.Scene.SceneID
			if _, err := turn(db.Session, "meh 文件的帧格式长什么样", "26 字节帧头加 crc32"); err != nil {
				t.Fatalf("turn: %v", err)
			}
			if i == 0 {
				namedTopic = res.NewTopicID
			}
		}
		return sceneID, namedTopic
	}
	primary, firstTopic := settle(true, graphID, 2)
	secondary, _ := settle(true, "", 2)

	if _, err := db.UpdateScene(primary, memhop.ScenePatch{Name: ptr("锚在项目域的那段对话")}); err != nil {
		t.Fatalf("title the primary: %v", err)
	}
	if _, err := db.UpdateScene(secondary, memhop.ScenePatch{Name: ptr("另一段对话")}); err != nil {
		t.Fatalf("title the secondary: %v", err)
	}
	if _, err := db.RenameTopic(firstTopic, "帧格式那一轮"); err != nil {
		t.Fatalf("RenameTopic: %v", err)
	}
	// Snapshot every label the host wrote before anything rewrites these records: the
	// claim is that a pass which sinks a turn into a fused group rewrites around the name,
	// so it compares the whole transcript row by row rather than following one label.
	before := topicNames(t, db, primary, secondary)
	rep, err := db.Dream(context.Background(), "")
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if rep.L2TopicsCompressed == 0 {
		t.Fatalf("nothing consolidated, so the label check below would prove nothing: %+v", rep)
	}
	nodes, err := db.ListL1()
	if err != nil || len(nodes) != 2 {
		t.Fatalf("Dream built %+v (err %v), want one node per conversation", nodes, err)
	}
	after := topicNames(t, db, primary, secondary)
	sameLabels(t, before, after, "Dream")

	if err := db.MergeScenes(primary, []string{secondary}); err != nil {
		t.Fatalf("MergeScenes: %v", err)
	}
	sameLabels(t, after, topicNames(t, db, primary), "MergeScenes")
	nodes, err = db.ListL1()
	if err != nil {
		t.Fatalf("ListL1 after the merge: %v", err)
	}
	if len(nodes) != 1 || nodes[0].SceneID != primary {
		t.Fatalf("the merged-away conversation left a node behind: %+v", nodes)
	}
	scenes, err := db.ListScenes(graphID)
	if err != nil || len(scenes) != 1 {
		t.Fatalf("anchored listing after the merge = %+v err %v, want the surviving scene alone", scenes, err)
	}
	if scenes[0].SceneID != primary || scenes[0].SceneName != "锚在项目域的那段对话" {
		t.Fatalf("the merge did not keep the primary's name and anchor: %+v", scenes[0])
	}
	if all, err := db.ListScenes(""); err != nil || len(all) != 1 {
		t.Fatalf("the swallowed scene is still listed: %+v err %v", all, err)
	}

	if err := db.DeleteScene(primary); err != nil {
		t.Fatalf("DeleteScene: %v", err)
	}
	if nodes, err := db.ListL1(); err != nil || len(nodes) != 0 {
		t.Fatalf("the deleted scene still has a node: %+v err %v", nodes, err)
	}
}

// topicNames snapshots a scene's transcript as topic id → the label its host wrote
// (empty where nobody named that turn).
func topicNames(tb testing.TB, db *testDB, sceneIDs ...string) map[string]string {
	tb.Helper()
	out := map[string]string{}
	for _, scene := range sceneIDs {
		ctx, err := db.SceneContext(scene)
		if err != nil {
			tb.Fatalf("SceneContext(%s): %v", scene, err)
		}
		for _, topic := range ctx.Topics {
			out[topic.TopicID] = topic.Name
		}
	}
	return out
}

// sameLabels reports every row both snapshots list under a different label — the shape of
// a rewrite that went over a host's name instead of around it.
func sameLabels(tb testing.TB, before, after map[string]string, stage string) {
	tb.Helper()
	for id, name := range before {
		if got, still := after[id]; still && got != name {
			tb.Errorf("%s moved the host's label on topic %s: %q -> %q", stage, id, name, got)
		}
	}
}
