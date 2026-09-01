// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package api is the public facade of MemHop. It contains no business
// logic: handle types embed the internal domain-bound session
// (internal.Session) so the promoted method set is exactly the externally
// callable surface, and every remaining method is a one-line forward to the
// internal composition root.
//
// Multi-agent is the only mode: OpenMulti is the single entry point, every
// memory operation runs through an agent-domain session addressed by the
// 16-char hex agent id.

package api

import (
	"github.com/qyiun666/MemHop/capabilities"
	"github.com/qyiun666/MemHop/internal"
)

// MultiAgentDB is the public handle returned by OpenMulti: one shared .meh
// file carrying isolated agent domains. Business operations are reached via
// Session(hexID); the methods below cover tenant management and file-level
// lifecycle only.
type MultiAgentDB struct {
	db *internal.DB
}

// OpenMulti creates or opens a multi-agent MemHop database. internal.Open
// performs all assembly (engine, per-domain caches, builtins); the embedded
// capability toolbox is injected here because internal must not import the
// capabilities data package.
func OpenMulti(cfg *MemHopConfig) (*MultiAgentDB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	d, err := internal.Open(cfg, capabilities.FS)
	if err != nil {
		return nil, err
	}
	return &MultiAgentDB{db: d}, nil
}

// ---- tenant management (hex-id surface) ----

// CreateAgent returns the stable 16-char hex agent id for name, registering
// a new tenant on first use.
func (m *MultiAgentDB) CreateAgent(name string) (string, error) {
	id, err := m.db.CreateAgent(name)
	if err != nil {
		return "", err
	}
	return internal.FormatAgentID(id), nil
}

// AgentInfo is one registered agent on the public hex-id surface.
type AgentInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListAgents returns every registered agent, sorted by id.
func (m *MultiAgentDB) ListAgents() ([]AgentInfo, error) {
	agents, err := m.db.ListAgents()
	if err != nil {
		return nil, err
	}
	out := make([]AgentInfo, len(agents))
	for i, a := range agents {
		out[i] = AgentInfo{ID: internal.FormatAgentID(a.ID), Name: a.Name}
	}
	return out, nil
}

// DeleteAgent removes a tenant domain: in-flight Dreams are cancelled,
// every record of the domain is tombstoned and the name mapping is dropped.
func (m *MultiAgentDB) DeleteAgent(agentIDHex string) error {
	id, err := internal.ParseAgentID(agentIDHex)
	if err != nil {
		return err
	}
	return m.db.DeleteAgent(id)
}

// Session returns the per-agent handle bound to the 16-char hex agent id.
// The id must address a registered tenant or the implicit default domain
// (all-zero hex); admission is enforced in the internal layer.
func (m *MultiAgentDB) Session(agentIDHex string) (*Session, error) {
	id, err := internal.ParseAgentID(agentIDHex)
	if err != nil {
		return nil, err
	}
	s, err := m.db.NewSession(id)
	if err != nil {
		return nil, err
	}
	return &Session{s}, nil
}

// ---- file-level lifecycle ----

// Checkpoint persists the per-agent index snapshots without closing.
func (m *MultiAgentDB) Checkpoint() error { return m.db.Checkpoint() }

// Close checkpoints every agent domain and releases the file.
func (m *MultiAgentDB) Close() error { return m.db.Close() }

// IsClosed reports whether the database has been closed.
func (m *MultiAgentDB) IsClosed() bool { return m.db.IsClosed() }

// Lock serializes the default agent domain against host-side writes to the
// database file made outside the library; other domains keep running.
// Unlock resumes. Panics on a closed DB (Unlock is then a no-op).
func (m *MultiAgentDB) Lock()   { m.db.Lock() }
func (m *MultiAgentDB) Unlock() { m.db.Unlock() }
