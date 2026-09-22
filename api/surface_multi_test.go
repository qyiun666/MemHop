// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Session surface tests: every exported method of the domain handle, exercised
// against a stub LLM.

package api

import (
	"testing"
)

// turnStamp is one turn's closing time in milliseconds — the unit every timestamp here
// carries. Update records the stimulus and the answer with the one timestamp a host
// hands it, so both dialogue lines carry this value.
const turnStamp = 1_700_000_060_000

// settleTurn closes the turn the way a host does: Update writes the two originals onto
// the slots dialogue owns and distills them into the topic Search opened, whose
// distilled track comes back. A closing call names no ids: which turn is open is the
// library's own memory of the last Search, so a scenario that wants a particular turn
// closed has to do the read that opens it.
func settleTurn(sess *Session, userText, agentText string) (*TopicSlot, error) {
	return sess.Update(TurnEnd{Input: userText, Output: agentText, CreatedAt: turnStamp})
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

	// Search opens the host session; Update closes one turn into it.
	res, err := s.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("session search: %v", err)
	}
	sceneID := res.Scene.SceneID
	topicID := res.NewTopicID
	if _, err := settleTurn(s, "session boot memory", "session reply"); err != nil {
		t.Fatalf("session update: %v", err)
	}
	// What the turn holds is found by keyword under the key Search issued — the
	// dialogue lines Update wrote, and the event appended below while this turn is
	// still the one the library holds.
	hits, err := s.SearchL4(L4Query{Keyword: "session boot"})
	if err != nil || len(hits) != 1 || hits[0].TopicID != topicID {
		t.Fatalf("settled turn content = %+v err=%v", hits, err)
	}
	if _, err := s.SearchL4(L4Query{Keyword: "session"}); err != nil {
		t.Fatalf("session searchL4: %v", err)
	}
	if _, err := s.AppendArchive(event("tool_call", "p", 1_700_000_061_000)); err != nil {
		t.Fatalf("session appendArchive: %v", err)
	}
	if evs := eventsOf(t, s, topicID); len(evs) != 1 {
		t.Fatalf("session events of %s: %d", topicID, len(evs))
	}
	scenes, err := s.ListScenes("")
	if err != nil || len(scenes) == 0 {
		t.Fatalf("session listScenes: %d %v", len(scenes), err)
	}
	if _, err := s.SceneContext(sceneID); err != nil {
		t.Fatalf("session sceneContext: %v", err)
	}
	// The turn just written is what the host reads back for this session. This read
	// opens the next turn, so everything above had to be written on the first one.
	reread, err := s.Search(SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("session reread: %v", err)
	}
	if len(reread.Topics) != 1 || reread.Topics[0].ID != topicID {
		t.Fatalf("scene surface = %+v, want the one turn", reread.Topics)
	}

	// A second scene — asked for by name, since an un-named read continues the
	// current one — then merge + archive fetch to cover the rest.
	res2, err := s.Search(SearchQuery{NewScene: true})
	if err != nil {
		t.Fatalf("session search2: %v", err)
	}
	if _, err := settleTurn(s, "second session scene", "second reply"); err != nil {
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
