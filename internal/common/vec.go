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
	for i := 0; i < dim; i++ {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return vec
}

func F32SliceToBytes(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}
