// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"encoding/json"
	"log/slog"

	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/dream"
	"github.com/qyiun666/memhop/memhop/internal/core/query"
)

// UpdateMemory updates a memory item at the specified layer.
// Unified entry point: validates parameters, optionally preprocesses with LLM,
// then dispatches to layer-specific update logic.
func (m *MemHop) UpdateMemory(req query.UpdateRequest) (*query.UpdateResult, error) {
	if req.ID == "" {
		return nil, core.NewError(core.ErrInvalidQuery, "id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, core.ErrClosed
	}

	// Extract dialogue_text from fields for LLM preprocessing.
	dialogueText := extractDialogueText(req.Fields)

	// Auto-preprocess with LLM when enabled and no keywords provided by caller.
	if dialogueText != "" {
		m.maybePreprocessUpdate(req.Fields, dialogueText)
	}

	var result *query.UpdateResult
	var err error
	switch req.Layer {
	case 2:
		result, err = m.updateL2Memory(req)
	case 3:
		result, err = m.updateL3Memory(req)
	case 5:
		result, err = m.updateL5Memory(req)
	case 0:
		result, err = query.UpdateProfile(m.engine, req)
	default:
		return nil, core.NewError(core.ErrInvalidQuery, "unsupported layer for update")
	}

	// Rebuild IVF index after mutation (single update may have added vectors).
	if err == nil {
		m.rebuildIVFIndex()
	}

	return result, err
}

// extractDialogueText extracts "dialogue_text" from raw fields.
func extractDialogueText(fields map[string]json.RawMessage) string {
	raw, ok := fields["dialogue_text"]
	if !ok {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return ""
	}
	return text
}

// maybePreprocessUpdate runs LLM preprocessing on dialogue_text to extract
// keywords, and injects them into the fields map if not already present.
func (m *MemHop) maybePreprocessUpdate(fields map[string]json.RawMessage, dialogueText string) {
	cfg := m.defaults.LlmPreprocess
	if cfg == nil || cfg.PreprocessMaxTokens <= 0 {
		return
	}
	// Only preprocess if no user_keywords already provided.
	if _, ok := fields["user_keywords"]; ok {
		return
	}
	llm := m.llmChatProvider()
	if llm == nil {
		return
	}
	result, err := preprocessWriteWithLLM(llm, dialogueText)
	if err != nil {
		slog.Warn("LLM preprocess failed, continuing without enhancement", "error", err)
		return
	}
	if result != nil && len(result.Keywords) > 0 {
		kwJSON, marshalErr := json.Marshal(result.Keywords)
		if marshalErr != nil {
			slog.Warn("LLM preprocess: failed to marshal keywords", "error", marshalErr)
			return
		}
		fields["user_keywords"] = kwJSON
	}
}

// preprocessWriteWithLLM extracts keywords via LLM for write operations.
func preprocessWriteWithLLM(llm dream.ChatProvider, content string) (*query.WritePreprocessResult, error) {
	return dream.PreprocessWriteContent(llm, content)
}

func (m *MemHop) updateL2Memory(req query.UpdateRequest) (*query.UpdateResult, error) {
	var fields query.UpdateL2Fields
	data, err := json.Marshal(req.Fields)
	if err != nil {
		return nil, core.NewError(core.ErrSerialization, "marshal update fields", err)
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, core.NewError(core.ErrDeserialization, "unmarshal update fields", err)
	}
	_, err = query.UpdateL2(m.engine, m.sparseIndex, req.ID, fields)
	if err != nil {
		return nil, err
	}
	return &query.UpdateResult{Status: query.StatusUpdated, ID: req.ID}, nil
}

func (m *MemHop) updateL3Memory(req query.UpdateRequest) (*query.UpdateResult, error) {
	var fields query.UpdateL3Fields
	data, err := json.Marshal(req.Fields)
	if err != nil {
		return nil, core.NewError(core.ErrSerialization, "marshal update fields", err)
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, core.NewError(core.ErrDeserialization, "unmarshal update fields", err)
	}
	_, err = query.UpdateL3(m.engine, req.ID, fields)
	if err != nil {
		return nil, err
	}
	return &query.UpdateResult{Status: query.StatusUpdated, ID: req.ID}, nil
}

func (m *MemHop) updateL5Memory(req query.UpdateRequest) (*query.UpdateResult, error) {
	var fields query.UpdateL5Fields
	data, err := json.Marshal(req.Fields)
	if err != nil {
		return nil, core.NewError(core.ErrSerialization, "marshal update fields", err)
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, core.NewError(core.ErrDeserialization, "unmarshal update fields", err)
	}
	if err := query.UpdateL5(m.engine, req.ID, fields); err != nil {
		return nil, err
	}
	return &query.UpdateResult{Status: query.StatusUpdated, ID: req.ID}, nil
}
