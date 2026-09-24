// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"path/filepath"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

// `Start`/`End` filter the same creation time the cross-topic order sorts by, and the facade
// promises they are milliseconds — the unit a stored record's `CreatedAt` carries. Two ways to
// break that quietly: compare against a different clock than the one the read reports, or apply
// the window to a narrowed set in the wrong order. Neither shows up as an error.
//
// So the assertions here are structural rather than literal, which keeps them honest about a
// boundary the facade does not spell out (whether the ends are inclusive):
//
//   - a record is returned by a windowed read **exactly when** the timestamp that same read
//     reports for it falls inside the window — checked both ways, so a window wired to some
//     other clock fails on the first pass and a dropped predicate fails on the second;
//   - a window whose two ends are a record's own stamp must still return it, which is the
//     inclusive reading of the sentence, pinned by behaviour instead of by wording;
//   - and the window composes with `Kind` and `Limit` the way the filters do: window first as a
//     condition, the tail of what survives.
func TestInterfaceL4WindowIsTheClockTheReadReports(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "window.meh")
	m := openMockDB(t, path, llm.srv.URL)
	sess, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}

	base := time.Now().Add(-2 * time.Hour).UnixMilli()
	for i := 0; i < 4; i++ {
		if _, err := sess.Search(memhop.SearchQuery{NewScene: i == 0}); err != nil {
			t.Fatalf("open round %d: %v", i, err)
		}
		stamp := base + int64(i)*time.Minute.Milliseconds()
		_, err = sess.Update(memhop.TurnEnd{Input: "问一", Output: "答一",
			Outcome: "answered", CreatedAt: stamp})
		if err != nil {
			t.Fatalf("close round %d: %v", i, err)
		}
		if _, err := sess.AppendArchive(memhop.ArchiveInput{Kind: memhop.KindEvent,
			ContentType: memhop.ContentText, EventType: "tool_call",
			Content: "事件一", CreatedAt: stamp}); err != nil {
			t.Fatalf("append event %d: %v", i, err)
		}
	}

	all, err := sess.SearchL4(memhop.L4Query{})
	if err != nil {
		t.Fatalf("unfiltered read: %v", err)
	}
	if len(all) < 8 {
		t.Fatalf("the fixture holds %d records, want the four rounds' pairs: %+v", len(all), all)
	}
	// A window that names no record at all: an early cut before every stamp.
	before := memhop.L4Query{End: base - 1}
	if got, err := sess.SearchL4(before); err != nil || len(got) != 0 {
		t.Fatalf("a window ending before every record answered %d rows (err %v), want none", len(got), err)
	}

	// Each record's own stamp, used as a closed one-millisecond window, must select it.
	for _, r := range all {
		got, err := sess.SearchL4(memhop.L4Query{Start: r.CreatedAt, End: r.CreatedAt})
		if err != nil {
			t.Fatalf("window at %d: %v", r.CreatedAt, err)
		}
		found := false
		for _, w := range got {
			if w.ID == r.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("a window whose two ends are the record's own CreatedAt did not return it: %s at %d (window answered %d rows)",
				r.ID, r.CreatedAt, len(got))
		}
	}

	// The structural check, both directions, on a window that cuts the fixture in half.
	lo := base + 90*time.Second.Milliseconds()
	cut := base + 2*time.Minute.Milliseconds()
	windowed, err := sess.SearchL4(memhop.L4Query{Start: lo, End: cut})
	if err != nil {
		t.Fatalf("windowed read: %v", err)
	}
	inWindow := map[string]bool{}
	for _, w := range windowed {
		if w.CreatedAt < base || w.CreatedAt > cut {
			t.Fatalf("the window returned %s stamped %d, outside [%d,%d] — the predicate and the reported clock disagree",
				w.ID, w.CreatedAt, lo, cut)
		}
		inWindow[w.ID] = true
	}
	for _, r := range all {
		inside := r.CreatedAt >= lo && r.CreatedAt <= cut
		if inside != inWindow[r.ID] {
			t.Fatalf("record %s stamped %d is %v in [%d,%d] but the windowed read %v it: the filter is not applied to the clock the read reports",
				r.ID, r.CreatedAt, inside, lo, cut, map[bool]string{true: "returned", false: "skipped"}[inWindow[r.ID]])
		}
	}
	if len(windowed) == 0 || len(windowed) == len(all) {
		t.Fatalf("the window selected %d of %d records, so neither arm above was really exercised",
			len(windowed), len(all))
	}

	// Composition: window + Kind + Limit answers the tail of what the other two leave.
	events, err := sess.SearchL4(memhop.L4Query{Kind: ptr(memhop.KindEvent), Start: base, End: cut})
	if err != nil {
		t.Fatalf("windowed event read: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("the fixture leaves %d events inside the window, want at least 2 to test a limit", len(events))
	}
	limited, err := sess.SearchL4(memhop.L4Query{Kind: ptr(memhop.KindEvent), Start: base, End: cut, Limit: 2})
	if err != nil {
		t.Fatalf("windowed limited event read: %v", err)
	}
	for i := range limited {
		if limited[i].ID != events[len(events)-2+i].ID {
			t.Fatalf("window+Kind+Limit answered %v, want the tail of the same query unfiltered (%v)",
				[]string{limited[0].ID, limited[1].ID}, []string{events[len(events)-2].ID, events[len(events)-1].ID})
		}
	}
	// And a window whose bounds are second-scale is refused rather than answered as "nothing
	// fell in here" — the same unit rule the write boundary enforces, on the read side.
	if _, err := sess.SearchL4(memhop.L4Query{Start: 1_700_000_000}); err == nil {
		t.Fatal("a second-scale Start must be refused: compared against millisecond stamps it silently selects nothing")
	}
}
