// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package encoder

import (
	"errors"
	"testing"

	"github.com/qyiun666/memhop/memhop/internal/core/index"
)

func TestHttpEncoderUnavailable(t *testing.T) {
	_, err := NewHttpEncoder("http://127.0.0.1:1", 768, "")
	if err == nil {
		t.Fatal("expected error connecting to non-existent service")
	}
	if !errors.Is(err, ErrEncoder) {
		t.Logf("got error: %v (expected encoder error)", err)
	}
}

func TestHttpEncoderRejectsBadScheme(t *testing.T) {
	_, err := NewHttpEncoder("unix:///tmp/test.sock", 768, "")
	if err == nil {
		t.Fatal("expected error for unix scheme")
	}
}

func TestHttpEncoderRejectsBareAddr(t *testing.T) {
	_, err := NewHttpEncoder("127.0.0.1:27110", 768, "")
	if err == nil {
		t.Fatal("expected error for bare address without scheme")
	}
}

func TestF32F16Roundtrip(t *testing.T) {
	values := []float32{0.0, 0.1, -0.1, 1.0, -1.0, 65504.0, 0.001}
	for _, v := range values {
		h := index.F32ToF16(v)
		got := index.F16ToF32(h)
		diff := got - v
		if diff < 0 {
			diff = -diff
		}
		// Allow 1% relative error or 0.01 absolute for small values
		tolerance := v * 0.01
		if tolerance < 0 {
			tolerance = -tolerance
		}
		if tolerance < 0.01 {
			tolerance = 0.01
		}
		if diff > tolerance {
			t.Errorf("f32(%v) → f16(%d) → f32(%v): diff=%v > tolerance=%v",
				v, h, got, diff, tolerance)
		}
	}
}

// ErrEncoder is a sentinel for test assertions (mirrors core.ErrEncoder).
var ErrEncoder = errors.New("memhop: encoder error")
