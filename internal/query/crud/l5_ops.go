// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 ActionChain CRUD operations.

package crud

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/common/timeutil"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// GetL5 loads an L5 action chain by hex ID.
func GetL5(engine *storage.StorageEngine, id string) (*model.ActionChainSlot, error) {
	idHash, err := hash.ParseID(id)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "parse l5 id", err)
	}
	return loadActionChain(engine, idHash)
}

// UpdateL5 partially updates an L5 action chain.
func UpdateL5(
	engine *storage.StorageEngine,
	id string,
	fields UpdateL5Fields,
) error {
	idHash, err := hash.ParseID(id)
	if err != nil {
		return mherrors.NewError(mherrors.ErrInvalidQuery, "parse l5 id", err)
	}
	chain, err := loadActionChain(engine, idHash)
	if err != nil {
		return err
	}
	applyL5Updates(chain, fields)
	chain.UpdatedAt = timeutil.NowMs()
	return writeActionChain(engine, idHash, chain)
}

// DeleteL5 deletes an L5 action chain and all its steps.
func DeleteL5(engine *storage.StorageEngine, id string) error {
	idHash, err := hash.ParseID(id)
	if err != nil {
		return mherrors.NewError(mherrors.ErrInvalidQuery, "parse l5 id", err)
	}
	// Steps first, then the chain record: a failure mid-cascade leaves the
	// chain discoverable so the delete can be retried.
	if err := deleteActionSteps(engine, idHash); err != nil {
		return fmt.Errorf("delete l5 %s: steps: %w", id, err)
	}
	if _, err := engine.DeleteRecord(idHash); err != nil {
		return fmt.Errorf("delete l5 %s: %w", id, err)
	}
	return nil
}

// ListCrystals lists L5 action chains with pagination.
func ListCrystals(
	engine *storage.StorageEngine,
	q CrystalListQuery,
) (*CrystalListResult, error) {
	all := collectAllChains(engine)
	filterChains(&all, q.StatusFilter, q.MinTriggerCount, q.Keyword)
	sortChainsByUpdated(all)
	skip, take := paginationParams(int(q.Page), int(q.PageSize))
	total := len(all)
	crystals := make([]CrystalSummary, 0, take)
	for i := skip; i < skip+take && i < total; i++ {
		crystals = append(crystals, ToCrystalSummary(&all[i]))
	}
	return &CrystalListResult{
		Crystals: crystals,
		Total:    uint32(total),
		Page:     q.Page,
	}, nil
}

// --- internal helpers ---

func loadActionChain(engine *storage.StorageEngine, idHash uint64) (*model.ActionChainSlot, error) {
	rt, data, err := engine.ReadRecord(idHash)
	if err != nil {
		return nil, err
	}
	if rt != storage.RecL5ActionChain {
		return nil, mherrors.ErrNotFound
	}
	var chain model.ActionChainSlot
	if err := json.Unmarshal(data, &chain); err != nil {
		return nil, mherrors.NewError(mherrors.ErrDeserialization, "unmarshal action chain", err)
	}
	return &chain, nil
}

func writeActionChain(
	engine *storage.StorageEngine,
	idHash uint64,
	chain *model.ActionChainSlot,
) error {
	return record.WriteActionChainSlot(engine, idHash, chain)
}

func applyL5Updates(chain *model.ActionChainSlot, fields UpdateL5Fields) {
	if fields.Title != nil {
		chain.Title = *fields.Title
	}
	if fields.Trigger != nil {
		chain.Trigger = *fields.Trigger
	}
	if fields.Status != nil {
		chain.Status = parseChainStatus(*fields.Status)
	}
	if fields.Confidence != nil {
		chain.Confidence = *fields.Confidence
	}
	if fields.SuccessRate != nil {
		chain.SuccessRate = *fields.SuccessRate
	}
	if fields.TriggerCount != nil {
		chain.TriggerCount = *fields.TriggerCount
	}
	if fields.LastTriggered != nil {
		chain.LastTriggered = *fields.LastTriggered
	}
}

func parseChainStatus(s string) model.ChainStatus {
	switch strings.ToLower(s) {
	case "active":
		return model.ChainActive
	case "deprecated":
		return model.ChainDeprecated
	default:
		return model.ChainDraft
	}
}

func deleteActionSteps(engine *storage.StorageEngine, chainID uint64) error {
	var stepHashes []uint64
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL5ActionStep {
			return true
		}
		var step model.ActionStep
		if json.Unmarshal(data, &step) == nil && step.ChainID == chainID {
			stepHashes = append(stepHashes, idHash)
		}
		return true
	})
	for _, h := range stepHashes {
		if _, err := engine.DeleteRecord(h); err != nil {
			return fmt.Errorf("delete action step %016x: %w", h, err)
		}
	}
	return nil
}

func collectAllChains(engine *storage.StorageEngine) []model.ActionChainSlot {
	var all []model.ActionChainSlot
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL5ActionChain {
			return true
		}
		var chain model.ActionChainSlot
		if json.Unmarshal(data, &chain) == nil {
			all = append(all, chain)
		}
		return true
	})
	return all
}

