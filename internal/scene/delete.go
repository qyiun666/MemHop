// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package scene

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// DeleteTopics removes the given topics together with the L4 content they own,
// the plan trees they opened and their cache entries, in one engine pass.
// Records go first and the mirrors are dropped only after the disk agrees, so a
// failed pass never leaves an index entry naming a deleted record. Callers hold
// ac.Mu.
func DeleteTopics(ac *domain.Context, agentID uint64, topics []uint64) error {
	if !repo.DeleteL2(ac.Engine, agentID, topics, repo.DeleteTopicsL2) {
		return common.NewError(common.ErrIO, "delete topics", nil)
	}
	if err := repo.DeleteTopicArchives(ac.Engine, agentID, ac.L4, topics); err != nil {
		return err
	}
	if _, err := repo.DeletePlanNodesByTopicIDs(ac.Engine, agentID, topics); err != nil {
		return err
	}
	ac.RemoveTopicsFromIndices(topics)
	return nil
}

// DetachGraph clears the L3 anchor of every scene that named graphID, scanning
// the domain because anchors live only on scenes — a graph slot keeps no reverse
// list. Callers hold the domain lock.
func DetachGraph(engine *core.StorageEngine, agentID uint64, graphID uint64) error {
	var targets []core.SceneSlot
	for s := range core.IterAll[core.SceneSlot](engine, agentID, core.RecL2Scene) {
		if s.L3ID == graphID {
			targets = append(targets, s)
		}
	}
	for _, slot := range targets {
		slot.L3ID = 0
		if err := core.WriteSceneSlot(engine, agentID, slot.SceneID, &slot); err != nil {
			return err
		}
	}
	return nil
}
