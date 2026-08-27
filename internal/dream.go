// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/qyiun666/MemHop/internal/cap/engram"
	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/cap/profile"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// RunDream runs one full dream pipeline for a single agent domain: parallel
// L2 compression on the given scene (or all active scenes when sceneID is
// empty), then L1 rebuild/decay, L0 profile/distill; rebuilt sparse and
// L2Meta indexes are installed into the agent context. Any stage failure
// returns an error. The whole pipeline holds the domain lock, so same-agent
// operations wait while different agents run in parallel.
func (db *DB) RunDream(ctx context.Context, agentID uint64, sceneID string) (bool, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return false, err
	}
	defer ac.mu.Unlock()
	// Re-check closed: Close may have completed while the lock was waited
	// for; RunDream must not write to a closed engine.
	if db.closed.Load() {
		return false, common.NewError(common.ErrClosed, "database is closed")
	}

	scenes, err := dreamSceneSet(ac, sceneID)
	if err != nil {
		return false, err
	}
	if sceneID == "" && len(scenes) == 0 {
		// Nothing to consolidate: decide under the domain lock so a scene
		// registered by a racing Update is never missed (check-then-act).
		return true, nil
	}

	// Stage 1: one goroutine per scene compresses L2 (writes only;
	// indexes rebuilt at the end). Scenes with at least one merged group
	// leave the in-memory active set here; Search re-activates them.
	succeeded, failures := db.compressActiveScenes(ctx, ac, scenes)
	if len(succeeded) == 0 && failures > 0 {
		return false, fmt.Errorf("dream: LLM consolidation failed for all scenes")
	}
	if err := db.stageCancelled(ctx, "l2_compress"); err != nil {
		return false, err
	}
	// Drop compressed scenes so Dream does not spin empty goroutines.
	// Scenes skipped below the compress threshold stay active for the
	// next Dream; Search re-activates a compressed scene on its next hit.
	ac.dropActiveScenes(succeeded)

	if err := db.dreamStructureStages(ctx, agentID, ac); err != nil {
		return false, err
	}
	ac.lastDreamAt.Store(time.Now().UnixMilli()) // direct store: peekContext would take agentsMu under ac.mu (lock-order cycle)
	return true, nil
}

// dreamSceneSet resolves the target scenes of one pass: an explicit hex
// scene id, or the domain's active scene set.
func dreamSceneSet(ac *agentContext, sceneID string) ([]uint64, error) {
	if sceneID == "" {
		return ac.activeScenes, nil
	}
	hash, err := common.ParseID(sceneID)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	return []uint64{hash}, nil
}

// dreamStructureStages runs stages 2 through 6 of the pipeline: index
// rebuild, L1 sync/edges/rebuild/decay, L0 profile/distill, then installs
// the rebuilt indexes into the agent context.
func (db *DB) dreamStructureStages(ctx context.Context, agentID uint64, ac *agentContext) error {
	// Stage 2: rebuild retrieval indexes (sparse/L2Meta) in one scan of the
	// agent domain.
	newSparse, newL2Meta := index.RebuildSearchIndexes(db.engine, agentID)
	decayParams := engram.DecayParams{
		LambdaNode:             float64(lambdaNode),
		LambdaEdge:             float64(lambdaEdge),
		NodeRemoveThreshold:    nodeRemoveThreshold,
		NodePruneEdgeThreshold: nodePruneEdgesThreshold,
		EdgeRemoveThreshold:    edgeRemoveThreshold,
		MinEdgeNodes:           minEdgeNodes,
	}

	// Stage 2.5: L6 usage feedback — adjust L1 importance from scene-level
	// retrieval usage so the rebuild/decay below reflects actual usage.
	db.applyUsageFeedback(agentID)

	// Stages 2.25 to 4: L1 write/update/rebuild/decay.
	if err := db.dreamL1Stages(ctx, agentID, newL2Meta, &decayParams); err != nil {
		return err
	}

	// Stage 5: L0 profile rebuild.
	if err := profile.Generate(db.engine, agentID, newSparse); err != nil {
		return err
	}
	if err := db.stageCancelled(ctx, "l0_profile"); err != nil {
		return err
	}

	// Stage 6: L0 distillation (LLM emotion/MBTI, backfilled into L1).
	if err := db.distillL0Stage(ctx, agentID); err != nil {
		return err
	}

	// Final: install the rebuilt indexes into the agent context.
	ac.sparseIndex = newSparse
	ac.l2Meta = newL2Meta
	return nil
}

