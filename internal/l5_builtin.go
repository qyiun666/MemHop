// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Built-in L5 capabilities: read-only reference capabilities shipped with
// the project (see the root capabilities/ directory) — MemHop's own usage
// manuals plus the atomic capability cards a harness/agent is expected to
// have. They form the toolbox served by the L5 read APIs (ListCapabilities
// / GetCapability), are never written to the .meh file, and are NOT
// attached to Search responses (Search stays pure retrieval of stored
// capabilities). Lifecycle writes (Activate/Usage/Delete) do not apply to
// them.

package internal

import (
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// SetBuiltinCapabilities installs the built-in capabilities served by the
// L5 read APIs. Call once at Open, before the DB is published (no locking).
func (db *DB) SetBuiltinCapabilities(caps []core.Capability) {
	db.builtinCapabilities = caps
}

// findBuiltinCapability returns a copy of the built-in capability with the
// given 16-hex ID, or nil. A copy keeps the shared built-in set immutable:
// callers must not be able to mutate state visible to other readers.
func (db *DB) findBuiltinCapability(id string) *core.Capability {
	idHash, err := common.ParseID(id)
	if err != nil {
		return nil
	}
	for i := range db.builtinCapabilities {
		if db.builtinCapabilities[i].IDHash == idHash {
			b := db.builtinCapabilities[i]
			return &b
		}
	}
	return nil
}

// builtinMatchingList returns built-in capabilities passing the list
// filters, excluding any whose ID is already stored (the stored copy
// carries usage statistics and wins).
func (db *DB) builtinMatchingList(q CapabilityListQuery, kw string, stored map[uint64]struct{}) []core.Capability {
	var out []core.Capability
	for _, b := range db.builtinCapabilities {
		if _, ok := stored[b.IDHash]; ok {
			continue
		}
		if q.Status != nil && b.Status != *q.Status {
			continue
		}
		if q.Type != nil && b.Type != *q.Type {
			continue
		}
		if kw != "" && !strings.Contains(strings.ToLower(b.Name+" "+b.Summary+" "+b.Trigger), kw) {
			continue
		}
		out = append(out, b)
	}
	return out
}
