// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package encoder

// EncoderOutput holds the result of encoding a text.
type EncoderOutput struct {
	Dense []float32 // f32 dense vector
}

// Encoder defines the interface for text encoding.
type Encoder interface {
	// Encode returns a dense embedding for the input text.
	Encode(text string) (*EncoderOutput, error)
	// Dim returns the fixed vector dimensionality.
	Dim() int
	// Mode returns a short label identifying the encoder implementation
	// (e.g. "http", "ollama:nomic-embed-text"). Used for diagnostics.
	Mode() string
	// IsAvailable performs a lightweight health probe.
	IsAvailable() bool
}
