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
	"time"

	"github.com/qyiun666/MemHop/internal/cap/profile"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/scene"
	"github.com/qyiun666/MemHop/internal/turn"
)

// Search reads one scene and opens the turn the host is about to run: it
// returns the scene record, its depth-1 topics in turn order (the host's
// context), the domain's L0 profile and the topic id this read minted for the
// new turn — Update settles that turn into it and the L6 trajectory binds to
// it. An empty SceneID allocates a fresh scene, anchored to L3ID when given
// and named by the library; a non-empty SceneID must already exist, so a
// session always reads the scene it owns.
func (db *DB) Search(agentID uint64, q SearchQuery) (*SearchResult, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()

	resolved, err := scene.ResolveForRead(db.engine, agentID, q)
	if err != nil {
		return nil, err
	}
	sceneSlot, err := scene.OpenTurn(db.engine, agentID, resolved.SceneID, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	slot, err := turn.ReadProfile(db.engine, agentID)
	if err != nil {
		return nil, err
	}
	topics := scene.SurfaceTopics(ac, sceneSlot.SceneID)
	// TopicCount is derived, never stored: the scene's depth-1 set is exactly
	// what ListScenes counts, so the read fills the same number from the
	// topics it already loaded.
	sceneSlot.TopicCount = len(topics)
	return &SearchResult{
		Profile:      slot,
		ProfileBrief: profile.Brief(slot),
		Scene:        *sceneSlot,
		Topics:       topics,
		NewTopicID:   core.ComputeTurnTopicID(sceneSlot.SceneID, sceneSlot.TurnSeq),
	}, nil
}
