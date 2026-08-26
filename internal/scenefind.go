// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package sub scores all L2 topics via three concurrent channels (BM25 +
// vector + entity), fuses them with RRF, adds keyword overlap, then
// aggregates by scene with bonuses (active +0.2, latest-timestamp +0.1).
package internal

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
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
// l2Meta supplies the candidate topics from the in-memory cache (nil falls
// back to a full record scan). Scoring constants (RRF k, bonuses, vector
// floor scale) are package-private tuning constants; threshold stays an
// explicit parameter so tests can exercise it.
func TopScene(ctx context.Context, engine *core.StorageEngine, l2Meta *index.L2MetaIndex,
	sparse *index.SparseIndex,
	enc Encoder, query string, keywords []string,
	activeSceneIDs []uint64, threshold float32, l3ID *string) (SceneHit, error) {
	topics, err := repo.ListTopicsL2(repo.TopicListQuery{
		Engine:  engine,
		MetaIdx: l2Meta,
		SceneID: "",
		Depth:   2,
		Num:     1,
	}) // all scenes, depth<=2, by UserTimestamp
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
	rrf := rrfFuse(bm25Docs, vecDocs, entDocs)
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

	// Vector floor: a semantic fallback that only lifts below-threshold
	// scenes. When keyword/entity channels score nothing, a topic with
	// cosine >= vectorMinScore floors its scene to
	// threshold + cosine*vectorFloorScale so the scene still enters
	// contention. Scenes already above threshold are never touched — their
	// ordering comes from real signals (RRF + keyword overlap + bonuses),
	// not from the fallback.
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
	if len(sceneScores) == 0 {
		return SceneHit{}, nil
	}

	// Scene bonuses: active scenes +0.2, latest-timestamp scene +0.1 (one per scene, active wins).
	activeSet := make(map[uint64]struct{}, len(activeSceneIDs))
	for _, sid := range activeSceneIDs {
		activeSet[sid] = struct{}{}
	}
	lastSceneID := topics[len(topics)-1].SceneID // last item = max UserTimestamp
	applySceneBonuses(sceneScores, activeSet, lastSceneID)

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
	slices.SortStableFunc(best.Topics, func(a, b ScoredTopic) int {
		if a.Score != b.Score {
			return cmp.Compare(b.Score, a.Score) // higher score first
		}
		return cmp.Compare(a.Topic.ID, b.Topic.ID) // ties by ID for determinism
	})
	return best, nil
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

func filterByL3(topics []core.TopicSlot, l3Hash uint64) []core.TopicSlot {
	var filtered []core.TopicSlot
	for _, t := range topics {
		if slices.Contains(t.L3Refs, l3Hash) {
			filtered = append(filtered, t)
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
	slices.SortFunc(docs, func(a, b index.ScoredDoc) int {
		return cmp.Compare(b.Score, a.Score)
	})
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
	var centroidBuf []float32 // reused across topics to avoid one allocation per centroid
	var docs []index.ScoredDoc
	for _, t := range topics {
		if t.CentroidPageRef == 0 {
			continue
		}
		_, vecData, err := engine.ReadRecord(core.DefaultAgentID, t.CentroidPageRef)
		if err != nil || len(vecData) < len(queryVec)*4 {
			continue
		}
		centroid, err := common.DecodeF32VecInto(vecData, len(queryVec), centroidBuf)
		if err != nil {
			continue
		}
		centroidBuf = centroid
		if sc := common.CosineSimilarity(queryVec, centroid); sc > 0 {
			docs = append(docs, index.ScoredDoc{IDHash: t.ID, Score: sc})
		}
	}
	slices.SortFunc(docs, func(a, b index.ScoredDoc) int {
		return cmp.Compare(b.Score, a.Score)
	})
	return docs
}

func rrfFuse(rankedLists ...[]index.ScoredDoc) map[uint64]float32 {
	scores := make(map[uint64]float32)
	for _, docs := range rankedLists {
		for i, doc := range docs {
			scores[doc.IDHash] += 1.0 / (rrfK + float32(i+1))
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

// SpreadingActivation walks the L1 scene hypergraph from startSceneID and
// returns the most strongly activated other scenes with their depth<=1
// topics, ordered by activation (desc). Activation starts at 1.0 at the
// source node and propagates along hyperedges as act × edge.Weight ×
// dampening per hop; paths below the activation threshold stop spreading and
// the walk never exceeds max hops. The start scene itself is never returned.
// A scene without an L1 node (created after the last Dream) yields an empty
// result. The walk is a pure storage-level graph read — no in-memory graph
// index is maintained. All walk limits are package-private tuning constants.
func SpreadingActivation(engine *core.StorageEngine, l2Meta *index.L2MetaIndex,
	startSceneID uint64) []SceneHit {
	maxHops, dampening, threshold, maxScenes := l1EdgeMaxHops,
		l1ActivationDampening, l1ActivationThreshold, l1AssocMaxScenes
	if maxHops <= 0 || maxScenes <= 0 || dampening <= 0 {
		return nil
	}
	startNodeID := core.SceneNodeID(startSceneID)
	if _, err := core.ReadSceneNode(engine, core.DefaultAgentID, startNodeID); err != nil {
		return nil // never dreamed; nothing associated yet
	}

	type entry struct {
		nodeID uint64
		act    float32
		hops   int
	}
	queue := []entry{{nodeID: startNodeID, act: 1.0}}
	sceneAct := make(map[uint64]float32)
	for len(queue) > 0 {
		e := queue[0]
		queue = queue[1:]
		if e.hops >= maxHops {
			continue
		}
		node, err := core.ReadSceneNode(engine, core.DefaultAgentID, e.nodeID)
		if err != nil {
			continue
		}
		for _, edgeID := range node.EdgeIDs {
			edge, err := core.ReadSceneEdge(engine, core.DefaultAgentID, edgeID)
			if err != nil {
				continue
			}
			for _, neighborID := range edge.NodeIDs {
				if neighborID == e.nodeID {
					continue
				}
				act := e.act * edge.Weight * dampening
				if act < threshold {
					continue
				}
				neighbor, err := core.ReadSceneNode(engine, core.DefaultAgentID, neighborID)
				if err != nil {
					continue
				}
				if neighbor.SceneID != startSceneID && act > sceneAct[neighbor.SceneID] {
					sceneAct[neighbor.SceneID] = act
				}
				queue = append(queue, entry{nodeID: neighborID, act: act, hops: e.hops + 1})
			}
		}
	}
	if len(sceneAct) == 0 {
		return nil
	}

	ids := make([]uint64, 0, len(sceneAct))
	for sid := range sceneAct {
		ids = append(ids, sid)
	}
	slices.SortFunc(ids, func(a, b uint64) int {
		if sceneAct[a] != sceneAct[b] {
			return cmp.Compare(sceneAct[b], sceneAct[a]) // higher activation first
		}
		return cmp.Compare(a, b) // ties by scene ID for determinism
	})
	if len(ids) > maxScenes {
		ids = ids[:maxScenes]
	}
	hits := make([]SceneHit, 0, len(ids))
	for _, sid := range ids {
		topics, err := repo.ListTopicsL2(repo.TopicListQuery{
			Engine:  engine,
			MetaIdx: l2Meta,
			SceneID: common.FormatHash(sid),
			Depth:   1,
			Num:     2,
		})
		if err != nil {
			continue
		}
		scored := make([]ScoredTopic, 0, len(topics))
		for _, t := range topics {
			scored = append(scored, ScoredTopic{Topic: t, Score: sceneAct[sid]})
		}
		hits = append(hits, SceneHit{SceneID: sid, Score: sceneAct[sid], Topics: scored})
	}
	return hits
}
