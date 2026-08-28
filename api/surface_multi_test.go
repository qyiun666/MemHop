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
	return internal.FormatAgentID(common.HashID(s))
}

func TestSurfaceMultiAgent(t *testing.T) {
	llm := stubLLM()
	t.Cleanup(llm.Close)
	m, err := OpenMultiWithEncoder(surfaceConfig(t, llm.URL), &openTestEncoder{dim: 4})
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
	if sess.AgentID() != alice {
		t.Fatalf("session id mismatch: %s", sess.AgentID())
	}
	res, err := sess.Search(context.Background(), SearchQuery{Text: "alice private memory", AutoCreate: true, Timestamp: 1_700_000_050_000})
	if err != nil {
		t.Fatalf("alice search: %v", err)
	}
	if err := sess.Update(res.NewTopicID, "alice reply", 1_700_000_050_500); err != nil {
		t.Fatalf("alice update: %v", err)
	}
	// Cross-agent isolation: bob sees none of alice's scenes.
	bobSess, _ := m.Session(bob)
	bobScenes, err := bobSess.ListScenes()
	if err != nil {
		t.Fatalf("bob list scenes: %v", err)
	}
	if len(bobScenes) != 0 {
		t.Fatalf("bob must not see alice scenes, got %d", len(bobScenes))
	}
	aliceScenes, _ := sess.ListScenes()
	if len(aliceScenes) == 0 {
		t.Fatal("alice must see her own scene")
	}
	// Empty-scene dream succeeds via the session handle.
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
	if got := sess.AgentID(); !isHexID(got) {
		t.Fatalf("agent id not hex: %q", got)
	}
	if _, err := m.Session("zzzz"); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("Session must reject non-hex ids, got %v", err)
	}
}

// TestSurfaceSessionMethods exercises the full Session surface of the
// single-agent DB surface so the per-agent handle is covered end to end.
func TestSurfaceSessionMethods(t *testing.T) {
	llm := stubLLM()
	t.Cleanup(llm.Close)
	m, err := OpenMultiWithEncoder(surfaceConfig(t, llm.URL), &openTestEncoder{dim: 4})
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

	if err := s.Checkpoint(); err != nil {
		t.Fatalf("session checkpoint: %v", err)
	}
	if s.IsClosed() {
		t.Fatal("session must be open")
	}
	if err := s.UpdateL0(&ProfileSlot{Name: "worker"}); err != nil {
		t.Fatalf("session updateL0: %v", err)
	}
	if _, err := s.GetL0(); err != nil {
		t.Fatalf("session getL0: %v", err)
	}

	res, err := s.Search(ctx, SearchQuery{Text: "session boot memory", AutoCreate: true, Timestamp: 1_700_000_060_000})
	if err != nil {
		t.Fatalf("session search: %v", err)
	}
	topicID := res.NewTopicID
	if err := s.Update(topicID, "session reply", 1_700_000_060_500); err != nil {
		t.Fatalf("session update: %v", err)
	}
	if err := s.RefineTopicKeywords(ctx, topicID); err != nil {
		t.Fatalf("session refine: %v", err)
	}
	if _, err := s.AppendL4Message(topicID, "more", 1_700_000_060_600, 0, 0); err != nil {
		t.Fatalf("session append: %v", err)
	}
	if _, err := s.SearchL4(L4Query{Keyword: "session"}); err != nil {
		t.Fatalf("session searchL4: %v", err)
	}
	scenes, err := s.ListScenes()
	if err != nil || len(scenes) == 0 {
		t.Fatalf("session listScenes: %d %v", len(scenes), err)
	}
	sceneID := scenes[0].SceneID
	if ids := s.ActiveSceneIDs(); ids != nil {
		for _, x := range ids {
			if !isHexID(x) {
				t.Fatalf("session active id not hex: %q", x)
			}
		}
	}
	if _, err := s.SceneContext(sceneID); err != nil {
		t.Fatalf("session sceneContext: %v", err)
	}
	if !s.HasActiveScenes() {
		t.Fatal("session should have an active scene after writes")
	}
	// Second scene then merge + archive fetch to cover the remaining handles.
	if _, err := s.Search(ctx, SearchQuery{Text: "second session scene", AutoCreate: true, Timestamp: 1_700_000_060_700}); err != nil {
		t.Fatalf("session search2: %v", err)
	}
	scenes, err = s.ListScenes()
	if err != nil || len(scenes) < 2 {
		t.Fatalf("session want 2 scenes: %d %v", len(scenes), err)
	}
	arcs, err := s.SearchL4(L4Query{Keyword: "session reply"})
	if err != nil {
		t.Fatalf("session searchL4 reply: %v", err)
	}
	if len(arcs) > 0 {
		if _, err := s.GetArchive(arcs[0].IDHash); err != nil {
			t.Fatalf("session getArchive: %v", err)
		}
	}
	if err := s.MergeScenes(scenes[0].SceneID, []string{scenes[1].SceneID}); err != nil {
		t.Fatalf("session mergeScenes: %v", err)
	}
	// Re-fetch the primary scene list after merge (secondary is gone).
	sceneID = scenes[0].SceneID
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
	// L5 capability via session (import → get → activate → usage → delete).
	sid := t.TempDir()
	c, err := s.ImportCapability(writeCapability(t, sid, "sess-cap"))
	if err != nil {
		t.Fatalf("session importCap: %v", err)
	}
	cid := c.IDHash
	if _, err := s.GetCapability(cid); err != nil {
		t.Fatalf("session getCap: %v", err)
	}
	if _, err := s.ListCapabilities(CapabilityListQuery{Keyword: "sess"}); err != nil {
		t.Fatalf("session listCap: %v", err)
	}
	sum := "patched via session"
	if got, err := s.UpdateCapability(cid, CapabilityPatch{Summary: &sum}); err != nil || got.Summary != sum {
		t.Fatalf("session updateCap: %v %+v", err, got)
	}
	if _, err := s.ActivateCapability(cid); err != nil {
		t.Fatalf("session activate: %v", err)
	}
	if _, err := s.RecordCapabilityUsage(cid, true); err != nil {
		t.Fatalf("session usage: %v", err)
	}
	if err := s.DeleteCapability(cid); err != nil {
		t.Fatalf("session deleteCap: %v", err)
	}
	// L6 trajectory via session.
	traj := internal.FormatAgentID(common.HashID("sess-traj"))
	if err := s.AppendTrajectory(traj, TrajectorySlot{EventType: "tool_call", Payload: "p", Timestamp: 1_700_000_061_000}); err != nil {
		t.Fatalf("session appendTraj: %v", err)
	}
	if evs, err := s.ReadTrajectory(traj); err != nil || len(evs) != 1 {
		t.Fatalf("session readTraj: %d %v", len(evs), err)
	}
	if _, err := s.Crystallize(ctx, traj); err != nil {
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
