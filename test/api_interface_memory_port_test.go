// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// An agent framework does not hand MemHop its memory: it defines a port — open a round per
// invocation, recall before every model call, hand the invocation's facts back once at the end —
// and expects somebody to write the adapter. That adapter is the whole integration cost, and so
// far it existed only in a private branch of the tooling that drives all three repositories.
// MemHop must not import the framework, so what belongs here is not the port but the mapping:
// this file is the ~40 lines an integrator writes, executable and asserted in this repository.
//
// What the assertions pin is the part a host cannot check without running it: that the adapter
// holds no ids at all (so a restart with a fresh one still remembers everything), that the
// framework's own outcome word is what comes back out of the store, and that a write the store
// refuses is reported to the framework rather than quietly shortened.

package test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

// memoryPort is the adapter. Its entire state is one session handle: no scene id, no turn key,
// no topic id — those are the library's to remember, which is what makes a reopened file resume
// the same conversation with a brand-new adapter.
type memoryPort struct {
	sess *memhop.Session
	// The profile digest travels from the round-opening read to every recall of that round:
	// it is what the per-turn read hands back, and re-reading the scene would open turns.
	brief string
}

// begin opens the round this invocation will write into: the framework's before-invocation hook.
func (p *memoryPort) begin() error {
	res, err := p.sess.Search(memhop.SearchQuery{})
	if err != nil {
		return err
	}
	p.brief = res.ProfileBrief
	return nil
}

// recall is the pure read before a model call: the profile digest the per-turn read handed back,
// this round's plan forest with its folded conclusions, and one row per settled round.
// recall never opens a round: everything in it comes from reads that consume nothing, which is
// what lets a framework think several times per invocation without burning turns.
func (p *memoryPort) recall() ([]string, error) {
	var out []string
	if p.brief != "" {
		out = append(out, "profile:\n"+p.brief)
	}
	tree, err := p.sess.PlanState()
	switch {
	case err == nil:
		for _, root := range tree.Roots {
			out = append(out, "plan:\n"+renderSteps(root))
		}
	case memhop.CodeOf(err) == memhop.ErrInvalidQuery:
		// No round is open — the fresh adapter right after a restart, or a framework that reads
		// before its begin hook. That is one answer ("nothing in progress"), not a storage
		// failure, and the port has no second channel to tell the two apart: treating it as a
		// failure would abort the invocation over an empty plan block.
	default:
		return nil, err
	}
	scene, err := p.sess.SceneContext("")
	if err != nil {
		return nil, err
	}
	for _, row := range scene.Topics {
		out = append(out, "round:\n"+strings.Join(row.Keywords, " "))
	}
	return out, nil
}

// remember closes the round with the three facts the framework knows by construction.
func (p *memoryPort) remember(input, output, outcome string) error {
	_, err := p.sess.Update(memhop.TurnEnd{
		Input: input, Output: output, Outcome: outcome, CreatedAt: time.Now().UnixMilli(),
	})
	return err
}

func renderSteps(n memhop.PlanNodeView) string {
	line := "#" + strconv.FormatUint(uint64(n.Seq), 10) + " " + n.Title + " [" + n.Status + "]"
	if n.Summary != "" {
		line += " — " + n.Summary
	}
	for _, c := range n.Children {
		line += "\n" + renderSteps(c)
	}
	return line
}

