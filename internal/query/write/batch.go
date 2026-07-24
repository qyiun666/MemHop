// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// BatchStore: five-phase batch memory storage pipeline.

package write

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"memhop/internal/common/hash"
	"memhop/internal/common/numeric"
	"memhop/internal/common/strutil"
	"memhop/internal/common/timeutil"
	"memhop/internal/core/index"
	"memhop/internal/core/model"
	"memhop/internal/core/record"
	"memhop/internal/core/storage"
	"memhop/internal/query/crud"
)

// BatchStore runs the five-phase batch store pipeline.
func BatchStore(batch StoreBatch, deps *BatchDeps) (*BatchReport, error) {
	if len(batch.Items) == 0 {
		return &BatchReport{}, nil
	}
	report := &BatchReport{}
	skipExisting := batch.ImportMode != nil && *batch.ImportMode == ImportSkip

	// Phase 1: Encode
	encoded, err := encodeItems(batch.Items, deps)
	if err != nil {
		return nil, err
	}

	// Phase 2: L4 Archive
	archiveIDs, err := archiveDocuments(deps.Engine, encoded, batch.SourceInfo)
	if err != nil {
		return nil, err
	}
	report.L4Docs = uint32(len(archiveIDs))

	// Phase 3: L1 Write with dedup
	l1IDs, created, skipped, err := dedupAndWriteL1(deps, encoded, skipExisting)
	if err != nil {
		return nil, err
	}
	report.L1NodesCreated = created
	report.DedupSkipped = skipped

	// Phase 4: L2 Topic Update
	topicsUpdated, err := updateTopics(deps, encoded, l1IDs, archiveIDs)
	if err != nil {
		return nil, err
	}
	report.L2TopicsUpdated = topicsUpdated

	// Phase 5: Hyperedges
	edgesCreated, err := createBatchHyperedges(deps.Engine, l1IDs)
	if err != nil {
		return nil, err
	}
	report.EdgesCreated = edgesCreated

	return report, nil
}

// --- Phase 1: Encode ---

type encodedItem struct {
	text       string
	dense      []uint16
	keywords   []string
	topicLabel *string
	importance float32
	source     string
	sourceType string
}

func encodeItems(items []StoreItem, deps *BatchDeps) ([]encodedItem, error) {
	encoded := make([]encodedItem, 0, len(items))
	for i, item := range items {
		keywords := item.Keywords
		// For batch import, keywords are required (caller must provide pre-extracted facts).
		if len(keywords) == 0 {
			return nil, fmt.Errorf("encode item %d: keywords required for batch import", i)
		}
		ei := encodedItem{
			text:       item.Content,
			keywords:   keywords,
			topicLabel: item.TopicLabel,
			importance: float32(item.Score),
			source:     item.Source,
			sourceType: item.SourceType,
		}
		if deps.Encoder != nil && deps.Encoder.IsAvailable() {
			encodeText := strutil.JoinStrings(keywords, " ")
			output, err := deps.Encoder.Encode(encodeText)
			if err != nil {
				return nil, fmt.Errorf("encode item %d (%q): %w", i, strutil.SafeCharSlice(item.Content, 40), err)
			}
			if len(output.Dense) > 0 {
				ei.dense = output.Dense
			}
		}
		encoded = append(encoded, ei)
	}
	return encoded, nil
}

// --- Phase 2: L4 Archive ---

// archiveDocuments stores L4 archives. sourceInfo is stored in Metadata.
func archiveDocuments(engine *storage.StorageEngine, items []encodedItem, sourceInfo *string) ([]uint64, error) {
	now := timeutil.NowMs()
	ids := make([]uint64, len(items))
	for i, item := range items {
		idHash := hash.HashID("archive:" + item.text)
		metadata := buildArchiveMetadata(item, sourceInfo)
		arc := model.ArchiveSlot{
			IDHash:      idHash,
			ContentType: model.ContentText,
			Role:        0,
			ContextID:   0,
			CreatedAt:   now,
			Content:     item.text,
			Metadata:    metadata,
		}
		if err := record.WriteArchiveSlot(engine, idHash, &arc); err != nil {
			return nil, fmt.Errorf("write archive slot: %w", err)
		}
		ids[i] = idHash
	}
	return ids, nil
}

