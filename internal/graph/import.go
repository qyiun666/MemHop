// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package graph holds the L3 knowledge-graph small methods: the batch-import
// steps and the node/subgraph query steps, each assembling repo/core record
// features.

package graph

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/qyiun666/MemHop/internal/cap/knowledge"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ImportBatch carries one import: its policy for a node the graph already holds,
// its result plus the three indexes built once up front — domain → graph id,
// graph → node titles, graph → edge keys — so applying an item never re-reads the
// pool. Callers hold the domain lock.
type ImportBatch struct {
	engine     *core.StorageEngine
	agentID    uint64
	merge      mergeFn
	result     *core.L3ImportResult
	graphIDs   map[string]uint64
	touched    map[uint64]struct{}
	changed    map[uint64]struct{}
	nodeTitles map[uint64]map[string]struct{}
	edgeKeys   map[uint64]map[string]struct{}
}

// mergeFn is one field-merge policy applied to a node the graph already holds.
type mergeFn func(*core.HypergraphNode, string, string, []string, string, int64)

// mergePolicy resolves the host's mode into a field-merge policy once, where the
// batch is built. A nil policy is Skip mode: a node the graph holds is left exactly
// as stored. An undefined mode is refused here, not answered per item.
func mergePolicy(mode core.L3ImportMode) (mergeFn, error) {
	if !mode.Valid() {
		// Spelled from the constants, because a host follows the words in a refusal and
		// words that get refused are a dead end.
		return nil, common.NewError(common.ErrInvalidQuery,
			"import mode "+string(mode)+" is none of "+
				string(core.L3ImportSkip)+", "+string(core.L3ImportMerge)+", "+string(core.L3ImportOverwrite))
	}
	switch mode {
	case core.L3ImportMerge:
		return knowledge.MergeFields, nil
	case core.L3ImportOverwrite:
		return knowledge.OverwriteFields, nil
	}
	return nil, nil
}

// NewImportBatch seeds a batch with the domain's existing graph names, so a
// repeated import extends a graph instead of starting a second one. The seeding scan
// is strict: its answer decides a write, and treating a slot that will not read back
// as a free label mints a second graph under a label that is in use.
func NewImportBatch(engine *core.StorageEngine, agentID uint64, mode core.L3ImportMode) (*ImportBatch, error) {
	merge, err := mergePolicy(mode)
	if err != nil {
		return nil, err
	}
	slots, err := core.CollectAllGraphSlots(engine, agentID)
	if err != nil {
		return nil, err
	}
	b := &ImportBatch{
		engine:     engine,
		agentID:    agentID,
		merge:      merge,
		result:     &core.L3ImportResult{CreatedIDs: []string{}, UpdatedIDs: []string{}, Errors: []string{}},
		graphIDs:   make(map[string]uint64),
		touched:    make(map[uint64]struct{}),
		changed:    make(map[uint64]struct{}),
		nodeTitles: make(map[uint64]map[string]struct{}),
		edgeKeys:   make(map[uint64]map[string]struct{}),
	}
	for _, g := range slots {
		if cur, ok := b.graphIDs[g.Name]; !ok || preferGraphID(g.Name, cur, g.IDHash) {
			b.graphIDs[g.Name] = g.IDHash
		}
	}
	if err := b.loadContents(); err != nil {
		return nil, err
	}
	return b, nil
}

// loadContents indexes every node title and edge key of the pool, keyed by graph.
// Strict: the batch needs the whole membership of a graph it is asked about, and a
// record-by-record read is what let an undecodable node look like a free title.
func (b *ImportBatch) loadContents() error {
	nodes, err := core.CollectAllStrict[core.HypergraphNode](b.engine, b.agentID, core.RecL3GraphNode)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		b.titles(n.GraphID)[n.Title] = struct{}{}
	}
	edges, err := core.CollectAllStrict[core.HypergraphEdge](b.engine, b.agentID, core.RecL3GraphEdge)
	if err != nil {
		return err
	}
	for _, e := range edges {
		members := slices.Clone(e.NodeIDs)
		slices.Sort(members)
		b.edges(e.GraphID)[repo.EdgeKeyL3(members, e.Kind)] = struct{}{}
	}
	return nil
}

// preferGraphID arbitrates two slots sharing one domain label. A file can carry
// that collision and the record scan visits slots in map order, so without a rule
// here the same import writes a different graph each run. The graph whose id derives
// from the label owns it; a tie falls to the smaller id.
func preferGraphID(name string, cur, next uint64) bool {
	derived := common.HashID(name)
	if (next == derived) != (cur == derived) {
		return next == derived
	}
	return next < cur
}

// CheckName refuses a rename onto a label another graph of this domain already
// carries. The scan is strict for the same reason as the seeding one: a slot the
// engine cannot decode still holds its label.
func CheckName(engine *core.StorageEngine, agentID uint64, id uint64, name string) error {
	slots, err := core.CollectAllGraphSlots(engine, agentID)
	if err != nil {
		return err
	}
	for _, g := range slots {
		if g.IDHash != id && g.Name == name {
			return common.NewError(common.ErrInvalidQuery,
				fmt.Sprintf("graph %s already carries the label %q", common.FormatHash(g.IDHash), name))
		}
	}
	return nil
}

// Result is the report the batch has accumulated so far.
func (b *ImportBatch) Result() *core.L3ImportResult { return b.result }

