// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Acceptance items 2 and 9 on the premise the host actually deploys: several agent domains
// share one .meh file, and each one's consolidation stays inside its own domain. Two claims,
// both silent if broken. The first is the association layer — co-occurrence pairs scenes whose
// keyword sets overlap, and the overlap here is maximal *across* the domain line, so a build
// that reached past its own domain would pair up immediately. The second is retention: the
// sweep drops records past the window, and a neighbour's aged record is nobody else's to
// collect — including from the report, whose count is the one place a host learns what left.

package test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

func TestInterfaceDreamStaysInsideItsOwnDomain(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "two_domains.meh")
	// 60s, not 1s: the window has to separate the day-old record from the rest at
	// any runner's pace. Settling the turns ahead of the Dream already costs real
	// LLM round trips, and a 1s window let a slow runner's own turns age out with
	// the planted record (the CI failure that widened this).
	knobs := func(d *memhop.MemHopDefaults) { d.ContentRetentionMs = 60000 }

	m := openMockDB(t, path, llm.srv.URL, knobs)
	alpha := newTestDB(t, m)
	beta := mustSub(t, m, llm.srv.URL, "beta-domain")

	// Two conversations per domain, in the same words: the stub endpoint answers every
	// keyword request with one fixed set, so any two scenes here score the same similarity —
	// including a pair straddling the domain line.
	for _, sess := range []*memhop.Session{alpha.Session, beta} {
		for i := 0; i < 2; i++ {
			settleOneTurn(t, sess, "用户要求重构代码", "好的,我来重构这段代码")
		}
	}
	// One record past the window per domain, riding on a round that is itself fresh, so each
	// domain's sweep has exactly one thing to take and the report's number is checkable.
	for _, pair := range []struct {
		sess *memhop.Session
		mark string
	}{{alpha.Session, "alpha"}, {beta, "beta"}} {
		if _, err := pair.sess.Search(memhop.SearchQuery{}); err != nil {
			t.Fatalf("open a turn on %s's current scene: %v", pair.mark, err)
		}
		if _, err := pair.sess.AppendArchive(memhop.ArchiveInput{
			Kind: memhop.KindEvent, ContentType: memhop.ContentText, EventType: "tool_call",
			CreatedAt: time.Now().Add(-24 * time.Hour).UnixMilli(),
			Content:   "a record past the window in " + pair.mark,
		}); err != nil {
			t.Fatalf("AppendArchive(%s): %v", pair.mark, err)
		}
		if _, err := turn(pair.sess, "顺手记一笔 "+pair.mark, "记下了"); err != nil {
			t.Fatalf("turn(%s): %v", pair.mark, err)
		}
	}

	betaBefore := domainState(t, beta)

	rep, err := alpha.Dream(context.Background(), "")
	if err != nil {
		t.Fatalf("Dream(alpha): %v", err)
	}
	if rep.L4RecordsPruned != 1 {
		t.Fatalf("alpha's Dream swept %d records, want the one aged record in alpha's own domain",
			rep.L4RecordsPruned)
	}

	// Reopen before reading any of it: the Dream left its own domain's mirrors warm, and a
	// neighbour's untouched state read off a cache would prove nothing about the disk.
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	m2 := openMockDB(t, path, llm.srv.URL, knobs)
	alpha2 := newTestDB(t, m2)
	beta2 := mustSub(t, m2, llm.srv.URL, "beta-domain")

	alphaNodes, err := alpha2.ListL1()
	if err != nil {
		t.Fatalf("ListL1(alpha): %v", err)
	}
	alphaEdge := onlyEdge(t, "alpha", alphaNodes)
	if betaNodes, err := beta2.ListL1(); err != nil || len(betaNodes) != 0 {
		t.Fatalf("the neighbour's association layer after alpha's Dream: %+v (err %v), want nothing — "+
			"it has never consolidated", betaNodes, err)
	}
	if left, err := beta2.SearchL4(memhop.L4Query{Keyword: "in beta"}); err != nil || len(left) != 1 {
		t.Fatalf("alpha's sweep took the neighbour's aged record: %+v (err %v), want it still there", left, err)
	}
	if left, err := alpha2.SearchL4(memhop.L4Query{Keyword: "in alpha"}); err != nil || len(left) != 0 {
		t.Fatalf("alpha's own aged record survived its Dream: %+v (err %v)", left, err)
	}
	if after := domainState(t, beta2); after != betaBefore {
		t.Fatalf("alpha's Dream moved the neighbour domain's reads\n before: %s\n after:  %s", betaBefore, after)
	}

	// The other domain consolidates now: same shape, its own edge, and no reach back.
	rep, err = beta2.Dream(context.Background(), "")
	if err != nil {
		t.Fatalf("Dream(beta): %v", err)
	}
	if rep.L4RecordsPruned != 1 {
		t.Fatalf("beta's Dream swept %d records, want its own one aged record", rep.L4RecordsPruned)
	}
	betaNodes, err := beta2.ListL1()
	if err != nil {
		t.Fatalf("ListL1(beta): %v", err)
	}
	betaEdge := onlyEdge(t, "beta", betaNodes)
	if betaEdge == alphaEdge {
		t.Fatalf("both domains name the same edge %s: an edge id is derived from its two endpoints, so "+
			"that is one endpoint shared across domains", betaEdge)
	}
	if left, err := beta2.SearchL4(memhop.L4Query{Keyword: "in beta"}); err != nil || len(left) != 0 {
		t.Fatalf("beta's aged record outlived its own Dream: %+v (err %v)", left, err)
	}
	if again, err := alpha2.ListL1(); err != nil || onlyEdge(t, "alpha", again) != alphaEdge {
		t.Fatalf("beta's Dream rewrote alpha's association layer: %+v (err %v), want the edge %s unchanged",
			again, err, alphaEdge)
	}
}

