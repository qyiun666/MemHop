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

// DeleteSceneNodeL1 removes one scene's L1 node record by scene ID; missing
// nodes are a no-op. It drops the node record only — incident hyperedges are
// another pass's to clean.
func DeleteSceneNodeL1(engine *core.StorageEngine, agentID uint64, sceneID uint64) error {
	if _, err := engine.DeleteRecordBatch(agentID, []uint64{core.SceneNodeID(sceneID)}); err != nil {
		return common.NewError(common.ErrIO, "delete l1 scene node", err)
	}
	return nil
}

// SyncL1NodesFromL2 rebuilds one L1 node per scene from the current
// depth<=2 topics and returns the ids of the nodes it wrote. The node id is
// stable across runs: existing nodes keep Importance/Valence/Arousal — this
// pass never decays them — while a scene whose topic set changed has its
// UpdatedAt moved to now, restarting the clock its decay runs on. The returned
// set names the scenes whose topic list came out different (a turn lost counts
// as much as one gained): the only new evidence the co-occurrence pass may
// strengthen an existing edge from. A topic or node that will not read back
// stops the pass with that cause, since both would otherwise be written as a
// record that lost fields.
func SyncL1NodesFromL2(engine *core.StorageEngine, agentID uint64) (map[uint64]struct{}, error) {
	byScene, err := collectTopicIDsByScene(engine, agentID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	touched := make(map[uint64]struct{})
	for sceneID, set := range byScene {
		nodeID, changed, err := syncOneSceneNode(engine, agentID, sceneID, sortedIDs(set), now)
		if err != nil {
			return touched, err
		}
		if changed {
			touched[nodeID] = struct{}{}
		}
	}
	return touched, nil
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
// unchanged nodes keep UpdatedAt so decay accumulates. It returns the node's id
// and whether this pass wrote it.
func syncOneSceneNode(engine *core.StorageEngine, agentID uint64, sceneID uint64, ids []uint64, now int64) (uint64, bool, error) {
	nodeID := core.SceneNodeID(sceneID)
	node, err := core.ReadSceneNode(engine, agentID, nodeID)
	if err != nil {
		if common.CodeOf(err) != common.ErrNotFound {
			// A node that is there but will not read back is not a missing node:
			// a fresh one would overwrite it with no history, and the hyperedges
			// naming it would point at a node that denies them.
			return nodeID, false, err
		}
		node = nil
	}
	if node != nil && slices.Equal(node.TopicIDs, ids) {
		return nodeID, false, nil
	}
	if node == nil {
		node = &core.SceneNode{IDHash: nodeID, SceneID: sceneID, CreatedAt: now, Importance: 1.0}
	}
	node.TopicIDs = ids
	node.UpdatedAt = now
	if err := core.WriteSceneNode(engine, agentID, nodeID, node); err != nil {
		return nodeID, false, common.NewError(common.ErrIO, "write l1 scene node", err)
	}
	return nodeID, true, nil
}

// sortedIDs renders an idHash set as a sorted slice: node TopicIDs must be
// deterministic to be comparable.
func sortedIDs(set map[uint64]struct{}) []uint64 {
	ids := make([]uint64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// BackfillL1Emotions stamps the emotion signals a distillation computed onto
// the nodes no pass has stamped yet and leaves a stamped node alone. The test
// is the node's EmotionSet marker, not its values: both ends of the scale are
// legal readings, so one distilled to (0,0) is indistinguishable from an
// unstamped one by value alone, and re-stamping would replace a settled reading
// and restart the clock it decays on. An unreadable node aborts the pass with
// that cause.
func BackfillL1Emotions(engine *core.StorageEngine, agentID uint64, perNode map[uint64]core.NodeEmotion) error {
	for id, em := range perNode {
		node, err := core.ReadSceneNode(engine, agentID, id)
		if err != nil {
			return fmt.Errorf("backfill L1 emotions: node %s: %w", common.FormatHash(id), err)
		}
		if node.EmotionSet {
			continue
		}
		moved := node.Valence != em.Valence || node.Arousal != em.Arousal
		node.Valence, node.Arousal, node.EmotionSet = em.Valence, em.Arousal, true
		if moved {
			node.UpdatedAt = time.Now().UnixMilli()
		}
		if err := core.WriteSceneNode(engine, agentID, id, node); err != nil {
			return fmt.Errorf("backfill L1 emotions: node %s: %w", common.FormatHash(id), err)
		}
	}
	return nil
}
