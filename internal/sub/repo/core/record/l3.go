// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package record

import (
	"encoding/json"
	"fmt"

	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
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

// CollectAllGraphSlots iterates the engine index and loads every L3 HypergraphSlot.
func CollectAllGraphSlots(engine *storage.StorageEngine) []model.HypergraphSlot {
	var all []model.HypergraphSlot
	_ = engine.IterIndexByType(storage.RecL3GraphSlot, func(idHash uint64) error {
		slot, err := ReadGraphSlot(engine, idHash)
		if err != nil {
			return nil // 单条损坏/解析失败不影响整体遍历
		}
		all = append(all, *slot)
		return nil
	})
	return all
}

// CollectAllHypergraphNodes iterates the engine index and loads every L3 HypergraphNode.
func CollectAllHypergraphNodes(engine *storage.StorageEngine) []model.HypergraphNode {
	var all []model.HypergraphNode
	_ = engine.IterIndexByType(storage.RecL3GraphNode, func(idHash uint64) error {
		node, err := ReadHypergraphNode(engine, idHash)
		if err != nil {
			return nil // 单条损坏/解析失败不影响整体遍历
		}
		all = append(all, *node)
		return nil
	})
	return all
}

// CollectAllHypergraphEdges iterates the engine index and loads every L3 HypergraphEdge.
func CollectAllHypergraphEdges(engine *storage.StorageEngine) []model.HypergraphEdge {
	var all []model.HypergraphEdge
	_ = engine.IterIndexByType(storage.RecL3GraphEdge, func(idHash uint64) error {
		edge, err := ReadHypergraphEdge(engine, idHash)
		if err != nil {
			return nil // 单条损坏/解析失败不影响整体遍历
		}
		all = append(all, *edge)
		return nil
	})
	return all
}