// --- Phase 3: L1 Write with dedup ---

const cosineThreshold = 0.95

// L1NodeIDHash returns the storage ID for an L1 node holding the given text.
// The "l1:" prefix keeps L1 node IDs out of the L2 topic ID space
// (topics use HashID(label)), preventing cross-type ID collisions.
func L1NodeIDHash(text string) uint64 {
	return hash.HashID("l1:" + text)
}

func dedupAndWriteL1(
	deps *BatchDeps,
	items []encodedItem,
	skipExisting bool,
) ([]uint64, uint32, uint32, error) {
	now := timeutil.NowMs()
	nodeIDs := make([]uint64, len(items))
	var created, skipped uint32

	for i, item := range items {
		idHash := L1NodeIDHash(item.text)
		// Exact dedup
		if deps.Engine.Contains(idHash) {
			skipped++
			nodeIDs[i] = idHash
			if skipExisting {
				continue
			}
			// When not skipping, fall through to update existing node
			continue // TODO: Overwrite mode would re-write the node here
		}
		// Cosine dedup (scoped to same scene when L2Meta is available)
		topicLabel := ""
		if item.topicLabel != nil {
			topicLabel = *item.topicLabel
		}
		if existingID := findDuplicate(deps, item.dense, topicLabel); existingID != 0 {
			skipped++
			nodeIDs[i] = existingID
			continue
		}
		// Write vector
		var vecRef uint64
		if len(item.dense) > 0 {
			vecIDHash := hash.HashID(fmt.Sprintf("v:%d", idHash))
			vecBytes := numeric.F16SliceToBytes(item.dense)
			if _, err := deps.Engine.WriteRecord(storage.RecVecCentroid, vecIDHash, vecBytes); err != nil {
				return nil, 0, 0, fmt.Errorf("write centroid vector: %w", err)
			}
			vecRef = vecIDHash
		}
		// Write L1 node
		node := model.SceneNode{
			IDHash:        idHash,
			SceneID:       0,
			VectorPageRef: vecRef,
			Importance:    item.importance,
			CreatedAt:     now,
			UpdatedAt:     now,
			EdgeIDs:       []uint64{},
		}
		if err := writeL1Node(deps.Engine, &node, item.keywords, deps.SparseIndex); err != nil {
			return nil, 0, 0, err
		}
		created++
		nodeIDs[i] = idHash
	}
	return nodeIDs, created, skipped, nil
}

// buildArchiveMetadata constructs archive metadata from source info fields.
func buildArchiveMetadata(item encodedItem, sourceInfo *string) *string {
	type arcMeta struct {
		Source     string `json:"source,omitempty"`
		SourceType string `json:"source_type,omitempty"`
		SourceInfo string `json:"source_info,omitempty"`
	}
	m := arcMeta{
		Source:     item.source,
		SourceType: item.sourceType,
	}
	if sourceInfo != nil {
		m.SourceInfo = *sourceInfo
	}
	if m.Source == "" && m.SourceType == "" && m.SourceInfo == "" {
		return nil
	}
	data, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	s := string(data)
	return &s
}

func writeL1Node(
	engine *storage.StorageEngine,
	node *model.SceneNode,
	keywords []string,
	sparse *index.SparseIndex,
) error {
	if err := record.WriteSceneNode(engine, node.IDHash, node); err != nil {
		return fmt.Errorf("write scene node: %w", err)
	}
	if len(keywords) > 0 {
		kwText := strutil.JoinStrings(keywords, " ")
		terms := index.Tokenize(kwText)
		sparse.AddDocument(node.IDHash, terms, uint32(len(terms)))
	}
	return nil
}

