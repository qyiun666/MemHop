// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Agent tenant registry records: one RecAgentRegistry frame per agent maps
// the random 8-byte agentID to its external name. The record lives inside
// the agent's own domain (idHash == agentID), so DeleteAgentRecords removes
// it together with everything else; Open rebuilds the name map by scanning.

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
// agentID -> name; corrupt or empty entries are skipped so a damaged record
// never breaks Open.
func ListAgentRegistry(engine *core.StorageEngine) map[uint64]string {
	out := make(map[uint64]string)
	for agentID := range engine.IterAgents() {
		for idHash := range engine.IndexByType(agentID, core.RecAgentRegistry) {
			_, data, err := engine.ReadRecord(agentID, idHash)
			if err != nil {
				continue
			}
			var name string
			if err := json.Unmarshal(data, &name); err != nil || name == "" {
				continue
			}
			out[agentID] = name
		}
	}
	return out
}
