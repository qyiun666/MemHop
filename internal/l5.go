// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 API of the internal assembly layer: thin delegation to the sub layer
// DB methods, reusing the DB instance returned by Open.

package memhop

import (
	"github.com/qyiun666/MemHop/internal/sub"
	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// GetCapability reads one L5 capability by ID.
func (db *DB) GetCapability(id string) (*core.Capability, error) {
	return db.DB.GetCapability(id)
}

// ImportCapability imports/upserts a memhop-capability/v2 file. The write
// lock is held here; sub.ImportCapability does no locking itself.
func (db *DB) ImportCapability(path string) (*core.Capability, error) {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.ImportCapability(path)
}

// DeleteCapability removes an L5 capability record.
func (db *DB) DeleteCapability(id string) error {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.DeleteCapability(id)
}

// UpdateCapability partially updates an L5 capability record. The write
// lock is held here; sub.UpdateCapability does no locking itself.
func (db *DB) UpdateCapability(id string, patch sub.CapabilityPatch) (*core.Capability, error) {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.UpdateCapability(id, patch)
}

// ListCapabilities lists and filters L5 capabilities.
func (db *DB) ListCapabilities(q sub.CapabilityListQuery) ([]core.Capability, error) {
	return db.DB.ListCapabilities(q)
}

// ActivateCapability promotes a crystallized draft capability to active.
func (db *DB) ActivateCapability(id string) (*core.Capability, error) {
	return db.DB.ActivateCapability(id)
}

// RecordCapabilityUsage records host feedback after a capability was used.
func (db *DB) RecordCapabilityUsage(id string, success bool) (*core.Capability, error) {
	return db.DB.RecordCapabilityUsage(id, success)
}
