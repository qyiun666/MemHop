// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search implementation of the sub layer: three-route dispatch
// (auto_create, directed, retrieval) as methods on DB.

package sub

import (
	"context"
	"fmt"
	"strings"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
	"github.com/qyiun666/MemHop/internal/sub/repo/index"
)

// SearchQuery is the search request.
type SearchQuery struct {
	Text         string  `json:"text"`
	DirectedL2ID *string `json:"directed_l2_id,omitempty"`
	DirectedL3ID *string `json:"directed_l3_id,omitempty"`
	AutoCreate   bool    `json:"auto_create,omitempty"`
	Timestamp    int64   `json:"timestamp"`
}

// SearchResult is the top-level search response.
type SearchResult struct {
	Profile            core.ProfileSlot       `json:"profile"`
	Contexts           []core.TopicSlot       `json:"contexts"`
	AssociatedContexts []core.TopicSlot       `json:"associated_contexts"`
	Crystals           []core.ActionChainSlot `json:"crystals"`
	NewTopicID         uint64                 `json:"new_topic_id,omitempty"`
}

// Search 三路由检索：AutoCreate 直建、DirectedL2ID 定向、默认检索
// （DirectedL3ID 非空时限定含该 L3 的话题）。LLM 关键词提取失败即返回错误，不降级分词。
func (db *DB) Search(q SearchQuery) (*SearchResult, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	if q.Timestamp <= 0 {
		return nil, common.NewError(common.ErrInvalidQuery,
			"SearchQuery.Timestamp is required (Unix milliseconds)")
	}
	keywords, err := db.llm.ExtractKeywords(context.Background(), q.Text)
	if err != nil {
		return nil, err
	}
	var (
		contexts   []core.TopicSlot
		newTopicID uint64
	)
	switch {
	case q.AutoCreate:
		contexts, newTopicID, err = db.searchAutoCreate(q, keywords)
	case q.DirectedL2ID != nil:
		contexts, newTopicID, err = db.searchDirected(q, keywords)
	default:
		contexts, newTopicID, err = db.searchNormal(q, keywords)
	}
	if err != nil {
		return nil, err
	}
	// 新建话题写入稀疏索引（仅本轮新建时，命中场景复用既有索引）。
	if newTopicID != 0 {
		terms := index.Tokenize(strings.Join(keywords, " "))
		db.sparseIndex.AddDocument(newTopicID, terms, uint32(len(terms)))
	}
	// L1 管理的关联话题：统一在主流程计算。
	var associated []core.TopicSlot
	if len(contexts) > 0 {
		associated = db.associatedContexts(contexts[0].SceneID)
	}
	return db.assembleResult(q, contexts, associated, newTopicID)
}

// searchAutoCreate 直接创建新场景与新话题，跳过检索，返回该场景 depth<=1 的话题列表。
func (db *DB) searchAutoCreate(q SearchQuery, keywords []string) ([]core.TopicSlot, uint64, error) {
	topics, topicID, err := db.createTopicInScene(q, keywords, 0)
	if err != nil {
		return nil, 0, err
	}
	return topics, topicID, nil
}

// searchDirected 在指定场景下创建新话题与 L4 归档，返回该场景 depth<=1 的话题列表。
func (db *DB) searchDirected(q SearchQuery, keywords []string) ([]core.TopicSlot, uint64, error) {
	sceneID, err := common.ParseID(*q.DirectedL2ID)
	if err != nil {
		return nil, 0, err
	}
	topics, topicID, err := db.createTopicInScene(q, keywords, sceneID)
	if err != nil {
		return nil, 0, err
	}
	return topics, topicID, nil
}

// searchNormal 三通道检索（DirectedL3ID 非空时限定含该 L3 的话题）：
// 在评分最高场景（无命中时新建场景）下新建话题与 L4 归档，
// 返回该场景 depth<=1 的话题列表与新建话题 ID。
func (db *DB) searchNormal(q SearchQuery, keywords []string) ([]core.TopicSlot, uint64, error) {
	hit, err := TopScene(context.Background(), db.engine, db.sparseIndex, db.encoder,
		q.Text, keywords, db.activeScenes, &db.config.Defaults, db.config.Defaults.MinSceneScore, q.DirectedL3ID)
	if err != nil {
		return nil, 0, err
	}
	topics, topicID, err := db.createTopicInScene(q, keywords, hit.SceneID)
	if err != nil {
		return nil, 0, err
	}
	return topics, topicID, nil
}

