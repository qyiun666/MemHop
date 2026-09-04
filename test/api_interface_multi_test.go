// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests for the file-level surface a host holds: the tenant
// registry (CreateAgent / ListAgents / DeleteAgent / Session) and CompactTo.
// These are the MultiAgentDB methods, so this file works against the handle
// directly rather than through the single-domain testDB used elsewhere.

package test

import (
	"path/filepath"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
)

func mustSession(t *testing.T, m *memhop.MultiAgentDB, agentID string) *memhop.Session {
	t.Helper()
	sess, err := m.Session(agentID)
	if err != nil {
		t.Fatalf("Session(%s): %v", agentID, err)
	}
	return sess
}

// settleOneTurn opens a session in a domain and settles one turn into it, so
// the domain holds memory a test can look for.
func settleOneTurn(t *testing.T, sess *memhop.Session, user, agent string) string {
	t.Helper()
	res, err := sess.Search(memhop.SearchQuery{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	sceneID := res.Scene.SceneID
	if _, err := sess.Update(turn(sceneID, res.NewTopicID, user, agent)); err != nil {
		t.Fatalf("Update: %v", err)
	}
	return sceneID
}

// queryFor is an L4 lookup by keyword — what a host uses to ask "where did we
// talk about this".
func queryFor(keyword string) internal.L4Query {
	return internal.L4Query{Keyword: keyword}
}

func TestInterfaceAgentDomainsAreIsolated(t *testing.T) {
	llm := newMockLLM(t)
	m := openMockMulti(t, filepath.Join(t.TempDir(), "multi.meh"), llm.srv.URL)
	t.Cleanup(func() { _ = m.Close() })

	alpha, err := m.CreateAgent("alpha")
	if err != nil {
		t.Fatalf("CreateAgent alpha: %v", err)
	}
	beta, err := m.CreateAgent("beta")
	if err != nil {
		t.Fatalf("CreateAgent beta: %v", err)
	}
	// Registering the same name again must be idempotent — a host calls this at
	// every startup and treats the id as the key to its own records.
	if again, err := m.CreateAgent("alpha"); err != nil || again != alpha {
		t.Fatalf("CreateAgent(alpha) again = %s/%v, want %s", again, err, alpha)
	}
	sa, sb := mustSession(t, m, alpha), mustSession(t, m, beta)

	sceneA := settleOneTurn(t, sa, "alpha 的专属话题", "记录 alpha 的事实")
	sceneB := settleOneTurn(t, sb, "beta 的专属话题", "记录 beta 的事实")
	if sceneA == sceneB {
		t.Fatal("two domains minted the same scene id")
	}

	// Each domain lists only its own conversation, and finds only its own text.
	if scenes, err := sa.ListScenes(""); err != nil || len(scenes) != 1 || scenes[0].SceneID != sceneA {
		t.Fatalf("alpha scenes = %+v err %v, want only %s", scenes, err, sceneA)
	}
	if scenes, err := sb.ListScenes(""); err != nil || len(scenes) != 1 || scenes[0].SceneID != sceneB {
		t.Fatalf("beta scenes = %+v err %v, want only %s", scenes, err, sceneB)
	}
	if arcs, err := sa.SearchL4(queryFor("beta 的专属")); err != nil || len(arcs) != 0 {
		t.Fatalf("alpha can read beta's originals: %+v err %v", arcs, err)
	}
	if arcs, err := sb.SearchL4(queryFor("beta 的专属")); err != nil || len(arcs) == 0 {
		t.Fatalf("beta cannot read its own originals: %+v err %v", arcs, err)
	}

	// A profile belongs to one domain too.
	profile := &memhop.ProfileSlot{Name: "Only alpha"}
	if err := sa.UpdateL0(profile); err != nil {
		t.Fatalf("UpdateL0: %v", err)
	}
	if got, err := sb.GetL0(); err != nil || got.Name != "" {
		t.Fatalf("beta saw alpha's profile: %+v err %v", got, err)
	}

	// The registry lists every tenant by hex id and never the implicit default
	// domain, which has no name to report.
	agents, err := m.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("ListAgents = %+v, want the two tenants", agents)
	}
	names := map[string]string{}
	for _, a := range agents {
		if len(a.ID) != 16 || a.ID == memhop.DefaultAgentID {
			t.Fatalf("agent id %q is not a minted hex id", a.ID)
		}
		names[a.Name] = a.ID
	}
	if names["alpha"] != alpha || names["beta"] != beta {
		t.Fatalf("registry lost a mapping: %+v", agents)
	}
	for i := 1; i < len(agents); i++ {
		if agents[i-1].ID > agents[i].ID {
			t.Fatalf("ListAgents is not sorted by id: %+v", agents)
		}
	}

	// Deleting a tenant destroys its memories and its handle, and leaves the
	// neighbour alone. The tombstones cost bytes until the host compacts.
	if err := m.DeleteAgent(beta); err != nil {
		t.Fatalf("DeleteAgent(beta): %v", err)
	}
	if _, err := m.Session(beta); err == nil {
		t.Fatal("Session on a deleted tenant should be refused")
	}
	if agents, err := m.ListAgents(); err != nil || len(agents) != 1 || agents[0].Name != "alpha" {
		t.Fatalf("registry after delete = %+v err %v", agents, err)
	}
	if scenes, err := sa.ListScenes(""); err != nil || len(scenes) != 1 || scenes[0].SceneID != sceneA {
		t.Fatalf("deleting beta disturbed alpha: %+v err %v", scenes, err)
	}
	// The tombstone rejects handles the host still holds: a stale session must
	// report a failure, not answer with an empty result.
	if _, err := sb.SearchL4(queryFor("beta 的专属")); err == nil {
		t.Fatal("a handle to a deleted tenant must stop working")
	}

	// The implicit domain is not a tenant and cannot be destroyed by name, and
	// an id the registry never issued is refused at the handle boundary.
	if err := m.DeleteAgent(memhop.DefaultAgentID); err == nil {
		t.Fatal("the default domain must not be deletable")
	}
	// Deleting a domain that is not there is reported, not accepted: the
	// success return would claim a record deletion this call did not do.
	if err := m.DeleteAgent(beta); err == nil {
		t.Fatal("deleting a tenant twice should be refused")
	}
	if err := m.DeleteAgent("ffffffffffffffff"); err == nil {
		t.Fatal("deleting an unregistered agent id should be refused")
	}
	if _, err := m.Session("ffffffffffffffff"); err == nil {
		t.Fatal("Session on an unknown agent id should be refused")
	}
}

func TestInterfaceCompactTo(t *testing.T) {
	llm := newMockLLM(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "meow.meh")
	m := openMockMulti(t, path, llm.srv.URL)
	db := newTestDB(t, m)
	sceneID := settleOneTurn(t, db.Session, "将被删除的对话", "回答")
	if _, err := db.ImportL3([]internal.L3ImportItem{
		{Title: "追加式存储", Domain: "引擎", NodeType: "concept", Content: "单文件 append-only"},
	}, internal.L3ImportSkip); err != nil {
		db.Close()
		t.Fatalf("ImportL3: %v", err)
	}
	if err := db.DeleteScene(sceneID); err != nil {
		db.Close()
		t.Fatalf("DeleteScene: %v", err)
	}

	// An output path is never overwritten — the host swaps files itself.
	if err := db.CompactTo(path); err == nil {
		t.Fatal("compacting onto the open file should be refused")
	}
	taken := filepath.Join(dir, "taken.meh")
	if err := db.CompactTo(taken); err != nil {
		db.Close()
		t.Fatalf("CompactTo: %v", err)
	}
	if err := db.CompactTo(taken); err == nil {
		t.Fatal("CompactTo over an existing file should be refused")
	}

	// The live file keeps working untouched, so a host can compact before it
	// decides to swap.
	if scenes, err := db.ListScenes(""); err != nil || len(scenes) != 0 {
		db.Close()
		t.Fatalf("scenes after delete = %+v err %v, want none", scenes, err)
	}
	if graphs, err := db.ListL3(); err != nil || len(graphs) != 1 {
		db.Close()
		t.Fatalf("graphs after compact = %+v err %v", graphs, err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The copy is a complete database: it opens on its own, carries the live
	// records and none of what was deleted.
	reopened := openMockMulti(t, taken, llm.srv.URL)
	t.Cleanup(func() { _ = reopened.Close() })
	id, err := reopened.CreateAgent("test")
	if err != nil {
		t.Fatalf("CreateAgent on the compacted copy: %v", err)
	}
	sess := mustSession(t, reopened, id)
	if scenes, err := sess.ListScenes(""); err != nil || len(scenes) != 0 {
		t.Fatalf("compacted copy still holds the deleted scene: %+v err %v", scenes, err)
	}
	graphs, err := sess.ListL3()
	if err != nil || len(graphs) != 1 {
		t.Fatalf("compacted copy lost the graph: %+v err %v", graphs, err)
	}
	if got, err := sess.GetL3(graphs[0].IDHash); err != nil || len(got.Nodes) != 1 {
		t.Fatalf("compacted graph = %+v err %v", got, err)
	}
	if _, err := sess.Search(memhop.SearchQuery{SceneID: sceneID}); err == nil {
		t.Fatal("the deleted scene came back in the compacted copy")
	}
}
