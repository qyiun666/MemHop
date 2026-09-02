// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

func TestMarshalResultIDsAsHex(t *testing.T) {
	// uint64 IDs must serialize as 16-digit hex strings (JS hosts lose
	// precision on JSON numbers); timestamps and values stay numeric.
	type sample struct {
		ID        uint64   `json:"id"`
		SceneID   uint64   `json:"scene_id"`
		L4Refs    []uint64 `json:"l4_refs"`
		CreatedAt int64    `json:"created_at"`
		Score     float64  `json:"score"`
	}
	data, err := marshalResult(sample{
		ID: 0x506056d97468a833, SceneID: 0x77cf4d9fbc676640,
		L4Refs: []uint64{0xeccd7bd4d0db74cc}, CreatedAt: 1786987484275, Score: 0.5,
	})
	if err != nil {
		t.Fatalf("marshalResult: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["id"] != "506056d97468a833" {
		t.Errorf("id = %v, want hex string", got["id"])
	}
	if got["scene_id"] != "77cf4d9fbc676640" {
		t.Errorf("scene_id = %v, want hex string", got["scene_id"])
	}
	refs, ok := got["l4_refs"].([]any)
	if !ok || len(refs) != 1 || refs[0] != "eccd7bd4d0db74cc" {
		t.Errorf("l4_refs = %v, want hex array", got["l4_refs"])
	}
	if got["created_at"] != float64(1786987484275) {
		t.Errorf("created_at = %v, want numeric timestamp", got["created_at"])
	}
	if got["score"] != 0.5 {
		t.Errorf("score = %v, want numeric", got["score"])
	}
}

func TestMarshalResultZeroIDs(t *testing.T) {
	// Zero-value IDs (e.g. a fresh profile) must stay present and hex-shaped.
	data, err := marshalResult(map[string]any{"id_hash": uint64(0)})
	if err != nil {
		t.Fatalf("marshalResult: %v", err)
	}
	if string(data) != `{"id_hash":"0000000000000000"}` {
		t.Errorf("zero id = %s, want 0000000000000000", data)
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