func filterChains(
	all *[]model.ActionChainSlot,
	statusFilter *string,
	minTrigger *uint32,
	keyword *string,
) {
	filtered := make([]model.ActionChainSlot, 0, len(*all))
	for _, c := range *all {
		if statusFilter != nil && c.Status.String() != *statusFilter {
			continue
		}
		if minTrigger != nil && c.TriggerCount < *minTrigger {
			continue
		}
		if keyword != nil {
			kw := strings.ToLower(*keyword)
			if !strings.Contains(strings.ToLower(c.Title), kw) {
				continue
			}
		}
		filtered = append(filtered, c)
	}
	*all = filtered
}

func sortChainsByUpdated(all []model.ActionChainSlot) {
	sort.Slice(all, func(i, j int) bool {
		return all[i].UpdatedAt > all[j].UpdatedAt
	})
}

func ToCrystalSummary(c *model.ActionChainSlot) CrystalSummary {
	var lastTriggered *int64
	if c.LastTriggered > 0 {
		lastTriggered = &c.LastTriggered
	}
	return CrystalSummary{
		ID:            hash.FormatHash(c.IDHash),
		Title:         c.Title,
		Condition:     c.Trigger,
		Status:        c.Status.String(),
		TriggerCount:  c.TriggerCount,
		SuccessRate:   c.SuccessRate,
		LastTriggered: lastTriggered,
		CreatedAt:     c.CreatedAt,
	}
}

// CreateL5Chain creates a new L5 action chain with optional steps.
func CreateL5Chain(engine *storage.StorageEngine, input L5ChainInput) (string, error) {
	nowMs := timeutil.NowMs()
	chainID := hash.HashID(fmt.Sprintf("crystal_%s_%d", input.Trigger, nowMs))

	chain := model.ActionChainSlot{
		IDHash:     chainID,
		Title:      input.Title,
		Trigger:    input.Trigger,
		Status:     model.ChainDraft,
		Confidence: 1.0,
		CreatedAt:  nowMs,
		UpdatedAt:  nowMs,
	}
	if err := writeActionChain(engine, chainID, &chain); err != nil {
		return "", err
	}

	for i, step := range input.Steps {
		stepID := hash.HashID(fmt.Sprintf("step_%d_%d_%d", chainID, i, nowMs))
		s := model.ActionStep{
			IDHash:     stepID,
			ChainID:    chainID,
			StepOrder:  uint16(i),
			Action:     step.Action,
			Parameters: step.Parameters,
			CreatedAt:  nowMs,
		}
		if err := record.WriteActionStep(engine, stepID, &s); err != nil {
			return "", err
		}
	}

	return hash.FormatHash(chainID), nil
}

// AppendL5Step appends a new step to an existing L5 action chain.
func AppendL5Step(engine *storage.StorageEngine, chainID uint64, step L5StepInput) (string, error) {
	nowMs := timeutil.NowMs()

	// Find the max existing step order
	maxOrder := uint16(0)
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL5ActionStep {
			return true
		}
		var s model.ActionStep
		if json.Unmarshal(data, &s) == nil && s.ChainID == chainID {
			if s.StepOrder >= maxOrder {
				maxOrder = s.StepOrder + 1
			}
		}
		return true
	})

	stepID := hash.HashID(fmt.Sprintf("step_%d_%d_%d", chainID, maxOrder, nowMs))
	s := model.ActionStep{
		IDHash:     stepID,
		ChainID:    chainID,
		StepOrder:  maxOrder,
		Action:     step.Action,
		Parameters: step.Parameters,
		CreatedAt:  nowMs,
	}
	if err := record.WriteActionStep(engine, stepID, &s); err != nil {
		return "", err
	}
	return hash.FormatHash(stepID), nil
}

// IncrL5Trigger increments the trigger count and updates last triggered time.
func IncrL5Trigger(engine *storage.StorageEngine, chainID uint64) error {
	chain, err := loadActionChain(engine, chainID)
	if err != nil {
		return err
	}
	chain.TriggerCount++
	chain.LastTriggered = timeutil.NowMs()
	chain.UpdatedAt = timeutil.NowMs()
	return writeActionChain(engine, chainID, chain)
}

// UpdateL5Confidence applies EMA confidence update based on success/failure.
func UpdateL5Confidence(engine *storage.StorageEngine, chainID uint64, success bool) error {
	chain, err := loadActionChain(engine, chainID)
	if err != nil {
		return err
	}
	score := float32(0)
	if success {
		score = 1.0
	}
	chain.Confidence = 0.9*chain.Confidence + 0.1*score
	chain.SuccessRate = 0.9*chain.SuccessRate + 0.1*score
	chain.UpdatedAt = timeutil.NowMs()
	return writeActionChain(engine, chainID, chain)
}

// BatchDeleteL5 deletes multiple L5 action chains.
func BatchDeleteL5(engine *storage.StorageEngine, ids []string) error {
	for _, id := range ids {
		if err := DeleteL5(engine, id); err != nil {
			return err
		}
	}
	return nil
}

// BatchUpdateL5 applies field updates to multiple L5 chains.
func BatchUpdateL5(engine *storage.StorageEngine, updates []L5ChainUpdate) error {
	for _, u := range updates {
		if err := UpdateL5(engine, u.ID, u.Fields); err != nil {
			return err
		}
	}
	return nil
}
