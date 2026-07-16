// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 Archive query operations.

package query

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/qyiun666/memhop/memhop/internal/hash"
	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
)

// QueryArchives searches L4 archives with filters.
func QueryArchives(
	engine *storage.StorageEngine,
	q ArchiveQuery,
) (*ArchiveListResult, error) {
	all := collectArchives(engine)
	filterArchivesByTopic(&all, q.TopicID)
	filterArchivesByTimeRange(&all, q.TimeRange)
	filterArchivesByKeyword(&all, q.Keyword)
	sortArchivesByCreated(all)
	skip, take := paginationParams(q.Page, q.PageSize)
	total := len(all)
	items := make([]Archive, 0, take)
	for i := skip; i < skip+take && i < total; i++ {
		items = append(items, toArchiveDTO(&all[i]))
	}
	return &ArchiveListResult{
		Items:    items,
		Total:    total,
		Page:     q.Page,
		PageSize: q.PageSize,
		HasMore:  skip+take < total,
	}, nil
}

// --- internal helpers ---

func collectArchives(engine *storage.StorageEngine) []model.ArchiveSlot {
	var all []model.ArchiveSlot
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL4Archive {
			return true
		}
		var arc model.ArchiveSlot
		if json.Unmarshal(data, &arc) == nil {
			all = append(all, arc)
		}
		return true
	})
	return all
}

func filterArchivesByTopic(all *[]model.ArchiveSlot, topicID *string) {
	if topicID == nil {
		return
	}
	tid, err := hash.ParseID(*topicID)
	if err != nil {
		return
	}
	filtered := make([]model.ArchiveSlot, 0, len(*all))
	for _, arc := range *all {
		if arc.ContextID == tid {
			filtered = append(filtered, arc)
		}
	}
	*all = filtered
}

func filterArchivesByTimeRange(all *[]model.ArchiveSlot, tr *TimeRange) {
	if tr == nil {
		return
	}
	start := tr[0]
	end := tr[1]
	filtered := make([]model.ArchiveSlot, 0, len(*all))
	for _, arc := range *all {
		if arc.CreatedAt < start {
			continue
		}
		if arc.CreatedAt > end {
			continue
		}
		filtered = append(filtered, arc)
	}
	*all = filtered
}

func filterArchivesByKeyword(all *[]model.ArchiveSlot, keyword *string) {
	if keyword == nil {
		return
	}
	kw := strings.ToLower(*keyword)
	filtered := make([]model.ArchiveSlot, 0, len(*all))
	for _, arc := range *all {
		if strings.Contains(strings.ToLower(arc.Content), kw) {
			filtered = append(filtered, arc)
		}
	}
	*all = filtered
}

func sortArchivesByCreated(all []model.ArchiveSlot) {
	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt > all[j].CreatedAt
	})
}

func toArchiveDTO(arc *model.ArchiveSlot) Archive {
	var topicID *string
	if arc.ContextID != 0 {
		s := hash.FormatHash(arc.ContextID)
		topicID = &s
	}
	return Archive{
		ID:          hash.FormatHash(arc.IDHash),
		Content:     arc.Content,
		ContentType: arc.ContentType.String(),
		Role:        arc.Role,
		ContextID:   arc.ContextID,
		TopicID:     topicID,
		EngramIDs:   []string{},
		Metadata:    arc.Metadata,
		CreatedAt:   arc.CreatedAt,
	}
}
