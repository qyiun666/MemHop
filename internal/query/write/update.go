// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Update logic for memory layers: L2 dialogue append, L3/L5 field updates.

package write

import (
	"encoding/json"
	"log/slog"

	"github.com/qyiun666/MemHop/internal/common/config"
	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/core/index"
	"github.com/qyiun666/MemHop/internal/core/storage"
	"github.com/qyiun666/MemHop/internal/query/crud"
	"github.com/qyiun666/MemHop/internal/query/dream"
)

// UpdateDeps holds all dependencies injected into the update pipeline.
type UpdateDeps struct {
	Engine      *storage.StorageEngine
	SparseIndex *index.SparseIndex
	LlmCfg      *config.LlmConfig
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

// maybePreprocessUpdate runs LLM fact extraction on dialogue_text to extract
// atomic memory facts, and injects them into the fields map if not already present.
func maybePreprocessUpdate(
	fields map[string]json.RawMessage,
	dialogueText string,
	deps *UpdateDeps,
) *dream.FactExtractionResult {
	// Only extract if no user_keywords already provided.
	if _, ok := fields["user_keywords"]; ok {
		return nil
	}
	llm := dream.BuildChatProvider(deps.LlmCfg)
	if llm == nil {
		return nil
	}
	result, err := dream.ExtractFacts(llm, dialogueText)
	if err != nil {
		slog.Warn("LLM fact extraction failed", "error", err)
		return nil
	}
	if result != nil && len(result.Facts) > 0 {
		kwJSON, marshalErr := json.Marshal(result.Facts)
		if marshalErr != nil {
			slog.Warn("LLM fact extraction: failed to marshal facts", "error", marshalErr)
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
		// LLM fact extraction
		factResult := maybePreprocessUpdate(req.Fields, dialogueText, deps)

		// Get extracted facts as keywords
		var keywords []string
		if factResult != nil {
			keywords = factResult.Facts
		}

		// Parse topic ID
		topicID, err := hash.ParseID(req.ID)
		if err != nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "invalid topic id", err)
		}

		// Append L4 archive
		if _, err := crud.AppendDialogueL4(deps.Engine, deps.SparseIndex, topicID, dialogueText, role, keywords, req.Timestamp); err != nil {
			return nil, err
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
	_, err = crud.UpdateL2(deps.Engine, deps.SparseIndex, req.ID, fields, req.Timestamp)
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
