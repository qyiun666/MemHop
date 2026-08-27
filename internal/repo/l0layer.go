// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 profile record primitives.
package repo

import (
	"fmt"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 profile operations: singleton ProfileSlot at the fixed ID hash("profile")
// inside the agent domain; GetProfileL0 returns ErrNotFound when absent.
func GetProfileL0(engine *core.StorageEngine, agentID uint64) (*core.ProfileSlot, error) {
	slot, err := core.ReadProfileSlot(engine, agentID, common.HashID("profile"))
	if err != nil {
		return nil, common.NewError(common.ErrNotFound, "profile not found", err)
	}
	return slot, nil
}

func UpdateProfileL0(engine *core.StorageEngine, agentID uint64, slot *core.ProfileSlot) error {
	slot.IDHash = common.HashID("profile")
	return core.WriteProfileSlot(engine, agentID, slot.IDHash, slot)
}

func BackfillL1Emotions(engine *core.StorageEngine, agentID uint64, perNode map[uint64]core.NodeEmotion) (int, error) {
	written := 0
	for id, em := range perNode {
		node, err := core.ReadSceneNode(engine, agentID, id)
		if err != nil {
			return written, fmt.Errorf("backfill L1 emotions: node %s not found", common.FormatHash(id))
		}
		if node.Valence != 0 || node.Arousal != 0 {
			continue
		}
		node.Valence = em.Valence
		node.Arousal = em.Arousal
		node.UpdatedAt = time.Now().UnixMilli()
		if err := core.WriteSceneNode(engine, agentID, id, node); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}