// onlyEdge asserts the shape one domain of two conversations must come out as: two scene nodes,
// and exactly one edge which both of them name. A pairing that reached into another domain would
// add an edge to one of these nodes' lists, and an edge naming a foreign endpoint would show up
// as the two nodes disagreeing about which edge is theirs.
func onlyEdge(t *testing.T, who string, nodes []memhop.SceneNodeView) string {
	t.Helper()
	if len(nodes) != 2 {
		t.Fatalf("%s consolidated into %+v, want one node per conversation (2)", who, nodes)
	}
	if len(nodes[0].EdgeIDs) != 1 || len(nodes[1].EdgeIDs) != 1 ||
		nodes[0].EdgeIDs[0] != nodes[1].EdgeIDs[0] {
		t.Fatalf("%s's two scene nodes do not name one edge between them: %+v", who, nodes)
	}
	return nodes[0].EdgeIDs[0]
}

// domainState renders everything one domain can read about itself, so a neighbour's
// consolidation is checked as an absence of change rather than against a list of fields.
func domainState(t *testing.T, sess *memhop.Session) string {
	t.Helper()
	scenes, err := sess.ListScenes("")
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	names := make([]string, 0, len(scenes))
	for _, s := range scenes {
		names = append(names, s.SceneID)
	}
	l1, err := sess.ListL1()
	if err != nil {
		t.Fatalf("ListL1: %v", err)
	}
	l4, err := sess.SearchL4(memhop.L4Query{})
	if err != nil {
		t.Fatalf("SearchL4: %v", err)
	}
	profile, err := sess.GetL0()
	if err != nil {
		t.Fatalf("GetL0: %v", err)
	}
	brief := struct {
		Scenes  []string               `json:"scenes"`
		Nodes   []memhop.SceneNodeView `json:"nodes"`
		Records []memhop.ArchiveSlot   `json:"records"`
		Slot    *memhop.ProfileSlot    `json:"slot"`
		Turns   []memhop.SceneContext  `json:"turns"`
	}{}
	for _, id := range names {
		ctx, err := sess.SceneContext(id)
		if err != nil {
			t.Fatalf("SceneContext(%s): %v", id, err)
		}
		brief.Turns = append(brief.Turns, *ctx)
	}
	brief.Scenes, brief.Nodes, brief.Records, brief.Slot = names, l1, l4, profile
	out, err := json.Marshal(brief)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}
