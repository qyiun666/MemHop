// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search of the composition root: a scene-scoped read of the host's own
// session plus the turn it opens. A scene is a host session, so Search never
// guesses which scene a message belongs to and never distills anything — it
// returns the scene's depth-1 topic set (the host's context) and the topic id
// the coming turn will settle into.

package internal

import (
	"cmp"
	"crypto/rand"
	"encoding/binary"
	"slices"
	"time"

	"github.com/qyiun666/MemHop/internal/cap/profile"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
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

	scene, err := db.sceneForSearch(ac, q)
	if err != nil {
		return nil, err
	}
	// Opening the turn is the one record a read writes: the usage counters
	// feed Dream's importance feedback and the turn counter mints the topic
	// id, so a failed write fails the read instead of reissuing an id.
	scene, err = repo.OpenSceneTurn(db.engine, agentID, scene.SceneID, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	slot, err := turn.ReadProfile(db.engine, agentID)
	if err != nil {
		return nil, err
	}
	topics := sceneSurfaceTopics(ac, scene.SceneID)
	// TopicCount is derived, never stored: the scene's depth-1 set is exactly
	// what ListScenes counts, so the read fills the same number from the
	// topics it already loaded.
	scene.TopicCount = len(topics)
	return &SearchResult{
		Profile:      slot,
		ProfileBrief: profile.Brief(slot),
		Scene:        *scene,
		Topics:       topics,
		NewTopicID:   core.ComputeTurnTopicID(scene.SceneID, scene.TurnSeq),
	}, nil
}

// sceneForSearch resolves the scene a read is scoped to. Errors from the
// record layer pass through unchanged so an unknown scene stays ErrNotFound
// and a closing database stays ErrClosed.
func (db *DB) sceneForSearch(ac *domain.Context, q SearchQuery) (*core.SceneSlot, error) {
	if q.SceneID == "" {
		return db.newScene(ac, q)
	}
	id, err := common.ParseID(q.SceneID)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	return core.ReadSceneSlot(db.engine, ac.ID, id)
}

// newScene allocates a scene id the host has not used yet, persists the scene
// record under a library-generated name and applies the optional L3 anchor
// (write-once semantics).
func (db *DB) newScene(ac *domain.Context, q SearchQuery) (*core.SceneSlot, error) {
	id, err := db.freshSceneID(ac.ID)
	if err != nil {
		return nil, err
	}
	name := "session:" + common.FormatHash(id)
	if err := repo.CreateSceneL2WithID(db.engine, ac.ID, id, name); err != nil {
		return nil, err
	}
	if q.L3ID != "" {
		l3Hash, err := common.ParseID(q.L3ID)
		if err != nil {
			return nil, common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
		}
		if err := repo.SetSceneL3ID(db.engine, ac.ID, id, l3Hash); err != nil {
			return nil, err
		}
	}
	return core.ReadSceneSlot(db.engine, ac.ID, id)
}

// freshSceneID mints an unused 8-byte scene id. Zero is skipped: it is the
// "no scene" sentinel of the ID surface. A collision with a live scene would
// silently merge two host sessions, so allocation loops until the id is free.
func (db *DB) freshSceneID(agentID uint64) (uint64, error) {
	for {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return 0, common.NewError(common.ErrIO, "scene id allocation", err)
		}
		id := binary.LittleEndian.Uint64(b[:])
		if id == 0 {
			continue
		}
		if _, err := core.ReadSceneSlot(db.engine, agentID, id); err != nil {
			// Only "no such scene" means the id is free; any other error
			// (closing database, IO) must not mint a colliding scene.
			if common.CodeOf(err) != common.ErrNotFound {
				return 0, err
			}
			return id, nil
		}
	}
}

// sceneSurfaceTopics returns one scene's depth-1 topics in turn order: the
// read surface a host injects as its conversation context. It is served from
// the L2Meta cache, so a read costs no record scan; ties break by ID to keep
// the order deterministic.
func sceneSurfaceTopics(ac *domain.Context, sceneID uint64) []core.TopicSlot {
	out := make([]core.TopicSlot, 0, 16)
	for _, id := range ac.L2Meta.GetByScene(sceneID) {
		meta := ac.L2Meta.Get(id)
		if meta == nil || meta.Depth != 1 {
			continue
		}
		out = append(out, meta.ToTopicSlot())
	}
	slices.SortFunc(out, func(a, b core.TopicSlot) int {
		if a.UserTimestamp != b.UserTimestamp {
			return cmp.Compare(a.UserTimestamp, b.UserTimestamp)
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}
