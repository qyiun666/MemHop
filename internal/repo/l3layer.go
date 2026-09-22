// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ReadSharedGraphL3 reads one graph slot by its numeric id. Graphs live in the
// file-wide shared L3 domain, not in the caller's own, so every read of one goes
// through SharedPoolAgentID whatever domain asked. An unknown id is whatever the
// typed record read reports (ErrNotFound).
func ReadSharedGraphL3(engine *core.StorageEngine, graphID uint64) (*core.HypergraphSlot, error) {
	return core.ReadGraphSlot(engine, core.SharedPoolAgentID, graphID)
}

// CreateEdgeL3 creates a hyperedge whose id derives from EdgeKeyL3 — one
// formula for the address and for the semantic key a caller matches on —
// scoped to the graph, so members must arrive sorted: an unsorted list would
// address a second edge over the same relation. The kind is part of the
// identity because a node pair can carry several relations at once.
func CreateEdgeL3(engine *core.StorageEngine, agentID uint64, graphID uint64, kind core.GraphEdgeKind, nodeIDs []uint64) (uint64, error) {
	edgeID := common.HashID(common.FormatHash(graphID) + ":" + EdgeKeyL3(nodeIDs, kind))
	if err := l3AddressFree(engine, agentID, edgeID); err != nil {
		return 0, err
	}
	edge := &core.HypergraphEdge{
		IDHash:    edgeID,
		GraphID:   graphID,
		Kind:      kind,
		NodeIDs:   nodeIDs,
		CreatedAt: time.Now().UnixMilli(),
	}
	if err := core.WriteHypergraphEdge(engine, agentID, edgeID, edge); err != nil {
		return 0, err
	}
	return edgeID, nil
}

// EdgeKeyL3 is a hyperedge's semantic identity within a graph: the member
// nodes and the relation kind. The caller sorts the ids first, since edges are
// unordered over their members; matching on this key rather than on the hash
// keeps a re-import idempotent for edges written before the kind joined the id.
func EdgeKeyL3(nodeIDs []uint64, kind core.GraphEdgeKind) string {
	return fmt.Sprintf("%v:%d", nodeIDs, kind)
}

// ListEdgeL3 lists one graph's edges sorted by id. The order is part of the
// contract: the index under the collect is a hash map, so an unsorted listing
// would answer the same call twice in two orders and let a caller's cap fall on
// an arbitrary subset. The collect is strict: an edge that will not decode is
// not one less row, it is two members the graph stops relating.
func ListEdgeL3(engine *core.StorageEngine, agentID uint64, graphID uint64) ([]core.HypergraphEdge, error) {
	all, err := core.CollectAllStrict[core.HypergraphEdge](engine, agentID, core.RecL3GraphEdge)
	if err != nil {
		return nil, err
	}
	var out []core.HypergraphEdge
	for _, edge := range all {
		if edge.GraphID == graphID {
			out = append(out, edge)
		}
	}
	slices.SortFunc(out, func(a, b core.HypergraphEdge) int {
		return cmp.Compare(a.IDHash, b.IDHash)
	})
	return out, nil
}

