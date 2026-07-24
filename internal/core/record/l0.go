// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package record

import (
	"encoding/json"
	"fmt"

	"github.com/qyiun666/MemHop/internal/core/model"
	"github.com/qyiun666/MemHop/internal/core/storage"
)

// ReadProfileSlot reads and deserializes a ProfileSlot from the storage engine.
func ReadProfileSlot(engine *storage.StorageEngine, id uint64) (*model.ProfileSlot, error) {
	_, data, err := engine.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	var slot model.ProfileSlot
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, fmt.Errorf("unmarshal ProfileSlot: %w", err)
	}
	return &slot, nil
}

// WriteProfileSlot serializes and writes a ProfileSlot to the storage engine.
func WriteProfileSlot(engine *storage.StorageEngine, id uint64, slot *model.ProfileSlot) error {
	data, err := json.Marshal(slot)
	if err != nil {
		return fmt.Errorf("marshal ProfileSlot: %w", err)
	}
	_, err = engine.WriteRecord(storage.RecL0Profile, id, data)
	return err
}
