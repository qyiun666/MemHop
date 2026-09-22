// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 profile operations of the internal layer: the read shares the turn package's
// profile read, the write is a thin wrapper over the repo layer.

package internal

import (
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/turn"
)

// GetL0 reads the profile singleton of one agent. An absent profile is
// returned as an empty, non-nil ProfileSlot; storage/corruption errors are
// surfaced.
func (db *DB) GetL0(agentID uint64) (*core.ProfileSlot, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	slot, err := turn.ReadProfile(db.engine, agentID)
	if err != nil {
		return nil, err
	}
	return &slot, nil
}

// UpdateL0 writes the host-owned half of the profile (Name/Role/Personality/
// Preferences). Name is required here as at the two creation entries: a domain
// with a nameless profile cannot be named back by anything that reads it.
// EmotionState, MBTI and AgentType are inherited from the stored record — a
// host edit never wipes what Dream evolved and never moves the domain between
// primary and sub. Personality is the exception in both directions: Dream
// evolves it too and this write does not inherit it, so leaving it empty
// clears the last distilled summary until the next pass evolves it again.
// UpdatedAtMs is stamped here; the stored id stays hash("profile").
// slot must be non-nil — nil-ness is refused at the facade, not re-checked here.
func (db *DB) UpdateL0(agentID uint64, slot *core.ProfileSlot) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	if strings.TrimSpace(slot.Name) == "" {
		return common.NewError(common.ErrInvalidQuery, "UpdateL0: profile Name is required")
	}
	cur, err := repo.GetProfileL0(db.engine, agentID)
	if err != nil {
		if common.CodeOf(err) != common.ErrNotFound {
			return err
		}
	} else {
		slot.EmotionState = cur.EmotionState
		slot.MBTI = cur.MBTI
		slot.AgentType = cur.AgentType
	}
	slot.UpdatedAtMs = time.Now().UnixMilli()
	return repo.UpdateProfileL0(db.engine, agentID, slot)
}
