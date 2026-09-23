// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// The sibling of TestDeleteL3DetachesAnchorsAcrossDomains, which covers a live tenant
// context. Two things that case cannot see: a domain whose in-memory context has already been
// reclaimed by the idle sweep (the detach walks the tenant registry, not the live contexts,
// and must reach it anyway), and whether the scenes themselves survive the detach — a listing
// by the deleted graph answers empty either way, so that assertion alone would also pass if
// the sweep deleted scenes. A dangling anchor is not cosmetic: it is an id the host cannot
// resolve, which it would hand straight back to Search or UpdateScene and hear ErrNotFound.
func TestDeleteL3DetachesAnchorsInReclaimedDomains(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	path := filepath.Join(t.TempDir(), "anchors.meh")

	defaults := DefaultMemHopDefaults
	defaults.AgentIdleTTLMs = 30 // any pause between two accesses reclaims an idle domain
	defaults.SceneDreamTopicThreshold = -1
	db, err := OpenDB(path, LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"},
		defaults, primaryProfile("primary"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	alpha, beta := createPair(t, db)

	res, err := db.ImportL3(core.DefaultAgentID, []L3ImportItem{
		{Title: "engine", Domain: "proj", NodeType: "package", Content: "one .meh file"},
	}, L3ImportOverwrite)
	if err != nil {
		t.Fatalf("ImportL3: %v", err)
	}
	graphID := res.GraphIDs[0]

	// One anchored scene per domain: the primary's own session, and a tenant's.
	for _, agent := range []uint64{core.DefaultAgentID, beta} {
		sc, err := db.Search(agent, SearchQuery{L3ID: graphID, NewScene: true})
		if err != nil {
			t.Fatalf("anchoring search on %d: %v", agent, err)
		}
		if sc.Scene.L3ID == 0 {
			t.Fatalf("scene %s was not anchored to %s", common.FormatHash(sc.Scene.SceneID), graphID)
		}
	}
	// alpha is a third domain with no anchor at all: deleting the graph must leave it alone.
	if _, err := db.Search(alpha, SearchQuery{NewScene: true}); err != nil {
		t.Fatalf("alpha search: %v", err)
	}

	// Let beta go idle past the TTL, then touch the default domain: that access runs the
	// sweep, and after it beta has no in-memory context.
	time.Sleep(80 * time.Millisecond)
	if _, err := db.Search(core.DefaultAgentID, SearchQuery{}); err != nil {
		t.Fatalf("touch default: %v", err)
	}
	db.agentsMu.Lock()
	_, live := db.agents[beta]
	db.agentsMu.Unlock()
	if live {
		t.Fatal("beta was not reclaimed, so this test would not cover the reclaimed case")
	}

	if err := db.DeleteL3(core.DefaultAgentID, graphID); err != nil {
		t.Fatalf("DeleteL3: %v", err)
	}

	dead, err := common.ParseID(graphID)
	if err != nil {
		t.Fatalf("parse graph id: %v", err)
	}
	for _, agent := range []uint64{core.DefaultAgentID, beta} {
		scenes, err := db.ListScenes(agent, "")
		if err != nil {
			t.Fatalf("ListScenes(%d): %v", agent, err)
		}
		if len(scenes) == 0 {
			t.Fatalf("domain %d lost its scenes when the graph was deleted", agent)
		}
		for _, sc := range scenes {
			if sc.L3ID == dead {
				t.Fatalf("domain %d still anchors scene %s to the deleted graph", agent, common.FormatHash(sc.SceneID))
			}
		}
		byGraph, err := db.ListScenes(agent, graphID)
		if err != nil {
			t.Fatalf("ListScenes(%d, %s): %v", agent, graphID, err)
		}
		if len(byGraph) != 0 {
			t.Fatalf("domain %d lists %d scenes under a graph that no longer exists: %+v", agent, len(byGraph), byGraph)
		}
	}

	// The domain that never anchored anything is untouched by all of this.
	untouched, err := db.ListScenes(alpha, "")
	if err != nil || len(untouched) != 1 {
		t.Fatalf("alpha's scenes = %+v err %v, want the one it created", untouched, err)
	}
}
