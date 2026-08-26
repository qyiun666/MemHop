// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 capability operations: import/upsert, query, delete, lifecycle and
// usage feedback. Capabilities are stored, never executed by MemHop.

package repo

import (
	"slices"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// UpsertCapabilityL5 writes a capability by stable name ID. Existing runtime
// fields (SuccessRate/TriggerCount/LastTriggered/Status when the capability
// is active or deprecated) are preserved.
func UpsertCapabilityL5(engine *core.StorageEngine, cap *core.Capability) (bool, error) {
	cap.IDHash = core.CapabilityID(cap.Name)
	now := time.Now().UnixMilli()
	if existing, err := core.ReadCapability(engine, cap.IDHash); err == nil {
		cap.SuccessRate = existing.SuccessRate
		cap.TriggerCount = existing.TriggerCount
		cap.LastTriggered = existing.LastTriggered
		cap.CreatedAt = existing.CreatedAt
		if cap.Status == core.CapabilityDraft && existing.Status != core.CapabilityDraft {
			cap.Status = existing.Status
		}
		cap.UpdatedAt = now
		if err := core.WriteCapability(engine, cap.IDHash, cap); err != nil {
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
	if err := core.WriteCapability(engine, cap.IDHash, cap); err != nil {
		return false, err
	}
	return false, nil
}

func GetCapabilityL5(engine *core.StorageEngine, id string) (*core.Capability, error) {
	idHash, err := common.ParseID(id)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse capability id", err)
	}
	return core.ReadCapability(engine, idHash)
}

func DeleteCapabilityL5(engine *core.StorageEngine, id string) bool {
	capHash, err := common.ParseID(id)
	if err != nil {
		return false
	}
	_, err = engine.DeleteRecordBatch([]uint64{capHash})
	return err == nil
}

func ListCapabilitiesL5(engine *core.StorageEngine) []core.Capability {
	return core.CollectAllCapabilities(engine)
}

// MatchCapabilitiesL5 returns active capabilities relevant to a query.
// Matching covers name, summary, trigger, resource names/descriptions and
// workflow refs.
func MatchCapabilitiesL5(engine *core.StorageEngine, query string) []core.Capability {
	terms := index.Tokenize(query)
	if len(terms) == 0 {
		return nil
	}
	var out []core.Capability
	for _, cap := range core.CollectAllCapabilities(engine) {
		if cap.Status != core.CapabilityActive {
			continue
		}
		text := capabilitySearchText(cap)
		for _, term := range terms {
			if strings.Contains(text, strings.ToLower(term)) {
				out = append(out, cap)
				break
			}
		}
	}
	return out
}

func capabilitySearchText(cap core.Capability) string {
	var b strings.Builder
	write := func(s string) {
		if s != "" {
			b.WriteByte(' ')
			b.WriteString(s)
		}
	}
	write(cap.Name)
	write(cap.Summary)
	write(cap.Trigger)
	if cap.Workflow != nil {
		for _, step := range cap.Workflow.Steps {
			write(step.Ref)
			write(step.Action)
		}
	}
	for _, r := range cap.Resources {
		write(r.Name)
		write(r.Ref)
		write(r.Desc)
		write(r.Input)
		write(r.Output)
	}
	return strings.ToLower(b.String())
}

// ActivateCapabilityL5 promotes a draft to active.
func ActivateCapabilityL5(engine *core.StorageEngine, id string) (*core.Capability, error) {
	cap, err := GetCapabilityL5(engine, id)
	if err != nil {
		return nil, err
	}
	cap.Status = core.CapabilityActive
	cap.UpdatedAt = time.Now().UnixMilli()
	if err := core.WriteCapability(engine, cap.IDHash, cap); err != nil {
		return nil, err
	}
	return cap, nil
}

// RecordCapabilityUsageL5 updates runtime feedback after a host uses a
// capability.
func RecordCapabilityUsageL5(engine *core.StorageEngine, id string, success bool) (*core.Capability, error) {
	cap, err := GetCapabilityL5(engine, id)
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
	if err := core.WriteCapability(engine, cap.IDHash, cap); err != nil {
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
