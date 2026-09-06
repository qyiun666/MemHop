// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Built-in L5 capabilities: read-only reference cards assembled in code by
// BuiltinCards (builtin_capabilities.go) — manuals covering every api.Session
// business method. They are served by the L5 read API (ListCapabilities),
// never written to the .meh file, and NOT attached to Search responses
// (Search stays pure retrieval of stored capabilities). A stored record with
// the same id shadows its built-in twin in listings; a write to a
// built-in-only id reports ErrNotFound like any record that is not there,
// and Crystallize's fold-back rejects candidates whose id would shadow a
// manual (findBuiltinCapability).

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

// findBuiltinCapability reports whether the id hash names a built-in card.
// Used by Crystallize's fold-back: an LLM-minted candidate must never shadow
// a manual in listings.
func (db *DB) findBuiltinCapability(idHash uint64) bool {
	for i := range db.builtinCapabilities {
		if db.builtinCapabilities[i].IDHash == idHash {
			return true
		}
	}
	return false
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
