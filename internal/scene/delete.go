// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package scene

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// PruneParentChild removes the deleted topic from its surviving parent's
// ChildrenIDs and refreshes the parent record and L2Meta entry, so no
// dangling child reference survives the deletion. Callers hold ac.Mu.
func PruneParentChild(ac *domain.Context, topicID uint64) error {
	root, err := core.ReadTopicSlot(ac.Engine, ac.ID, topicID)
	if err != nil || root == nil || root.ParentID == nil {
		return err
	}
	parentID := *root.ParentID
	parent, err := core.ReadTopicSlot(ac.Engine, ac.ID, parentID)
	if err != nil || parent == nil {
		return err
	}
	parent.ChildrenIDs = common.RemoveOnce(parent.ChildrenIDs, topicID)
	if err := core.WriteTopicSlot(ac.Engine, ac.ID, parentID, parent); err != nil {
		return err
	}
	ac.SyncL2Meta(parentID)
	return nil
}

// DeleteTopics removes the given topics (with their L2Meta cache entries)
// and the given archives in one engine pass. Callers hold ac.Mu.
func DeleteTopics(ac *domain.Context, agentID uint64, topics, archives []uint64) error {
	if !repo.DeleteL2(ac.Engine, agentID, topics, repo.DeleteTopicsL2) {
		return common.NewError(common.ErrIO, "delete topics", nil)
	}
	if err := repo.DeleteArchivesL4(ac.Engine, agentID, archives); err != nil {
		return err
	}
	ac.RemoveTopicsFromIndices(topics)
	return nil
}
