// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
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
	slot := memhop.SceneSlot{SceneID: "000000000000000f", L3ID: "77cf4d9fbc676640", TopicCount: 2, LastHitAt: 1786987484275}
	got := okResult(slot)
	if len(got.Content) != 1 {
		t.Fatalf("content = %+v", got.Content)
	}
	text := got.Content[0].(*mcp.TextContent).Text
	want := `{"scene_id":"000000000000000f","scene_name":"","topic_count":2,"hit_count":0,"last_hit_at":1786987484275,"l3_id":"77cf4d9fbc676640"}`
	if text != want {
		t.Fatalf("result = %s, want %s", text, want)
	}
}

// contentTypeNames is the one vocabulary this package states by hand, so it is
// where a new engine content type would silently go missing: enumerate the
// defined values through String() instead of restating the list.
func TestContentTypeNamesMatchTheEngine(t *testing.T) {
	defined := map[string]bool{}
	for i := 0; i <= math.MaxUint8; i++ {
		name := memhop.ContentType(i).String()
		if strings.HasPrefix(name, "ContentType(") {
			continue // not a defined value
		}
		defined[name] = true
	}
	if len(defined) == 0 {
		t.Fatal("no content type resolved — the unknown-value shape changed")
	}
	for name := range defined {
		if _, ok := contentTypeNames[name]; !ok {
			t.Errorf("engine defines content type %q but memhop_archive_search cannot name it", name)
		}
	}
	for name := range contentTypeNames {
		if !defined[name] {
			t.Errorf("contentTypeNames accepts %q, which the engine does not define", name)
		}
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
	out, isErr := call(t, h, `{"scene_id":"a1b2c3d4e5f67890","user_text":"u","user_ts":1,"agent_text":"a","agent_ts":2}`)
	if !isErr {
		t.Fatal("expected error result")
	}
	if out != "boom" {
		t.Errorf("error text mismatch: %q", out)
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
