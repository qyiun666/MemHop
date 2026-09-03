// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package scene holds the L2 scene small methods: the read-side resolution
// (scene lookup / allocation / turn opening / surface topics), the
// scene-context rendering steps and the deletion steps. The composition
// root keeps the big methods (Search, SceneContext, DeleteTopic,
// DeleteScene, ...) that lock the domain and compose them.

package scene

import (
	"crypto/rand"
	"encoding/binary"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ResolveForRead resolves the scene a read is scoped to. Errors from the
// record layer pass through unchanged so an unknown scene stays ErrNotFound
// and a closing database stays ErrClosed.
func ResolveForRead(engine *core.StorageEngine, agentID uint64, q core.SearchQuery) (*core.SceneSlot, error) {
	if q.SceneID == "" {
		return Create(engine, agentID, q.L3ID)
	}
	id, err := common.ParseID(q.SceneID)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	return core.ReadSceneSlot(engine, agentID, id)
}

// Create allocates a scene id the host has not used yet, persists the scene
// record under a library-generated name and applies the optional L3 anchor
// (write-once semantics).
func Create(engine *core.StorageEngine, agentID uint64, l3ID string) (*core.SceneSlot, error) {
	id, err := FreshID(engine, agentID)
	if err != nil {
		return nil, err
	}
	name := "session:" + common.FormatHash(id)
	if err := repo.CreateSceneL2WithID(engine, agentID, id, name); err != nil {
		return nil, err
	}
	if l3ID != "" {
		l3Hash, err := common.ParseID(l3ID)
		if err != nil {
			return nil, common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
		}
		if err := repo.SetSceneL3ID(engine, agentID, id, l3Hash); err != nil {
			return nil, err
		}
	}
	return core.ReadSceneSlot(engine, agentID, id)
}

// FreshID mints an unused 8-byte scene id. Zero is skipped: it is the
// "no scene" sentinel of the ID surface. A collision with a live scene would
// silently merge two host sessions, so allocation loops until the id is free.
func FreshID(engine *core.StorageEngine, agentID uint64) (uint64, error) {
	for {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return 0, common.NewError(common.ErrIO, "scene id allocation", err)
		}
		id := binary.LittleEndian.Uint64(b[:])
		if id == 0 {
			continue
		}
		if _, err := core.ReadSceneSlot(engine, agentID, id); err != nil {
			// Only "no such scene" means the id is free; any other error
			// (closing database, IO) must not mint a colliding scene.
			if common.CodeOf(err) != common.ErrNotFound {
				return 0, err
			}
			return id, nil
		}
	}
}

// OpenTurn pushes the scene's turn counter to the next turn: the one record
// a read writes. The usage counters feed Dream's importance feedback and the
// turn counter mints the topic id, so a failed write fails the read instead
// of reissuing an id.
func OpenTurn(engine *core.StorageEngine, agentID, sceneID uint64, nowMs int64) (*core.SceneSlot, error) {
	return repo.OpenSceneTurn(engine, agentID, sceneID, nowMs)
}
