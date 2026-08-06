// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// SceneSlot 持久化读写原语：L2 场景容器记录（RecL2Scene）。
package record

import (
	"encoding/json"
	"fmt"

	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// ReadSceneSlot reads and deserializes a SceneSlot from the storage engine.
func ReadSceneSlot(engine *storage.StorageEngine, id uint64) (*model.SceneSlot, error) {
	rt, data, err := engine.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	if rt != storage.RecL2Scene {
		return nil, mherrors.ErrNotFound
	}
	var slot model.SceneSlot
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, fmt.Errorf("unmarshal SceneSlot: %w", err)
	}
	return &slot, nil
}

// WriteSceneSlot serializes and writes a SceneSlot to the storage engine.
func WriteSceneSlot(engine *storage.StorageEngine, id uint64, slot *model.SceneSlot) error {
	data, err := json.Marshal(slot)
	if err != nil {
		return fmt.Errorf("marshal SceneSlot: %w", err)
	}
	_, err = engine.WriteRecord(storage.RecL2Scene, id, data)
	return err
}
