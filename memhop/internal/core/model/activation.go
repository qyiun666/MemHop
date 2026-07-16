// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Activation state and LLM parameter types for L2 context nodes.

package model

import (
	"encoding/json"
	"fmt"
)

// ============================================================================
// ActivationState — lifecycle state of a context node (context.rs)
// ============================================================================

// ActivationState represents the lifecycle state of a context node.
type ActivationState uint8

const (
	ActivationDormant      ActivationState = 0
	ActivationActive       ActivationState = 1
	ActivationCrystallized ActivationState = 2
)

var activationStateNames = map[ActivationState]string{
	ActivationDormant:      "dormant",
	ActivationActive:       "active",
	ActivationCrystallized: "crystallized",
}

func (a ActivationState) String() string {
	if s, ok := activationStateNames[a]; ok {
		return s
	}
	return fmt.Sprintf("ActivationState(%d)", a)
}

// MarshalJSON encodes ActivationState as a JSON number.
func (a ActivationState) MarshalJSON() ([]byte, error) {
	return json.Marshal(uint8(a))
}

// UnmarshalJSON decodes ActivationState from a JSON number.
func (a *ActivationState) UnmarshalJSON(data []byte) error {
	var v uint8
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*a = ActivationState(v)
	return nil
}

// ============================================================================
// LlmParams — LLM generation parameters (context.rs)
// ============================================================================

// LlmParams holds LLM generation parameters for context-level overrides.
type LlmParams struct {
	Temperature       float32 `json:"temperature"`
	TopP              float32 `json:"top_p"`
	PresencePenalty   float32 `json:"presence_penalty"`
	FrequencyPenalty  float32 `json:"frequency_penalty"`
}

// DefaultLlmParams returns default LLM parameters.
func DefaultLlmParams() LlmParams {
	return LlmParams{
		Temperature:      0.7,
		TopP:             0.9,
		PresencePenalty:  0.0,
		FrequencyPenalty: 0.0,
	}
}
