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
// surfaced. The classification is turn.ReadProfile's — one rule, one place.
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
// Preferences). Name is required here as it is at the two creation entries: a
// domain whose profile lost its name cannot be named back by anything that reads
// the profile. The three fields the library owns are inherited from the stored
// record: EmotionState and MBTI, which Dream evolves, and AgentType, stamped
// when the domain was created — so a host editing its profile never wipes the
// distilled half and never moves its domain between primary and sub.
// Personality is the exception in both directions: Dream evolves it too, and this
// write does not inherit it, so a host that leaves it empty clears whatever the
// last pass distilled and the next one evolves it again.
// UpdatedAtMs is stamped here rather than taken from the caller. ID
// is forced to hash("profile"); the domain lock comes from the agent context.
func (db *DB) UpdateL0(agentID uint64, slot *core.ProfileSlot) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	if slot == nil {
		return common.NewError(common.ErrInvalidQuery, "UpdateL0: slot is required")
	}
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
