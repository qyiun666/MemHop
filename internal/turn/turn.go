// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package turn holds the small methods that settle one finished turn into
// the topic Search opened for it: payload validation, the read of what that
// turn already owns, the archive writes and the tombstone diff. The Update
// big method in the composition root locks the domain and composes them
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

// PriorArchives returns the archives this topic already owns — what a settle
// of the same turn supersedes. The domain's archive index is the list, and a
// topic nobody has settled yet owns nothing rather than erroring.
func PriorArchives(ac *domain.Context, topicID uint64) []uint64 {
	return ac.Arch.Hashes(topicID)
}

// DropRetained yields the ids of before that no longer appear in after.
func DropRetained(before, after []uint64) []uint64 {
	keep := common.ToSet(after)
	var out []uint64
	for _, id := range before {
		if _, ok := keep[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}

// WriteArchives appends the turn's originals as L4 archives under the topic
// that owns them, each under the content type the host declared. The returned
// ids are only what this settle wrote: the caller diffs them against what the
// turn owned before, and tombstones what fell out.
func WriteArchives(ac *domain.Context, agentID, topicID uint64, in core.TurnUpdate) ([]uint64, error) {
	userRef, err := repo.AppendArchiveL4(ac.Engine, agentID, ac.Arch, repo.ArchiveContent{
		TopicID: topicID, Role: core.RoleUser, Type: in.UserType, Text: in.UserText, CreatedAt: in.UserTS})
	if err != nil {
		return nil, err
	}
	agentRef, err := repo.AppendArchiveL4(ac.Engine, agentID, ac.Arch, repo.ArchiveContent{
		TopicID: topicID, Role: core.RoleAgent, Type: in.AgentType, Text: in.AgentText, CreatedAt: in.AgentTS})
	if err != nil {
		return nil, err
	}
	return []uint64{userRef, agentRef}, nil
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
