// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package graph holds the L3 knowledge-graph small methods: batch-import steps
// and node/subgraph query steps. Each assembles repo/core record features; the
// composition root keeps the big methods (ImportL3, QueryL3Nodes,
// QueryL3Subgraph) that lock the domain and compose them.

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

// ImportBatch carries one ImportL3 call: its conflict mode and result plus the
// caches that keep the batch a single pass over the stored graph set (domain →
// graph id, graph → node titles, graph → edge keys). The composition root
// creates it under the domain lock and reads the result back afterwards.
type ImportBatch struct {
	engine     *core.StorageEngine
	agentID    uint64
	mode       core.L3ImportMode
	result     *core.L3ImportResult
	graphIDs   map[string]uint64
	touched    map[uint64]struct{}
	nodeTitles map[uint64]map[string]struct{}
	edgeKeys   map[uint64]map[string]struct{}
}

// NewImportBatch seeds a batch with the domain's existing graph names, so a
// repeated import extends a graph instead of starting a second one.
func NewImportBatch(engine *core.StorageEngine, agentID uint64, mode core.L3ImportMode) *ImportBatch {
	b := &ImportBatch{
		engine:     engine,
		agentID:    agentID,
		mode:       mode,
		result:     &core.L3ImportResult{CreatedIDs: []string{}, UpdatedIDs: []string{}},
		graphIDs:   make(map[string]uint64),
		touched:    make(map[uint64]struct{}),
		nodeTitles: make(map[uint64]map[string]struct{}),
		edgeKeys:   make(map[uint64]map[string]struct{}),
	}
	for _, g := range core.CollectAllGraphSlots(engine, agentID) {
		if cur, ok := b.graphIDs[g.Name]; !ok || preferGraphID(g.Name, cur, g.IDHash) {
			b.graphIDs[g.Name] = g.IDHash
		}
	}
	return b
}

// preferGraphID arbitrates two slots that share one domain label. A file can
// carry that collision (a rename done before the label check existed), and the
// record scan visits slots in map order — so without a rule here, importing the
// label would write into a different graph on each run. The graph whose id
// derives from the label owns it; a tie falls to the smaller id.
func preferGraphID(name string, cur, next uint64) bool {
	derived := common.HashID(name)
	if (next == derived) != (cur == derived) {
		return next == derived
	}
	return next < cur
}

// CheckName refuses a rename onto a label another graph of this domain already
// carries. The label is how a domain addresses a graph, so two slots under one
// label would make ImportL3 resolve that domain ambiguously.
func CheckName(engine *core.StorageEngine, agentID uint64, id uint64, name string) error {
	for _, g := range core.CollectAllGraphSlots(engine, agentID) {
		if g.IDHash != id && g.Name == name {
			return common.NewError(common.ErrInvalidQuery,
				fmt.Sprintf("graph %s already carries the label %q", common.FormatHash(g.IDHash), name))
		}
	}
	return nil
}

// Result is the report the batch has accumulated so far.
func (b *ImportBatch) Result() *core.L3ImportResult { return b.result }

// GraphIDs lists, in hex and sorted, every graph this batch wrote into. A host
// gets the ids back because a graph id is hash(Domain) and nothing else on the
// public surface derives that hash — without it an imported graph can only be
// found again by listing the domain and matching names.
func (b *ImportBatch) GraphIDs() []string {
	out := make([]string, 0, len(b.touched))
	for id := range b.touched {
		out = append(out, common.FormatHash(id))
	}
	slices.Sort(out)
	return out
}

// ImportNode applies one item's node: graph slot create/reuse, then node
// create/merge/overwrite per mode with its SourceRef.
func (b *ImportBatch) ImportNode(item *core.L3ImportItem) error {
	graphID, err := b.graphFor(item.Domain)
	if err != nil {
		return err
	}
	titles := b.titles(graphID)
	if _, exists := titles[item.Title]; exists {
		var merge func(*core.HypergraphNode, string, string, []string, string, int64)
		switch b.mode {
		case core.L3ImportSkip:
			b.result.SkippedCount++
			return nil
		case core.L3ImportMerge:
			merge = knowledge.MergeFields
		case core.L3ImportOverwrite:
			merge = knowledge.OverwriteFields
		default:
			return common.NewError(common.ErrInvalidQuery,
				fmt.Sprintf("import mode %q is not supported", b.mode))
		}
		if err := b.mutateNode(graphID, *item, merge); err != nil {
			return err
		}
		b.result.UpdatedIDs = append(b.result.UpdatedIDs,
			common.FormatHash(repo.NodeIDL3(graphID, item.Title)))
		return nil
	}
	id, err := repo.CreateNodeL3(b.engine, b.agentID, graphID, item.Title, item.NodeType, item.Content, item.Keywords, item.SourceRef)
	if err != nil {
		return err
	}
	titles[item.Title] = struct{}{}
	b.result.CreatedIDs = append(b.result.CreatedIDs, common.FormatHash(id))
	return nil
}

