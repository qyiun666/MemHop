// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search implementation of the internal layer: three-route dispatch
// (auto_create, directed, retrieval) as methods on DB.

package internal

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

type SearchQuery struct {
	Text         string  `json:"text"`
	DirectedL2ID *string `json:"directed_l2_id,omitempty"`
	DirectedL3ID *string `json:"directed_l3_id,omitempty"`
	AutoCreate   bool    `json:"auto_create,omitempty"`
	Timestamp    int64   `json:"timestamp"`
}

type SearchResult struct {
	Profile            core.ProfileSlot `json:"profile"`
	Contexts           []core.TopicSlot `json:"contexts"`
	AssociatedContexts []core.TopicSlot `json:"associated_contexts"`
	NewTopicID         uint64           `json:"new_topic_id,omitempty"`
}

// Search runs three-route retrieval (AutoCreate, DirectedL2ID, default;
// DirectedL3ID restricts topics referencing that L3). LLM keyword
// extraction failure returns an error, never degrades. The ctx cancels LLM
// keyword extraction, encoder calls and the internally triggered Dream.
func (db *DB) Search(ctx context.Context, q SearchQuery) (*SearchResult, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	if q.Timestamp <= 0 {
		return nil, common.NewError(common.ErrInvalidQuery,
			"SearchQuery.Timestamp is required (Unix milliseconds)")
	}
	keywords, err := db.llm.ExtractKeywords(ctx, q.Text)
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
		contexts, newTopicID, err = db.searchNormal(ctx, q, keywords)
	}
	if err != nil {
		return nil, err
	}
	// Add the new topic to the sparse index (only when created this round).
	// L2Meta was already refreshed inside createTopicInScene (before the
	// depth<=1 listing); sparse comes last per storage → l2meta → sparse.
	if newTopicID != 0 {
		terms := index.Tokenize(strings.Join(keywords, " "))
		db.sparseIndex.AddDocument(newTopicID, terms, uint32(len(terms)))
	}
	var associated []core.TopicSlot
	if len(contexts) > 0 {
		associated = db.associatedContexts(contexts[0].SceneID)
		// Trigger a Dream when the scene's context grows beyond the
		// threshold so its depth-1 topics get compressed (best-effort;
		// the scene is re-activated by the next hit). Zero threshold
		// (host-constructed literal) disables the trigger.
		if t := db.config.Defaults.SearchDreamContextThreshold; t > 0 && len(contexts) > t {
			db.triggerSceneDream(ctx, contexts[0].SceneID)
		}
	}
	return db.assembleResult(q, contexts, associated, newTopicID)
}

// searchAutoCreate creates a new scene and topic directly, skipping retrieval.
func (db *DB) searchAutoCreate(q SearchQuery, keywords []string) ([]core.TopicSlot, uint64, error) {
	topics, topicID, err := db.createTopicInScene(q, keywords, 0)
	if err != nil {
		return nil, 0, err
	}
	return topics, topicID, nil
}

// searchDirected creates a topic and L4 archive in the given scene.
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

// searchNormal runs three-channel retrieval (DirectedL3ID restricts topics),
// creates a topic in the top scene (or a new one), and returns the scene's
// depth<=1 topics plus the new topic ID.
func (db *DB) searchNormal(ctx context.Context, q SearchQuery, keywords []string) ([]core.TopicSlot, uint64, error) {
	hit, err := TopScene(ctx, db.engine, db.l2Meta, db.sparseIndex, db.encoder,
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

// createTopicInScene creates a topic (new scene when sceneID==0) with an
// L4 archive and L4Refs; sparse index updates happen in Search.
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
	centroidRef, err := db.writeCentroid(q.Text)
	if err != nil {
		return nil, 0, err
	}
	if !repo.CreateTopicL2(db.engine, common.FormatHash(sceneID), keywords, q.Timestamp, centroidRef) {
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
	// Link matching L3 graphs onto the new topic: this is what makes
	// DirectedL3ID scoping work.
	if ids := repo.MatchL3Graphs(db.engine, keywords, q.Text); len(ids) > 0 {
		if !repo.AppendTopicL3RefsL2(db.engine, topicIDStr, ids) {
			return nil, 0, common.NewError(common.ErrIO, "link topic l3 refs", nil)
		}
	}
	// Refresh the L2Meta entry before the listing below so the newly
	// created topic is part of the returned Contexts (all three routes flow
	// through here). Lock order: storage writes above → l2meta → sparse.
	db.syncL2Meta(topicID)
	latest, err := repo.ListTopicsL2(repo.TopicListQuery{
		Engine:  db.engine,
		MetaIdx: db.l2Meta,
		SceneID: common.FormatHash(sceneID),
		Depth:   1,
		Num:     2,
	})
	if err != nil {
		return nil, 0, err
	}
	// Activation unified here for all three routes; idempotent.
	db.activateScene(sceneID)
	// L6: record scene-level retrieval usage feedback (best-effort, non-fatal).
	if err := repo.UpsertSceneUsage(db.engine, sceneID, time.Now().UnixMilli()); err != nil {
		slog.Warn("search: record scene usage failed", "err", err)
	}
	return latest, topicID, nil
}

// associatedContexts finds the scene with the most linked topics via L1
// reverse lookup and returns its depth<=1 topics.
func (db *DB) associatedContexts(sceneID uint64) []core.TopicSlot {
	l1Rev := db.l1Reverse.Load()
	if l1Rev == nil {
		return []core.TopicSlot{}
	}
	nodes := repo.FindAssociatedNodesL1(db.engine, l1Rev, []string{common.FormatHash(sceneID)})
	counts := make(map[uint64]int)
	for _, node := range nodes {
		for _, topicID := range node.TopicIDs {
			ts, err := repo.ListTopicsL2(repo.TopicListQuery{
				Engine:  db.engine,
				MetaIdx: db.l2Meta,
				SceneID: common.FormatHash(topicID),
				Depth:   0,
				Num:     3,
			})
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
	topics, err := repo.ListTopicsL2(repo.TopicListQuery{
		Engine:  db.engine,
		MetaIdx: db.l2Meta,
		SceneID: common.FormatHash(bestScene),
		Depth:   1,
		Num:     2,
	})
	if err != nil {
		return []core.TopicSlot{}
	}
	return topics
}

func (db *DB) assembleResult(_ SearchQuery, contexts, associated []core.TopicSlot, newTopicID uint64) (*SearchResult, error) {
	return &SearchResult{
		Profile:            db.readProfile(),
		Contexts:           contexts,
		AssociatedContexts: associated,
		NewTopicID:         newTopicID,
	}, nil
}

func (db *DB) readProfile() core.ProfileSlot {
	slot, err := repo.GetProfileL0(db.engine)
	if err != nil {
		return core.ProfileSlot{}
	}
	return *slot
}

// writeCentroid encodes text as a centroid vector record; encoder failure
// returns an error (no silent skip).
func (db *DB) writeCentroid(text string) (uint64, error) {
	if db.encoder == nil || !db.encoder.IsAvailable() {
		return 0, common.NewError(common.ErrEncoder, "encoder unavailable for centroid", nil)
	}
	vec, err := db.encoder.Encode(text)
	if err != nil {
		return 0, common.NewError(common.ErrEncoder, "encode centroid", err)
	}
	if len(vec) == 0 {
		return 0, common.NewError(common.ErrEncoder, "encode centroid: empty vector", nil)
	}
	return repo.WriteVecCentroid(db.engine, vec)
}
