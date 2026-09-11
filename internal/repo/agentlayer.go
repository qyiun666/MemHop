// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Agent tenant registry records: one RecAgentRegistry frame per agent maps
// the random 8-byte agentID to its external name. The record lives inside
// the agent's own domain (idHash == agentID); Open rebuilds the name map by
// scanning.
//
// The payload is that name and nothing else — a tenant key to resolve a domain
// by, not a description of it. What kind of agent a domain holds lives on its
// L0 profile instead (core.AgentTypePrimary / core.AgentTypeSub). Keeping
// identity off this record is what makes the primary domain unnameable: the
// primary carries no registry record at all, so no name can resolve to it, and
// a caller asking for a sub agent by name can never land on the primary by
// accident.

package repo

import (
	"encoding/json"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// WriteAgentRegistry upserts the tenant registration record of one agent.
func WriteAgentRegistry(engine *core.StorageEngine, agentID uint64, name string) error {
	data, err := json.Marshal(name)
	if err != nil {
		return common.NewError(common.ErrSerialization, "agent registry", err)
	}
	_, err = engine.WriteRecord(agentID, core.RecAgentRegistry, agentID, data)
	return err
}

// ListAgentRegistry scans every domain's registry records and returns
// agentID -> name, plus the failure of the first record that exists but resolves
// to no name — one that will not read back, will not decode, or carries an empty
// key. Those two answers are not interchangeable: a domain holding an unreadable
// key is still a domain, and the key it should have carried is exactly what will
// not read, so it cannot be attributed to any name. A caller that only lists is
// free to ignore the failure; a caller about to hand out a domain by name is not.
func ListAgentRegistry(engine *core.StorageEngine) (map[uint64]string, error) {
	out := make(map[uint64]string)
	var unresolved error
	for agentID := range engine.IterAgents() {
		for idHash := range engine.IndexByType(agentID, core.RecAgentRegistry) {
			_, data, err := engine.ReadRecord(agentID, idHash)
			var name string
			if err == nil {
				if uerr := json.Unmarshal(data, &name); uerr != nil {
					err = common.NewError(common.ErrDeserialization, "unmarshal tenant key", uerr)
				} else if name == "" {
					err = common.NewError(common.ErrDeserialization, "the key is empty")
				}
			}
			if err != nil {
				if unresolved == nil {
					unresolved = common.NewError(common.CodeOf(err),
						"agent registry: domain "+common.FormatHash(agentID)+" carries no readable tenant key", err)
				}
				continue
			}
			out[agentID] = name
		}
	}
	return out, unresolved
}
