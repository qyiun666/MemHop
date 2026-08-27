// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package knowledge is the L3 knowledge-graph capability: matching stored
// hypergraph nodes against a query. It reads nodes through the engine and
// owns the match policy (keyword overlap both ways, title/content mention).
package knowledge

import (
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// MatchGraphs returns graph IDs whose nodes are mentioned by the query
// keywords or text. Search attaches these graph IDs to a new topic as
// L3Refs, which is what makes DirectedL3ID scoping work.
func MatchGraphs(engine *core.StorageEngine, agentID uint64, keywords []string, text string) []uint64 {
	query := strings.ToLower(strings.TrimSpace(text))
	terms := make([]string, 0, len(keywords))
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw != "" {
			terms = append(terms, kw)
		}
	}

	var graphIDs []uint64
	for _, node := range core.CollectAllHypergraphNodes(engine, agentID) {
		if !nodeMatches(node, terms, query) {
			continue
		}
		graphIDs = append(graphIDs, node.GraphID)
	}
	if len(graphIDs) == 0 {
		return nil
	}
	slices.Sort(graphIDs)
	return common.DedupSorted(graphIDs)
}

func nodeMatches(node core.HypergraphNode, terms []string, query string) bool {
	title := strings.ToLower(node.Title)
	content := strings.ToLower(node.Content)
	for _, term := range terms {
		if term == "" {
			continue
		}
		if strings.Contains(title, term) || strings.Contains(content, term) {
			return true
		}
		for _, kw := range node.Keywords {
			kw = strings.ToLower(kw)
			if strings.Contains(kw, term) || strings.Contains(term, kw) {
				return true
			}
		}
	}
	if query == "" {
		return false
	}
	if strings.Contains(query, title) && title != "" {
		return true
	}
	for _, kw := range node.Keywords {
		kw = strings.ToLower(kw)
		if kw != "" && strings.Contains(query, kw) {
			return true
		}
	}
	return false
}