func findDuplicate(deps *BatchDeps, queryVec []uint16, topicLabel string) uint64 {
	if len(queryVec) == 0 {
		return 0
	}

	// If L2Meta is available and topicLabel is set, scope comparison to
	// L1 nodes belonging to the same scene, reducing cosine scan from
	// full table to the relevant subset.
	var sceneIDs map[uint64]struct{}
	if deps.L2Meta != nil && topicLabel != "" {
		sceneID := hash.HashID(topicLabel)
		topicIDs := deps.L2Meta.GetByScene(sceneID)
		sceneIDs = make(map[uint64]struct{}, len(topicIDs)+1)
		sceneIDs[sceneID] = struct{}{}
		for _, tid := range topicIDs {
			sceneIDs[tid] = struct{}{}
		}
	}

	var best uint64
	_ = deps.Engine.IterIndexByType(storage.RecL1SceneNode, func(idHash uint64) error {
		_, data, err := deps.Engine.ReadRecord(idHash)
		if err != nil {
			return nil
		}
		var node model.SceneNode
		if json.Unmarshal(data, &node) != nil || node.VectorPageRef == 0 {
			return nil
		}
		// When scoping to a scene, skip nodes not in that scene.
		if sceneIDs != nil {
			if _, ok := sceneIDs[node.SceneID]; !ok {
				return nil
			}
		}
		_, vecData, err := deps.Engine.ReadRecord(node.VectorPageRef)
		if err != nil || len(vecData) < len(queryVec)*2 {
			return nil
		}
		existingVec := numeric.DecodeF16Vec(vecData, len(queryVec))
		if len(existingVec) != len(queryVec) {
			return nil
		}
		sim := numeric.CosineSimilarity(queryVec, existingVec)
		if sim > cosineThreshold {
			best = idHash
		}
		return nil
	})
	return best
}

// --- Phase 4: L2 Topic Update ---

func updateTopics(
	deps *BatchDeps,
	items []encodedItem,
	l1NodeIDs, archiveIDs []uint64,
) (uint32, error) {
	// Group by topic label
	groups := groupByTopicLabel(items)
	var topicsUpdated uint32
	now := timeutil.NowMs()

	for label, indices := range groups {
		contextID := hash.HashID(label)
		nodeIDs := collectNodeIDs(indices, l1NodeIDs)
		archiveRefs := collectArchiveRefs(indices, archiveIDs)
		keywords := collectKeywords(items, indices)

		centroidRef, err := computeTopicCentroid(deps, keywords, nodeIDs)
		if err != nil {
			return 0, err
		}

		ctx := buildTopicSlot(contextID, label, keywords, archiveRefs, centroidRef, now)
		if err := writeTopicToEngine(deps.Engine, deps.SparseIndex, deps.L2Meta, &ctx); err != nil {
			return 0, err
		}
		backfillContextID(deps.Engine, nodeIDs, contextID, now)
		topicsUpdated++
	}
	return topicsUpdated, nil
}

func groupByTopicLabel(items []encodedItem) map[string][]int {
	groups := make(map[string][]int)
	for i, item := range items {
		label := "default"
		if item.topicLabel != nil && *item.topicLabel != "" {
			label = *item.topicLabel
		}
		groups[label] = append(groups[label], i)
	}
	return groups
}

func collectNodeIDs(indices []int, l1NodeIDs []uint64) []uint64 {
	ids := make([]uint64, 0, len(indices))
	for _, idx := range indices {
		if idx < len(l1NodeIDs) {
			ids = append(ids, l1NodeIDs[idx])
		}
	}
	return ids
}

func collectArchiveRefs(indices []int, archiveIDs []uint64) []uint64 {
	refs := make([]uint64, 0, len(indices))
	for _, idx := range indices {
		if idx < len(archiveIDs) {
			refs = append(refs, archiveIDs[idx])
		}
	}
	return refs
}

