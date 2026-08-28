// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 trajectory record primitives: append one event, batch delete by id.
// Reads, listing, pruning and topic aggregation run through the domain's
// TrajIndex in the internal layer, which owns every trajectory write and
// delete under the same domain lock.
package repo

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// AppendTrajectory writes one trajectory event; ID = hash(sessionID:seq).
// Re-writing the same sessionID+seq points the index at the newest record
// (append-only upsert). Returns the assigned record id.
func AppendTrajectory(engine *core.StorageEngine, agentID uint64, ev core.TrajectorySlot) (uint64, error) {
	ev.IDHash = common.HashID(fmt.Sprintf("%d:%d", ev.SessionID, ev.Seq))
	if err := core.WriteTrajectorySlot(engine, agentID, ev.IDHash, &ev); err != nil {
		return 0, err
	}
	return ev.IDHash, nil
}

// DeleteTrajectoryByIDs batch-deletes trajectory events by record id and
// returns how many were removed.
func DeleteTrajectoryByIDs(engine *core.StorageEngine, agentID uint64, idHashes []uint64) (int, error) {
	if len(idHashes) == 0 {
		return 0, nil
	}
	n, err := engine.DeleteRecordBatch(agentID, idHashes)
	if err != nil {
		return 0, common.NewError(common.ErrIO, "delete trajectory", err)
	}
	return n, nil
}
