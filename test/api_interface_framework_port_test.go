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

// An agent framework hands its memory organ a port of its own shape and expects it filled;
// meowire's is two methods over three small structs. This file declares that shape **here**,
// as a mirror — MemHop does not import the framework and the framework does not import
// MemHop, and neither should have to. The point of the mirror is the compile-time assertion
// below plus one measured question: how much does a host still have to convert?
//
// The answer the assertions give: an ordinal-free, id-free round trip, where the only fields
// that change representation are `Content` (a string on the memory side, bytes on the port
// side), `Kind`, and the outcome word — and the port's timestamps pass through untouched,
// because both sides already mean Unix milliseconds.
type portRecord struct {
	Key     string
	Kind    string
	Content []byte
	Created int64
}

type portQuery struct {
	CellID string
	Cue    string
}

type portFacts struct {
	CellID  string
	Input   string
	Output  string
	Outcome string // the framework's own typed value, read through its String()
}

type port interface {
	Recall(ctx context.Context, q portQuery) ([]portRecord, error)
	Remember(ctx context.Context, facts portFacts) error
}

// memhopPort is the whole adapter: one session handle, no id bookkeeping, no cache of scene
// or turn — the library holds those, so the adapter has nothing to keep in sync.
type memhopPort struct{ sess *memhop.Session }

var _ port = (*memhopPort)(nil)

func (p *memhopPort) Recall(ctx context.Context, q portQuery) ([]portRecord, error) {
	if _, err := p.sess.Search(memhop.SearchQuery{}); err != nil {
		return nil, err
	}
	scenes, err := p.sess.SceneContext("")
	if err != nil {
		return nil, err
	}
	var out []portRecord
	for _, row := range scenes.Topics {
		id := row.TopicID
		// The cue is not a retrieval key here: this engine keeps no relevance index, and a match
		// invented on it would be the adapter guessing. What the round holds is read back whole,
		// and `q.CellID` is the host's own name for a memory — this adapter was built for that
		// cell, so nothing is looked up by it either.
		slots, err := p.sess.SearchL4(memhop.L4Query{TopicID: &id})
		if err != nil {
			return nil, err
		}
		for _, s := range slots {
			out = append(out, portRecord{
				Key: s.ID, Kind: s.Kind.String(), Content: []byte(s.Content), Created: s.CreatedAt,
			})
		}
	}
	return out, nil
}

func (p *memhopPort) Remember(ctx context.Context, f portFacts) error {
	_, err := p.sess.Update(memhop.TurnEnd{Input: f.Input, Output: f.Output,
		Outcome: f.Outcome, CreatedAt: time.Now().UnixMilli()})
	return err
}

func TestInterfaceFrameworkPortShapesFitWithoutIdBookkeeping(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "port_shape.meh")
	m := openMockDB(t, path, llm.srv.URL)
	sess, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	p := &memhopPort{sess: sess}
	ctx := context.Background()

	// The framework's two timepoints are the whole protocol: Recall before every Think,
	// Remember once at the invocation's terminal point. Driven in that order the adapter keeps
	// no state of its own — and driven out of it, Remember answers "no turn is open", which is
	// the library refusing rather than guessing a round nobody opened.
	if first, err := p.Recall(ctx, portQuery{CellID: "cell-1", Cue: "端口"}); err != nil || len(first) != 0 {
		t.Fatalf("a recall on a memory with nothing said yet must answer empty, not fail: %d records, %v",
			len(first), err)
	}
	if err := p.Remember(ctx, portFacts{CellID: "cell-1", Input: "端口这一问", Output: "端口这一答",
		Outcome: "succeeded"}); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	records, err := p.Recall(ctx, portQuery{CellID: "cell-1", Cue: "端口"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	byKind := map[string]int{}
	var content string
	var created int64
	var key string
	for _, r := range records {
		byKind[r.Kind]++
		if string(r.Content) == "端口这一问" {
			content, created, key = string(r.Content), r.Created, r.Key
		}
	}
	if byKind["event"] != 1 {
		t.Fatalf("the port read carried %v — an adapter that answers only the dialogue lines "+
			"loses what a round did: %+v", byKind, records)
	}
	if byKind["utterance"] != 2 {
		t.Fatalf("the port read back %v, want the pair of utterances this round closed: %+v", byKind, records)
	}
	// A port `Kind` word is the same lowercase spelling the memory side prints, so the host
	// needs no table of its own to name a record.
	//
	// The timestamp crosses with no unit conversion at all: both sides mean milliseconds.
	if content == "" || created == 0 {
		t.Fatalf("the round's own text did not come back through the port: %q %d", content, created)
	}
	if created < 1e12 || created >= 1e14 {
		t.Fatalf("Created crossed as something other than Unix milliseconds: %d", created)
	}
	// The key is the record's own library-issued id — a host that keys its own table on it
	// still never constructs one.
	if len(key) != 16 || strings.TrimLeft(key, "0123456789abcdef") != "" {
		t.Fatalf("the port's Key is not the hex id the library issued: %q", key)
	}

	// The outcome word survives the one typed-value conversion the port needs and lands as an
	// event, so "how did the invocation end" is queryable, not lost in a return value.
	events, err := sess.SearchL4(memhop.L4Query{Kind: ptr(memhop.KindEvent)})
	if err != nil {
		t.Fatalf("SearchL4 events: %v", err)
	}
	found := false
	for _, e := range events {
		if e.EventType == "turn_outcome" && e.Content == "succeeded" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the outcome word the port handed over did not survive into the event track: %+v", events)
	}
}
