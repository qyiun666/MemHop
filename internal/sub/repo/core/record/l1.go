// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package record provides typed Read/Write helpers for all storage record types.
package record

import (
	"encoding/json"
	"fmt"

	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
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

// CollectAllSceneNodes iterates the engine index and loads every L1 SceneNode.
func CollectAllSceneNodes(engine *storage.StorageEngine) []model.SceneNode {
	var all []model.SceneNode
	_ = engine.IterIndexByType(storage.RecL1SceneNode, func(idHash uint64) error {
		node, err := ReadSceneNode(engine, idHash)
		if err != nil {
			return nil // 单条损坏/解析失败不影响整体遍历
		}
		all = append(all, *node)
		return nil
	})
	return all
}
