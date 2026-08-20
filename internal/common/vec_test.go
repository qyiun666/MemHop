// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package common

import (
	"math"
	"testing"
)

func equalF32Bits(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Float32bits(a[i]) != math.Float32bits(b[i]) {
			return false
		}
	}
	return true
}

func TestDecodeF32VecIntoConsistentWithDecodeF32Vec(t *testing.T) {
	cases := [][]float32{
		{},
		{0},
		{1.5},
		{-1, -2, -3},
		{1, 2, 3, 4},
		{0.1, -0.2, 3.14, 2.718, -1e-9, 1e9},
		{float32(math.Inf(1)), float32(math.Inf(-1)), float32(math.NaN()), 0},
	}
	for i, want := range cases {
		data := F32SliceToBytes(want)
		got, err := DecodeF32VecInto(data, len(want), nil)
		if err != nil {
			t.Fatalf("case %d: unexpected error: %v", i, err)
		}
		if !equalF32Bits(got, want) {
			t.Fatalf("case %d: got %v want %v", i, got, want)
		}
		ref := DecodeF32Vec(data, len(want))
		if !equalF32Bits(got, ref) {
			t.Fatalf("case %d: mismatch with DecodeF32Vec: got %v ref %v", i, got, ref)
		}
	}
}

func TestDecodeF32VecIntoReusesBuffer(t *testing.T) {
	data := F32SliceToBytes([]float32{1, 2, 3, 4, 5})
	dst := make([]float32, 0, 8)
	out, err := DecodeF32VecInto(data, 5, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 5 || &out[:1][0] != &dst[:1][0] {
		t.Fatalf("expected reuse of dst backing array, got len=%d", len(out))
	}
	if !equalF32Bits(out, []float32{1, 2, 3, 4, 5}) {
		t.Fatalf("values corrupted: %v", out)
	}
	// second iteration reuses the same backing array
	out2, err := DecodeF32VecInto(data, 5, out)
	if err != nil {
		t.Fatal(err)
	}
	if &out2[0] != &out[0] {
		t.Fatal("second call should reuse the same backing array")
	}
	if !equalF32Bits(out2, []float32{1, 2, 3, 4, 5}) {
		t.Fatalf("values corrupted after reuse: %v", out2)
	}
}

func TestDecodeF32VecIntoGrowsBuffer(t *testing.T) {
	small := make([]float32, 0, 2)
	big := []float32{1, 2, 3, 4, 5, 6, 7, 8}
	data := F32SliceToBytes(big)
	out, err := DecodeF32VecInto(data, len(big), small)
	if err != nil {
		t.Fatal(err)
	}
	if &out[:1][0] == &small[:1][0] {
		t.Fatal("expected a new grown buffer when capacity is insufficient")
	}
	if cap(out) < len(big) {
		t.Fatalf("grown buffer cap %d < dim %d", cap(out), len(big))
	}
	if !equalF32Bits(out, big) {
		t.Fatalf("values corrupted: %v", out)
	}
	// the grown buffer is reusable for a smaller dim
	smallData := F32SliceToBytes([]float32{9})
	out2, err := DecodeF32VecInto(smallData, 1, out)
	if err != nil {
		t.Fatal(err)
	}
	if &out2[0] != &out[0] {
		t.Fatal("grown buffer should be reused for a smaller dim")
	}
	if len(out2) != 1 || out2[0] != 9 {
		t.Fatalf("got %v want [9]", out2)
	}
}

func TestDecodeF32VecIntoShortData(t *testing.T) {
	data := F32SliceToBytes([]float32{1, 2})
	dst := make([]float32, 0, 4)
	out, err := DecodeF32VecInto(data, 3, dst)
	if err == nil {
		t.Fatal("expected error for short data")
	}
	if CodeOf(err) != ErrDeserialization {
		t.Fatalf("expected ErrDeserialization, got %d", CodeOf(err))
	}
	if len(out) != 0 || cap(out) != cap(dst) {
		t.Fatalf("short data should return dst unchanged, got len=%d cap=%d", len(out), cap(out))
	}
	// a nil dst must also produce an error, not a nil decode
	if out2, err := DecodeF32VecInto(nil, 1, nil); err == nil || out2 != nil {
		t.Fatal("nil data with dim 1 must error and return the nil dst")
	}
}

func TestDecodeF32VecIntoIgnoresTrailingBytes(t *testing.T) {
	data := F32SliceToBytes([]float32{1, 2, 3})
	data = append(data, 0xAB, 0xCD) // trailing garbage beyond dim*4
	out, err := DecodeF32VecInto(data, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !equalF32Bits(out, []float32{1, 2, 3}) {
		t.Fatalf("got %v", out)
	}
}

func TestDecodeF32VecIntoZeroDim(t *testing.T) {
	out, err := DecodeF32VecInto(nil, 0, nil)
	if err != nil {
		t.Fatalf("dim 0 should succeed: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty result, got %v", out)
	}
}