// createTopicInScene 创建话题：sceneID 为 0 时新建场景，否则写入指定场景；
// 同时写 L4 归档并更新 L4Refs；稀疏索引由 Search 主流程统一写入。
// 返回该场景 depth<=1 的话题列表与新建话题 ID。
func (db *DB) createTopicInScene(q SearchQuery, keywords []string, sceneID uint64) ([]core.TopicSlot, uint64, error) {
	if sceneID == 0 {
		sceneName := fmt.Sprintf("%d:%s", q.Timestamp, common.SafeCharSlice(q.Text, 10))
		sid, err := repo.CreateSceneL2(db.engine, sceneName)
		if err != nil {
			return nil, 0, err
		}
		sceneID = sid
	}
	topicID := core.ComputeTopicID(sceneID, q.Timestamp, 0)
	if !repo.CreateTopicL2(db.engine, common.FormatHash(sceneID), keywords, q.Timestamp) {
		return nil, 0, common.NewError(common.ErrIO, "create topic", nil)
	}
	topicIDStr := common.FormatHash(topicID)
	archiveID, err := repo.AppendArchiveL4(db.engine, topicIDStr, 0, core.ContentText, q.Text, q.Timestamp)
	if err != nil {
		return nil, 0, err
	}
	if !repo.UpdateTopicL4RefsL2(db.engine, topicIDStr, []uint64{archiveID}) {
		return nil, 0, common.NewError(common.ErrIO, "update topic l4 ref", nil)
	}
	// 按场景 ID 读回 depth<=1 话题列表（含刚写入的 L4Refs 等字段）。
	latest, err := repo.ListTopicsL2(db.engine, common.FormatHash(sceneID), 1, 2)
	if err != nil {
		return nil, 0, err
	}
	// 激活场景：新建/指定/命中三路由统一在此收口，重复激活幂等去重。
	db.activateScene(sceneID)
	return latest, topicID, nil
}

// associatedContexts 通过 L1 反查取关联度最高的场景：命中场景的 L1 节点
// 的 TopicIDs 按所属场景聚合，取话题总数最多的场景，返回该场景 depth<=1 话题列表。
func (db *DB) associatedContexts(sceneID uint64) []core.TopicSlot {
	l1Rev := db.l1Reverse.Load()
	if l1Rev == nil {
		return []core.TopicSlot{}
	}
	nodes := repo.FindAssociatedNodesL1(db.engine, l1Rev, []string{common.FormatHash(sceneID)})
	counts := make(map[uint64]int)
	for _, node := range nodes {
		for _, topicID := range node.TopicIDs {
			ts, err := repo.ListTopicsL2(db.engine, common.FormatHash(topicID), 0, 3)
			if err != nil {
				continue
			}
			counts[ts[0].SceneID]++
		}
	}
	if len(counts) == 0 {
		return []core.TopicSlot{}
	}
	bestScene, bestCount := uint64(0), 0
	for sid, n := range counts {
		if n > bestCount {
			bestScene, bestCount = sid, n
		}
	}
	topics, err := repo.ListTopicsL2(db.engine, common.FormatHash(bestScene), 1, 2)
	if err != nil {
		return []core.TopicSlot{}
	}
	return topics
}

// assembleResult 组装统一结果：Profile + Crystals + 路由产出。
func (db *DB) assembleResult(q SearchQuery, contexts, associated []core.TopicSlot, newTopicID uint64) (*SearchResult, error) {
	return &SearchResult{
		Profile:            db.readProfile(),
		Contexts:           contexts,
		AssociatedContexts: associated,
		Crystals:           db.matchCrystals(q.Text),
		NewTopicID:         newTopicID,
	}, nil
}

// readProfile 读取 L0 画像；不存在时返回空画像。
func (db *DB) readProfile() core.ProfileSlot {
	slot, err := repo.GetProfileL0(db.engine)
	if err != nil {
		return core.ProfileSlot{}
	}
	return *slot
}

// matchCrystals 按查询文本匹配 L5 动作链。
func (db *DB) matchCrystals(text string) []core.ActionChainSlot {
	chains := repo.MatchChainsL5(db.engine, text)
	if chains == nil {
		return []core.ActionChainSlot{}
	}
	return chains
}
