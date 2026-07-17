// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package mherrors

import (
	"errors"
	"testing"
)

func TestSentinelErrorsNotNil(t *testing.T) {
	sentinels := []struct {
		name string
		err  error
	}{
		{"ErrIO", ErrIO},
		{"ErrInvalidMagic", ErrInvalidMagic},
		{"ErrCRCMismatch", ErrCRCMismatch},
		{"ErrCorruption", ErrCorruption},
		{"ErrNotFound", ErrNotFound},
		{"ErrVectorDimMismatch", ErrVectorDimMismatch},
		{"ErrSerialization", ErrSerialization},
		{"ErrDeserialization", ErrDeserialization},
		{"ErrEncoder", ErrEncoder},
		{"ErrConfig", ErrConfig},
		{"ErrLLM", ErrLLM},
		{"ErrInvalidQuery", ErrInvalidQuery},
		{"ErrClosed", ErrClosed},
	}

	for _, tt := range sentinels {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				t.Errorf("%s should not be nil", tt.name)
			}
		})
	}
}

func TestNewErrorFields(t *testing.T) {
	cause := errors.New("underlying cause")
	err := NewError(ErrIO, "failed to open file", cause)

	if err.Kind != ErrIO {
		t.Errorf("Kind = %v; want %v", err.Kind, ErrIO)
	}
	if err.Message != "failed to open file" {
		t.Errorf("Message = %q; want %q", err.Message, "failed to open file")
	}
	if err.Cause != cause {
		t.Errorf("Cause = %v; want %v", err.Cause, cause)
	}
}

func TestNewErrorNoCause(t *testing.T) {
	err := NewError(ErrConfig, "invalid config")
	if err.Cause != nil {
		t.Errorf("expected nil Cause, got %v", err.Cause)
	}
}

func TestMemHopError_Error(t *testing.T) {
	tests := []struct {
		name    string
		kind    error
		message string
		cause   error
		want    string
	}{
		{
			name:    "without cause",
			kind:    ErrConfig,
			message: "missing field",
			want:    "memhop: configuration error: missing field",
		},
		{
			name:    "with cause",
			kind:    ErrIO,
			message: "read failed",
			cause:   errors.New("permission denied"),
			want:    "memhop: io error: read failed: permission denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err *MemHopError
			if tt.cause != nil {
				err = NewError(tt.kind, tt.message, tt.cause)
			} else {
				err = NewError(tt.kind, tt.message)
			}
			got := err.Error()
			if got != tt.want {
				t.Errorf("Error() = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestMemHopError_Is(t *testing.T) {
	err := NewError(ErrNotFound, "record missing")

	if !errors.Is(err, ErrNotFound) {
		t.Error("errors.Is(err, ErrNotFound) should be true")
	}
	if errors.Is(err, ErrIO) {
		t.Error("errors.Is(err, ErrIO) should be false")
	}
}

func TestMemHopError_Unwrap(t *testing.T) {
	cause := errors.New("root cause")
	err := NewError(ErrIO, "wrapped", cause)

	unwrapped := errors.Unwrap(err)
	if unwrapped != cause {
		t.Errorf("Unwrap() = %v; want %v", unwrapped, cause)
	}
}

func TestNewErrorMultipleCauses(t *testing.T) {
	cause1 := errors.New("cause 1")
	cause2 := errors.New("cause 2")
	err := NewError(ErrCorruption, "corrupt data", cause1, cause2)

	if err.Cause != cause1 {
		t.Errorf("Cause should be cause1, got %v", err.Cause)
	}
}
