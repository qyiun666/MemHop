// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Update-related DTOs for the MemHop Query layer.

package query

import "encoding/json"

// UpdateRequest is a layer-generic memory update request.
type UpdateRequest struct {
	ID     string                     `json:"id"`
	Layer  uint8                      `json:"layer"`
	Fields map[string]json.RawMessage `json:"fields"`
}

// UpdateResult is the response to an update request.
type UpdateResult struct {
	Status UpdateStatus `json:"status"`
	ID     string       `json:"id"`
}

// UpdateStatus indicates what happened during an update.
type UpdateStatus string

const (
	StatusCreated  UpdateStatus = "Created"
	StatusUpdated  UpdateStatus = "Updated"
	StatusArchived UpdateStatus = "Archived"
)

// MarshalJSON implements custom JSON encoding for UpdateStatus.
func (s UpdateStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(s))
}

// UnmarshalJSON implements custom JSON decoding for UpdateStatus.
func (s *UpdateStatus) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*s = UpdateStatus(v)
	return nil
}

// TargetLayer identifies which layer to import into.
type TargetLayer string

const (
	TargetProfile   TargetLayer = "Profile"
	TargetTopic     TargetLayer = "Topic"
	TargetKnowledge TargetLayer = "Knowledge"
)

// MarshalJSON implements custom JSON encoding for TargetLayer.
func (t TargetLayer) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(t))
}

// UnmarshalJSON implements custom JSON decoding for TargetLayer.
func (t *TargetLayer) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*t = TargetLayer(v)
	return nil
}

// ActionItem is an L5 action chain item.
type ActionItem struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	ActionType  ActionType        `json:"action_type"`
	Parameters  map[string]string `json:"parameters,omitempty"`
}

// ActionType is the kind of action.
type ActionType string

const (
	ActionCreate  ActionType = "Create"
	ActionRead    ActionType = "Read"
	ActionUpdate  ActionType = "Update"
	ActionDelete  ActionType = "Delete"
	ActionExecute ActionType = "Execute"
	ActionQuery   ActionType = "Query"
	ActionCustom  ActionType = "Custom"
)

// MarshalJSON implements custom JSON encoding for ActionType.
func (a ActionType) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(a))
}

// UnmarshalJSON implements custom JSON decoding for ActionType.
func (a *ActionType) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*a = ActionType(v)
	return nil
}

// UpdateL2Fields are partial update fields for an L2 TopicSlot.
type UpdateL2Fields struct {
	UserKeywords  []string `json:"user_keywords,omitempty"`
	AgentKeywords []string `json:"agent_keywords,omitempty"`
	FusedSummary  *string  `json:"fused_summary,omitempty"`
	L3Refs        []string `json:"l3_refs,omitempty"`
}

// UpdateL3Fields are partial update fields for an L3 hypergraph.
type UpdateL3Fields struct {
	Name *string `json:"name,omitempty"`
}

// UpdateL5Fields are partial update fields for an L5 action chain.
type UpdateL5Fields struct {
	Title         *string  `json:"title,omitempty"`
	Trigger       *string  `json:"trigger,omitempty"`
	Status        *string  `json:"status,omitempty"`
	Confidence    *float32 `json:"confidence,omitempty"`
	SuccessRate   *float32 `json:"success_rate,omitempty"`
	TriggerCount  *uint32  `json:"trigger_count,omitempty"`
	LastTriggered *int64   `json:"last_triggered,omitempty"`
}
