// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// compressFanout bounds how many scenes consolidate at once. Every scene costs an
// LLM round-trip taken inside the domain lock, and a whole-domain pass fans out over
// every scene the domain has: unbounded, that is a burst of as many concurrent
// requests as there are scenes — the shape that trips an endpoint's rate limit and
// then has every scene report a failed call.
const compressFanout = 4

// CompressScenes consolidates every scene named, at most compressFanout at a time:
// reads depth-1 topics, asks the LLM for merge groups and applies them. It returns
// the scenes that had at least one group applied, and how many scenes produced
// nothing usable — a call that failed, or a group this engine could not apply, both
// of which are the model's answer rather than a fault in the records. An engine
// refusal comes back as the error instead of joining that count: it carries its own
// code, and folding it in is what let a failed write be reported to the host as a
// model that never answered. The topics the applied groups sank accumulate into
// rep.L2TopicsCompressed under mu. All scenes belong to ac's domain; cross-agent
// merging is structurally impossible.
func CompressScenes(ctx context.Context, ac *domain.Context, scenes []uint64, rep *core.DreamReport) (map[uint64]struct{}, int, error) {
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded = make(map[uint64]struct{})
		unusable  int
		failed    error
		sem       = make(chan struct{}, compressFanout)
	)
	for _, sid := range scenes {
		sem <- struct{}{}
		wg.Add(1)
		go func(sceneID uint64) {
			defer wg.Done()
			defer func() { <-sem }()
			topics := repo.ListTopicsL2(repo.TopicListQuery{
				MetaIdx: ac.L2Meta,
				SceneID: sceneID,
				Depth:   1,
			})
			// Skip below the compress threshold: few topics keep raw detail.
			if len(topics) < ac.Defaults.DreamCompressMinTopics {
				return
			}
			out, err := llmops.Consolidate(ctx, ac.LLM, topics, ac.Defaults.DreamCompressMinTopics)
			if err != nil {
				mu.Lock()
				unusable++
				mu.Unlock()
				return
			}
			got, err := applyGroups(ctx, ac, sceneID, topics, out)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if failed == nil {
					failed = err
				}
				return
			}
			unusable += got.unusable
			if got.unusable > 0 {
				slog.Warn("dream: merge groups proposed but not applied",
					"scene", common.FormatHash(sceneID), "applied", got.groups, "rejected", got.unusable)
			}
			if got.groups > 0 {
				succeeded[sceneID] = struct{}{}
				rep.L2TopicsCompressed += got.topics
			}
		}(sid)
	}
	wg.Wait()
	return succeeded, unusable, failed
}

// groupOutcome is one scene's consolidation result: the groups that landed, the
// topics those groups sank under them, and the proposals that produced nothing.
type groupOutcome struct {
	groups   uint32
	topics   int
	unusable int
}

// applyGroups applies one scene's groups: store MergedSummary as an L4
// archive of the fused topic, extract keywords for that topic, create it, then
// sink the group nodes. A proposal that produces nothing — one name, a member
// another group already claimed, bounds this engine cannot resolve, a summary the
// model left empty — counts as unusable and the rest continue, so a pass that
// applied nothing because every proposed group was unusable does not look like a
// scene with nothing to consolidate. A refusal that came out of the records is
// returned instead: applyOneGroup has already rolled that group back, and the
// caller reports it under the code it arrived with.
func applyGroups(ctx context.Context, ac *domain.Context, sceneID uint64, topics []core.TopicSlot, out *llmops.ConsolidationOutput) (groupOutcome, error) {
	byID := make(map[uint64]core.TopicSlot, len(topics))
	for _, t := range topics {
		byID[t.ID] = t
	}
	// Members a group has already sunk. Two groups claiming one topic cannot both
	// be applied: the second re-parents it under itself, which leaves the first
	// group's summary claiming a child that no longer answers to it and pushes the
	// topic one level below the deepest read that reaches it.
	claimed := make(map[uint64]struct{}, len(topics))
	var got groupOutcome
	for _, g := range out.L2Groups {
		if len(g.NodeHashes) < 2 {
			// A one-name group is a proposal this engine cannot apply: fusing buys a
			// parent over children, and there is nothing to put under it. It counts as
			// proposed-but-not-applied, not as nothing to consolidate.
			got.unusable++
			continue
		}
		if sharesMember(g.NodeHashes, claimed) {
			got.unusable++
			continue
		}
		minTS, maxTS, ok := groupTimestamps(g.NodeHashes, byID)
		if !ok {
			got.unusable++
			continue
		}
		if err := applyOneGroup(ctx, ac, sceneID, g, minTS, maxTS); err != nil {
			if common.CodeOf(err) != common.ErrLLM {
				return got, err
			}
			got.unusable++
			continue
		}
		for _, id := range g.NodeHashes {
			claimed[id] = struct{}{}
		}
		got.groups++
		got.topics += len(g.NodeHashes)
	}
	return got, nil
}

// sharesMember reports whether any of a proposed group's members already belongs
// to a group this scene applied.
func sharesMember(nodeHashes []uint64, claimed map[uint64]struct{}) bool {
	for _, id := range nodeHashes {
		if _, ok := claimed[id]; ok {
			return true
		}
	}
	return false
}

