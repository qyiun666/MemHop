// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 node sync: rebuilds one scene node per scene from the current
// depth<=2 L2 topics. Runs only during Dream, before edge building.
package repo

import (
	"slices"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// DeleteSceneNodeL1 removes one scene's L1 node record by scene ID (the
// node ID is derivable without an index); missing nodes are a no-op.
// Incident hyperedges are cleaned by the next Dream's rebuild.
func DeleteSceneNodeL1(engine *core.StorageEngine, agentID uint64, sceneID uint64) error {
	if _, err := engine.DeleteRecordBatch(agentID, []uint64{core.SceneNodeID(sceneID)}); err != nil {
		return common.NewError(common.ErrIO, "delete l1 scene node", err)
	}
	return nil
}

// SyncL1NodesFromL2 rebuilds one L1 node per scene from the current
// depth<=2 topics; L1 is written/updated only during Dream. The node ID
// (hash("l1:"+sceneID)) is stable across dreams: existing nodes keep
// Importance/Valence/Arousal (decay belongs to DecayL1Network) and are
// refreshed only when the topic set changed, so UpdatedAt keeps
// accumulating decay. Returns the number of nodes created or updated.
func SyncL1NodesFromL2(engine *core.StorageEngine, agentID uint64) (int, error) {
	byScene := collectTopicIDsByScene(engine, agentID)
	now := time.Now().UnixMilli()
	changed := 0
	for sceneID, set := range byScene {
		ids := sortedIDs(set)
		count, err := syncOneSceneNode(engine, agentID, sceneID, ids, now)
		changed += count
		if err != nil {
			return changed, err
		}
	}
	return changed, nil
}

// collectTopicIDsByScene groups live topic idHashes per scene, keeping
// only depth<=2 topics (deeper ones are managed by compression).
func collectTopicIDsByScene(engine *core.StorageEngine, agentID uint64) map[uint64]map[uint64]struct{} {
	byScene := make(map[uint64]map[uint64]struct{})
	for idHash := range engine.IndexByType(agentID, core.RecL2Topic) {
		topic, err := core.ReadTopicLenient(engine, agentID, idHash)
		if err != nil || topic == nil || topic.Depth > 2 {
			continue
		}
		set := byScene[topic.SceneID]
		if set == nil {
			set = make(map[uint64]struct{})
			byScene[topic.SceneID] = set
		}
		set[idHash] = struct{}{}
	}
	return byScene
}

// syncOneSceneNode refreshes one scene's node when its topic set changed;
// unchanged nodes keep UpdatedAt so decay accumulates. Returns 1 when the
// node was created or updated.
func syncOneSceneNode(engine *core.StorageEngine, agentID uint64, sceneID uint64, ids []uint64, now int64) (int, error) {
	nodeID := core.SceneNodeID(sceneID)
	node, err := core.ReadSceneNode(engine, agentID, nodeID)
	if err != nil {
		node = nil
	}
	if node != nil && slices.Equal(node.TopicIDs, ids) {
		return 0, nil
	}
	if node == nil {
		node = &core.SceneNode{IDHash: nodeID, SceneID: sceneID, CreatedAt: now, Importance: 1.0}
	}
	node.TopicIDs = ids
	node.UpdatedAt = now
	if err := core.WriteSceneNode(engine, agentID, nodeID, node); err != nil {
		return 1, common.NewError(common.ErrIO, "write l1 scene node", err)
	}
	return 1, nil
}

// sortedIDs renders an idHash set as a sorted slice so node TopicIDs are
// deterministic and comparable via slices.Equal.
func sortedIDs(set map[uint64]struct{}) []uint64 {
	ids := make([]uint64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
