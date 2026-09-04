// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"fmt"
	"slices"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// CreateEdgeL3 creates a hyperedge; ID = hash(graphID:nodeIDs:kind). The kind
// is part of the identity because a node pair can carry several relations at
// once — hashing the pair alone made "a part of b" overwrite "a related to b".
func CreateEdgeL3(engine *core.StorageEngine, agentID uint64, graphID uint64, kind core.GraphEdgeKind, nodeIDs []uint64, weight float32) (uint64, error) {
	edgeID := common.HashID(fmt.Sprintf("%s:%v:%d", common.FormatHash(graphID), nodeIDs, kind))
	edge := &core.HypergraphEdge{
		IDHash:    edgeID,
		GraphID:   graphID,
		Kind:      kind,
		NodeIDs:   nodeIDs,
		Weight:    weight,
		CreatedAt: time.Now().UnixMilli(),
	}
	if err := core.WriteHypergraphEdge(engine, agentID, edgeID, edge); err != nil {
		return 0, err
	}
	return edgeID, nil
}

// EdgeKeyL3 is a hyperedge's semantic identity within a graph: the member
// nodes and the relation kind. Edges are unordered over their members, so the
// caller sorts the ids first; matching on this key rather than on the hash
// keeps a re-import idempotent for edges written before the kind joined the id.
func EdgeKeyL3(nodeIDs []uint64, kind core.GraphEdgeKind) string {
	return fmt.Sprintf("%v:%d", nodeIDs, kind)
}

func ListEdgeL3(engine *core.StorageEngine, agentID uint64, graphID uint64) []core.HypergraphEdge {
	var out []core.HypergraphEdge
	for _, edge := range core.CollectAllHypergraphEdges(engine, agentID) {
		if edge.GraphID == graphID {
			out = append(out, edge)
		}
	}
	return out
}

// CreateGraphL3 imports/creates a hypergraph; ID = hash(name). It writes the
// slot unconditionally, so it is only for a graph the caller has confirmed
// does not exist yet — see EnsureGraphL3 for the import path.
func CreateGraphL3(engine *core.StorageEngine, agentID uint64, name string, source core.HypergraphSource) (uint64, error) {
	graphID := common.HashID(name)
	now := time.Now().UnixMilli()
	slot := &core.HypergraphSlot{
		IDHash:    graphID,
		Name:      name,
		Source:    source,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := core.WriteGraphSlot(engine, agentID, graphID, slot); err != nil {
		return 0, err
	}
	return graphID, nil
}

// EnsureGraphL3 returns the graph of a domain name, creating its slot only
// when no record with that id exists. The id derives from the name an import
// batch was submitted under, but the stored Name is the host's own label —
// UpdateL3 may have renamed the graph. Reusing an existing slot therefore keeps
// that name and its CreatedAt intact, instead of a re-import silently undoing
// the rename.
func EnsureGraphL3(engine *core.StorageEngine, agentID uint64, name string, source core.HypergraphSource) (uint64, error) {
	graphID := common.HashID(name)
	if slot, err := core.ReadGraphSlot(engine, agentID, graphID); err == nil {
		return slot.IDHash, nil
	} else if common.CodeOf(err) != common.ErrNotFound {
		return 0, err
	}
	return CreateGraphL3(engine, agentID, name, source)
}

// DeleteGraphL3 cascades: collects all nodes/edges of the graph plus the
// graph record and deletes them in one batch.
func DeleteGraphL3(engine *core.StorageEngine, agentID uint64, id uint64) bool {
	var targets []uint64
	for _, node := range core.CollectAllHypergraphNodes(engine, agentID) {
		if node.GraphID == id {
			targets = append(targets, node.IDHash)
		}
	}
	for _, edge := range core.CollectAllHypergraphEdges(engine, agentID) {
		if edge.GraphID == id {
			targets = append(targets, edge.IDHash)
		}
	}
	targets = append(targets, id)
	_, err := engine.DeleteRecordBatch(agentID, targets)
	return err == nil
}

// DeleteNodesL3 removes the named nodes of one graph and every hyperedge that
// touches them: an edge left pointing at a deleted node resolves to nothing.
// Every id has to be a node of this graph, so a wrong id is reported instead of
// quietly deleting nothing.
func DeleteNodesL3(engine *core.StorageEngine, agentID uint64, graphID uint64, nodeIDs []uint64) error {
	members := make(map[uint64]struct{}, len(nodeIDs))
	for _, id := range nodeIDs {
		members[id] = struct{}{}
	}
	inGraph := make(map[uint64]struct{})
	for _, n := range ListNodeL3(engine, agentID, graphID) {
		inGraph[n.IDHash] = struct{}{}
	}
	for _, id := range nodeIDs {
		if _, ok := inGraph[id]; !ok {
			return common.NewError(common.ErrNotFound, fmt.Sprintf("node %s is not a node of graph %s",
				common.FormatHash(id), common.FormatHash(graphID)))
		}
	}
	targets := slices.Clone(nodeIDs)
	for _, e := range ListEdgeL3(engine, agentID, graphID) {
		for _, node := range e.NodeIDs {
			if _, hit := members[node]; hit {
				targets = append(targets, e.IDHash)
				break
			}
		}
	}
	_, err := engine.DeleteRecordBatch(agentID, targets)
	return err
}

// UpdateGraphL3 partially updates a graph slot (currently Name only).
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

func ListNodeL3(engine *core.StorageEngine, agentID uint64, graphID uint64) []core.HypergraphNode {
	var out []core.HypergraphNode
	for _, node := range core.CollectAllHypergraphNodes(engine, agentID) {
		if node.GraphID == graphID {
			out = append(out, node)
		}
	}
	return out
}

// MutateNodeL3 reads one node, applies mutate and writes it back; the merge
// policy itself belongs to the caller (cap/knowledge), so this module keeps
// record access and membership validation only.
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
