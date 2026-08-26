// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 API of the public facade: thin delegation to the internal layer
// DB methods, reusing the DB instance returned by Open.

package api

import (
	"github.com/qyiun666/MemHop/internal/common"
)

// GetCapability reads one L5 capability by ID.
func (db *DB) GetCapability(id string) (*Capability, error) {
	return db.DB.GetCapability(id)
}

// ImportCapability imports/upserts a memhop-capability/v3 file. The write
// lock is held here; internal.ImportCapability does no locking itself.
func (db *DB) ImportCapability(path string) (*Capability, error) {
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
// lock is held here; internal.UpdateCapability does no locking itself.
func (db *DB) UpdateCapability(id string, patch CapabilityPatch) (*Capability, error) {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.UpdateCapability(id, patch)
}

// ListCapabilities lists and filters L5 capabilities.
func (db *DB) ListCapabilities(q CapabilityListQuery) ([]Capability, error) {
	return db.DB.ListCapabilities(q)
}

// ActivateCapability promotes a crystallized draft capability to active.
func (db *DB) ActivateCapability(id string) (*Capability, error) {
	return db.DB.ActivateCapability(id)
}

// RecordCapabilityUsage records host feedback after a capability was used.
func (db *DB) RecordCapabilityUsage(id string, success bool) (*Capability, error) {
	return db.DB.RecordCapabilityUsage(id, success)
}
