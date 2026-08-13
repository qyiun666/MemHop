// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 action chain operations; deletion cascades to ActionSteps.
package repo

import (
	"fmt"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
	"github.com/qyiun666/MemHop/internal/sub/repo/index"
)

// CreateChainL5 creates an action chain; ID = hash(title:trigger).
func CreateChainL5(engine *core.StorageEngine, title, trigger string) (uint64, error) {
	return CreateChainL5WithPath(engine, title, trigger, nil)
}

// CreateChainL5WithPath creates an action chain with an optional location;
// ID = hash(title:trigger), so re-crystallizing the same pattern is idempotent.
func CreateChainL5WithPath(engine *core.StorageEngine, title, trigger string, path *string) (uint64, error) {
	chainID := common.HashID(fmt.Sprintf("%s:%s", title, trigger))
	now := time.Now().UnixMilli()
	chain := &core.ActionChainSlot{
		IDHash:    chainID,
		Title:     title,
		Trigger:   trigger,
		Path:      path,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := core.WriteActionChainSlot(engine, chainID, chain); err != nil {
		return 0, err
	}
	return chainID, nil
}

// CreateOrUpdateChainL5WithPath creates a chain or, when the same
// title:trigger already exists, preserves its runtime fields
// (Confidence/SuccessRate/TriggerCount/...) and only refreshes Path and
// UpdatedAt. Returns the chain ID and whether it already existed.
func CreateOrUpdateChainL5WithPath(engine *core.StorageEngine, title, trigger string, path *string) (uint64, bool, error) {
	chainID := common.HashID(fmt.Sprintf("%s:%s", title, trigger))
	now := time.Now().UnixMilli()
	if existing, err := core.ReadActionChainSlot(engine, chainID); err == nil {
		existing.Path = path
		existing.UpdatedAt = now
		if err := core.WriteActionChainSlot(engine, chainID, existing); err != nil {
			return 0, true, err
		}
		return chainID, true, nil
	}
	chain := &core.ActionChainSlot{
		IDHash:    chainID,
		Title:     title,
		Trigger:   trigger,
		Path:      path,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := core.WriteActionChainSlot(engine, chainID, chain); err != nil {
		return 0, false, err
	}
	return chainID, false, nil
}

// DeleteStepsL5 removes all steps of a chain (keeps the chain record),
// so re-crystallization with fewer steps leaves no orphan steps behind.
func DeleteStepsL5(engine *core.StorageEngine, chainID uint64) error {
	var targets []uint64
	for _, step := range core.CollectAllActionSteps(engine) {
		if step.ChainID == chainID {
			targets = append(targets, step.IDHash)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	_, err := engine.DeleteRecordBatch(targets)
	return err
}

// CreateStepL5 creates one action step; ID = hash(chainID:stepOrder).
func CreateStepL5(engine *core.StorageEngine, chainID uint64, stepOrder uint16, action string, params *string) (uint64, error) {
	stepID := common.HashID(fmt.Sprintf("%d:%d", chainID, stepOrder))
	step := &core.ActionStep{
		IDHash:     stepID,
		ChainID:    chainID,
		StepOrder:  stepOrder,
		Action:     action,
		Parameters: params,
		CreatedAt:  time.Now().UnixMilli(),
	}
	if err := core.WriteActionStep(engine, stepID, step); err != nil {
		return 0, err
	}
	return stepID, nil
}

func GetChainL5(engine *core.StorageEngine, id string) (*core.ActionChainSlot, error) {
	idHash, err := common.ParseID(id)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse chain id", err)
	}
	return core.ReadActionChainSlot(engine, idHash)
}

func UpdateChainL5(engine *core.StorageEngine, id string, slot *core.ActionChainSlot) error {
	idHash, err := common.ParseID(id)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse chain id", err)
	}
	slot.IDHash = idHash
	slot.UpdatedAt = time.Now().UnixMilli()
	return core.WriteActionChainSlot(engine, idHash, slot)
}

// DeleteChainL5 deletes all ActionSteps of the chain plus the chain record
// in one batch.
func DeleteChainL5(engine *core.StorageEngine, id string) bool {
	chainHash, err := common.ParseID(id)
	if err != nil {
		return false
	}
	var targets []uint64
	for _, step := range core.CollectAllActionSteps(engine) {
		if step.ChainID == chainHash {
			targets = append(targets, step.IDHash)
		}
	}
	targets = append(targets, chainHash)
	_, err = engine.DeleteRecordBatch(targets)
	return err == nil
}

func ListChainsL5(engine *core.StorageEngine) []core.ActionChainSlot {
	return core.CollectAllActionChains(engine)
}

func MatchChainsL5(engine *core.StorageEngine, query string) []core.ActionChainSlot {
	terms := index.Tokenize(query)
	if len(terms) == 0 {
		return nil
	}
	var out []core.ActionChainSlot
	for _, chain := range core.CollectAllActionChains(engine) {
		text := strings.ToLower(chain.Title + " " + chain.Trigger)
		for _, term := range terms {
			if strings.Contains(text, strings.ToLower(term)) {
				out = append(out, chain)
				break
			}
		}
	}
	return out
}
