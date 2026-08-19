// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 scene usage feedback operations: one upserted record per scene,
// consumed by Dream to steer L1 importance from retrieval usage.
package repo

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// UpsertSceneUsage reads-modifies-writes the per-scene usage record;
// ID = hash("usage:"+sceneID), same singleton pattern as the L0 Profile.
// Best-effort statistics: concurrent Search hits on the same scene may lose
// an increment (Dream only distinguishes HitCount == 0, so impact is nil).
func UpsertSceneUsage(engine *core.StorageEngine, sceneID uint64, ts int64) error {
	id := common.HashID(fmt.Sprintf("usage:%d", sceneID))
	slot, err := core.ReadSceneUsageSlot(engine, id)
	if err != nil {
		slot = &core.SceneUsageSlot{}
	}
	slot.IDHash = id
	slot.SceneID = sceneID
	slot.HitCount++
	slot.LastHitAt = ts
	return core.WriteSceneUsageSlot(engine, id, slot)
}

func CollectAllSceneUsages(engine *core.StorageEngine) []core.SceneUsageSlot {
	return core.CollectAllSceneUsages(engine)
}
