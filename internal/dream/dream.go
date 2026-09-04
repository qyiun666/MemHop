// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package dream holds the consolidation pipeline's stage small methods:
// scene-set resolution, the L6 retention prune, the per-scene LLM
// compression with group apply/rollback, the L2Meta rebuild and L1
// sync/edges/rebuild/decay stages, the L0 distillation and the usage
// feedback. The RunDream big method keeps the domain lock and composes the
// stages; the background trigger stays in the composition root.

package dream

import (
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// SceneSet resolves the target scenes of one pass: a non-zero scene id,
// or every scene of the domain. A scene is a host session, so a domain-wide
// Dream sweeps them all and the compress threshold filters which ones are
// worth visiting.
func SceneSet(engine *core.StorageEngine, agentID uint64, sceneID uint64) ([]uint64, error) {
	if sceneID != 0 {
		// Existence is part of the answer: a scene the host names but that is
		// gone (or never was) must fail the pass, not report a clean no-op.
		if _, err := core.ReadSceneSlot(engine, agentID, sceneID); err != nil {
			return nil, err
		}
		return []uint64{sceneID}, nil
	}
	scenes, err := repo.CollectAllScenesL2(engine, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]uint64, 0, len(scenes))
	for _, s := range scenes {
		out = append(out, s.SceneID)
	}
	return out, nil
}
