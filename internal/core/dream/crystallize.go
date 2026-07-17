// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"encoding/json"
	"fmt"

	"memhop/internal/core"
	"memhop/internal/core/model"
	"memhop/internal/core/storage"
	"memhop/internal/hash"
	"memhop/internal/timeutil"
)

// ExtractExistingChains reads all L5 ActionChain slots for the LLM input.
func ExtractExistingChains(engine *storage.StorageEngine) []ChainSummary {
	var chains []ChainSummary
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL5ActionChain {
			return true
		}
		var chain model.ActionChainSlot
		if json.Unmarshal(data, &chain) == nil {
			chains = append(chains, ChainSummary{
				Title:        chain.Title,
				Trigger:      chain.Trigger,
				TriggerCount: chain.TriggerCount,
				Confidence:   chain.Confidence,
			})
		}
		return true
	})
	sortChainsByTriggerCount(chains)
	if len(chains) > 20 {
		chains = chains[:20]
	}
	return chains
}

// ApplyCrystals writes pre-computed crystal definitions as L5 ActionChains.
func ApplyCrystals(
	crystals []CrystalDef,
	engine *storage.StorageEngine,
) ([]string, error) {
	var newIDs []string
	for i := range crystals {
		id, err := writeOneCrystal(&crystals[i], engine)
		if err != nil {
			return newIDs, err
		}
		newIDs = append(newIDs, id)
	}
	return newIDs, nil
}

func writeOneCrystal(c *CrystalDef, engine *storage.StorageEngine) (string, error) {
	nowMs := timeutil.NowMs()
	chainID := hash.HashID(fmt.Sprintf("crystal_%s_%d", c.Condition, nowMs))

	title := "crystal_" + truncateStr(c.Condition, 30)
	chain := model.ActionChainSlot{
		IDHash:     chainID,
		Title:      title,
		Trigger:    c.Condition,
		Status:     model.ChainDraft,
		Confidence: c.Confidence,
		CreatedAt:  nowMs,
		UpdatedAt:  nowMs,
		Version:    1,
	}
	if err := writeActionChain(engine, chainID, &chain); err != nil {
		return "", err
	}

	for i, step := range c.Steps {
		if err := writeActionStep(engine, chainID, i, &step, nowMs); err != nil {
			return "", err
		}
	}
	return hash.FormatHash(chainID), nil
}

func writeActionChain(engine *storage.StorageEngine, id uint64, chain *model.ActionChainSlot) error {
	data, err := json.Marshal(chain)
	if err != nil {
		return core.NewError(core.ErrSerialization, "marshal action chain", err)
	}
	_, err = engine.WriteRecord(storage.RecL5ActionChain, id, data)
	return err
}

func writeActionStep(
	engine *storage.StorageEngine,
	chainID uint64,
	order int,
	step *CrystalStep,
	nowMs int64,
) error {
	stepID := hash.HashID(fmt.Sprintf("step_%d_%d_%d", chainID, order, nowMs))
	s := model.ActionStep{
		IDHash:     stepID,
		ChainID:    chainID,
		StepOrder:  uint16(order),
		Action:     step.Action,
		Parameters: step.Parameters,
		CreatedAt:  nowMs,
	}
	data, err := json.Marshal(s)
	if err != nil {
		return core.NewError(core.ErrSerialization, "marshal action step", err)
	}
	_, err = engine.WriteRecord(storage.RecL5ActionStep, stepID, data)
	return err
}

// PruneLowQualityCrystals removes chains with low confidence and low trigger count.
func PruneLowQualityCrystals(engine *storage.StorageEngine) ([]string, error) {
	var pruned []string
	var entries []uint64
	engine.IterIndex(func(idHash, _ uint64) bool {
		entries = append(entries, idHash)
		return true
	})

	for _, idHash := range entries {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL5ActionChain {
			continue
		}
		var chain model.ActionChainSlot
		if json.Unmarshal(data, &chain) != nil {
			continue
		}
		if chain.Confidence < 0.3 && chain.TriggerCount < 5 {
			_, _ = engine.DeleteRecord(idHash)
			pruned = append(pruned, hash.FormatHash(idHash))
		}
	}
	return pruned, nil
}

func sortChainsByTriggerCount(chains []ChainSummary) {
	for i := 1; i < len(chains); i++ {
		for j := i; j > 0 && chains[j].TriggerCount > chains[j-1].TriggerCount; j-- {
			chains[j], chains[j-1] = chains[j-1], chains[j]
		}
	}
}

func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return string(runes)
	}
	return string(runes[:maxLen])
}
