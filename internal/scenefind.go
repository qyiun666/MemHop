// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package sub scores all L2 topics via three concurrent channels (BM25 +
// vector + entity), fuses them with RRF, adds keyword overlap, then
// aggregates by scene with bonuses (active +0.2, latest-timestamp +0.1).
package internal

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// SceneHit is the retrieval result: the winning scene, its aggregated
// score, and the scene's topics ordered by fused relevance.
type SceneHit struct {
	SceneID uint64
	Score   float32
	Topics  []ScoredTopic
}

// ScoredTopic is one topic of the hit scene with its fused relevance score.
type ScoredTopic struct {
	Topic core.TopicSlot
	Score float32
}

// Encoder is the text encoding interface for the vector channel, injected by the host.
type Encoder interface {
	Encode(text string) ([]float32, error)
	IsAvailable() bool
}

// TopScene scores all L2 topics via three concurrent channels, aggregates
// by scene with bonuses, and returns the top scene above threshold.
// activeSceneIDs get +0.2 once; l3ID restricts topics referencing that L3.
func TopScene(ctx context.Context, engine *core.StorageEngine, sparse *index.SparseIndex,
	enc Encoder, query string, keywords []string,
	activeSceneIDs []uint64, defaults *MemHopDefaults, threshold float32, l3ID *string) (SceneHit, error) {
	topics, err := repo.ListTopicsL2(engine, "", 2, 1) // all scenes, depth<=2, by UserTimestamp
	if err != nil {
		return SceneHit{}, err
	}
	if l3ID != nil {
		target, err := common.ParseID(*l3ID)
		if err != nil {
			return SceneHit{}, common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
		}
		topics = filterByL3(topics, target)
	}
	if len(topics) == 0 {
		return SceneHit{}, nil
	}

	searchText := strings.Join(keywords, " ")
	if query != "" {
		if searchText != "" {
			searchText = query + " " + searchText
		} else {
			searchText = query
		}
	}

	// Three channels score concurrently with no shared writes; SparseIndex is
	// self-locked and ReadRecord is read-only.
	var wg sync.WaitGroup
	var bm25Docs, vecDocs, entDocs []index.ScoredDoc
	wg.Add(3)
	go func() {
		defer wg.Done()
		bm25Docs = retrieveBM25(sparse, topics, searchText)
	}()
	go func() {
		defer wg.Done()
		vecDocs = retrieveVector(engine, enc, topics, searchText)
	}()
	go func() {
		defer wg.Done()
		entDocs = sparse.EntitySearch(searchText)
	}()
	wg.Wait()

	// RRF fusion (k=60, equal weights): score(id) = sum 1/(k+rank).
	rrf := rrfFuse(defaults.RRFK, bm25Docs, vecDocs, entDocs)
	if len(rrf) == 0 {
		return SceneHit{}, nil
	}

	kwSet := keywordSet(keywords)
	byID := make(map[uint64]core.TopicSlot, len(topics))
	for _, t := range topics {
		byID[t.ID] = t
	}
	sceneScores := make(map[uint64]float32)
	topicScores := make(map[uint64]float32)
	for id, r := range rrf {
		t, ok := byID[id]
		if !ok {
			continue
		}
		sc := r + keywordHit(t, kwSet)
		sceneScores[t.SceneID] += sc
		topicScores[id] = sc
	}

	// Vector floor: when keyword overlap is zero, BM25/entity score nothing,
	// so topics with cosine >= VectorMinScore get their scene floored to
	// threshold + similarity, preserving semantic recall.
	for _, d := range vecDocs {
		if d.Score < defaults.VectorMinScore {
			continue
		}
		t, ok := byID[d.IDHash]
		if !ok {
			continue
		}
		floor := threshold + d.Score
		if sceneScores[t.SceneID] < floor {
			sceneScores[t.SceneID] = floor
		}
	}
	if len(sceneScores) == 0 {
		return SceneHit{}, nil
	}

	// Scene bonuses: active scenes +0.2, latest-timestamp scene +0.1 (one per scene, active wins).
	activeSet := make(map[uint64]struct{}, len(activeSceneIDs))
	for _, sid := range activeSceneIDs {
		activeSet[sid] = struct{}{}
	}
	lastSceneID := topics[len(topics)-1].SceneID // last item = max UserTimestamp
	applySceneBonuses(sceneScores, activeSet, lastSceneID, defaults)

	var best SceneHit
	for sid, sc := range sceneScores {
		if sc > best.Score || (sc == best.Score && sid < best.SceneID) {
			best = SceneHit{SceneID: sid, Score: sc}
		}
	}
	if best.Score <= threshold {
		return SceneHit{}, nil
	}

	// Winning scene's topics, ordered by fused relevance (ties by ID for
	// determinism); topics with no channel hit still carry their keyword score.
	best.Topics = make([]ScoredTopic, 0, len(byID))
	for id, t := range byID {
		if t.SceneID != best.SceneID {
			continue
		}
		best.Topics = append(best.Topics, ScoredTopic{Topic: t, Score: topicScores[id]})
	}
	sort.Slice(best.Topics, func(i, j int) bool {
		if best.Topics[i].Score != best.Topics[j].Score {
			return best.Topics[i].Score > best.Topics[j].Score
		}
		return best.Topics[i].Topic.ID < best.Topics[j].Topic.ID
	})
	return best, nil
}

