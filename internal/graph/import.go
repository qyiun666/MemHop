// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package graph holds the L3 knowledge-graph small methods: batch-import
// steps and node/subgraph query steps. Each assembles repo/core record
// features; the composition root keeps the big methods (ImportL3,
// QueryL3Nodes, QueryL3Subgraph) that lock the domain and compose them.

package graph

import (
	"fmt"
	"slices"
	"time"

	"github.com/qyiun666/MemHop/internal/cap/knowledge"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ImportOneNode applies one item's node: graph slot create/reuse, then
// node create/merge/overwrite per mode with its SourceRef. Reports whether
// the node landed (false = skipped existing node in Skip mode).
func ImportOneNode(engine *core.StorageEngine, agentID uint64, item *core.L3ImportItem, mode core.L3ImportMode, graphCache map[string]uint64, nodeTitles map[uint64]map[string]struct{}, result *core.L3ImportResult) (bool, error) {
	graphID, ok := graphCache[item.Domain]
	if !ok {
		gid, err := repo.CreateGraphL3(engine, agentID, item.Domain, core.HypergraphSource{Kind: core.SourceManual})
		if err != nil {
			return false, err
		}
		graphID, graphCache[item.Domain] = gid, gid
	}
	if _, seen := nodeTitles[graphID]; !seen {
		titles := make(map[string]struct{})
		for _, n := range repo.ListNodeL3(engine, agentID, graphID) {
			titles[n.Title] = struct{}{}
		}
		nodeTitles[graphID] = titles
	}
	nodeID := repo.NodeIDL3(graphID, item.Title)
	if _, exists := nodeTitles[graphID][item.Title]; exists {
		switch mode {
		case core.L3ImportSkip:
			result.SkippedCount++
			return false, nil
		case core.L3ImportMerge:
			if err := mutateImportedNode(engine, agentID, graphID, *item, knowledge.MergeFields); err != nil {
				return false, err
			}
		case core.L3ImportOverwrite:
			if err := mutateImportedNode(engine, agentID, graphID, *item, knowledge.OverwriteFields); err != nil {
				return false, err
			}
		}
		result.UpdatedIDs = append(result.UpdatedIDs, common.FormatHash(nodeID))
		return true, nil
	}
	id, err := repo.CreateNodeL3(engine, agentID, graphID, item.Title, item.NodeType, item.Content, item.Keywords, item.SourceRef)
	if err != nil {
		return false, err
	}
	nodeTitles[graphID][item.Title] = struct{}{}
	result.CreatedIDs = append(result.CreatedIDs, common.FormatHash(id))
	return true, nil
}

// ImportRelations resolves one item's Related entries against the graph's
// node set and creates the hyperedges; every unresolvable entry is recorded
// in result.Errors and the rest continue. Edge ids hash the sorted node
// pair, so re-importing the same batch is idempotent.
func ImportRelations(engine *core.StorageEngine, agentID uint64, item *core.L3ImportItem, graphCache map[string]uint64, nodeTitles map[uint64]map[string]struct{}, result *core.L3ImportResult) {
	if len(item.Related) == 0 {
		return
	}
	graphID, ok := graphCache[item.Domain]
	if !ok {
		return
	}
	fromID := repo.NodeIDL3(graphID, item.Title)
	for _, rel := range item.Related {
		switch {
		case rel.Title == "" || rel.Title == item.Title:
			result.Errors = append(result.Errors, fmt.Sprintf("%s: relation %q: empty or self target", item.Title, rel.Title))
			continue
		case rel.Kind > core.EdgeCustom:
			result.Errors = append(result.Errors, fmt.Sprintf("%s: relation %q: invalid edge kind %d", item.Title, rel.Title, rel.Kind))
			continue
		}
		if _, exists := nodeTitles[graphID][rel.Title]; !exists {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: relation %q: target node not found", item.Title, rel.Title))
			continue
		}
		ids := []uint64{fromID, repo.NodeIDL3(graphID, rel.Title)}
		slices.Sort(ids)
		if _, err := repo.CreateEdgeL3(engine, agentID, graphID, rel.Kind, ids, 1.0); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: relation %q: %v", item.Title, rel.Title, err))
			continue
		}
		result.EdgesCreated++
	}
}

// mutateImportedNode applies one knowledge field-merge policy to the stored
// node of an import item (record access and membership stay in the repo).
func mutateImportedNode(engine *core.StorageEngine, agentID uint64, graphID uint64, item core.L3ImportItem,
	merge func(*core.HypergraphNode, string, string, []string, string, int64)) error {
	now := time.Now().UnixMilli()
	_, err := repo.MutateNodeL3(engine, agentID, graphID, item.Title, func(n *core.HypergraphNode) {
		merge(n, item.NodeType, item.Content, item.Keywords, item.SourceRef, now)
	})
	return err
}
