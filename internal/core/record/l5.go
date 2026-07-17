// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package record

import (
	"encoding/json"
	"fmt"

	"memhop/internal/core/model"
	"memhop/internal/core/storage"
)

// ReadActionChainSlot reads and deserializes an ActionChainSlot from the storage engine.
func ReadActionChainSlot(engine *storage.StorageEngine, id uint64) (*model.ActionChainSlot, error) {
	_, data, err := engine.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	var slot model.ActionChainSlot
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, fmt.Errorf("unmarshal ActionChainSlot: %w", err)
	}
	return &slot, nil
}

// WriteActionChainSlot serializes and writes an ActionChainSlot to the storage engine.
func WriteActionChainSlot(engine *storage.StorageEngine, id uint64, slot *model.ActionChainSlot) error {
	data, err := json.Marshal(slot)
	if err != nil {
		return fmt.Errorf("marshal ActionChainSlot: %w", err)
	}
	_, err = engine.WriteRecord(storage.RecL5ActionChain, id, data)
	return err
}

// ReadActionStep reads and deserializes an ActionStep from the storage engine.
func ReadActionStep(engine *storage.StorageEngine, id uint64) (*model.ActionStep, error) {
	_, data, err := engine.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	var step model.ActionStep
	if err := json.Unmarshal(data, &step); err != nil {
		return nil, fmt.Errorf("unmarshal ActionStep: %w", err)
	}
	return &step, nil
}

// WriteActionStep serializes and writes an ActionStep to the storage engine.
func WriteActionStep(engine *storage.StorageEngine, id uint64, step *model.ActionStep) error {
	data, err := json.Marshal(step)
	if err != nil {
		return fmt.Errorf("marshal ActionStep: %w", err)
	}
	_, err = engine.WriteRecord(storage.RecL5ActionStep, id, data)
	return err
}
