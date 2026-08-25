package repo

import (
	"cmp"
	"encoding/json"
	"math"
	"slices"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// MaxDepth: topic depth threshold that triggers deletion on sinking.
const MaxDepth = 4

type CompressResult struct {
	L3Refs         []uint64 // deduplicated merged L3 refs
	UserTimestamp  int64    // earliest user timestamp
	AgentTimestamp int64    // latest agent timestamp
}

// CompressTopicsL2 compresses topics under a scene.
func CompressTopicsL2(engine *core.StorageEngine, ids []uint64, parentID uint64) (*CompressResult, error) {
	result := &CompressResult{
		UserTimestamp:  math.MaxInt64,
		AgentTimestamp: math.MinInt64,
	}
	var writes []core.RecordEntry
	var deletes []uint64
	for _, id := range ids {
		topic, err := core.ReadTopicLenient(engine, id)
		if err != nil || topic == nil {
			continue // skip missing or non-topic records
		}
		topic.Depth++
		topic.ParentID = &parentID

		result.L3Refs = append(result.L3Refs, topic.L3Refs...)
		if topic.UserTimestamp < result.UserTimestamp {
			result.UserTimestamp = topic.UserTimestamp
		}
		if topic.AgentTimestamp > result.AgentTimestamp {
			result.AgentTimestamp = topic.AgentTimestamp
		}

		if topic.Depth >= MaxDepth {
			deletes = append(deletes, topic.ID)
			continue
		}
		data, err := json.Marshal(topic)
		if err != nil {
			return result, err
		}
		writes = append(writes, core.RecordEntry{
			RecordType: core.RecL2Topic,
			IDHash:     topic.ID,
			Data:       data,
		})
	}
	if len(writes) > 0 {
		if _, err := engine.WriteRecordBatch(writes); err != nil {
			return result, err
		}
	}
	if len(deletes) > 0 {
		if _, err := engine.DeleteRecordBatch(deletes); err != nil {
			return result, err
		}
	}
	if result.UserTimestamp == math.MaxInt64 {
		result.UserTimestamp = 0
	}
	if result.AgentTimestamp == math.MinInt64 {
		result.AgentTimestamp = 0
	}
	result.L3Refs = common.DedupSorted(result.L3Refs)
	return result, nil
}

// DeleteL2 batch-deletes: num==1 treats ids as scene IDs (all topics of the
// scene plus the scene record itself); num==2 treats them as topic IDs.
func DeleteL2(engine *core.StorageEngine, ids []string, num uint8) bool {
	hashes, ok := common.ParseAll(ids)
	if !ok {
		return false
	}
	var targets []uint64
	switch num {
	case 1: // scenes
		sceneSet := common.ToSet(hashes)
		for _, topic := range core.CollectAllTopics(engine) {
			if _, ok := sceneSet[topic.SceneID]; ok {
				targets = append(targets, topic.ID)
			}
		}
		targets = append(targets, hashes...) // the scene records themselves
	case 2: // topics
		idSet := common.ToSet(hashes)
		for _, topic := range core.CollectAllTopics(engine) {
			if _, ok := idSet[topic.ID]; ok {
				targets = append(targets, topic.ID)
			}
		}
	default:
		return false
	}
	_, err := engine.DeleteRecordBatch(targets)
	return err == nil
}

// MergeScenesL2 rewrites topics of the secondary scenes to the primary
// scene in one batch, then deletes the secondary scene records (now empty).
func MergeScenesL2(engine *core.StorageEngine, primaryID string, secondaryIDs []string) bool {
	primaryHash, err := common.ParseID(primaryID)
	if err != nil {
		return false
	}
	secondaryHashes, ok := common.ParseAll(secondaryIDs)
	if !ok {
		return false
	}
	secondarySet := common.ToSet(secondaryHashes)
	var writes []core.RecordEntry
	for _, topic := range core.CollectAllTopics(engine) {
		if _, ok := secondarySet[topic.SceneID]; !ok {
			continue
		}
		topic.SceneID = primaryHash
		data, err := json.Marshal(&topic)
		if err != nil {
			return false
		}
		writes = append(writes, core.RecordEntry{
			RecordType: core.RecL2Topic,
			IDHash:     topic.ID,
			Data:       data,
		})
	}
	if len(writes) > 0 {
		if _, err := engine.WriteRecordBatch(writes); err != nil {
			return false
		}
	}
	return DeleteL2(engine, secondaryIDs, 1)
}

// TouchSceneUsage bumps the scene record's retrieval-hit counters (folded
// from the former L6 usage record into SceneSlot). Best-effort by design:
// concurrent hits may lose an increment; Dream only distinguishes
// HitCount == 0, so the impact is nil.
func TouchSceneUsage(engine *core.StorageEngine, sceneID uint64, ts int64) error {
	slot, err := core.ReadSceneSlot(engine, sceneID)
	if err != nil {
		return err
	}
	slot.HitCount++
	slot.LastHitAt = ts
	return core.WriteSceneSlot(engine, sceneID, slot)
}

func ListScenesL2(engine *core.StorageEngine, ids []string) []core.SceneSlot {
	var out []core.SceneSlot
	for _, id := range ids {
		sceneHash, err := common.ParseID(id)
		if err != nil {
			continue
		}
		slot, err := core.ReadSceneSlot(engine, sceneHash)
		if err != nil {
			continue
		}
		out = append(out, *slot)
	}
	return out
}

// CreateSceneL2 creates a scene; the ID is the hash of the name.
func CreateSceneL2(engine *core.StorageEngine, name string) (uint64, error) {
	slot := core.NewSceneSlot(name)
	if err := core.WriteSceneSlot(engine, slot.SceneID, &slot); err != nil {
		return 0, err
	}
	return slot.SceneID, nil
}

// CollectAllScenesL2 returns every scene with TopicCount set to the number
// of depth-1 root topics under it (single pass over all topics).
func CollectAllScenesL2(engine *core.StorageEngine) []core.SceneSlot {
	var out []core.SceneSlot
	for idHash := range engine.IndexByType(core.RecL2Scene) {
		slot, err := core.ReadSceneSlot(engine, idHash)
		if err != nil {
			continue
		}
		out = append(out, *slot)
	}
	if len(out) == 0 {
		return out
	}
	counts := make(map[uint64]int, len(out))
	for _, topic := range core.CollectAllTopics(engine) {
		if topic.Depth == 1 {
			counts[topic.SceneID]++
		}
	}
	for i := range out {
		out[i].TopicCount = counts[out[i].SceneID]
	}
	return out
}

// TopicListQuery carries ListTopicsL2 inputs. MetaIdx is the L2MetaIndex
// cache: modes 1/2 rebuild candidates from it instead of unmarshalling
// every topic record; a nil MetaIdx falls back to the full record scan
// with identical semantics.
type TopicListQuery struct {
	Engine  *core.StorageEngine
	MetaIdx *index.L2MetaIndex
	SceneID string
	Depth   uint8
	Num     uint8
}

// ListTopicsL2 lists topics by mode: 1 = all topics up to depth, 2 = same but
// restricted to sceneID, 3 = one topic by ID. depth is clamped to [1, MaxDepth];
// results sorted by UserTimestamp for modes 1/2.
func ListTopicsL2(q TopicListQuery) ([]core.TopicSlot, error) {
	var idHash uint64
	var err error
	depth := q.Depth
	if depth == 0 {
		depth = 1
	} else if depth > MaxDepth {
		depth = MaxDepth
	}
	if q.Num != 1 {
		idHash, err = common.ParseID(q.SceneID)
		if err != nil {
			return nil, common.NewError(common.ErrInvalidQuery, "parse topic id", err)
		}
	}
	if q.Num == 3 {
		return core.ReadTopicSlot(q.Engine, idHash)
	}
	var out []core.TopicSlot
	if q.MetaIdx != nil {
		q.MetaIdx.Iter(func(_ uint64, meta *index.L2Meta) bool {
			if meta.Depth > depth {
				return true
			}
			if q.Num == 2 && meta.SceneID != idHash {
				return true
			}
			out = append(out, meta.ToTopicSlot())
			return true
		})
	} else {
		for _, topic := range core.CollectAllTopics(q.Engine) {
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
func WriteVecCentroid(engine *core.StorageEngine, vec []float32) (uint64, error) {
	if len(vec) == 0 {
		return 0, common.NewError(common.ErrInvalidQuery, "empty centroid vector", nil)
	}
	data := common.F32SliceToBytes(vec)
	idHash := common.HashID(string(data))
	if _, err := engine.WriteRecord(core.RecVecCentroid, idHash, data); err != nil {
		return 0, err
	}
	return idHash, nil
}

// CreateTopicL2 creates a topic (ID from ComputeTopicID, depth 1);
// centroidRef 0 means no vector.
func CreateTopicL2(engine *core.StorageEngine, sceneID string, userKeywords []string, userTS int64, centroidRef uint64) bool {
	sceneHash, err := common.ParseID(sceneID)
	if err != nil {
		return false
	}
	return CreateTopicL2WithID(engine, sceneHash, core.ComputeTopicID(sceneHash, userTS, 0),
		userKeywords, userTS, centroidRef)
}

// CreateTopicL2WithID writes a topic with a caller-derived ID. Search uses
// ComputeTopicIDForText so same-scene/same-millisecond messages with
// different content no longer overwrite each other.
func CreateTopicL2WithID(engine *core.StorageEngine, sceneHash uint64, topicID uint64, userKeywords []string, userTS int64, centroidRef uint64) bool {
	topic := core.TopicSlot{
		ID:              topicID,
		SceneID:         sceneHash,
		Depth:           1,
		UserKeywords:    userKeywords,
		UserTimestamp:   userTS,
		CentroidPageRef: centroidRef,
	}
	return core.WriteTopicSlot(engine, topic.ID, &topic) == nil
}

// CreateFusedTopicL2 creates a compressed topic (ID from ComputeTopicID,
// depth 1): only FusedKeywords carry values; L3/L4 refs are added via
// AppendTopicL3RefsL2 / UpdateTopicL4RefsL2.
func CreateFusedTopicL2(engine *core.StorageEngine, sceneID string, fusedKeywords []string, userTS, agentTS int64, childrenIDs []uint64, centroidRef uint64) bool {
	sceneHash, err := common.ParseID(sceneID)
	if err != nil {
		return false
	}
	topic := core.TopicSlot{
		ID:              core.ComputeTopicID(sceneHash, userTS, agentTS),
		SceneID:         sceneHash,
		Depth:           1,
		UserTimestamp:   userTS,
		AgentTimestamp:  agentTS,
		FusedKeywords:   fusedKeywords,
		ChildrenIDs:     childrenIDs,
		CentroidPageRef: centroidRef,
	}
	return core.WriteTopicSlot(engine, topic.ID, &topic) == nil
}

func AppendTopicL3RefsL2(engine *core.StorageEngine, id string, l3Refs []uint64) bool {
	idHash, err := common.ParseID(id)
	if err != nil {
		return false
	}
	topic, err := core.ReadTopicLenient(engine, idHash)
	if err != nil || topic == nil {
		return false
	}
	topic.L3Refs = common.DedupSorted(append(topic.L3Refs, l3Refs...))
	return core.WriteTopicSlot(engine, idHash, topic) == nil
}

func UpdateTopicL4RefsL2(engine *core.StorageEngine, id string, l4Refs []uint64) bool {
	idHash, err := common.ParseID(id)
	if err != nil {
		return false
	}
	topic, err := core.ReadTopicLenient(engine, idHash)
	if err != nil || topic == nil {
		return false
	}
	topic.L4Refs = common.DedupSorted(append(topic.L4Refs, l4Refs...))
	return core.WriteTopicSlot(engine, idHash, topic) == nil
}

func UpdateTopicL2(engine *core.StorageEngine, id string, agentKeywords []string, agentTS int64) bool {
	idHash, err := common.ParseID(id)
	if err != nil {
		return false
	}
	topic, err := core.ReadTopicLenient(engine, idHash)
	if err != nil || topic == nil {
		return false
	}
	topic.AgentKeywords = agentKeywords
	topic.AgentTimestamp = agentTS
	return core.WriteTopicSlot(engine, idHash, topic) == nil
}

// RefineTopicKeywordsL2 replaces the topic's keyword tracks with a fused
// set: FusedKeywords = fused, User/AgentKeywords cleared. Timestamps are
// preserved — Dream grouping (groupTimestamps) relies on them.
func RefineTopicKeywordsL2(engine *core.StorageEngine, id string, fusedKeywords []string) bool {
	idHash, err := common.ParseID(id)
	if err != nil {
		return false
	}
	topic, err := core.ReadTopicLenient(engine, idHash)
	if err != nil || topic == nil {
		return false
	}
	topic.FusedKeywords = fusedKeywords
	topic.UserKeywords = nil
	topic.AgentKeywords = nil
	return core.WriteTopicSlot(engine, idHash, topic) == nil
}

func UpdateChildrenL2(engine *core.StorageEngine, id string, childrenIDs []uint64) bool {
	idHash, err := common.ParseID(id)
	if err != nil {
		return false
	}
	topic, err := core.ReadTopicLenient(engine, idHash)
	if err != nil || topic == nil {
		return false
	}
	topic.ChildrenIDs = childrenIDs
	return core.WriteTopicSlot(engine, idHash, topic) == nil
}

// RecoverDeletedScenesL2 re-appends the pre-delete payload of every L2
// scene record whose newest frame is a tombstone, returning the restored
// scene IDs. Frames reclaimed by compaction cannot be recovered.
func RecoverDeletedScenesL2(engine *core.StorageEngine) ([]uint64, error) {
	payloads, err := engine.ScanDeletedPayloads(core.RecL2Scene)
	if err != nil {
		return nil, err
	}
	if len(payloads) == 0 {
		return nil, nil
	}
	writes := make([]core.RecordEntry, 0, len(payloads))
	ids := make([]uint64, 0, len(payloads))
	for id, data := range payloads {
		writes = append(writes, core.RecordEntry{RecordType: core.RecL2Scene, IDHash: id, Data: data})
		ids = append(ids, id)
	}
	if _, err := engine.WriteRecordBatch(writes); err != nil {
		return nil, err
	}
	return ids, nil
}
