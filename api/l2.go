// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 API of the public facade: thin delegation to the internal layer
// DB methods, reusing the DB instance returned by Open.

package api

import (
	"github.com/qyiun666/MemHop/internal/common"
)

// Thin wrapper; see internal/l2.go ((db *DB) ListScenes).
func (db *DB) ListScenes() ([]SceneSlot, error) {
	return db.DB.ListScenes()
}

// ActiveSceneIDs returns the active scene IDs as 16-char hex strings,
// consistent with the hex ID parameters of SceneContext / MergeScenes /
// Search.DirectedL2ID.
func (db *DB) ActiveSceneIDs() []string {
	ids := db.DB.ActiveSceneIDs()
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, common.FormatHash(id))
	}
	return out
}

// Thin wrapper; returns one scene's topics with their L4 messages.
func (db *DB) SceneContext(sceneID string) (*SceneContext, error) {
	return db.DB.SceneContext(sceneID)
}

// Thin wrapper; write op, delegates under the write lock.
func (db *DB) MergeScenes(primaryID string, secondaryIDs []string) error {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.MergeScenes(primaryID, secondaryIDs)
}

// DeleteTopic removes a topic and its whole subtree (children at any
// depth), the L4 archives they reference, and their L2Meta/sparse entries,
// so the deleted topic no longer surfaces in retrieval.
func (db *DB) DeleteTopic(topicID string) error {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.DeleteTopic(topicID)
}

// DeleteScene removes a scene: its scene record, every topic (all depths),
// the referenced L4 archives, and the L2Meta/sparse entries, so the scene
// disappears from listings and retrieval.
func (db *DB) DeleteScene(sceneID string) error {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.DeleteScene(sceneID)
}
