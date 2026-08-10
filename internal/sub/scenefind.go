// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package sub 对全量 L2 话题做三重并发检索打分（BM25 + 向量 + 实体），
// 三通道 RRF 融合后叠加请求关键词重合分得到话题得分；按相同场景 ID 将话题
// 得分相加，再对场景加分（激活场景 +0.2、时间戳最后话题所在场景 +0.1，
// 每场景至多一种、激活优先）；返回得分高于阈值的最高分场景，无命中或全部
// 不高于阈值时返回空。
package sub

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
	"github.com/qyiun666/MemHop/internal/sub/repo/index"
)

// SceneHit 检索结果：场景 ID 及其聚合得分。
type SceneHit struct {
	SceneID uint64
	Score   float32
}

// Encoder 文本编码接口（向量通道）：由调用方适配注入，scenefind 不依赖 query 层。
type Encoder interface {
	Encode(text string) ([]float32, error)
	IsAvailable() bool
}

// TopScene 对全量 L2 话题三重并发检索打分，按场景聚合得分并加场景分，
// 返回得分高于 threshold 的最高分场景；无命中或全部不高于阈值时返回零值 SceneHit。
// activeSceneIDs 为 DB.activeScenes（激活场景，每场景 +0.2，仅一次）。
// l3ID 非 nil 时只对 L3Refs 含目标 L3 的话题检索（nil = 全量）。
func TopScene(ctx context.Context, engine *core.StorageEngine, sparse *index.SparseIndex,
	enc Encoder, query string, keywords []string,
	activeSceneIDs []uint64, defaults *MemHopDefaults, threshold float32, l3ID *string) (SceneHit, error) {
	topics, err := repo.ListTopicsL2(engine, "", 2, 1) // 全部场景 depth<=2 的话题，按 UserTimestamp 升序
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

	// 三通道并发检索打分：各自写独立结果，无共享写；
	// SparseIndex 方法自带 RWMutex、ReadRecord 只读，并发安全。
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

	// RRF 融合（k=60，三通道等权）：score(id) = Σ 1/(k+rank)。
	rrf := rrfFuse(defaults.RRFK, bm25Docs, vecDocs, entDocs)
	if len(rrf) == 0 {
		return SceneHit{}, nil
	}

	// 关键词重合分 + 按场景聚合话题得分。
	kwSet := keywordSet(keywords)
	byID := make(map[uint64]core.TopicSlot, len(topics))
	for _, t := range topics {
		byID[t.ID] = t
	}
	sceneScores := make(map[uint64]float32)
	for id, r := range rrf {
		t, ok := byID[id]
		if !ok {
			continue
		}
		sceneScores[t.SceneID] += r + keywordHit(t, kwSet)
	}
	if len(sceneScores) == 0 {
		return SceneHit{}, nil
	}

	// 场景加分：激活场景 +0.2、时间戳最后话题所在场景 +0.1（每场景至多一种、激活优先）。
	activeSet := make(map[uint64]struct{}, len(activeSceneIDs))
	for _, sid := range activeSceneIDs {
		activeSet[sid] = struct{}{}
	}
	lastSceneID := topics[len(topics)-1].SceneID // ListAllTopics 末位 = UserTimestamp 最大
	applySceneBonuses(sceneScores, activeSet, lastSceneID, defaults)

	// 取最高分场景；同分取 SceneID 小者保证确定性。
	var best SceneHit
	for sid, sc := range sceneScores {
		if sc > best.Score || (sc == best.Score && sid < best.SceneID) {
			best = SceneHit{SceneID: sid, Score: sc}
		}
	}
	if best.Score <= threshold {
		return SceneHit{}, nil
	}
	return best, nil
}

// applySceneBonuses 对场景得分加分：激活场景 +defaults.ActivationBonus（同一场景仅一次），
// 时间戳最后话题所在场景 +defaults.RecentChatBonus（同一场景仅一次）；激活优先，每场景至多一种加分。
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

// filterByL3 保留 L3Refs 含目标 L3 的话题，保持原顺序。
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

// retrieveBM25 以查询文本分词对每个话题计算 BM25 得分，取 >0 结果按分降序。
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

// retrieveVector 编码查询文本后对每个话题的质心向量计算余弦相似度，
// 取 >0 结果按分降序；编码器不可用或编码失败时通道为空。
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

// rrfFuse 将多路按得分降序的排名列表按 RRF 融合为 id → 分数映射。
func rrfFuse(k float32, rankedLists ...[]index.ScoredDoc) map[uint64]float32 {
	scores := make(map[uint64]float32)
	for _, docs := range rankedLists {
		for i, doc := range docs {
			scores[doc.IDHash] += 1.0 / (k + float32(i+1))
		}
	}
	return scores
}

// keywordSet 将请求关键词小写归一并去重。
func keywordSet(keywords []string) map[string]struct{} {
	set := make(map[string]struct{}, len(keywords))
	for _, kw := range keywords {
		set[strings.ToLower(kw)] = struct{}{}
	}
	return set
}

// keywordHit 计算话题 3 个 []string 检索字段（FusedKeywords ∪ UserKeywords ∪
// AgentKeywords）与请求关键词的重合比例：命中数 / 请求关键词数（去重后）。
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
