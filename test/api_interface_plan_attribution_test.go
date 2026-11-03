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
// away is no longer on the tree either, and neither kind of content may reach back to it. The
// rule is tree membership, not "was ever created".
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

func TestInterfaceContentMayNotNameAStepTheSweepTook(t *testing.T) {
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
	if _, err := sess.AppendArchive(memhop.ArchiveInput{Kind: memhop.KindUtterance,
		ContentType: memhop.ContentText, Role: memhop.RoleUser, NodeSeq: step,
		Content: "想回绑已清理步骤的对话", CreatedAt: time.Now().UnixMilli()}); memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("an utterance naming a swept step answered %v (code %d), want ErrInvalidQuery", err, memhop.CodeOf(err))
	}
}

func TestInterfacePlanSubtreeReadsUtterancesAndEvents(t *testing.T) {
	llm := newMockLLM(t)
	m := openMockDB(t, filepath.Join(t.TempDir(), "content_attribution.meh"), llm.srv.URL)
	sess, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	opened, err := sess.Search(memhop.SearchQuery{NewScene: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	root, err := sess.PlanNodeAdd(0, "根步骤")
	if err != nil {
		t.Fatalf("PlanNodeAdd root: %v", err)
	}
	child, err := sess.PlanNodeAdd(root, "子步骤")
	if err != nil {
		t.Fatalf("PlanNodeAdd child: %v", err)
	}
	sibling, err := sess.PlanNodeAdd(0, "旁支")
	if err != nil {
		t.Fatalf("PlanNodeAdd sibling: %v", err)
	}
	stamp := time.Now().UnixMilli()
	inputs := []memhop.ArchiveInput{
		{Kind: memhop.KindUtterance, ContentType: memhop.ContentText, Role: memhop.RoleUser,
			NodeSeq: root, Content: "根步骤里的追问", CreatedAt: stamp},
		{Kind: memhop.KindUtterance, ContentType: memhop.ContentText, Role: memhop.RoleAgent,
			NodeSeq: child, Content: "子步骤里的答复", CreatedAt: stamp + 1},
		{Kind: memhop.KindEvent, ContentType: memhop.ContentText, EventType: "tool_call",
			NodeSeq: child, Content: "读取配置", CreatedAt: stamp + 2},
		{Kind: memhop.KindUtterance, ContentType: memhop.ContentText, Role: memhop.RoleUser,
			NodeSeq: sibling, Content: "旁支对话", CreatedAt: stamp + 3},
		{Kind: memhop.KindUtterance, ContentType: memhop.ContentText, Role: memhop.RoleUser,
			Content: "未绑定对话", CreatedAt: stamp + 4},
	}
	for _, in := range inputs {
		if _, err := sess.AppendArchive(in); err != nil {
			t.Fatalf("AppendArchive %q: %v", in.Content, err)
		}
	}

	got, err := sess.SearchL4(memhop.L4Query{TopicID: &opened.NewTopicID, NodeSeq: root})
	if err != nil {
		t.Fatalf("SearchL4 root: %v", err)
	}
	want := []string{"根步骤里的追问", "子步骤里的答复", "读取配置"}
	if len(got) != len(want) {
		t.Fatalf("root subtree returned %d records, want %d: %+v", len(got), len(want), got)
	}
	for i, content := range want {
		if got[i].Content != content {
			t.Errorf("root subtree record %d = %q, want %q", i, got[i].Content, content)
		}
	}
	if got[0].Kind != memhop.KindUtterance || got[1].Kind != memhop.KindUtterance || got[2].Kind != memhop.KindEvent {
		t.Errorf("the subtree did not preserve both content kinds: %+v", got)
	}

	got, err = sess.SearchL4(memhop.L4Query{TopicID: &opened.NewTopicID, NodeSeq: child})
	if err != nil {
		t.Fatalf("SearchL4 child: %v", err)
	}
	if len(got) != 2 || got[0].Content != "子步骤里的答复" || got[1].Content != "读取配置" {
		t.Fatalf("child subtree = %+v, want its utterance and event", got)
	}
}

func TestInterfaceAnUtteranceMayOnlyNameAStepOnTheTree(t *testing.T) {
	llm := newMockLLM(t)
	m := openMockDB(t, filepath.Join(t.TempDir(), "utterance_attribution.meh"), llm.srv.URL)
	sess, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	if _, err := sess.Search(memhop.SearchQuery{NewScene: true}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	step, err := sess.PlanNodeAdd(0, "已有步骤")
	if err != nil {
		t.Fatalf("PlanNodeAdd: %v", err)
	}
	before, err := sess.AppendArchive(memhop.ArchiveInput{Kind: memhop.KindEvent,
		ContentType: memhop.ContentText, EventType: "tool_call", Content: "前一条", CreatedAt: time.Now().UnixMilli()})
	if err != nil {
		t.Fatalf("AppendArchive control: %v", err)
	}
	_, err = sess.AppendArchive(memhop.ArchiveInput{Kind: memhop.KindUtterance,
		ContentType: memhop.ContentText, Role: memhop.RoleUser, NodeSeq: step + 10,
		Content: "指向不存在步骤的对话", CreatedAt: time.Now().UnixMilli()})
	if memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("an utterance naming a missing step answered %v (code %d), want ErrInvalidQuery", err, memhop.CodeOf(err))
	}
	after, err := sess.AppendArchive(memhop.ArchiveInput{Kind: memhop.KindEvent,
		ContentType: memhop.ContentText, EventType: "tool_call", Content: "后一条", CreatedAt: time.Now().UnixMilli()})
	if err != nil {
		t.Fatalf("AppendArchive after refusal: %v", err)
	}
	if after != before+1 {
		t.Fatalf("the refused utterance consumed Seq: before=%d after=%d, want %d", before, after, before+1)
	}
}
