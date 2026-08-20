// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package common

import (
	"encoding/binary"
	"math"
)

func DecodeF32Vec(data []byte, dim int) []float32 {
	if len(data) < dim*4 {
		return nil
	}
	vec := make([]float32, dim)
	for i := range dim {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return vec
}

// DecodeF32VecInto decodes a little-endian f32 vector of the given dim
// into dst, growing it when its capacity is insufficient. The decoded
// vector is returned (either dst or the grown slice), ready to be passed
// back as dst by the caller for zero-allocation reuse across iterations.
// Unlike DecodeF32Vec, a short data buffer is reported as an error
// instead of silently yielding nil.
func DecodeF32VecInto(data []byte, dim int, dst []float32) ([]float32, error) {
	if len(data) < dim*4 {
		return dst, NewError(ErrDeserialization, "f32 vector data too short")
	}
	if cap(dst) < dim {
		dst = make([]float32, dim)
	} else {
		dst = dst[:dim]
	}
	for i := range dim {
		dst[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return dst, nil
}

func F32SliceToBytes(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}
