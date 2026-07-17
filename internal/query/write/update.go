// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Update logic for memory layers: L2 dialogue append, L3/L5 field updates.

package write

import (
	"encoding/json"
	"log/slog"

	"memhop/internal/common/config"
	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
	"memhop/internal/core/index"
	"memhop/internal/core/storage"
	"memhop/internal/query/crud"
	"memhop/internal/query/dream"
)

// UpdateDeps holds all dependencies injected into the update pipeline.
type UpdateDeps struct {
	Engine        *storage.StorageEngine
	SparseIndex   *index.SparseIndex
	LlmCfg        *config.LlmConfig
	PreprocessCfg *config.LlmPreprocessConfig
}

// UpdateMemory updates a memory item at the specified layer.
// Unified entry point: validates parameters, optionally preprocesses with LLM,
// then dispatches to layer-specific update logic.
func UpdateMemory(req crud.UpdateRequest, deps *UpdateDeps) (*crud.UpdateResult, error) {
	if req.ID == "" {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "id is required")
	}

	var result *crud.UpdateResult
	var err error
	switch req.Layer {
	case 2:
		result, err = updateL2(req, deps)
	case 3:
		result, err = updateL3(req, deps)
	case 5:
		result, err = updateL5(req, deps)
	case 0:
		result, err = crud.UpdateProfile(deps.Engine, req)
	default:
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "unsupported layer for update")
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

// importL3Entities logs L3 entity import requests (stub for future implementation).
func importL3Entities(topicID uint64, entities []dream.L3EntityHint) {
	slog.Info("L3 import requested", "topic_id", hash.FormatHash(topicID), "entities", len(entities))
}

// maybePreprocessUpdate runs LLM preprocessing on dialogue_text to extract
// keywords, and injects them into the fields map if not already present.
func maybePreprocessUpdate(
	fields map[string]json.RawMessage,
	dialogueText string,
	deps *UpdateDeps,
) *dream.SearchPreprocessResult {
	cfg := deps.PreprocessCfg
	if cfg == nil || cfg.PreprocessMaxTokens <= 0 {
		return nil
	}
	// Only preprocess if no user_keywords already provided.
	if _, ok := fields["user_keywords"]; ok {
		return nil
	}
	llm := dream.BuildChatProvider(deps.LlmCfg)
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

func updateL2(req crud.UpdateRequest, deps *UpdateDeps) (*crud.UpdateResult, error) {
	// Extract dialogue_text and role from fields
	dialogueText := extractDialogueText(req.Fields)
	role := extractRole(req.Fields)

	if dialogueText != "" {
		// LLM preprocess
		preprocessResult := maybePreprocessUpdate(req.Fields, dialogueText, deps)

		// Get keywords
		var keywords []string
		if preprocessResult != nil {
			keywords = preprocessResult.Keywords
		}

		// Parse topic ID
		topicID, err := hash.ParseID(req.ID)
		if err != nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "invalid topic id", err)
		}

		// Append L4 archive
		if _, err := crud.AppendDialogueL4(deps.Engine, deps.SparseIndex, topicID, dialogueText, role, keywords); err != nil {
			return nil, err
		}

		// Handle L3 import if needed
		if preprocessResult != nil && preprocessResult.NeedsL3Import && len(preprocessResult.L3Entities) > 0 {
			importL3Entities(topicID, preprocessResult.L3Entities)
		}

		return &crud.UpdateResult{Status: crud.StatusUpdated, ID: req.ID}, nil
	}

	// Fallback: original field-based update
	var fields crud.UpdateL2Fields
	data, err := json.Marshal(req.Fields)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrSerialization, "marshal update fields", err)
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, mherrors.NewError(mherrors.ErrDeserialization, "unmarshal update fields", err)
	}
	_, err = crud.UpdateL2(deps.Engine, deps.SparseIndex, req.ID, fields)
	if err != nil {
		return nil, err
	}
	return &crud.UpdateResult{Status: crud.StatusUpdated, ID: req.ID}, nil
}

func updateL3(req crud.UpdateRequest, deps *UpdateDeps) (*crud.UpdateResult, error) {
	var fields crud.UpdateL3Fields
	data, err := json.Marshal(req.Fields)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrSerialization, "marshal update fields", err)
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, mherrors.NewError(mherrors.ErrDeserialization, "unmarshal update fields", err)
	}
	_, err = crud.UpdateL3(deps.Engine, req.ID, fields)
	if err != nil {
		return nil, err
	}
	return &crud.UpdateResult{Status: crud.StatusUpdated, ID: req.ID}, nil
}

func updateL5(req crud.UpdateRequest, deps *UpdateDeps) (*crud.UpdateResult, error) {
	var fields crud.UpdateL5Fields
	data, err := json.Marshal(req.Fields)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrSerialization, "marshal update fields", err)
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, mherrors.NewError(mherrors.ErrDeserialization, "unmarshal update fields", err)
	}
	if err := crud.UpdateL5(deps.Engine, req.ID, fields); err != nil {
		return nil, err
	}
	return &crud.UpdateResult{Status: crud.StatusUpdated, ID: req.ID}, nil
}
