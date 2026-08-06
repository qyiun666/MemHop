// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 Archive query operations.

package crud

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/repo/core/index"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
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
		items = append(items, ToArchiveDTO(&all[i]))
	}
	return &ArchiveListResult{
		Items:    items,
		Total:    total,
		Page:     q.Page,
		PageSize: q.PageSize,
		HasMore:  skip+take < total,
	}, nil
}

// GetArchive loads a single archive by hex ID.
func GetArchive(engine *storage.StorageEngine, id string) (*Archive, error) {
	idHash, err := hash.ParseID(id)
	if err != nil {
		return nil, err
	}
	rt, data, err := engine.ReadRecord(idHash)
	if err != nil {
		return nil, err
	}
	if rt != storage.RecL4Archive {
		return nil, mherrors.NewError(mherrors.ErrNotFound, "archive type mismatch")
	}
	var slot model.ArchiveSlot
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, mherrors.NewError(mherrors.ErrDeserialization, "unmarshal archive", err)
	}
	arc := ToArchiveDTO(&slot)
	return &arc, nil
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

func ToArchiveDTO(arc *model.ArchiveSlot) Archive {
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
// timestamp is the message timestamp in milliseconds; it replaces the internally-generated time.
func AppendDialogueL4(
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
	topicID uint64,
	text string,
	role uint8,
	keywords []string,
	timestamp int64,
) (uint64, error) {
	if text == "" {
		return 0, mherrors.NewError(mherrors.ErrInvalidQuery, "cannot append empty dialogue as L4", nil)
	}

	nowMs := timestamp

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
		return 0, mherrors.NewError(mherrors.ErrSerialization, "marshal archive", err)
	}
	if _, err := engine.WriteRecord(storage.RecL4Archive, archiveIDHash, archiveData); err != nil {
		return 0, err
	}

	// Load and update the L2 topic
	topic, err := record.ReadTopicSlot(engine, topicID)
	if err != nil {
		return 0, mherrors.NewError(mherrors.ErrNotFound, "read topic for L4 append", err)
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

	if err := record.WriteTopicSlot(engine, topicID, topic); err != nil {
		return 0, err
	}

	// Rebuild the topic's BM25 document so merged keywords are searchable.
	if sparseIdx != nil {
		ReindexTopic(sparseIdx, topic)
	}

	return archiveIDHash, nil
}