// applySceneBonuses adds ActivationBonus (active scenes) and RecentChatBonus
// (latest-timestamp scene), one per scene, active first.
func applySceneBonuses(scores map[uint64]float32, activeSet map[uint64]struct{}, lastSceneID uint64, defaults *MemHopDefaults) {
	for sid := range activeSet {
		if _, ok := scores[sid]; ok {
			scores[sid] += defaults.ActivationBonus
		}
	}
	if _, ok := scores[lastSceneID]; ok {
		if _, isActive := activeSet[lastSceneID]; !isActive {
			scores[lastSceneID] += defaults.RecentChatBonus
		}
	}
}

func filterByL3(topics []core.TopicSlot, l3Hash uint64) []core.TopicSlot {
	var filtered []core.TopicSlot
	for _, t := range topics {
		for _, ref := range t.L3Refs {
			if ref == l3Hash {
				filtered = append(filtered, t)
				break
			}
		}
	}
	return filtered
}

func retrieveBM25(sparse *index.SparseIndex, topics []core.TopicSlot, text string) []index.ScoredDoc {
	terms := index.Tokenize(text)
	if len(terms) == 0 {
		return nil
	}
	var docs []index.ScoredDoc
	for _, t := range topics {
		if sc := sparse.BM25Score(terms, t.ID); sc > 0 {
			docs = append(docs, index.ScoredDoc{IDHash: t.ID, Score: sc})
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Score > docs[j].Score })
	return docs
}

// retrieveVector encodes the query and computes cosine similarity against
// each topic centroid; the channel is empty when the encoder is unavailable.
func retrieveVector(engine *core.StorageEngine, enc Encoder,
	topics []core.TopicSlot, text string) []index.ScoredDoc {
	if enc == nil || !enc.IsAvailable() {
		return nil
	}
	queryVec, err := enc.Encode(text)
	if err != nil {
		slog.Warn("scenefind: vector channel encode failed, skipped", "error", err)
		return nil
	}
	if len(queryVec) == 0 {
		return nil
	}
	var docs []index.ScoredDoc
	for _, t := range topics {
		if t.CentroidPageRef == 0 {
			continue
		}
		_, vecData, err := engine.ReadRecord(t.CentroidPageRef)
		if err != nil || len(vecData) < len(queryVec)*4 {
			continue
		}
		centroid := common.DecodeF32Vec(vecData, len(queryVec))
		if len(centroid) != len(queryVec) {
			continue
		}
		if sc := common.CosineSimilarity(queryVec, centroid); sc > 0 {
			docs = append(docs, index.ScoredDoc{IDHash: t.ID, Score: sc})
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Score > docs[j].Score })
	return docs
}

func rrfFuse(k float32, rankedLists ...[]index.ScoredDoc) map[uint64]float32 {
	scores := make(map[uint64]float32)
	for _, docs := range rankedLists {
		for i, doc := range docs {
			scores[doc.IDHash] += 1.0 / (k + float32(i+1))
		}
	}
	return scores
}

func keywordSet(keywords []string) map[string]struct{} {
	set := make(map[string]struct{}, len(keywords))
	for _, kw := range keywords {
		set[strings.ToLower(kw)] = struct{}{}
	}
	return set
}

// keywordHit computes the overlap ratio of request keywords across the
// topic's Fused/User/Agent keywords.
func keywordHit(topic core.TopicSlot, kwSet map[string]struct{}) float32 {
	if len(kwSet) == 0 {
		return 0
	}
	fields := make([]string, 0, len(topic.FusedKeywords)+len(topic.UserKeywords)+len(topic.AgentKeywords))
	fields = append(fields, topic.FusedKeywords...)
	fields = append(fields, topic.UserKeywords...)
	fields = append(fields, topic.AgentKeywords...)
	seen := make(map[string]struct{}, len(fields))
	hit := 0
	for _, kw := range fields {
		k := strings.ToLower(kw)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		if _, ok := kwSet[k]; ok {
			hit++
		}
	}
	return float32(hit) / float32(len(kwSet))
}
