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

// call invokes a ToolHandler with the given JSON arguments and returns the
// parsed text content of the result.
func call(t *testing.T, h mcp.ToolHandler, args string) (string, bool) {
	t.Helper()
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(args)},
	}
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler protocol error: %v", err)
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
	out, isErr := call(t, h, `{"text":"hi","timestamp":1700000000000,"auto_create":true}`)
	if isErr {
		t.Fatalf("unexpected error result: %s", out)
	}
	var got searchArgs
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got.Text != "hi" || got.Timestamp != 1700000000000 || !got.AutoCreate {
		t.Errorf("args not round-tripped: %+v", got)
	}
}

func TestHandleInvalidArgs(t *testing.T) {
	h := handle[searchArgs, searchArgs](func(a searchArgs) (searchArgs, error) { return a, nil })
	out, isErr := call(t, h, `{"text":123}`) // type mismatch: text must be string
	if !isErr {
		t.Fatalf("expected error result, got %s", out)
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
	out, isErr := call(t, h, `{"topic_id":"a1b2c3d4e5f67890","text":"x","timestamp":1}`)
	if !isErr {
		t.Fatal("expected error result")
	}
	if out != "boom" {
		t.Errorf("error text mismatch: %q", out)
	}
}

func TestHandleNoArgs(t *testing.T) {
	h := handleNoArgs[statusResult](func() (statusResult, error) {
		return statusResult{Closed: true, HasActiveScenes: true}, nil
	})
	out, isErr := call(t, h, "")
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	var got statusResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Closed || !got.HasActiveScenes {
		t.Errorf("result mismatch: %+v", got)
	}
}

func TestParseEdgeKind(t *testing.T) {
	for _, valid := range []string{"related", "causal", "part_of", "sequence", "dependency", "custom"} {
		if _, err := parseEdgeKind(valid); err != nil {
			t.Errorf("parseEdgeKind(%q): %v", valid, err)
		}
	}
	if _, err := parseEdgeKind("bogus"); err == nil {
		t.Error("expected error for unknown edge kind")
	}
}

func TestParseHexID(t *testing.T) {
	v, err := parseHexID("a1b2c3d4e5f67890")
	if err != nil {
		t.Fatalf("parseHexID: %v", err)
	}
	if v != 0xa1b2c3d4e5f67890 {
		t.Errorf("value mismatch: %x", v)
	}
	if _, err := parseHexID("short"); err == nil {
		t.Error("expected error for short id")
	}
	if _, err := parseHexID("zzzzzzzzzzzzzzzz"); err == nil {
		t.Error("expected error for non-hex id")
	}
}

func TestErrResultAndOKResult(t *testing.T) {
	ok := okResult(map[string]any{"a": 1})
	if ok.IsError || len(ok.Content) == 0 {
		t.Fatal("okResult should be non-error with content")
	}
	errRes := errResult(errors.New("fail"))
	if !errRes.IsError {
		t.Fatal("errResult should set IsError")
	}
}
