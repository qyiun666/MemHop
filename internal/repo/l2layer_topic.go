// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 topic record primitives: listing, creation, keyword/ref mutation and
// centroid writes. Scene primitives stay in l2layer.go.
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

// WriteVecCentroid writes the topic centroid vector (f32) as a
// RecVecCentroid record and returns its idHash for CentroidPageRef.
func WriteVecCentroid(engine *core.StorageEngine, agentID uint64, vec []float32) (uint64, error) {
	if len(vec) == 0 {
		return 0, common.NewError(common.ErrInvalidQuery, "empty centroid vector", nil)
	}
	data := common.F32SliceToBytes(vec)
	idHash := common.HashBytes(data)
	if _, err := engine.WriteRecord(agentID, core.RecVecCentroid, idHash, data); err != nil {
		return 0, err
	}
	return idHash, nil
}

// CreateTopicL2 creates a topic (ID from ComputeTopicID, depth 1);
// centroidRef 0 means no vector.
func CreateTopicL2(engine *core.StorageEngine, agentID uint64, sceneID uint64, userKeywords []string, userTS int64, centroidRef uint64) bool {
	return CreateTopicL2WithID(engine, agentID, sceneID, core.ComputeTopicID(sceneID, userTS, 0),
		userKeywords, userTS, centroidRef)
}

// CreateTopicL2WithID writes a topic with a caller-derived ID. Search uses
// ComputeTopicIDForText so same-scene/same-millisecond messages with
// different content no longer overwrite each other.
func CreateTopicL2WithID(engine *core.StorageEngine, agentID uint64, sceneHash uint64, topicID uint64, userKeywords []string, userTS int64, centroidRef uint64) bool {
	topic := core.TopicSlot{
		ID:              topicID,
		SceneID:         sceneHash,
		Depth:           1,
		UserKeywords:    userKeywords,
		UserTimestamp:   userTS,
		CentroidPageRef: centroidRef,
	}
	return core.WriteTopicSlot(engine, agentID, topic.ID, &topic) == nil
}

// CreateFusedTopicL2 creates a compressed topic (ID from ComputeTopicID,
// depth 1): only FusedKeywords carry values; L3/L4 refs are added via
// AppendTopicL3RefsL2 / UpdateTopicL4RefsL2.
func CreateFusedTopicL2(engine *core.StorageEngine, agentID uint64, sceneID uint64, fusedKeywords []string, userTS, agentTS int64, childrenIDs []uint64, centroidRef uint64) bool {
	topic := core.TopicSlot{
		ID:              core.ComputeTopicID(sceneID, userTS, agentTS),
		SceneID:         sceneID,
		Depth:           1,
		UserTimestamp:   userTS,
		AgentTimestamp:  agentTS,
		FusedKeywords:   fusedKeywords,
		ChildrenIDs:     childrenIDs,
		CentroidPageRef: centroidRef,
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

func AppendTopicL3RefsL2(engine *core.StorageEngine, agentID uint64, id uint64, l3Refs []uint64) bool {
	return mutateTopic(engine, agentID, id, func(t *core.TopicSlot) {
		t.L3Refs = common.DedupSorted(append(t.L3Refs, l3Refs...))
	})
}

func UpdateTopicL4RefsL2(engine *core.StorageEngine, agentID uint64, id uint64, l4Refs []uint64) bool {
	return mutateTopic(engine, agentID, id, func(t *core.TopicSlot) {
		t.L4Refs = common.DedupSorted(append(t.L4Refs, l4Refs...))
	})
}

func UpdateTopicL2(engine *core.StorageEngine, agentID uint64, id uint64, agentKeywords []string, agentTS int64) bool {
	return mutateTopic(engine, agentID, id, func(t *core.TopicSlot) {
		t.AgentKeywords = agentKeywords
		t.AgentTimestamp = agentTS
	})
}

// RefineTopicKeywordsL2 replaces the topic's keyword tracks with a fused
// set: FusedKeywords = fused, User/AgentKeywords cleared. Timestamps are
// preserved — Dream grouping (groupTimestamps) relies on them.
func RefineTopicKeywordsL2(engine *core.StorageEngine, agentID uint64, id uint64, fusedKeywords []string) bool {
	return mutateTopic(engine, agentID, id, func(t *core.TopicSlot) {
		t.FusedKeywords = fusedKeywords
		t.UserKeywords = nil
		t.AgentKeywords = nil
	})
}

func UpdateChildrenL2(engine *core.StorageEngine, agentID uint64, id uint64, childrenIDs []uint64) bool {
	return mutateTopic(engine, agentID, id, func(t *core.TopicSlot) {
		t.ChildrenIDs = childrenIDs
	})
}
