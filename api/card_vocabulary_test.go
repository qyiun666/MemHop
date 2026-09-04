// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The built-in cards are the manuals an LLM reads before it calls anything, and
// they enumerate value sets in prose ("kind is related|causal|…",
// "Type (text|image|…)"). A set the engine grew or retired without a card edit
// turns that prose into a lie, so this walks the enums instead of restating
// them.

package api

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/capabilities"
	"github.com/qyiun666/MemHop/internal"
)

func definedEnumNames[T ~uint8](unknown string, name func(T) string) []string {
	var out []string
	for i := 0; i <= math.MaxUint8; i++ {
		s := name(T(i))
		if strings.HasPrefix(s, unknown) {
			continue // not a defined value
		}
		out = append(out, s)
	}
	return out
}

// cardEntry returns one resource's desc from a built-in card.
func cardEntry(t *testing.T, card, resource string) string {
	t.Helper()
	data, err := capabilities.FS.ReadFile(card + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var parsed internal.CapabilityImport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse %s: %v", card, err)
	}
	for _, r := range parsed.Resources {
		if r.Name == resource {
			return r.Desc
		}
	}
	t.Fatalf("%s has no resource %q", card, resource)
	return ""
}

func TestEdgeKindVocabularyMatchesTheEngine(t *testing.T) {
	desc := cardEntry(t, "memhop-knowledge", "ImportL3")
	kinds := definedEnumNames("GraphEdgeKind(", func(k internal.GraphEdgeKind) string { return k.String() })
	if len(kinds) == 0 {
		t.Fatal("no edge kind resolved")
	}
	for _, kind := range kinds {
		if !strings.Contains(desc, kind) {
			t.Errorf("memhop-knowledge ImportL3 does not name edge kind %q", kind)
		}
	}
}

func TestContentTypeVocabularyMatchesTheEngine(t *testing.T) {
	desc := cardEntry(t, "memhop-archive", "SearchL4")
	types := definedEnumNames("ContentType(", func(c internal.ContentType) string { return c.String() })
	if len(types) == 0 {
		t.Fatal("no content type resolved")
	}
	for _, ct := range types {
		if !strings.Contains(desc, ct) {
			t.Errorf("memhop-archive SearchL4 does not name content type %q", ct)
		}
	}
}
