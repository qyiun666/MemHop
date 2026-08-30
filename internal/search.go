// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search orchestration of the composition root: three-route dispatch
// (auto_create, directed, retrieval) over the agent domain state, with the
// scoring pipeline delegated to the scenefind capability package.

package internal

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/cap/knowledge"
	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/cap/profile"
	"github.com/qyiun666/MemHop/internal/cap/scenefind"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// Search runs three-route retrieval (AutoCreate, DirectedL2ID, default;
// DirectedL3ID restricts topics referencing that L3). LLM keyword
// extraction failure returns an error, never degrades. The ctx cancels LLM
// keyword extraction, encoder calls and the internally triggered Dream.
func (db *DB) Search(ctx context.Context, agentID uint64, q SearchQuery) (*SearchResult, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
	if q.Timestamp <= 0 {
		return nil, common.NewError(common.ErrInvalidQuery,
			"SearchQuery.Timestamp is required (Unix milliseconds)")
	}
	keywords, err := llmops.ExtractKeywords(ctx, db.llm, q.Text)
	if err != nil {
		return nil, err
	}
	contexts, newTopicID, err := ac.routeSearch(ctx, db, q, keywords)
	if err != nil {
		return nil, err
	}
	// Add the new topic to the sparse index (only when created this round).
	// L2Meta was already refreshed inside createTopicInScene (before the
	// depth<=1 listing); sparse comes last per storage → l2meta → sparse.
	if newTopicID != 0 {
		terms := index.Tokenize(strings.Join(keywords, " "))
		ac.sparseIndex.AddDocument(newTopicID, terms, uint32(len(terms)))
	}
	var associated []core.TopicSlot
	if len(contexts) > 0 {
		associated = ac.associatedContexts(db, contexts[0].SceneID)
		// Trigger a Dream when the scene's context grows beyond the
		// threshold so its depth-1 topics get compressed (best-effort and
		// asynchronous: Search returns immediately, the Dream runs in the
		// background under the domain lock; the scene is re-activated by the
		// next hit). Zero threshold (host-constructed literal) disables the
		// trigger.
		if t := db.config.Defaults.SearchDreamContextThreshold; t > 0 && len(contexts) > t {
			db.triggerSceneDream(ac, contexts[0].SceneID)
		}
	}
	return db.assembleResult(agentID, q, contexts, associated, newTopicID)
}

// routeSearch picks the retrieval path: direct creation, directed lookup
// into a given scene, or the normal three-channel retrieval.
func (ac *agentContext) routeSearch(ctx context.Context, db *DB, q SearchQuery, keywords []string) ([]core.TopicSlot, uint64, error) {
	switch {
	case q.AutoCreate:
		return ac.searchAutoCreate(db, q, keywords)
	case q.DirectedL2ID != nil:
		return ac.searchDirected(db, q, keywords)
	default:
		return ac.searchNormal(ctx, db, q, keywords)
	}
}

// searchAutoCreate creates a new scene and topic directly, skipping retrieval.
func (ac *agentContext) searchAutoCreate(db *DB, q SearchQuery, keywords []string) ([]core.TopicSlot, uint64, error) {
	topics, topicID, err := ac.createTopicInScene(db, q, keywords, 0)
	if err != nil {
		return nil, 0, err
	}
	return topics, topicID, nil
}

// searchDirected creates a topic and L4 archive in the given scene.
func (ac *agentContext) searchDirected(db *DB, q SearchQuery, keywords []string) ([]core.TopicSlot, uint64, error) {
	sceneID, err := common.ParseID(*q.DirectedL2ID)
	if err != nil {
		return nil, 0, err
	}
	topics, topicID, err := ac.createTopicInScene(db, q, keywords, sceneID)
	if err != nil {
		return nil, 0, err
	}
	return topics, topicID, nil
}

// searchNormal runs three-channel retrieval (DirectedL3ID restricts topics),
// creates a topic in the top scene (or a new one), and returns the scene's
// depth<=1 topics plus the new topic ID.
func (ac *agentContext) searchNormal(ctx context.Context, db *DB, q SearchQuery, keywords []string) ([]core.TopicSlot, uint64, error) {
	hit, err := scenefind.TopScene(ctx, ac.id, db.engine, ac.l2Meta, ac.sparseIndex, db.encoder,
		q.Text, keywords, ac.activeScenes, minSceneScore, q.DirectedL3ID, q.L3ID)
	if err != nil {
		return nil, 0, err
	}
	topics, topicID, err := ac.createTopicInScene(db, q, keywords, hit.SceneID)
	if err != nil {
		return nil, 0, err
	}
	return topics, topicID, nil
}

