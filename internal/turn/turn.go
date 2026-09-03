// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package turn holds the small methods that settle one finished turn into
// the topic Search opened for it: payload validation, the superseded-ref
// read, the archive writes and the tombstone diff. The Update big method in
// the composition root locks the domain and composes them around the single
// keyword-distillation call.

package turn

import (
	"github.com/qyiun666/MemHop/internal/common"
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

// PriorL4Refs returns the L4 refs stored on a turn topic; a topic that does
// not exist yet (the first settle of a turn) yields nil.
func PriorL4Refs(engine *core.StorageEngine, agentID, topicID uint64) ([]uint64, error) {
	topic, err := core.ReadTopicLenient(engine, agentID, topicID)
	if err != nil {
		if common.CodeOf(err) == common.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	if topic == nil {
		return nil, nil
	}
	return topic.L4Refs, nil
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

// WriteArchives appends the turn's two originals as L4 archives, each
// under the content type the host declared. The returned ids go to the
// topic's L4Refs, which persist id-sorted — conversation order comes from
// the archives' timestamps, not from this slice.
func WriteArchives(engine *core.StorageEngine, agentID, topicID uint64, in core.TurnUpdate) ([]uint64, error) {
	userRef, err := repo.AppendArchiveL4(engine, agentID, topicID, core.RoleUser, in.UserType, in.UserText, in.UserTS)
	if err != nil {
		return nil, err
	}
	agentRef, err := repo.AppendArchiveL4(engine, agentID, topicID, core.RoleAgent, in.AgentType, in.AgentText, in.AgentTS)
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
