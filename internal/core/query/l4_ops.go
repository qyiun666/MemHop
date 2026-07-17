// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 Archive query operations.

package query

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"memhop/internal/core"
	"memhop/internal/core/index"
	"memhop/internal/core/model"
	"memhop/internal/core/storage"
	"memhop/internal/hash"
	"memhop/internal/timeutil"
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

// AppendDialogueL4 creates an L4 archive and appends it to a topic's L4Refs.
// role: 0=user, 1=agent. Updates UserL4Refs or AgentL4Refs accordingly.
func AppendDialogueL4(
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
	topicID uint64,
	text string,
	role uint8,
	keywords []string,
) (uint64, error) {
	if text == "" {
		return 0, core.NewError(core.ErrInvalidQuery, "cannot append empty dialogue as L4", nil)
	}

	nowMs := timeutil.NowMs()

	// Create L4 archive
	archiveIDStr := fmt.Sprintf("msg_%d_%d", nowMs, topicID)
	archiveIDHash := hash.HashID(archiveIDStr)
	archive := model.ArchiveSlot{
		IDHash:      archiveIDHash,
		ContentType: model.ContentText,
		Role:        role,
		ContextID:   topicID,
		CreatedAt:   nowMs,
		Content:     text,
	}
	archiveData, err := json.Marshal(archive)
	if err != nil {
		return 0, core.NewError(core.ErrSerialization, "marshal archive", err)
	}
	if _, err := engine.WriteRecord(storage.RecL4Archive, archiveIDHash, archiveData); err != nil {
		return 0, err
	}

	// Load and update the L2 topic
	_, topicData, err := engine.ReadRecord(topicID)
	if err != nil {
		return 0, core.NewError(core.ErrNotFound, "read topic for L4 append", err)
	}
	var topic model.TopicSlot
	if err := json.Unmarshal(topicData, &topic); err != nil {
		return 0, core.NewError(core.ErrDeserialization, "unmarshal topic for L4 append", err)
	}

	// Append ref and merge keywords based on role
	if role == 0 {
		topic.UserL4Refs = append(topic.UserL4Refs, archiveIDHash)
		// Merge keywords into user keywords if provided and not already present
		for _, kw := range keywords {
			found := false
			for _, existing := range topic.UserKeywords {
				if existing == kw {
					found = true
					break
				}
			}
			if !found {
				topic.UserKeywords = append(topic.UserKeywords, kw)
			}
		}
		topic.UserTimestamp = nowMs
	} else {
		topic.AgentL4Refs = append(topic.AgentL4Refs, archiveIDHash)
		// Merge keywords into agent keywords
		for _, kw := range keywords {
			found := false
			for _, existing := range topic.AgentKeywords {
				if existing == kw {
					found = true
					break
				}
			}
			if !found {
				topic.AgentKeywords = append(topic.AgentKeywords, kw)
			}
		}
		topic.AgentTimestamp = nowMs
	}

	topic.UpdatedAt = nowMs
	topic.Version++

	topicData2, err := json.Marshal(topic)
	if err != nil {
		return 0, core.NewError(core.ErrSerialization, "marshal topic after L4 append", err)
	}
	if _, err := engine.WriteRecord(storage.RecL2Topic, topicID, topicData2); err != nil {
		return 0, err
	}

	// Rebuild the topic's BM25 document so merged keywords are searchable.
	if sparseIdx != nil {
		reindexTopic(sparseIdx, &topic)
	}

	return archiveIDHash, nil
}
