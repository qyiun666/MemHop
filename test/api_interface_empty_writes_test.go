// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"path/filepath"
	"strings"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
)

// The write surface refuses more than it documents, and a refusal is only worth something to
// a host if it is the same refusal tomorrow. Each boundary below answers with a code and a
// sentence naming what was missing — measured before being promised — and each also answers
// the half that actually matters: what the file looks like afterwards.
//
// The asymmetry is deliberate. A round may close with only one side of the dialogue, because a
// round the agent started has no user line to carry; an event may not be empty, because an
// event with no content says nothing happened and no reading of that is not a guess. Same
// split for the outcome word: an empty one records nothing, since a `turn_outcome` row whose
// word is blank would be listed beside the real ones and read as a verdict nobody gave.
func TestInterfaceEmptyWritesRefuseAndStoreNothing(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "empty_writes.meh")
	m := openMockDB(t, path, llm.srv.URL)
	sess, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	if _, err := sess.Search(memhop.SearchQuery{NewScene: true}); err != nil {
		t.Fatalf("open a scene: %v", err)
	}

	// 1. a round with neither side and no outcome word is not a round.
	_, err = sess.Update(memhop.TurnEnd{CreatedAt: 1_700_000_000_000})
	if memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("an empty close must be refused, got %v (code %d)", err, memhop.CodeOf(err))
	}
	if !strings.Contains(err.Error(), "input") || !strings.Contains(err.Error(), "outcome") {
		t.Fatalf("the refusal must name the three things a round can close with: %v", err)
	}
	// Refused means nothing was written: the scene lists no turn, and the round the refusal
	// happened inside is still the one a later close settles.
	ctx, err := sess.SceneContext("")
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}
	if len(ctx.Topics) != 0 {
		t.Fatalf("a refused close left %d rows on the scene: %+v", len(ctx.Topics), ctx.Topics)
	}

	// 2. an event that says nothing is refused the same way, inside the same open round.
	if _, err := sess.AppendArchive(memhop.ArchiveInput{Kind: memhop.KindEvent,
		ContentType: memhop.ContentText, CreatedAt: 1_700_000_001_000}); err == nil {
		t.Fatal("an event with no content must be refused")
	} else if memhop.CodeOf(err) != memhop.ErrInvalidQuery || !strings.Contains(err.Error(), "content") {
		t.Fatalf("the refusal should say content is required, got code %d (%v)", memhop.CodeOf(err), err)
	}

	// 3. that round then closes for real, with no outcome word: the two dialogue lines are
	// there and the event track is empty, so neither refusal left a row behind.
	id, err := turn(sess, "被拒之后这一轮还在", "在，所以这里收得下来")
	if err != nil {
		t.Fatalf("close after the refusals: %v", err)
	}
	rows, err := sess.SearchL4(memhop.L4Query{TopicID: &id})
	if err != nil {
		t.Fatalf("SearchL4: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("a round closed without an outcome word carries %d rows, want the two dialogue lines: %+v",
			len(rows), rows)
	}
	for _, r := range rows {
		if r.Kind != memhop.KindUtterance {
			t.Fatalf("an empty outcome recorded a %v row beside the dialogue: %+v", r.Kind, r)
		}
	}

	// 5. the paired half: a round closed WITH an outcome word carries that word verbatim, in
	// its own row, beside the two dialogue lines. Blank records nothing and a word records the
	// word — the same judgement asked from both sides, so neither direction can drift alone.
	if _, err := sess.Search(memhop.SearchQuery{}); err != nil {
		t.Fatalf("open the outcome turn: %v", err)
	}
	withOutcome, err := sess.Update(memhop.TurnEnd{Input: "这件事办完了吗", Output: "办完了",
		Outcome: "已交付", CreatedAt: 1_700_000_003_000})
	if err != nil {
		t.Fatalf("close with an outcome: %v", err)
	}
	lines, err := sess.SearchL4(memhop.L4Query{TopicID: &withOutcome.ID})
	if err != nil {
		t.Fatalf("SearchL4 the outcome turn: %v", err)
	}
	verdict := ""
	events := 0
	for _, r := range lines {
		if r.Kind == memhop.KindEvent {
			events++
			verdict = r.Content
		}
	}
	if events != 1 || verdict != "已交付" {
		t.Fatalf("the outcome word this round was given did not come back as its own row: %d events %q (%+v)",
			events, verdict, lines)
	}

	// 6. one side alone is a real round, and the slot the missing side would have taken stays
	// empty rather than being filled with a blank line.
	if _, err := sess.Search(memhop.SearchQuery{}); err != nil {
		t.Fatalf("open the next turn: %v", err)
	}
	oneSided, err := sess.Update(memhop.TurnEnd{Output: "我先把这件事说清楚", CreatedAt: 1_700_000_002_000})
	if err != nil {
		t.Fatalf("close with only an output: %v", err)
	}
	own, err := sess.SearchL4(memhop.L4Query{TopicID: &oneSided.ID})
	if err != nil {
		t.Fatalf("SearchL4 the one-sided turn: %v", err)
	}
	if len(own) != 1 || own[0].Role != memhop.RoleAgent || own[0].Content != "我先把这件事说清楚" {
		t.Fatalf("a one-sided round must carry exactly the side it has: %+v", own)
	}
	// The agent's line belongs to the answer slot. Reading it back from Seq 1 would tell a host
	// "the user said this", which is the one thing this shape did not do.
	if own[0].Seq != 2 {
		t.Fatalf("an output landed on Seq %d, not the answer slot the dialogue reserves: %+v", own[0].Seq, own)
	}
}
