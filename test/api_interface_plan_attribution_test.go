// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

// A trajectory event's whole meaning is the step it names: the read side takes a step's
// `Seq` and asks what happened under it (item 6 of the host's loop — 追加轨迹绑定 plan 节点).
// So an event bound to a step that is not on the tree is worse than a stray row: the ordinal a
// turn hands out is derived from the highest number either side has seen, so accepting one
// would reserve numbers for a step nobody ever created and leave a hole the host cannot
// explain. What the surface answers instead (measured, then pinned here) is a refusal naming
// the step, and the refused append consumes no ordinal.
//
// The second file checks the same rule from the other end: a step the retention sweep took
// away is no longer on the tree either, and the event may not reach back to it. The rule is
// tree membership, not "was ever created".
func TestInterfaceAnEventMayOnlyNameAStepOnTheTree(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "attribution.meh")
	m := openMockDB(t, path, llm.srv.URL)
	sess, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	if _, err := sess.Search(memhop.SearchQuery{NewScene: true}); err != nil {
		t.Fatalf("open a scene: %v", err)
	}
	step, err := sess.PlanNodeAdd(0, "树上有的那一步")
	if err != nil {
		t.Fatalf("PlanNodeAdd: %v", err)
	}

	_, err = sess.AppendArchive(memhop.ArchiveInput{Kind: memhop.KindEvent,
		ContentType: memhop.ContentText, EventType: "tool_call", NodeSeq: step + 6,
		Content: "指着一步不存在的事件", CreatedAt: time.Now().UnixMilli()})
	if memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("binding to a step that is not on the tree must be refused, got code %d (%v)",
			memhop.CodeOf(err), err)
	}
	if !strings.Contains(err.Error(), "step") || !strings.Contains(err.Error(), "plan tree") {
		t.Fatalf("the refusal should say which step is missing and why, got: %v", err)
	}

	// The refusal is not a reservation: the next step still gets the very next ordinal, so the
	// turn's numbering has no hole where an attempted event used to be.
	next, err := sess.PlanNodeAdd(0, "紧随其后的一步")
	if err != nil {
		t.Fatalf("PlanNodeAdd after the refusal: %v", err)
	}
	if next != step+1 {
		t.Fatalf("a refused append consumed an ordinal: the tree went %d -> %d, want %d", step, next, step+1)
	}

	// The control, so the arm above is a missing-step refusal and not a blanket one.
	ownSeq, err := sess.AppendArchive(memhop.ArchiveInput{Kind: memhop.KindEvent,
		ContentType: memhop.ContentText, EventType: "tool_call", NodeSeq: step,
		Content: "指着真存在的那一步", CreatedAt: time.Now().UnixMilli()})
	if err != nil {
		t.Fatalf("binding to a step on the tree must be accepted: %v", err)
	}
	if ownSeq == 0 {
		t.Fatal("the accepted append returned no slot ordinal")
	}
}

func TestInterfaceAnEventMayNotNameAStepTheSweepTook(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "attribution_swept.meh")
	m := openMockDB(t, path, llm.srv.URL, sweepOnly)
	sess, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	if _, err := sess.Search(memhop.SearchQuery{NewScene: true}); err != nil {
		t.Fatalf("open a scene: %v", err)
	}
	step, err := sess.PlanNodeAdd(0, "会被扫掉的一步")
	if err != nil {
		t.Fatalf("PlanNodeAdd: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	rep, err := sess.Dream(context.Background(), "")
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if rep.L5NodesPruned != 1 {
		t.Fatalf("the premise of this arm is that the sweep took the step, it took %d: %+v",
			rep.L5NodesPruned, rep)
	}
	tree, err := sess.PlanState()
	if err != nil {
		t.Fatalf("PlanState after the sweep: %v", err)
	}
	if tree.TotalCount != 0 {
		t.Fatalf("the tree still counts %d step(s) the sweep just took: %+v", tree.TotalCount, tree.Roots)
	}

	if _, err := sess.AppendArchive(memhop.ArchiveInput{Kind: memhop.KindEvent,
		ContentType: memhop.ContentText, EventType: "tool_call", NodeSeq: step,
		Content: "想指着已被扫掉的那一步", CreatedAt: time.Now().UnixMilli()}); err == nil {
		t.Fatal("an event may not reach back to a step that is no longer on the tree — the ordinal it " +
			"names would then be reserved for a record that does not exist")
	} else if memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("the refusal must stay the same one the missing step gets, got code %d (%v)",
			memhop.CodeOf(err), err)
	}
}
