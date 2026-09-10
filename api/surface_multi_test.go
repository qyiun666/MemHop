// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Multi-agent DB and per-agent session surface tests.

package api

import (
	"context"
	"testing"

	"github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
)

// testAgentHex renders the deterministic 16-char hex agent id for s (the
// same form the public Session surface accepts).
func testAgentHex(s string) string {
	return internal.FormatID(common.HashID(s))
}

func TestSurfaceMultiAgent(t *testing.T) {
	llm := stubLLM()
	t.Cleanup(llm.Close)
	m, err := OpenMulti(surfaceConfig(t, llm.URL))
	if err != nil {
		t.Fatalf("openmulti: %v", err)
	}
	defer m.Close()

	alice, err := m.CreateAgent("alice")
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := m.CreateAgent("bob")
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}
	if alice == bob {
		t.Fatal("distinct names must get distinct ids")
	}
	if !isHexID(alice) {
		t.Fatalf("agent id render: %q", alice)
	}
	agents, err := m.ListAgents()
	if err != nil || len(agents) < 2 {
		t.Fatalf("list agents: %d err=%v", len(agents), err)
	}
	// Session for an unknown agent must be rejected.
	if _, err := m.Session(testAgentHex("nobody")); CodeOf(err) != ErrAgentNotFound {
		t.Fatalf("unknown session: want ErrAgentNotFound, got %v", err)
	}

	sess, err := m.Session(alice)
	if err != nil {
		t.Fatalf("session alice: %v", err)
	}
	res, err := sess.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("alice search: %v", err)
	}
	if err := settleTurn(sess, res.Scene.SceneID, res.NewTopicID, "alice private memory", "alice reply"); err != nil {
		t.Fatalf("alice update: %v", err)
	}
	// Cross-agent isolation: bob sees none of alice's scenes.
	bobSess, _ := m.Session(bob)
	bobScenes, err := bobSess.ListScenes("")
	if err != nil {
		t.Fatalf("bob list scenes: %v", err)
	}
	if len(bobScenes) != 0 {
		t.Fatalf("bob must not see alice scenes, got %d", len(bobScenes))
	}
	aliceScenes, _ := sess.ListScenes("")
	if len(aliceScenes) == 0 {
		t.Fatal("alice must see her own scene")
	}
	// A domain with no scenes still answers a dream cleanly.
	if rep, err := bobSess.Dream(context.Background(), ""); err != nil || rep == nil {
		t.Fatalf("bob empty dream: rep=%v err=%v", rep, err)
	}
	if err := m.DeleteAgent(alice); err != nil {
		t.Fatalf("delete alice: %v", err)
	}
	// The deleted domain's handle must no longer resolve.
	if _, err := m.Session(alice); CodeOf(err) != ErrAgentNotFound {
		t.Fatalf("session after delete: want ErrNotFound, got %v", err)
	}
	// Multi-agent DB-level ops and hex id helpers.
	if err := m.Checkpoint(); err != nil {
		t.Fatalf("multi checkpoint: %v", err)
	}
	if m.IsClosed() {
		t.Fatal("multi DB must be open before close")
	}
	if _, err := m.Session("zzzz"); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("Session must reject non-hex ids, got %v", err)
	}
}

// settleTurn runs a whole turn the way a host now does: the two originals land in
// the slots dialogue owns, then the turn is settled into the topic Search opened.
func settleTurn(sess *Session, sceneID, topicID, userText, agentText string) error {
	utterances := []ArchiveSlot{
		{Kind: KindUtterance, Seq: 1, Role: RoleUser, Content: userText, CreatedAt: 1_700_000_060_000},
		{Kind: KindUtterance, Seq: 2, Role: RoleAgent, Content: agentText, CreatedAt: 1_700_000_060_500},
	}
	for _, u := range utterances {
		if err := sess.AppendArchive(topicID, u); err != nil {
			return err
		}
	}
	return sess.Update(sceneID, topicID)
}

// TestSurfaceSessionMethods exercises the full Session surface of the
// single-agent DB surface so the per-agent handle is covered end to end.
func TestSurfaceSessionMethods(t *testing.T) {
	llm := stubLLM()
	t.Cleanup(llm.Close)
	m, err := OpenMulti(surfaceConfig(t, llm.URL))
	if err != nil {
		t.Fatalf("openmulti: %v", err)
	}
	defer m.Close()
	id, err := m.CreateAgent("worker")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	s, err := m.Session(id)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	ctx := context.Background()

	if err := s.UpdateL0(&ProfileSlot{Name: "worker"}); err != nil {
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
	if err := settleTurn(s, sceneID, topicID, "session boot memory", "session reply"); err != nil {
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
	if err := settleTurn(s, res2.Scene.SceneID, res2.NewTopicID, "second session scene", "second reply"); err != nil {
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
		one, err := s.SearchL4(L4Query{IDs: []string{arcs[0].IDHash}})
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
	gid := graphs[0].IDHash
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
	if _, err := s.QueryL3Subgraph(gid, nodes[0].IDHash, 1, nil); err != nil {
		t.Fatalf("session querySubgraph: %v", err)
	}
	// Turn events via session.
	traj := internal.FormatID(common.HashID("sess-traj"))
	if err := s.AppendArchive(traj, event("tool_call", "p", 1_700_000_061_000)); err != nil {
		t.Fatalf("session appendArchive: %v", err)
	}
	if evs := eventsOf(t, s, traj); len(evs) != 1 {
		t.Fatalf("session events of %s: %d", traj, len(evs))
	}
	if _, err := s.Crystallize(ctx, traj, nil); err != nil {
		t.Fatalf("session crystallize: %v", err)
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
