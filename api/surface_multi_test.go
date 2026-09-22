// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Session surface tests: every exported method of the domain handle, exercised
// against a stub LLM.

package api

import (
	"testing"
)

// settleTurn runs a whole turn the way a host now does: the two originals land in
// the slots dialogue owns, then the turn is settled into the topic Search opened,
// whose distilled track comes back.
func settleTurn(sess *Session, sceneID, topicID, userText, agentText string) (*TopicSlot, error) {
	utterances := []ArchiveSlot{
		{Kind: KindUtterance, Seq: 1, Role: RoleUser, Content: userText, CreatedAt: 1_700_000_060_000},
		{Kind: KindUtterance, Seq: 2, Role: RoleAgent, Content: agentText, CreatedAt: 1_700_000_060_500},
	}
	for _, u := range utterances {
		if _, err := sess.AppendArchive(sceneID, topicID, u); err != nil {
			return nil, err
		}
	}
	return sess.Settle(sceneID, topicID)
}

// TestSurfaceSessionMethods exercises the full Session surface of one domain
// handle so every method is covered end to end.
func TestSurfaceSessionMethods(t *testing.T) {
	llm := stubLLM()
	t.Cleanup(llm.Close)
	m, s := openSurfaceSession(t, llm.URL)
	defer m.Close()

	if err := s.UpdateL0(&ProfileInput{Name: "worker"}); err != nil {
		t.Fatalf("session updateL0: %v", err)
	}
	if _, err := s.GetL0(); err != nil {
		t.Fatalf("session getL0: %v", err)
	}

	// Search opens the host session; Update settles one turn into it.
	res, err := s.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("session search: %v", err)
	}
	sceneID := res.Scene.SceneID
	topicID := res.NewTopicID
	if _, err := settleTurn(s, sceneID, topicID, "session boot memory", "session reply"); err != nil {
		t.Fatalf("session update: %v", err)
	}
	// Update writes no content: the turn's records are the ones the host appended,
	// and a keyword search finds them under the key Search issued.
	hits, err := s.SearchL4(L4Query{Keyword: "session boot"})
	if err != nil || len(hits) != 1 || hits[0].TopicID != topicID {
		t.Fatalf("settled turn content = %+v err=%v", hits, err)
	}
	if _, err := s.SearchL4(L4Query{Keyword: "session"}); err != nil {
		t.Fatalf("session searchL4: %v", err)
	}
	scenes, err := s.ListScenes("")
	if err != nil || len(scenes) == 0 {
		t.Fatalf("session listScenes: %d %v", len(scenes), err)
	}
	if _, err := s.SceneContext(sceneID); err != nil {
		t.Fatalf("session sceneContext: %v", err)
	}
	// The turn just written is what the host reads back for this session.
	reread, err := s.Search(SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("session reread: %v", err)
	}
	if len(reread.Topics) != 1 || reread.Topics[0].ID != topicID {
		t.Fatalf("scene surface = %+v, want the one turn", reread.Topics)
	}

	// Second session scene, then merge + archive fetch to cover the rest.
	res2, err := s.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("session search2: %v", err)
	}
	if _, err := settleTurn(s, res2.Scene.SceneID, res2.NewTopicID, "second session scene", "second reply"); err != nil {
		t.Fatalf("session update2: %v", err)
	}
	scenes, err = s.ListScenes("")
	if err != nil || len(scenes) < 2 {
		t.Fatalf("session want 2 scenes: %d %v", len(scenes), err)
	}
	arcs, err := s.SearchL4(L4Query{Keyword: "session reply"})
	if err != nil {
		t.Fatalf("session searchL4 reply: %v", err)
	}
	if len(arcs) > 0 {
		one, err := s.SearchL4(L4Query{IDs: []string{arcs[0].ID}})
		if err != nil || len(one) != 1 {
			t.Fatalf("session archive by id: %d found, err %v", len(one), err)
		}
	}
	if err := s.MergeScenes(sceneID, []string{res2.Scene.SceneID}); err != nil {
		t.Fatalf("session mergeScenes: %v", err)
	}
	// L3 knowledge via session.
	if _, err := s.ImportL3([]L3ImportItem{{Title: "n1", Domain: "d", NodeType: "c", Content: "x", Keywords: []string{"k"}}}, L3ImportSkip); err != nil {
		t.Fatalf("session importL3: %v", err)
	}
	graphs, err := s.ListL3()
	if err != nil || len(graphs) == 0 {
		t.Fatalf("session listL3: %d %v", len(graphs), err)
	}
	gid := graphs[0].ID
	if _, err := s.GetL3(gid); err != nil {
		t.Fatalf("session getL3: %v", err)
	}
	rn := "nn"
	if _, err := s.UpdateL3(gid, &rn); err != nil {
		t.Fatalf("session updateL3: %v", err)
	}
	nodes, err := s.QueryL3Nodes(L3NodeQuery{GraphID: gid, NodeType: "c"})
	if err != nil || len(nodes) == 0 {
		t.Fatalf("session queryNodes: %d %v", len(nodes), err)
	}
	if _, err := s.QueryL3Subgraph(gid, nodes[0].ID, 1, nil); err != nil {
		t.Fatalf("session querySubgraph: %v", err)
	}
	// Turn events via session, under the turn key Search already handed back.
	if _, err := s.AppendArchive(sceneID, topicID, event("tool_call", "p", 1_700_000_061_000)); err != nil {
		t.Fatalf("session appendArchive: %v", err)
	}
	if evs := eventsOf(t, s, topicID); len(evs) != 1 {
		t.Fatalf("session events of %s: %d", topicID, len(evs))
	}
	// Deletion lifecycle: topic, scene, graph.
	if err := s.DeleteTopic(topicID); err != nil {
		t.Fatalf("session deleteTopic: %v", err)
	}
	if err := s.DeleteScene(sceneID); err != nil {
		t.Fatalf("session deleteScene: %v", err)
	}
	if err := s.DeleteL3(gid); err != nil {
		t.Fatalf("session deleteL3: %v", err)
	}
}
