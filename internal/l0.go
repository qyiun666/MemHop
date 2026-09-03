// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 profile operations of the internal layer: thin wrappers over the repo layer.

package internal

import (
	"context"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/dream"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
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
	slot, err := repo.GetProfileL0(db.engine, agentID)
	if err != nil {
		if common.CodeOf(err) == common.ErrNotFound {
			return &core.ProfileSlot{}, nil
		}
		return nil, err
	}
	return slot, nil
}

// UpdateL0 writes the host-owned half of the profile (Name/Role/Personality/
// Preferences). The two fields Dream evolves — EmotionState and MBTI — are
// inherited from the stored record, so a host editing its profile never wipes
// them, and UpdatedAtMs is stamped here rather than taken from the caller. ID
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
	cur, err := repo.GetProfileL0(db.engine, agentID)
	if err != nil {
		if common.CodeOf(err) != common.ErrNotFound {
			return err
		}
	} else {
		slot.EmotionState = cur.EmotionState
		slot.MBTI = cur.MBTI
	}
	slot.UpdatedAtMs = time.Now().UnixMilli()
	return repo.UpdateProfileL0(db.engine, agentID, slot)
}

// DistillL0 runs only Dream's L0 distillation stage (LLM emotion/MBTI
// refresh backfilled into L1), leaving L1/L2 untouched; a cheap refresh
// entry after long conversations or profile edits. Skipped silently when
// the domain has no profile samples.
func (db *DB) DistillL0(ctx context.Context, agentID uint64) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	_, err = dream.DistillL0Stage(ctx, ac, agentID)
	return err
}
