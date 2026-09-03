// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"context"
	"fmt"
	"log/slog"
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

// Internal tuning of the consolidation stages: the L1 decay parameters, the
// scene-similarity floor of hyperedge construction and the usage-feedback
// window. Hosts configure only the business knobs in config.MemHopDefaults.
const (
	// L1 decay.
	lambdaNode              float32 = 0.01
	lambdaEdge              float32 = 0.02
	nodeRemoveThreshold     float32 = 0.05
	nodePruneEdgesThreshold float32 = 0.15
	edgeRemoveThreshold     float32 = 0.05
	minEdgeNodes            int     = 2
	// L1 scene hypergraph construction.
	l1EdgeMinSimilarity float32 = 0.15
	// Infrastructure.
	defaultTTLMs int64 = 3600000 // 1 hour: scene-usage feedback window
)

// StructureStages runs stages 2 through 5 of the pipeline: the L2Meta
// rebuild, L1 sync/edges/rebuild/decay, L0 distillation, then installs the
// rebuilt cache into the agent context. Callers hold ac.Mu.
func StructureStages(ctx context.Context, ac *domain.Context, agentID uint64, rep *core.DreamReport) error {
	start := time.Now()
	// Stage 2: rebuild the L2Meta cache in one scan of the agent domain.
	newL2Meta := index.BuildL2MetaFromEngine(ac.Engine, agentID)
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
	applyUsageFeedback(ac, agentID)
	AppendStage(rep, "index_rebuild", start, nil)

	if err := l1Stages(ctx, ac, agentID, newL2Meta, &decayParams, rep); err != nil {
		return err
	}
	// Install the rebuilt cache as soon as the stages that produced it are
	// done: L0 distillation only writes the profile and L1 emotions, so a
	// failed LLM call there must not throw the whole rebuild away.
	ac.L2Meta = newL2Meta

	// Stage 5: L0 distillation (LLM emotion/MBTI, backfilled into L1).
	start = time.Now()
	ran, dErr := DistillL0Stage(ctx, ac, agentID)
	status := stageStatus(dErr)
	if dErr == nil {
		if ran {
			rep.L0Updated = true
		} else {
			status = "skipped"
		}
	}
	rep.Stages = append(rep.Stages, core.DreamStage{Name: "l0_distill", Status: status, DurationMs: time.Since(start).Milliseconds()})
	return dErr
}

// l1Stages runs the L1 portion of the pipeline: scene nodes synced
// from the current L2 structure (L1 is written only during Dream; stale
// nodes removed below), co-occurrence hyperedges (keyword-overlap Jaccard
// >= l1EdgeMinSimilarity; fresh edges decayed like every other edge),
// stale-node rebuild and finally time decay.
func l1Stages(ctx context.Context, ac *domain.Context, agentID uint64, newL2Meta *index.L2MetaIndex, decayParams *engram.DecayParams, rep *core.DreamReport) error {
	start := time.Now()
	synced, err := repo.SyncL1NodesFromL2(ac.Engine, agentID)
	if err != nil {
		AppendStage(rep, "l1_nodes", start, err)
		return err
	}
	rep.L1NodesAdded += synced
	AppendStage(rep, "l1_nodes", start, nil)

	start = time.Now()
	added, err := engram.BuildHyperedges(ac.Engine, agentID, l1EdgeMinSimilarity)
	cErr := err
	if cErr == nil {
		cErr = StageCancelled(ctx, "l1_hyperedges")
	}
	rep.L1EdgesAdded += added
	AppendStage(rep, "l1_hyperedges", start, cErr)
	if err != nil || cErr != nil {
		if err != nil {
			return err
		}
		return cErr
	}

	start = time.Now()
	removedIDs, err := engram.RebuildFromL2(ac.Engine, agentID, newL2Meta, decayParams)
	cErr = err
	if cErr == nil {
		cErr = StageCancelled(ctx, "l1_rebuild")
	}
	rep.L1NodesRemoved += len(removedIDs)
	AppendStage(rep, "l1_rebuild", start, cErr)
	if err != nil {
		return err
	}
	if cErr != nil {
		return cErr
	}

	start = time.Now()
	report, err := engram.DecayNetwork(ac.Engine, agentID, newL2Meta, decayParams)
	if report != nil {
		rep.L1NodesRemoved += report.RemovedNodes
		rep.L1EdgesRemoved += report.RemovedEdges
	}
	cErr = err
	if cErr == nil {
		cErr = StageCancelled(ctx, "l1_decay")
	}
	AppendStage(rep, "l1_decay", start, cErr)
	if err != nil {
		return err
	}
	return cErr
}

// DistillL0Stage runs Dream's L0 distillation (LLM emotion/MBTI, backfilled
// into L1) and reports whether it ran. Callers hold ac.Mu.
func DistillL0Stage(ctx context.Context, ac *domain.Context, agentID uint64) (bool, error) {
	samples, _ := profile.Samples(ac.Engine, agentID)
	if len(samples) == 0 {
		return false, nil
	}
	llmSamples := make([]llmops.L1Sample, len(samples))
	for i, s := range samples {
		llmSamples[i] = llmops.L1Sample{IDHash: s.IDHash, Keywords: s.Keywords, Importance: s.Importance}
	}
	out, err := llmops.Distill(ctx, ac.LLM, llmSamples)
	if err != nil {
		return false, err
	}
	emo := core.EmotionScore{Valence: out.Emotion.Valence, Arousal: out.Emotion.Arousal, Dominance: out.Emotion.Dominance}
	mbti := core.MBTIScore{IE: out.MBTI.IE, NS: out.MBTI.NS, TF: out.MBTI.TF, JP: out.MBTI.JP, Type: out.MBTI.Type}
	if err := profile.MergeDistill(ac.Engine, agentID, emo, mbti, out.Personality); err != nil {
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
	if _, err := repo.BackfillL1Emotions(ac.Engine, agentID, perNode); err != nil {
		return false, fmt.Errorf("distill l0: backfill l1 emotions: %w", err)
	}
	return true, nil
}

// applyUsageFeedback adjusts L1 node importance from scene usage stats
// (folded into the L2 scene record): scenes hit within the usage TTL get
// +0.05 (active), the rest get -0.05 (cold). Best-effort; failures only
// warn and never abort Dream.
func applyUsageFeedback(ac *domain.Context, agentID uint64) {
	scenes := repo.CollectAllScenesL2(ac.Engine, agentID)
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
	for _, node := range core.CollectAllSceneNodes(ac.Engine, agentID) {
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
		if err := core.WriteSceneNode(ac.Engine, agentID, node.IDHash, &node); err != nil {
			slog.Warn("dream: apply usage feedback failed", "node", node.IDHash, "err", err)
		}
	}
}