// ImportRelations resolves one item's Related entries against the graph's node
// set and creates the hyperedges; every unresolvable entry is recorded in
// result.Errors while the rest continue. A relation names its whole far side,
// so the edge it creates spans the item plus every target — an n-node fact
// stays one edge instead of dissolving into n pairs. An edge the graph already
// carries (same member set, same kind) is not created again, so re-importing a
// batch is idempotent at any arity.
func (b *ImportBatch) ImportRelations(item *core.L3ImportItem) {
	if len(item.Related) == 0 {
		return
	}
	graphID, ok := b.graphIDs[item.Domain]
	if !ok {
		return
	}
	titles := b.titles(graphID)
	keys := b.edges(graphID)
	for _, rel := range item.Related {
		members, reason := b.relationMembers(graphID, item.Title, rel, titles)
		if reason != "" {
			b.result.Errors = append(b.result.Errors, fmt.Sprintf("%s: relation %v: %s", item.Title, rel.Titles, reason))
			continue
		}
		key := repo.EdgeKeyL3(members, rel.Kind)
		if _, exists := keys[key]; exists {
			continue
		}
		if _, err := repo.CreateEdgeL3(b.engine, b.agentID, graphID, rel.Kind, members, 1.0); err != nil {
			b.result.Errors = append(b.result.Errors, fmt.Sprintf("%s: relation %v: %v", item.Title, rel.Titles, err))
			continue
		}
		keys[key] = struct{}{}
		b.result.EdgesCreated++
	}
}

// relationMembers turns one relation into its sorted member set, or returns
// the reason it names no valid edge. The source item is always a member, so
// Titles is the far side: one title is a binary relation, several are one
// n-ary hyperedge over the whole set.
func (b *ImportBatch) relationMembers(graphID uint64, source string, rel core.L3Relation, titles map[string]struct{}) ([]uint64, string) {
	if rel.Kind > core.EdgeCustom {
		return nil, fmt.Sprintf("invalid edge kind %d", rel.Kind)
	}
	if len(rel.Titles) == 0 {
		return nil, "no target node named"
	}
	seen := map[string]struct{}{source: {}}
	members := []uint64{repo.NodeIDL3(graphID, source)}
	for _, target := range rel.Titles {
		switch {
		case target == "":
			return nil, "empty target title"
		case target == source:
			return nil, "self-referencing target"
		}
		if _, dup := seen[target]; dup {
			return nil, fmt.Sprintf("target %q named twice", target)
		}
		if _, exists := titles[target]; !exists {
			return nil, fmt.Sprintf("target node %q not found", target)
		}
		seen[target] = struct{}{}
		members = append(members, repo.NodeIDL3(graphID, target))
	}
	slices.Sort(members)
	return members, ""
}

// graphFor returns the graph of a domain, creating its slot only when the
// domain has none. An existing slot is reused as stored: its Name is the host's
// label and may have been set by UpdateL3, which the derived id must not undo.
func (b *ImportBatch) graphFor(domain string) (uint64, error) {
	graphID, ok := b.graphIDs[domain]
	if !ok {
		gid, err := repo.EnsureGraphL3(b.engine, b.agentID, domain, core.HypergraphSource{Kind: core.SourceManual})
		if err != nil {
			return 0, err
		}
		graphID, b.graphIDs[domain] = gid, gid
	}
	b.touched[graphID] = struct{}{}
	return graphID, nil
}

// titles returns the graph's node-title set, loading it once per graph.
func (b *ImportBatch) titles(graphID uint64) map[string]struct{} {
	set, ok := b.nodeTitles[graphID]
	if !ok {
		set = make(map[string]struct{})
		for _, n := range repo.ListNodeL3(b.engine, b.agentID, graphID) {
			set[n.Title] = struct{}{}
		}
		b.nodeTitles[graphID] = set
	}
	return set
}

// edges returns the graph's edge keys, loading them once per graph.
func (b *ImportBatch) edges(graphID uint64) map[string]struct{} {
	set, ok := b.edgeKeys[graphID]
	if !ok {
		set = make(map[string]struct{})
		for _, e := range repo.ListEdgeL3(b.engine, b.agentID, graphID) {
			members := slices.Clone(e.NodeIDs)
			slices.Sort(members)
			set[repo.EdgeKeyL3(members, e.Kind)] = struct{}{}
		}
		b.edgeKeys[graphID] = set
	}
	return set
}

// mutateNode applies one knowledge field-merge policy to the stored node of an
// import item (record access and membership stay in the repo).
func (b *ImportBatch) mutateNode(graphID uint64, item core.L3ImportItem,
	merge func(*core.HypergraphNode, string, string, []string, string, int64)) error {
	now := time.Now().UnixMilli()
	_, err := repo.MutateNodeL3(b.engine, b.agentID, graphID, item.Title, func(n *core.HypergraphNode) {
		merge(n, item.NodeType, item.Content, item.Keywords, item.SourceRef, now)
	})
	return err
}
