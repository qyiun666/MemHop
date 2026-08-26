// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Tenant management of the multi-agent facade.

package api

import (
	"github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
)

// AgentInfo is one registered agent as reported by ListAgents.
type AgentInfo = internal.AgentInfo

// CreateAgent returns the stable agentID for name, registering a new tenant
// on first use. agentIDs are externally rendered as 16-char hex via
// FormatAgentID.
func (m *MultiAgentDB) CreateAgent(name string) (uint64, error) {
	return m.db.CreateAgent(name)
}

// ListAgents returns every registered agent sorted by ID.
func (m *MultiAgentDB) ListAgents() ([]AgentInfo, error) {
	return m.db.ListAgents()
}

// DeleteAgent removes a tenant: in-flight Dreams are cancelled, every
// record of the domain is tombstoned and the name mapping is dropped.
func (m *MultiAgentDB) DeleteAgent(agentID uint64) error {
	return m.db.DeleteAgent(agentID)
}

// FormatAgentID renders an agentID as its external 16-char hex form.
func FormatAgentID(agentID uint64) string {
	return common.FormatHash(agentID)
}

// ParseAgentID parses a 16-char hex agentID.
func ParseAgentID(s string) (uint64, error) {
	return common.ParseID(s)
}
