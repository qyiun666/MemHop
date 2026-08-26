// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Multi-agent facade of the public api layer: one shared .meh file, one
// isolated agent domain per tenant. Single-agent hosts keep using Open,
// which maps every call to the default domain.

package api

import (
	"github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
)

// MultiAgentDB is the multi-tenant handle returned by OpenMulti.
type MultiAgentDB struct {
	db *internal.DB
}

// OpenMulti creates or opens a multi-agent MemHop database.
func OpenMulti(cfg *MemHopConfig) (*MultiAgentDB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	enc, err := internal.CreateEncoder(cfg)
	if err != nil {
		return nil, err
	}
	return openMultiWithEncoder(cfg, enc)
}

// OpenMultiWithEncoder creates or opens a multi-agent MemHop database with
// a custom encoder.
func OpenMultiWithEncoder(cfg *MemHopConfig, enc Encoder) (*MultiAgentDB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return openMultiWithEncoder(cfg, enc)
}

func openMultiWithEncoder(cfg *MemHopConfig, enc Encoder) (*MultiAgentDB, error) {
	d, err := openInternal(cfg, enc)
	if err != nil {
		return nil, err
	}
	return &MultiAgentDB{db: d}, nil
}

// Session returns the per-agent handle bound to agentID. The ID must be the
// default domain or a tenant created via CreateAgent.
func (m *MultiAgentDB) Session(agentID uint64) (*AgentSession, error) {
	if m.db.IsClosed() {
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	if !m.db.HasAgent(agentID) {
		return nil, common.NewError(common.ErrAgentNotFound, "unknown agent: "+common.FormatHash(agentID))
	}
	return &AgentSession{db: m.db, agentID: agentID}, nil
}

// Checkpoint persists the per-agent index snapshots without closing.
func (m *MultiAgentDB) Checkpoint() error { return m.db.Checkpoint() }

// Close checkpoints every agent domain and releases the file.
func (m *MultiAgentDB) Close() error { return m.db.Close() }

// IsClosed reports whether the database has been closed.
func (m *MultiAgentDB) IsClosed() bool { return m.db.IsClosed() }
