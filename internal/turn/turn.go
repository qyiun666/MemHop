// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package turn holds the small methods that settle one finished turn into
// the topic Search opened for it: payload validation, the gate on which topic
// may be settled, and the two content slots the turn's originals occupy. The
// Update big method in the composition root locks the domain and composes them
// around the single keyword-distillation call.

package turn

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Targets validates a turn's payload and resolves the scene it settles
// into plus the topic id Search minted for it.
func Targets(in core.TurnUpdate) (uint64, uint64, error) {
	if in.UserText == "" || in.AgentText == "" {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "Update requires both the user and the agent text")
	}
	if in.UserTS <= 0 || in.AgentTS <= 0 {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "Update requires positive timestamps for both messages")
	}
	if in.AgentTS < in.UserTS {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "Update requires the agent timestamp not earlier than the user timestamp")
	}
	if !in.UserType.Valid() || !in.AgentType.Valid() {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "Update requires a defined content type on both sides")
	}
	sceneID, err := common.ParseID(in.SceneID)
	if err != nil {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	topicID, err := common.ParseID(in.TopicID)
	if err != nil {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "parse topic id", err)
	}
	if topicID == 0 {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "Update requires the topic id Search issued for this turn")
	}
	return sceneID, topicID, nil
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

// WriteArchives settles a turn's originals into the two content slots their topic
// reserves for them — Seq 1 for what the user said, Seq 2 for the reply — each
// under the content type the host declared. The slots are addressed by position,
// so re-settling a turn rewrites the same two records: nothing has to be listed
// as owned before it can be replaced.
//
// That is also the limit of what a settle reclaims. Rewriting a turn with fewer
// or different texts never deletes what an earlier settle left in a slot this one
// does not fill, so a revised turn can keep surfacing a withdrawn reply until the
// topic is deleted or the retention window passes. The library does not track
// "the set this turn wrote" a second time to undo it.
func WriteArchives(ac *domain.Context, agentID, topicID uint64, in core.TurnUpdate) error {
	if _, err := repo.AppendArchiveL4(ac.Engine, agentID, ac.L4, repo.ArchiveContent{
		TopicID: topicID, Seq: core.SeqUser, Kind: core.KindUtterance,
		Role: core.RoleUser, Type: in.UserType, Text: in.UserText, CreatedAt: in.UserTS,
	}); err != nil {
		return err
	}
	_, err := repo.AppendArchiveL4(ac.Engine, agentID, ac.L4, repo.ArchiveContent{
		TopicID: topicID, Seq: core.SeqAgent, Kind: core.KindUtterance,
		Role: core.RoleAgent, Type: in.AgentType, Text: in.AgentText, CreatedAt: in.AgentTS,
	})
	return err
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
