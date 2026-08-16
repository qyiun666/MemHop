// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Tool registration for the memhop-mcp server. Every public DB method of
// the root memhop package is exposed as one MCP tool; arguments and results
// are plain JSON (the DB DTOs carry json tags already).

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop"
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

// okResult serializes v as the JSON text content of a successful result.
func okResult(v any) *mcp.CallToolResult {
	data, err := json.Marshal(v)
	if err != nil {
		return errResult(fmt.Errorf("serialize result: %w", err))
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

// ---- input schema helpers (JSON Schema 2020-12) ----

func objSchema(props map[string]any, required ...string) map[string]any {
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

func arrProp(desc, itemType string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": itemType}, "description": desc}
}

// registerTools registers all MemHop tools on the server.
func registerTools(s *mcp.Server, db *memhop.DB) {
	registerCoreTools(s, db)
	registerL2Tools(s, db)
	registerL3Tools(s, db)
	registerL4Tools(s, db)
	registerL5Tools(s, db)
	registerL7Tools(s, db)
}
