// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package llmops

import (
	"context"
	"errors"
	"fmt"
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
	// Both blocks present and quiet is a real answer about the emotion; the four
	// silent dimensions answer nothing, so no type word is claimed for them.
	out, err := parseDistillResponse(`{"emotion":{},"mbti":{},"personality":""}`, nil)
	if err != nil {
		t.Fatalf("an in-contract neutral reply was refused: %v", err)
	}
	if out.MBTI.Type != "" {
		t.Fatalf("type = %q, want no type word derived from four silent dimensions", out.MBTI.Type)
	}
	partial, err := parseDistillResponse(`{"emotion":{},"mbti":{"i_e":0,"n_s":-0.2,"t_f":0.3,"j_p":0}}`, nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if partial.MBTI.Type != "XNFX" {
		t.Fatalf("type = %q, want the two answered axes and X on the two silent ones", partial.MBTI.Type)
	}
}

// The personality budget is one number with two consumers: the prompt that asks
// the model for it and the parser that cuts to it. Stating it twice let them drift,
// and a reply written to a length the parser then cuts reads as a truncated
// sentence in every later prompt.
func TestSystemDistillStatesTheBudgetTheParserEnforces(t *testing.T) {
	want := fmt.Sprintf("at most %d characters", distillPersonalityMaxRunes)
	if !strings.Contains(systemDistill, want) {
		t.Fatalf("the prompt never states %q, so the cap and the ask are two facts again", want)
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
	if len(out.PerNode) != 1 {
		t.Fatalf("per_node = %+v, want only the row naming a sampled node", out.PerNode)
	}
	if em, ok := out.PerNode[1]; !ok || em.Valence != 0.5 || em.Arousal != 0.5 {
		t.Fatalf("per_node[1] = %+v (present: %v), want the sampled node's own row", em, ok)
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

// retryFailsOnce answers the first attempt with something no parser can use and fails
// every later call — the shape of a format-constrained retry that gets cancelled, or
// refused by the endpoint.
type retryFailsOnce struct {
	budgetSpy
	attempts int
	failWith error
}

func (s *retryFailsOnce) Chat(_ context.Context, _, _ string, _ int) (string, error) {
	s.attempts++
	if s.attempts == 1 {
		return "not json at all", nil
	}
	return "", s.failWith
}

// Both call points make their first attempt through ChatWithRetry, so the two-budget
// route has to run through the same counting Chat — otherwise the injected failure
// never reaches the format retry and the test asserts on a call that did not happen.
func (s *retryFailsOnce) ChatWithRetry(ctx context.Context, system, user string, primaryMax, _ int) (string, error) {
	return s.Chat(ctx, system, user, primaryMax)
}

// The retry's own failure is what the host has to hear: an off-contract first reply
// followed by a cancelled retry is a client that walked away, and reporting the parse
// error instead sends the host to debug a model that never refused anything.
func TestFormatRetryFailureKeepsItsOwnCode(t *testing.T) {
	for _, fail := range []error{
		common.NewError(common.ErrCancelled, "llm call cancelled", context.Canceled),
		common.NewError(common.ErrLLM, "llm api: 503 - busy"),
	} {
		spy := &retryFailsOnce{budgetSpy: budgetSpy{ceiling: 8192}, failWith: fail}
		_, err := Distill(context.Background(), spy,
			[]L1Sample{{IDHash: 1, Keywords: []string{"a"}, Importance: 1}})
		if common.CodeOf(err) != common.CodeOf(fail) {
			t.Fatalf("distill over a %v retry: code=%d err=%v", fail, common.CodeOf(err), err)
		}
		// The two cases share code 9002, so the code alone would pass on the parse
		// error: the retry's own failure has to be reachable in the chain.
		if !errors.Is(err, fail) {
			t.Fatalf("distill dropped the retry's failure from the chain: %v", err)
		}
		spy.attempts = 0
		_, err = Consolidate(context.Background(), spy,
			[]core.TopicSlot{{ID: 1, UserTimestamp: 1}, {ID: 2, UserTimestamp: 2}}, 1)
		if common.CodeOf(err) != common.CodeOf(fail) {
			t.Fatalf("consolidate over a %v retry: code=%d err=%v", fail, common.CodeOf(err), err)
		}
		if !errors.Is(err, fail) {
			t.Fatalf("consolidate dropped the retry's failure from the chain: %v", err)
		}
	}
}

// An endpoint configured for 64 output tokens refuses a request for 8192 outright,
// so no rung of any ladder may ask above the ceiling. The shape is part of the
// contract too: three widening budgets and then the format-constrained retry at the
// widest of them.
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

// A reply cut off by the output ceiling is not a model that cannot do structured output,
// and the host tells the two apart only from this error: one is fixed by raising
// MaxOutputTokens, the other by changing model. Keyword extraction is the call point a
// reasoning model most often trips - the ladder's own comment says why - so the
// truncation has to survive the whole ladder rather than be relabelled at the last step.
type alwaysTruncated struct {
	calls int
}

func (s *alwaysTruncated) Chat(_ context.Context, _, _ string, maxTokens int) (string, error) {
	s.calls++
	return "", common.NewError(common.ErrLLM,
		fmt.Sprintf("llm response hit the output ceiling at max_tokens=%d", maxTokens),
		common.ErrTruncated)
}

// Both call points make their first attempt through ChatWithRetry, so the two-budget seam
// has to run through the same counting Chat.
func (s *alwaysTruncated) ChatWithRetry(ctx context.Context, system, user string, primaryMax, _ int) (string, error) {
	return s.Chat(ctx, system, user, primaryMax)
}

func (s *alwaysTruncated) MaxOutputTokens() int { return 8192 }

func TestKeywordExtractionKeepsATruncationAsTheCeilingItIs(t *testing.T) {
	spy := &alwaysTruncated{}
	_, err := ExtractKeywords(context.Background(), spy, "the user asked to rebuild the auth module and add tests")
	if err == nil {
		t.Fatal("every attempt was truncated, so extraction must not report success")
	}
	if common.CodeOf(err) != common.ErrLLM {
		t.Fatalf("the code has to stay the LLM one, got %v", err)
	}
	if !errors.Is(err, common.ErrTruncated) {
		t.Fatalf("the truncation did not survive the ladder: %v", err)
	}
	if strings.Contains(err.Error(), "no parseable JSON") {
		t.Fatalf("a ceiling failure was reported as a model that cannot answer in JSON: %v", err)
	}
	if spy.calls != 4 {
		t.Fatalf("the ladder made %d calls, want 3 widening budgets plus the format retry", spy.calls)
	}
}

// Which chunk failed is known only to the chunking loop, and the kind of failure has to
// survive its wrapping: a truncated chunk is still a ceiling the host can act on, not a
// complaint about JSON.
func TestChunkedExtractionNamesTheChunkAndKeepsTheCause(t *testing.T) {
	spy := &alwaysTruncated{}
	if _, err := ExtractKeywords(context.Background(), spy, strings.Repeat("word ", 1000)); err == nil {
		t.Fatal("a truncated chunk must fail the whole extraction, not skip that chunk")
	} else {
		if !strings.Contains(err.Error(), "chunk 0 of") {
			t.Fatalf("the error does not name the chunk: %v", err)
		}
		if !errors.Is(err, common.ErrTruncated) {
			t.Fatalf("the wrapping dropped the cause: %v", err)
		}
	}
	if spy.calls != 4 {
		t.Fatalf("the loop made %d calls, want the ladder to stop at the first failing chunk", spy.calls)
	}
}
