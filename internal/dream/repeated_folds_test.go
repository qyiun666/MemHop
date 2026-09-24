// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Consolidation folds what the surface listing offers, and the group it builds is itself a
// surface row — so a later pass can fold the fold. Two such passes are already pinned; this one
// asks where that chain ends, because the answer is a memory claim: a sunk row is rewritten one
// level deeper, and `CompressTopicsL2` **deletes** a member that would reach `MaxDepth`. A turn
// deleted there is not "buried" — the scene read flattens to depth 2, and that listing is the
// only way a host learns a topic id exists, so the row's summary, its keyword track and the
// originals addressed by its id all stop being reachable.
//
// So this folds until the surface can no longer offer a pair (six passes at most) and requires
// that folding terminates by collapsing to one surface row rather than by deepening: no written
// turn disappears, no row sits below the read's cap, and every parent a row names is a row the
// read lists.

package dream

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/config"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

func TestRepeatedFoldingCollapsesInsteadOfDeleting(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "many_folds.meh"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	const sceneID = uint64(7)
	turnIDs := []uint64{91, 92, 93, 94, 95, 96}
	base := time.Now().Add(-9 * time.Minute)
	for i, id := range turnIDs {
		at := base.Add(time.Duration(i) * time.Minute)
		topic := core.TopicSlot{
			ID: id, SceneID: sceneID, Depth: 1, FusedKeywords: []string{"登录"},
			UserTimestamp: at.UnixMilli(), AgentTimestamp: at.UnixMilli(),
		}
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, id, &topic); err != nil {
			t.Fatalf("write turn %d: %v", id, err)
		}
	}
	ac := domain.NewContext(core.DefaultAgentID, context.Background(), engine, anyKeywords{}, &config.MemHopDefaults{})
	surface := func() []core.TopicSlot {
		ac.L2Meta = index.BuildL2MetaFromEngine(engine, core.DefaultAgentID)
		return repo.ListTopicsL2(repo.TopicListQuery{MetaIdx: ac.L2Meta, SceneID: sceneID, Depth: 1})
	}

	// Always merge the two oldest surface rows: that is the shape that keeps re-folding the group
	// from the previous pass, which is the only way a chain deepens.
	folds := 0
	for pass := 0; pass < 6; pass++ {
		rows := surface()
		if len(rows) < 2 {
			break
		}
		members := []core.TopicSlot{rows[0], rows[1]}
		if _, err := applyGroups(context.Background(), ac, sceneID, members, &llmops.ConsolidationOutput{
			L2Groups: []llmops.L2Group{{
				NodeHashes: []uint64{members[0].ID, members[1].ID}, MergedSummary: "组"}}}); err != nil {
			t.Fatalf("fold %d: %v", folds+1, err)
		}
		folds++
	}
	if folds < 3 {
		t.Fatalf("only %d folds ran, so the chain never re-folded a group and this proved nothing", folds)
	}

	all := repo.ListTopicsL2(repo.TopicListQuery{
		MetaIdx: index.BuildL2MetaFromEngine(engine, core.DefaultAgentID), SceneID: sceneID, Depth: 2})
	byID := make(map[uint64]core.TopicSlot, len(all))
	surfaced := 0
	for _, row := range all {
		byID[row.ID] = row
		if row.Depth == 1 {
			surfaced++
		}
		if row.Depth > 2 {
			t.Fatalf("fold %d pushed topic %d to depth %d, past the scene read's own cap (%s)",
				folds, row.ID, row.Depth, summarize(all))
		}
	}
	// Every turn the host settled must still be there: a fold that ate one would show up here as a
	// shorter listing, and in the host's world as a round it can no longer name.
	for _, id := range turnIDs {
		if _, ok := byID[id]; !ok {
			t.Fatalf("after %d folds turn %d is gone from the scene read — consolidation deleted a "+
				"member instead of sinking it (%s)", folds, id, summarize(all))
		}
	}
	if surfaced != 1 {
		t.Fatalf("after %d folds %d rows are still on the surface (%s): the chain was supposed to "+
			"collapse into one group, not keep growing sideways", folds, surfaced, summarize(all))
	}
	for _, row := range all {
		hops, parent := 0, row.ParentID
		for parent != nil {
			if hops++; hops > 8 {
				t.Fatalf("topic %d sits in a parent cycle: %s", row.ID, summarize(all))
			}
			next, ok := byID[*parent]
			if !ok {
				t.Fatalf("topic %d names parent %d, which the scene read no longer lists — the row above "+
					"it was deleted, so the host cannot reach what it summarises: %s",
					row.ID, *parent, summarize(all))
			}
			parent = next.ParentID
		}
	}
	t.Logf("after %d folds: %s", folds, summarize(all))
}

func summarize(rows []core.TopicSlot) string {
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parent := "no-parent"
		if r.ParentID != nil {
			parent = fmt.Sprintf("parent=%d", *r.ParentID)
		}
		parts = append(parts, fmt.Sprintf("%d(depth=%d,%s)", r.ID, r.Depth, parent))
	}
	return strings.Join(parts, " ")
}
