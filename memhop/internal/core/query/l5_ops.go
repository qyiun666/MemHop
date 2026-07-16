// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 ActionChain CRUD operations.

package query

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/qyiun666/memhop/memhop/internal/hash"
	"github.com/qyiun666/memhop/memhop/internal/timeutil"
	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
)

// GetL5 loads an L5 action chain by hex ID.
func GetL5(engine *storage.StorageEngine, id string) (*model.ActionChainSlot, error) {
	idHash, err := hash.ParseID(id)
	if err != nil {
		return nil, core.NewError(core.ErrInvalidQuery, "parse l5 id", err)
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
		return core.NewError(core.ErrInvalidQuery, "parse l5 id", err)
	}
	chain, err := loadActionChain(engine, idHash)
	if err != nil {
		return err
	}
	applyL5Updates(chain, fields)
	chain.UpdatedAt = timeutil.NowMs()
	chain.Version++
	return writeActionChain(engine, idHash, chain)
}

// DeleteL5 deletes an L5 action chain and all its steps.
func DeleteL5(engine *storage.StorageEngine, id string) error {
	idHash, err := hash.ParseID(id)
	if err != nil {
		return core.NewError(core.ErrInvalidQuery, "parse l5 id", err)
	}
	engine.DeleteRecord(idHash)
	deleteActionSteps(engine, idHash)
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
		crystals = append(crystals, toCrystalSummary(&all[i]))
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
		return nil, core.ErrNotFound
	}
	var chain model.ActionChainSlot
	if err := json.Unmarshal(data, &chain); err != nil {
		return nil, core.NewError(core.ErrDeserialization, "unmarshal action chain", err)
	}
	return &chain, nil
}

func writeActionChain(
	engine *storage.StorageEngine,
	idHash uint64,
	chain *model.ActionChainSlot,
) error {
	data, err := json.Marshal(chain)
	if err != nil {
		return core.NewError(core.ErrSerialization, "marshal action chain", err)
	}
	_, err = engine.WriteRecord(storage.RecL5ActionChain, idHash, data)
	return err
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

func deleteActionSteps(engine *storage.StorageEngine, chainID uint64) {
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
		engine.DeleteRecord(h)
	}
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

func toCrystalSummary(c *model.ActionChainSlot) CrystalSummary {
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
