// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package turn holds the small methods that settle one finished turn into the
// topic Search opened for it: resolving the two ids the settle names, and the
// gate on which topic may be settled. The Update big method in the composition
// root locks the domain and composes them around the one keyword-distillation
// call that gives the turn its keyword track.

package turn

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Targets resolves the scene a turn settles into plus the topic id Search minted
// for it. Both are ids the library issued and the host hands back, so nothing here
// interprets them: an unparsable id or the reserved zero topic is refused before
// any record is read.
func Targets(sceneID, topicID string) (uint64, uint64, error) {
	parsedScene, err := common.ParseID(sceneID)
	if err != nil {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	parsedTopic, err := common.ParseID(topicID)
	if err != nil {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "parse topic id", err)
	}
	if parsedTopic == 0 {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "Update requires the topic id Search issued for this turn")
	}
	return parsedScene, parsedTopic, nil
}

// SettleTarget validates the topic a turn may settle into: the id has to be one
// this scene opened — the turn topic minted for a turn count the scene has
// already reached. That single rule covers every case where settling would do
// damage: a Dream-fused group's parent (also depth 1, in this scene, but derived
// from timestamps, not from the turn counter), a child the group sunk, another
// scene's turn, and an id nobody issued. Replaying the current turn and settling
// an opened-but-earlier turn both stay valid.
func SettleTarget(sceneID, topicID, turnSeq uint64) error {
	for seq := uint64(1); seq <= turnSeq; seq++ {
		if core.ComputeTurnTopicID(sceneID, seq) == topicID {
			return nil
		}
	}
	return common.NewError(common.ErrInvalidQuery,
		"Update: topic_id is not a turn this scene opened; settle the id Search returned")
}

// ReadProfile loads the domain's L0 profile. A profile that was never
// written reads as empty (the same surface GetL0 gives); any other failure
// aborts the read, so Search never hands back a context silently missing
// its profile.
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
