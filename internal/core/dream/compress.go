// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"encoding/json"
	"fmt"

	"memhop/internal/core"
	"memhop/internal/core/encoder"
	"memhop/internal/core/index"
	"memhop/internal/core/model"
	"memhop/internal/core/storage"
	"memhop/internal/hash"
	"memhop/internal/timeutil"
)

// CompressResult holds L2 compression metrics.
type CompressResult struct {
	GroupsDetected uint32
	NodesMerged    uint32
	ParentsCreated uint32
	NodesSunk      uint32
	NodesRemoved   uint32
}

// ApplyL2Groups applies pre-computed L2 groups from the LLM.
func ApplyL2Groups(
	groups []L2Group,
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
	l2Meta *index.L2MetaIndex,
	enc encoder.Encoder,
) (*CompressResult, error) {
	result := &CompressResult{}
	for _, g := range groups {
		if len(g.NodeHashes) < 2 {
			continue
		}
		result.GroupsDetected++
		result.NodesMerged += uint32(len(g.NodeHashes))
		if err := applyOneGroup(&g, engine, sparseIdx, l2Meta, enc, result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func applyOneGroup(
	g *L2Group,
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
	l2Meta *index.L2MetaIndex,
	enc encoder.Encoder,
	result *CompressResult,
) error {
	nowMs := timeutil.NowMs()
	parentID := hash.HashID(fmt.Sprintf("merged_parent_%d_%d", g.SceneID, nowMs))

	// Optional centroid encoding (non-fatal on failure).
	centroidVec := encodeCentroid(enc, g.MergedSummary)

	parent := buildParentNode(parentID, g, nowMs)

	// Write centroid vector to storage and set reference on parent topic.
	if len(centroidVec) > 0 {
		vecIDHash := index.VecRecordHash(parentID)
		vecBytes := f16SliceToBytes(centroidVec)
		if _, err := engine.WriteRecord(storage.RecVecCentroid, vecIDHash, vecBytes); err != nil {
			return fmt.Errorf("write centroid vector: %w", err)
		}
		parent.CentroidPageRef = vecIDHash
	}

	if err := writeTopicRecord(engine, parentID, &parent); err != nil {
		return err
	}
	indexParentInSparse(sparseIdx, parentID, &parent)
	l2Meta.Update(metaFromTopic(parentID, &parent))
	result.ParentsCreated++

	for _, childID := range g.NodeHashes {
		if err := sinkSubtree(childID, parentID, engine, sparseIdx, l2Meta, result); err != nil {
			return err
		}
	}
	return nil
}

func encodeCentroid(enc encoder.Encoder, text string) []uint16 {
	if enc == nil || text == "" {
		return nil
	}
	out, err := enc.Encode(text)
	if err != nil {
		return nil
	}
	return out.Dense
}

func buildParentNode(parentID uint64, g *L2Group, nowMs int64) model.TopicSlot {
	return model.TopicSlot{
		ID:             parentID,
		SceneID:        g.SceneID,
		ParentID:       nil,
		ChildrenIDs:    g.NodeHashes,
		Depth:          1,
		UserKeywords:   []string{},
		UserTimestamp:  nowMs,
		AgentKeywords:  []string{},
		AgentTimestamp: nowMs,
		FusedKeywords:  []string{g.MergedTitle},
		FusedSummary:   &g.MergedSummary,
		CreatedAt:      nowMs,
		UpdatedAt:      nowMs,
		Version:        3,
	}
}

func writeTopicRecord(engine *storage.StorageEngine, id uint64, topic *model.TopicSlot) error {
	data, err := json.Marshal(topic)
	if err != nil {
		return core.NewError(core.ErrSerialization, "marshal topic", err)
	}
	_, err = engine.WriteRecord(storage.RecL2Topic, id, data)
	return err
}

func indexParentInSparse(sparseIdx *index.SparseIndex, id uint64, topic *model.TopicSlot) {
	summary := ""
	if topic.FusedSummary != nil {
		summary = *topic.FusedSummary
	}
	terms := index.Tokenize(summary)
	sparseIdx.AddDocument(id, terms, uint32(len(terms)))
}

func metaFromTopic(id uint64, t *model.TopicSlot) *index.L2Meta {
	title := ""
	if len(t.FusedKeywords) > 0 {
		title = t.FusedKeywords[0]
	}
	summary := ""
	if t.FusedSummary != nil {
		summary = *t.FusedSummary
	}
	return &index.L2Meta{
		IDHash:      id,
		Title:       title,
		Summary:     summary,
		Depth:       t.Depth,
		SceneID:     t.SceneID,
		ChildrenIDs: t.ChildrenIDs,
		Timestamp:   uint64(t.UpdatedAt),
	}
}

// sinkSubtree increases a node's depth by 1 and recurses.
// Depth >= 4 triggers deletion.
func sinkSubtree(
	idHash, newParentID uint64,
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
	l2Meta *index.L2MetaIndex,
	result *CompressResult,
) error {
	topic, err := readTopic(engine, idHash)
	if err != nil || topic == nil {
		return nil
	}

	topic.Depth++
	topic.ParentID = &newParentID
	topic.UpdatedAt = timeutil.NowMs()

	if topic.Depth >= 4 {
		return freeNodeAndDescendants(idHash, engine, sparseIdx, l2Meta, result)
	}

	if err := writeTopicRecord(engine, idHash, topic); err != nil {
		return err
	}
	l2Meta.Update(metaFromTopic(idHash, topic))
	result.NodesSunk++

	for _, childID := range topic.ChildrenIDs {
		if err := sinkSubtree(childID, idHash, engine, sparseIdx, l2Meta, result); err != nil {
			return err
		}
	}
	if err := cleanDeletedChildren(idHash, topic, engine, l2Meta); err != nil {
		return err
	}
	return nil
}

func cleanDeletedChildren(
	idHash uint64,
	topic *model.TopicSlot,
	engine *storage.StorageEngine,
	l2Meta *index.L2MetaIndex,
) error {
	original := len(topic.ChildrenIDs)
	filtered := topic.ChildrenIDs[:0]
	for _, cid := range topic.ChildrenIDs {
		if engine.Contains(cid) {
			filtered = append(filtered, cid)
		}
	}
	topic.ChildrenIDs = filtered
	if len(topic.ChildrenIDs) < original {
		if err := writeTopicRecord(engine, idHash, topic); err != nil {
			return err
		}
		l2Meta.Update(metaFromTopic(idHash, topic))
	}
	return nil
}

// freeNodeAndDescendants recursively deletes a node and all descendants.
func freeNodeAndDescendants(
	idHash uint64,
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
	l2Meta *index.L2MetaIndex,
	result *CompressResult,
) error {
	topic, err := readTopic(engine, idHash)
	if err != nil || topic == nil {
		return nil
	}
	for _, childID := range topic.ChildrenIDs {
		if err := freeNodeAndDescendants(childID, engine, sparseIdx, l2Meta, result); err != nil {
			return err
		}
	}
	sparseIdx.RemoveDocument(idHash)
	l2Meta.Remove(idHash)
	_, err = engine.DeleteRecord(idHash)
	if err != nil {
		return err
	}
	result.NodesRemoved++
	return nil
}

// readTopic reads and deserializes a TopicSlot from the engine.
func readTopic(engine *storage.StorageEngine, idHash uint64) (*model.TopicSlot, error) {
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

// f16SliceToBytes converts a slice of f16 uint16 values to little-endian bytes.
func f16SliceToBytes(vec []uint16) []byte {
	buf := make([]byte, len(vec)*2)
	for i, v := range vec {
		buf[i*2] = byte(v)
		buf[i*2+1] = byte(v >> 8)
	}
	return buf
}
