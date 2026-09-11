// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package llmops

import (
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

// A reply is JSON the model may wrap in a fence, and the two id fields come back
// as numbers or as quoted strings depending on the model — both shapes have to
// parse, since a group that fails to parse is a merge the scene never gets.
func TestParseConsolidateResponseAcceptsBothIDShapes(t *testing.T) {
	out, err := parseConsolidateResponse("```json\n" +
		`{"l2_groups":[{"scene_id":"7","node_hashes":[11,"22"],"merged_summary":"合并"}],` +
		`"l2_compression_needed":true}` + "\n```")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !out.L2CompressionNeeded || len(out.L2Groups) != 1 {
		t.Fatalf("output = %+v", out)
	}
	g := out.L2Groups[0]
	if g.SceneID != 7 || len(g.NodeHashes) != 2 || g.NodeHashes[0] != 11 || g.NodeHashes[1] != 22 {
		t.Fatalf("group = %+v, want scene 7 holding nodes 11 and 22", g)
	}
	if g.MergedSummary != "合并" {
		t.Fatalf("summary = %q", g.MergedSummary)
	}
}

// An id that cannot be parsed is an error rather than a dropped group, and a
// reply that is not JSON at all is an error rather than an empty merge list:
// either one silently thinned would read exactly like a model that merged less.
func TestParseConsolidateResponseRefusesWhatItCannotUse(t *testing.T) {
	for _, reply := range []string{
		`{"l2_groups":[{"scene_id":"not-a-number","node_hashes":[1]}]}`,
		`{"l2_groups":[{"scene_id":7,"node_hashes":["nope"]}]}`,
		`{"l2_groups":`,
		`I merged everything, sorry about the JSON`,
	} {
		if _, err := parseConsolidateResponse(reply); common.CodeOf(err) != common.ErrLLM {
			t.Fatalf("%q: want ErrLLM, got %v", reply, err)
		}
	}
}

// The MBTI type word is re-derived from the four dimensions, so a reply whose
// type contradicts its own numbers does not get to keep it. The dimensions
// themselves are clamped: an out-of-range signal would otherwise flow straight
// into the profile record.
func TestParseDistillResponseDerivesTypeAndClamps(t *testing.T) {
	out, err := parseDistillResponse(`{"emotion":{"valence":4,"arousal":-2,"dominance":0.6},` +
		`"mbti":{"i_e":-9,"n_s":0.2,"t_f":-0.3,"j_p":0.1,"type":"ZZZZ"},` +
		`"personality":"  务实直接  ","per_node":[]}`)
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

// A personality past the cap is cut to it rather than refused: the summary goes
// into the profile record and into every later prompt, so the cap is what keeps
// one verbose reply from inflating both.
func TestParseDistillResponseCapsPersonality(t *testing.T) {
	long := strings.Repeat("话", distillPersonalityMaxRunes+40)
	out, err := parseDistillResponse(`{"emotion":{},"mbti":{},"personality":"` + long + `","per_node":[]}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := len([]rune(out.Personality)); got != distillPersonalityMaxRunes {
		t.Fatalf("personality is %d runes, want the cap %d", got, distillPersonalityMaxRunes)
	}
}

// Per-node rows carry the id they belong to as hex; a row whose id will not
// parse is dropped, because writing it would attribute one node's emotion to
// whatever the hex happened to collide with.
func TestParseDistillResponseSkipsUnparsableNodeRows(t *testing.T) {
	out, err := parseDistillResponse(`{"emotion":{},"mbti":{},"personality":"",` +
		`"per_node":[{"id_hex":"0000000000000001","valence":0.5,"arousal":0.5},` +
		`{"id_hex":"nonsense","valence":0.9,"arousal":0.9}]}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out.PerNode) != 1 || out.PerNode[0].IDHex != "0000000000000001" {
		t.Fatalf("per_node = %+v, want only the row with a parsable id", out.PerNode)
	}
}

func TestParseDistillResponseRefusesNonJSON(t *testing.T) {
	if _, err := parseDistillResponse("the agent seems cheerful"); common.CodeOf(err) != common.ErrLLM {
		t.Fatalf("want ErrLLM, got %v", err)
	}
}
