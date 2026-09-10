// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"fmt"
	"testing"
)

// Every id the library derives must name exactly one kind of record, because an
// id is all a caller hands back: no record type travels with it. Two layers of
// guarantee make that true — a derivation namespace per family, and the typed
// reader that refuses a frame of the wrong type (see
// TestTypedReadersRejectForeignRecordType). This is the first half, proved by
// exhaustion rather than by a case picked to pass: every derivation is run over
// one shared input space and no id may come out twice.
//
// The space deliberately reuses the same numbers across families (topic 7 as a
// turn's scene, as an L4 context, as a plan's owner), which is exactly the shape
// a collision would take if a namespace were missing.
func TestDerivedIdNamespacesAreDisjoint(t *testing.T) {
	scenes := []uint64{0, 1, 7, 42, 0xdeadbeef, 1 << 40}
	seqs := []uint64{0, 1, 2, 3, 10, 1000}
	stepSeqs := []uint32{0, 1, 2, 3, 10, 1000}
	times := []int64{0, 1, 2, 1000, 1_700_000_000_000, -1}

	families := map[string]func() []uint64{
		"turn": func() []uint64 {
			var out []uint64
			for _, s := range scenes {
				for _, q := range seqs {
					out = append(out, ComputeTurnTopicID(s, q))
				}
			}
			return out
		},
		"l4": func() []uint64 {
			var out []uint64
			for _, s := range scenes {
				for _, q := range seqs {
					out = append(out, HashContent(s, q))
				}
			}
			return out
		},
		"plan": func() []uint64 {
			var out []uint64
			for _, s := range scenes {
				for _, q := range stepSeqs {
					out = append(out, HashPlanNode(s, q))
				}
			}
			return out
		},
		"l1": func() []uint64 {
			var out []uint64
			for _, s := range scenes {
				out = append(out, SceneNodeID(s))
			}
			return out
		},
		// Dream's fused-topic key is the one derivation with no prefix; it
		// shares the turn family's (scene, number) space and must not land on
		// any of it.
		"fused": func() []uint64 {
			var out []uint64
			for _, s := range scenes {
				for _, a := range times {
					for _, b := range times {
						out = append(out, ComputeTopicID(s, a, b))
					}
				}
			}
			return out
		},
	}

	seen := make(map[uint64]string)
	for family, derive := range families {
		for i, id := range derive() {
			where := fmt.Sprintf("%s[%d]", family, i)
			if prior, ok := seen[id]; ok {
				t.Fatalf("id %016x derived by both %s and %s", id, prior, where)
			}
			seen[id] = where
		}
	}
	if len(seen) == 0 {
		t.Fatal("no ids derived; the input space is empty")
	}
}

// A derivation is only safe if distinct inputs give distinct ids within its own
// family too — otherwise the namespace keeps families apart while one of them
// aliases its own records.
func TestDerivedIdFamiliesAreInjective(t *testing.T) {
	check := func(name string, ids []uint64) {
		t.Helper()
		seen := make(map[uint64]int, len(ids))
		for i, id := range ids {
			if at, ok := seen[id]; ok {
				t.Fatalf("%s: input %d and input %d derive the same id %016x", name, at, i, id)
			}
			seen[id] = i
		}
	}

	var turns, contents, plans, nodes []uint64
	for s := range uint64(64) {
		for q := uint64(0); q < 64; q++ {
			turns = append(turns, ComputeTurnTopicID(s, q))
			contents = append(contents, HashContent(s, q))
		}
		for _, p := range []uint32{1, 2, 3, 10, 11, 70, 1000} {
			plans = append(plans, HashPlanNode(s, p))
		}
		nodes = append(nodes, SceneNodeID(s))
	}
	check("turn", turns)
	check("l4", contents)
	check("plan", plans)
	check("l1", nodes)
}
