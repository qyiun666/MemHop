// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package record

import (
	"encoding/json"
	"fmt"

	"github.com/qyiun666/MemHop/internal/core/model"
	"github.com/qyiun666/MemHop/internal/core/storage"
)

// ReadTopicSlot reads and deserializes a TopicSlot from the storage engine.
func ReadTopicSlot(engine *storage.StorageEngine, id uint64) (*model.TopicSlot, error) {
	_, data, err := engine.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	var slot model.TopicSlot
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, fmt.Errorf("unmarshal TopicSlot: %w", err)
	}
	return &slot, nil
}

// WriteTopicSlot serializes and writes a TopicSlot to the storage engine.
func WriteTopicSlot(engine *storage.StorageEngine, id uint64, slot *model.TopicSlot) error {
	data, err := json.Marshal(slot)
	if err != nil {
		return fmt.Errorf("marshal TopicSlot: %w", err)
	}
	_, err = engine.WriteRecord(storage.RecL2Topic, id, data)
	return err
}
