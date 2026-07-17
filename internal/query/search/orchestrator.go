// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search orchestrator: single-entry pipeline with LLM preprocessing,
// retrieval, boost, and session touch.

package search

import (
	"log/slog"

	"memhop/internal/common/config"
	"memhop/internal/common/hash"
	"memhop/internal/core/index"
	"memhop/internal/query/dream"
	"memhop/internal/query/session"
)

// RunSearch orchestrates the full search pipeline:
//  1. LLM preprocessing (extract keywords, judge L3 import)
//  2. L2 retrieval (with activation/recent boosts)
//  3. L1-associated L2 lookup
//  4. Return L0 + L2 + associated L2 + L5
//  5. Touch search results for session management
func RunSearch(
	q SearchQuery,
	deps *SearchDeps,
	sessionMgr *session.SessionManager,
	llmCfg *config.LlmConfig,
	defaults *config.MemHopDefaults,
) (*SearchResult, error) {
	// Step 1: LLM preprocessing — extract keywords for topic creation and L3 import decision.
	var keywords []string
	if llm := dream.BuildChatProvider(llmCfg); llm != nil {
		preprocessResult, err := dream.PreprocessSearchQuery(llm, q.Text)
		if err != nil {
			slog.Warn("[search] LLM preprocessing failed, falling back to tokenizer",
				"error", err)
		} else if preprocessResult != nil {
			keywords = preprocessResult.Keywords
			if preprocessResult.NeedsL3Import && len(preprocessResult.L3Entities) > 0 {
				slog.Info("L3 import needed from search", "entities", len(preprocessResult.L3Entities))
			}
		}
	}
	// Fallback to tokenizer if LLM not configured or returned no keywords
	if len(keywords) == 0 {
		keywords = index.Tokenize(q.Text)
	}
	deps.PreprocessedKeywords = keywords

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
	BoostSearchResults(result, activeIDs, mostRecent, weights)

	// Step 4: Touch search results for session management.
	touchContexts(result.Contexts, sessionMgr, defaults)
	return result, nil
}

// RunDirectedSearch handles the directed_l2_id fast path: load context directly
// without running LLM preprocessing or search pipeline.
func RunDirectedSearch(
	q SearchQuery,
	deps *SearchDeps,
	sessionMgr *session.SessionManager,
	defaults *config.MemHopDefaults,
) (*SearchResult, error) {
	result, err := SearchContext(q, deps)
	if err != nil {
		return nil, err
	}
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
