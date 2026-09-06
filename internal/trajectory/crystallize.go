// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package trajectory

import (
	"errors"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/cap/capability"
	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// candidateRejected marks a reuse candidate whose target is gone and whose
// payload then failed card validation on the degrade-to-create path;
// ApplyCandidate records it as a skip while other errors are fatal.
type candidateRejected struct{ reason string }

func (e candidateRejected) Error() string { return e.reason }

// ApplyCandidate folds one LLM candidate into the result. create and merge
// overwrite the stored card wholesale, so they run card validation upfront;
// a reuse that hits its target writes nothing and is accepted as-is (the
// prompt may emit a minimal name-only payload), while a reuse that misses
// degrades into a create and is gated inside applyCrystallized instead.
func ApplyCandidate(engine *core.StorageEngine, agentID uint64, cand llmops.CrystallizeCapability, result *core.CrystallizeResult) error {
	detail := core.CrystallizeDetail{Name: cand.Capability.Name}
	recordSkip := func(reason string) {
		detail.Action = "skip"
		detail.Reason = reason
		result.Errors = append(result.Errors, detail.Name+": "+reason)
		result.Details = append(result.Details, detail)
	}
	action := strings.ToLower(strings.TrimSpace(cand.Action))
	if action != "reuse" {
		if err := capability.ValidateCard(&cand.Capability); err != nil {
			recordSkip(err.Error())
			return nil
		}
	}
	id, disposition, err := applyCrystallized(engine, agentID, cand)
	if err != nil {
		var rejected candidateRejected
		if errors.As(err, &rejected) {
			recordSkip(rejected.reason)
			return nil
		}
		return err
	}
	detail.Action = disposition // create | reuse | merge
	detail.CapabilityID = id
	result.Details = append(result.Details, detail)
	switch disposition {
	case "reuse":
		result.ReusedIDs = append(result.ReusedIDs, id)
	case "merge":
		result.MergedIDs = append(result.MergedIDs, id)
	default:
		result.CreatedIDs = append(result.CreatedIDs, id)
	}
	return nil
}

func applyCrystallized(engine *core.StorageEngine, agentID uint64, cand llmops.CrystallizeCapability) (string, string, error) {
	now := time.Now().UnixMilli()
	action := strings.ToLower(strings.TrimSpace(cand.Action))
	if action == "" {
		action = "create"
	}
	cap := capability.BuildCrystallized(&cand.Capability, now)

	// Name is the canonical identity. A create candidate whose name already
	// exists is always treated as reuse: crystallization must never silently
	// overwrite an active capability.
	existing, id, found, err := findTarget(engine, agentID, cap, cand.ReuseID)
	if err != nil {
		return "", "", err
	}
	if found {
		if action == "merge" {
			capability.MergeDefinition(existing, cap, now)
			if _, err := repo.UpsertCapabilityL5(engine, agentID, existing); err != nil {
				return "", "", err
			}
			return id, "merge", nil
		}
		return id, "reuse", nil
	}
	if action == "reuse" {
		// The reuse target is gone and the candidate degrades into a
		// create; its payload skipped upfront validation, so gate it here
		// before it reaches the store.
		if err := capability.ValidateCard(&cand.Capability); err != nil {
			return "", "", candidateRejected{err.Error()}
		}
	}
	if _, err := repo.UpsertCapabilityL5(engine, agentID, cap); err != nil {
		return "", "", err
	}
	return common.FormatHash(cap.IDHash), "create", nil
}

// findTarget locates an existing capability by name ID (canonical
// identity) then explicit ReuseID. found=false means a new record must be
// created. Only a genuine "no such capability" says so: treating a transient
// read failure as absent would re-create an existing card and drop its usage
// counters. A malformed ReuseID (LLM-supplied) is ignored, not fatal.
func findTarget(engine *core.StorageEngine, agentID uint64, cap *core.Capability, reuseID string) (*core.Capability, string, bool, error) {
	nameIDHash := core.CapabilityID(cap.Name)
	existing, err := repo.GetCapabilityL5(engine, agentID, nameIDHash)
	if err == nil {
		return existing, common.FormatHash(nameIDHash), true, nil
	}
	if common.CodeOf(err) != common.ErrNotFound {
		return nil, "", false, err
	}
	if reuseID == "" {
		return nil, "", false, nil
	}
	reuseHash, err := common.ParseID(reuseID)
	if err != nil {
		return nil, "", false, nil
	}
	existing, err = repo.GetCapabilityL5(engine, agentID, reuseHash)
	if err != nil {
		if common.CodeOf(err) != common.ErrNotFound {
			return nil, "", false, err
		}
		return nil, "", false, nil
	}
	return existing, common.FormatHash(existing.IDHash), true, nil
}
