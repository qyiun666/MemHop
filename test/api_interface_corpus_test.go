// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

// What the recall read owes a host is not a judgment call: after the corpus goes in, every
// settled round must come back — same set, same order, same bytes. QA accuracy over these
// fixtures needs a real model (the answers are derived, not quoted), so this pins the half
// that does not: nothing dropped, nothing reordered, nothing altered on the way through the
// log, and the round a cue points at found by the rule the host's recall uses (substring over
// the round's own prose). It runs on the offline stub, so it costs no quota and can gate a
// change to the write or read path.
func TestInterfaceCorpusRoundTripsVerbatim(t *testing.T) {
	sample := loadLocomoSample(t, 0)
	db := openMockDB(t, filepath.Join(t.TempDir(), "corpus.meh"), newMockLLM(t).srv.URL,
		func(d *memhop.MemHopDefaults) { d.SceneDreamTopicThreshold = -1 })
	sess, err := db.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}

	stamp := time.Now().Add(-24 * time.Hour).UnixMilli()
	sessions := 0
	for _, sc := range sample.Sessions {
		rounds := pairedRounds(t, sc.Turns)
		if len(rounds) == 0 {
			continue
		}
		sessions++
		for i, r := range rounds {
			if _, err := sess.Search(memhop.SearchQuery{NewScene: i == 0}); err != nil {
				t.Fatalf("session %s round %d: Search: %v", sc.ID, i, err)
			}
			stamp += 1000
			if _, err := sess.Update(memhop.TurnEnd{Input: r.user, Output: r.agent,
				Outcome: "answered", CreatedAt: stamp}); err != nil {
				t.Fatalf("session %s round %d: Update: %v", sc.ID, i, err)
			}
		}

		read, err := sess.SceneContext("")
		if err != nil {
			t.Fatalf("session %s: SceneContext: %v", sc.ID, err)
		}
		if read.SceneName == "" {
			t.Fatalf("session %s: the scene read named no scene", sc.ID)
		}
		if len(read.Topics) != len(rounds) {
			t.Fatalf("session %s: the scene read listed %d rows for %d settled rounds",
				sc.ID, len(read.Topics), len(rounds))
		}
		for i, row := range read.Topics {
			want := []string{rounds[i].user, rounds[i].agent}
			if len(row.Messages) != 2 {
				t.Fatalf("session %s round %d: %d messages, want the pair the round closed with: %+v",
					sc.ID, i, len(row.Messages), row.Messages)
			}
			for j, m := range row.Messages {
				if m.Content != want[j] {
					t.Fatalf("session %s round %d message %d came back altered:\n got: %q\nwant: %q",
						sc.ID, i, j, m.Content, want[j])
				}
			}
			if len(row.Keywords) == 0 {
				t.Fatalf("session %s round %d settled with no keyword track: %+v", sc.ID, i, row)
			}
			if row.TopicID == "" {
				t.Fatalf("session %s round %d came back with no addressable topic id", sc.ID, i)
			}
			if i > 0 && row.UserTimestamp < read.Topics[i-1].UserTimestamp {
				t.Fatalf("session %s: rows are out of speaking order at %d (%d < %d)",
					sc.ID, i, row.UserTimestamp, read.Topics[i-1].UserTimestamp)
			}
		}

		// The host's recall rule over the round's own prose: a cue lifted from the middle of
		// the session must land on that round, on this row and no other.
		mid := len(rounds) / 2
		cue := rounds[mid].user
		hits := 0
		for i, row := range read.Topics {
			prose := row.Messages[0].Content + " " + row.Messages[1].Content
			if strings.Contains(prose, cue) {
				hits++
				if i != mid {
					t.Fatalf("session %s: the cue from round %d matched row %d instead", sc.ID, mid, i)
				}
			}
		}
		if hits != 1 {
			t.Fatalf("session %s: the cue matched %d rows, want exactly 1", sc.ID, hits)
		}
	}
	if sessions < 3 {
		t.Fatalf("the fixture yielded only %d sessions with rounds; the loader or the pairing is wrong", sessions)
	}
	t.Logf("corpus: %d sessions of sample %s round-tripped verbatim through the loop", sessions, sample.SampleID)
}

type corpusTurn struct {
	Text    string `json:"text"`
	Speaker string `json:"speaker"`
}

type corpusSession struct {
	ID    string       `json:"id"`
	Turns []corpusTurn `json:"turns"`
}

type corpusSample struct {
	SampleID string          `json:"sample_id"`
	Sessions []corpusSession `json:"sessions"`
}

type round struct{ user, agent string }

func loadLocomoSample(tb testing.TB, n int) corpusSample {
	tb.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "benches", "fixtures", "locomo10.json"))
	if err != nil {
		tb.Fatalf("read fixture: %v", err)
	}
	var fx struct {
		Items []corpusSample `json:"items"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		tb.Fatalf("parse fixture: %v", err)
	}
	if n >= len(fx.Items) {
		tb.Fatalf("fixture has %d samples, asked for index %d", len(fx.Items), n)
	}
	return fx.Items[n]
}

// pairedRounds folds a session's alternating lines into the library's round shape: one
// settled round per (asked, answered) pair, which is what `Update` closes. A trailing
// unpaired line is left out — a round needs both sides.
func pairedRounds(tb testing.TB, turns []corpusTurn) []round {
	tb.Helper()
	out := make([]round, 0, len(turns)/2)
	for i := 0; i+1 < len(turns); i += 2 {
		if strings.TrimSpace(turns[i].Text) == "" || strings.TrimSpace(turns[i+1].Text) == "" {
			tb.Skipf("fixture line is blank; the write boundary refuses it")
		}
		out = append(out, round{user: turns[i].Text, agent: turns[i+1].Text})
	}
	return out
}
