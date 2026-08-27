// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 capability operations of the internal layer: path import / query / lifecycle /
// usage feedback. MemHop stores capabilities; the host executes them from
// the referenced paths or registered MCP tools.

package internal

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/cap/capability"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ImportCapability reads a memhop-capability/v3 file (or a directory
// containing capability.json) and upserts it into L5. Repeated imports by
// the same name update the definition while preserving usage statistics.
func (db *DB) ImportCapability(agentID uint64, path string) (*core.Capability, error) {
	data, resolved, err := capability.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return db.importCapabilityData(agentID, data, resolved)
}

// importCapabilityData upserts one imported capability document into L5.
// Re-importing byte-identical content under the same name is a no-op: the
// append-only file must not grow on every startup import.
func (db *DB) importCapabilityData(agentID uint64, data []byte, source string) (*core.Capability, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
	cap, err := capability.Build(data, source)
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	cap.Status = core.CapabilityActive
	cap.Origin = core.CapabilityOriginImported
	cap.CreatedAt = now
	cap.UpdatedAt = now
	// Byte-identical re-import under the same name: return the stored
	// record without appending, preserving usage stats and timestamps.
	if existing, err := core.ReadCapability(db.engine, agentID, core.CapabilityID(cap.Name)); err == nil &&
		existing.FileHash != "" && existing.FileHash == cap.FileHash {
		return existing, nil
	}
	if _, err := repo.UpsertCapabilityL5(db.engine, agentID, cap); err != nil {
		return nil, err
	}
	return cap, nil
}

// GetCapability reads one L5 capability by ID. IDs not stored in the file
// fall back to the built-in toolbox, so listed built-ins stay retrievable.
func (db *DB) GetCapability(agentID uint64, id string) (*core.Capability, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
	cap, err := repo.GetCapabilityL5(db.engine, agentID, id)
	if err != nil {
		if common.CodeOf(err) == common.ErrNotFound {
			if b := db.findBuiltinCapability(id); b != nil {
				return b, nil
			}
		}
		return nil, err
	}
	return cap, nil
}

// UpdateCapability partially updates a stored capability (built-ins are
// read-only and rejected).
func (db *DB) UpdateCapability(agentID uint64, id string, patch CapabilityPatch) (*core.Capability, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
	if db.findBuiltinCapability(id) != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "built-in capabilities are read-only")
	}
	cap, err := repo.GetCapabilityL5(db.engine, agentID, id)
	if err != nil {
		return nil, err
	}
	if patch.Version != nil {
		cap.Version = *patch.Version
	}
	if patch.Type != nil {
		cap.Type = *patch.Type
	}
	if patch.Summary != nil {
		cap.Summary = *patch.Summary
	}
	if patch.Trigger != nil {
		cap.Trigger = *patch.Trigger
	}
	if patch.Status != nil {
		cap.Status = *patch.Status
	}
	if patch.Resources != nil {
		cap.Resources = *patch.Resources
	}
	if patch.Workflow != nil {
		cap.Workflow = patch.Workflow
	}
	if err := capability.Validate(&CapabilityImport{
		Name: cap.Name, Version: cap.Version, Type: cap.Type,
		Summary: cap.Summary, Trigger: cap.Trigger,
		Resources: cap.Resources, Workflow: cap.Workflow,
	}); err != nil {
		return nil, err
	}
	// The stored content is no longer the imported bytes.
	cap.FileHash = ""
	if _, err := repo.UpsertCapabilityL5(db.engine, agentID, cap); err != nil {
		return nil, err
	}
	return cap, nil
}

// DeleteCapability removes a capability record. Built-in capabilities are
// read-only: deleting one is rejected instead of silently succeeding.
func (db *DB) DeleteCapability(agentID uint64, id string) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.mu.Unlock()
	if _, err := common.ParseID(id); err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse capability id", err)
	}
	if db.findBuiltinCapability(id) != nil {
		return common.NewError(common.ErrInvalidQuery, "built-in capabilities are read-only")
	}
	if !repo.DeleteCapabilityL5(db.engine, agentID, id) {
		return common.NewError(common.ErrIO, "delete capability", nil)
	}
	return nil
}

// ListCapabilities lists and filters L5 capabilities.
func (db *DB) ListCapabilities(agentID uint64, q CapabilityListQuery) ([]core.Capability, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
	kw := strings.ToLower(q.Keyword)
	all := core.CollectAllCapabilities(db.engine, agentID)
	filtered := make([]core.Capability, 0, len(all))
	for _, cap := range all {
		if capability.Matches(&cap, &q, kw) {
			filtered = append(filtered, cap)
		}
	}
	// Merge the built-in toolbox through the same filters; a stored record
	// with the same ID wins over its built-in twin. The dedup set is built
	// from ALL stored records (not just the filtered ones) so a stored
	// record filtered out by status/kind still suppresses its built-in twin.
	stored := make(map[uint64]struct{}, len(all))
	for _, cap := range all {
		stored[cap.IDHash] = struct{}{}
	}
	filtered = append(filtered, db.builtinMatchingList(q, kw, stored)...)
	slices.SortFunc(filtered, func(a, b core.Capability) int {
		return cmp.Compare(b.UpdatedAt, a.UpdatedAt)
	})
	if filtered == nil {
		return []core.Capability{}, nil
	}
	return filtered, nil
}

// ActivateCapability promotes a draft capability to active. Built-in
// capabilities are read-only and rejected.
func (db *DB) ActivateCapability(agentID uint64, id string) (*core.Capability, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
	if db.findBuiltinCapability(id) != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "built-in capabilities are read-only")
	}
	return repo.ActivateCapabilityL5(db.engine, agentID, id)
}

// RecordCapabilityUsage records host feedback after a capability was used.
// Built-in capabilities are read-only and rejected.
func (db *DB) RecordCapabilityUsage(agentID uint64, id string, success bool) (*core.Capability, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
	if db.findBuiltinCapability(id) != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "built-in capabilities are read-only")
	}
	return repo.RecordCapabilityUsageL5(db.engine, agentID, id, success)
}
