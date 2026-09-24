// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"os"
	"regexp"
	"testing"
)

// The vocabularies on this surface come in two wire forms: three enums travel as their
// numbers (content medium, archive kind, L3 edge kind) and two as words (a plan step's
// status, an import's conflict mode). A host writing a tool schema for a model has to know
// which is which and what the whole set of values is, and it has to be able to trust that
// the guide still says so — so the tables below are checked against the code and against
// both guides, one place each.

var snakeKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func TestStringEnumsSpellTheirValuesLowercase(t *testing.T) {
	for _, pair := range []struct {
		kind  string
		value string
	}{
		{"PlanStatus", string(PlanStatusInProgress)},
		{"PlanStatus", string(PlanStatusDone)},
		{"PlanStatus", string(PlanStatusFailed)},
		{"L3ImportMode", string(L3ImportSkip)},
		{"L3ImportMode", string(L3ImportMerge)},
		{"L3ImportMode", string(L3ImportOverwrite)},
	} {
		if !snakeKey.MatchString(pair.value) {
			t.Errorf("%s carries the value %q: every string value on this surface is lowercase with underscores", pair.kind, pair.value)
		}
	}
}

func TestEnumVocabulariesAreWhatTheGuidesSay(t *testing.T) {
	type row struct {
		name   string
		value  string // how it reads through String()
		number string // its numeric value, for the three numeric enums
	}
	tables := map[string][]row{
		"ContentType": {
			{"ContentText", "text", "0"}, {"ContentImage", "image", "1"}, {"ContentVideo", "video", "2"},
			{"ContentDocument", "document", "3"}, {"ContentAudio", "audio", "4"}, {"ContentCode", "code", "5"},
			{"ContentOther", "other", "255"},
		},
		"ArchiveKind": {
			{"KindUtterance", "utterance", "0"}, {"KindEvent", "event", "1"},
		},
		"GraphEdgeKind": {
			{"EdgeRelated", "related", "0"}, {"EdgeCausal", "causal", "1"}, {"EdgePartOf", "part_of", "2"},
			{"EdgeSequence", "sequence", "3"}, {"EdgeDependency", "dependency", "4"}, {"EdgeCustom", "custom", "5"},
		},
	}
	for enum, rows := range tables {
		for _, r := range rows {
			got := enumValue(t, enum, r.name)
			if got.String() != r.value {
				t.Errorf("%s.%s prints %q, want %q", enum, r.name, got.String(), r.value)
			}
			if !got.Valid() {
				t.Errorf("%s.%s reports itself undefined, so the vocabulary table and Valid() disagree", enum, r.name)
			}
			wire := encode(t, enum+"."+r.name, got)
			if wire != r.number {
				t.Errorf("%s.%s encodes as %s, want the number %s a tool schema has to promise",
					enum, r.name, wire, r.number)
			}
		}
		// One value past the table's top has to be refused rather than ignored, or a
		// model's invented word becomes a filter that quietly matches nothing.
		if last := enumMax(t, enum); last.Valid() {
			t.Errorf("%s still validates one past its defined set (%v)", enum, last)
		}
	}

	// Every name above has to appear in both guides: the table is the host's answer for
	// a tool schema, and a guide that dropped a word is a guide that no longer answers.
	for _, path := range []string{"../INTEGRATION_GUIDE.md", "../INTEGRATION_GUIDE.zh.md"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(raw)
		for _, word := range []string{"in_progress", "done", "failed", "skip", "merge", "overwrite",
			"utterance", "text", "image", "video", "document", "audio", "code", "other",
			"part_of", "sequence", "dependency", "causal", "custom"} {
			// Whole words only: `merge` inside the identifier `L3ImportMerge` is a Go
			// name, not a host being told what value to send.
			if !regexp.MustCompile(`\b` + word + `\b`).MatchString(text) {
				t.Errorf("%s no longer names the value %q as a word", path, word)
			}
		}
	}
}

// enumValue reads one named constant of one of the three numeric enums, so the table
// above stays the only place their values are written down.
func enumValue(tb testing.TB, enum, name string) interface {
	String() string
	Valid() bool
} {
	tb.Helper()
	switch enum + "." + name {
	case "ContentType.ContentText":
		return ContentText
	case "ContentType.ContentImage":
		return ContentImage
	case "ContentType.ContentVideo":
		return ContentVideo
	case "ContentType.ContentDocument":
		return ContentDocument
	case "ContentType.ContentAudio":
		return ContentAudio
	case "ContentType.ContentCode":
		return ContentCode
	case "ContentType.ContentOther":
		return ContentOther
	case "ArchiveKind.KindUtterance":
		return KindUtterance
	case "ArchiveKind.KindEvent":
		return KindEvent
	case "GraphEdgeKind.EdgeRelated":
		return EdgeRelated
	case "GraphEdgeKind.EdgeCausal":
		return EdgeCausal
	case "GraphEdgeKind.EdgePartOf":
		return EdgePartOf
	case "GraphEdgeKind.EdgeSequence":
		return EdgeSequence
	case "GraphEdgeKind.EdgeDependency":
		return EdgeDependency
	case "GraphEdgeKind.EdgeCustom":
		return EdgeCustom
	}
	tb.Fatalf("no such constant in the table: %s.%s", enum, name)
	return nil
}

// enumMax hands back one value past each enum's defined set, for the refusal check.
func enumMax(tb testing.TB, enum string) interface {
	String() string
	Valid() bool
} {
	tb.Helper()
	switch enum {
	case "ContentType":
		return ContentType(6)
	case "ArchiveKind":
		return ArchiveKind(2)
	case "GraphEdgeKind":
		return GraphEdgeKind(6)
	}
	tb.Fatalf("no top value defined for %s", enum)
	return nil
}
