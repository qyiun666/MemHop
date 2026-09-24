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
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/scene"
	"github.com/qyiun666/MemHop/internal/turn"
)

// Search reads the domain's conversation and opens the turn the host is about to
// run: it returns the scene record, its depth-1 topics in turn order (the host's
// context), the domain's L0 profile and the topic id this read minted for the new
// turn — Update closes that turn, and everything the turn records (its L4 content,
// its L5 plan tree) keys on it. Which scene and which turn are the domain's to
// remember, so a host running one agent over one library carries no id across
// calls. Naming a SceneID scopes this read to that scene instead, and an L3ID
// handed in alongside one is refused rather than dropped, since that anchor is a
// creation-time field (UpdateScene moves it).
func (db *DB) Search(agentID uint64, q SearchQuery) (*SearchResult, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()

	sceneID, err := db.resolveScene(ac, agentID, q)
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
	opened := core.ComputeTurnTopicID(sceneSlot.SceneID, sceneSlot.TurnSeq)
	if opened == 0 {
		// Zero is what the domain uses to say "no turn is open", so a turn key that
		// hashes to it could never be told apart from a read that never happened.
		// The scene's counter has already advanced, so this reports the collision
		// instead of silently dropping the turn the host is about to run.
		return nil, common.NewError(common.ErrCorruption,
			"the turn key this scene allocated is the reserved zero value")
	}
	ac.Scene, ac.Turn = sceneSlot.SceneID, opened
	return &SearchResult{
		Profile:      slot,
		ProfileBrief: profile.Brief(slot),
		Scene:        *sceneSlot,
		Topics:       topics,
		NewTopicID:   opened,
	}, nil
}

// resolveScene reads the host's query into the scene this read is scoped to. An
// empty SceneID continues the domain's current one, which restores from the records
// on the first read after an open or a sweep; a domain with no scene yet gets its
// first one. NewScene asks for a fresh conversation instead. An anchor is a
// creation-time field, so it is parsed on the creating paths and refused on the one
// that continues a scene — dropping it there would let a project domain go unadopted
// while the read looked like it had taken one. A named scene is resolved by
// scene.ResolveExisting, whose refusal reports the scene the host actually pointed
// at. Continuing never re-checks existence: OpenSceneTurn reads the record next.
func (db *DB) resolveScene(ac *domain.Context, agentID uint64, q SearchQuery) (uint64, error) {
	if q.SceneID != "" {
		if q.NewScene {
			// The two flags ask for opposite things — one names a conversation to go on,
			// the other asks for a different one — and answering by quietly dropping the
			// second would leave a host starting a new session while reading the old scene.
			return 0, common.NewError(common.ErrInvalidQuery,
				"scene_id names a conversation to continue and new_scene asks for a fresh one: pass one or the other")
		}
		named, err := parseID("scene", q.SceneID)
		if err != nil {
			return 0, err
		}
		return scene.ResolveExisting(db.engine, agentID, named, q.L3ID != "")
	}
	anchor, err := sceneAnchor(q.L3ID)
	if err != nil {
		return 0, err
	}
	if q.NewScene {
		return scene.Create(db.engine, agentID, anchor)
	}
	if err := db.ensureScene(ac, agentID); err != nil {
		return 0, err
	}
	if ac.Scene == 0 {
		return scene.Create(db.engine, agentID, anchor)
	}
	if anchor != 0 {
		return 0, common.NewError(common.ErrInvalidQuery,
			"an L3 anchor is set when a scene is created: pass NewScene to hang a new conversation on a project, or UpdateScene to move the current one")
	}
	return ac.Scene, nil
}

// ensureScene fills the domain's memory of which scene it is working from the records,
// when nothing holds it: the first read after an open, an idle sweep, a delete or a
// merge. It stays 0 when the domain holds no scene at all, which each caller answers in
// its own way — the read that opens a turn creates the first one, the pure read has
// nothing to show and says so.
func (db *DB) ensureScene(ac *domain.Context, agentID uint64) error {
	if ac.Scene != 0 {
		return nil
	}
	id, err := scene.CurrentScene(db.engine, agentID)
	ac.Scene = id
	return err
}

// readScene resolves the scene a write-free read is scoped to: the id the host named, or
// the domain's own current one when it names none. That is the whole of the host's side of
// a pure read — no id held, no turn opened, nothing written.
//
// hasScene is false for one situation only: this domain has never had a conversation. That
// is an answer the read can give without writing anything (minting a scene stays what
// opening a turn does), so it is not folded into ErrNotFound, which a caller reads as "the
// scene you named is not here". A named id passes through untouched, including a zero the
// library never issued, so a named miss keeps being a miss.
func (db *DB) readScene(ac *domain.Context, agentID uint64, sceneID string) (uint64, bool, error) {
	if sceneID != "" {
		id, err := parseID("scene", sceneID)
		return id, true, err
	}
	if err := db.ensureScene(ac, agentID); err != nil {
		return 0, false, err
	}
	if ac.Scene == 0 {
		return 0, false, nil
	}
	return ac.Scene, true, nil
}

// sceneAnchor parses the project domain a created scene hangs on; an empty string
// is no anchor, and a named one that does not parse is refused before any scene is
// allocated.
func sceneAnchor(l3ID string) (uint64, error) {
	if l3ID == "" {
		return 0, nil
	}
	return parseID("l3", l3ID)
}
