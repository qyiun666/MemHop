// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 topic record primitives: listing, creation and the read-modify-write
// mutations of the keyword track, L4 refs and tree links. Scene primitives
// stay in l2layer.go.
package repo

import (
	"cmp"
	"slices"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// TopicListQuery carries ListTopicsL2 inputs. MetaIdx is the L2MetaIndex
// cache: modes 1/2 rebuild candidates from it instead of unmarshalling
// every topic record; a nil MetaIdx falls back to the full record scan
// with identical semantics.
type TopicListQuery struct {
	Engine  *core.StorageEngine
	AgentID uint64
	MetaIdx *index.L2MetaIndex
	SceneID uint64
	Depth   uint8
	Num     uint8
}

// ListTopicsL2 lists topics by mode: 1 = all topics up to depth, 2 = same but
// restricted to sceneID, 3 = one topic by ID. depth is clamped to [1, MaxDepth];
// results sorted by UserTimestamp for modes 1/2.
func ListTopicsL2(q TopicListQuery) ([]core.TopicSlot, error) {
	var idHash uint64
	depth := q.Depth
	if depth == 0 {
		depth = 1
	} else if depth > MaxDepth {
		depth = MaxDepth
	}
	if q.Num != 1 {
		idHash = q.SceneID
	}
	if q.Num == 3 {
		slot, err := core.ReadTopicSlot(q.Engine, q.AgentID, idHash)
		if err != nil {
			return nil, err
		}
		return []core.TopicSlot{*slot}, nil
	}
	var out []core.TopicSlot
	if q.MetaIdx != nil {
		for _, meta := range q.MetaIdx.Iter() {
			if meta.Depth > depth {
				continue
			}
			if q.Num == 2 && meta.SceneID != idHash {
				continue
			}
			out = append(out, meta.ToTopicSlot())
		}
	} else {
		for _, topic := range core.CollectAllTopics(q.Engine, q.AgentID) {
			if topic.Depth > depth {
				continue
			}
			if q.Num == 2 && topic.SceneID != idHash {
				continue
			}
			out = append(out, topic)
		}
	}
	slices.SortFunc(out, func(a, b core.TopicSlot) int {
		return cmp.Compare(a.UserTimestamp, b.UserTimestamp)
	})
	return out, nil
}

// CreateTurnTopicL2 writes one turn topic (depth 1) under sceneHash with its
// single keyword track and both message timestamps.
func CreateTurnTopicL2(engine *core.StorageEngine, agentID uint64, sceneHash, topicID uint64, keywords []string, userTS, agentTS int64) bool {
	topic := core.TopicSlot{
		ID:             topicID,
		SceneID:        sceneHash,
		Depth:          1,
		FusedKeywords:  keywords,
		UserTimestamp:  userTS,
		AgentTimestamp: agentTS,
	}
	return core.WriteTopicSlot(engine, agentID, topic.ID, &topic) == nil
}

// CreateFusedTopicL2 creates a compressed topic (depth 1) whose Keywords are
// the fusion of its children; L4 refs are added via UpdateTopicL4RefsL2.
func CreateFusedTopicL2(engine *core.StorageEngine, agentID uint64, sceneID uint64, fusedKeywords []string, userTS, agentTS int64, childrenIDs []uint64) bool {
	topic := core.TopicSlot{
		ID:             core.ComputeTopicID(sceneID, userTS, agentTS),
		SceneID:        sceneID,
		Depth:          1,
		UserTimestamp:  userTS,
		AgentTimestamp: agentTS,
		FusedKeywords:  fusedKeywords,
		ChildrenIDs:    childrenIDs,
	}
	return core.WriteTopicSlot(engine, agentID, topic.ID, &topic) == nil
}

// mutateTopic is the shared read-modify-write template for L2 topic fields:
// leniently read the record by idHash, apply mutate, write back. Returns
// false when the record is unreadable or the write fails.
func mutateTopic(
	engine *core.StorageEngine, agentID uint64, idHash uint64,
	mutate func(*core.TopicSlot),
) bool {
	topic, err := core.ReadTopicLenient(engine, agentID, idHash)
	if err != nil || topic == nil {
		return false
	}
	mutate(topic)
	return core.WriteTopicSlot(engine, agentID, idHash, topic) == nil
}

func UpdateTopicL4RefsL2(engine *core.StorageEngine, agentID uint64, id uint64, l4Refs []uint64) bool {
	return mutateTopic(engine, agentID, id, func(t *core.TopicSlot) {
		t.L4Refs = common.DedupSorted(append(t.L4Refs, l4Refs...))
	})
}
