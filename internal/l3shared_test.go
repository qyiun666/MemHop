// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The file-wide shared L3 pool: one file hosts a single knowledge-graph pool
// (core.SharedPoolAgentID) that every agent domain reads and writes, while
// scenes, archives and profiles stay domain-local.

package internal

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/llm"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func newSharedL3DB(t *testing.T, llmURL string) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "l3shared.meh")
	engine, err := core.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	db := newTestDB(t, engine)
	db.llm = llm.New(&MemHopConfig{LLM: LlmConfig{APIURL: llmURL, APIKey: "test", Model: "mock"}})
	t.Cleanup(func() { _ = db.Close() })
	return db, path
}

func createPair(t *testing.T, db *DB) (alpha, beta uint64) {
	t.Helper()
	alpha, err := db.CreateAgent("alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err = db.CreateAgent("beta")
	if err != nil {
		t.Fatal(err)
	}
	return alpha, beta
}

// Import as one agent, read and query as another, anchor a scene on it: the
// pool is file-wide, not per-domain.
func TestL3PoolSharedAcrossAgents(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["x"]}`)
	db, _ := newSharedL3DB(t, srv.URL)
	alpha, beta := createPair(t, db)

	res, err := db.ImportL3(alpha, []L3ImportItem{
		{Title: "append-only", Domain: "engine", Content: "single .meh"},
	}, L3ImportSkip)
	if err != nil {
		t.Fatal(err)
	}
	graphID := res.GraphIDs[0]

	graphs, err := db.ListL3(beta)
	if err != nil || len(graphs) != 1 {
		t.Fatalf("beta ListL3 = %+v err %v, want alpha's graph", graphs, err)
	}
	nodes, err := db.QueryL3Nodes(beta, L3NodeQuery{GraphID: graphID})
	if err != nil || len(nodes) != 1 {
		t.Fatalf("beta QueryL3Nodes = %+v err %v", nodes, err)
	}

	// Anchoring validates against the shared pool, not the caller's domain.
	sc, err := db.Search(beta, SearchQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateScene(beta, common.FormatHash(sc.Scene.SceneID), ScenePatch{L3ID: &graphID}); err != nil {
		t.Fatalf("beta anchor on alpha's graph: %v", err)
	}
}

// The shared domain is reserved infrastructure: never listed, never bindable
// — yet reachable through any live caller.
func TestSharedL3DomainIsReserved(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["x"]}`)
	db, _ := newSharedL3DB(t, srv.URL)

	if err := db.CheckSession(core.SharedPoolAgentID); err == nil {
		t.Fatal("Session on the shared domain must be refused")
	}
	if _, err := db.ListL3(core.DefaultAgentID); err != nil {
		t.Fatalf("a live caller must reach the pool: %v", err)
	}
	agents, err := db.ListAgents()
	if err != nil || len(agents) != 0 {
		t.Fatalf("ListAgents must not list the shared domain: %+v err %v", agents, err)
	}
}

// Deleting a graph detaches the scenes that anchor it in every domain, not
// just the caller's own.
func TestDeleteL3DetachesAnchorsAcrossDomains(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["x"]}`)
	db, _ := newSharedL3DB(t, srv.URL)
	alpha, beta := createPair(t, db)

	res, err := db.ImportL3(alpha, []L3ImportItem{{Title: "proj", Domain: "proj", Content: "c"}}, L3ImportSkip)
	if err != nil {
		t.Fatal(err)
	}
	graphID := res.GraphIDs[0]
	sc, err := db.Search(beta, SearchQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateScene(beta, common.FormatHash(sc.Scene.SceneID), ScenePatch{L3ID: &graphID}); err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteL3(alpha, graphID); err != nil {
		t.Fatalf("DeleteL3: %v", err)
	}
	if scenes, err := db.ListScenes(beta, graphID); err != nil || len(scenes) != 0 {
		t.Fatalf("beta scene still anchored to the deleted graph: %+v err %v", scenes, err)
	}
	if _, err := db.GetL3(alpha, graphID); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("graph survived deletion: %v", err)
	}
}

// The hostile sibling of the sequential case above: a scene anchors the graph
// while DeleteL3 is mid-detach. The anchor write holds its domain lock across
// validation and write, and the detach phase takes that same lock, so an
// anchor either fails validation (graph already gone) or is written first and
// cleared by the detach — once DeleteL3 returns, no scene carries the deleted
// id. Breaking either half of that lock discipline leaves a dangling anchor
// and fails the per-round assertion.
func TestDeleteL3RaceWithSceneAnchor(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["x"]}`)
	db, _ := newSharedL3DB(t, srv.URL)
	alpha, beta := createPair(t, db)

	sc, err := db.Search(beta, SearchQuery{})
	if err != nil {
		t.Fatal(err)
	}
	sceneID := common.FormatHash(sc.Scene.SceneID)

	const rounds = 50
	for i := 0; i < rounds; i++ {
		res, err := db.ImportL3(alpha, []L3ImportItem{{Title: "proj", Domain: "proj", Content: "c"}}, L3ImportSkip)
		if err != nil {
			t.Fatal(err)
		}
		graphID := res.GraphIDs[0]

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = db.UpdateScene(beta, sceneID, ScenePatch{L3ID: &graphID})
		}()
		go func() {
			defer wg.Done()
			_ = db.DeleteL3(alpha, graphID)
		}()
		wg.Wait()

		scenes, err := db.ListScenes(beta, graphID)
		if err != nil || len(scenes) != 0 {
			t.Fatalf("round %d: scene anchored to the deleted graph: %+v err %v", i, scenes, err)
		}
	}
}

// The shared pool rides the ordinary persistence path: close, reopen, and a
// registered tenant sees its graphs again.
func TestL3PoolSurvivesRestart(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["x"]}`)
	db, path := newSharedL3DB(t, srv.URL)
	alpha, beta := createPair(t, db)

	if _, err := db.ImportL3(alpha, []L3ImportItem{{Title: "n", Domain: "d", Content: "c"}}, L3ImportSkip); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	engine, err := core.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	db2 := newTestDB(t, engine)
	// The real Open assembly reloads the tenant registry; mirror it here.
	db2.idToName, db2.nameToID = loadTenantRegistry(engine)
	graphs, err := db2.ListL3(beta)
	if err != nil || len(graphs) != 1 {
		t.Fatalf("reopened file lost the shared pool: %+v err %v", graphs, err)
	}
}

// Concurrent imports and cross-domain anchor writes race the shared pool:
// the caller holds only its own domain lock while validating against the
// pool, so the engine-level record mutex is what keeps these reads clean —
// the race detector is the assertion, not the return values.
func TestL3PoolConcurrentImportAndAnchor(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["x"]}`)
	db, _ := newSharedL3DB(t, srv.URL)
	alpha, beta := createPair(t, db)

	res, err := db.ImportL3(alpha, []L3ImportItem{{Title: "n0", Domain: "d", Content: "c"}}, L3ImportSkip)
	if err != nil {
		t.Fatal(err)
	}
	graphID := res.GraphIDs[0]
	sc, err := db.Search(beta, SearchQuery{})
	if err != nil {
		t.Fatal(err)
	}

	const rounds = 50
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			_, _ = db.ImportL3(alpha, []L3ImportItem{
				{Title: fmt.Sprintf("n%d", i), Domain: "d", Content: "c"},
			}, L3ImportOverwrite)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			_, _ = db.UpdateScene(beta, common.FormatHash(sc.Scene.SceneID), ScenePatch{L3ID: &graphID})
		}
	}()
	wg.Wait()
}