func collectKeywords(items []encodedItem, indices []int) []string {
	seen := make(map[string]struct{})
	for _, idx := range indices {
		if idx >= len(items) {
			continue
		}
		for _, kw := range items[idx].keywords {
			trimmed := kw
			if trimmed != "" {
				seen[trimmed] = struct{}{}
			}
		}
	}
	kws := make([]string, 0, len(seen))
	for k := range seen {
		kws = append(kws, k)
	}
	return kws
}

func computeTopicCentroid(
	deps *BatchDeps,
	keywords []string,
	nodeIDs []uint64,
) (uint64, error) {
	// Prefer keyword-based encoding for vector space symmetry with search
	if deps.Encoder != nil && deps.Encoder.IsAvailable() && len(keywords) > 0 {
		encodeText := strutil.JoinStrings(keywords, " ")
		output, err := deps.Encoder.Encode(encodeText)
		if err != nil {
			return 0, fmt.Errorf("computeTopicCentroid: encode keywords: %w", err)
		}
		if len(output.Dense) > 0 {
			contextID := hash.HashID(encodeText)
			vecIDHash := hash.HashID(fmt.Sprintf("v:%d", contextID))
			vecBytes := numeric.F16SliceToBytes(output.Dense)
			if _, err := deps.Engine.WriteRecord(storage.RecVecCentroid, vecIDHash, vecBytes); err != nil {
				return 0, fmt.Errorf("computeTopicCentroid: write record: %w", err)
			}
			return vecIDHash, nil
		}
	}
	// Fallback: average L1 node vectors
	return AverageNodeCentroid(deps, nodeIDs)
}

func AverageNodeCentroid(deps *BatchDeps, nodeIDs []uint64) (uint64, error) {
	if deps.VectorDim == 0 || len(nodeIDs) == 0 {
		return 0, nil
	}
	sum := make([]float32, deps.VectorDim)
	count := 0
	for _, nid := range nodeIDs {
		vec := readNodeVector(deps.Engine, nid, deps.VectorDim)
		if vec == nil {
			continue
		}
		for i, v := range vec {
			f32 := numeric.F16ToF32(v)
			sum[i] += f32
		}
		count++
	}
	if count == 0 {
		return 0, nil
	}
	centroid := make([]uint16, deps.VectorDim)
	for i := range sum {
		centroid[i] = numeric.F32ToF16(sum[i] / float32(count))
	}
	vecBytes := numeric.F16SliceToBytes(centroid)
	contextID := hash.HashID("centroid:" + string(vecBytes))
	vecIDHash := hash.HashID(fmt.Sprintf("v:%d", contextID))
	if _, err := deps.Engine.WriteRecord(storage.RecVecCentroid, vecIDHash, vecBytes); err != nil {
		return 0, fmt.Errorf("averageNodeCentroid: write record: %w", err)
	}
	return vecIDHash, nil
}

func readNodeVector(engine *storage.StorageEngine, nodeID uint64, dim int) []uint16 {
	_, data, err := engine.ReadRecord(nodeID)
	if err != nil {
		return nil
	}
	var node model.SceneNode
	if json.Unmarshal(data, &node) != nil || node.VectorPageRef == 0 {
		return nil
	}
	_, vecData, err := engine.ReadRecord(node.VectorPageRef)
	if err != nil || len(vecData) < dim*2 {
		return nil
	}
	return numeric.DecodeF16Vec(vecData, dim)
}

func buildTopicSlot(
	contextID uint64,
	label string,
	keywords []string,
	archiveRefs []uint64,
	centroidRef uint64,
	nowMs int64,
) model.TopicSlot {
	userKws := keywords
	if len(userKws) == 0 {
		userKws = []string{label}
	}
	return model.TopicSlot{
		ID:              contextID,
		Depth:           1,
		UserKeywords:    userKws,
		UserTimestamp:   nowMs,
		UserL4Refs:      archiveRefs,
		UserL3Refs:      []uint64{},
		AgentKeywords:   []string{},
		AgentTimestamp:  nowMs,
		AgentL4Refs:     []uint64{},
		AgentL3Refs:     []uint64{},
		FusedKeywords:   []string{},
		ChildrenIDs:     []uint64{},
		CentroidPageRef: centroidRef,
		CreatedAt:       nowMs,
		UpdatedAt:       nowMs,
		Version:         1,
	}
}

