// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package scenefind is the scene-scoring capability: a stateless pipeline
// over injected storage primitives (engine, sparse/L2Meta indices, host
// encoder). TopScene orchestrates candidate gathering, three-channel
// retrieval (retrieve.go), RRF fusion, scene aggregation with bonuses and
// the vector floor; the L1 activation walk lives in activation.go. It owns
// its retrieval tuning constants and imports no upper layer.

package scenefind

import (
	"cmp"
	"context"
	"slices"
	"sync"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// Result shapes of this capability (identity aliases of the bottom-layer
// DTOs, so callers above can name them without importing core).
type (
	SceneHit    = core.SceneHit
	ScoredTopic = core.ScoredTopic
)

// Encoder is the text encoding interface for the vector channel, injected by
// the composition root (the host's encoder satisfies it structurally).
type Encoder interface {
	Encode(text string) ([]float32, error)
	IsAvailable() bool
}

// Retrieval tuning constants (previously internal/tuning.go): RRF fusion,
// scene bonuses and the vector fallback floor.
const (
	rrfK             float32 = 60.0 // RRF fusion constant
	activationBonus  float32 = 0.2  // active-scene score bonus
	recentChatBonus  float32 = 0.1  // latest-timestamp scene score bonus
	vectorMinScore   float32 = 0.5  // cosine floor for the vector fallback
	vectorFloorScale float32 = 0.5  // vector floor = threshold + cosine*scale (kept below RRF+keyword reach)
)

// TopScene scores all L2 topics via three concurrent channels, aggregates
// by scene with bonuses, and returns the top scene above threshold.
// activeSceneIDs get +0.2 once; l3ID restricts topics referencing that L3;
// sceneL3ID restricts candidate topics to scenes whose organizational L3
// domain matches (backfilled by SetSceneL3ID). l2Meta supplies the candidate
// topics from the in-memory cache (nil falls back to a full record scan).
// Scoring constants (RRF k, bonuses, vector floor scale) are package-private
// tuning constants; threshold stays an explicit parameter so tests can
// exercise it.
func TopScene(ctx context.Context, agentID uint64, engine *core.StorageEngine, l2Meta *index.L2MetaIndex,
	sparse *index.SparseIndex,
	enc Encoder, query string, keywords []string,
	activeSceneIDs []uint64, threshold float32, l3ID *string, sceneL3ID *string) (SceneHit, error) {
	topics, err := candidateTopics(agentID, engine, l2Meta, l3ID, sceneL3ID)
	if err != nil {
		return SceneHit{}, err
	}
	if len(topics) == 0 {
		return SceneHit{}, nil
	}
	bm25Docs, vecDocs, entDocs := runChannels(agentID, engine, sparse, enc, topics, buildSearchText(query, keywords))
	rrf := rrfFuse(bm25Docs, vecDocs, entDocs)
	if len(rrf) == 0 {
		return SceneHit{}, nil
	}
	byID := indexTopicsByID(topics)
	sceneScores, topicScores := scoreScenes(rrf, byID, keywordSet(keywords))
	// Vector floor: a semantic fallback that only lifts below-threshold
	// scenes (see applyVectorFloor for the exact rule).
	applyVectorFloor(sceneScores, byID, vecDocs, threshold)
	if len(sceneScores) == 0 {
		return SceneHit{}, nil
	}
	// Scene bonuses: active scenes +0.2, latest-timestamp scene +0.1 (one per scene, active wins).
	activeSet := make(map[uint64]struct{}, len(activeSceneIDs))
	for _, sid := range activeSceneIDs {
		activeSet[sid] = struct{}{}
	}
	applySceneBonuses(sceneScores, activeSet, topics[len(topics)-1].SceneID) // last item = max UserTimestamp
	best, ok := pickBestScene(sceneScores, threshold)
	if !ok {
		return SceneHit{}, nil
	}
	best.Topics = sceneTopicsRanked(byID, best.SceneID, topicScores)
	return best, nil
}

// candidateTopics lists all depth<=2 topics (by UserTimestamp) and, when an
// L3 filter is given, keeps only topics referencing that hypergraph. When a
// sceneL3ID is given, topics are first narrowed to scenes whose
// organizational L3 domain (backfilled by SetSceneL3ID) matches.
func candidateTopics(agentID uint64, engine *core.StorageEngine, l2Meta *index.L2MetaIndex, l3ID *string, sceneL3ID *string) ([]core.TopicSlot, error) {
	topics, err := repo.ListTopicsL2(repo.TopicListQuery{
		Engine:  engine,
		AgentID: agentID,
		MetaIdx: l2Meta,
		SceneID: 0,
		Depth:   2,
		Num:     1,
	}) // all scenes, depth<=2, by UserTimestamp
	if err != nil {
		return nil, err
	}
	if sceneL3ID != nil {
		sceneHash, err := common.ParseID(*sceneL3ID)
		if err != nil {
			return nil, common.NewError(common.ErrInvalidQuery, "parse scene l3 id", err)
		}
		keep := make(map[uint64]struct{})
		for _, sc := range repo.CollectAllScenesL2(engine, agentID) {
			if sc.L3ID == sceneHash {
				keep[sc.SceneID] = struct{}{}
			}
		}
		filtered := topics[:0]
		for _, t := range topics {
			if _, ok := keep[t.SceneID]; ok {
				filtered = append(filtered, t)
			}
		}
		topics = filtered
	}
	if l3ID == nil {
		return topics, nil
	}
	target, err := common.ParseID(*l3ID)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
	}
	return filterByL3(topics, target), nil
}