// createGraphL3 writes a graph slot; ID = hash(name). An address some other
// record already holds is refused (l3AddressFree). EnsureGraphL3 is the
// exported create: it reuses a slot that is already there.
func createGraphL3(engine *core.StorageEngine, agentID uint64, name string) (uint64, error) {
	graphID := common.HashID(name)
	if err := l3AddressFree(engine, agentID, graphID); err != nil {
		return 0, err
	}
	now := time.Now().UnixMilli()
	slot := &core.HypergraphSlot{
		IDHash:    graphID,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := core.WriteGraphSlot(engine, agentID, graphID, slot); err != nil {
		return 0, err
	}
	return graphID, nil
}

// EnsureGraphL3 returns the graph of a domain name, creating its slot only when
// the address holds nothing at all. The id derives from the name asked for, but
// the stored Name is a label of its own that may have been renamed since —
// reusing an existing slot keeps that name and its CreatedAt intact.
func EnsureGraphL3(engine *core.StorageEngine, agentID uint64, name string) (uint64, error) {
	graphID := common.HashID(name)
	if slot, err := core.ReadGraphSlot(engine, agentID, graphID); err == nil {
		return slot.IDHash, nil
	} else if common.CodeOf(err) != common.ErrNotFound {
		return 0, err
	}
	return createGraphL3(engine, agentID, name)
}

// DeleteGraphL3 cascades: collects all nodes/edges of the graph plus the
// graph record and deletes them in one batch. Both collections are strict —
// a member that will not read back has to stop the delete instead of surviving
// a graph the host was told is gone.
func DeleteGraphL3(engine *core.StorageEngine, agentID uint64, id uint64) error {
	nodes, err := core.CollectAllStrict[core.HypergraphNode](engine, agentID, core.RecL3GraphNode)
	if err != nil {
		return err
	}
	edges, err := core.CollectAllStrict[core.HypergraphEdge](engine, agentID, core.RecL3GraphEdge)
	if err != nil {
		return err
	}
	var targets []uint64
	for _, node := range nodes {
		if node.GraphID == id {
			targets = append(targets, node.IDHash)
		}
	}
	for _, edge := range edges {
		if edge.GraphID == id {
			targets = append(targets, edge.IDHash)
		}
	}
	targets = append(targets, id)
	_, err = engine.DeleteRecordBatch(agentID, targets)
	return err
}

// UpdateGraphL3 partially updates a graph slot (currently Name only) and always
// moves UpdatedAt forward. A nil name is therefore a stamp: the way a caller
// whose write landed on the graph's nodes and edges, not on its label, records
// that the graph changed.
func UpdateGraphL3(engine *core.StorageEngine, agentID uint64, id uint64, name *string) (*core.HypergraphSlot, error) {
	slot, err := core.ReadGraphSlot(engine, agentID, id)
	if err != nil {
		return nil, err
	}
	if name != nil {
		slot.Name = *name
	}
	slot.UpdatedAt = time.Now().UnixMilli()
	if err := core.WriteGraphSlot(engine, agentID, id, slot); err != nil {
		return nil, err
	}
	return slot, nil
}

// CreateNodeL3 creates a hypergraph node; ID = hash(graphID:title). A
// non-empty sourceRef lands on the node's SourceRef.
func CreateNodeL3(engine *core.StorageEngine, agentID uint64, graphID uint64, title, nodeType, content string, keywords []string, sourceRef string) (uint64, error) {
	nodeID := NodeIDL3(graphID, title)
	if err := l3AddressFree(engine, agentID, nodeID); err != nil {
		return 0, err
	}
	now := time.Now().UnixMilli()
	node := &core.HypergraphNode{
		IDHash:    nodeID,
		GraphID:   graphID,
		Title:     title,
		NodeType:  nodeType,
		Content:   content,
		Keywords:  keywords,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if sourceRef != "" {
		node.SourceRef = &sourceRef
	}
	if err := core.WriteHypergraphNode(engine, agentID, nodeID, node); err != nil {
		return 0, err
	}
	return nodeID, nil
}

// NodeIDL3 derives the stable node ID from a graph ID and node title.
func NodeIDL3(graphID uint64, title string) uint64 {
	return common.HashID(fmt.Sprintf("%s:%s", common.FormatHash(graphID), title))
}

// ListNodeL3 lists one graph's nodes sorted and strict, for the same reasons
// ListEdgeL3 is.
func ListNodeL3(engine *core.StorageEngine, agentID uint64, graphID uint64) ([]core.HypergraphNode, error) {
	all, err := core.CollectAllStrict[core.HypergraphNode](engine, agentID, core.RecL3GraphNode)
	if err != nil {
		return nil, err
	}
	var out []core.HypergraphNode
	for _, node := range all {
		if node.GraphID == graphID {
			out = append(out, node)
		}
	}
	slices.SortFunc(out, func(a, b core.HypergraphNode) int {
		return cmp.Compare(a.IDHash, b.IDHash)
	})
	return out, nil
}

// MutateNodeL3 reads one node, applies mutate and writes it back. The merge
// policy itself is the caller's; this module keeps record access and membership
// validation only.
func MutateNodeL3(engine *core.StorageEngine, agentID uint64, graphID uint64, title string, mutate func(*core.HypergraphNode)) (uint64, error) {
	nodeID := NodeIDL3(graphID, title)
	node, err := core.ReadHypergraphNode(engine, agentID, nodeID)
	if err != nil {
		return 0, err
	}
	if node.GraphID != graphID {
		return 0, common.NewError(common.ErrNotFound, "node graph mismatch")
	}
	mutate(node)
	if err := core.WriteHypergraphNode(engine, agentID, nodeID, node); err != nil {
		return 0, err
	}
	return nodeID, nil
}

// l3AddressFree refuses a create whose derived id already holds a record. Every
// L3 id comes out of host-supplied text — a graph label, "<graph hex>:<title>",
// an edge's members and kind — and the whole pool shares one id space, so a
// label can literally spell another kind's derived form. The typed readers
// answer such a collision with ErrNotFound, the one answer that must not
// license a write: the record would come back as a type it never was.
func l3AddressFree(engine *core.StorageEngine, agentID uint64, id uint64) error {
	if engine.Contains(agentID, id) {
		return common.NewError(common.ErrInvalidQuery,
			"record "+common.FormatHash(id)+" already holds this address: a domain label or node title may not name another record's id")
	}
	return nil
}
