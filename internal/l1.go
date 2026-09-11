// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 big methods of the composition root: the read face of the scene
// hypergraph. Nothing here writes — the nodes and the edges between them are
// built, decayed and pruned by Dream alone, so a host can read what
// consolidation decided but cannot set it. The building and decay steps live in
// internal/cap/engram.

package internal

import (
	"cmp"
	"slices"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ListL1 returns every scene node of the domain. The values are Dream's:
// Importance and the two emotion signals are what consolidation computed, and
// EdgeIDs name the co-occurrence edges incident on the node — two nodes sharing
// one id are a pair Dream judged related, which is all the structure this read
// exposes, since an edge itself has no public read.
//
// The result is sorted by id because the index underneath is a hash map: without
// sorting, one domain would answer the same call twice in two different orders.
func (db *DB) ListL1(agentID uint64) ([]core.SceneNode, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	nodes := core.CollectAllSceneNodes(db.engine, agentID)
	if nodes == nil {
		return []core.SceneNode{}, nil
	}
	slices.SortFunc(nodes, func(a, b core.SceneNode) int {
		return cmp.Compare(a.IDHash, b.IDHash)
	})
	return nodes, nil
}
