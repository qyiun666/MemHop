// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// RunDream runs one full dream pipeline: parallel L2 compression on the
// given scene (or all active scenes when sceneID is empty), then L1
// rebuild/decay, L0 profile/distill; rebuilt sparse and L1 reverse indexes
// are installed into db. Any stage failure returns an error.
func (db *DB) RunDream(ctx context.Context, sceneID string) (bool, error) {
	scenes := db.activeScenes
	if sceneID != "" {
		hash, err := common.ParseID(sceneID)
		if err != nil {
			return false, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
		}
		scenes = []uint64{hash}
	}

	// Stage 1: one goroutine per scene compresses L2 (writes only;
	// indexes rebuilt at the end). Scenes with at least one merged group
	// leave the in-memory active set here; Search re-activates them.
	succeeded, failures := db.compressActiveScenes(ctx, scenes)
	if len(succeeded) == 0 && failures > 0 {
		return false, fmt.Errorf("dream: LLM consolidation failed for all scenes")
	}
	if err := db.stageCancelled(ctx, "l2_compress"); err != nil {
		return false, err
	}
	// Drop compressed scenes so Dream does not spin empty goroutines.
	// Scenes skipped below the compress threshold stay active for the
	// next Dream; Search re-activates a compressed scene on its next hit.
	if len(succeeded) > 0 {
		kept := db.activeScenes[:0]
		for _, sid := range db.activeScenes {
			if _, done := succeeded[sid]; !done {
				kept = append(kept, sid)
			}
		}
		db.activeScenes = kept
	}

	// Stage 2: rebuild retrieval indexes (sparse/L2Meta) in one scan.
	newSparse, newL2Meta, err := index.RebuildSearchIndexes(db.engine, core.DefaultAgentID)
	if err != nil {
		return false, err
	}
	decayParams := repo.DecayParams{
		LambdaNode:             float64(lambdaNode),
		LambdaEdge:             float64(lambdaEdge),
		NodeRemoveThreshold:    nodeRemoveThreshold,
		NodePruneEdgeThreshold: nodePruneEdgesThreshold,
		EdgeRemoveThreshold:    edgeRemoveThreshold,
		MinEdgeNodes:           minEdgeNodes,
	}

	// Stage 2.5: L6 usage feedback — adjust L1 importance from scene-level
	// retrieval usage so the rebuild/decay below reflects actual usage.
	db.applyUsageFeedback()

	// Stage 2.25: L1 write/update from the current L2 structure (L1 is
	// written only during Dream; stale nodes are removed in Stage 3).
	if _, err := repo.SyncL1NodesFromL2(db.engine, core.DefaultAgentID); err != nil {
		return false, err
	}

	// Stage 2.3: create/refresh co-occurrence hyperedges between scenes
	// (keyword-overlap Jaccard >= L1EdgeMinSimilarity). Fresh edges are
	// decayed by Stage 4 like every other edge.
	if _, err := repo.BuildL1Hyperedges(db.engine, core.DefaultAgentID, l1EdgeMinSimilarity); err != nil {
		return false, err
	}
	if err := db.stageCancelled(ctx, "l1_hyperedges"); err != nil {
		return false, err
	}

	// Stage 3: L1 rebuild (remove stale nodes).
	if _, err := repo.RebuildL1FromL2(db.engine, core.DefaultAgentID, newL2Meta, &decayParams); err != nil {
		return false, err
	}
	if err := db.stageCancelled(ctx, "l1_rebuild"); err != nil {
		return false, err
	}

	// Stage 4: L1 time decay.
	if _, err := repo.DecayL1Network(db.engine, core.DefaultAgentID, newL2Meta, &decayParams); err != nil {
		return false, err
	}
	if err := db.stageCancelled(ctx, "l1_decay"); err != nil {
		return false, err
	}

	// Stage 5: L0 profile rebuild.
	if err := repo.GenerateProfileL0(db.engine, core.DefaultAgentID, newSparse); err != nil {
		return false, err
	}
	if err := db.stageCancelled(ctx, "l0_profile"); err != nil {
		return false, err
	}

	// Stage 6: L0 distillation (LLM emotion/MBTI, backfilled into L1).
	if err := db.distillL0Stage(ctx); err != nil {
		return false, err
	}

	// Final: install the rebuilt indexes into db.
	db.sparseIndex = newSparse
	db.l2Meta = newL2Meta
	return true, nil
}

// triggerSceneDream schedules one scene's Dream in the background so the
// caller (Search/Update) returns immediately instead of blocking on the
// LLM-heavy pipeline. The goroutine acquires the write lock itself and
// exits when RunDream returns or the DB is closed; an in-flight set
// prevents stacking multiple Dreams for the same scene. Failures are
// logged and never fail the caller. RunDream runs under db.dreamCtx, a
// detached context cancelled at Close so a pending Dream never blocks
// shutdown on LLM calls.
func (db *DB) triggerSceneDream(sceneID uint64) {
	db.dreamMu.Lock()
	if _, ok := db.dreamInFlight[sceneID]; ok {
		db.dreamMu.Unlock()
		return
	}
	db.dreamInFlight[sceneID] = struct{}{}
	db.dreamMu.Unlock()

	go func() {
		defer func() {
			db.dreamMu.Lock()
			delete(db.dreamInFlight, sceneID)
			db.dreamMu.Unlock()
		}()
		db.mu.Lock()
		defer db.mu.Unlock()
		// Re-check closed: Close may have completed while the write lock
		// was waited for; RunDream must not write to a closed engine.
		if db.closed.Load() {
			return
		}
		if _, err := db.RunDream(db.dreamCtx, common.FormatHash(sceneID)); err != nil {
			slog.Warn("dream: trigger failed", "scene", common.FormatHash(sceneID), "err", err)
		}
	}()
}

