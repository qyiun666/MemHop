package repo

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// MaxDepth 话题下沉后触发删除的深度阈值。
const MaxDepth = 4

// CompressResult 是压缩聚合结果。
type CompressResult struct {
	L3Refs         []uint64 // L3 合体去重后的引用列表
	UserTimestamp  int64    // 最早的用户时间戳
	AgentTimestamp int64    // 最晚的 agent 时间戳
}

// CompressTopicsL2 压缩L2场景下的话题
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
			continue // 记录不存在或非话题类型：跳过
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

// DeleteL2 批量删除。num==1 时 ids 是场景 id 列表：遍历所有话题删除 SceneID
func DeleteL2(engine *core.StorageEngine, ids []string, num uint8) bool {
	hashes, ok := common.ParseAll(ids)
	if !ok {
		return false
	}
	var targets []uint64
	switch num {
	case 1: // 场景
		sceneSet := common.ToSet(hashes)
		for _, topic := range core.CollectAllTopics(engine) {
			if _, ok := sceneSet[topic.SceneID]; ok {
				targets = append(targets, topic.ID)
			}
		}
		targets = append(targets, hashes...) // 场景记录本身
	case 2: // 话题
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

// MergeScenesL2 遍历所有话题：SceneID 属于副场景 id 列表的，改写为主场景 id
// 并收集，遍历结束后一次性批量写回；随后复用 DeleteL2 的场景模式删除副场景
// （此时副场景下已无话题，只删场景记录本身）。成功返回 true。
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

// ListScenesL2 按场景 id 列表查询场景，不存在的场景跳过。
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

// CreateSceneL2 新增场景：ID 由场景名哈希生成并写入文件，返回场景 ID。
func CreateSceneL2(engine *core.StorageEngine, name string) (uint64, error) {
	slot := core.NewSceneSlot(name)
	if err := core.WriteSceneSlot(engine, slot.SceneID, &slot); err != nil {
		return 0, err
	}
	return slot.SceneID, nil
}

// CollectAllScenesL2 返回全部场景槽，按记录扫描顺序。
func CollectAllScenesL2(engine *core.StorageEngine) []core.SceneSlot {
	var out []core.SceneSlot
	engine.IterIndexByType(core.RecL2Scene, func(idHash uint64) error {
		slot, err := core.ReadSceneSlot(engine, idHash)
		if err != nil {
			return nil
		}
		out = append(out, *slot)
		return nil
	})
	return out
}

// ListTopicsL2 查询话题列表：num==1 返回全部话题（所有场景）中 depth 深度以内
// 的，num==2 返回指定场景中 depth 深度以内的（depth==0 视为 1，超过 MaxDepth
// 截断为 MaxDepth），num==3 按 ID 读取单个话题（sceneID 参数为话题 ID）。
// num==1/2 均按 UserTimestamp 升序返回。
func ListTopicsL2(engine *core.StorageEngine, sceneID string, depth uint8, num uint8) ([]core.TopicSlot, error) {
	var idHash uint64
	var err error
	if depth == 0 {
		depth = 1
	} else if depth > MaxDepth {
		depth = MaxDepth
	}
	if num != 1 {
		idHash, err = common.ParseID(sceneID)
		if err != nil {
			return nil, common.NewError(common.ErrInvalidQuery, "parse topic id", err)
		}
	}
	if num == 3 {
		return core.ReadTopicSlot(engine, idHash)
	}
	var out []core.TopicSlot
	for _, topic := range core.CollectAllTopics(engine) {
		if topic.Depth > depth {
			continue
		}
		if num == 2 && topic.SceneID != idHash {
			continue
		}
		out = append(out, topic)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserTimestamp < out[j].UserTimestamp })
	return out, nil
}

// CreateTopicL2 新增话题：ID 由 ComputeTopicID 生成（sceneID + 双时间戳哈希），
// depth 固定 1，只写 ID/SceneID/Depth/UserKeywords/UserTimestamp，成功返回 true。
func CreateTopicL2(engine *core.StorageEngine, sceneID string, userKeywords []string, userTS int64) bool {
	sceneHash, err := common.ParseID(sceneID)
	if err != nil {
		return false
	}
	topic := core.TopicSlot{
		ID:            core.ComputeTopicID(sceneHash, userTS, 0),
		SceneID:       sceneHash,
		Depth:         1,
		UserKeywords:  userKeywords,
		UserTimestamp: userTS,
	}
	return core.WriteTopicSlot(engine, topic.ID, &topic) == nil
}

// CreateFusedTopicL2 新增话题并附带融合字段：ID 由 ComputeTopicID 生成（sceneID + 双时间戳哈希），
// depth 固定 1，除基础字段外额外写 AgentTimestamp、FusedKeywords（复用 userKeywords）与
// ChildrenIDs，成功返回 true。L3/L4 由 AppendTopicL3RefsL2 / UpdateTopicL4RefsL2 更新。
func CreateFusedTopicL2(engine *core.StorageEngine, sceneID string, userKeywords []string, userTS, agentTS int64, childrenIDs []uint64) bool {
	sceneHash, err := common.ParseID(sceneID)
	if err != nil {
		return false
	}
	topic := core.TopicSlot{
		ID:             core.ComputeTopicID(sceneHash, userTS, agentTS),
		SceneID:        sceneHash,
		Depth:          1,
		UserKeywords:   userKeywords,
		UserTimestamp:  userTS,
		AgentTimestamp: agentTS,
		FusedKeywords:  userKeywords,
		ChildrenIDs:    childrenIDs,
	}
	return core.WriteTopicSlot(engine, topic.ID, &topic) == nil
}

// AppendTopicL3RefsL2 追加 L3 引用到指定话题并去重，成功返回 true。
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

// UpdateTopicL4RefsL2 追加 L4 引用到指定话题并去重，成功返回 true。
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

// UpdateTopicL2 更新指定话题的 AgentKeywords 与 AgentTimestamp，成功返回 true。
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

// UpdateChildrenL2 替换指定话题的 ChildrenIDs，成功返回 true。
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
