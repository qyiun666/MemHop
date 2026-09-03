// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/qyiun666/MemHop/internal/cap/engram"
	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/cap/profile"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// RunDream runs one full dream pipeline for a single agent domain: parallel
// L2 compression on the given scene (or every scene of the domain when
// sceneID is empty), then L1 rebuild/decay, L0 profile/distill; the rebuilt
// L2Meta cache is installed into the agent context. Any stage failure returns
// an error together with the partially filled DreamReport. The whole pipeline
// holds the domain lock, so same-agent operations wait while different agents
// run in parallel.
func (db *DB) RunDream(ctx context.Context, agentID uint64, sceneID uint64) (*DreamReport, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()

	rep := &DreamReport{}
	// Retention first: the L6 prune runs on every Dream, even when there is
	// nothing to consolidate (early return below).
	db.pruneTrajectoryStage(agentID, ac, rep)

	scenes := dreamSceneSet(db.engine, agentID, sceneID)
	if len(scenes) == 0 {
		return rep, nil
	}

	start := time.Now()
	succeeded, failures := db.compressScenes(ctx, ac, scenes, rep)
	if len(succeeded) == 0 && failures > 0 {
		err = errors.New("dream: LLM consolidation failed for all scenes")
		appendStage(rep, "l2_compress", start, err)
		return rep, err
	}
	rep.ConsolidatedScenes = len(succeeded)
	appendStage(rep, "l2_compress", start, db.stageCancelled(ctx, "l2_compress"))
	if cerr := ctx.Err(); cerr != nil {
		return rep, fmt.Errorf("dream: cancelled after l2_compress stage: %w", cerr)
	}

	if err := db.dreamStructureStages(ctx, agentID, ac, rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// stageStatus classifies a stage outcome into a report status string:
// ok / cancelled (context errors) / error.
func stageStatus(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return "cancelled"
	default:
		return "error"
	}
}

// appendStage records one pipeline phase's outcome and wall time in the
// report; a non-nil err is classified cancelled-vs-error via context errors.
func appendStage(rep *DreamReport, name string, start time.Time, err error) {
	rep.Stages = append(rep.Stages, DreamStage{Name: name, Status: stageStatus(err), DurationMs: time.Since(start).Milliseconds()})
}

// dreamSceneSet resolves the target scenes of one pass: a non-zero scene id,
// or every scene of the domain. A scene is a host session, so a domain-wide
// Dream sweeps them all and the compress threshold filters which ones are
// worth visiting.
func dreamSceneSet(engine *core.StorageEngine, agentID uint64, sceneID uint64) []uint64 {
	if sceneID != 0 {
		return []uint64{sceneID}
	}
	scenes := repo.CollectAllScenesL2(engine, agentID)
	out := make([]uint64, 0, len(scenes))
	for _, s := range scenes {
		out = append(out, s.SceneID)
	}
	return out
}

// pruneTrajectoryStage drops L6 trajectory events older than the retention
// window; durable products live in L4/L5, so L6 stays a bounded process
// index. Best-effort: a failure is logged and recorded in the report but
// never aborts Dream.
func (db *DB) pruneTrajectoryStage(agentID uint64, ac *domain.Context, rep *DreamReport) {
	start := time.Now()
	cutoff := time.Now().Add(-trajectoryRetention).UnixMilli()
	hashes := ac.Traj.RemoveBefore(cutoff)
	var err error
	if len(hashes) > 0 {
		if _, err = repo.DeleteTrajectoryByIDs(db.engine, agentID, hashes); err != nil {
			slog.Warn("dream: trajectory prune failed", "agent", common.FormatHash(agentID), "err", err)
		}
	}
	// Plan nodes sit outside the event TrajIndex, so sweep them by their own
	// timestamp from the engine (authoritative — Dream is a disk maintainer,
	// not a hot path). A plan is exempt only while it BOTH holds a non-Done
	// node AND saw activity inside the retention window: an in-flight task
	// must not lose its tree mid-task, but once a plan has been silent past
	// the window it is abandoned and sweeps like any other record, so L6
	// stays bounded. Expired nodes of the swept plans cascade their bound
	// events so no orphan PlanNodeRef survives. The in-memory planCache is
	// refreshed only after the disk sweep succeeds, keeping cache and engine
	// in sync. Cascade-deleted events that are still fresh may linger in the
	// TrajIndex until the periodic prune or a context rebuild; readers skip
	// missing records, so the drift is benign.
	type pruneDel struct {
		planID   uint64
		nodeDel  []uint64
		eventDel []uint64
	}
	var prunes []pruneDel
	var delIDs []uint64
	for _, agg := range repo.CollectPlanAggregates(db.engine, agentID) {
		if agg.HasNonDone && agg.LastActiveAt >= cutoff {
			continue
		}
		var nodeDel []uint64
		for _, n := range agg.Nodes {
			if n.Timestamp < cutoff {
				nodeDel = append(nodeDel, n.IDHash)
			}
		}
		if len(nodeDel) == 0 {
			continue
		}
		expired := make(map[uint64]struct{}, len(nodeDel))
		for _, id := range nodeDel {
			expired[id] = struct{}{}
		}
		var eventDel []uint64
		for _, ev := range agg.Events {
			if _, ok := expired[ev.PlanNodeRef]; ok {
				eventDel = append(eventDel, ev.IDHash)
			}
		}
		delIDs = append(delIDs, nodeDel...)
		delIDs = append(delIDs, eventDel...)
		prunes = append(prunes, pruneDel{planID: agg.PlanID, nodeDel: nodeDel, eventDel: eventDel})
	}
	if len(delIDs) > 0 {
		if _, derr := repo.DeleteTrajectoryByIDs(db.engine, agentID, delIDs); derr != nil {
			slog.Warn("dream: plan-node prune failed", "agent", common.FormatHash(agentID), "err", derr)
		} else {
			for _, p := range prunes {
				ac.Plans.RemovePlanIDs(p.planID, p.nodeDel, p.eventDel)
			}
		}
	}
	appendStage(rep, "l6_prune", start, err)
}

// dreamStructureStages runs stages 2 through 5 of the pipeline: the L2Meta
// rebuild, L1 sync/edges/rebuild/decay, L0 distillation, then installs the
// rebuilt cache into the agent context.
func (db *DB) dreamStructureStages(ctx context.Context, agentID uint64, ac *domain.Context, rep *DreamReport) error {
	start := time.Now()
	// Stage 2: rebuild the L2Meta cache in one scan of the agent domain.
	newL2Meta := index.BuildL2MetaFromEngine(db.engine, agentID)
	decayParams := engram.DecayParams{
		LambdaNode:             float64(lambdaNode),
		LambdaEdge:             float64(lambdaEdge),
		NodeRemoveThreshold:    nodeRemoveThreshold,
		NodePruneEdgeThreshold: nodePruneEdgesThreshold,
		EdgeRemoveThreshold:    edgeRemoveThreshold,
		MinEdgeNodes:           minEdgeNodes,
	}

	// Stage 2.5: usage feedback — adjust L1 importance from how recently each
	// scene was read, so the rebuild/decay below reflects actual usage.
	db.applyUsageFeedback(agentID)
	appendStage(rep, "index_rebuild", start, nil)

	if err := db.dreamL1Stages(ctx, agentID, newL2Meta, &decayParams, rep); err != nil {
		return err
	}
	// Install the rebuilt cache as soon as the stages that produced it are
	// done: L0 distillation only writes the profile and L1 emotions, so a
	// failed LLM call there must not throw the whole rebuild away.
	ac.L2Meta = newL2Meta

	// Stage 5: L0 distillation (LLM emotion/MBTI, backfilled into L1).
	start = time.Now()
	ran, dErr := db.distillL0Stage(ctx, agentID)
	status := stageStatus(dErr)
	if dErr == nil {
		if ran {
			rep.L0Updated = true
		} else {
			status = "skipped"
		}
	}
	rep.Stages = append(rep.Stages, DreamStage{Name: "l0_distill", Status: status, DurationMs: time.Since(start).Milliseconds()})
	return dErr
}

// dreamL1Stages runs the L1 portion of the pipeline: scene nodes synced
// from the current L2 structure (L1 is written only during Dream; stale
// nodes removed below), co-occurrence hyperedges (keyword-overlap Jaccard
// >= L1EdgeMinSimilarity; fresh edges decayed like every other edge),
// stale-node rebuild and finally time decay.
func (db *DB) dreamL1Stages(ctx context.Context, agentID uint64, newL2Meta *index.L2MetaIndex, decayParams *engram.DecayParams, rep *DreamReport) error {
	start := time.Now()
	synced, err := repo.SyncL1NodesFromL2(db.engine, agentID)
	if err != nil {
		appendStage(rep, "l1_nodes", start, err)
		return err
	}
	rep.L1NodesAdded += synced
	appendStage(rep, "l1_nodes", start, nil)

	start = time.Now()
	added, err := engram.BuildHyperedges(db.engine, agentID, l1EdgeMinSimilarity)
	cErr := err
	if cErr == nil {
		cErr = db.stageCancelled(ctx, "l1_hyperedges")
	}
	rep.L1EdgesAdded += added
	appendStage(rep, "l1_hyperedges", start, cErr)
	if err != nil || cErr != nil {
		if err != nil {
			return err
		}
		return cErr
	}

	start = time.Now()
	removedIDs, err := engram.RebuildFromL2(db.engine, agentID, newL2Meta, decayParams)
	cErr = err
	if cErr == nil {
		cErr = db.stageCancelled(ctx, "l1_rebuild")
	}
	rep.L1NodesRemoved += len(removedIDs)
	appendStage(rep, "l1_rebuild", start, cErr)
	if err != nil {
		return err
	}
	if cErr != nil {
		return cErr
	}

	start = time.Now()
	report, err := engram.DecayNetwork(db.engine, agentID, newL2Meta, decayParams)
	if report != nil {
		rep.L1NodesRemoved += report.RemovedNodes
		rep.L1EdgesRemoved += report.RemovedEdges
	}
	cErr = err
	if cErr == nil {
		cErr = db.stageCancelled(ctx, "l1_decay")
	}
	appendStage(rep, "l1_decay", start, cErr)
	if err != nil {
		return err
	}
	return cErr
}

// triggerSceneDream schedules one scene's Dream in the background so the
// caller (Search/Update) returns immediately instead of blocking on the
// LLM-heavy pipeline. The goroutine acquires the domain lock itself and
// exits when RunDream returns or the DB is closed; the per-agent in-flight
// set prevents stacking multiple Dreams for the same scene. Failures are
// logged and never fail the caller. RunDream runs under the agent's
// opCtx, cancelled at Close/DeleteAgent so a pending Dream never writes
// to a destroyed domain nor blocks shutdown on LLM calls. Caller must hold
// ac.Mu.
func (db *DB) triggerSceneDream(ac *domain.Context, sceneID uint64) {
	if _, ok := ac.DreamInFlight[sceneID]; ok {
		return
	}
	ac.DreamInFlight[sceneID] = struct{}{}

	go func() {
		defer func() {
			ac.Mu.Lock()
			delete(ac.DreamInFlight, sceneID)
			ac.Mu.Unlock()
		}()
		if _, err := db.RunDream(ac.OpCtx, ac.ID, sceneID); err != nil {
			slog.Warn("dream: trigger failed",
				"agent", common.FormatHash(ac.ID), "scene", common.FormatHash(sceneID), "err", err)
		}
	}()
}

// compressScenes runs one goroutine per scene: reads depth-1 topics, asks the
// LLM for merge groups and applies them; returns the set of scenes that had at
// least one group applied and the LLM failure count. Applied groups
// accumulate into rep.L2TopicsCompressed under mu. All scenes belong to ac's
// domain; cross-agent merging is structurally impossible.
func (db *DB) compressScenes(ctx context.Context, ac *domain.Context, scenes []uint64, rep *DreamReport) (map[uint64]struct{}, int) {
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
				AgentID: ac.ID,
				MetaIdx: ac.L2Meta,
				SceneID: sceneID,
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
			if applied := db.applyGroups(ctx, ac.ID, sceneID, topics, out); applied > 0 {
				mu.Lock()
				succeeded[sceneID] = struct{}{}
				rep.L2TopicsCompressed += int(applied)
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
// parent topic with the archive ref, then sinks the group nodes. A failed step
// rolls back what this group already wrote and reports false, so a group is
// either fully applied or leaves nothing behind.
func (db *DB) applyOneGroup(ctx context.Context, agentID uint64, sceneID uint64, g L2Group, minTS, maxTS int64) bool {
	parentID := core.ComputeTopicID(sceneID, minTS, maxTS)
	archiveID, err := repo.AppendArchiveL4(db.engine, agentID, parentID, core.RoleDream, core.ContentText, g.MergedSummary, maxTS)
	if err != nil {
		slog.Warn("dream: archive merged summary failed", "parent", common.FormatHash(parentID), "err", err)
		return false
	}

	// Keywords of MergedSummary become the fused topic's single track.
	keywords, err := llmops.ExtractKeywords(ctx, db.llm, g.MergedSummary)
	if err != nil || len(keywords) == 0 {
		slog.Warn("dream: extract keywords from merged summary failed, skip group", "parent", common.FormatHash(parentID), "err", err)
		db.discardFusedGroup(agentID, parentID, archiveID)
		return false
	}

	if !repo.CreateFusedTopicL2(db.engine, agentID, sceneID, keywords, minTS, maxTS, g.NodeHashes) {
		slog.Warn("dream: create fused topic failed", "parent", common.FormatHash(parentID))
		db.discardFusedGroup(agentID, parentID, archiveID)
		return false
	}
	// Attach the summary archive ref so the host can pull the fused text back.
	if !repo.UpdateTopicL4RefsL2(db.engine, agentID, parentID, []uint64{archiveID}) {
		slog.Warn("dream: attach summary archive ref failed", "parent", common.FormatHash(parentID))
		db.discardFusedGroup(agentID, parentID, archiveID)
		return false
	}
	if _, err := repo.CompressTopicsL2(db.engine, agentID, g.NodeHashes, parentID); err != nil {
		slog.Warn("dream: compress child topics failed", "parent", common.FormatHash(parentID), "err", err)
		db.discardFusedGroup(agentID, parentID, archiveID)
		return false
	}
	return true
}

// discardFusedGroup rolls back a partially applied merge group: no orphan
// summary archive and no fused parent sitting above children that were never
// sunk. Rollback failures only warn — the children stay at depth 1, so the next
// Dream re-picks the group.
func (db *DB) discardFusedGroup(agentID, parentID, archiveID uint64) {
	if !repo.DeleteL2(db.engine, agentID, []uint64{parentID}, repo.DeleteTopicsL2) {
		slog.Warn("dream: rollback fused topic failed", "parent", common.FormatHash(parentID))
	}
	if err := repo.DeleteArchivesL4(db.engine, agentID, []uint64{archiveID}); err != nil {
		slog.Warn("dream: rollback summary archive failed", "parent", common.FormatHash(parentID), "err", err)
	}
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

func (db *DB) distillL0Stage(ctx context.Context, agentID uint64) (bool, error) {
	samples, _ := profile.Samples(db.engine, agentID)
	if len(samples) == 0 {
		return false, nil
	}
	llmSamples := make([]L1Sample, len(samples))
	for i, s := range samples {
		llmSamples[i] = L1Sample{IDHash: s.IDHash, Keywords: s.Keywords, Importance: s.Importance}
	}
	out, err := llmops.Distill(ctx, db.llm, llmSamples)
	if err != nil {
		return false, err
	}
	emo := core.EmotionScore{Valence: out.Emotion.Valence, Arousal: out.Emotion.Arousal, Dominance: out.Emotion.Dominance}
	mbti := core.MBTIScore{IE: out.MBTI.IE, NS: out.MBTI.NS, TF: out.MBTI.TF, JP: out.MBTI.JP, Type: out.MBTI.Type}
	if err := profile.MergeDistill(db.engine, agentID, emo, mbti, out.Personality); err != nil {
		return false, err
	}
	perNode := make(map[uint64]core.NodeEmotion, len(out.PerNode))
	for _, n := range out.PerNode {
		id, err := common.ParseID(n.IDHex)
		if err != nil {
			continue
		}
		perNode[id] = core.NodeEmotion{Valence: n.Valence, Arousal: n.Arousal}
	}
	if _, err := repo.BackfillL1Emotions(db.engine, agentID, perNode); err != nil {
		return false, fmt.Errorf("distill l0: backfill l1 emotions: %w", err)
	}
	return true, nil
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
