// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

// call drives a ToolHandler with JSON args and returns the text content.
func call(t *testing.T, h mcp.ToolHandler, args string) (string, bool) {
	t.Helper()
	var raw json.RawMessage
	if args != "" {
		raw = json.RawMessage(args)
	}
	res, err := h(context.Background(), &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: raw},
	})
	if err != nil {
		t.Fatalf("handler returned transport error: %v", err)
	}
	if len(res.Content) == 0 {
		return "", res.IsError
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", res.Content[0])
	}
	return text.Text, res.IsError
}

func TestHandleDecodesArgs(t *testing.T) {
	h := handle[searchArgs, searchArgs](func(a searchArgs) (searchArgs, error) { return a, nil })
	out, isErr := call(t, h, `{"scene_id":"a1b2c3d4e5f67890","l3_id":"000000000000000f"}`)
	if isErr {
		t.Fatalf("unexpected error result: %s", out)
	}
	var got searchArgs
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got.SceneID != "a1b2c3d4e5f67890" || got.L3ID != "000000000000000f" {
		t.Errorf("args not round-tripped: %+v", got)
	}
}

func TestHandleInvalidArgs(t *testing.T) {
	h := handle[searchArgs, searchArgs](func(a searchArgs) (searchArgs, error) { return a, nil })
	out, isErr := call(t, h, `{"scene_id":123}`) // type mismatch: scene_id must be string
	if !isErr {
		t.Fatalf("expected error result, got %s", out)
	}
}

// The id contract is rendered by the api DTOs (api/surface_public_test.go
// rejects a numeric id in any host-visible signature), so a tool result is a
// straight serialization of the DTO — including a zero id, which stays
// hex-shaped rather than disappearing as a number.
func TestOkResultCarriesHexIDStrings(t *testing.T) {
	slot := memhop.SceneSlot{SceneID: "000000000000000f", L3ID: "77cf4d9fbc676640"}
	got := okResult(slot)
	if len(got.Content) != 1 {
		t.Fatalf("content = %+v", got.Content)
	}
	text := got.Content[0].(*mcp.TextContent).Text
	want := `{"scene_id":"000000000000000f","scene_name":"","l3_id":"77cf4d9fbc676640"}`
	if text != want {
		t.Fatalf("result = %s, want %s", text, want)
	}
}

// statedNames is the hand-stated half of a vocabulary, reduced to its names.
func statedNames[T any](m map[string]T) map[string]bool {
	out := make(map[string]bool, len(m))
	for name := range m {
		out[name] = true
	}
	return out
}

// This package states four vocabularies by hand: the content types, the two archive
// kinds, the six edge kinds and the roles a host may name. Each is a place where a
// value the engine defines would silently go missing — the tool would refuse a name
// the library accepts, and no test would notice. The three the engine can enumerate
// are checked against its own String() in both directions; roles have no engine table
// to enumerate, so that one runs the other way: this face may name exactly what the
// public face exports, which is what keeps role 3 — the library's own mark on a fused
// group's summary, exported under no name at all — out of a host's reach.
func TestStatedVocabulariesMatchTheEngine(t *testing.T) {
	defined := func(typeName string, name func(uint8) string) map[string]bool {
		out := map[string]bool{}
		for i := 0; i <= math.MaxUint8; i++ {
			n := name(uint8(i))
			if strings.HasPrefix(n, typeName+"(") {
				continue // not a defined value
			}
			out[n] = true
		}
		if len(out) == 0 {
			t.Fatalf("%s: no value resolved — the unknown-value shape changed", typeName)
		}
		return out
	}
	for _, tc := range []struct {
		vocab           string
		stated, defined map[string]bool
	}{
		{"content type", statedNames(contentTypeNames),
			defined("ContentType", func(i uint8) string { return memhop.ContentType(i).String() })},
		{"archive kind", statedNames(kindNames),
			defined("ArchiveKind", func(i uint8) string { return memhop.ArchiveKind(i).String() })},
		{"edge kind", statedNames(edgeKindNames),
			defined("GraphEdgeKind", func(i uint8) string { return memhop.GraphEdgeKind(i).String() })},
	} {
		for name := range tc.defined {
			if !tc.stated[name] {
				t.Errorf("the engine defines %s %q but this package states no name for it", tc.vocab, name)
			}
		}
		for name := range tc.stated {
			if !tc.defined[name] {
				t.Errorf("this package names %s %q, which the engine does not define", tc.vocab, name)
			}
		}
	}
	if !maps.Equal(roleNames, map[string]uint8{
		"user": memhop.RoleUser, "agent": memhop.RoleAgent, "system": memhop.RoleSystem,
	}) {
		t.Errorf("roleNames = %v, want exactly the three roles the public face exports", roleNames)
	}
}