// createTopicInScene creates a topic (new scene when sceneID==0) with an
// L4 archive and L4Refs; sparse index updates happen in Search.
func (ac *agentContext) createTopicInScene(db *DB, q SearchQuery, keywords []string, sceneID uint64) ([]core.TopicSlot, uint64, error) {
	sceneID, err := ac.ensureSceneForTopic(db, q, sceneID)
	if err != nil {
		return nil, 0, err
	}
	// 回填场景的组织归属 L3 域（若传入 L3ID）；SetSceneL3ID 幂等，命中老场景
	// 无 L3ID 时同样补挂。
	if q.L3ID != nil {
		sceneHash, err := common.ParseID(*q.L3ID)
		if err != nil {
			return nil, 0, common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
		}
		if err := repo.SetSceneL3ID(db.engine, ac.id, sceneID, sceneHash); err != nil {
			return nil, 0, err
		}
	}
	topicID, err := ac.writeNewTopic(db, q, keywords, sceneID)
	if err != nil {
		return nil, 0, err
	}
	// Refresh the L2Meta entry before the listing below so the newly
	// created topic is part of the returned Contexts (all three routes flow
	// through here). Lock order: storage writes above → l2meta → sparse.
	ac.syncL2Meta(db, topicID)
	latest, err := repo.ListTopicsL2(repo.TopicListQuery{
		Engine:  db.engine,
		AgentID: ac.id,
		MetaIdx: ac.l2Meta,
		SceneID: sceneID,
		Depth:   1,
		Num:     2,
	})
	if err != nil {
		return nil, 0, err
	}
	// Activation unified here for all three routes; idempotent.
	ac.activateScene(sceneID)
	// Scene-level retrieval usage feedback, folded into the scene record
	// (best-effort, non-fatal).
	if err := repo.TouchSceneUsage(db.engine, ac.id, sceneID, time.Now().UnixMilli()); err != nil {
		slog.Warn("search: record scene usage failed", "err", err)
	}
	return latest, topicID, nil
}

// ensureSceneForTopic assigns a fresh scene (named from the query
// timestamp/text head) when the route did not pin one; returns the scene
// the topic will live in.
func (ac *agentContext) ensureSceneForTopic(db *DB, q SearchQuery, sceneID uint64) (uint64, error) {
	if sceneID != 0 {
		return sceneID, nil
	}
	sceneName := fmt.Sprintf("%d:%s", q.Timestamp, common.SafeCharSlice(q.Text, 10))
	sid, err := repo.CreateSceneL2(db.engine, ac.id, sceneName)
	if err != nil {
		return 0, err
	}
	return sid, nil
}

// writeNewTopic persists the topic record with its centroid, its first L4
// archive (linked back via L4Refs) and the matched L3 graph refs (this is
// what makes DirectedL3ID scoping work); returns the topic hash id.
func (ac *agentContext) writeNewTopic(db *DB, q SearchQuery, keywords []string, sceneID uint64) (uint64, error) {
	topicID := core.ComputeTopicID(sceneID, q.Timestamp, 0)
	centroidRef, err := db.writeCentroid(ac.id, q.Text)
	if err != nil {
		return 0, err
	}
	if !repo.CreateTopicL2(db.engine, ac.id, sceneID, keywords, q.Timestamp, centroidRef) {
		return 0, common.NewError(common.ErrIO, "create topic", nil)
	}
	archiveID, err := repo.AppendArchiveL4(db.engine, ac.id, topicID, core.RoleUser, core.ContentText, q.Text, q.Timestamp)
	if err != nil {
		return 0, err
	}
	if !repo.UpdateTopicL4RefsL2(db.engine, ac.id, topicID, []uint64{archiveID}) {
		return 0, common.NewError(common.ErrIO, "update topic l4 ref", nil)
	}
	if ids := knowledge.MatchGraphs(db.engine, ac.id, keywords, q.Text); len(ids) > 0 {
		if !repo.AppendTopicL3RefsL2(db.engine, ac.id, topicID, ids) {
			return 0, common.NewError(common.ErrIO, "link topic l3 refs", nil)
		}
	}
	return topicID, nil
}

// associatedContexts runs spreading activation over the L1 scene hypergraph
// from the hit scene and flattens the activated scenes' depth<=1 topics
// (activation order preserved). Empty when the scene has no L1 node yet.
func (ac *agentContext) associatedContexts(db *DB, sceneID uint64) []core.TopicSlot {
	hits := scenefind.SpreadingActivation(ac.id, db.engine, ac.l2Meta, sceneID)
	if len(hits) == 0 {
		return []core.TopicSlot{}
	}
	out := make([]core.TopicSlot, 0, len(hits)*2)
	for _, h := range hits {
		for _, st := range h.Topics {
			out = append(out, st.Topic)
		}
	}
	return out
}

func (db *DB) assembleResult(agentID uint64, _ SearchQuery, contexts, associated []core.TopicSlot, newTopicID uint64) (*SearchResult, error) {
	slot := db.readProfile(agentID)
	return &SearchResult{
		Profile:            slot,
		ProfileBrief:       profile.Brief(slot),
		Contexts:           contexts,
		AssociatedContexts: associated,
		NewTopicID:         newTopicID,
	}, nil
}

func (db *DB) readProfile(agentID uint64) core.ProfileSlot {
	slot, err := repo.GetProfileL0(db.engine, agentID)
	if err != nil {
		return core.ProfileSlot{}
	}
	return *slot
}

// writeCentroid encodes text as a centroid vector record; encoder failure
// returns an error (no silent skip).
func (db *DB) writeCentroid(agentID uint64, text string) (uint64, error) {
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
	return repo.WriteVecCentroid(db.engine, agentID, vec)
}
