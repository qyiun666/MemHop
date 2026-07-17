// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package record provides typed Read/Write helpers for all storage record types.
package record

import (
	"encoding/json"
	"fmt"

	"memhop/internal/core/model"
	"memhop/internal/core/storage"
)

// ReadSceneNode reads and deserializes a SceneNode from the storage engine.
func ReadSceneNode(engine *storage.StorageEngine, id uint64) (*model.SceneNode, error) {
	_, data, err := engine.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	var slot model.SceneNode
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, fmt.Errorf("unmarshal SceneNode: %w", err)
	}
	return &slot, nil
}

// WriteSceneNode serializes and writes a SceneNode to the storage engine.
func WriteSceneNode(engine *storage.StorageEngine, id uint64, slot *model.SceneNode) error {
	data, err := json.Marshal(slot)
	if err != nil {
		return fmt.Errorf("marshal SceneNode: %w", err)
	}
	_, err = engine.WriteRecord(storage.RecL1SceneNode, id, data)
	return err
}

// ReadHyperedgeSlot reads and deserializes a HyperedgeSlot from the storage engine.
func ReadHyperedgeSlot(engine *storage.StorageEngine, id uint64) (*model.HyperedgeSlot, error) {
	_, data, err := engine.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	var slot model.HyperedgeSlot
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, fmt.Errorf("unmarshal HyperedgeSlot: %w", err)
	}
	return &slot, nil
}

// WriteHyperedgeSlot serializes and writes a HyperedgeSlot to the storage engine.
func WriteHyperedgeSlot(engine *storage.StorageEngine, id uint64, slot *model.HyperedgeSlot) error {
	data, err := json.Marshal(slot)
	if err != nil {
		return fmt.Errorf("marshal HyperedgeSlot: %w", err)
	}
	_, err = engine.WriteRecord(storage.RecL1Hyperedge, id, data)
	return err
}
