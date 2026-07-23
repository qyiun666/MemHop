// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Core search engine: three-route dispatch (auto_create, directed, normal).

package search

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"memhop/internal/common/config"
	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
	"memhop/internal/common/numeric"
	"memhop/internal/common/strutil"
	"memhop/internal/core/index"
	"memhop/internal/core/model"
	"memhop/internal/core/record"
	"memhop/internal/core/storage"
	"memhop/internal/query/crud"
)

// SearchContext orchestrates the search pipeline.
// Steps: 1. LLM preprocess (skip if AutoCreate or DirectedL2ID)
//  2. L2 retrieval (skip if AutoCreate or DirectedL2ID)
//  3. L1-associated L2 lookup
//  4. Assemble L0 + L2 + associated L2 + L5
func SearchContext(q SearchQuery, deps *SearchDeps) (*SearchResult, error) {
	switch {
	case q.AutoCreate:
		return searchAutoCreate(q, deps)
	case q.DirectedL2ID != nil:
		return searchDirected(q, deps)
	default:
		return searchNormal(q, deps)
	}
}

// searchAutoCreate creates a new L2 topic directly, skipping retrieval.
func searchAutoCreate(q SearchQuery, deps *SearchDeps) (*SearchResult, error) {
	ctx, err := createNewL2Context(q, deps)
	if err != nil {
		return nil, err
	}
	cr := topicToContextResult(ctx, 1.0)
	result := &SearchResult{
		Profile:            readProfileResult(deps),
		Contexts:           []ContextResult{cr},
		AssociatedContexts: []ContextResult{},
		Crystals:           []crud.CrystalSummary{},
	}
	result.Crystals = matchActionChains(deps.Engine, q.Text)
	return result, nil
}

// searchDirected loads a specific L2 by ID, skipping retrieval.
func searchDirected(q SearchQuery, deps *SearchDeps) (*SearchResult, error) {
	targetHash, err := hash.ParseID(*q.DirectedL2ID)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "parse directed l2 id", err)
	}
	_, data, err := deps.Engine.ReadRecord(targetHash)
	if err != nil {
		return emptyResult(), nil
	}
	var ctx model.TopicSlot
	if err := json.Unmarshal(data, &ctx); err != nil {
		return emptyResult(), nil
	}
	cr := topicToContextResult(&ctx, 1.0)
	result := &SearchResult{
		Profile:            readProfileResult(deps),
		Contexts:           []ContextResult{cr},
		AssociatedContexts: []ContextResult{},
		Crystals:           []crud.CrystalSummary{},
	}
	result.Crystals = matchActionChains(deps.Engine, q.Text)
	return result, nil
}

// searchNormal runs the full retrieval pipeline.
func searchNormal(q SearchQuery, deps *SearchDeps) (*SearchResult, error) {
	// Step 1: Build candidate set (DirectedL3ID filters to L2s containing that L3)
	candidates := buildCandidateSetWithL3(deps.L2Meta, deps.SparseIndex, q.DirectedL3ID)
	if q.DirectedL3ID != nil && candidates == nil {
		return emptyResult(), nil
	}

	// Use LLM-extracted keywords when available, otherwise use raw text
	searchText := q.Text
	if len(deps.PreprocessedKeywords) > 0 {
		searchText = strings.Join(deps.PreprocessedKeywords, " ")
	}

	limit := q.EffectiveMaxResults()

	// Step 2: Multi-channel retrieval (BM25 + Vector + Entity)
	bm25Results := retrieveL2BM25(deps.Engine, deps.SparseIndex, searchText, candidates, 100)
	vectorResults := retrieveL2VectorSafe(q.Text, candidates, deps)
	entityResults := retrieveL2Entity(deps.Engine, deps.SparseIndex, searchText, candidates)

	// RRF fusion with channel weights
	rrfK := DefaultSearchConfig.DefaultRRFK
	if deps.Weights != nil && deps.Weights.RRFK > 0 {
		rrfK = deps.Weights.RRFK
	}
	merged := RRFMerge3(bm25Results, vectorResults, entityResults, rrfK)

	// RRF scores are ranking scores (max ≈ 3/61), truncated by limit.
	scored := loadAndScoreContextsDepth1(deps.Engine, merged, limit)
	if len(scored) > 0 {
		return buildSearchResult(q, deps, scored)
	}

	// No match → return empty result
	return emptyResult(), nil
}

