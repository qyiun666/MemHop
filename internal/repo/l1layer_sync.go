// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 node writes: rebuild one scene node per scene from the current depth<=2 L2
// topics, and backfill the emotion signals a distillation computed for nodes the
// sync pass created.
package repo

import (
	"fmt"
	"slices"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// DeleteSceneNodeL1 removes one scene's L1 node record by scene ID (the
// node ID is derivable without an index); missing nodes are a no-op. It drops
// the node record only — incident hyperedges are another pass's to clean.
func DeleteSceneNodeL1(engine *core.StorageEngine, agentID uint64, sceneID uint64) error {
	if _, err := engine.DeleteRecordBatch(agentID, []uint64{core.SceneNodeID(sceneID)}); err != nil {
		return common.NewError(common.ErrIO, "delete l1 scene node", err)
	}
	return nil
}

// SyncL1NodesFromL2 rebuilds one L1 node per scene from the current
// depth<=2 topics. The node ID (hash("scene-node:"+sceneID)) is stable across
// runs: existing nodes keep Importance/Valence/Arousal — this pass never decays
// them — and are refreshed only when the topic set changed, so UpdatedAt keeps
// accumulating decay. Returns the number of nodes created or updated. A topic or
// node that will not read back stops the pass with that cause, since both would
// otherwise be written as a record that lost fields.
func SyncL1NodesFromL2(engine *core.StorageEngine, agentID uint64) (int, error) {
	byScene, err := collectTopicIDsByScene(engine, agentID)
	if err != nil {
		return 0, err
	}
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
func collectTopicIDsByScene(engine *core.StorageEngine, agentID uint64) (map[uint64]map[uint64]struct{}, error) {
	topics, err := core.CollectAllTopicsStrict(engine, agentID)
	if err != nil {
		return nil, err
	}
	byScene := make(map[uint64]map[uint64]struct{})
	for _, topic := range topics {
		if topic.Depth > 2 {
			continue
		}
		set := byScene[topic.SceneID]
		if set == nil {
			set = make(map[uint64]struct{})
			byScene[topic.SceneID] = set
		}
		set[topic.ID] = struct{}{}
	}
	return byScene, nil
}

// syncOneSceneNode refreshes one scene's node when its topic set changed;
// unchanged nodes keep UpdatedAt so decay accumulates. Returns 1 when the
// node was created or updated.
func syncOneSceneNode(engine *core.StorageEngine, agentID uint64, sceneID uint64, ids []uint64, now int64) (int, error) {
	nodeID := core.SceneNodeID(sceneID)
	node, err := core.ReadSceneNode(engine, agentID, nodeID)
	if err != nil {
		if common.CodeOf(err) != common.ErrNotFound {
			// A node that is there but will not read back is not a node that is
			// missing: a fresh one would overwrite it with no emotion, no
			// importance history and no EdgeIDs, and the hyperedges naming it
			// would be left pointing at a node that denies them.
			return 0, err
		}
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

// BackfillL1Emotions stamps the emotion signals a distillation computed onto the
// nodes that carry none yet and leaves a node that already has them alone, so a
// later pass never overwrites what an earlier one settled. It returns how many
// nodes it wrote. A node it cannot read aborts the pass with that cause: an absent
// node and an unreadable one both stop the backfill, but they are not the same
// fact to report.
func BackfillL1Emotions(engine *core.StorageEngine, agentID uint64, perNode map[uint64]core.NodeEmotion) (int, error) {
	written := 0
	for id, em := range perNode {
		node, err := core.ReadSceneNode(engine, agentID, id)
		if err != nil {
			return written, fmt.Errorf("backfill L1 emotions: node %s: %w", common.FormatHash(id), err)
		}
		if node.Valence != 0 || node.Arousal != 0 {
			continue
		}
		node.Valence = em.Valence
		node.Arousal = em.Arousal
		node.UpdatedAt = time.Now().UnixMilli()
		if err := core.WriteSceneNode(engine, agentID, id, node); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}
