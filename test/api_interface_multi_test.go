// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests for the file-level surface a host holds: the two kinds
// of domain (the primary a file is opened on, and sub-agents addressed by name)
// and CompactTo. These are the DB handle's methods, so this file works against
// the handle directly rather than through the single-domain testDB used
// elsewhere.

package test

import (
	"path/filepath"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
)

// mustSub returns the sub-agent domain of one name, creating it the first time.
func mustSub(t *testing.T, m *memhop.DB, llmURL, name string) *memhop.Session {
	t.Helper()
	sess, err := m.SubAgent(testLLM(llmURL), memhop.ProfileInput{Name: name})
	if err != nil {
		t.Fatalf("SubAgent(%s): %v", name, err)
	}
	return sess
}

// settleOneTurn opens a session in a domain and closes one turn into it, so
// the domain holds memory a test can look for.
func settleOneTurn(t *testing.T, sess *memhop.Session, user, agent string) string {
	t.Helper()
	res, err := sess.Search(memhop.SearchQuery{NewScene: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if _, err := turn(sess, user, agent); err != nil {
		t.Fatalf("turn: %v", err)
	}
	return res.Scene.SceneID
}

// queryFor is an L4 lookup by keyword — what a host uses to ask "where did we
// talk about this".
func queryFor(keyword string) internal.L4Query {
	return internal.L4Query{Keyword: keyword}
}

func TestInterfaceAgentDomainsAreIsolated(t *testing.T) {
	llm := newMockLLM(t)
	m := openMockDB(t, filepath.Join(t.TempDir(), "multi.meh"), llm.srv.URL)
	t.Cleanup(func() { _ = m.Close() })

	sa := mustSub(t, m, llm.srv.URL, "alpha")
	sb := mustSub(t, m, llm.srv.URL, "beta")

	// A name is the domain's address, so asking twice is one domain: a host calls
	// this at every startup and treats the name as the key to its own records. The
	// second handle has to read what the first one settled — proved below, once
	// there is something to read.
	again := mustSub(t, m, llm.srv.URL, "alpha")

	sceneA := settleOneTurn(t, sa, "alpha 的专属话题", "记录 alpha 的事实")
	sceneB := settleOneTurn(t, sb, "beta 的专属话题", "记录 beta 的事实")
	if sceneA == sceneB {
		t.Fatal("two domains minted the same scene id")
	}

	// Same name, same domain: the second handle lists the scene the first one
	// settled and reads the originals written under it.
	if scenes, err := again.ListScenes(""); err != nil || len(scenes) != 1 || scenes[0].SceneID != sceneA {
		t.Fatalf("the second alpha handle = %+v err %v, want the scene the first one settled", scenes, err)
	}
	if arcs, err := again.SearchL4(queryFor("alpha 的专属")); err != nil || len(arcs) == 0 {
		t.Fatalf("the second alpha handle cannot read what the first settled: %+v err %v", arcs, err)
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

	// A profile belongs to one domain too, and each sub-agent domain is stamped
	// as one: the identity is the library's, not the caller's. The handle itself is
	// not a field this write can move (TestInterfaceSubAgentNameIsItsHandle), so the
	// domain-owned text written here is the Role.
	if err := sa.UpdateL0(memhop.ProfileInput{Name: "alpha", Role: "only alpha"}); err != nil {
		t.Fatalf("UpdateL0: %v", err)
	}
	alphaL0, err := sa.GetL0()
	if err != nil {
		t.Fatalf("alpha GetL0: %v", err)
	}
	if alphaL0.Name != "alpha" || alphaL0.Role != "only alpha" || alphaL0.AgentType != memhop.AgentTypeSub {
		t.Fatalf("alpha's profile = %+v, want its own handle and role, stamped as a sub-agent", alphaL0)
	}
	betaL0, err := sb.GetL0()
	if err != nil {
		t.Fatalf("beta GetL0: %v", err)
	}
	if betaL0.Role == "only alpha" {
		t.Fatalf("beta saw alpha's profile: %+v", betaL0)
	}
	if betaL0.AgentType != memhop.AgentTypeSub {
		t.Fatalf("beta is not stamped as a sub-agent: %+v", betaL0)
	}

	// The primary is a third domain, apart from both, and stamped as the primary.
	primary, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	primaryL0, err := primary.GetL0()
	if err != nil {
		t.Fatalf("primary GetL0: %v", err)
	}
	if primaryL0.AgentType != memhop.AgentTypePrimary {
		t.Fatalf("the file's primary is not stamped as one: %+v", primaryL0)
	}
	if scenes, err := primary.ListScenes(""); err != nil || len(scenes) != 0 {
		t.Fatalf("the primary sees a sub-agent's scenes: %+v err %v", scenes, err)
	}

	// L3 is the one file-wide pool: a graph alpha imports is visible to beta and
	// to the primary through list and query.
	if _, err := sa.ImportL3([]internal.L3ImportItem{
		{Title: "append-only", Domain: "engine", NodeType: "concept", Content: "single .meh"},
	}, internal.L3ImportSkip); err != nil {
		t.Fatalf("ImportL3: %v", err)
	}
	graphs, err := sb.ListL3()
	if err != nil || len(graphs) != 1 {
		t.Fatalf("beta cannot see alpha's graph: %+v err %v", graphs, err)
	}
	if nodes, err := sb.QueryL3Nodes(internal.L3NodeQuery{GraphID: graphs[0].ID}); err != nil || len(nodes) != 1 {
		t.Fatalf("beta query over alpha's graph: %+v err %v", nodes, err)
	}
	if shared, err := primary.ListL3(); err != nil || len(shared) != 1 {
		t.Fatalf("the primary cannot see the shared pool: %+v err %v", shared, err)
	}
}

// A sub-agent domain is addressed by name, so a restart finds the same one
// instead of minting a second — that is what lets a host treat the name as the
// key to its own records. A name nobody registered is a new empty domain, not an
// error and not somebody else's memory.
func TestInterfaceSubAgentDomainSurvivesReopen(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "reopen.meh")
	m := openMockDB(t, path, llm.srv.URL)
	sa := mustSub(t, m, llm.srv.URL, "alpha")
	sceneA := settleOneTurn(t, sa, "alpha 的记忆", "记下了")
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened := openMockDB(t, path, llm.srv.URL)
	t.Cleanup(func() { _ = reopened.Close() })
	back := mustSub(t, reopened, llm.srv.URL, "alpha")
	scenes, err := back.ListScenes("")
	if err != nil || len(scenes) != 1 || scenes[0].SceneID != sceneA {
		t.Fatalf("the reopened domain lost its scene: %+v err %v", scenes, err)
	}
	fresh := mustSub(t, reopened, llm.srv.URL, "gamma")
	if scenes, err := fresh.ListScenes(""); err != nil || len(scenes) != 0 {
		t.Fatalf("a fresh name inherited memory: %+v err %v", scenes, err)
	}
}

// A sub-agent's Name is the handle its door is filed under, so `UpdateL0` cannot move
// it: the write is refused, one name stays in both the roster and the profile, and the
// door opens the same memory. Before this refusal a host that renamed its worker's
// profile got a roster saying one thing and a profile another, and opening the worker by
// its new name answered with an empty domain — amnesia with no error to read. The
// primary is exempt because nothing addresses it by name, so its label is free text.
func TestInterfaceSubAgentNameIsItsHandle(t *testing.T) {
	llm := newMockLLM(t)
	m := openMockDB(t, filepath.Join(t.TempDir(), "names.meh"), llm.srv.URL)
	t.Cleanup(func() { _ = m.Close() })
	url := llm.srv.URL

	worker := mustSub(t, m, url, "worker")
	id := worker.AgentID()
	scene := settleOneTurn(t, worker, "worker 的第一轮", "记下了")

	for _, want := range []string{"renamed", " worker ", "worker\u200b"} {
		if err := worker.UpdateL0(memhop.ProfileInput{Name: want, Role: "helper"}); memhop.CodeOf(err) != memhop.ErrInvalidQuery {
			t.Fatalf("UpdateL0(Name=%q): want ErrInvalidQuery, got %v", want, err)
		}
	}
	stored, err := worker.GetL0()
	if err != nil || stored.Name != "worker" || stored.Role != "" {
		t.Fatalf("a refused rename left the profile moved: %+v err %v", stored, err)
	}
	if agents, err := m.Agents(); err != nil || len(agents) != 2 {
		t.Fatalf("roster after the refusals = %+v err %v, want the two domains it started with", agents, err)
	} else {
		for _, a := range agents {
			if a.ID == id && a.Name != "worker" {
				t.Fatalf("the roster names the domain %q while its profile says %q", a.Name, stored.Name)
			}
		}
	}
	if again := mustSub(t, m, url, "worker"); again.AgentID() != id {
		t.Fatalf("SubAgent(%q) opened a second domain %s instead of %s", "worker", again.AgentID(), id)
	}
	if scenes, err := worker.ListScenes(""); err != nil || len(scenes) != 1 || scenes[0].SceneID != scene {
		t.Fatalf("the domain's own memory moved: %+v err %v", scenes, err)
	}

	// The same handle restated is an ordinary write, and so is the other half of the
	// record: only a different Name is refused.
	if err := worker.UpdateL0(memhop.ProfileInput{Name: "worker", Role: "helper"}); err != nil {
		t.Fatalf("restating the handle: %v", err)
	}
	if slot, err := worker.GetL0(); err != nil || slot.Role != "helper" || slot.Name != "worker" {
		t.Fatalf("profile after the accepted write = %+v err %v", slot, err)
	}
	if err := worker.UpdateL0(memhop.ProfileInput{}); memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("empty Name: want ErrInvalidQuery, got %v", err)
	}

	primary, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	if err := primary.UpdateL0(memhop.ProfileInput{Name: "renamed-primary", Role: "the file's own domain"}); err != nil {
		t.Fatalf("the primary's label is free text: %v", err)
	}
	agents, err := m.Agents()
	if err != nil || len(agents) != 2 || agents[0].Name != "renamed-primary" || !agents[0].Primary {
		t.Fatalf("the roster does not follow the primary's own name: %+v err %v", agents, err)
	}
}

func TestInterfaceCompactTo(t *testing.T) {
	llm := newMockLLM(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "meow.meh")
	m := openMockDB(t, path, llm.srv.URL)
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
	// records and none of what was deleted. It also carries the primary the
	// original was opened with, which is what lets it open at all.
	reopened := openMockDB(t, taken, llm.srv.URL)
	t.Cleanup(func() { _ = reopened.Close() })
	sess, err := reopened.Primary()
	if err != nil {
		t.Fatalf("Primary on the compacted copy: %v", err)
	}
	if scenes, err := sess.ListScenes(""); err != nil || len(scenes) != 0 {
		t.Fatalf("compacted copy still holds the deleted scene: %+v err %v", scenes, err)
	}
	graphs, err := sess.ListL3()
	if err != nil || len(graphs) != 1 {
		t.Fatalf("compacted copy lost the graph: %+v err %v", graphs, err)
	}
	if got, err := sess.GetL3(graphs[0].ID); err != nil || len(got.Nodes) != 1 {
		t.Fatalf("compacted graph = %+v err %v", got, err)
	}
	if _, err := sess.Search(memhop.SearchQuery{SceneID: sceneID}); err == nil {
		t.Fatal("the deleted scene came back in the compacted copy")
	}
}