// runChannels scores through the three retrieval channels concurrently;
// they share no writes (SparseIndex is self-locked, ReadRecord is read-only).
func runChannels(agentID uint64, engine *core.StorageEngine, sparse *index.SparseIndex,
	enc Encoder, topics []core.TopicSlot, searchText string) (bm25Docs, vecDocs, entDocs []index.ScoredDoc) {
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		bm25Docs = retrieveBM25(sparse, topics, searchText)
	}()
	go func() {
		defer wg.Done()
		vecDocs = retrieveVector(agentID, engine, enc, topics, searchText)
	}()
	go func() {
		defer wg.Done()
		entDocs = sparse.EntitySearch(searchText)
	}()
	wg.Wait()
	return bm25Docs, vecDocs, entDocs
}

// scoreScenes distributes each fused topic score (RRF + keyword overlap)
// onto its scene aggregate and per-topic record.
func scoreScenes(rrf map[uint64]float32, byID map[uint64]core.TopicSlot, kwSet map[string]struct{}) (map[uint64]float32, map[uint64]float32) {
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
	return sceneScores, topicScores
}

// applyVectorFloor lifts below-threshold scenes from the vector channel
// only: when keyword/entity channels score nothing, a topic with
// cosine >= vectorMinScore floors its scene to threshold + cosine *
// vectorFloorScale so the scene still enters contention. Scenes already
// above threshold keep their real-signal score (RRF + keyword overlap +
// bonuses), never a fallback value.
func applyVectorFloor(sceneScores map[uint64]float32, byID map[uint64]core.TopicSlot, vecDocs []index.ScoredDoc, threshold float32) {
	for _, d := range vecDocs {
		if d.Score < vectorMinScore {
			continue
		}
		t, ok := byID[d.IDHash]
		if !ok {
			continue
		}
		if sceneScores[t.SceneID] > threshold {
			continue // already above threshold: keep real-signal score
		}
		floor := threshold + d.Score*vectorFloorScale
		if sceneScores[t.SceneID] < floor {
			sceneScores[t.SceneID] = floor
		}
	}
}

// pickBestScene returns the highest-scoring scene (ties by lower scene ID
// for determinism); ok is false when nothing clears the threshold.
func pickBestScene(sceneScores map[uint64]float32, threshold float32) (SceneHit, bool) {
	var best SceneHit
	for sid, sc := range sceneScores {
		if sc > best.Score || (sc == best.Score && sid < best.SceneID) {
			best = SceneHit{SceneID: sid, Score: sc}
		}
	}
	if best.Score <= threshold {
		return SceneHit{}, false
	}
	return best, true
}

// sceneTopicsRanked returns the winning scene's topics ordered by fused
// relevance (ties by ID for determinism); topics with no channel hit still
// carry their keyword score.
func sceneTopicsRanked(byID map[uint64]core.TopicSlot, sceneID uint64, topicScores map[uint64]float32) []ScoredTopic {
	topics := make([]ScoredTopic, 0, len(byID))
	for id, t := range byID {
		if t.SceneID != sceneID {
			continue
		}
		topics = append(topics, ScoredTopic{Topic: t, Score: topicScores[id]})
	}
	slices.SortStableFunc(topics, func(a, b ScoredTopic) int {
		if a.Score != b.Score {
			return cmp.Compare(b.Score, a.Score) // higher score first
		}
		return cmp.Compare(a.Topic.ID, b.Topic.ID) // ties by ID for determinism
	})
	return topics
}

// applySceneBonuses adds ActivationBonus (active scenes) and RecentChatBonus
// (latest-timestamp scene), one per scene, active first.
func applySceneBonuses(scores map[uint64]float32, activeSet map[uint64]struct{}, lastSceneID uint64) {
	for sid := range activeSet {
		if _, ok := scores[sid]; ok {
			scores[sid] += activationBonus
		}
	}
	if _, ok := scores[lastSceneID]; ok {
		if _, isActive := activeSet[lastSceneID]; !isActive {
			scores[lastSceneID] += recentChatBonus
		}
	}
}
