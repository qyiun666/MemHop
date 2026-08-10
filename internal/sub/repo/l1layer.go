package repo

// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 超级图操作：SceneNode 写盘 + L1ReverseIndex 索引同步一体化。
// dream 通过新的 L2 depth≤2 话题调 CreateNodeL1/UpdateNodeL1 更新 L1；
// search 调 FindAssociatedNodesL1 按选中场景找关联上下文。
import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
	"github.com/qyiun666/MemHop/internal/sub/repo/index"
)

// CreateNodeL1 新建 L1 节点：写入 SceneNode 记录并注册到 L1 反查索引，
// 返回节点 ID。ID = hash(sceneID:topics)。
func CreateNodeL1(engine *core.StorageEngine, l1Idx *index.L1ReverseIndex, sceneID string, topicIDs []uint64) (uint64, error) {
	sceneHash, err := common.ParseID(sceneID)
	if err != nil {
		return 0, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	// ID = hash(sceneID:topics)，与 spec 公式一致
	nodeID := common.HashID(fmt.Sprintf("%s:%v", sceneID, topicIDs))
	now := time.Now().UnixMilli()
	node := &core.SceneNode{
		IDHash:    nodeID,
		SceneID:   sceneHash,
		TopicIDs:  topicIDs,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := core.WriteSceneNode(engine, nodeID, node); err != nil {
		return 0, err
	}
	l1Idx.Add(sceneHash, nodeID)
	return nodeID, nil
}

// UpdateNodeL1 全量覆盖写回节点（ID 以参数为准）并同步反查索引：
// 先从所有场景移除旧注册，再按新 SceneID 注册。
func UpdateNodeL1(engine *core.StorageEngine, l1Idx *index.L1ReverseIndex, id string, slot *core.SceneNode) error {
	idHash, err := common.ParseID(id)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse node id", err)
	}
	if _, err := core.ReadSceneNode(engine, idHash); err != nil {
		return err
	}
	slot.IDHash = idHash
	slot.UpdatedAt = time.Now().UnixMilli()
	if err := core.WriteSceneNode(engine, idHash, slot); err != nil {
		return err
	}
	l1Idx.RemoveNode(idHash)
	l1Idx.Add(slot.SceneID, idHash)
	return nil
}

// RebuildIndexL1 全量扫盘重建 L1 反查索引（dream 压缩后可整体刷新）。
func RebuildIndexL1(engine *core.StorageEngine) *index.L1ReverseIndex {
	return index.BuildL1ReverseIndex(engine)
}

// ListNodesL1 按场景查询节点（nil 表示全部）。
func ListNodesL1(engine *core.StorageEngine, sceneID *string) []core.SceneNode {
	var sceneHash uint64
	filter := false
	if sceneID != nil {
		h, err := common.ParseID(*sceneID)
		if err != nil {
			return nil
		}
		sceneHash = h
		filter = true
	}
	var out []core.SceneNode
	for _, node := range core.CollectAllSceneNodes(engine) {
		if filter && node.SceneID != sceneHash {
			continue
		}
		out = append(out, node)
	}
	return out
}

// FindAssociatedNodesL1 根据选中的场景列表通过 L1 反查索引找关联节点，
// 返回节点记录（上层取 node.TopicIDs 即关联上下文）。
func FindAssociatedNodesL1(engine *core.StorageEngine, l1Idx *index.L1ReverseIndex, sceneIDs []string) []core.SceneNode {
	ctxSet := make(map[uint64]struct{}, len(sceneIDs))
	for _, sid := range sceneIDs {
		h, err := common.ParseID(sid)
		if err != nil {
			continue
		}
		ctxSet[h] = struct{}{}
	}
	var out []core.SceneNode
	for _, nodeID := range l1Idx.FindAssociated(ctxSet) {
		node, err := core.ReadSceneNode(engine, nodeID)
		if err != nil {
			continue
		}
		out = append(out, *node)
	}
	return out
}

// ============================================================================
// Dream 辅助：L1 重建与衰减
// ============================================================================

// DecayParams 是 L1 时间衰减的参数集，由调用方从引擎配置映射。
type DecayParams struct {
	LambdaNode             float64
	LambdaEdge             float64
	NodeRemoveThreshold    float32
	NodePruneEdgeThreshold float32
	EdgeRemoveThreshold    float32
	MinEdgeNodes           int
}

// L1DecayReport 记录一次 L1 衰减的结果。
type L1DecayReport struct {
	DecayedNodes int
	PrunedEdges  int
	RemovedNodes int
	RemovedEdges int
}

// RebuildL1FromL2 清理 L1 中的 stale 节点：TopicIDs 为空、首话题已删除、
// 或话题深度过深且不满足保留条件的节点连同其边引用一并删除。
// 返回被删除节点的 hex ID 列表。
func RebuildL1FromL2(engine *core.StorageEngine, l2Meta *index.L2MetaIndex, cfg *DecayParams) ([]string, error) {
	var updated []string
	for _, node := range core.CollectAllSceneNodes(engine) {
		if !isNodeStale(&node, engine, l2Meta) {
			continue
		}
		for _, edgeID := range node.EdgeIDs {
			if _, err := removeNodeFromEdge(engine, edgeID, node.IDHash, cfg); err != nil {
				return updated, err
			}
		}
		if _, err := engine.DeleteRecord(node.IDHash); err != nil {
			return updated, err
		}
		updated = append(updated, common.FormatHash(node.IDHash))
	}
	return updated, nil
}

func isNodeStale(node *core.SceneNode, engine *core.StorageEngine, l2Meta *index.L2MetaIndex) bool {
	if len(node.TopicIDs) == 0 {
		return true
	}
	firstID := node.TopicIDs[0]
	if firstID == 0 || !engine.Contains(firstID) {
		return true
	}
	meta := l2Meta.Get(firstID)
	if meta == nil {
		return false
	}
	if meta.Depth <= 2 {
		return false
	}
	return !keepDeepNode(node, firstID, meta, engine, l2Meta)
}

// keepDeepNode 深度 3 且父话题深度 <=2 的节点保留（父话题仍可检索时
// 其压缩组节点保持可见）。
func keepDeepNode(node *core.SceneNode, topicID uint64, meta *index.L2Meta, engine *core.StorageEngine, l2Meta *index.L2MetaIndex) bool {
	if meta.Depth != 3 {
		return false
	}
	topic, err := core.ReadTopicLenient(engine, topicID)
	if err != nil || topic == nil || topic.ParentID == nil {
		return false
	}
	parentMeta := l2Meta.Get(*topic.ParentID)
	return parentMeta != nil && parentMeta.Depth <= 2
}

// DecayL1Network 按时间指数衰减 L1 节点与边的权重：先衰减节点（低于阈值
// 删除、低于剪枝阈值清空边），再传播清理被清的边，最后衰减剩余边。
func DecayL1Network(engine *core.StorageEngine, l2Meta *index.L2MetaIndex, cfg *DecayParams) (*L1DecayReport, error) {
	nowMs := time.Now().UnixMilli()
	report := &L1DecayReport{}
	removedNodeIDs, clearedEdges, err := decayNodes(engine, l2Meta, cfg, nowMs, report)
	if err != nil {
		return report, err
	}
	if err := propagateClearedEdges(engine, cfg, clearedEdges, report); err != nil {
		return report, err
	}
	if err := decayRemainingEdges(engine, cfg, removedNodeIDs, nowMs, report); err != nil {
		return report, err
	}
	return report, nil
}

func decayNodes(engine *core.StorageEngine, l2Meta *index.L2MetaIndex, cfg *DecayParams, nowMs int64, report *L1DecayReport) (map[uint64]bool, map[uint64]map[uint64]bool, error) {
	removedNodeIDs := make(map[uint64]bool)
	clearedEdges := make(map[uint64]map[uint64]bool)
	for _, node := range core.CollectAllSceneNodes(engine) {
		if skipDeepNode(&node, l2Meta) {
			continue
		}
		if err := decayOneNode(engine, cfg, &node, nowMs, report, removedNodeIDs, clearedEdges); err != nil {
			return removedNodeIDs, clearedEdges, err
		}
	}
	return removedNodeIDs, clearedEdges, nil
}

// skipDeepNode 深度 >2 的节点由压缩阶段管理，不参与衰减。
func skipDeepNode(node *core.SceneNode, l2Meta *index.L2MetaIndex) bool {
	if len(node.TopicIDs) == 0 {
		return false
	}
	meta := l2Meta.Get(node.TopicIDs[0])
	return meta != nil && meta.Depth > 2
}

func decayOneNode(engine *core.StorageEngine, cfg *DecayParams, node *core.SceneNode, nowMs int64, report *L1DecayReport, removedNodeIDs map[uint64]bool, clearedEdges map[uint64]map[uint64]bool) error {
	dtHours := dtHoursFrom(nowMs, node.UpdatedAt)
	lambda := applyEmotionalBoost(cfg.LambdaNode, node.Valence, node.Arousal)
	newImportance := node.Importance * float32(math.Exp(-lambda*dtHours))
	if newImportance < cfg.NodeRemoveThreshold {
		if _, err := engine.DeleteRecord(node.IDHash); err != nil {
			return err
		}
		removedNodeIDs[node.IDHash] = true
		report.RemovedNodes++
		return nil
	}
	node.Importance = newImportance
	if newImportance < cfg.NodePruneEdgeThreshold {
		report.PrunedEdges += len(node.EdgeIDs)
		for _, edgeID := range node.EdgeIDs {
			if clearedEdges[edgeID] == nil {
				clearedEdges[edgeID] = make(map[uint64]bool)
			}
			clearedEdges[edgeID][node.IDHash] = true
		}
		node.EdgeIDs = nil
	}
	node.UpdatedAt = nowMs
	if err := core.WriteSceneNode(engine, node.IDHash, node); err != nil {
		return err
	}
	report.DecayedNodes++
	return nil
}

func propagateClearedEdges(engine *core.StorageEngine, cfg *DecayParams, clearedEdges map[uint64]map[uint64]bool, report *L1DecayReport) error {
	for edgeID, nodeIDs := range clearedEdges {
		for nodeID := range nodeIDs {
			gone, err := removeNodeFromEdge(engine, edgeID, nodeID, cfg)
			if err != nil {
				return err
			}
			if gone {
				report.RemovedEdges++
			}
		}
	}
	return nil
}

func decayRemainingEdges(engine *core.StorageEngine, cfg *DecayParams, removedNodeIDs map[uint64]bool, nowMs int64, report *L1DecayReport) error {
	var entries []uint64
	_ = engine.IterIndexByType(core.RecL1Hyperedge, func(idHash uint64) error {
		entries = append(entries, idHash)
		return nil
	})
	for _, idHash := range entries {
		edge := readSceneEdge(engine, idHash)
		if edge == nil {
			continue
		}
		if err := decayOneEdge(engine, cfg, edge, idHash, removedNodeIDs, nowMs, report); err != nil {
			return err
		}
	}
	return nil
}

// decayOneEdge 按上次衰减时间增量衰减边权重，并清理指向已删除节点的引用；
// 边不足 MinEdgeNodes 或权重低于阈值时删除整条边。
func decayOneEdge(engine *core.StorageEngine, cfg *DecayParams, edge *core.SceneEdge, idHash uint64, removedNodeIDs map[uint64]bool, nowMs int64, report *L1DecayReport) error {
	baseMs := edge.LastDecayAt
	if baseMs == 0 {
		baseMs = edge.CreatedAt
	}
	dtHours := dtHoursFrom(nowMs, baseMs)
	newWeight := edge.Weight * float32(math.Exp(-cfg.LambdaEdge*dtHours))

	filtered := edge.NodeIDs[:0]
	for _, ptr := range edge.NodeIDs {
		if !removedNodeIDs[ptr] {
			filtered = append(filtered, ptr)
		}
	}
	edge.NodeIDs = filtered

	if len(edge.NodeIDs) < cfg.MinEdgeNodes || newWeight < cfg.EdgeRemoveThreshold {
		for _, nodePtr := range edge.NodeIDs {
			if err := removeEdgeFromNode(engine, nodePtr, idHash); err != nil {
				return err
			}
		}
		if _, err := engine.DeleteRecord(idHash); err != nil {
			return err
		}
		report.RemovedEdges++
		return nil
	}
	edge.Weight = newWeight
	edge.LastDecayAt = nowMs
	return writeSceneEdge(engine, idHash, edge)
}

// removeNodeFromEdge 从边中移除节点引用；边剩余节点不足 MinEdgeNodes 时
// 删除整条边并反清其他节点的边引用，返回是否删边。
func removeNodeFromEdge(engine *core.StorageEngine, edgeID, nodeID uint64, cfg *DecayParams) (bool, error) {
	edge := readSceneEdge(engine, edgeID)
	if edge == nil {
		return false, nil
	}
	found := false
	filtered := edge.NodeIDs[:0]
	for _, n := range edge.NodeIDs {
		if n == nodeID {
			found = true
		} else {
			filtered = append(filtered, n)
		}
	}
	if !found {
		return false, nil
	}
	edge.NodeIDs = filtered
	if len(edge.NodeIDs) < cfg.MinEdgeNodes {
		for _, surviving := range edge.NodeIDs {
			if err := removeEdgeFromNode(engine, surviving, edgeID); err != nil {
				return false, err
			}
		}
		if _, err := engine.DeleteRecord(edgeID); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, writeSceneEdge(engine, edgeID, edge)
}

func removeEdgeFromNode(engine *core.StorageEngine, nodeID, edgeID uint64) error {
	node := readSceneNode(engine, nodeID)
	if node == nil {
		return nil
	}
	found := false
	filtered := node.EdgeIDs[:0]
	for _, e := range node.EdgeIDs {
		if e == edgeID {
			found = true
		} else {
			filtered = append(filtered, e)
		}
	}
	if !found {
		return nil
	}
	node.EdgeIDs = filtered
	return core.WriteSceneNode(engine, nodeID, node)
}

// readSceneNode 读取并反序列化 L1 节点，记录类型不匹配或解析失败返回 nil。
func readSceneNode(engine *core.StorageEngine, idHash uint64) *core.SceneNode {
	rt, data, err := engine.ReadRecord(idHash)
	if err != nil || rt != core.RecL1SceneNode {
		return nil
	}
	var node core.SceneNode
	if json.Unmarshal(data, &node) != nil {
		return nil
	}
	return &node
}

// readSceneEdge 读取并反序列化 L1 超边，记录类型不匹配或解析失败返回 nil。
func readSceneEdge(engine *core.StorageEngine, idHash uint64) *core.SceneEdge {
	rt, data, err := engine.ReadRecord(idHash)
	if err != nil || rt != core.RecL1Hyperedge {
		return nil
	}
	var edge core.SceneEdge
	if json.Unmarshal(data, &edge) != nil {
		return nil
	}
	return &edge
}

func writeSceneEdge(engine *core.StorageEngine, id uint64, edge *core.SceneEdge) error {
	data, err := json.Marshal(edge)
	if err != nil {
		return common.NewError(common.ErrSerialization, "marshal scene edge", err)
	}
	_, err = engine.WriteRecord(core.RecL1Hyperedge, id, data)
	return err
}

// applyEmotionalBoost 情绪强度（|valence|×arousal）越高衰减越慢，结果非负。
func applyEmotionalBoost(baseLambda float64, valence, arousal float64) float64 {
	result := baseLambda - math.Abs(valence)*arousal*2.0
	if result < 0 {
		return 0
	}
	return result
}

func dtHoursFrom(nowMs, updatedAtMs int64) float64 {
	dtMs := nowMs - updatedAtMs
	if dtMs < 0 {
		dtMs = 0
	}
	return float64(dtMs) / 3_600_000.0
}
