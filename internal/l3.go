// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 hypergraph big methods of the composition root: view / import / update
// / delete. The import steps live in internal/graph. All L3 records live in
// the file-wide shared domain (core.SharedPoolAgentID): one file hosts a single
// L3 pool that every agent domain shares, and the agentID parameter of these
// methods only proves that the caller's own domain is still alive.

package internal

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/graph"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/scene"
)

func (db *DB) GetL3(agentID uint64, id string) (*L3Graph, error) {
	ac, err := db.lockSharedPool(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	return db.getL3Graph(id)
}

// getL3Graph is the lock-free impl shared by GetL3 and UpdateL3 (shared
// pool domain lock held by the caller).
func (db *DB) getL3Graph(id string) (*L3Graph, error) {
	slot, err := repo.ReadSharedGraphL3(db.engine, id)
	if err != nil {
		return nil, err
	}
	return db.graphView(slot)
}

// graphView assembles the host-facing graph around an already-read slot. An
// empty member set renders as an empty slice, not nil, so a graph with nothing
// in it is distinguishable from a field the engine forgot to fill. A member that
// will not read back stops the assembly: this view is the whole graph, so one
// node short is not a smaller answer but a claim that the graph never held it.
func (db *DB) graphView(slot *core.HypergraphSlot) (*L3Graph, error) {
	nodes, err := repo.ListNodeL3(db.engine, core.SharedPoolAgentID, slot.IDHash)
	if err != nil {
		return nil, err
	}
	edges, err := repo.ListEdgeL3(db.engine, core.SharedPoolAgentID, slot.IDHash)
	if err != nil {
		return nil, err
	}
	if nodes == nil {
		nodes = []core.HypergraphNode{}
	}
	if edges == nil {
		edges = []core.HypergraphEdge{}
	}
	return &L3Graph{Slot: *slot, Nodes: nodes, Edges: edges}, nil
}

// ListL3 lists every graph of the file-wide pool, sorted by id: the scan under
// it is a hash map, so without a sort one host would see the same graphs in a
// different order on each call. The scan is the strict one — a host resolves its
// anchors against this answer, so a slot missing from it reads as "the pool holds
// no such graph", which is the same reading the import path refuses to give.
func (db *DB) ListL3(agentID uint64) ([]core.HypergraphSlot, error) {
	ac, err := db.lockSharedPool(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	all, err := core.CollectAllGraphSlots(db.engine, core.SharedPoolAgentID)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(all, func(a, b core.HypergraphSlot) int {
		return cmp.Compare(a.IDHash, b.IDHash)
	})
	if all == nil {
		return []core.HypergraphSlot{}, nil
	}
	return all, nil
}

// ImportL3 batch-imports knowledge nodes: shared-domain graph slot
// create/reuse, existing nodes handled by mode, then Related hyperedges
// resolved in a second pass (a relation may target an item later in the
// batch). The batch is validated up front — every item needs a Title and a
// Domain — so a malformed request writes nothing at all; per-item storage
// failures are what result.Errors reports. nil is only returned on success.
// The result carries the graph ids each domain resolved into (a Skip-mode batch that
// added nothing still reports its graph) as well as the node ids, because a host
// needs the former to hang the graph on a scene.
// Every graph this batch actually changed gets one slot write at the end, moving
// its UpdatedAt forward; a graph it only read keeps its clock, and a stamp that
// fails is reported in result.Errors rather than undoing records already stored.
// Graphs imported by one agent are visible to every agent of the file.
func (db *DB) ImportL3(agentID uint64, items []L3ImportItem, mode L3ImportMode) (*L3ImportResult, error) {
	ac, err := db.lockSharedPool(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	if len(items) == 0 {
		return nil, common.NewError(common.ErrInvalidQuery, "import: no items")
	}
	for i := range items {
		if items[i].Title == "" {
			return nil, common.NewError(common.ErrInvalidQuery,
				fmt.Sprintf("import: item %d has no title", i))
		}
		if items[i].Domain == "" {
			return nil, common.NewError(common.ErrInvalidQuery,
				fmt.Sprintf("import: item %q has no domain", items[i].Title))
		}
	}
	batch, err := graph.NewImportBatch(db.engine, core.SharedPoolAgentID, mode)
	if err != nil {
		// The mode is judged there, ahead of any read or write, so a batch refused for
		// an undefined one leaves nothing behind — same as a malformed item.
		return nil, common.NewError(common.CodeOf(err), "import", err)
	}
	for i := range items {
		if err := batch.ImportNode(&items[i]); err != nil {
			batch.Result().Errors = append(batch.Result().Errors, fmt.Sprintf("%s: %v", items[i].Title, err))
			continue
		}
	}
	// Relations for every item, including one whose node was skipped: edges
	// are deduped by their sorted members plus kind, so re-declaring one is a
	// no-op — while withholding it would silently drop the relations this batch
	// declares onto a node that already existed.
	for i := range items {
		batch.ImportRelations(&items[i])
	}
	result := batch.Result()
	result.GraphIDs = batch.GraphIDs()
	if err := batch.StampChanged(); err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
	return result, nil
}

// UpdateL3 partially updates a graph slot (currently Name only). The new name
// has to be free: a domain label addresses a graph for the import path, so two
// slots under one label would make that label resolve ambiguously. An empty one
// is refused for the same reason in the other direction — a graph carrying no
// label is one ImportL3 can never find again — while a nil name is the "change
// nothing" spelling.
func (db *DB) UpdateL3(agentID uint64, id string, name *string) (*L3Graph, error) {
	ac, err := db.lockSharedPool(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	graphHash, err := common.ParseID(id)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
	}
	if name != nil {
		if *name == "" {
			return nil, common.NewError(common.ErrInvalidQuery,
				"a graph label is how ImportL3 finds the graph, so an empty one names nothing", nil)
		}
		if err := graph.CheckName(db.engine, core.SharedPoolAgentID, graphHash, *name); err != nil {
			return nil, err
		}
	}
	slot, err := repo.UpdateGraphL3(db.engine, core.SharedPoolAgentID, graphHash, name)
	if err != nil {
		return nil, err
	}
	return db.graphView(slot)
}

// DeleteL3 cascades: deletes the graph with all its nodes and edges from the
// shared L3 domain, then drops the L2 anchors that named it in every agent
// domain (the default domain plus all registered tenants). The two phases never
// hold two domain locks at once, so no agent domain can end up blocking the shared
// pool behind a long operation.
//
// The graph goes first because that is what makes the cascade close: an anchor is
// written only while the graph it names exists, and the detach takes the same
// domain lock the anchor write holds. So an anchor is either already there — and
// this pass clears it — or it loses validation against a deleted graph. Reversing
// the two lets an anchor validate, land after the detach, and outlive the graph.
func (db *DB) DeleteL3(agentID uint64, id string) error {
	ac, err := db.lockSharedPool(agentID)
	if err != nil {
		return err
	}
	slot, err := repo.ReadSharedGraphL3(db.engine, id)
	if err != nil {
		ac.Mu.Unlock()
		return err
	}
	graphHash := slot.IDHash
	err = repo.DeleteGraphL3(db.engine, core.SharedPoolAgentID, graphHash)
	ac.Mu.Unlock()
	if err != nil {
		return err
	}
	return db.detachGraphAnchors(graphHash)
}

// detachGraphAnchors clears every scene anchor naming graphHash across the
// default domain and all registered tenants.
func (db *DB) detachGraphAnchors(graphHash uint64) error {
	db.agentsMu.Lock()
	targets := make([]uint64, 0, len(db.idToName)+1)
	targets = append(targets, core.DefaultAgentID)
	for id := range db.idToName {
		targets = append(targets, id)
	}
	db.agentsMu.Unlock()
	var errs []error
	for _, id := range targets {
		ac, err := db.lockAgent(id)
		if err != nil {
			// The domain is gone, being deleted or the DB is closing: its
			// records are gone or going, so there is no anchor left to clear.
			continue
		}
		if err := scene.DetachGraph(db.engine, id, graphHash); err != nil {
			errs = append(errs, err)
		}
		ac.Mu.Unlock()
	}
	return errors.Join(errs...)
}
