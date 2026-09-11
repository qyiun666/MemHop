// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"context"
	"fmt"
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

// Internal tuning of the consolidation stages: the L1 decay parameters and the
// scene-similarity floor of hyperedge construction.
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
)

// StructureStages runs stages 2 through 5 of the pipeline: rebuild the L2Meta
// cache from the records and install it, run the L1 sync/edges/rebuild/decay
// stages off that copy, then L0 distillation. Callers hold ac.Mu.
func StructureStages(ctx context.Context, ac *domain.Context, agentID uint64, rep *core.DreamReport) error {
	start := time.Now()
	// Stage 2: rebuild the L2Meta cache in one scan of the agent domain.
	newL2Meta := index.BuildL2MetaFromEngine(ac.Engine, agentID)
	// Install it here rather than after the L1 stages: the rebuild reads the
	// records as they now stand and every L1 stage works from the copy passed
	// below, so nothing an L1 failure leaves behind makes this cache wrong.
	// Installing late keeps the domain serving a cache from before a compression
	// whose records are already on disk — its topic depths and child links then
	// read stale until the next successful pass, while every stage that just ran
	// used the fresh one.
	ac.L2Meta = newL2Meta
	decayParams := engram.DecayParams{
		LambdaNode:             float64(lambdaNode),
		LambdaEdge:             float64(lambdaEdge),
		NodeRemoveThreshold:    nodeRemoveThreshold,
		NodePruneEdgeThreshold: nodePruneEdgesThreshold,
		EdgeRemoveThreshold:    edgeRemoveThreshold,
		MinEdgeNodes:           minEdgeNodes,
	}
	AppendStage(rep, "index_rebuild", start, nil)

	if cerr := StageCancelled(ctx, "index_rebuild"); cerr != nil {
		// Reconcile first, then cancel. A pass that sank topics wrote new depths
		// with no incremental mirror step, so this rebuilt table is the only thing
		// that puts the read path back in step with the records. Cancelling above the
		// install would leave the domain still listing turns this pass already
		// swallowed, until the cache is dropped or the file reopened. What is
		// skipped here (L1 sync, edges, decay, distill) re-runs on the next pass.
		return cerr
	}

	if err := l1Stages(ctx, ac, agentID, newL2Meta, &decayParams, rep); err != nil {
		return err
	}

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

// l1Stages runs the L1 portion of the pipeline: scene nodes synced from the
// current L2 structure, co-occurrence hyperedges (keyword-overlap Jaccard
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
	samples := profile.Samples(ac.Engine, agentID)
	if len(samples) == 0 {
		return false, nil
	}
	out, err := llmops.Distill(ctx, ac.LLM, samples)
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