// dreamL1Stages runs the L1 portion of the pipeline: scene nodes synced
// from the current L2 structure (L1 is written only during Dream; stale
// nodes removed below), co-occurrence hyperedges (keyword-overlap Jaccard
// >= L1EdgeMinSimilarity; fresh edges decayed like every other edge),
// stale-node rebuild and finally time decay.
func (db *DB) dreamL1Stages(ctx context.Context, agentID uint64, newL2Meta *index.L2MetaIndex, decayParams *engram.DecayParams) error {
	if _, err := repo.SyncL1NodesFromL2(db.engine, agentID); err != nil {
		return err
	}
	if _, err := engram.BuildHyperedges(db.engine, agentID, l1EdgeMinSimilarity); err != nil {
		return err
	}
	if err := db.stageCancelled(ctx, "l1_hyperedges"); err != nil {
		return err
	}
	if _, err := engram.RebuildFromL2(db.engine, agentID, newL2Meta, decayParams); err != nil {
		return err
	}
	if err := db.stageCancelled(ctx, "l1_rebuild"); err != nil {
		return err
	}
	if _, err := engram.DecayNetwork(db.engine, agentID, newL2Meta, decayParams); err != nil {
		return err
	}
	return db.stageCancelled(ctx, "l1_decay")
}

// triggerSceneDream schedules one scene's Dream in the background so the
// caller (Search/Update) returns immediately instead of blocking on the
// LLM-heavy pipeline. The goroutine acquires the domain lock itself and
// exits when RunDream returns or the DB is closed; the per-agent in-flight
// set prevents stacking multiple Dreams for the same scene. Failures are
// logged and never fail the caller. RunDream runs under the agent's
// opCtx, cancelled at Close/DeleteAgent so a pending Dream never writes
// to a destroyed domain nor blocks shutdown on LLM calls. Caller must hold
// ac.mu.
func (db *DB) triggerSceneDream(ac *agentContext, sceneID uint64) {
	if _, ok := ac.dreamInFlight[sceneID]; ok {
		return
	}
	ac.dreamInFlight[sceneID] = struct{}{}

	go func() {
		defer func() {
			ac.mu.Lock()
			delete(ac.dreamInFlight, sceneID)
			ac.mu.Unlock()
		}()
		if _, err := db.RunDream(ac.opCtx, ac.id, common.FormatHash(sceneID)); err != nil {
			slog.Warn("dream: trigger failed",
				"agent", common.FormatHash(ac.id), "scene", common.FormatHash(sceneID), "err", err)
		}
	}()
}

// compressActiveScenes runs one goroutine per scene: reads depth-1 topics,
// asks the LLM for merge groups and applies them; returns the set of scenes
// that had at least one group applied and the LLM failure count. All scenes
// belong to ac's domain; cross-agent merging is structurally impossible.
func (db *DB) compressActiveScenes(ctx context.Context, ac *agentContext, scenes []uint64) (map[uint64]struct{}, int) {
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded = make(map[uint64]struct{})
		failures  int
	)
	for _, sid := range scenes {
		wg.Add(1)
		go func(sceneID uint64) {
			defer wg.Done()
			topics, err := repo.ListTopicsL2(repo.TopicListQuery{
				Engine:  db.engine,
				AgentID: ac.id,
				MetaIdx: ac.l2Meta,
				SceneID: common.FormatHash(sceneID),
				Depth:   1,
				Num:     2,
			})
			if err != nil {
				return
			}
			// Skip below the compress threshold: few topics keep raw detail.
			if len(topics) < db.config.Defaults.DreamCompressMinTopics {
				return
			}
			out, err := llmops.Consolidate(ctx, db.llm, topics)
			if err != nil {
				mu.Lock()
				failures++
				mu.Unlock()
				return
			}
			if applied := db.applyGroups(ctx, ac.id, sceneID, topics, out); applied > 0 {
				mu.Lock()
				succeeded[sceneID] = struct{}{}
				mu.Unlock()
			}
		}(sid)
	}
	wg.Wait()
	return succeeded, failures
}

// applyGroups applies one scene's groups: store MergedSummary as an L4
// dream archive, extract keywords for the fused topic, create the parent
// topic with the archive ref, then sink the group nodes.
func (db *DB) applyGroups(ctx context.Context, agentID uint64, sceneID uint64, topics []core.TopicSlot, out *ConsolidationOutput) uint32 {
	byID := make(map[uint64]core.TopicSlot, len(topics))
	for _, t := range topics {
		byID[t.ID] = t
	}
	var count uint32
	for _, g := range out.L2Groups {
		if len(g.NodeHashes) < 2 {
			continue
		}
		minTS, maxTS, ok := db.groupTimestamps(g.NodeHashes, byID)
		if !ok {
			continue
		}
		if db.applyOneGroup(ctx, agentID, sceneID, g, minTS, maxTS) {
			count++
		}
	}
	return count
}

