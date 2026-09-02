// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Tool registration for the memhop-mcp server. Every public DB method of
// the api package is exposed as one MCP tool; arguments and results are
// plain JSON (the DB DTOs carry json tags already). uint64 record IDs are
// serialized as 16-char hex strings so JS hosts never lose precision.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

// handle wraps a typed handler: it decodes raw JSON arguments into In,
// calls fn, and serializes Out as the tool result. Handler errors become
// tool errors (IsError=true) so the client LLM can see and self-correct.
func handle[In, Out any](fn func(In) (Out, error)) mcp.ToolHandler {
	return func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in In
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &in); err != nil {
				return errResult(fmt.Errorf("invalid arguments: %w", err)), nil
			}
		}
		out, err := fn(in)
		if err != nil {
			return errResult(err), nil
		}
		return okResult(out), nil
	}
}

// handleNoArgs wraps a handler that takes no arguments.
func handleNoArgs[Out any](fn func() (Out, error)) mcp.ToolHandler {
	return handle(func(struct{}) (Out, error) { return fn() })
}

// idHexFields are the JSON keys whose values are MemHop record IDs. Only
// these are converted to hex strings; everything else stays numeric.
var idHexFields = map[string]bool{
	"id":           true, // TopicSlot.ID
	"id_hash":      true, // slots (L0/L1/L3/L4/L5/L6)
	"scene_id":     true,
	"parent_id":    true,
	"new_topic_id": true,
	"context_id":   true, // HypergraphSource / ArchiveSlot
	"graph_id":     true, // L3 hypergraph + node
	"topic_ids":    true, // L1 scene slot
	"children_ids": true,
	"edge_ids":     true, // L1 scene slot
	"l4_refs":      true,
	"l3_refs":      true,
	"session_id":   true, // L6 TrajectorySlot
	"l4_ref":       true, // L6 TrajectorySlot optional archive ref
}

// marshalResult marshals v with ID fields converted to hex strings.
func marshalResult(v any) ([]byte, error) {
	// Round-trip through json.Number so uint64 values never lose precision
	// before the conversion (encoding/json decodes into float64 by default).
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, err
	}
	return json.Marshal(idsToHex(tree))
}

// idsToHex recursively converts ID fields (by key) and all elements of ID
// arrays to 16-digit hex strings.
func idsToHex(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if idHexFields[k] {
				t[k] = idValueToHex(val)
			} else {
				t[k] = idsToHex(val)
			}
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = idsToHex(val)
		}
		return t
	default:
		return v
	}
}

// idValueToHex converts a uint64-ish JSON number (or array of them) to hex.
func idValueToHex(v any) any {
	switch t := v.(type) {
	case json.Number:
		if u, err := strconv.ParseUint(string(t), 10, 64); err == nil {
			return fmt.Sprintf("%016x", u)
		}
		return t.String()
	case []any:
		for i, val := range t {
			t[i] = idValueToHex(val)
		}
		return t
	default:
		return v
	}
}

// okResult serializes v as the JSON text content of a successful result.
func okResult(v any) *mcp.CallToolResult {
	data, err := marshalResult(v)
	if err != nil {
		r := &mcp.CallToolResult{}
		r.SetError(fmt.Errorf("encode result: %w", err))
		return r
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
	}
}

// errResult reports a tool error to the client (IsError=true).
func errResult(err error) *mcp.CallToolResult {
	r := &mcp.CallToolResult{}
	r.SetError(err)
	return r
}

// updateResult is the uniform OK response of write tools.
type updateResult struct {
	OK bool `json:"ok"`
}

// ---- JSON Schema helpers ----

func objSchema(props map[string]any, required ...string) map[string]any {
	// JSON Schema 2020-12: properties defaults to {}; an explicit JSON null
	// (Go nil map) breaks strict MCP clients (e.g. the TypeScript SDK's zod
	// validation requires a record for tools/inputSchema/properties).
	if props == nil {
		props = map[string]any{}
	}
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

// mapProp declares a string-to-string object property (e.g. Preferences).
func mapProp(desc string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": map[string]any{"type": "string"},
		"description":          desc,
	}
}

func arrProp(desc, itemType string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": itemType}, "description": desc}
}

// resolveContentType maps an optional human-readable content-type argument to
// a ContentType. An empty value means text; an unknown value is rejected
// before touching the DB.
func resolveContentType(name string) (memhop.ContentType, error) {
	if name == "" {
		return memhop.ContentText, nil
	}
	v, ok := contentTypeNames[name]
	if !ok {
		return 0, fmt.Errorf("invalid content_type %q (want text, image, video, document, audio, code or other)", name)
	}
	return v, nil
}

// registerTools attaches all 31 tools to the server for one tenant DB.
func registerTools(s *mcp.Server, db *memhop.Session) {
	registerCoreTools(s, db)
	registerL2Tools(s, db)
	registerL3Tools(s, db)
	registerL4Tools(s, db)
	registerL5Tools(s, db)
	registerL6Tools(s, db)
}
