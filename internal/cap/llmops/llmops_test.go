// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package llmops

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// A reply is JSON the model may wrap in a fence, and node ids come back as numbers
// or as quoted strings depending on the model — both shapes have to parse, since
// a group that fails to parse is a merge the scene never gets.
func TestParseConsolidateResponseAcceptsBothIDShapes(t *testing.T) {
	out, err := parseConsolidateResponse("```json\n" +
		`{"l2_groups":[{"node_hashes":[11,"22"],"merged_summary":"合并"}]}` + "\n```")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out.L2Groups) != 1 {
		t.Fatalf("output = %+v", out)
	}
	g := out.L2Groups[0]
	if len(g.NodeHashes) != 2 || g.NodeHashes[0] != 11 || g.NodeHashes[1] != 22 {
		t.Fatalf("group = %+v, want nodes 11 and 22", g)
	}
	if g.MergedSummary != "合并" {
		t.Fatalf("summary = %q", g.MergedSummary)
	}
}

// A member id that cannot be parsed is an error rather than a dropped group, and a
// reply that is not JSON at all is an error rather than an empty merge list: either
// one silently thinned would read exactly like a model that merged less.
func TestParseConsolidateResponseRefusesWhatItCannotUse(t *testing.T) {
	for _, reply := range []string{
		`{"l2_groups":[{"node_hashes":["nope"]}]}`,
		`{"l2_groups":[{"node_hashes":[1,"still-not-a-number"]}]}`,
		`{"l2_groups":`,
		`I merged everything, sorry about the JSON`,
	} {
		if _, err := parseConsolidateResponse(reply); common.CodeOf(err) != common.ErrLLM {
			t.Fatalf("%q: want ErrLLM, got %v", reply, err)
		}
	}
}

// The topic count the prompt aims at is the caller's configured floor, stated
// twice: a constant here would tell the model to merge to a number the engine
// never runs by, and rule 2 (never fuse different subjects) outranks it.
func TestSystemConsolidateStatesTheConfiguredFloor(t *testing.T) {
	prompt := systemConsolidate(40)
	for _, want := range []string{"down toward 40 or below", "the total is already 40 or fewer"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the prompt never states %q", want)
		}
	}
	if strings.Contains(prompt, " 20") {
		t.Fatal("a topic count that is not the one passed in is still in the prompt")
	}
}

