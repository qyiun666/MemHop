// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"log/slog"

	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/dream"
	"github.com/qyiun666/memhop/memhop/internal/core/index"
	"github.com/qyiun666/memhop/memhop/internal/core/query"
	"github.com/qyiun666/memhop/memhop/internal/hash"
)

// Search runs the full search pipeline and returns matching contexts.
// Steps: 1. LLM preprocess (extract keywords, judge L3 import)
//  2. L2 retrieval (with activation/recent boosts)
//  3. L1-associated L2 lookup
//  4. Return L0 + L2 + associated L2 + L5
func (m *MemHop) Search(q query.SearchQuery) (*query.SearchResult, error) {
	// Fast path: directed search only needs read lock.
	if q.DirectedL2ID != nil {
		m.mu.RLock()
		defer m.mu.RUnlock()
		if m.closed {
			return nil, core.ErrClosed
		}
		return m.searchDirected(q)
	}

	// Normal path: needs write lock (auto_create + session touch).
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, core.ErrClosed
	}

	deps := m.searchDeps()

	// Step 1: LLM preprocessing — extract keywords for topic creation and L3 import decision.
	// Always runs when an LLM is configured, regardless of AutoCreate flag.
	var keywords []string
	if llm := m.llmChatProvider(); llm != nil {
		preprocessResult, err := dream.PreprocessSearchQuery(llm, q.Text)
		if err != nil {
			slog.Warn("[search] LLM preprocessing failed, falling back to tokenizer",
				"error", err)
		} else if preprocessResult != nil {
			keywords = preprocessResult.Keywords
			if preprocessResult.NeedsL3Import && len(preprocessResult.L3Entities) > 0 {
				slog.Info("L3 import needed from search", "entities", len(preprocessResult.L3Entities))
				// TODO: create L3 nodes and link to matched topics
			}
		}
	}
	// Fallback to tokenizer if LLM not configured or returned no keywords
	if len(keywords) == 0 {
		keywords = index.Tokenize(q.Text)
	}
	deps.PreprocessedKeywords = keywords

	// Step 2: Search or auto-create L2 context.
	result, err := query.SearchContext(q, deps)
	if err != nil {
		return nil, err
	}

	// Apply activation boosts: additive bonus for active topics + recent chat.
	activeIDs := m.sessionMgr.GetActiveTopicIDs()
	mostRecent := m.sessionMgr.MostRecentTopic()
	query.BoostSearchResults(result, activeIDs, mostRecent, m.defaults.SearchWeights)

	m.touchSearchResults(result)

	return result, nil
}

// searchDirected handles the directed_l2_id fast path: load context directly
// without running the search pipeline.
func (m *MemHop) searchDirected(q query.SearchQuery) (*query.SearchResult, error) {
	deps := m.searchDeps()
	result, err := query.SearchContext(q, deps)
	if err != nil {
		return nil, err
	}
	m.touchSearchResults(result)
	return result, nil
}

// rebuildIVFIndex rebuilds the IVF index based on current engine record count.
func (m *MemHop) rebuildIVFIndex() {
	if m.ivfIndex != nil {
		m.ivfIndex.RebuildIfNeeded(int(m.engine.RecordCount()))
	}
}

// llmChatProvider returns a ChatProvider if LLM is configured, nil otherwise.
func (m *MemHop) llmChatProvider() dream.ChatProvider {
	if m.config.LLM.APIURL == "" || m.config.LLM.APIKey == "" {
		return nil
	}
	return dream.NewOpenAIProvider(&m.config.LLM)
}

func (m *MemHop) touchSearchResults(result *query.SearchResult) {
	ttlMs := int64(0)
	if m.defaults.SessionConfig != nil {
		ttlMs = m.defaults.SessionConfig.DefaultTTLMs
	}
	for _, ctx := range result.Contexts {
		if idHash, err := hash.ParseID(ctx.ID); err == nil {
			m.sessionMgr.Touch(idHash, ttlMs)
		}
	}
}
