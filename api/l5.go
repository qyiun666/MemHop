// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 API of the public facade: thin delegation to the internal layer
// DB methods, reusing the DB instance returned by Open.

package api

import "github.com/qyiun666/MemHop/internal/repo/core"

// GetCapability reads one L5 capability by ID.
func (db *DB) GetCapability(id string) (*Capability, error) {
	return db.DB.GetCapability(core.DefaultAgentID, id)
}

// ImportCapability imports/upserts a memhop-capability/v3 file.
func (db *DB) ImportCapability(path string) (*Capability, error) {
	return db.DB.ImportCapability(core.DefaultAgentID, path)
}

// DeleteCapability removes an L5 capability record.
func (db *DB) DeleteCapability(id string) error {
	return db.DB.DeleteCapability(core.DefaultAgentID, id)
}

// UpdateCapability partially updates an L5 capability record.
func (db *DB) UpdateCapability(id string, patch CapabilityPatch) (*Capability, error) {
	return db.DB.UpdateCapability(core.DefaultAgentID, id, patch)
}

// ListCapabilities lists and filters L5 capabilities.
func (db *DB) ListCapabilities(q CapabilityListQuery) ([]Capability, error) {
	return db.DB.ListCapabilities(core.DefaultAgentID, q)
}

// ActivateCapability promotes a crystallized draft capability to active.
func (db *DB) ActivateCapability(id string) (*Capability, error) {
	return db.DB.ActivateCapability(core.DefaultAgentID, id)
}

// RecordCapabilityUsage records host feedback after a capability was used.
func (db *DB) RecordCapabilityUsage(id string, success bool) (*Capability, error) {
	return db.DB.RecordCapabilityUsage(core.DefaultAgentID, id, success)
}
