// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"context"
	"log/slog"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/repo/core/index"
	"github.com/qyiun666/MemHop/internal/query/crud"
	"github.com/qyiun666/MemHop/internal/query/dream"
	"github.com/qyiun666/MemHop/internal/query/write"
)

// Update appends an agent reply to the specified topic.
// topicID is the hex topic ID returned by Search (SearchResult.NewTopicID,
// the depth1 topic created for the current turn).
// It extracts atomic facts via LLM and appends the reply as L4 archive.
// This is the core-loop counterpart to Search: Search stores user content,
// Update stores agent content.
func (m *MemHop) Update(topicID string, text string, timestamp int64) error {
	if err := m.beginRead(); err != nil {
		return err
	}
	defer m.mu.RUnlock()
	if text == "" {
		return mherrors.NewError(mherrors.ErrInvalidQuery, "text is required")
	}
	if timestamp <= 0 {
		return mherrors.NewError(mherrors.ErrInvalidQuery,
			"timestamp is required (Unix milliseconds)")
	}

	// Parse topic ID from Search result.
	parsedID, err := hash.ParseID(topicID)
	if err != nil {
		return mherrors.NewError(mherrors.ErrInvalidQuery, "invalid topic id", err)
	}

	// Extract atomic facts from agent reply.
	var facts []string
	if llm := dream.BuildChatProvider(&m.config.LLM); llm != nil {
		factResult, err := dream.ExtractFacts(context.Background(), llm, text)
		if err != nil {
			slog.Warn("[update] LLM fact extraction failed", "error", err)
		} else if factResult != nil && len(factResult.Facts) > 0 {
			facts = factResult.Facts
		}
	}
	if len(facts) == 0 {
		facts = index.Tokenize(text)
	}

	// Append agent reply as L4 archive + update AgentKeywords.
	_, err = crud.AppendDialogueL4(m.engine, m.sparseIndex, parsedID, text, 1, facts, timestamp)
	return err
}

// UpdateMemory is the layer-generic field-level update entry point.
// Dispatches to L0 / L2 / L3 / L5 field updates based on req.Layer.
func (m *MemHop) UpdateMemory(req crud.UpdateRequest) (*crud.UpdateResult, error) {
	if err := m.beginRead(); err != nil {
		return nil, err
	}
	defer m.mu.RUnlock()
	deps := &write.UpdateDeps{
		Engine:      m.engine,
		SparseIndex: m.sparseIndex,
		LlmCfg:      &m.config.LLM,
	}
	return write.UpdateMemory(req, deps)
}