// scoredContext pairs a ContextResult with its TopicSlot for reranking.
type scoredContext struct {
	result *ContextResult
	topic  *model.TopicSlot
	score  float32
}

// retrieveL2BM25 performs BM25 keyword retrieval scoped to candidates.
func retrieveL2BM25(
	engine *storage.StorageEngine,
	sparse *index.SparseIndex,
	queryText string,
	candidates map[uint64]struct{},
	limit int,
) []index.ScoredDoc {
	terms := index.Tokenize(queryText)
	if len(terms) == 0 {
		return nil
	}
	hits := sparse.Search(terms, limit*2)
	return filterByCandidates(hits, candidates, limit)
}

// retrieveL2VectorSafe performs vector retrieval, logging a warning on failure.
// Returns nil if encoder is unavailable or encoding fails.
func retrieveL2VectorSafe(
	queryText string,
	candidates map[uint64]struct{},
	deps *SearchDeps,
) []index.ScoredDoc {
	if deps.Encoder == nil || !deps.Encoder.IsAvailable() {
		return nil
	}
	output, err := deps.Encoder.Encode(queryText)
	if err != nil {
		slog.Warn("vector retrieval: encode failed, vector channel skipped",
			"error", err)
		return nil
	}
	if len(output.Dense) == 0 {
		slog.Warn("vector retrieval: encoder returned empty dense vector, vector channel skipped")
		return nil
	}
	return bruteForceVectorSearch(deps.Engine, output.Dense, candidates, 100)
}

