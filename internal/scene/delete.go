// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package scene

import (
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// DeleteCascade removes the given L2 records — scene slots and/or topics — with
// the L4 content and L5 plan trees they own and their cache entries. The id sets
// come from a strict enumeration the caller has already run, so the only
// whole-bucket scan left here is the plan-node one, and it runs before the first
// tombstone: a refusal therefore leaves the disk exactly as it was. After that
// point the only failures left are the deletes themselves, and the mirrors go
// last, so a failed pass never leaves an index entry naming a deleted record.
// Callers hold ac.Mu.
func DeleteCascade(ac *domain.Context, agentID uint64, scenes, topics []uint64) error {
	planNodes, err := repo.PlanNodeIDsByTopicIDs(ac.Engine, agentID, topics)
	if err != nil {
		return err
	}
	records := make([]uint64, 0, len(scenes)+len(topics))
	records = append(records, topics...)
	records = append(records, scenes...)
	if err := repo.DeleteL2Records(ac.Engine, agentID, records); err != nil {
		return err
	}
	if _, err := repo.DeletePlanNodesByIDs(ac.Engine, agentID, planNodes); err != nil {
		return err
	}
	if err := repo.DeleteTopicArchives(ac.Engine, agentID, ac.L4, topics); err != nil {
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
