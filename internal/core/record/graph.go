// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package record

import (
	"encoding/json"
	"fmt"

	"memhop/internal/core/model"
	"memhop/internal/core/storage"
)

// ReadHypergraphNode reads and deserializes a HypergraphNode from the storage engine.
func ReadHypergraphNode(engine *storage.StorageEngine, id uint64) (*model.HypergraphNode, error) {
	_, data, err := engine.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	var slot model.HypergraphNode
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, fmt.Errorf("unmarshal HypergraphNode: %w", err)
	}
	return &slot, nil
}

// WriteHypergraphNode serializes and writes a HypergraphNode to the storage engine.
func WriteHypergraphNode(engine *storage.StorageEngine, id uint64, slot *model.HypergraphNode) error {
	data, err := json.Marshal(slot)
	if err != nil {
		return fmt.Errorf("marshal HypergraphNode: %w", err)
	}
	_, err = engine.WriteRecord(storage.RecL3GraphNode, id, data)
	return err
}

// ReadHypergraphEdge reads and deserializes a HypergraphEdge from the storage engine.
func ReadHypergraphEdge(engine *storage.StorageEngine, id uint64) (*model.HypergraphEdge, error) {
	_, data, err := engine.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	var slot model.HypergraphEdge
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, fmt.Errorf("unmarshal HypergraphEdge: %w", err)
	}
	return &slot, nil
}

// WriteHypergraphEdge serializes and writes a HypergraphEdge to the storage engine.
func WriteHypergraphEdge(engine *storage.StorageEngine, id uint64, slot *model.HypergraphEdge) error {
	data, err := json.Marshal(slot)
	if err != nil {
		return fmt.Errorf("marshal HypergraphEdge: %w", err)
	}
	_, err = engine.WriteRecord(storage.RecL3GraphEdge, id, data)
	return err
}

// ReadGraphSlot reads and deserializes a HypergraphSlot from the storage engine.
func ReadGraphSlot(engine *storage.StorageEngine, id uint64) (*model.HypergraphSlot, error) {
	_, data, err := engine.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	var slot model.HypergraphSlot
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, fmt.Errorf("unmarshal HypergraphSlot: %w", err)
	}
	return &slot, nil
}

// WriteGraphSlot serializes and writes a HypergraphSlot to the storage engine.
func WriteGraphSlot(engine *storage.StorageEngine, id uint64, slot *model.HypergraphSlot) error {
	data, err := json.Marshal(slot)
	if err != nil {
		return fmt.Errorf("marshal HypergraphSlot: %w", err)
	}
	_, err = engine.WriteRecord(storage.RecL3GraphSlot, id, data)
	return err
}
