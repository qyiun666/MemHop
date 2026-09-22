// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package scene holds the small methods over one scene record: resolving or
// allocating it, listing its depth-1 topics, rendering one topic together with
// the utterances it owns, and the deletion steps that keep parents, content
// mirrors and L3 anchors consistent.

package scene

import (
	"crypto/rand"
	"encoding/binary"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ResolveExisting answers which scene a read is scoped to when the host named one,
// and refuses the combination where the host also handed over an anchor: it returns
// the id, not the record — opening the turn is the step that reads the scene back to
// bump its counter. Record-layer errors pass through unchanged, so an unknown scene
// stays ErrNotFound and a closing database ErrClosed.
//
// The anchor is creation-time only: a scene that already exists keeps its anchor
// until UpdateScene moves it, so this refusal needs no lookup of the named graph.
func ResolveExisting(engine *core.StorageEngine, agentID uint64, sceneID uint64, anchored bool) (uint64, error) {
	slot, err := core.ReadSceneSlot(engine, agentID, sceneID)
	if err != nil {
		return 0, err
	}
	if anchored {
		return 0, common.NewError(common.ErrInvalidQuery,
			"scene "+common.FormatHash(sceneID)+" already exists; its L3 anchor is set at creation only (use UpdateScene)")
	}
	return slot.SceneID, nil
}

// Create allocates a free scene id and persists the scene under a library-generated
// name, anchored on the given L3 graph when one is named (0 anchors nothing). The
// anchor graph is resolved before anything is written: a refusal has to leave no
// scene behind. Nothing is read back afterwards — freshID proved the id free under
// the caller's domain lock.
func Create(engine *core.StorageEngine, agentID uint64, anchor uint64) (uint64, error) {
	if anchor != 0 {
		g, err := repo.ReadSharedGraphL3(engine, anchor)
		if err != nil {
			return 0, err
		}
		anchor = g.IDHash
	}
	id, err := freshID(engine, agentID)
	if err != nil {
		return 0, err
	}
	slot := core.NewSceneSlot(id, "session:"+common.FormatHash(id))
	slot.L3ID = anchor
	if err := repo.CreateSceneL2(engine, agentID, &slot); err != nil {
		return 0, err
	}
	return slot.SceneID, nil
}

// freshID mints an unused 8-byte scene id. Zero is skipped: it is the
// "no scene" sentinel of the ID surface. A collision would silently merge two
// distinct scenes, so allocation loops until the id is free.
func freshID(engine *core.StorageEngine, agentID uint64) (uint64, error) {
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
			// Any error other than "nothing here" must not mint a scene: a closing
			// database or an IO failure says nothing about whether the id is free.
			if common.CodeOf(err) != common.ErrNotFound {
				return 0, err
			}
			return id, nil
		}
	}
}
