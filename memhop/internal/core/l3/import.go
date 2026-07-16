// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Entity import from LLM hints into L3 hypergraph.

package l3

import (
	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
	"github.com/qyiun666/memhop/memhop/internal/hash"
	"github.com/qyiun666/memhop/memhop/internal/timeutil"
)

// EntityHint describes an entity extracted by the LLM for import.
type EntityHint struct {
	Title    string
	NodeType string
	Content  string
	Keywords []string
}

// ImportEntities imports entity hints as nodes into an L3 graph.
// Returns the list of newly created node hashes.
func ImportEntities(
	engine *storage.StorageEngine,
	graphID uint64,
	hints []EntityHint,
	l2ID uint64,
) ([]uint64, error) {
	now := timeutil.NowMs()
	hashes := make([]uint64, 0, len(hints))

	for _, hint := range hints {
		nodeHash, err := importOneEntity(engine, graphID, hint, l2ID, now)
		if err != nil {
			return hashes, err
		}
		hashes = append(hashes, nodeHash)
	}
	return hashes, nil
}

// importOneEntity creates and persists a single node from an entity hint.
func importOneEntity(
	engine *storage.StorageEngine,
	graphID uint64,
	hint EntityHint,
	l2ID uint64,
	now int64,
) (uint64, error) {
	nodeHash := hash.HashID(hint.Title)
	sourceRef := hash.FormatHash(l2ID)
	node := &model.HypergraphNode{
		IDHash:     nodeHash,
		GraphID:    graphID,
		Title:      hint.Title,
		NodeType:   hint.NodeType,
		Content:    hint.Content,
		Keywords:   hint.Keywords,
		SourceRef:  &sourceRef,
		Importance: 0.5,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Version:    1,
	}
	if err := AddNode(engine, node); err != nil {
		return 0, err
	}
	return nodeHash, nil
}
