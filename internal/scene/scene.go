// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package scene holds the small methods over one scene record: resolving or
// allocating it, opening its next turn, listing its depth-1 topics, rendering
// one topic together with the utterances it owns, and the deletion steps that
// keep parents, content mirrors and L3 anchors consistent.

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
	slot, err := core.ReadSceneSlot(engine, agentID, id)
	if err != nil {
		return nil, err
	}
	// The anchor is a creation-time field: a scene that already exists keeps the
	// anchor it has until UpdateScene moves it, and the named graph has to at
	// least resolve.
	if q.L3ID != "" {
		if _, err := repo.ReadSharedGraphL3(engine, q.L3ID); err != nil {
			return nil, err
		}
		return nil, common.NewError(common.ErrInvalidQuery,
			"scene "+q.SceneID+" already exists; its L3 anchor is set at creation only (use UpdateScene)")
	}
	return slot, nil
}

// Create allocates a free scene id, persists the scene record under a
// library-generated name and applies the optional L3 anchor (write-once
// semantics). The anchor is resolved before anything is written: a refusal has to
// leave no scene behind, and the id minted here never reaches the caller on that
// path.
func Create(engine *core.StorageEngine, agentID uint64, l3ID string) (*core.SceneSlot, error) {
	var anchor *core.HypergraphSlot
	if l3ID != "" {
		g, err := repo.ReadSharedGraphL3(engine, l3ID)
		if err != nil {
			return nil, err
		}
		anchor = g
	}
	id, err := FreshID(engine, agentID)
	if err != nil {
		return nil, err
	}
	name := "session:" + common.FormatHash(id)
	if err := repo.CreateSceneL2WithID(engine, agentID, id, name); err != nil {
		return nil, err
	}
	if anchor != nil {
		if err := repo.SetSceneL3ID(engine, agentID, id, anchor.IDHash); err != nil {
			return nil, err
		}
	}
	return core.ReadSceneSlot(engine, agentID, id)
}

// FreshID mints an unused 8-byte scene id. Zero is skipped: it is the
// "no scene" sentinel of the ID surface. A collision would silently merge two
// distinct scenes, so allocation loops until the id is free.
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

// OpenTurn pushes the scene's turn counter to the next turn: the scene's only
// write on the read path. The counter mints the turn's topic id, so a failed
// write must fail the caller's read instead of reissuing an id.
func OpenTurn(engine *core.StorageEngine, agentID, sceneID uint64) (*core.SceneSlot, error) {
	return repo.OpenSceneTurn(engine, agentID, sceneID)
}
