// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Tool registration for the memhop-mcp server. Every public DB method of
// the api package is exposed as one MCP tool; arguments and results are
// plain JSON (the DB DTOs carry json tags already). Record IDs reach a
// client as the 16-char hex strings the api DTOs render — no numeric id
// crosses this boundary, which api/surface_public_test.go pins.

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

// handle wraps a typed handler: it decodes raw JSON arguments into In,
// calls fn, and serializes Out as the tool result. Handler errors become
// tool errors (IsError=true) so the client LLM can see and self-correct.
func handle[In, Out any](fn func(In) (Out, error)) mcp.ToolHandler {
	return func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		in, err := decodeArgs[In](req)
		if err != nil {
			return errResult(err), nil
		}
		out, err := fn(in)
		if err != nil {
			return errResult(err), nil
		}
		return okResult(out), nil
	}
}

// handlePartial is handle for a tool whose result stands on its own even when
// the call failed. Only Dream needs it: the report lists what the pipeline
// already did (the two retention prunes, the stage that failed), so dropping it
// would leave a host unable to tell "cleaned up, then consolidation failed"
// from "nothing happened at all". It also gets the request context: Dream holds
// the domain lock through LLM round trips, and a client that has gone should
// stop that work rather than let it run to the end of the pipeline.
func handlePartial[In, Out any](fn func(context.Context, In) (Out, error)) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		in, err := decodeArgs[In](req)
		if err != nil {
			return errResult(err), nil
		}
		out, err := fn(ctx, in)
		if err != nil {
			return errResultWith(out, err), nil
		}
		return okResult(out), nil
	}
}

// decodeArgs turns the raw call arguments into the handler's input type. An
// absent argument object is an empty input, which is what a no-argument tool
// and every optional field mean.
func decodeArgs[In any](req *mcp.CallToolRequest) (In, error) {
	var in In
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &in); err != nil {
			return in, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	return in, nil
}

// handleNoArgs wraps a handler that takes no arguments.
func handleNoArgs[Out any](fn func() (Out, error)) mcp.ToolHandler {
	return handle(func(struct{}) (Out, error) { return fn() })
}

// okResult serializes v as the JSON text content of a successful result.
func okResult(v any) *mcp.CallToolResult {
	data, err := json.Marshal(v)
	if err != nil {
		r := &mcp.CallToolResult{}
		r.SetError(fmt.Errorf("encode result: %w", err))
		return r
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
	}
}

// errResult reports a tool failure to the client (IsError=true). The engine's
// numeric code goes into the text because a tool client has no other channel for
// it: several descriptions promise a refusal by its code name, and "no such record"
// (3001) versus "that record will not read" (5001) is not something the sentence
// alone lets a client tell apart. An error with no code of its own stays as it is.
func errResult(err error) *mcp.CallToolResult {
	r := &mcp.CallToolResult{}
	if code := memhop.CodeOf(err); code != 0 {
		err = fmt.Errorf("[%d] %w", code, err)
	}
	r.SetError(err)
	return r
}

// errResultWith reports a tool error and carries the result the handler
// produced on the way out: the first content block is the error message, the
// second the result JSON. A result that will not encode is not reported
// alongside the failure that already outranks it.
func errResultWith(v any, err error) *mcp.CallToolResult {
	data, merr := json.Marshal(v)
	r := errResult(err)
	if merr != nil {
		return r
	}
	r.Content = append(r.Content, &mcp.TextContent{Text: string(data)})
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

// resolveArchiveKind maps an optional kind argument to the L4 kind it names. An
// empty value means an utterance: the MCP host that appends without saying which
// is recording what somebody said, not what happened.
func resolveArchiveKind(name string) (memhop.ArchiveKind, error) {
	if name == "" {
		return memhop.KindUtterance, nil
	}
	v, ok := kindNames[name]
	if !ok {
		return 0, fmt.Errorf("invalid kind %q (want utterance or event)", name)
	}
	return v, nil
}

// resolveRole maps a speaker name onto the role an utterance carries. There is no
// default: a record of who spoke that does not say who spoke is exactly the
// collapsed transcript keyword extraction loses the thread of.
func resolveRole(name string) (uint8, error) {
	v, ok := roleNames[name]
	if !ok {
		return 0, fmt.Errorf("invalid role %q (want user, agent or system)", name)
	}
	return v, nil
}

// registerTools attaches all tools to the server for one tenant DB.
func registerTools(s *mcp.Server, m *memhop.DB, db *memhop.Session) {
	registerCoreTools(s, m, db)
	registerL1Tools(s, db)
	registerL2Tools(s, db)
	registerL3Tools(s, db)
	registerL4Tools(s, db)
}
