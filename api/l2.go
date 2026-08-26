// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 API of the public facade: thin delegation to the internal layer
// DB methods, reusing the DB instance returned by Open.

package api

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Thin wrapper; see internal/l2.go ((db *DB) ListScenes).
func (db *DB) ListScenes() ([]SceneSlot, error) {
	return db.DB.ListScenes(core.DefaultAgentID)
}

// ActiveSceneIDs returns the active scene IDs as 16-char hex strings,
// consistent with the hex ID parameters of SceneContext / MergeScenes /
// Search.DirectedL2ID.
func (db *DB) ActiveSceneIDs() []string {
	ids := db.DB.ActiveSceneIDs(core.DefaultAgentID)
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, common.FormatHash(id))
	}
	return out
}

// Thin wrapper; returns one scene's topics with their L4 messages.
func (db *DB) SceneContext(sceneID string) (*SceneContext, error) {
	return db.DB.SceneContext(core.DefaultAgentID, sceneID)
}

// Thin wrapper; see internal/l2.go ((db *DB) MergeScenes).
func (db *DB) MergeScenes(primaryID string, secondaryIDs []string) error {
	return db.DB.MergeScenes(core.DefaultAgentID, primaryID, secondaryIDs)
}

// DeleteTopic removes a topic and its whole subtree (children at any
// depth), the L4 archives they reference, and their L2Meta/sparse entries,
// so the deleted topic no longer surfaces in retrieval.
func (db *DB) DeleteTopic(topicID string) error {
	return db.DB.DeleteTopic(core.DefaultAgentID, topicID)
}

// DeleteScene removes a scene: its scene record, every topic (all depths),
// the referenced L4 archives, and the L2Meta/sparse entries, so the scene
// disappears from listings and retrieval.
func (db *DB) DeleteScene(sceneID string) error {
	return db.DB.DeleteScene(core.DefaultAgentID, sceneID)
}
