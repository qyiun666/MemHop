// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"errors"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/llm"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// A Dream cancelled after its compression has landed still has to hand the
// domain a read path that matches the records. Compression sinks the merged
// topics by rewriting their depth, and nothing mirrors that write, so skipping
// the whole-table rebuild left the scene listing turns the pass had already
// swallowed — and never showing the summary that replaced them — until the
// domain's cache was dropped or the file reopened.
func TestCancelledDreamReconcilesTheReadPath(t *testing.T) {
	const (
		sceneID = uint64(7)
		leftID  = uint64(11)
		rightID = uint64(12)
		// The model is asked to consolidate, then once per group for the fused
		// topic's keywords. Cancelling on the third call — the second group's — means
		// the first group has already landed and been reported, which is the state
		// the checkpoint order has to get right. Whether the second group finishes
		// first is not what this test claims.
		groups = `{"l2_groups":[{"node_hashes":[11,12],"merged_summary":"两轮并成一事"},` +
			`{"node_hashes":[13,14],"merged_summary":"另两轮并成一事"}]}`
		keywords = `{"keywords":["登录","刷新"]}`
	)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := cancellingLLMServer(t, cancel, 3, groups, keywords)
	db := newTestDB(t, newTestEngine(t))
	db.llm = llm.New(LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"})
	db.config.Defaults.DreamCompressMinTopics = 4

	mustWriteScene(t, db.engine, core.DefaultAgentID, sceneID, "session:cancel")
	for _, id := range []uint64{11, 12, 13, 14} {
		writeTopic(t, db.engine, core.DefaultAgentID, newTopic(id, sceneID, int64(100*id), []string{"关键词"}))
	}
	ac := testDefaultContext(db)
	for _, id := range []uint64{leftID, rightID} {
		if got := ac.L2Meta.Get(id); got == nil || got.Depth != 1 {
			t.Fatalf("fixture wants both turns at depth 1, got %+v", got)
		}
	}

	rep, err := db.RunDream(ctx, core.DefaultAgentID, sceneID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a dream whose context was cancelled mid-pass must report the cancellation, got %v", err)
	}
	// The code is the other half of the report: an error whose code is 0 is an
	// error a host that branches on codes reads as a pass that finished.
	if got := common.CodeOf(err); got != common.ErrCancelled {
		t.Fatalf("a cancelled pass must carry ErrCancelled, got code %d (%v)", got, err)
	}
	if rep == nil || rep.L2TopicsCompressed < 1 {
		t.Fatalf("the group this pass landed must be reported: %+v", rep)
	}
	for _, id := range []uint64{leftID, rightID} {
		got := ac.L2Meta.Get(id)
		if got == nil {
			t.Fatalf("turn %x vanished from the cache the read path serves", id)
		}
		if got.Depth != 2 {
			t.Fatalf("turn %x still reads at depth %d after the pass sank it: the scene is serving a tree from before its own records",
				id, got.Depth)
		}
	}
}

// The other exit a cancelled pass takes: not one scene got in, so every scene's
// model call failed. Reported as a model failure, the host goes to check a model
// that was never asked.
func TestDreamCancelledBeforeAnySceneLandedReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv := mockLLMServer(t, `{"l2_groups":[]}`)
	db := newTestDB(t, newTestEngine(t))
	db.llm = llm.New(LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"})
	db.config.Defaults.DreamCompressMinTopics = 4

	mustWriteScene(t, db.engine, core.DefaultAgentID, 7, "session:pre-cancel")
	for _, id := range []uint64{11, 12, 13, 14} {
		writeTopic(t, db.engine, core.DefaultAgentID, newTopic(id, 7, int64(100*id), []string{"关键词"}))
	}

	rep, err := db.RunDream(ctx, core.DefaultAgentID, 7)
	if common.CodeOf(err) != common.ErrCancelled {
		t.Fatalf("a pass cancelled before its first scene must report ErrCancelled, got code %d (%v)",
			common.CodeOf(err), err)
	}
	if rep == nil {
		t.Fatal("the prunes that ran before the cancellation are still the report's content")
	}
}