func writeTopicToEngine(
	engine *storage.StorageEngine,
	sparse *index.SparseIndex,
	l2Meta *index.L2MetaIndex,
	ctx *model.TopicSlot,
) error {
	if err := record.WriteTopicSlot(engine, ctx.ID, ctx); err != nil {
		return fmt.Errorf("write topic slot: %w", err)
	}
	crud.ReindexTopic(sparse, ctx)
	// Keep L2Meta in sync so the topic is searchable before the next rebuild.
	if l2Meta != nil {
		l2Meta.Update(index.L2MetaFromTopic(ctx))
	}
	return nil
}

func backfillContextID(
	engine *storage.StorageEngine,
	nodeIDs []uint64,
	contextID uint64,
	nowMs int64,
) {
	for _, nid := range nodeIDs {
		rt, data, err := engine.ReadRecord(nid)
		if err != nil || rt != storage.RecL1SceneNode {
			// Not an L1 node (e.g. legacy ID collision with another record
			// type): never overwrite a foreign record.
			continue
		}
		var node model.SceneNode
		if json.Unmarshal(data, &node) != nil {
			continue
		}
		node.SceneID = contextID
		node.UpdatedAt = nowMs
		if err := record.WriteSceneNode(engine, nid, &node); err != nil {
			continue
		}
	}
}

// --- Phase 5: Hyperedges ---

func createBatchHyperedges(engine *storage.StorageEngine, l1NodeIDs []uint64) (uint32, error) {
	if len(l1NodeIDs) <= 1 {
		return 0, nil
	}
	now := timeutil.NowMs()
	var edgeCount uint32

	// Co-occurrence edge. ID is derived from the sorted node set so that
	// distinct batches never overwrite each other's edge, while re-importing
	// the same set stays idempotent.
	sortedIDs := make([]uint64, len(l1NodeIDs))
	copy(sortedIDs, l1NodeIDs)
	sort.Slice(sortedIDs, func(i, j int) bool { return sortedIDs[i] < sortedIDs[j] })
	assocEdge := model.HyperedgeSlot{
		IDHash:    hash.HashID("assoc:" + uint64sKey(sortedIDs)),
		Kind:      model.HyperCoOccurrence,
		NodePtrs:  l1NodeIDs,
		Weight:    1.0,
		CreatedAt: now,
		Version:   1,
	}
	if err := writeHyperedge(engine, &assocEdge); err != nil {
		return 0, err
	}
	edgeCount++

	// Temporal evolution edges: ID derived from the endpoint pair.
	for i := 1; i < len(l1NodeIDs); i++ {
		edgeID := hash.HashID(fmt.Sprintf("evolution:%x:%x", l1NodeIDs[i-1], l1NodeIDs[i]))
		evolEdge := model.HyperedgeSlot{
			IDHash:    edgeID,
			Kind:      model.HyperTemporal,
			NodePtrs:  []uint64{l1NodeIDs[i-1], l1NodeIDs[i]},
			Weight:    1.0,
			CreatedAt: now,
			Version:   1,
		}
		if err := writeHyperedge(engine, &evolEdge); err != nil {
			return 0, err
		}
		edgeCount++
	}
	return edgeCount, nil
}

// uint64sKey renders IDs as a comma-separated hex string for hashing.
func uint64sKey(ids []uint64) string {
	var sb strings.Builder
	for i, id := range ids {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatUint(id, 16))
	}
	return sb.String()
}

func writeHyperedge(engine *storage.StorageEngine, edge *model.HyperedgeSlot) error {
	if err := record.WriteHyperedgeSlot(engine, edge.IDHash, edge); err != nil {
		return fmt.Errorf("write hyperedge slot: %w", err)
	}
	return nil
}
