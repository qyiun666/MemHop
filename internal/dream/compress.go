// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"context"
	"log/slog"
	"strings"
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
	countFailure := func() {
		mu.Lock()
		failures++
		mu.Unlock()
	}
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
				countFailure()
				slog.Warn("dream: read scene topics failed", "scene", common.FormatHash(sceneID), "err", err)
				return
			}
			// Skip below the compress threshold: few topics keep raw detail.
			if len(topics) < ac.Defaults.DreamCompressMinTopics {
				return
			}
			out, err := llmops.Consolidate(ctx, ac.LLM, topics)
			if err != nil {
				countFailure()
				return
			}
			applied, rejected := applyGroups(ctx, ac, sceneID, topics, out)
			for i := 0; i < rejected; i++ {
				countFailure()
			}
			if rejected > 0 {
				slog.Warn("dream: merge groups proposed but not applied",
					"scene", common.FormatHash(sceneID), "applied", applied, "rejected", rejected)
			}
			if applied > 0 {
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
// archive of the fused topic, extract keywords for that topic, create it, then
// sink the group nodes. It reports how many groups landed and how many the
// model proposed but the engine could not apply — the two are different facts,
// and a pass that applied nothing because every proposed group was unusable
// must not look like a scene with nothing to consolidate.
func applyGroups(ctx context.Context, ac *domain.Context, sceneID uint64, topics []core.TopicSlot, out *llmops.ConsolidationOutput) (uint32, int) {
	byID := make(map[uint64]core.TopicSlot, len(topics))
	for _, t := range topics {
		byID[t.ID] = t
	}
	var count uint32
	var rejected int
	for _, g := range out.L2Groups {
		if len(g.NodeHashes) < 2 {
			continue
		}
		minTS, maxTS, ok := groupTimestamps(g.NodeHashes, byID)
		if !ok {
			rejected++
			continue
		}
		if err := applyOneGroup(ctx, ac, sceneID, g, minTS, maxTS); err != nil {
			rejected++
			continue
		}
		count++
	}
	return count, rejected
}

// applyOneGroup consolidates a single merge group: stores MergedSummary as the
// fused topic's own content slot, extracts keywords for that topic, creates it,
// then sinks the group nodes. Any step that cannot be applied rolls back what
// this group already wrote and returns the reason, so a group is either fully
// applied or leaves nothing behind.
func applyOneGroup(ctx context.Context, ac *domain.Context, sceneID uint64, g llmops.L2Group, minTS, maxTS int64) error {
	parentID := core.ComputeTopicID(sceneID, minTS, maxTS)
	// An empty summary is not a group the engine can fuse: it would sink the
	// children under a parent carrying nothing. Refused here, ahead of the first
	// record this group would own, so a rejected proposal leaves nothing to undo.
	if strings.TrimSpace(g.MergedSummary) == "" {
		return common.NewError(common.ErrLLM, "dream: merge group proposed an empty merged_summary", nil)
	}
	// The fused group's summary is the parent topic's own utterance: it occupies
	// the slot a turn's user side would, and no reference list points at it.
	if err := repo.AppendArchiveL4(ac.Engine, ac.ID, ac.L4, &core.ArchiveSlot{
		TopicID: parentID, Seq: core.SeqUser, Kind: core.KindUtterance,
		Role: core.RoleDream, ContentType: core.ContentText, Content: g.MergedSummary, CreatedAt: maxTS,
	}); err != nil {
		return common.NewError(common.ErrIO, "dream: archive merged summary", err)
	}

	// Keywords of MergedSummary become the fused topic's single track.
	keywords, err := llmops.ExtractKeywords(ctx, ac.LLM, g.MergedSummary)
	if err != nil || len(keywords) == 0 {
		discardFusedGroup(ac, parentID)
		if err == nil {
			err = common.NewError(common.ErrLLM, "extracted no keywords", nil)
		}
		return common.NewError(common.ErrLLM, "dream: extract keywords from merged summary", err)
	}

	if !repo.CreateFusedTopicL2(ac.Engine, ac.ID, sceneID, keywords, minTS, maxTS, g.NodeHashes) {
		discardFusedGroup(ac, parentID)
		return common.NewError(common.ErrIO, "dream: create fused topic", nil)
	}
	if _, err := repo.CompressTopicsL2(ac.Engine, ac.ID, g.NodeHashes, parentID); err != nil {
		discardFusedGroup(ac, parentID)
		return common.NewError(common.ErrIO, "dream: compress child topics", err)
	}
	return nil
}

// discardFusedGroup rolls back a partially applied merge group: no orphan
// summary content and no fused parent sitting above children that were never
// sunk. The fused topic owns exactly the one slot this group wrote, so dropping
// the topic's content by key is the whole rollback — no id has to be carried
// forward to undo a write. Rollback failures only warn — the children stay at
// depth 1, so the next Dream re-picks the group.
func discardFusedGroup(ac *domain.Context, parentID uint64) {
	if !repo.DeleteL2(ac.Engine, ac.ID, []uint64{parentID}, repo.DeleteTopicsL2) {
		slog.Warn("dream: rollback fused topic failed", "parent", common.FormatHash(parentID))
	}
	if err := repo.DeleteTopicArchives(ac.Engine, ac.ID, ac.L4, []uint64{parentID}); err != nil {
		slog.Warn("dream: rollback summary content failed", "parent", common.FormatHash(parentID), "err", err)
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