// compressActiveScenes runs one goroutine per scene: reads depth-1 topics,
// asks the LLM for merge groups and applies them; returns the set of scenes
// that had at least one group applied and the LLM failure count.
func (db *DB) compressActiveScenes(ctx context.Context, scenes []uint64) (map[uint64]struct{}, int) {
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
				MetaIdx: db.l2Meta,
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
			out, err := db.llm.Consolidate(ctx, topics)
			if err != nil {
				mu.Lock()
				failures++
				mu.Unlock()
				return
			}
			if applied := db.applyGroups(ctx, sceneID, topics, out); applied > 0 {
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
func (db *DB) applyGroups(ctx context.Context, sceneID uint64, topics []core.TopicSlot, out *ConsolidationOutput) uint32 {
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
		parentID := core.ComputeTopicID(sceneID, minTS, maxTS)
		parentIDStr := common.FormatHash(parentID)

		archiveID, err := repo.AppendArchiveL4(db.engine, core.DefaultAgentID, parentIDStr, core.RoleDream, core.ContentText, g.MergedSummary, maxTS)
		if err != nil {
			slog.Warn("dream: archive merged summary failed", "parent", parentIDStr, "err", err)
			continue
		}

		// Keywords of MergedSummary become FusedKeywords (it already merges both sides).
		keywords, err := db.llm.ExtractKeywords(ctx, g.MergedSummary)
		if err != nil || len(keywords) == 0 {
			slog.Warn("dream: extract keywords from merged summary failed, skip group", "parent", parentIDStr, "err", err)
			continue
		}

		centroidRef, err := db.writeCentroid(g.MergedSummary)
		if err != nil {
			slog.Warn("dream: encode merged summary centroid failed", "parent", parentIDStr, "err", err)
			continue
		}

		if !repo.CreateFusedTopicL2(db.engine, core.DefaultAgentID, common.FormatHash(sceneID), keywords, minTS, maxTS, g.NodeHashes, centroidRef) {
			slog.Warn("dream: create fused topic failed", "parent", parentIDStr)
			continue
		}
		// Attach the summary archive ref so retrieval can return the full text.
		if !repo.UpdateTopicL4RefsL2(db.engine, core.DefaultAgentID, parentIDStr, []uint64{archiveID}) {
			slog.Warn("dream: attach summary archive ref failed", "parent", parentIDStr)
			continue
		}
		if _, err := repo.CompressTopicsL2(db.engine, core.DefaultAgentID, g.NodeHashes, parentID); err != nil {
			slog.Warn("dream: compress child topics failed", "parent", parentIDStr, "err", err)
			continue
		}
		count++
	}
	return count
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

func (db *DB) distillL0Stage(ctx context.Context) error {
	samples, _ := repo.SampleL1ForDistill(db.engine, core.DefaultAgentID)
	if len(samples) == 0 {
		return nil
	}
	llmSamples := make([]L1Sample, len(samples))
	for i, s := range samples {
		llmSamples[i] = L1Sample{IDHash: s.IDHash, Keywords: s.Keywords, Importance: s.Importance}
	}
	out, err := db.llm.Distill(ctx, llmSamples)
	if err != nil {
		return err
	}
	if err := repo.MergeDistillIntoProfile(db.engine, core.DefaultAgentID,
		repo.DistillEmotion{Valence: out.Emotion.Valence, Arousal: out.Emotion.Arousal, Dominance: out.Emotion.Dominance},
		repo.DistillMBTI{IE: out.MBTI.IE, NS: out.MBTI.NS, TF: out.MBTI.TF, JP: out.MBTI.JP, Type: out.MBTI.Type},
	); err != nil {
		return err
	}
	perNode := make(map[uint64]repo.L1NodeEmotion, len(out.PerNode))
	for _, n := range out.PerNode {
		id, err := common.ParseID(n.IDHex)
		if err != nil {
			continue
		}
		perNode[id] = repo.L1NodeEmotion{Valence: n.Valence, Arousal: n.Arousal}
	}
	repo.BackfillL1Emotions(db.engine, core.DefaultAgentID, perNode)
	return nil
}

// applyUsageFeedback adjusts L1 node importance from scene usage stats
// (folded into the L2 scene record): scenes hit within the usage TTL get
// +0.05 (active), the rest get -0.05 (cold). Best-effort; failures only
// warn and never abort Dream.
func (db *DB) applyUsageFeedback() {
	scenes := repo.CollectAllScenesL2(db.engine, core.DefaultAgentID)
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
	for _, node := range core.CollectAllSceneNodes(db.engine, core.DefaultAgentID) {
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
		if err := core.WriteSceneNode(db.engine, core.DefaultAgentID, node.IDHash, &node); err != nil {
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
