// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package turn holds the small methods over one finished turn: the gate on which
// topic a turn may settle into, and the L0 profile read the read path shares. The
// ids are already resolved by the time this package is reached.

package turn

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// SettleTarget validates the topic a turn may settle into: the id has to be one
// this scene opened — the turn topic minted for a turn count the scene has
// already reached. That single rule refuses everything settling must not touch: a
// Dream-fused group's parent (also depth 1 and in this scene, but derived from
// timestamps rather than from the turn counter), another scene's turn, and an id
// nobody issued.
//
// Two replays stay valid on purpose: settling the current turn again, and settling
// a turn Dream has since sunk under a fused parent — a sunk turn is still one this
// scene opened. Keeping it sunk is the settle write's job, not this gate's: the gate
// judges the key, and a key it refused here would make a replay of an already
// consolidated turn an error instead of a rewrite.
func SettleTarget(sceneID, topicID, turnSeq uint64) error {
	for seq := uint64(1); seq <= turnSeq; seq++ {
		if core.ComputeTurnTopicID(sceneID, seq) == topicID {
			return nil
		}
	}
	return common.NewError(common.ErrInvalidQuery,
		"Update: topic_id is not a turn this scene opened; settle the id Search returned")
}

// ReadProfile loads the domain's L0 profile. A profile that was never written
// reads as the zero value; any other failure aborts the read, so a caller never
// gets a context silently missing its profile.
func ReadProfile(engine *core.StorageEngine, agentID uint64) (core.ProfileSlot, error) {
	slot, err := repo.GetProfileL0(engine, agentID)
	if err != nil {
		if common.CodeOf(err) == common.ErrNotFound {
			return core.ProfileSlot{}, nil
		}
		return core.ProfileSlot{}, err
	}
	return *slot, nil
}
