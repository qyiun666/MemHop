// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// RunDream of the composition root: one full dream pipeline for a single
// agent domain. The stage implementations live in internal/dream.

package internal

import (
	"context"
	"log/slog"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/dream"
)

// RunDream runs one full dream pipeline for a single agent domain: parallel
// L2 compression on the given scene (or every scene of the domain when
// sceneID is empty), then L1 rebuild/decay, L0 profile/distill; the rebuilt
// L2Meta cache is installed into the agent context. Any stage failure returns
// an error together with the partially filled DreamReport, and naming a scene
// that does not exist is reported as ErrNotFound. The whole pipeline
// holds the domain lock, so same-agent operations wait while different agents
// run in parallel.
func (db *DB) RunDream(ctx context.Context, agentID uint64, sceneID uint64) (*DreamReport, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()

	rep := &DreamReport{}
	// Retention first: both prunes run on every Dream, even when there is
	// nothing to consolidate (early return below). Content and plan trees share
	// one window but not one clock — an L4 record ages on when it was said, a
	// node on when it was last committed.
	dream.PruneContentStage(ac, agentID, rep)
	dream.PrunePlanStage(ac, agentID, rep)

	scenes, err := dream.SceneSet(db.engine, agentID, sceneID)
	if err != nil {
		return rep, err
	}
	if len(scenes) == 0 {
		return rep, nil
	}

	start := time.Now()
	succeeded, unusable, failed := dream.CompressScenes(ctx, ac, scenes, rep)
	if failed != nil {
		// An engine refusal keeps the code it came with: a host told "the model
		// failed" would go check an endpoint that answered every call it was sent.
		dream.AppendStage(rep, "l2_compress", start, failed)
		return rep, common.NewError(common.CodeOf(failed), "dream: consolidation", failed)
	}
	if len(succeeded) == 0 && unusable > 0 {
		// A cancelled pass fails every scene's call at once, and a host told "the
		// model failed" would go check the model.
		err := common.NewError(common.ErrLLM, "dream: consolidation produced nothing usable for any scene")
		if cerr := ctx.Err(); cerr != nil {
			err = common.NewError(common.ErrCancelled,
				"dream: consolidation cancelled before any scene finished", cerr)
		}
		dream.AppendStage(rep, "l2_compress", start, err)
		return rep, err
	}
	rep.ConsolidatedScenes = len(succeeded)
	dream.AppendStage(rep, "l2_compress", start, dream.StageCancelled(ctx, "l2_compress"))
	// StructureStages owns the next cancellation checkpoint: it sits after the
	// rebuilt L2Meta is installed, so a cancelled pass still leaves the read path
	// serving what compression wrote rather than the tree from before it.
	if err := dream.StructureStages(ctx, ac, agentID, rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// triggerSceneDream schedules one scene's Dream in the background so the
// caller (the close-time consolidation check) returns immediately instead of
// blocking on the LLM-heavy pipeline. The goroutine acquires the domain lock
// itself and exits when RunDream returns or the DB is closed; the per-agent
// in-flight set prevents stacking multiple Dreams for the same scene. Failures
// are logged and never fail the caller. RunDream runs under the agent's
// opCtx, cancelled at Close so a pending Dream never blocks shutdown on
// LLM calls. Caller must hold ac.Mu.
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
			// Two answers mean the host stopped asking: the database is closed, or this pass was
			// cancelled by that close. Neither is a consolidation that went wrong — the memory simply
			// was not consolidated before the file was handed back, which is what closing means — and
			// a warning on every clean exit is a warning nobody reads, so those two go unreported.
			// Anything else the pipeline answered is a real failure and stays a warning.
			switch code := common.CodeOf(err); code {
			case common.ErrClosed, common.ErrCancelled:
			default:
				slog.Warn("dream: trigger failed",
					"agent", common.FormatHash(ac.ID), "scene", common.FormatHash(sceneID), "err", err)
			}
		}
	}()
}
