// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"context"
	"log/slog"
	"sync"

	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// CompressScenes runs one goroutine per scene: reads depth-1 topics, asks the
// LLM for merge groups and applies them; returns the set of scenes that had at
// least one group applied and the LLM failure count. Applied groups
// accumulate into rep.L2TopicsCompressed under mu. All scenes belong to ac's
// domain; cross-agent merging is structurally impossible.
func CompressScenes(ctx context.Context, ac *domain.Context, scenes []uint64, rep *core.DreamReport) (map[uint64]struct{}, int) {
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded = make(map[uint64]struct{})
		failures  int
	)
	for _, sid := range scenes {
		wg.Add(1)
		go func(sceneID uint64) {
			defer wg.Done()
			topics, err := repo.ListTopicsL2(repo.TopicListQuery{
				Engine:  ac.Engine,
				AgentID: ac.ID,
				MetaIdx: ac.L2Meta,
				SceneID: sceneID,
				Depth:   1,
				Num:     2,
			})
			if err != nil {
				return
			}
			// Skip below the compress threshold: few topics keep raw detail.
			if len(topics) < ac.Defaults.DreamCompressMinTopics {
				return
			}
			out, err := llmops.Consolidate(ctx, ac.LLM, topics)
			if err != nil {
				mu.Lock()
				failures++
				mu.Unlock()
				return
			}
			if applied := applyGroups(ctx, ac, sceneID, topics, out); applied > 0 {
				mu.Lock()
				succeeded[sceneID] = struct{}{}
				rep.L2TopicsCompressed += int(applied)
				mu.Unlock()
			}
		}(sid)
	}
	wg.Wait()
	return succeeded, failures
}

// applyGroups applies one scene's groups: store MergedSummary as an L4
// dream archive, extract keywords for the fused topic, create the parent
// topic with the archive ref, then sink the group nodes.
func applyGroups(ctx context.Context, ac *domain.Context, sceneID uint64, topics []core.TopicSlot, out *llmops.ConsolidationOutput) uint32 {
	byID := make(map[uint64]core.TopicSlot, len(topics))
	for _, t := range topics {
		byID[t.ID] = t
	}
	var count uint32
	for _, g := range out.L2Groups {
		if len(g.NodeHashes) < 2 {
			continue
		}
		minTS, maxTS, ok := groupTimestamps(g.NodeHashes, byID)
		if !ok {
			continue
		}
		if applyOneGroup(ctx, ac, sceneID, g, minTS, maxTS) {
			count++
		}
	}
	return count
}

// applyOneGroup consolidates a single merge group: stores MergedSummary as
// an L4 dream archive, extracts keywords for the fused topic, creates the
// parent topic with the archive ref, then sinks the group nodes. A failed step
// rolls back what this group already wrote and reports false, so a group is
// either fully applied or leaves nothing behind.
func applyOneGroup(ctx context.Context, ac *domain.Context, sceneID uint64, g llmops.L2Group, minTS, maxTS int64) bool {
	parentID := core.ComputeTopicID(sceneID, minTS, maxTS)
	archiveID, err := repo.AppendArchiveL4(ac.Engine, ac.ID, parentID, core.RoleDream, core.ContentText, g.MergedSummary, maxTS)
	if err != nil {
		slog.Warn("dream: archive merged summary failed", "parent", common.FormatHash(parentID), "err", err)
		return false
	}

	// Keywords of MergedSummary become the fused topic's single track.
	keywords, err := llmops.ExtractKeywords(ctx, ac.LLM, g.MergedSummary)
	if err != nil || len(keywords) == 0 {
		slog.Warn("dream: extract keywords from merged summary failed, skip group", "parent", common.FormatHash(parentID), "err", err)
		discardFusedGroup(ac, parentID, archiveID)
		return false
	}

	if !repo.CreateFusedTopicL2(ac.Engine, ac.ID, sceneID, keywords, minTS, maxTS, g.NodeHashes) {
		slog.Warn("dream: create fused topic failed", "parent", common.FormatHash(parentID))
		discardFusedGroup(ac, parentID, archiveID)
		return false
	}
	// Attach the summary archive ref so the host can pull the fused text back.
	if !repo.UpdateTopicL4RefsL2(ac.Engine, ac.ID, parentID, []uint64{archiveID}) {
		slog.Warn("dream: attach summary archive ref failed", "parent", common.FormatHash(parentID))
		discardFusedGroup(ac, parentID, archiveID)
		return false
	}
	if _, err := repo.CompressTopicsL2(ac.Engine, ac.ID, g.NodeHashes, parentID); err != nil {
		slog.Warn("dream: compress child topics failed", "parent", common.FormatHash(parentID), "err", err)
		discardFusedGroup(ac, parentID, archiveID)
		return false
	}
	return true
}

// discardFusedGroup rolls back a partially applied merge group: no orphan
// summary archive and no fused parent sitting above children that were never
// sunk. Rollback failures only warn — the children stay at depth 1, so the next
// Dream re-picks the group.
func discardFusedGroup(ac *domain.Context, parentID, archiveID uint64) {
	if !repo.DeleteL2(ac.Engine, ac.ID, []uint64{parentID}, repo.DeleteTopicsL2) {
		slog.Warn("dream: rollback fused topic failed", "parent", common.FormatHash(parentID))
	}
	if err := repo.DeleteArchivesL4(ac.Engine, ac.ID, []uint64{archiveID}); err != nil {
		slog.Warn("dream: rollback summary archive failed", "parent", common.FormatHash(parentID), "err", err)
	}
}

func groupTimestamps(nodeHashes []uint64, byID map[uint64]core.TopicSlot) (minTS, maxTS int64, ok bool) {
	for _, id := range nodeHashes {
		t, found := byID[id]
		if !found {
			continue
		}
		if !ok || t.UserTimestamp < minTS {
			minTS = t.UserTimestamp
		}
		if !ok || t.AgentTimestamp > maxTS {
			maxTS = t.AgentTimestamp
		}
		ok = true
	}
	return minTS, maxTS, ok
}
