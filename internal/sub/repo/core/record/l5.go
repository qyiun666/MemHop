// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package record

import (
	"encoding/json"
	"fmt"

	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
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

// CollectAllActionChains iterates the engine index and loads every L5 ActionChainSlot.
func CollectAllActionChains(engine *storage.StorageEngine) []model.ActionChainSlot {
	var all []model.ActionChainSlot
	_ = engine.IterIndexByType(storage.RecL5ActionChain, func(idHash uint64) error {
		chain, err := ReadActionChainSlot(engine, idHash)
		if err != nil {
			return nil // 单条损坏/解析失败不影响整体遍历
		}
		all = append(all, *chain)
		return nil
	})
	return all
}

// CollectAllActionSteps iterates the engine index and loads every L5 ActionStep.
func CollectAllActionSteps(engine *storage.StorageEngine) []model.ActionStep {
	var all []model.ActionStep
	_ = engine.IterIndexByType(storage.RecL5ActionStep, func(idHash uint64) error {
		step, err := ReadActionStep(engine, idHash)
		if err != nil {
			return nil // 单条损坏/解析失败不影响整体遍历
		}
		all = append(all, *step)
		return nil
	})
	return all
}