func TestInterfaceMemoryPortAdapterHoldsNoIds(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "port.meh")
	m := openMockDB(t, path, llm.srv.URL)
	port := &memoryPort{sess: newTestDB(t, m).Session}

	if err := port.begin(); err != nil {
		t.Fatalf("invocation 1 begin: %v", err)
	}
	step, err := port.sess.PlanNodeAdd(0, "查清回归")
	if err != nil {
		t.Fatalf("PlanNodeAdd: %v", err)
	}
	if err := port.sess.PlanNodeUpdate(memhop.PlanStep{
		Seq: step, Status: memhop.PlanStatusDone, Summary: "第 7 步引入的"}); err != nil {
		t.Fatalf("PlanNodeUpdate: %v", err)
	}
	before, err := port.recall()
	if err != nil {
		t.Fatalf("recall 1: %v", err)
	}
	// The prompt the framework assembles carries both of the things item 1 and item 6 promise:
	// the profile digest, and this round's plan block with the closed step's own conclusion.
	joined := strings.Join(before, "\n")
	if !strings.Contains(joined, "name: test-primary") || !strings.Contains(joined, "#1 查清回归 [done] — 第 7 步引入的") {
		t.Fatalf("the first recall gave the model no profile or no plan block:\n%s", joined)
	}
	if err := port.remember("我们回到那个回归", "查清了，是第 7 步", "succeeded"); err != nil {
		t.Fatalf("invocation 1 remember: %v", err)
	}
	// One invocation, one settled round — the three recalls of the next invocation must not add
	// rows of their own.
	afterOne, err := port.sess.SceneContext("")
	if err != nil {
		t.Fatalf("SceneContext after invocation 1: %v", err)
	}
	rounds := len(afterOne.Topics)

	if err := port.begin(); err != nil {
		t.Fatalf("invocation 2 begin: %v", err)
	}
	// Several thinks per invocation is the normal shape; recall must not cost a turn each time.
	for i := 0; i < 3; i++ {
		if _, err := port.recall(); err != nil {
			t.Fatalf("extra recall %d: %v", i, err)
		}
	}
	second, err := port.recall()
	if err != nil {
		t.Fatalf("recall 2: %v", err)
	}
	if !strings.Contains(strings.Join(second, "\n"), "round:") {
		t.Fatalf("invocation 2 recalled no settled round: %v", second)
	}
	if err := port.remember("再问一句", "再答一句", "succeeded"); err != nil {
		t.Fatalf("invocation 2 remember: %v", err)
	}
	afterTwo, err := port.sess.SceneContext("")
	if err != nil {
		t.Fatalf("SceneContext after invocation 2: %v", err)
	}
	if grew := len(afterTwo.Topics) - rounds; grew != 1 {
		t.Fatalf("one invocation with four recalls settled %d rounds, want exactly 1", grew)
	}
	// The framework's word is stored as it was said: nothing translates it and nothing invents
	// a synonym the host then has to map back.
	kind := memhop.KindEvent
	events, err := port.sess.SearchL4(memhop.L4Query{Kind: &kind})
	if err != nil {
		t.Fatalf("SearchL4: %v", err)
	}
	crossed := false
	for _, e := range events {
		if e.EventType == "turn_outcome" && e.Content == "succeeded" {
			crossed = true
		}
	}
	if !crossed {
		t.Fatalf("the outcome word never reached the event track verbatim: %+v", events)
	}

	// A write the store refuses must reach the framework as an error. Silent shortening would
	// look like a recorded fact, and the round would close on a lie.
	if err := port.begin(); err != nil {
		t.Fatalf("invocation 3 begin: %v", err)
	}
	if err := port.remember("短", strings.Repeat("长", 70*1024), "succeeded"); err == nil {
		t.Fatal("an over-budget original closed the round instead of being refused")
	}
	if err := port.remember("短", "改短之后重写", "succeeded"); err != nil {
		t.Fatalf("the retry after the refusal: %v", err)
	}

	// And a fresh adapter over the reopened file still knows the conversation: it had nowhere to
	// keep an id, so everything it recalls has to come out of the file.
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened := &memoryPort{sess: newTestDB(t, openMockDB(t, path, llm.srv.URL)).Session}
	rows, err := reopened.recall()
	if err != nil {
		t.Fatalf("recall after restart, with no round open yet: %v", err)
	}
	if strings.Contains(strings.Join(rows, "\n"), "plan:") {
		t.Fatalf("a fresh adapter with no round open rendered a plan block out of nowhere:\n%s",
			strings.Join(rows, "\n"))
	}
	if strings.Count(strings.Join(rows, "\n"), "round:") < 2 {
		t.Fatalf("after a restart the adapter recalled %d rounds, want the two settled here:\n%s",
			strings.Count(strings.Join(rows, "\n"), "round:"), strings.Join(rows, "\n"))
	}
}
