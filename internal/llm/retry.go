// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// retry.go is the transport policy: the truncation-aware chat retry used by
// every LLM capability. Prompt contracts, token budgets and response parsing
// live in internal/cap/llmops.

package llm

import (
	"context"
	"errors"

	"github.com/qyiun666/MemHop/internal/common"
)

// ChatWithRetry runs Chat with a primary max-token budget; if the response
// is truncated by the token ceiling, retries once with the retry budget.
// Non-truncation errors are returned immediately.
func (p *Provider) ChatWithRetry(ctx context.Context, system, user string, primaryMax, retryMax int) (string, error) {
	response, err := p.Chat(ctx, system, user, primaryMax, 0.0, 1.0)
	if err == nil || !errors.Is(err, common.ErrTruncated) || primaryMax >= retryMax {
		return response, err
	}
	return p.Chat(ctx, system, user, retryMax, 0.0, 1.0)
}

// MaxOutputTokens reports the configured output ceiling (the Chat seam).
func (p *Provider) MaxOutputTokens() int { return p.maxOutputTokens }
