// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package record

import (
	"encoding/json"
	"fmt"

	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// ReadArchiveSlot reads and deserializes an ArchiveSlot from the storage engine.
func ReadArchiveSlot(engine *storage.StorageEngine, id uint64) (*model.ArchiveSlot, error) {
	_, data, err := engine.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	var slot model.ArchiveSlot
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, fmt.Errorf("unmarshal ArchiveSlot: %w", err)
	}
	return &slot, nil
}

// WriteArchiveSlot serializes and writes an ArchiveSlot to the storage engine.
func WriteArchiveSlot(engine *storage.StorageEngine, id uint64, slot *model.ArchiveSlot) error {
	data, err := json.Marshal(slot)
	if err != nil {
		return fmt.Errorf("marshal ArchiveSlot: %w", err)
	}
	_, err = engine.WriteRecord(storage.RecL4Archive, id, data)
	return err
}

// CollectAllArchives iterates the engine index and loads every L4 ArchiveSlot.
func CollectAllArchives(engine *storage.StorageEngine) []model.ArchiveSlot {
	var all []model.ArchiveSlot
	_ = engine.IterIndexByType(storage.RecL4Archive, func(idHash uint64) error {
		arc, err := ReadArchiveSlot(engine, idHash)
		if err != nil {
			return nil // 单条损坏/解析失败不影响整体遍历
		}
		all = append(all, *arc)
		return nil
	})
	return all
}
