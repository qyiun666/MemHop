// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The vocabularies on this surface come in two wire forms: four enums travel as their
// numbers (content medium, archive kind, utterance speaker, L3 edge kind) and two as words
// (a plan step's status, an import's conflict mode). A host writing a tool schema for a
// model has to know which is which and what the whole set of values is, and it has to be
// able to trust that the guide still says so — so the tables below are checked against the
// code and against both guides, one place each.

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
		"ArchiveRole": {
			{"RoleUser", "user", "0"}, {"RoleAgent", "agent", "1"}, {"RoleSystem", "system", "2"},
			{"RoleDream", "dream", "3"},
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

	// Every vocabulary above has to hold a row of the guides' own table, carrying every
	// value: the table is the host's answer for a tool schema. Probing the document for
	// the words is not that check — `dream` also lives inside the `memory_dream` tool
	// name, so a table that lost a row could still read as complete.
	wordRows := map[string][]string{
		"status": {"in_progress", "done", "failed"},
		"mode":   {"skip", "merge", "overwrite"},
	}
	for _, path := range []string{"../INTEGRATION_GUIDE.md", "../INTEGRATION_GUIDE.zh.md"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		guideRows := enumTableRows(t, string(raw))
		for enum, rows := range tables {
			cell, listed := guideRows[enum]
			if !listed {
				t.Errorf("%s vocabulary table has no %s row", path, enum)
				continue
			}
			for _, r := range rows {
				if !strings.Contains(cell, "`"+r.number+"`") || !strings.Contains(cell, r.value) {
					t.Errorf("%s: the %s row does not carry %s = %q over the wire as %s",
						path, enum, r.name, r.value, r.number)
				}
			}
		}
		for field, words := range wordRows {
			cell, listed := guideRows[field]
			if !listed {
				t.Errorf("%s vocabulary table has no %s row", path, field)
				continue
			}
			for _, w := range words {
				if !strings.Contains(cell, "`"+w+"`") {
					t.Errorf("%s: the %s row no longer spells the value %q", path, field, w)
				}
			}
		}
	}
}

// enumTableRows reads the guide's vocabulary table — the one after the `enum-wire-table`
// marker — into its first-column name mapped to the cell listing its values.
func enumTableRows(tb testing.TB, text string) map[string]string {
	tb.Helper()
	at := strings.Index(text, "`enum-wire-table`")
	if at < 0 {
		tb.Fatal("the guide no longer marks its vocabulary table")
	}
	rows := map[string]string{}
	for _, line := range strings.Split(text[at:], "\n") {
		if !strings.HasPrefix(line, "|") {
			if len(rows) > 0 {
				break
			}
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 4 {
			continue
		}
		if name := firstBackticked(cells[1]); name != "" {
			rows[name] = cells[3]
		}
	}
	if len(rows) == 0 {
		tb.Fatal("no table rows follow the vocabulary marker")
	}
	return rows
}

func firstBackticked(cell string) string {
	open := strings.Index(cell, "`")
	if open < 0 {
		return ""
	}
	rest := cell[open+1:]
	end := strings.Index(rest, "`")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// enumValue reads one named constant of one of the four numeric enums, so the table
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
	case "ArchiveRole.RoleUser":
		return RoleUser
	case "ArchiveRole.RoleAgent":
		return RoleAgent
	case "ArchiveRole.RoleSystem":
		return RoleSystem
	case "ArchiveRole.RoleDream":
		return RoleDream
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
	case "ArchiveRole":
		return ArchiveRole(4)
	case "GraphEdgeKind":
		return GraphEdgeKind(6)
	}
	tb.Fatalf("no top value defined for %s", enum)
	return nil
}