// applyOneGroup consolidates a single merge group: stores MergedSummary as
// an L4 dream archive, extracts keywords for the fused topic, creates the
// parent topic with the archive ref, then sinks the group nodes. Any failed
// step skips the group (logged) and reports false.
func (db *DB) applyOneGroup(ctx context.Context, agentID uint64, sceneID uint64, g L2Group, minTS, maxTS int64) bool {
	parentID := core.ComputeTopicID(sceneID, minTS, maxTS)
	parentIDStr := common.FormatHash(parentID)

	archiveID, err := repo.AppendArchiveL4(db.engine, agentID, parentIDStr, core.RoleDream, core.ContentText, g.MergedSummary, maxTS)
	if err != nil {
		slog.Warn("dream: archive merged summary failed", "parent", parentIDStr, "err", err)
		return false
	}

	// Keywords of MergedSummary become FusedKeywords (it already merges both sides).
	keywords, err := llmops.ExtractKeywords(ctx, db.llm, g.MergedSummary)
	if err != nil || len(keywords) == 0 {
		slog.Warn("dream: extract keywords from merged summary failed, skip group", "parent", parentIDStr, "err", err)
		return false
	}

	centroidRef, err := db.writeCentroid(agentID, g.MergedSummary)
	if err != nil {
		slog.Warn("dream: encode merged summary centroid failed", "parent", parentIDStr, "err", err)
		return false
	}

	if !repo.CreateFusedTopicL2(db.engine, agentID, common.FormatHash(sceneID), keywords, minTS, maxTS, g.NodeHashes, centroidRef) {
		slog.Warn("dream: create fused topic failed", "parent", parentIDStr)
		return false
	}
	// Attach the summary archive ref so retrieval can return the full text.
	if !repo.UpdateTopicL4RefsL2(db.engine, agentID, parentIDStr, []uint64{archiveID}) {
		slog.Warn("dream: attach summary archive ref failed", "parent", parentIDStr)
		return false
	}
	if _, err := repo.CompressTopicsL2(db.engine, agentID, g.NodeHashes, parentID); err != nil {
		slog.Warn("dream: compress child topics failed", "parent", parentIDStr, "err", err)
		return false
	}
	return true
}

func (db *DB) groupTimestamps(nodeHashes []uint64, byID map[uint64]core.TopicSlot) (minTS, maxTS int64, ok bool) {
	for _, id := range nodeHashes {
		t, found := byID[id]
		if !found {
			continue
		}
		if !ok || t.UserTimestamp < minTS {
			minTS = t.UserTimestamp
		}
		if !ok || t.AgentTimestamp > maxTS {
			maxTS = t.AgentTimestamp
		}
		ok = true
	}
	return minTS, maxTS, ok
}

func (db *DB) distillL0Stage(ctx context.Context, agentID uint64) error {
	samples, _ := profile.Samples(db.engine, agentID)
	if len(samples) == 0 {
		return nil
	}
	llmSamples := make([]L1Sample, len(samples))
	for i, s := range samples {
		llmSamples[i] = L1Sample{IDHash: s.IDHash, Keywords: s.Keywords, Importance: s.Importance}
	}
	out, err := llmops.Distill(ctx, db.llm, llmSamples)
	if err != nil {
		return err
	}
	emo := core.EmotionScore{Valence: out.Emotion.Valence, Arousal: out.Emotion.Arousal, Dominance: out.Emotion.Dominance}
	mbti := core.MBTIScore{IE: out.MBTI.IE, NS: out.MBTI.NS, TF: out.MBTI.TF, JP: out.MBTI.JP, Type: out.MBTI.Type}
	if err := profile.MergeDistill(db.engine, agentID, emo, mbti); err != nil {
		return err
	}
	perNode := make(map[uint64]core.NodeEmotion, len(out.PerNode))
	for _, n := range out.PerNode {
		id, err := common.ParseID(n.IDHex)
		if err != nil {
			continue
		}
		perNode[id] = core.NodeEmotion{Valence: n.Valence, Arousal: n.Arousal}
	}
	repo.BackfillL1Emotions(db.engine, agentID, perNode)
	return nil
}

// applyUsageFeedback adjusts L1 node importance from scene usage stats
// (folded into the L2 scene record): scenes hit within the usage TTL get
// +0.05 (active), the rest get -0.05 (cold). Best-effort; failures only
// warn and never abort Dream.
func (db *DB) applyUsageFeedback(agentID uint64) {
	scenes := repo.CollectAllScenesL2(db.engine, agentID)
	if len(scenes) == 0 {
		return
	}
	now := time.Now().UnixMilli()
	ttl := defaultTTLMs
	byScene := make(map[uint64]core.SceneSlot, len(scenes))
	for _, s := range scenes {
		byScene[s.SceneID] = s
	}
	const step = 0.05
	for _, node := range core.CollectAllSceneNodes(db.engine, agentID) {
		u, ok := byScene[node.SceneID]
		imp := node.Importance
		switch {
		case !ok || u.HitCount == 0 || now-u.LastHitAt >= ttl:
			if imp -= step; imp < 0 {
				imp = 0
			}
		default:
			if imp += step; imp > 1 {
				imp = 1
			}
		}
		if imp == node.Importance {
			continue
		}
		node.Importance = imp
		if err := core.WriteSceneNode(db.engine, agentID, node.IDHash, &node); err != nil {
			slog.Warn("dream: apply usage feedback failed", "node", node.IDHash, "err", err)
		}
	}
}

func (db *DB) stageCancelled(ctx context.Context, stage string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("dream: cancelled after %s stage: %w", stage, err)
	}
	return nil
}
