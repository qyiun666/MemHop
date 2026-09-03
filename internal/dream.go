// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// RunDream of the composition root: one full dream pipeline for a single
// agent domain. The stage implementations live in internal/dream.

package internal

import (
	"context"
	"errors"
	"fmt"
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
	dream.PruneTrajectoryStage(ac, agentID, rep)

	scenes := dream.SceneSet(db.engine, agentID, sceneID)
	if len(scenes) == 0 {
		return rep, nil
	}

	start := time.Now()
	succeeded, failures := dream.CompressScenes(ctx, ac, scenes, rep)
	if len(succeeded) == 0 && failures > 0 {
		err = errors.New("dream: LLM consolidation failed for all scenes")
		dream.AppendStage(rep, "l2_compress", start, err)
		return rep, err
	}
	rep.ConsolidatedScenes = len(succeeded)
	dream.AppendStage(rep, "l2_compress", start, dream.StageCancelled(ctx, "l2_compress"))
	if cerr := ctx.Err(); cerr != nil {
		return rep, fmt.Errorf("dream: cancelled after l2_compress stage: %w", cerr)
	}

	if err := dream.StructureStages(ctx, ac, agentID, rep); err != nil {
		return rep, err
	}
	return rep, nil
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
