// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package write

import (
	"encoding/json"
	"testing"

	"github.com/qyiun666/MemHop/internal/query/crud"
)

// ---------------------------------------------------------------------------
// extractDialogueText — pure helper
// ---------------------------------------------------------------------------

func TestExtractDialogueText(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]json.RawMessage
		want   string
	}{
		{"present", map[string]json.RawMessage{
			"dialogue_text": json.RawMessage(`"hello world"`),
		}, "hello world"},
		{"missing key", map[string]json.RawMessage{
			"other": json.RawMessage(`"x"`),
		}, ""},
		{"empty map", map[string]json.RawMessage{}, ""},
		{"nil map", nil, ""},
		{"invalid json value", map[string]json.RawMessage{
			"dialogue_text": json.RawMessage(`not-json`),
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDialogueText(tt.fields)
			if got != tt.want {
				t.Errorf("extractDialogueText() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// extractRole — pure helper
// ---------------------------------------------------------------------------

func TestExtractRole(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]json.RawMessage
		want   uint8
	}{
		{"present", map[string]json.RawMessage{
			"role": json.RawMessage(`2`),
		}, 2},
		{"missing key defaults to 1", map[string]json.RawMessage{
			"other": json.RawMessage(`5`),
		}, 1},
		{"empty map defaults to 1", map[string]json.RawMessage{}, 1},
		{"nil map defaults to 1", nil, 1},
		{"invalid json defaults to 1", map[string]json.RawMessage{
			"role": json.RawMessage(`"not-a-number"`),
		}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRole(tt.fields)
			if got != tt.want {
				t.Errorf("extractRole() = %d, want %d", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// UpdateMemory — validation paths (no storage needed)
// ---------------------------------------------------------------------------

func TestUpdateMemoryValidation(t *testing.T) {
	t.Run("empty ID", func(t *testing.T) {
		_, err := UpdateMemory(crud.UpdateRequest{ID: "", Layer: 2}, &UpdateDeps{})
		if err == nil {
			t.Fatal("expected error for empty ID")
		}
	})

	t.Run("unsupported layer", func(t *testing.T) {
		_, err := UpdateMemory(crud.UpdateRequest{ID: "abc", Layer: 99, Timestamp: 1700000000000}, &UpdateDeps{})
		if err == nil {
			t.Fatal("expected error for unsupported layer")
		}
	})

	t.Run("missing timestamp", func(t *testing.T) {
		_, err := UpdateMemory(crud.UpdateRequest{ID: "abc", Layer: 2}, &UpdateDeps{})
		if err == nil {
			t.Fatal("expected error for missing timestamp")
		}
	})
}
