// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// SearchL4's two time bounds compare against a record's own millisecond stamp. A bound in
// another unit is never the window it names: as Start, a seconds value sits below every
// stamp and lets everything through; as End, it sits below every stamp and excludes
// everything. The write boundary already refuses both scales; this is the same judgement on
// the read side, so the host gets an error instead of a result set it has to second-guess.

package test

import (
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

func TestInterfaceL4TimeBoundsRefuseTheWrongUnit(t *testing.T) {
	db, _ := openTestDB(t)
	if _, err := db.Search(memhop.SearchQuery{}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	stamp := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := db.AppendArchive(memhop.ArchiveInput{
		Kind: memhop.KindEvent, ContentType: memhop.ContentText, EventType: "tool_call",
		CreatedAt: stamp, Content: "the record a window should catch",
	}); err != nil {
		t.Fatalf("AppendArchive: %v", err)
	}
	if _, err := db.Update(memhop.TurnEnd{Input: "in", Output: "out", Outcome: "done",
		CreatedAt: stamp}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// The honest case: milliseconds in, the window answers.
	hits, err := db.SearchL4(memhop.L4Query{Start: stamp - 1000, End: stamp + 1000})
	if err != nil || len(hits) == 0 {
		t.Fatalf("a millisecond-bound window answered %d rows (err %v), want the record above", len(hits), err)
	}

	// A seconds-scale bound would match nothing; a microsecond-scale one would match
	// everything. Both are refused, and the reason says which scale it saw.
	for _, bad := range []struct {
		label string
		query memhop.L4Query
	}{
		{"start in seconds", memhop.L4Query{Start: stamp / 1000}},
		{"end in seconds", memhop.L4Query{End: stamp / 1000}},
		{"start in microseconds", memhop.L4Query{Start: stamp * 1000}},
		{"end in microseconds", memhop.L4Query{End: stamp * 1000}},
	} {
		got, err := db.SearchL4(bad.query)
		if err == nil {
			t.Fatalf("%s was answered with %d rows, want a refusal", bad.label, len(got))
		}
		if memhop.CodeOf(err) != memhop.ErrInvalidQuery {
			t.Fatalf("%s refused with code %d (%v), want ErrInvalidQuery", bad.label, memhop.CodeOf(err), err)
		}
		if !strings.Contains(err.Error(), "not milliseconds") {
			t.Fatalf("%s refused without naming the unit: %v", bad.label, err)
		}
	}

	// Below the seconds band a number is a relative counter, not a wrong unit, so it stays
	// a legitimate bound — this query simply matches nothing, which is what it says.
	if _, err := db.SearchL4(memhop.L4Query{End: 1000}); err != nil {
		t.Fatalf("a small relative bound was refused: %v", err)
	}
	// Zero is "this bound is unset" on a query, unlike on a write.
	if _, err := db.SearchL4(memhop.L4Query{Start: 0, End: 0}); err != nil {
		t.Fatalf("unset bounds were refused: %v", err)
	}
}
