// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search of the composition root: a scene-scoped read of the host's own
// session. A scene is a host session, so Search never guesses which scene a
// message belongs to and never distills anything — it returns the scene's
// depth-1 topic set, which is the host's context for that session.

package internal

import (
	"cmp"
	"crypto/rand"
	"encoding/binary"
	"log/slog"
	"slices"
	"time"

	"github.com/qyiun666/MemHop/internal/cap/profile"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Search reads one scene: its record plus its depth-1 topics in turn order.
// An empty SceneID allocates a fresh scene (anchored to L3ID when given,
// named SceneName or "session:<id>"); a non-empty SceneID must already exist,
// so a session always reads the scene it owns. The only write is the scene's
// usage counter, which feeds Dream's importance feedback.
func (db *DB) Search(agentID uint64, q SearchQuery) (*SearchResult, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()

	scene, err := db.sceneForSearch(ac, q)
	if err != nil {
		return nil, err
	}
	// The usage counter is the only record a read touches; it feeds Dream's
	// importance feedback. Return the bumped record, never the pre-read copy.
	if touched, terr := repo.TouchSceneUsage(db.engine, agentID, scene.SceneID, time.Now().UnixMilli()); terr != nil {
		slog.Warn("search: record scene usage failed", "err", terr)
	} else {
		scene = touched
	}
	slot := db.readProfile(agentID)
	topics := ac.sceneSurfaceTopics(scene.SceneID)
	// TopicCount is derived, never stored: the scene's depth-1 set is exactly
	// what ListScenes counts, so the read fills the same number from the
	// topics it already loaded.
	scene.TopicCount = len(topics)
	return &SearchResult{
		Profile:      slot,
		ProfileBrief: profile.Brief(slot),
		Scene:        *scene,
		Topics:       topics,
	}, nil
}

// sceneForSearch resolves the scene a read is scoped to. Errors from the
// record layer pass through unchanged so an unknown scene stays ErrNotFound
// and a closing database stays ErrClosed.
func (db *DB) sceneForSearch(ac *agentContext, q SearchQuery) (*core.SceneSlot, error) {
	if q.SceneID == "" {
		return db.newScene(ac, q)
	}
	id, err := common.ParseID(q.SceneID)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	return core.ReadSceneSlot(db.engine, ac.id, id)
}

// newScene allocates a scene id the host has not used yet, persists the scene
// record and applies the optional L3 anchor (write-once semantics).
func (db *DB) newScene(ac *agentContext, q SearchQuery) (*core.SceneSlot, error) {
	id, err := db.freshSceneID(ac.id)
	if err != nil {
		return nil, err
	}
	name := q.SceneName
	if name == "" {
		name = "session:" + common.FormatHash(id)
	}
	if err := repo.CreateSceneL2WithID(db.engine, ac.id, id, name); err != nil {
		return nil, err
	}
	if q.L3ID != "" {
		l3Hash, err := common.ParseID(q.L3ID)
		if err != nil {
			return nil, common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
		}
		if err := repo.SetSceneL3ID(db.engine, ac.id, id, l3Hash); err != nil {
			return nil, err
		}
	}
	return core.ReadSceneSlot(db.engine, ac.id, id)
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
func (ac *agentContext) sceneSurfaceTopics(sceneID uint64) []core.TopicSlot {
	out := make([]core.TopicSlot, 0, 16)
	for _, id := range ac.l2Meta.GetByScene(sceneID) {
		meta := ac.l2Meta.Get(id)
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

func (db *DB) readProfile(agentID uint64) core.ProfileSlot {
	slot, err := repo.GetProfileL0(db.engine, agentID)
	if err != nil {
		return core.ProfileSlot{}
	}
	return *slot
}
