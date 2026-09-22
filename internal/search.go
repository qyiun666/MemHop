// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search of the composition root: a scene-scoped read of the host's own
// session plus the turn it opens. A scene is a host session, so Search never
// guesses which scene a message belongs to and never distills anything — it
// returns the scene's depth-1 topic set (the host's context) and the topic id
// the coming turn will settle into. The read steps live in internal/scene
// and internal/turn.

package internal

import (
	"github.com/qyiun666/MemHop/internal/cap/profile"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/scene"
	"github.com/qyiun666/MemHop/internal/turn"
)

// Search reads one scene and opens the turn the host is about to run: it
// returns the scene record, its depth-1 topics in turn order (the host's
// context), the domain's L0 profile and the topic id this read minted for the
// new turn — Settle distills that turn into it, and everything the turn records
// (its L4 content, its L5 plan tree) keys on it. An empty SceneID allocates a
// fresh scene, anchored to L3ID when given; a non-empty SceneID must already
// exist, and an L3ID handed in alongside one is refused rather than dropped,
// since that anchor is a creation-time field (UpdateScene moves it).
func (db *DB) Search(agentID uint64, q SearchQuery) (*SearchResult, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()

	sceneID, err := db.resolveScene(agentID, q)
	if err != nil {
		return nil, err
	}
	sceneSlot, err := repo.OpenSceneTurn(db.engine, agentID, sceneID)
	if err != nil {
		return nil, err
	}
	slot, err := turn.ReadProfile(db.engine, agentID)
	if err != nil {
		return nil, err
	}
	topics := scene.SurfaceTopics(ac, sceneSlot.SceneID)
	return &SearchResult{
		Profile:      slot,
		ProfileBrief: profile.Brief(slot),
		Scene:        *sceneSlot,
		Topics:       topics,
		NewTopicID:   core.ComputeTurnTopicID(sceneSlot.SceneID, sceneSlot.TurnSeq),
	}, nil
}

// resolveScene reads the host's query into the scene this read is scoped to,
// creating one when the query names none. A host's hex ids stop here: an empty
// SceneID asks for a fresh scene, and its optional anchor is parsed only on that
// path — naming a scene that cannot be read back reports the scene, not the anchor
// handed in alongside it.
func (db *DB) resolveScene(agentID uint64, q SearchQuery) (uint64, error) {
	if q.SceneID == "" {
		var anchor uint64
		if q.L3ID != "" {
			id, err := parseID("l3", q.L3ID)
			if err != nil {
				return 0, err
			}
			anchor = id
		}
		return scene.Create(db.engine, agentID, anchor)
	}
	named, err := parseID("scene", q.SceneID)
	if err != nil {
		return 0, err
	}
	return scene.ResolveExisting(db.engine, agentID, named, q.L3ID != "")
}
