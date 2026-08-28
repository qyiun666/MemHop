// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 capability operations: import/upsert, query, delete, lifecycle and
// usage feedback. Capabilities are stored, never executed by MemHop.

package repo

import (
	"slices"
	"time"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

// UpsertCapabilityL5 writes a capability by stable name ID. Existing runtime
// fields (SuccessRate/TriggerCount/LastTriggered/Status when the capability
// is active or deprecated) are preserved.
func UpsertCapabilityL5(engine *core.StorageEngine, agentID uint64, cap *core.Capability) (bool, error) {
	cap.IDHash = core.CapabilityID(cap.Name)
	now := time.Now().UnixMilli()
	if existing, err := core.ReadCapability(engine, agentID, cap.IDHash); err == nil {
		cap.SuccessRate = existing.SuccessRate
		cap.TriggerCount = existing.TriggerCount
		cap.LastTriggered = existing.LastTriggered
		cap.CreatedAt = existing.CreatedAt
		if cap.Status == core.CapabilityDraft && existing.Status != core.CapabilityDraft {
			cap.Status = existing.Status
		}
		cap.UpdatedAt = now
		if err := core.WriteCapability(engine, agentID, cap.IDHash, cap); err != nil {
			return true, err
		}
		return true, nil
	}
	cap.CreatedAt = now
	cap.UpdatedAt = now
	// Status zero value is CapabilityDraft; keep it as-is.
	if cap.Origin == "" {
		cap.Origin = core.CapabilityOriginHost
	}
	if err := core.WriteCapability(engine, agentID, cap.IDHash, cap); err != nil {
		return false, err
	}
	return false, nil
}

func GetCapabilityL5(engine *core.StorageEngine, agentID uint64, id uint64) (*core.Capability, error) {
	return core.ReadCapability(engine, agentID, id)
}

func DeleteCapabilityL5(engine *core.StorageEngine, agentID uint64, id uint64) bool {
	_, err := engine.DeleteRecordBatch(agentID, []uint64{id})
	return err == nil
}

// ActivateCapabilityL5 promotes a draft to active.
func ActivateCapabilityL5(engine *core.StorageEngine, agentID uint64, id uint64) (*core.Capability, error) {
	cap, err := GetCapabilityL5(engine, agentID, id)
	if err != nil {
		return nil, err
	}
	cap.Status = core.CapabilityActive
	cap.UpdatedAt = time.Now().UnixMilli()
	if err := core.WriteCapability(engine, agentID, cap.IDHash, cap); err != nil {
		return nil, err
	}
	return cap, nil
}

// RecordCapabilityUsageL5 updates runtime feedback after a host uses a
// capability.
func RecordCapabilityUsageL5(engine *core.StorageEngine, agentID uint64, id uint64, success bool) (*core.Capability, error) {
	cap, err := GetCapabilityL5(engine, agentID, id)
	if err != nil {
		return nil, err
	}
	cap.TriggerCount++
	oldSuccess := float64(cap.SuccessRate) * float64(cap.TriggerCount-1)
	if success {
		oldSuccess++
	}
	cap.SuccessRate = float32(oldSuccess / float64(cap.TriggerCount))
	cap.LastTriggered = time.Now().UnixMilli()
	cap.UpdatedAt = cap.LastTriggered
	if err := core.WriteCapability(engine, agentID, cap.IDHash, cap); err != nil {
		return nil, err
	}
	return cap, nil
}

// CapabilityIDsFromNames is a small helper for tests and future graph views.
func CapabilityIDsFromNames(names []string) []uint64 {
	ids := make([]uint64, 0, len(names))
	for _, name := range names {
		ids = append(ids, core.CapabilityID(name))
	}
	slices.Sort(ids)
	return ids
}
