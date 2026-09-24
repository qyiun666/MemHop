// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"path/filepath"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

// The facade documents one exception among the L4 conditions: `IDs` is the only one answered
// by id rather than by a scan **when it is the only one set**. That clause is where a silent
// failure hides — a fast path keyed on "ids present" would keep answering by id when other
// conditions are present too, and a host would get records it explicitly filtered out. Since
// the same facade says the set conditions AND together, the host's reading is that a filter is
// never weakened by a companion filter. Measured here as three invariants:
//
//   - the two `Kind` halves rejoin into the unfiltered read, in the same order;
//   - `IDs` alone answers exactly the set it names, and `IDs` plus a condition answers the
//     subset that condition selects;
//   - an id that is not there is skipped rather than failing the read.
func TestInterfaceL4FiltersComposeRatherThanShortCircuit(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "filter_matrix.meh")
	m := openMockDB(t, path, llm.srv.URL)
	sess, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	if _, err := sess.Search(memhop.SearchQuery{NewScene: true}); err != nil {
		t.Fatalf("open a scene: %v", err)
	}
	step, err := sess.PlanNodeAdd(0, "查重试配置")
	if err != nil {
		t.Fatalf("PlanNodeAdd: %v", err)
	}
	if _, err := sess.AppendArchive(memhop.ArchiveInput{Kind: memhop.KindEvent,
		ContentType: memhop.ContentText, EventType: "tool_call", NodeSeq: step,
		Content: "grep retry_policy config/retry.yaml", CreatedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("append the bound event: %v", err)
	}
	topic, err := sess.Update(memhop.TurnEnd{Input: "重试策略在哪配", Output: "在 config/retry.yaml 的 retry_policy",
		Outcome: "answered", CreatedAt: time.Now().UnixMilli()})
	if err != nil {
		t.Fatalf("close the round: %v", err)
	}
	id := topic.ID

	all := readIDs(t, sess, memhop.L4Query{TopicID: &id})
	utterances := readIDs(t, sess, memhop.L4Query{TopicID: &id, Kind: ptr(memhop.KindUtterance)})
	events := readIDs(t, sess, memhop.L4Query{TopicID: &id, Kind: ptr(memhop.KindEvent)})
	// Exactly: the pair of dialogue lines, the event bound to the step, and the turn_outcome
	// the close recorded. Every assertion below picks its subset out of these four, so the
	// counts are pinned rather than sampled — a fixture that quietly grew a row would make the
	// partition test pass on its own.
	if len(all) != 4 || len(utterances) != 2 || len(events) != 2 {
		t.Fatalf("the fixture is not the shape this matrix assumes: all=%d utterances=%d events=%d",
			len(all), len(utterances), len(events))
	}
	// The unfiltered read spans both topics' records; the per-topic partitions must rejoin to
	// exactly its own subset, in its own order.
	if joined := append(append([]string{}, utterances...), events...); !sameSet(joined, within(all, utterances, events)) {
		t.Fatalf("the two Kind halves do not rejoin the unfiltered read: %v vs %v", joined, all)
	}

	// `IDs` alone: exactly what it names, no widening.
	if got := readIDs(t, sess, memhop.L4Query{IDs: []string{events[0]}}); len(got) != 1 || got[0] != events[0] {
		t.Fatalf("a single-id read answered %v, want exactly [%s]", got, events[0])
	}
	// `IDs` plus `Kind`: the condition still applies. This is the fast-path clause under test.
	if got := readIDs(t, sess, memhop.L4Query{IDs: all, Kind: ptr(memhop.KindEvent)}); !sameSet(got, events) {
		t.Fatalf("IDs+Kind answered %v, want the events %v — the id path ignored the companion filter", got, events)
	}
	// Same again with a keyword: the match is on stored text alone. The word chosen here lives
	// only in the event — "retry_policy" would not have worked, because this round's answer
	// line names it too, and a two-of-four answer would then prove nothing about the id path.
	if got := readIDs(t, sess, memhop.L4Query{IDs: all, Keyword: "grep"}); len(got) != 1 || got[0] != events[0] {
		t.Fatalf("IDs+Keyword answered %v, want exactly the event holding that word %s", got, events[0])
	}
	// A step's closure is how a host re-reads what one step did without pulling the whole turn
	// back. Only the event bound to it belongs here: the round's `turn_outcome` row carries no
	// step at all (NodeSeq 0 = attributed to nothing), so "every event of this turn" is not the
	// same set and must not come back as the answer.
	if got := readIDs(t, sess, memhop.L4Query{TopicID: &id, NodeSeq: step}); !sameSet(got, []string{events[0]}) {
		t.Fatalf("the step's closure answered %v, want only the event bound to it %s", got, events[0])
	}
	// An id that names nothing is "not selected", not an error, and it must not swallow the ids
	// beside it. Chosen non-zero on purpose: the reserved zero key is refused at the entry, which
	// is a different contract and is pinned where it belongs.
	if got := readIDs(t, sess, memhop.L4Query{IDs: []string{"7777777777777777", events[0]}}); len(got) != 1 ||
		got[0] != events[0] {
		t.Fatalf("a read naming one absent id answered %v, want the one live id", got)
	}
	if _, err := sess.SearchL4(memhop.L4Query{IDs: []string{"0000000000000000"}}); memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("the reserved zero id must be refused at the entry, got code %d (%v)", memhop.CodeOf(err), err)
	}
}

func readIDs(t *testing.T, sess *memhop.Session, q memhop.L4Query) []string {
	t.Helper()
	slots, err := sess.SearchL4(q)
	if err != nil {
		t.Fatalf("SearchL4(%+v): %v", q, err)
	}
	out := make([]string, 0, len(slots))
	for _, s := range slots {
		out = append(out, s.ID)
	}
	return out
}

// within returns `all` restricted to the ids named by the partitions, so the comparison is on
// the unfiltered read's own order rather than on whatever order two separate queries settled in.
func within(all []string, parts ...[]string) []string {
	want := map[string]bool{}
	for _, p := range parts {
		for _, id := range p {
			want[id] = true
		}
	}
	out := []string{}
	for _, id := range all {
		if want[id] {
			out = append(out, id)
		}
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
