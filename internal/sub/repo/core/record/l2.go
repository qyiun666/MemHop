// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// TopicSlot 持久化读写原语：所有对 TopicSlot 的 ReadRecord/WriteRecord
// 直连调用都应改走本包。
package record

import (
	"encoding/json"
	"fmt"

	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
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

// readTopicChecked reads a TopicSlot only when the stored record type is
// RecL2Topic; other record types are skipped. This preserves the type-filter
// semantics of the original crud collectAllTopics/loadTopicsByIDs.
func readTopicChecked(engine *storage.StorageEngine, id uint64) (*model.TopicSlot, error) {
	rt, data, err := engine.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	if rt != storage.RecL2Topic {
		return nil, mherrors.ErrNotFound
	}
	var slot model.TopicSlot
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, fmt.Errorf("unmarshal TopicSlot: %w", err)
	}
	return &slot, nil
}

// CollectAllTopics iterates the engine index and loads every L2 TopicSlot.
func CollectAllTopics(engine *storage.StorageEngine) []model.TopicSlot {
	var all []model.TopicSlot
	engine.IterIndex(func(idHash, _ uint64) bool {
		topic, err := readTopicChecked(engine, idHash)
		if err != nil {
			return true
		}
		all = append(all, *topic)
		return true
	})
	return all
}

// LoadTopicsByIDs loads the L2 TopicSlots for the given IDs, skipping misses.
func LoadTopicsByIDs(engine *storage.StorageEngine, ids []uint64) []model.TopicSlot {
	var nodes []model.TopicSlot
	for _, id := range ids {
		topic, err := readTopicChecked(engine, id)
		if err != nil {
			continue
		}
		nodes = append(nodes, *topic)
	}
	return nodes
}

// ReadTopicLenient reads and deserializes a TopicSlot from the engine.
// Unlike ReadTopicSlot it is lenient: a non-RecL2Topic record yields
// (nil, nil) instead of unmarshalling garbage.
func ReadTopicLenient(engine *storage.StorageEngine, idHash uint64) (*model.TopicSlot, error) {
	rt, data, err := engine.ReadRecord(idHash)
	if err != nil {
		return nil, err
	}
	if rt != storage.RecL2Topic {
		return nil, nil
	}
	var topic model.TopicSlot
	if err := json.Unmarshal(data, &topic); err != nil {
		return nil, err
	}
	return &topic, nil
}
