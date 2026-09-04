// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Multi-agent tenant management of the internal layer: CreateAgent
// allocates a random 8-byte agentID and persists a registry record so the
// name -> ID mapping survives restarts without stateless hashing; ListAgents
// enumerates registered agents; DeleteAgent destroys the domain context and
// tombstones every record of the domain.

package internal

import (
	"cmp"
	"crypto/rand"
	"encoding/binary"
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// AgentInfo is one registered agent as reported by ListAgents.
type AgentInfo struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

// CreateAgent returns the stable agentID for name, allocating a fresh
// crypto/rand ID (and writing its registry record) on first use. Different
// names never share an ID; the default domain is never handed out. The
// registry record is written under agentsMu so an ID becomes visible only
// after it is persisted; the fsync briefly blocks every domain lookup
// (agentsMu also guards contextFor) — accepted because CreateAgent is a
// low-frequency lifecycle operation.
func (db *DB) CreateAgent(name string) (uint64, error) {
	if db.closed.Load() {
		return 0, common.NewError(common.ErrClosed, "database is closed")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, common.NewError(common.ErrInvalidQuery, "agent name is empty")
	}
	db.agentsMu.Lock()
	defer db.agentsMu.Unlock()
	if db.nameToID == nil {
		db.nameToID = make(map[string]uint64)
		db.idToName = make(map[uint64]string)
	}
	if id, ok := db.nameToID[name]; ok {
		return id, nil
	}
	for {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return 0, common.NewError(common.ErrIO, "agent id allocation", err)
		}
		id := binary.LittleEndian.Uint64(b[:])
		if id == core.DefaultAgentID {
			continue
		}
		if _, taken := db.idToName[id]; taken {
			continue
		}
		if err := repo.WriteAgentRegistry(db.engine, id, name); err != nil {
			return 0, err
		}
		db.nameToID[name] = id
		db.idToName[id] = name
		return id, nil
	}
}

// ListAgents returns every registered agent sorted by ID; the default
// domain is implicit and not listed.
func (db *DB) ListAgents() ([]AgentInfo, error) {
	if db.closed.Load() {
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	db.agentsMu.Lock()
	defer db.agentsMu.Unlock()
	out := make([]AgentInfo, 0, len(db.idToName))
	for id, name := range db.idToName {
		out = append(out, AgentInfo{ID: id, Name: name})
	}
	slices.SortFunc(out, func(a, b AgentInfo) int {
		return cmp.Compare(a.ID, b.ID)
	})
	return out, nil
}

// HasAgent reports whether agentID is the default domain or a registered
// tenant; Session uses it to reject unknown IDs.
func (db *DB) HasAgent(agentID uint64) bool {
	if agentID == core.DefaultAgentID {
		return true
	}
	db.agentsMu.Lock()
	defer db.agentsMu.Unlock()
	_, ok := db.idToName[agentID]
	return ok
}

// CheckSession is the session-eligibility policy for the multi-agent
// facade: the database must be open and agentID must address a registered
// tenant or the default domain. It returns the error the public Session
// constructor surfaces, keeping the decision in the business layer.
func (db *DB) CheckSession(agentID uint64) error {
	if db.closed.Load() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	if !db.HasAgent(agentID) {
		return common.NewError(common.ErrAgentNotFound, "unknown agent: "+common.FormatHash(agentID))
	}
	return nil
}

// DeleteAgent tombstones every record of the domain (registry record
// included) and drops the tenant mapping. An id the registry does not know is
// refused before anything is destroyed. The mapping goes first so no new
// context can be created mid-delete; the context's tombstone then rejects
// stale handles, opCtx cancels so a pending Dream exits at its next
// stage boundary, and the domain-lock barrier waits for any operation
// still holding ac.Mu before the engine deletes the records. The tombstones
// free no space until the host writes a defragmented copy (DB.CompactTo). The
// default domain cannot be deleted.
func (db *DB) DeleteAgent(agentID uint64) error {
	if agentID == core.DefaultAgentID {
		return common.NewError(common.ErrInvalidQuery, "the default domain cannot be deleted")
	}
	if db.closed.Load() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	db.agentsMu.Lock()
	name := db.idToName[agentID]
	if name == "" {
		db.agentsMu.Unlock()
		// Not registered (never was, or already deleted): the registry refuses
		// to hand out a Session for this same id, so deleting it must not claim
		// a record deletion that did not happen.
		return common.NewError(common.ErrAgentNotFound, "unknown agent: "+common.FormatHash(agentID), nil)
	}
	delete(db.nameToID, name)
	delete(db.idToName, agentID)
	db.agentsMu.Unlock()
	if ac := db.destroyContext(agentID); ac != nil {
		ac.Deleted.Store(true)
		ac.Mu.Lock()
		ac.Mu.Unlock() //nolint:staticcheck // barrier only
	}
	if _, err := db.engine.DeleteAgentRecords(agentID); err != nil {
		// The record deletion failed: restore the tenant mapping so the
		// domain stays reachable (a name that vanished while its records
		// survive would orphan them and let CreateAgent mint a duplicate ID).
		// Guarded: never clobber a same-name mapping a concurrent CreateAgent
		// re-established while this deletion was in flight.
		db.agentsMu.Lock()
		if _, exists := db.nameToID[name]; !exists {
			db.nameToID[name] = agentID
		}
		if _, exists := db.idToName[agentID]; !exists {
			db.idToName[agentID] = name
		}
		db.agentsMu.Unlock()
		return err
	}
	return nil
}
