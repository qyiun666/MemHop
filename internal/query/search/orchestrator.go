// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search orchestrator: single-entry pipeline with LLM preprocessing,
// retrieval, boost, and session touch.

package search

import (
	"context"
	"log/slog"

	"github.com/qyiun666/MemHop/internal/common/config"
	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/core/index"
	"github.com/qyiun666/MemHop/internal/query/dream"
	"github.com/qyiun666/MemHop/internal/query/session"
)

// RunSearch orchestrates the full search pipeline:
//  1. LLM fact extraction (atomic facts from user dialogue)
//  2. L2 retrieval (with activation/recent boosts)
//  3. L1-associated L2 lookup
//  4. Store user content to matched topic
//  5. Touch search results for session management
func RunSearch(
	q SearchQuery,
	deps *SearchDeps,
	sessionMgr *session.SessionManager,
	llmCfg *config.LlmConfig,
	defaults *config.MemHopDefaults,
) (*SearchResult, error) {
	if q.Timestamp <= 0 {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery,
			"SearchQuery.Timestamp is required (Unix milliseconds)")
	}
	// Step 1: LLM fact extraction — extract atomic memory facts from user content.
	var facts []string
	if llm := dream.BuildChatProvider(llmCfg); llm != nil {
		factResult, err := dream.ExtractFacts(context.Background(), llm, q.Text)
		if err != nil {
			slog.Warn("[search] LLM fact extraction failed, falling back to tokenizer",
				"error", err)
		} else if factResult != nil && len(factResult.Facts) > 0 {
			facts = factResult.Facts
		}
	}
	// Fallback to tokenizer if LLM not configured or returned no facts
	if len(facts) == 0 {
		facts = index.Tokenize(q.Text)
	}
	deps.PreprocessedKeywords = facts

	// Step 2: Search or auto-create L2 context.
	result, err := SearchContext(q, deps)
	if err != nil {
		return nil, err
	}

	// Step 3: Apply activation boosts.
	activeIDs := sessionMgr.GetActiveTopicIDs()
	mostRecent := sessionMgr.MostRecentTopic()
	var weights *config.SearchWeights
	if defaults != nil {
		weights = defaults.SearchWeights
	}
	if err := BoostSearchResults(result, activeIDs, mostRecent, weights); err != nil {
		return nil, err
	}

	// Step 4: Touch search results for session management.
	touchContexts(result.Contexts, sessionMgr, defaults)
	return result, nil
}

// RunDirectedSearch handles the directed_l2_id fast path: load context directly,
// store user content to the directed topic, and touch session.
func RunDirectedSearch(
	q SearchQuery,
	deps *SearchDeps,
	sessionMgr *session.SessionManager,
	llmCfg *config.LlmConfig,
	defaults *config.MemHopDefaults,
) (*SearchResult, error) {
	if q.Timestamp <= 0 {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery,
			"SearchQuery.Timestamp is required (Unix milliseconds)")
	}
	// Extract facts for user content storage.
	var facts []string
	if llm := dream.BuildChatProvider(llmCfg); llm != nil {
		factResult, err := dream.ExtractFacts(context.Background(), llm, q.Text)
		if err != nil {
			slog.Warn("[search-directed] LLM fact extraction failed", "error", err)
		} else if factResult != nil && len(factResult.Facts) > 0 {
			facts = factResult.Facts
		}
	}
	if len(facts) == 0 {
		facts = index.Tokenize(q.Text)
	}
	deps.PreprocessedKeywords = facts

	result, err := SearchContext(q, deps)
	if err != nil {
		return nil, err
	}

	// Note: L4 persistence is handled inside createTopicInScene (called by
	// searchDirected), so no additional storeQueryAsL4 is needed here.

	touchContexts(result.Contexts, sessionMgr, defaults)
	return result, nil
}

// touchContexts touches all result contexts in the session manager.
func touchContexts(contexts []ContextResult, sessionMgr *session.SessionManager, defaults *config.MemHopDefaults) {
	ttlMs := int64(0)
	if defaults != nil && defaults.SessionConfig != nil {
		ttlMs = defaults.SessionConfig.DefaultTTLMs
	}
	for _, ctx := range contexts {
		if idHash, err := hash.ParseID(ctx.ID); err == nil {
			sessionMgr.Touch(idHash, ttlMs)
		}
	}
}
