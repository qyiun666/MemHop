// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Built-in L5 capabilities: read-only reference capabilities shipped with
// the project (see the root capabilities/ directory) — manuals for the
// capabilities an LLM may call itself, plus the guide card indexing them
// (Search / Update / Dream are host-driven loop operations and
// intentionally carry no card). They form the toolbox served by the L5
// read API (ListCapabilities), are never written to the
// .meh file, and are NOT attached to Search responses (Search stays pure
// retrieval of stored capabilities). Lifecycle writes (Activate/Usage/
// Delete) do not apply to them.

package internal

import (
	"github.com/qyiun666/MemHop/internal/cap/capability"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// SetBuiltinCapabilities installs the built-in capabilities served by the
// L5 read APIs. Call once at Open, before the DB is published (no locking).
func (db *DB) SetBuiltinCapabilities(caps []core.Capability) {
	db.builtinCapabilities = caps
}

// findBuiltinCapability returns a copy of the built-in capability with the
// given ID hash, or nil. A copy keeps the shared built-in set immutable:
// callers must not be able to mutate state visible to other readers.
func (db *DB) findBuiltinCapability(idHash uint64) *core.Capability {
	for i := range db.builtinCapabilities {
		if db.builtinCapabilities[i].IDHash == idHash {
			b := db.builtinCapabilities[i]
			return &b
		}
	}
	return nil
}

// builtinMatchingList returns built-in capabilities passing the list
// filters (capability.Matches, the shared predicate), excluding any whose ID
// is already stored (the stored copy carries usage statistics and wins).
func (db *DB) builtinMatchingList(q CapabilityListQuery, kw string, stored map[uint64]struct{}) []core.Capability {
	var out []core.Capability
	for i := range db.builtinCapabilities {
		b := &db.builtinCapabilities[i]
		if _, ok := stored[b.IDHash]; ok {
			continue
		}
		if capability.Matches(b, &q, kw) {
			out = append(out, *b)
		}
	}
	return out
}