func TestHandleEmptyArgs(t *testing.T) {
	h := handle[searchArgs, searchArgs](func(a searchArgs) (searchArgs, error) { return a, nil })
	if _, isErr := call(t, h, ""); isErr {
		t.Fatal("empty arguments should decode to zero value")
	}
	if _, isErr := call(t, h, "null"); isErr {
		t.Fatal("null arguments should decode to zero value")
	}
}

func TestHandlePropagatesError(t *testing.T) {
	sentinel := errors.New("boom")
	h := handle[updateArgs, updateResult](func(a updateArgs) (updateResult, error) {
		return updateResult{}, sentinel
	})
	out, isErr := call(t, h, `{"scene_id":"a1b2c3d4e5f67890","topic_id":"0000000000000001"}`)
	if !isErr {
		t.Fatal("expected error result")
	}
	if out != "boom" {
		t.Errorf("error text mismatch: %q", out)
	}
}

// A failed Dream still hands back the report of what it did before the stage
// that failed, so the tool must not drop it: content[0] stays the error text and
// the result JSON rides along behind it. The request context has to reach the
// handler on the way, since a Dream that outlived its client would otherwise hold
// the domain lock through the rest of the pipeline.
func TestHandlePartialKeepsResultAlongsideError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := handlePartial[dreamArgs, dreamResult](func(ctx context.Context, _ dreamArgs) (dreamResult, error) {
		if ctx.Err() == nil {
			t.Error("the cancelled request context never reached the handler")
		}
		return dreamResult{
			Consolidated: true,
			Report:       &memhop.DreamReport{L2TopicsCompressed: 7},
		}, errors.New("dream: LLM consolidation failed for all scenes")
	})
	res, err := h(ctx, &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{"scene_id":""}`)},
	})
	if err != nil {
		t.Fatalf("handler returned transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("a failed Dream must still report as a tool error")
	}
	if len(res.Content) != 2 {
		t.Fatalf("want error text plus the report, got %d content blocks", len(res.Content))
	}
	first, ok := res.Content[0].(*mcp.TextContent)
	if !ok || !strings.HasPrefix(first.Text, "dream: ") {
		t.Fatalf("first content block is not the error text: %#v", res.Content[0])
	}
	second, ok := res.Content[1].(*mcp.TextContent)
	if !ok {
		t.Fatalf("second content block is not text: %#v", res.Content[1])
	}
	var got dreamResult
	if err := json.Unmarshal([]byte(second.Text), &got); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if got.Report == nil || got.Report.L2TopicsCompressed != 7 {
		t.Fatalf("the partial report did not survive: %s", second.Text)
	}
}

func TestHandleNoArgs(t *testing.T) {
	h := handleNoArgs[statusResult](func() (statusResult, error) {
		return statusResult{Closed: true, SceneCount: 3}, nil
	})
	out, isErr := call(t, h, "")
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	var got statusResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Closed || got.SceneCount != 3 {
		t.Errorf("result mismatch: %+v", got)
	}
}

func TestParseImportMode(t *testing.T) {
	for _, valid := range []string{"Skip", "Merge", "Overwrite"} {
		if _, err := parseImportMode(valid); err != nil {
			t.Errorf("parseImportMode(%q): %v", valid, err)
		}
	}
	if _, err := parseImportMode("bogus"); err == nil {
		t.Error("expected error for unknown import mode")
	}
}

func TestParseEdgeKinds(t *testing.T) {
	for _, valid := range []string{"related", "causal", "part_of", "sequence", "dependency", "custom"} {
		if _, err := parseEdgeKinds([]string{valid}); err != nil {
			t.Errorf("parseEdgeKinds(%q): %v", valid, err)
		}
	}
	if _, err := parseEdgeKinds([]string{"bogus"}); err == nil {
		t.Error("expected error for unknown edge kind")
	}
	if kinds, err := parseEdgeKinds(nil); err != nil || kinds != nil {
		t.Errorf("empty edge kinds: got %v, %v; want nil, nil", kinds, err)
	}
}
