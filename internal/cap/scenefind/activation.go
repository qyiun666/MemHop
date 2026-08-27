// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 spreading activation: breadth-first walk over the scene hypergraph
// used by Search to surface associated scenes (pure storage-level graph
// read — no in-memory graph index is maintained).

package scenefind

import (
	"cmp"
	"slices"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// Activation walk tuning constants (previously internal/tuning.go).
const (
	l1EdgeMaxHops         int     = 2
	l1ActivationDampening float32 = 0.5
	l1ActivationThreshold float32 = 0.05
	l1AssocMaxScenes      int     = 3
)

// SpreadingActivation walks the L1 scene hypergraph from startSceneID and
// returns the most strongly activated other scenes with their depth<=1
// topics, ordered by activation (desc). Activation starts at 1.0 at the
// source node and propagates along hyperedges as act × edge.Weight ×
// dampening per hop; paths below the activation threshold stop spreading and
// the walk never exceeds max hops. The start scene itself is never returned.
// A scene without an L1 node (created after the last Dream) yields an empty
// result. All walk limits are package-private tuning constants.
func SpreadingActivation(agentID uint64, engine *core.StorageEngine, l2Meta *index.L2MetaIndex,
	startSceneID uint64) []SceneHit {
	maxHops, dampening, threshold, maxScenes := l1EdgeMaxHops,
		l1ActivationDampening, l1ActivationThreshold, l1AssocMaxScenes
	if maxHops <= 0 || maxScenes <= 0 || dampening <= 0 {
		return nil
	}
	startNodeID := core.SceneNodeID(startSceneID)
	if _, err := core.ReadSceneNode(engine, agentID, startNodeID); err != nil {
		return nil // never dreamed; nothing associated yet
	}
	sceneAct := spreadActivations(agentID, engine, startNodeID, startSceneID, maxHops, dampening, threshold)
	if len(sceneAct) == 0 {
		return nil
	}
	return collectSceneHits(agentID, engine, l2Meta, rankActivatedScenes(sceneAct, maxScenes), sceneAct)
}

// spreadActivations runs the BFS walk and returns the best activation per
// reached scene (the start scene excluded).
func spreadActivations(agentID uint64, engine *core.StorageEngine, startNodeID, startSceneID uint64,
	maxHops int, dampening, threshold float32) map[uint64]float32 {
	type entry struct {
		nodeID uint64
		act    float32
		hops   int
	}
	queue := []entry{{nodeID: startNodeID, act: 1.0}}
	sceneAct := make(map[uint64]float32)
	for len(queue) > 0 {
		e := queue[0]
		queue = queue[1:]
		if e.hops >= maxHops {
			continue
		}
		node, err := core.ReadSceneNode(engine, agentID, e.nodeID)
		if err != nil {
			continue
		}
		for _, edgeID := range node.EdgeIDs {
			edge, err := core.ReadSceneEdge(engine, agentID, edgeID)
			if err != nil {
				continue
			}
			for _, neighborID := range edge.NodeIDs {
				if neighborID == e.nodeID {
					continue
				}
				act := e.act * edge.Weight * dampening
				if act < threshold {
					continue
				}
				neighbor, err := core.ReadSceneNode(engine, agentID, neighborID)
				if err != nil {
					continue
				}
				if neighbor.SceneID != startSceneID && act > sceneAct[neighbor.SceneID] {
					sceneAct[neighbor.SceneID] = act
				}
				queue = append(queue, entry{nodeID: neighborID, act: act, hops: e.hops + 1})
			}
		}
	}
	return sceneAct
}

// rankActivatedScenes orders scenes by activation desc (ties by scene ID
// for determinism) and cuts to maxScenes.
func rankActivatedScenes(sceneAct map[uint64]float32, maxScenes int) []uint64 {
	ids := make([]uint64, 0, len(sceneAct))
	for sid := range sceneAct {
		ids = append(ids, sid)
	}
	slices.SortFunc(ids, func(a, b uint64) int {
		if sceneAct[a] != sceneAct[b] {
			return cmp.Compare(sceneAct[b], sceneAct[a]) // higher activation first
		}
		return cmp.Compare(a, b) // ties by scene ID for determinism
	})
	if len(ids) > maxScenes {
		ids = ids[:maxScenes]
	}
	return ids
}

// collectSceneHits loads each ranked scene's depth<=1 topics; a scene whose
// topic listing fails is skipped.
func collectSceneHits(agentID uint64, engine *core.StorageEngine, l2Meta *index.L2MetaIndex,
	ids []uint64, sceneAct map[uint64]float32) []SceneHit {
	hits := make([]SceneHit, 0, len(ids))
	for _, sid := range ids {
		topics, err := repo.ListTopicsL2(repo.TopicListQuery{
			Engine:  engine,
			AgentID: agentID,
			MetaIdx: l2Meta,
			SceneID: common.FormatHash(sid),
			Depth:   1,
			Num:     2,
		})
		if err != nil {
			continue
		}
		scored := make([]ScoredTopic, 0, len(topics))
		for _, t := range topics {
			scored = append(scored, ScoredTopic{Topic: t, Score: sceneAct[sid]})
		}
		hits = append(hits, SceneHit{SceneID: sid, Score: sceneAct[sid], Topics: scored})
	}
	return hits
}
