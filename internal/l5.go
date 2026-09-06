// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 capability operations of the internal layer: package import / query /
// lifecycle / usage feedback. The L5 pool is file-wide: records live in the
// reserved shared domain (core.SharedPoolAgentID) and every agent domain
// operates on the same pool through lockSharedPool. MemHop stores
// capabilities; the host executes them from the referenced paths or
// registered MCP tools.

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

// ImportCapability reads a memhop-capability/v4 package file (or a directory
// containing capability.json) and upserts every card into the shared L5 pool.
// Repeated imports by the same name update the definition while preserving
// usage statistics; per-card failures are reported in the result, not fatal.
func (db *DB) ImportCapability(agentID uint64, path string) (*core.CapabilityImportResult, error) {
	data, resolved, err := capability.ReadFile(path)
	if err != nil {
		return nil, err
	}
	caps, err := capability.BuildPackage(data, resolved)
	if err != nil {
		return nil, err
	}
	ac, err := db.lockSharedPool(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	return db.importCapabilitiesLocked(caps), nil
}

// importCapabilitiesLocked upserts parsed cards into the shared pool. The
// pool lock must already be held (lockSharedPool). A card whose stored copy
// carries the same FileHash is left untouched: re-importing an unchanged
// package (the plug/ scan runs on every Open) must not grow the append-only
// file, and host edits to such a card survive until the package content
// actually changes. A card whose name-derived id belongs to the read-only
// built-in toolbox is rejected — a stored shadow could never be updated or
// deleted again. Created/updated ids are 16-hex; a failed card is reported
// by name.
func (db *DB) importCapabilitiesLocked(caps []*core.Capability) *core.CapabilityImportResult {
	now := time.Now().UnixMilli()
	result := &core.CapabilityImportResult{CreatedIDs: []string{}, UpdatedIDs: []string{}}
	for _, cap := range caps {
		cap.IDHash = core.CapabilityID(cap.Name)
		id := common.FormatHash(cap.IDHash)
		if existing, err := core.ReadCapability(db.engine, core.SharedPoolAgentID, cap.IDHash); err == nil &&
			existing.FileHash != "" && existing.FileHash == cap.FileHash {
			result.UpdatedIDs = append(result.UpdatedIDs, id)
			continue
		}
		if db.findBuiltinCapability(cap.IDHash) != nil {
			result.Errors = append(result.Errors, cap.Name+": name is reserved by a built-in card")
			continue
		}
		cap.Status = core.CapabilityActive
		cap.Origin = core.CapabilityOriginImported
		cap.CreatedAt = now
		cap.UpdatedAt = now
		created, err := repo.UpsertCapabilityL5(db.engine, core.SharedPoolAgentID, cap)
		if err != nil {
			result.Errors = append(result.Errors, cap.Name+": "+err.Error())
			continue
		}
		// UpsertCapabilityL5 reports updated=true when a stored record was
		// refreshed and false when the card is new.
		if created {
			result.UpdatedIDs = append(result.UpdatedIDs, id)
		} else {
			result.CreatedIDs = append(result.CreatedIDs, id)
		}
	}
	return result
}

// UpdateCapability partially updates a stored capability (built-ins are
// read-only and rejected). The pool is file-wide, so the update is visible to
// every agent domain. The stored FileHash is kept: it is the package
// watermark, not a content fingerprint — re-importing the same package bytes
// stays a no-op, so a host's deprecation or patched definition survives
// restarts; a changed package overwrites the definition and lands active.
func (db *DB) UpdateCapability(agentID uint64, id string, patch CapabilityPatch) (*core.Capability, error) {
	ac, err := db.lockSharedPool(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	idHash, err := common.ParseID(id)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse capability id", err)
	}
	if db.findBuiltinCapability(idHash) != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "built-in capabilities are read-only")
	}
	cap, err := repo.GetCapabilityL5(db.engine, core.SharedPoolAgentID, idHash)
	if err != nil {
		return nil, err
	}
	if patch.Version != nil {
		cap.Version = *patch.Version
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
	if err := capability.ValidateCard(&core.CapabilityImport{
		Name: cap.Name, Version: cap.Version,
		Summary: cap.Summary, Trigger: cap.Trigger,
		Resources: cap.Resources,
	}); err != nil {
		return nil, err
	}
	if _, err := repo.UpsertCapabilityL5(db.engine, core.SharedPoolAgentID, cap); err != nil {
		return nil, err
	}
	return cap, nil
}

// DeleteCapability removes a capability record from the shared pool. Built-in
// capabilities are read-only: deleting one is rejected instead of silently
// succeeding.
func (db *DB) DeleteCapability(agentID uint64, id string) error {
	ac, err := db.lockSharedPool(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	idHash, err := common.ParseID(id)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse capability id", err)
	}
	if db.findBuiltinCapability(idHash) != nil {
		return common.NewError(common.ErrInvalidQuery, "built-in capabilities are read-only")
	}
	// Deleting a card that is not there is reported, not accepted: a host
	// reconciling its cards has to be able to tell a real deletion from a no-op.
	if _, err := repo.GetCapabilityL5(db.engine, core.SharedPoolAgentID, idHash); err != nil {
		return err
	}
	if !repo.DeleteCapabilityL5(db.engine, core.SharedPoolAgentID, idHash) {
		return common.NewError(common.ErrIO, "delete capability", nil)
	}
	return nil
}

// ListCapabilities lists and filters the shared L5 pool.
func (db *DB) ListCapabilities(agentID uint64, q CapabilityListQuery) ([]core.Capability, error) {
	ac, err := db.lockSharedPool(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	kw := strings.ToLower(q.Keyword)
	all := core.CollectAllCapabilities(db.engine, core.SharedPoolAgentID)
	filtered := make([]core.Capability, 0, len(all))
	for _, cap := range all {
		if capability.Matches(&cap, &q, kw) {
			filtered = append(filtered, cap)
		}
	}
	// Merge the built-in toolbox through the same filters; a stored record
	// with the same ID wins over its built-in twin. The dedup set is built
	// from ALL stored records (not just the filtered ones) so a stored
	// record filtered out by status/package still suppresses its built-in
	// twin.
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
	ac, err := db.lockSharedPool(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	idHash, err := common.ParseID(id)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse capability id", err)
	}
	if db.findBuiltinCapability(idHash) != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "built-in capabilities are read-only")
	}
	return repo.ActivateCapabilityL5(db.engine, core.SharedPoolAgentID, idHash)
}

// RecordCapabilityUsage records host feedback after a capability was used.
// Built-in capabilities are read-only and rejected.
func (db *DB) RecordCapabilityUsage(agentID uint64, id string, success bool) (*core.Capability, error) {
	ac, err := db.lockSharedPool(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	idHash, err := common.ParseID(id)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse capability id", err)
	}
	if db.findBuiltinCapability(idHash) != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "built-in capabilities are read-only")
	}
	return repo.RecordCapabilityUsageL5(db.engine, core.SharedPoolAgentID, idHash, success)
}
