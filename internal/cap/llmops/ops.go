// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package llmops hosts the LLM-assisted cognitive capabilities of the
// memory engine: keyword extraction, L2 consolidation, L1->L0 distillation
// and L6->L5 crystallization. Each is a self-contained prompt contract plus
// response parser; the transport is injected as Chat, so the package never
// depends on the composition root or a specific provider.
package llmops

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
)

// Chat is the injected chat-completion transport (implemented by the
// composition root's LLM provider). ChatWithRetry applies the transport's
// truncation-escalation policy; MaxOutputTokens exposes the configured
// output ceiling so each capability can budget its own calls.
type Chat interface {
	Chat(ctx context.Context, system, user string, maxTokens int, temperature, topP float32) (string, error)
	ChatWithRetry(ctx context.Context, system, user string, primaryMax, retryMax int) (string, error)
	MaxOutputTokens() int
}

// ConsolidationMaxTokens is the L2 consolidation output ceiling; it doubles
// as the escalation budget for every truncation retry across the call
// points (keyword extraction and distillation escalate up to it).
const ConsolidationMaxTokens = 8192

// minTokens keeps a call's output cap at or below the configured ceiling.
func minTokens(configured, ceiling int) int {
	if configured <= 0 || configured > ceiling {
		return ceiling
	}
	return configured
}

// parseUint64Flex parses a JSON number or quoted string as uint64, decimal
// first then hex (0x prefix).
func parseUint64Flex(raw json.RawMessage) (uint64, error) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return 0, common.NewError(common.ErrLLM, "empty uint64 value")
	}
	if v, err := strconv.ParseUint(s, 10, 64); err == nil {
		return v, nil
	}
	return strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
}

// stripCodeBlocks removes ```lang ... ``` markdown fences from LLM output.
func stripCodeBlocks(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	body := trimmed[3:]
	if nl := strings.IndexByte(body, '\n'); nl >= 0 {
		body = body[nl+1:]
	} else {
		body = ""
	}
	if end := strings.LastIndex(body, "```"); end >= 0 {
		body = body[:end]
	}
	return strings.TrimSpace(body)
}
