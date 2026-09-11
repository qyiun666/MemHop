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
// record that cannot be read or decoded keeps its own code, because callers
// branch on that difference — seeding or inheriting a profile is a decision for
// the first answer only, never the second.
func GetProfileL0(engine *core.StorageEngine, agentID uint64) (*core.ProfileSlot, error) {
	slot, err := core.ReadProfileSlot(engine, agentID, common.HashID("profile"))
	if err != nil {
		code := common.CodeOf(err)
		if code == 0 {
			code = common.ErrIO
		}
		return nil, common.NewError(code, "read profile", err)
	}
	return slot, nil
}

func UpdateProfileL0(engine *core.StorageEngine, agentID uint64, slot *core.ProfileSlot) error {
	slot.IDHash = common.HashID("profile")
	return core.WriteProfileSlot(engine, agentID, slot.IDHash, slot)
}

// HasProfileL0 answers whether a domain's profile record is there, for a caller
// that acts on presence alone and never reads the payload. An absent record is
// (false, nil), while a record that cannot be read is an error: deciding to seed
// a profile on that second answer would overwrite a record this call could not
// see.
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
