// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 profile record primitives.
package repo

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// L0 profile operations: the singleton ProfileSlot at the fixed ID
// hash("profile") inside the agent domain. An absent record is ErrNotFound; a
// record that cannot be read or decoded keeps its own code — callers branch on
// that difference, because seeding or inheriting a profile is a decision for
// the first answer only.
func GetProfileL0(engine *core.StorageEngine, agentID uint64) (*core.ProfileSlot, error) {
	slot, err := core.ReadProfileSlot(engine, agentID, common.HashID("profile"))
	if err != nil {
		return nil, common.NewError(common.CodeOf(err), "read profile", err)
	}
	return slot, nil
}

func UpdateProfileL0(engine *core.StorageEngine, agentID uint64, slot *core.ProfileSlot) error {
	id := common.HashID("profile")
	return core.WriteProfileSlot(engine, agentID, id, slot)
}

// HasProfileL0 answers whether a domain's profile record is there, for a caller
// that acts on presence alone. An absent record is (false, nil), an unreadable
// one is an error: seeding a profile on that second answer would overwrite a
// record this call could not see.
func HasProfileL0(engine *core.StorageEngine, agentID uint64) (bool, error) {
	_, err := core.ReadProfileSlot(engine, agentID, common.HashID("profile"))
	if err == nil {
		return true, nil
	}
	if common.CodeOf(err) == common.ErrNotFound {
		return false, nil
	}
	return false, err
}
