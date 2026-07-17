// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"encoding/json"
	"log/slog"

	"memhop/internal/core"
	"memhop/internal/core/dream"
	"memhop/internal/core/query"
	"memhop/internal/hash"
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
func (m *MemHop) maybePreprocessUpdate(fields map[string]json.RawMessage, dialogueText string) *query.SearchPreprocessResult {
	cfg := m.defaults.LlmPreprocess
	if cfg == nil || cfg.PreprocessMaxTokens <= 0 {
		return nil
	}
	// Only preprocess if no user_keywords already provided.
	if _, ok := fields["user_keywords"]; ok {
		return nil
	}
	llm := m.llmChatProvider()
	if llm == nil {
		return nil
	}
	result, err := dream.PreprocessSearchQuery(llm, dialogueText)
	if err != nil {
		slog.Warn("LLM preprocess failed, continuing without enhancement", "error", err)
		return nil
	}
	if result != nil && len(result.Keywords) > 0 {
		kwJSON, marshalErr := json.Marshal(result.Keywords)
		if marshalErr != nil {
			slog.Warn("LLM preprocess: failed to marshal keywords", "error", marshalErr)
			return nil
		}
		fields["user_keywords"] = kwJSON
	}
	return result
}

func (m *MemHop) updateL2Memory(req query.UpdateRequest) (*query.UpdateResult, error) {
	// Extract dialogue_text and role from fields
	dialogueText := extractDialogueText(req.Fields)
	role := extractRole(req.Fields)

	if dialogueText != "" {
		// LLM preprocess
		preprocessResult := m.maybePreprocessUpdate(req.Fields, dialogueText)

		// Get keywords
		var keywords []string
		if preprocessResult != nil {
			keywords = preprocessResult.Keywords
		}

		// Parse topic ID
		topicID, err := hash.ParseID(req.ID)
		if err != nil {
			return nil, core.NewError(core.ErrInvalidQuery, "invalid topic id", err)
		}

		// Append L4 archive
		if _, err := query.AppendDialogueL4(m.engine, m.sparseIndex, topicID, dialogueText, role, keywords); err != nil {
			return nil, err
		}

		// Handle L3 import if needed
		if preprocessResult != nil && preprocessResult.NeedsL3Import && len(preprocessResult.L3Entities) > 0 {
			m.importL3Entities(topicID, preprocessResult.L3Entities)
		}

		return &query.UpdateResult{Status: query.StatusUpdated, ID: req.ID}, nil
	}

	// Fallback: original field-based update
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

// extractRole extracts the "role" field from raw fields. Defaults to 1 (agent).
func extractRole(fields map[string]json.RawMessage) uint8 {
	raw, ok := fields["role"]
	if !ok {
		return 1 // default agent
	}
	var role uint8
	if err := json.Unmarshal(raw, &role); err != nil {
		return 1
	}
	return role
}

// importL3Entities imports L3 knowledge entities for a given topic.
// TODO: implement L3 entity creation and linking to L2 topic.
func (m *MemHop) importL3Entities(topicID uint64, entities []query.L3EntityHint) {
	slog.Info("L3 import requested", "topic_id", hash.FormatHash(topicID), "entities", len(entities))
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
