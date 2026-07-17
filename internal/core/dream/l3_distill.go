// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"encoding/json"
	"fmt"
	"strings"

	"memhop/internal/core"
	"memhop/internal/core/index"
	"memhop/internal/core/model"
	"memhop/internal/core/storage"
	"memhop/internal/hash"
	"memhop/internal/timeutil"
)

// ApplyL3Extractions writes L3 knowledge graph nodes and edges from LLM output.
func ApplyL3Extractions(
	extractions []L3Extraction,
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
) ([]string, error) {
	nowMs := timeutil.NowMs()
	var allNewIDs []string
	for i := range extractions {
		ids, err := applyOneExtraction(&extractions[i], engine, nowMs)
		if err != nil {
			return allNewIDs, err
		}
		allNewIDs = append(allNewIDs, ids...)
	}
	return allNewIDs, nil
}

func applyOneExtraction(
	ext *L3Extraction,
	engine *storage.StorageEngine,
	nowMs int64,
) ([]string, error) {
	topic, err := readTopic(engine, ext.ContextID)
	if err != nil || topic == nil {
		return nil, nil
	}
	if len(ext.Concepts) == 0 {
		return nil, nil
	}

	graphID, err := resolveOrCreateGraph(engine, topic, ext.ContextID, nowMs)
	if err != nil {
		return nil, err
	}

	var newIDs []string
	conceptMap := make(map[string]uint64, len(ext.Concepts))
	for i := range ext.Concepts {
		nodeHash := hash.HashID(fmt.Sprintf("%016x_%s", graphID, ext.Concepts[i].Name))
		if err := writeGraphNode(engine, graphID, nodeHash, &ext.Concepts[i], nowMs); err != nil {
			return newIDs, err
		}
		newIDs = append(newIDs, hash.FormatHash(nodeHash))
		conceptMap[ext.Concepts[i].Name] = nodeHash
	}

	for i := range ext.Relations {
		if err := writeGraphEdge(engine, graphID, conceptMap, &ext.Relations[i], nowMs); err != nil {
			return newIDs, err
		}
	}
	return newIDs, nil
}

func resolveOrCreateGraph(
	engine *storage.StorageEngine,
	topic *model.TopicSlot,
	topicID uint64,
	nowMs int64,
) (uint64, error) {
	allRefs := append(topic.UserL3Refs, topic.AgentL3Refs...)
	if len(allRefs) > 0 && engine.Contains(allRefs[0]) {
		return allRefs[0], nil
	}

	newGraphID := hash.HashID(fmt.Sprintf("l3_distill_%016x", topicID))
	displayName := joinStrings(topic.FusedKeywords, topic.UserKeywords)

	slot := model.HypergraphSlot{
		IDHash:    newGraphID,
		Name:      "Distilled: " + displayName,
		Source:    model.HypergraphSource{Kind: model.SourceContext, ContextID: topicID},
		CreatedAt: nowMs,
		UpdatedAt: nowMs,
		Version:   1,
	}
	data, err := json.Marshal(slot)
	if err != nil {
		return 0, core.NewError(core.ErrSerialization, "marshal graph slot", err)
	}
	if _, err := engine.WriteRecord(storage.RecL3GraphSlot, newGraphID, data); err != nil {
		return 0, err
	}
	if err := addL3RefToContext(engine, topicID, newGraphID, nowMs); err != nil {
		return 0, err
	}
	return newGraphID, nil
}

func writeGraphNode(
	engine *storage.StorageEngine,
	graphID, nodeHash uint64,
	c *LlmConcept,
	nowMs int64,
) error {
	node := model.HypergraphNode{
		IDHash:     nodeHash,
		GraphID:    graphID,
		Title:      c.Name,
		NodeType:   c.NodeType,
		Content:    c.Description,
		Keywords:   c.Keywords,
		Importance: 0.7,
		ValidFrom:  nowMs,
		CreatedAt:  nowMs,
		UpdatedAt:  nowMs,
		Version:    1,
	}
	data, err := json.Marshal(node)
	if err != nil {
		return core.NewError(core.ErrSerialization, "marshal graph node", err)
	}
	_, err = engine.WriteRecord(storage.RecL3GraphNode, nodeHash, data)
	return err
}

func writeGraphEdge(
	engine *storage.StorageEngine,
	graphID uint64,
	conceptMap map[string]uint64,
	rel *LlmRelation,
	nowMs int64,
) error {
	fromHash, ok1 := conceptMap[rel.From]
	toHash, ok2 := conceptMap[rel.To]
	if !ok1 || !ok2 {
		return nil
	}

	edgeID := hash.HashID(fmt.Sprintf("%016x->%016x", fromHash, toHash))
	label := rel.Kind
	if label == "" {
		label = "related"
	}
	edge := model.HypergraphEdge{
		IDHash:     edgeID,
		GraphID:    graphID,
		Kind:       mapEdgeKind(rel.Kind),
		NodeIDs:    []uint64{fromHash, toHash},
		Weight:     1.0,
		Label:      &label,
		Confidence: 1.0,
		ValidFrom:  nowMs,
		CreatedAt:  nowMs,
	}
	data, err := json.Marshal(edge)
	if err != nil {
		return core.NewError(core.ErrSerialization, "marshal graph edge", err)
	}
	_, err = engine.WriteRecord(storage.RecL3GraphEdge, edgeID, data)
	return err
}

func addL3RefToContext(engine *storage.StorageEngine, ctxID, graphHash uint64, nowMs int64) error {
	topic, err := readTopic(engine, ctxID)
	if err != nil || topic == nil {
		return nil
	}
	for _, ref := range append(topic.UserL3Refs, topic.AgentL3Refs...) {
		if ref == graphHash {
			return nil
		}
	}
	topic.AgentL3Refs = append(topic.AgentL3Refs, graphHash)
	topic.UpdatedAt = nowMs
	return writeTopicRecord(engine, ctxID, topic)
}

func mapEdgeKind(kind string) model.GraphEdgeKind {
	switch strings.ToLower(kind) {
	case "related", "相关":
		return model.EdgeRelated
	case "causal", "因果":
		return model.EdgeCausal
	case "partof", "part_of", "部分":
		return model.EdgePartOf
	case "sequence", "顺序":
		return model.EdgeSequence
	case "dependency", "依赖":
		return model.EdgeDependency
	default:
		return model.EdgeCustom
	}
}

func joinStrings(primary, fallback []string) string {
	src := primary
	if len(src) == 0 {
		src = fallback
	}
	if len(src) == 0 {
		return ""
	}
	result := src[0]
	for _, s := range src[1:] {
		result += ", " + s
	}
	return result
}
