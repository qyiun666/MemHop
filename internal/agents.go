// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Agent domain management of the internal layer. Two identities: the primary is
// the implicit zero domain a file is opened on, and a sub agent is a registered
// domain addressed by name. Registration allocates a random 8-byte agentID and
// persists a record so the name -> ID mapping survives restarts without stateless
// hashing. Two reserved domains are never handed out as sub agents: the default
// domain and the file-wide shared pool domain (core.SharedPoolAgentID, carrying
// the L3 knowledge graph).

package internal

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ensureRegistered returns the stable agentID for name, allocating a fresh
// crypto/rand ID (and writing its registry record) on first use. Different
// names never share an ID; the two reserved domains are never handed out. The
// name arrives trimmed and non-empty: that is the caller's business, because
// the caller is where a host's string enters the library. The registry record
// is written under agentsMu so an ID becomes visible only after it is
// persisted; the fsync briefly blocks every domain lookup — accepted because
// creating a domain is a low-frequency operation.
func (db *DB) ensureRegistered(name string) (uint64, error) {
	if db.closed.Load() {
		return 0, common.NewError(common.ErrClosed, "database is closed")
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
	// A name is only free while no domain is holding a key that will not
	// resolve: minting a second domain under a name an unreadable record
	// already carries would hand the host an empty memory with the real one
	// unreachable behind it. The scan is here rather than once at Open because
	// a name may be asked for long after the file was opened, and the registry
	// is written by this call.
	if _, unresolved := repo.ListAgentRegistry(db.engine); unresolved != nil {
		return 0, unresolved
	}
	for {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return 0, common.NewError(common.ErrIO, "agent id allocation", err)
		}
		id := binary.LittleEndian.Uint64(b[:])
		if id == core.DefaultAgentID || id == core.SharedPoolAgentID {
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

// HasAgent reports whether agentID is the default domain or a registered tenant.
func (db *DB) HasAgent(agentID uint64) bool {
	if agentID == core.DefaultAgentID {
		return true
	}
	db.agentsMu.Lock()
	defer db.agentsMu.Unlock()
	_, ok := db.idToName[agentID]
	return ok
}

// Primary returns the session bound to the domain the file was opened on —
// the implicit zero one, so a file holds exactly one primary.
func (db *DB) Primary() (*Session, error) {
	return db.NewSession(core.DefaultAgentID)
}

// maxSubAgentNameBytes caps a tenant key. The registry record holds the name as
// JSON in the file, so an unbounded name is an unbounded record; the cap is
// about that, not about which characters a name may hold.
const maxSubAgentNameBytes = 256

// SubAgent returns the session of the sub-agent domain named profile.Name,
// creating that domain the first time and handing back the same one after. The
// name is a tenant key, frozen at creation: editing the profile's Name
// afterwards does not move the domain, and asking for a name nobody registered
// opens a second one instead of finding the first. Naming the same domain again
// replaces its LLM endpoint.
//
// The profile is written only if the domain has none yet, which makes this
// call self-healing across a crash between the registry record and the
// profile. AgentType is stamped here, not taken from the caller: a domain
// created this way is a sub-agent whatever its profile claims.
func (db *DB) SubAgent(llmCfg LlmConfig, profile core.ProfileSlot) (*Session, error) {
	if err := llmCfg.Validate(); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		return nil, common.NewError(common.ErrInvalidQuery, "sub-agent profile Name is required")
	}
	if len(name) > maxSubAgentNameBytes {
		return nil, common.NewError(common.ErrInvalidQuery,
			fmt.Sprintf("sub-agent name exceeds %d bytes", maxSubAgentNameBytes))
	}
	id, err := db.ensureRegistered(name)
	if err != nil {
		return nil, err
	}
	provider := db.setDomainLLM(id, llmCfg)
	// Session admission reads the registry, so the handle has to be fetched
	// before the domain lock is taken: agentsMu under ac.Mu is the one lock
	// order this layer must never build.
	sess, err := db.NewSession(id)
	if err != nil {
		return nil, err
	}
	ac, err := db.lockAgent(id)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	// contextFor only reads the table when building a context, so a live one
	// has to be re-pointed here — under the domain lock, which is where every
	// operation reads the transport.
	ac.LLM = provider
	has, err := repo.HasProfileL0(db.engine, id)
	if err != nil {
		return nil, err
	}
	if has {
		return sess, nil
	}
	slot := profile
	slot.Name = name
	slot.AgentType = core.AgentTypeSub
	slot.UpdatedAtMs = time.Now().UnixMilli()
	if err := repo.UpdateProfileL0(db.engine, id, &slot); err != nil {
		return nil, err
	}
	return sess, nil
}

// Agent returns the session of a domain this file already holds, addressed by the id the
// library handed out for it — the one `Session.AgentID` renders — and points it at llmCfg
// the way SubAgent does. It creates nothing: an id nobody registered is refused with
// ErrAgentNotFound, so a mistyped or invented id cannot open an empty memory over somebody
// else's. The primary is addressed by its own id too (the implicit zero one), which is the
// same domain Primary hands back.
func (db *DB) Agent(llmCfg LlmConfig, agentIDHex string) (*Session, error) {
	if err := llmCfg.Validate(); err != nil {
		return nil, err
	}
	id, err := parseID("agent", agentIDHex)
	if err != nil {
		return nil, err
	}
	if err := db.CheckSession(id); err != nil {
		return nil, err
	}
	provider := db.setDomainLLM(id, llmCfg)
	sess, err := db.NewSession(id)
	if err != nil {
		return nil, err
	}
	ac, err := db.lockAgent(id)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	// Same reason as in SubAgent: a live context caches the transport, and the lock is
	// what makes that write and every read of it agree.
	ac.LLM = provider
	return sess, nil
}

// CheckSession is the session-eligibility policy: the database must be open
// and agentID must address a registered tenant or the default domain.
func (db *DB) CheckSession(agentID uint64) error {
	if db.closed.Load() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	if !db.HasAgent(agentID) {
		return common.NewError(common.ErrAgentNotFound, "unknown agent: "+common.FormatHash(agentID))
	}
	return nil
}