// bruteForceVectorSearch scans all L2 topics for vector similarity.
func bruteForceVectorSearch(
	engine *storage.StorageEngine,
	queryVec []uint16,
	candidates map[uint64]struct{},
	limit int,
) []index.ScoredDoc {
	var results []index.ScoredDoc
	_ = engine.IterIndexByType(storage.RecL2Topic, func(idHash uint64) error {
		if candidates != nil {
			if _, ok := candidates[idHash]; !ok {
				return nil
			}
		}
		_, data, err := engine.ReadRecord(idHash)
		if err != nil {
			return nil
		}
		var topic model.TopicSlot
		if json.Unmarshal(data, &topic) != nil || topic.Depth > 2 {
			return nil
		}
		if topic.CentroidPageRef == 0 {
			return nil
		}
		_, vecData, err := engine.ReadRecord(topic.CentroidPageRef)
		if err != nil || len(vecData) < len(queryVec)*2 {
			return nil
		}
		centroid := numeric.DecodeF16Vec(vecData, len(queryVec))
		if len(centroid) != len(queryVec) {
			return nil
		}
		score := numeric.CosineSimilarity(queryVec, centroid)
		results = append(results, index.ScoredDoc{IDHash: idHash, Score: score})
		return nil
	})
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

// retrieveL2Entity performs entity-based retrieval scoped to candidates.
func retrieveL2Entity(
	engine *storage.StorageEngine,
	sparse *index.SparseIndex,
	queryText string,
	candidates map[uint64]struct{},
) []index.ScoredDoc {
	hits := sparse.EntitySearch(queryText)
	return filterByCandidates(hits, candidates, 50)
}

// filterByCandidates filters scored docs by candidate set and depth≤2.
func filterByCandidates(
	hits []index.ScoredDoc,
	candidates map[uint64]struct{},
	limit int,
) []index.ScoredDoc {
	if candidates == nil {
		if len(hits) > limit {
			return hits[:limit]
		}
		return hits
	}
	var filtered []index.ScoredDoc
	for _, h := range hits {
		if _, ok := candidates[h.IDHash]; ok {
			filtered = append(filtered, h)
			if len(filtered) >= limit {
				break
			}
		}
	}
	return filtered
}

// loadAndScoreContexts loads TopicSlots and filters depth ≤ 2.
func loadAndScoreContexts(
	engine *storage.StorageEngine,
	merged []index.ScoredDoc,
	limit int,
) []scoredContext {
	var result []scoredContext
	for _, doc := range merged {
		_, data, err := engine.ReadRecord(doc.IDHash)
		if err != nil {
			continue
		}
		var topic model.TopicSlot
		if json.Unmarshal(data, &topic) != nil || topic.Depth > 2 {
			continue
		}
		cr := topicToContextResult(&topic, doc.Score)
		t := topic // copy
		result = append(result, scoredContext{result: &cr, topic: &t, score: doc.Score})
		if len(result) >= limit {
			break
		}
	}
	return result
}

// getL1Associated finds L1-associated contexts via the reverse index.
func getL1Associated(
	engine *storage.StorageEngine,
	primary []scoredContext,
	l1Reverse *index.L1ReverseIndex,
) []ContextResult {
	if l1Reverse == nil || len(primary) == 0 {
		return []ContextResult{}
	}
	primaryIDs := make(map[uint64]struct{}, len(primary))
	for _, sc := range primary {
		primaryIDs[sc.topic.ID] = struct{}{}
	}
	nodeIDs := l1Reverse.FindAssociated(primaryIDs)
	var associated []ContextResult
	seen := make(map[uint64]struct{})
	for _, sc := range primary {
		seen[sc.topic.ID] = struct{}{}
	}
	for _, nodeHash := range nodeIDs {
		rt, data, err := engine.ReadRecord(nodeHash)
		if err != nil || rt != storage.RecL1SceneNode {
			continue
		}
		var node model.SceneNode
		if json.Unmarshal(data, &node) != nil || node.SceneID == 0 {
			continue
		}
		if _, ok := seen[node.SceneID]; ok {
			continue
		}
		seen[node.SceneID] = struct{}{}
		_, ctxData, err := engine.ReadRecord(node.SceneID)
		if err != nil {
			continue
		}
		var ctx model.TopicSlot
		if json.Unmarshal(ctxData, &ctx) != nil {
			continue
		}
		cr := topicToContextResult(&ctx, float32(node.Importance))
		associated = append(associated, cr)
	}
	if associated == nil {
		return []ContextResult{}
	}
	return associated
}

// getL1Previews builds lightweight L1 previews for matched contexts.
func getL1Previews(
	engine *storage.StorageEngine,
	primary []scoredContext,
	l1Reverse *index.L1ReverseIndex,
	queryText string,
) []L1Preview {
	if l1Reverse == nil || len(primary) == 0 {
		return []L1Preview{}
	}
	primaryIDs := make(map[uint64]struct{}, len(primary))
	for _, sc := range primary {
		primaryIDs[sc.topic.ID] = struct{}{}
	}
	nodeIDs := l1Reverse.FindAssociated(primaryIDs)
	var previews []L1Preview
	for _, nodeHash := range nodeIDs {
		rt, data, err := engine.ReadRecord(nodeHash)
		if err != nil || rt != storage.RecL1SceneNode {
			continue
		}
		var node model.SceneNode
		if json.Unmarshal(data, &node) != nil {
			continue
		}
		p := L1Preview{
			ID:              hash.FormatHash(nodeHash),
			Importance:      float64Ptr(float64(node.Importance)),
			MatchedKeywords: []string{},
		}
		previews = append(previews, p)
	}
	if previews == nil {
		return []L1Preview{}
	}
	return previews
}

// createNewL2Context creates a new L2 topic + L4 archive from dialogue (auto_create path).
func createNewL2Context(q SearchQuery, deps *SearchDeps) (*model.TopicSlot, error) {
	nowMs := q.Timestamp

	// Use LLM keywords when available, fall back to tokenizer
	keywords := deps.PreprocessedKeywords
	if len(keywords) == 0 {
		keywords = index.Tokenize(q.Text)
	}
	if len(keywords) == 0 {
		keywords = []string{strutil.SafeCharSlice(q.Text, 50)}
	}
	idStr := fmt.Sprintf("ctx_%d_%s", nowMs, strutil.SafeCharSlice(q.Text, 10))
	idHash := hash.HashID(idStr)

	// Encode centroid
	var centroidRef uint64
	if deps.Encoder != nil && deps.Encoder.IsAvailable() {
		encodeText := strutil.JoinStrings(keywords, " ")
		output, err := deps.Encoder.Encode(encodeText)
		if err != nil {
			return nil, fmt.Errorf("createNewL2Context: encode centroid: %w", err)
		}
		if len(output.Dense) > 0 {
			vecIDHash := hash.HashID(fmt.Sprintf("v:%d", idHash))
			vecBytes := numeric.F16SliceToBytes(output.Dense)
			if _, err := deps.Engine.WriteRecord(storage.RecVecCentroid, vecIDHash, vecBytes); err != nil {
				return nil, fmt.Errorf("createNewL2Context: write centroid: %w", err)
			}
			centroidRef = vecIDHash
		}
	}

	// Create L4 archive for this query text
	archiveIDStr := fmt.Sprintf("archive_%d_%s", nowMs, strutil.SafeCharSlice(q.Text, 10))
	archiveIDHash := hash.HashID(archiveIDStr)
	archive := model.ArchiveSlot{
		IDHash:      archiveIDHash,
		ContentType: model.ContentText,
		Role:        0, // user
		ContextID:   idHash,
		CreatedAt:   nowMs,
		Content:     q.Text,
	}
	if err := record.WriteArchiveSlot(deps.Engine, archiveIDHash, &archive); err != nil {
		return nil, err
	}

	topic := model.TopicSlot{
		ID:      idHash,
		SceneID: idHash, // each auto-created topic is its own scene initially;
		// scenes are merged by Dream's L2 consolidation
		Depth:           1,
		UserKeywords:    keywords,
		UserTimestamp:   nowMs,
		UserL4Refs:      []uint64{archiveIDHash},
		UserL3Refs:      []uint64{},
		AgentKeywords:   []string{},
		AgentTimestamp:  0,
		AgentL4Refs:     []uint64{},
		AgentL3Refs:     []uint64{},
		FusedKeywords:   []string{},
		ChildrenIDs:     []uint64{},
		CentroidPageRef: centroidRef,
		CreatedAt:       nowMs,
		UpdatedAt:       nowMs,
		Version:         1,
	}

	if err := record.WriteTopicSlot(deps.Engine, idHash, &topic); err != nil {
		return nil, err
	}

	// Update sparse index
	searchText := strutil.JoinStrings(topic.UserKeywords, " ")
	terms := index.Tokenize(searchText)
	deps.SparseIndex.AddDocument(idHash, terms, uint32(len(terms)))

	// Keep L2MetaIndex in sync: the candidate set of searchNormal is built
	// from L2Meta (depth<=2), which is otherwise only rebuilt on Open/Dream,
	// so a freshly created topic would be filtered out until then.
	if deps.L2Meta != nil {
		deps.L2Meta.Update(index.L2MetaFromTopic(&topic))
	}

	return &topic, nil
}

// storeQueryAsL4 creates an L4 archive for the search query and links it
// to the given L2 topic.
func storeQueryAsL4(q SearchQuery, deps *SearchDeps, topicID uint64) {
	if q.Text == "" {
		return
	}
	// Best-effort: store query as L4 archive; failure is non-critical.
	_, _ = crud.AppendDialogueL4(deps.Engine, deps.SparseIndex, topicID, q.Text, 0, deps.PreprocessedKeywords, q.Timestamp)
}

// --- helpers ---

func topicToContextResult(ctx *model.TopicSlot, score float32) ContextResult {
	var parentID *string
	if ctx.ParentID != nil {
		s := hash.FormatHash(*ctx.ParentID)
		parentID = &s
	}
	l4Refs := make([]string, 0, len(ctx.UserL4Refs)+len(ctx.AgentL4Refs))
	for _, r := range ctx.UserL4Refs {
		l4Refs = append(l4Refs, hash.FormatHash(r))
	}
	for _, r := range ctx.AgentL4Refs {
		l4Refs = append(l4Refs, hash.FormatHash(r))
	}
	l3Refs := make([]string, 0, len(ctx.UserL3Refs)+len(ctx.AgentL3Refs))
	for _, r := range ctx.UserL3Refs {
		l3Refs = append(l3Refs, hash.FormatHash(r))
	}
	for _, r := range ctx.AgentL3Refs {
		l3Refs = append(l3Refs, hash.FormatHash(r))
	}
	childIDs := make([]string, len(ctx.ChildrenIDs))
	for i, c := range ctx.ChildrenIDs {
		childIDs[i] = hash.FormatHash(c)
	}
	return ContextResult{
		ID:             hash.FormatHash(ctx.ID),
		ParentID:       parentID,
		Depth:          ctx.Depth,
		SceneID:        hash.FormatHash(ctx.SceneID),
		UserKeywords:   ctx.UserKeywords,
		UserTimestamp:  ctx.UserTimestamp,
		AgentKeywords:  ctx.AgentKeywords,
		AgentTimestamp: ctx.AgentTimestamp,
		FusedKeywords:  ctx.FusedKeywords,
		FusedSummary:   ctx.FusedSummary,
		ChildrenIDs:    childIDs,
		L4Refs:         l4Refs,
		L3Refs:         l3Refs,
		RetrievalScore: score,
	}
}

func collectL3Refs(ctx *model.TopicSlot) []string {
	refs := make([]string, 0, len(ctx.UserL3Refs)+len(ctx.AgentL3Refs))
	for _, r := range ctx.UserL3Refs {
		refs = append(refs, hash.FormatHash(r))
	}
	for _, r := range ctx.AgentL3Refs {
		refs = append(refs, hash.FormatHash(r))
	}
	return refs
}

func collectAllL3IDs(scored []scoredContext) []string {
	seen := make(map[string]struct{})
	var ids []string
	for _, sc := range scored {
		for _, r := range sc.topic.UserL3Refs {
			h := hash.FormatHash(r)
			if _, ok := seen[h]; !ok {
				seen[h] = struct{}{}
				ids = append(ids, h)
			}
		}
		for _, r := range sc.topic.AgentL3Refs {
			h := hash.FormatHash(r)
			if _, ok := seen[h]; !ok {
				seen[h] = struct{}{}
				ids = append(ids, h)
			}
		}
	}
	if ids == nil {
		return []string{}
	}
	return ids
}

func readProfileResult(deps *SearchDeps) ProfileResult {
	// Return cached profile if available, avoiding deserialization.
	if deps.ProfileCache != nil && *deps.ProfileCache != nil {
		return **deps.ProfileCache
	}
	profileHash := hash.HashID("profile")
	_, data, err := deps.Engine.ReadRecord(profileHash)
	if err != nil {
		return emptyProfile()
	}
	var p model.ProfileSlot
	if json.Unmarshal(data, &p) != nil {
		return emptyProfile()
	}
	pr := ProfileResult{
		ID:              hash.FormatHash(p.IDHash),
		Name:            p.Name,
		Role:            p.Role,
		Personality:     p.Personality,
		Worldview:       p.Worldview,
		Preferences:     p.Preferences,
		Lexicon:         p.Lexicon,
		StyleTraits:     p.StyleTraits,
		EmotionPatterns: p.EmotionPatterns,
		CreatedAt:       p.CreatedAt,
		UpdatedAt:       p.UpdatedAt,
	}
	// Populate cache for subsequent calls.
	if deps.ProfileCache != nil {
		*deps.ProfileCache = &pr
	}
	return pr
}

func emptyProfile() ProfileResult {
	return ProfileResult{
		Preferences:     make(map[string]string),
		Lexicon:         make(map[string]string),
		StyleTraits:     []string{},
		EmotionPatterns: make(map[string]string),
	}
}

// filterByMinScore removes candidates below the minimum score threshold.
func filterByMinScore(candidates []scoredContext, minScore float32) []scoredContext {
	var filtered []scoredContext
	for _, sc := range candidates {
		if sc.score >= minScore {
			filtered = append(filtered, sc)
		}
	}
	if filtered == nil {
		return []scoredContext{}
	}
	return filtered
}

// filterByLayers keeps only candidates whose depth matches one of the layers.
func filterByLayers(candidates []scoredContext, layers []uint8) []scoredContext {
	if len(layers) == 0 {
		return candidates
	}
	layerSet := make(map[uint8]struct{}, len(layers))
	for _, l := range layers {
		layerSet[l] = struct{}{}
	}
	var filtered []scoredContext
	for _, sc := range candidates {
		if _, ok := layerSet[sc.topic.Depth]; ok {
			filtered = append(filtered, sc)
		}
	}
	if filtered == nil {
		return []scoredContext{}
	}
	return filtered
}

// buildCandidateSetWithL3 builds candidate set, filtering by L3 ID if provided.
func buildCandidateSetWithL3(
	l2Meta *index.L2MetaIndex,
	sparse *index.SparseIndex,
	l3ID *string,
) map[uint64]struct{} {
	base := BuildCandidateSet(l2Meta, sparse, l3ID)
	return base
}

func emptyResult() *SearchResult {
	return &SearchResult{
		Profile:            emptyProfile(),
		Contexts:           []ContextResult{},
		AssociatedContexts: []ContextResult{},
		Crystals:           make([]crud.CrystalSummary, 0),
	}
}

// loadAndScoreContextsDepth1 loads TopicSlots and filters depth == 1 only.
func loadAndScoreContextsDepth1(
	engine *storage.StorageEngine,
	merged []index.ScoredDoc,
	limit int,
) []scoredContext {
	var result []scoredContext
	for _, doc := range merged {
		_, data, err := engine.ReadRecord(doc.IDHash)
		if err != nil {
			continue
		}
		var topic model.TopicSlot
		if json.Unmarshal(data, &topic) != nil || topic.Depth != 1 {
			continue
		}
		cr := topicToContextResult(&topic, doc.Score)
		t := topic // copy
		result = append(result, scoredContext{result: &cr, topic: &t, score: doc.Score})
		if len(result) >= limit {
			break
		}
	}
	return result
}

// matchActionChains finds L5 action chains matching the query text.
// buildSearchResult assembles the full SearchResult from scored contexts.
// It stores the query as L4 archive, builds associations, and returns the result.
func buildSearchResult(q SearchQuery, deps *SearchDeps, scored []scoredContext) (*SearchResult, error) {
	// Store query as L4 archive linked to the best-matching topic
	storeQueryAsL4(q, deps, scored[0].topic.ID)

	// Build associated contexts from L1 reverse index
	associated := getL1Associated(deps.Engine, scored, deps.L1Reverse)

	// Assemble result
	contexts := make([]ContextResult, len(scored))
	for i, sc := range scored {
		contexts[i] = *sc.result
	}

	result := &SearchResult{
		Profile:            readProfileResult(deps),
		Contexts:           contexts,
		AssociatedContexts: associated,
		Crystals:           []crud.CrystalSummary{},
	}
	result.Crystals = matchActionChains(deps.Engine, q.Text)
	return result, nil
}

func matchActionChains(engine *storage.StorageEngine, queryText string) []crud.CrystalSummary {
	matches := make([]crud.CrystalSummary, 0)
	terms := index.Tokenize(queryText)
	if len(terms) == 0 {
		return matches
	}
	_ = engine.IterIndexByType(storage.RecL5ActionChain, func(idHash uint64) error {
		_, data, err := engine.ReadRecord(idHash)
		if err != nil {
			return nil
		}
		var chain model.ActionChainSlot
		if json.Unmarshal(data, &chain) != nil {
			return nil
		}
		// Simple keyword match against title and trigger
		text := chain.Title + " " + chain.Trigger
		score := 0
		for _, term := range terms {
			if strings.Contains(strings.ToLower(text), term) {
				score++
			}
		}
		if score > 0 {
			var lastTriggered *int64
			if chain.LastTriggered > 0 {
				lastTriggered = &chain.LastTriggered
			}
			matches = append(matches, crud.CrystalSummary{
				ID:            hash.FormatHash(chain.IDHash),
				Title:         chain.Title,
				Condition:     chain.Trigger,
				Status:        chain.Status.String(),
				TriggerCount:  chain.TriggerCount,
				SuccessRate:   chain.SuccessRate,
				LastTriggered: lastTriggered,
				CreatedAt:     chain.CreatedAt,
			})
		}
		return nil
	})
	return matches
}

func float64Ptr(v float64) *float64 { return &v }

// BoostSearchResults re-scores contexts by SCENE (not by individual topic).
//
// Additive scene bonus (v0.58 unified):
//   - If ANY topic in the scene is activated → add ActivationBonus to each topic
//   - Else if the scene has the mostRecent topic → add RecentChatBonus to each topic
//   - Active takes priority; only one bonus can apply per scene.
//
// Contexts are then sorted by final score descending.
func BoostSearchResults(
	result *SearchResult,
	activeIDs []uint64,
	mostRecent *uint64,
	weights *config.SearchWeights,
) {
	if weights == nil {
		return
	}
	activeSet := make(map[uint64]struct{}, len(activeIDs))
	for _, id := range activeIDs {
		activeSet[id] = struct{}{}
	}

	applyAdditiveBoost(result.Contexts, activeSet, mostRecent, weights)
	applyAdditiveBoost(result.AssociatedContexts, activeSet, mostRecent, weights)
	sortByScore(result.Contexts)
	sortByScore(result.AssociatedContexts)
}

// applyAdditiveBoost adds a fixed bonus to each topic's RetrievalScore based on
// scene-level activation state. Mutual exclusion: active takes priority over recent.
func applyAdditiveBoost(
	contexts []ContextResult,
	activeSet map[uint64]struct{},
	mostRecent *uint64,
	weights *config.SearchWeights,
) {
	if len(contexts) == 0 {
		return
	}

	// Step 1: Determine per-scene activation state.
	type sceneState struct {
		hasActivated bool
		isRecent     bool
	}
	sceneMap := make(map[uint64]*sceneState)
	for i := range contexts {
		ctx := &contexts[i]
		ctxHash, err := hash.ParseID(ctx.ID)
		if err != nil {
			continue
		}
		sceneHash, err := hash.ParseID(ctx.SceneID)
		if err != nil {
			continue
		}
		ss, exists := sceneMap[sceneHash]
		if !exists {
			ss = &sceneState{}
			sceneMap[sceneHash] = ss
		}
		if _, ok := activeSet[ctxHash]; ok {
			ss.hasActivated = true
		}
		if mostRecent != nil && *mostRecent == ctxHash {
			ss.isRecent = true
		}
	}

	// Step 2: Apply additive bonus per topic.
	for i := range contexts {
		sceneHash, err := hash.ParseID(contexts[i].SceneID)
		if err != nil {
			continue
		}
		ss := sceneMap[sceneHash]
		if ss == nil {
			continue
		}
		if ss.hasActivated {
			contexts[i].RetrievalScore += weights.ActivationBonus
		} else if ss.isRecent {
			contexts[i].RetrievalScore += weights.RecentChatBonus
		}
	}
}

func sortByScore(contexts []ContextResult) {
	sort.Slice(contexts, func(i, j int) bool {
		return contexts[i].RetrievalScore > contexts[j].RetrievalScore
	})
}