// applyOneGroup consolidates a single merge group: stores MergedSummary as the
// fused topic's own content slot, extracts keywords for that topic, creates it,
// then sinks the group nodes. Any step that cannot be applied rolls back what
// this group already wrote and returns the reason, so a group is either fully
// applied or leaves nothing behind. The one way it leaves a trace is a rollback
// that itself fails, and that is what its WARN exists to say out loud.
func applyOneGroup(ctx context.Context, ac *domain.Context, sceneID uint64, g llmops.L2Group, minTS, maxTS int64) error {
	parentID := core.ComputeTopicID(sceneID, minTS, maxTS)
	// An empty summary is not a group the engine can fuse: it would sink the
	// children under a parent carrying nothing. Refused here, ahead of the first
	// record this group would own, so a rejected proposal leaves nothing to undo.
	if strings.TrimSpace(g.MergedSummary) == "" {
		return common.NewError(common.ErrLLM, "dream: merge group proposed an empty merged_summary", nil)
	}
	// The parent id is the group's timestamp bounds, so two disjoint groups whose
	// members share those bounds hash to the same one — a host that stamps a batch
	// of turns with one timestamp makes that likely. Landing the second would
	// re-scope a parent over a different set of children: the summary then
	// describes one group while the other's originals hide under it.
	switch stored, err := core.ReadTopicLenient(ac.Engine, ac.ID, parentID); {
	case err != nil && common.CodeOf(err) != common.ErrNotFound:
		return common.NewError(common.ErrIO, "dream: read the parent id this group would create", err)
	case err != nil:
		// Nothing stored: the ordinary case, this group creates the parent.
	case stored == nil:
		return common.NewError(common.ErrIO, "dream: the parent id this group would create already names a record that is not a topic", nil)
	default:
		return common.NewError(common.ErrLLM, "dream: merge group's bounds collide with an existing topic", nil)
	}
	// The fused group's summary is the parent topic's own utterance: it occupies
	// the slot a turn's user side would, and no reference list points at it. It ages
	// from this pass and not from the group's last turn: the retention window sweeps
	// on CreatedAt, so consolidating turns already past the cutoff would hand the
	// summary a birthday older than the window and the next pass would delete the one
	// text consolidation produced. The group's own span stays on the topic record,
	// which is where a reader looks for it.
	if err := repo.AppendArchiveL4(ac.Engine, ac.ID, ac.L4, &core.ArchiveSlot{
		TopicID: parentID, Seq: core.SeqUser, Kind: core.KindUtterance,
		Role: core.RoleDream, ContentType: core.ContentText, Content: g.MergedSummary,
		CreatedAt: time.Now().UnixMilli(),
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

	if err := repo.CreateFusedTopicL2(ac.Engine, ac.ID, sceneID, keywords, minTS, maxTS); err != nil {
		discardFusedGroup(ac, parentID)
		return common.NewError(common.CodeOf(err), "dream: create fused topic", err)
	}
	if err := repo.CompressTopicsL2(ac.Engine, ac.ID, g.NodeHashes, parentID); err != nil {
		// The sink is one batch, and a batch that fails partway leaves the members it
		// reached at depth 2 under a parent this call is about to erase. Restoring them
		// is what makes the rollback complete: depth-1 listing is what both `Search`
		// and the next Dream's group picker read, so an unrestored member is a turn
		// that stops being findable without ever becoming part of a summary.
		if rerr := repo.RestoreSunkTopicsL2(ac.Engine, ac.ID, g.NodeHashes, parentID); rerr != nil {
			slog.Warn("dream: rollback sunk topics failed",
				"parent", common.FormatHash(parentID), "err", rerr)
		}
		discardFusedGroup(ac, parentID)
		return common.NewError(common.ErrIO, "dream: compress child topics", err)
	}
	return nil
}

// discardFusedGroup rolls back a partially applied merge group: no orphan
// summary content and no fused parent sitting above children that were never
// sunk. Undo is keyed by the one id this group wrote, so it needs nothing else in
// the domain — and it must not: a rollback that scanned the topic bucket could be
// refused by the very record whose write failed, leaving behind exactly the
// half-applied group it exists to erase. Rollback failures only warn — the children
// stay at depth 1, so the next Dream re-picks the group.
func discardFusedGroup(ac *domain.Context, parentID uint64) {
	if err := repo.DeleteL2Records(ac.Engine, ac.ID, []uint64{parentID}); err != nil {
		slog.Warn("dream: rollback fused topic failed", "parent", common.FormatHash(parentID), "err", err)
	}
	if err := repo.DeleteTopicArchives(ac.Engine, ac.ID, ac.L4, []uint64{parentID}); err != nil {
		slog.Warn("dream: rollback summary content failed", "parent", common.FormatHash(parentID), "err", err)
	}
}

// groupTimestamps returns the bounds a fused parent is keyed by. It refuses a group
// whose members the engine cannot all see: the listing handed to the model is the
// only source of these ids, so an id it invented or one that has since gone would
// otherwise buy a summary over turns nobody counted — and a two-name group with one
// resolvable member is a parent standing over a single child.
func groupTimestamps(nodeHashes []uint64, byID map[uint64]core.TopicSlot) (minTS, maxTS int64, ok bool) {
	for _, id := range nodeHashes {
		t, found := byID[id]
		if !found {
			return 0, 0, false
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