// GraphIDs lists, in hex and sorted, every graph this batch resolved a domain
// into — including one it imported nothing new into. Without it a graph this batch
// touched could only be found again by listing the domain and matching names.
func (b *ImportBatch) GraphIDs() []string {
	out := make([]string, 0, len(b.touched))
	for id := range b.touched {
		out = append(out, common.FormatHash(id))
	}
	slices.Sort(out)
	return out
}

// StampChanged moves UpdatedAt forward on every graph whose contents this batch
// actually wrote — a node created, merged or overwritten, a hyperedge added. A graph
// it merely read keeps its clock, so UpdatedAt answers "when did this graph last
// change" and not "when was an import last aimed at it".
//
// Call once, after the whole batch: one write per changed graph. Failures are joined
// rather than returned at the first, and they do not undo the records already
// stored — a graph whose stamp failed reads stale, which the caller reports.
func (b *ImportBatch) StampChanged() error {
	graphIDs := make([]uint64, 0, len(b.changed))
	for graphID := range b.changed {
		graphIDs = append(graphIDs, graphID)
	}
	// Sorted, so a batch that fails to stamp several graphs reports it in one
	// order every time — the joined lines reach the host as result.Errors.
	slices.Sort(graphIDs)
	var errs []error
	for _, graphID := range graphIDs {
		if _, err := repo.UpdateGraphL3(b.engine, b.agentID, graphID, nil); err != nil {
			errs = append(errs, fmt.Errorf("stamp graph %s: %w", common.FormatHash(graphID), err))
		}
	}
	return errors.Join(errs...)
}

// ImportNode applies one item's node: graph slot create/reuse, then the batch's
// policy for a node the graph already holds.
func (b *ImportBatch) ImportNode(item *core.L3ImportItem) error {
	graphID, err := b.graphFor(item.Domain)
	if err != nil {
		return err
	}
	titles := b.titles(graphID)
	if _, exists := titles[item.Title]; exists {
		if b.merge == nil {
			// Skip mode: a node the graph holds is left exactly as stored.
			b.result.SkippedCount++
			return nil
		}
		id, err := b.mutateNode(graphID, *item)
		if err != nil {
			return err
		}
		b.changed[graphID] = struct{}{}
		b.result.UpdatedIDs = append(b.result.UpdatedIDs, common.FormatHash(id))
		return nil
	}
	id, err := repo.CreateNodeL3(b.engine, b.agentID, graphID, item.Title, item.NodeType, item.Content, item.Keywords, item.SourceRef)
	if err != nil {
		return err
	}
	titles[item.Title] = struct{}{}
	b.changed[graphID] = struct{}{}
	b.result.CreatedIDs = append(b.result.CreatedIDs, common.FormatHash(id))
	return nil
}

// ImportRelations resolves one item's Related entries against the graph's node set
// and creates the hyperedges; every unresolvable entry is recorded in result.Errors
// while the rest continue. A relation names its whole far side, so an n-node fact
// stays one edge instead of dissolving into n pairs, and an edge the graph already
// carries (same member set, same kind) is not created again — re-importing a batch
// is idempotent at any arity.
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
		if _, err := repo.CreateEdgeL3(b.engine, b.agentID, graphID, rel.Kind, members); err != nil {
			b.result.Errors = append(b.result.Errors, fmt.Sprintf("%s: relation %v: %v", item.Title, rel.Titles, err))
			continue
		}
		keys[key] = struct{}{}
		b.changed[graphID] = struct{}{}
		b.result.EdgesCreated++
	}
}

// relationMembers turns one relation into its sorted member set, or returns the
// reason it names no valid edge. The source item is always a member, so Titles is
// the far side.
func (b *ImportBatch) relationMembers(graphID uint64, source string, rel core.L3Relation, titles map[string]struct{}) ([]uint64, string) {
	if !rel.Kind.Valid() {
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

// graphFor returns the graph of a domain, creating its slot only when the domain has
// none. An existing slot is reused as stored: its Name is a label that may have been
// changed since the id was derived, and reusing the id must not undo that label.
func (b *ImportBatch) graphFor(domain string) (uint64, error) {
	graphID, ok := b.graphIDs[domain]
	if !ok {
		gid, err := repo.EnsureGraphL3(b.engine, b.agentID, domain)
		if err != nil {
			return 0, err
		}
		graphID, b.graphIDs[domain] = gid, gid
	}
	b.touched[graphID] = struct{}{}
	return graphID, nil
}

// titles returns the set one graph holds, creating an empty one for a graph with no
// nodes yet. The batch writes into what it returns, so a new title lands in the
// index as well and a later item in the same batch sees it.
func (b *ImportBatch) titles(graphID uint64) map[string]struct{} {
	set, ok := b.nodeTitles[graphID]
	if !ok {
		set = make(map[string]struct{})
		b.nodeTitles[graphID] = set
	}
	return set
}

// edges returns the edge keys one graph holds, same shape as titles.
func (b *ImportBatch) edges(graphID uint64) map[string]struct{} {
	set, ok := b.edgeKeys[graphID]
	if !ok {
		set = make(map[string]struct{})
		b.edgeKeys[graphID] = set
	}
	return set
}

// mutateNode applies the batch's field-merge policy to the stored node of one item
// and reports that node's id — the write already derived it.
func (b *ImportBatch) mutateNode(graphID uint64, item core.L3ImportItem) (uint64, error) {
	now := time.Now().UnixMilli()
	return repo.MutateNodeL3(b.engine, b.agentID, graphID, item.Title, func(n *core.HypergraphNode) {
		b.merge(n, item.NodeType, item.Content, item.Keywords, item.SourceRef, now)
	})
}
