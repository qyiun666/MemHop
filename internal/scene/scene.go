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

// ResolveForRead answers which scene a read is scoped to, creating one when the
// query names none. It returns the id and not the record: the read that follows
// opens the turn, and that is the step which has to read the scene back to bump
// its counter. Errors from the record layer pass through unchanged so an unknown
// scene stays ErrNotFound and a closing database stays ErrClosed.
func ResolveForRead(engine *core.StorageEngine, agentID uint64, q core.SearchQuery) (uint64, error) {
	if q.SceneID == "" {
		return create(engine, agentID, q.L3ID)
	}
	id, err := common.ParseID(q.SceneID)
	if err != nil {
		return 0, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	slot, err := core.ReadSceneSlot(engine, agentID, id)
	if err != nil {
		return 0, err
	}
	// The anchor is a creation-time field: a scene that already exists keeps the
	// anchor it has until UpdateScene moves it. That refusal needs no lookup —
	// reaching for the named graph first would report an unresolvable anchor as a
	// not-found about a record the host never asked to read, and would pay a
	// shared-pool read inside the caller's domain lock to say nothing new.
	if q.L3ID != "" {
		return 0, common.NewError(common.ErrInvalidQuery,
			"scene "+q.SceneID+" already exists; its L3 anchor is set at creation only (use UpdateScene)")
	}
	return slot.SceneID, nil
}

// create allocates a free scene id and persists the scene under a
// library-generated name, anchored on the named L3 domain when one is given. The
// domain is resolved before anything is written: a refusal has to leave no scene
// behind. Nothing is read back afterwards — freshID proved the id free under the
// caller's domain lock — and no step remains where the scene is stored while its id
// cannot be handed to the caller.
func create(engine *core.StorageEngine, agentID uint64, l3ID string) (uint64, error) {
	var anchor uint64
	if l3ID != "" {
		g, err := repo.ReadSharedGraphL3(engine, l3ID)
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
			// "Nothing here" is also what the typed reader answers when another kind
			// of record holds the address — unlike the L3 ids, which a host's own text
			// derives and which can therefore spell out a neighbour's address, this one
			// is random, so that case is not reachable and needs no second probe.
			if common.CodeOf(err) != common.ErrNotFound {
				return 0, err
			}
			return id, nil
		}
	}
}