// The MBTI type word is re-derived from the four dimensions, so a reply whose
// type contradicts its own numbers does not get to keep it. The dimensions
// themselves are clamped: an out-of-range signal would otherwise flow straight
// into the profile record.
func TestParseDistillResponseDerivesTypeAndClamps(t *testing.T) {
	out, err := parseDistillResponse(`{"emotion":{"valence":4,"arousal":-2,"dominance":0.6},`+
		`"mbti":{"i_e":-9,"n_s":0.2,"t_f":-0.3,"j_p":0.1,"type":"ZZZZ"},`+
		`"personality":"  务实直接  ","per_node":[]}`, nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.MBTI.Type != "ISTP" {
		t.Fatalf("type = %q, want ISTP derived from the four dimensions", out.MBTI.Type)
	}
	if out.MBTI.IE != -1 {
		t.Fatalf("i_e = %v, want it clamped to -1", out.MBTI.IE)
	}
	if out.Emotion.Valence != 1 || out.Emotion.Arousal != 0 || out.Emotion.Dominance != 0.6 {
		t.Fatalf("emotion = %+v, want the unit range clamped and the in-range value kept", out.Emotion)
	}
	if out.Personality != "务实直接" {
		t.Fatalf("personality = %q, want it trimmed", out.Personality)
	}
}

// Valid JSON that answers none of the contract is not a thin answer, it is no
// answer: the zeros it decodes to would erase the distilled emotion, and four
// silent dimensions derive a personality type out of nothing.
func TestParseDistillResponseRefusesAReplyWithNoContract(t *testing.T) {
	for _, reply := range []string{
		`{}`,
		`{"personality":"看起来挺乐观"}`,
		`{"emotion":{"valence":0.5,"arousal":0.5,"dominance":0.5}}`,
		`{"emotion":{"valence":0.5,"arousal":0.5,"dominance":0.5},"mbti":null}`,
	} {
		if _, err := parseDistillResponse(reply, nil); common.CodeOf(err) != common.ErrLLM {
			t.Fatalf("%q: want ErrLLM, got %v", reply, err)
		}
	}
	// Both blocks present and quiet is a real answer: nothing to merge, nothing
	// to invent — the caller decides what to do with a neutral reading.
	out, err := parseDistillResponse(`{"emotion":{},"mbti":{},"personality":""}`, nil)
	if err != nil {
		t.Fatalf("an in-contract neutral reply was refused: %v", err)
	}
	if out.MBTI.Type != "ESFP" {
		t.Fatalf("type = %q, want the four zero dimensions read as their positive side", out.MBTI.Type)
	}
}

// A personality past the cap is cut to it rather than refused: the summary goes
// into the profile record and into every later prompt, so the cap is what keeps
// one verbose reply from inflating both.
func TestParseDistillResponseCapsPersonality(t *testing.T) {
	long := strings.Repeat("话", distillPersonalityMaxRunes+40)
	out, err := parseDistillResponse(`{"emotion":{},"mbti":{},"personality":"`+long+`","per_node":[]}`, nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := len([]rune(out.Personality)); got != distillPersonalityMaxRunes {
		t.Fatalf("personality is %d runes, want the cap %d", got, distillPersonalityMaxRunes)
	}
}

// A per-node row is kept only when it names a node this pass put in front of the
// model — as hex that parses and as one of the sampled ids. The id is the
// backfill's address: a row naming anything else has no node to write to, and
// handing it down aborts the whole distillation stage on ErrNotFound, every pass,
// until the model happens to answer differently.
func TestParseDistillResponseKeepsOnlySampledNodeRows(t *testing.T) {
	known := map[uint64]struct{}{1: {}}
	out, err := parseDistillResponse(`{"emotion":{},"mbti":{},"personality":"",`+
		`"per_node":[{"id_hex":"0000000000000001","valence":0.5,"arousal":0.5},`+
		`{"id_hex":"0000000000000002","valence":0.9,"arousal":0.9},`+
		`{"id_hex":"nonsense","valence":0.9,"arousal":0.9}]}`, known)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out.PerNode) != 1 || out.PerNode[0].IDHex != "0000000000000001" {
		t.Fatalf("per_node = %+v, want only the row naming a sampled node", out.PerNode)
	}
}

func TestParseDistillResponseRefusesNonJSON(t *testing.T) {
	if _, err := parseDistillResponse("the agent seems cheerful", nil); common.CodeOf(err) != common.ErrLLM {
		t.Fatalf("want ErrLLM, got %v", err)
	}
}

// budgetSpy records every output budget a call point asks the transport for, and
// answers with something no parser can use, so the whole attempt ladder runs.
type budgetSpy struct {
	ceiling    int
	calls      []int
	escalation [][2]int
}

func (s *budgetSpy) Chat(_ context.Context, _, _ string, maxTokens int) (string, error) {
	s.calls = append(s.calls, maxTokens)
	return "not json at all", nil
}

func (s *budgetSpy) ChatWithRetry(_ context.Context, _, _ string, primaryMax, retryMax int) (string, error) {
	s.calls = append(s.calls, primaryMax)
	s.escalation = append(s.escalation, [2]int{primaryMax, retryMax})
	return "not json at all", nil
}

func (s *budgetSpy) MaxOutputTokens() int { return s.ceiling }

// An endpoint configured for 64 output tokens refuses a request for 8192 outright,
// so no rung of any ladder may ask above the ceiling — the ladder that used to end
// at the consolidation constant failed the turn it was meant to rescue. The shape
// is part of the contract too: three widening budgets and then the format-constrained
// retry at the widest of them, which is the rung a dropped ladder loses first.
func TestKeywordLadderStaysWithinTheConfiguredCeiling(t *testing.T) {
	for _, tc := range []struct {
		ceiling int
		want    []int
	}{
		{ceiling: 64, want: []int{64, 64, 64, 64}},
		{ceiling: 1024, want: []int{512, 1024, 1024, 1024}},
		{ceiling: 8192, want: []int{512, 4096, 8192, 8192}},
		{ceiling: 16384, want: []int{512, 4096, 8192, 8192}},
	} {
		spy := &budgetSpy{ceiling: tc.ceiling}
		if _, err := ExtractKeywords(context.Background(), spy, "今天把存储层跑通了"); err == nil {
			t.Fatalf("ceiling %d: the spy never answers in JSON; extraction must report that", tc.ceiling)
		}
		if !slices.Equal(spy.calls, tc.want) {
			t.Fatalf("ceiling %d: ladder = %v, want %v", tc.ceiling, spy.calls, tc.want)
		}
	}
}

// The truncation retry is where headroom gets spent, and it may spend exactly what
// the endpoint declared: at or below the design ceiling nothing more is available,
// and above it that room is what a consolidated summary actually needs. The first
// attempt never asks above the design ceiling it was built for.
func TestTruncationRetryEscalatesToTheEndpointCeiling(t *testing.T) {
	for _, tc := range []struct {
		ceiling   int
		wantRetry int
	}{
		{ceiling: 1024, wantRetry: 1024},
		{ceiling: 16384, wantRetry: 16384},
	} {
		spy := &budgetSpy{ceiling: tc.ceiling}
		if _, err := Distill(context.Background(), spy, []L1Sample{{IDHash: 1, Importance: 0.5}}); err == nil {
			t.Fatalf("ceiling %d: the spy never answers in JSON", tc.ceiling)
		}
		if got := spy.escalation[0]; got[0] > tc.ceiling || got[1] != tc.wantRetry {
			t.Fatalf("ceiling %d: distill asked primary %d (over the ceiling?) then retry %d, want retry %d",
				tc.ceiling, got[0], got[1], tc.wantRetry)
		}

		spy = &budgetSpy{ceiling: tc.ceiling}
		topics := []core.TopicSlot{{ID: 1, SceneID: 7, UserTimestamp: 1000}}
		if _, err := Consolidate(context.Background(), spy, topics, 20); err == nil {
			t.Fatalf("ceiling %d: the spy never answers in JSON", tc.ceiling)
		}
		if got := spy.escalation[0]; got[0] > tc.ceiling || got[1] != tc.wantRetry {
			t.Fatalf("ceiling %d: consolidate asked primary %d then retry %d, want retry %d",
				tc.ceiling, got[0], got[1], tc.wantRetry)
		}
	}
}
