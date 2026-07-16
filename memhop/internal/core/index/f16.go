// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package index implements in-memory indexes: BM25 sparse and L2 metadata.
package index

import "math"

// F16ToF32 converts an IEEE 754 binary16 (uint16) to float32.
func F16ToF32(h uint16) float32 {
	sign := uint32(h>>15) & 1
	exp := uint32(h>>10) & 0x1F
	mant := uint32(h) & 0x3FF

	switch exp {
	case 0: // subnormal or zero
		if mant == 0 {
			return math.Float32frombits(sign << 31)
		}
		// subnormal: (-1)^sign × 2^-14 × (mant/1024)
		f := float32(mant) / 1024.0 * float32(math.Pow(2, -14))
		if sign == 1 {
			f = -f
		}
		return f
	case 0x1F: // inf or NaN
		if mant == 0 {
			return math.Float32frombits((sign << 31) | 0x7F800000)
		}
		return math.Float32frombits((sign << 31) | 0x7F800000 | (mant << 13))
	default: // normal
		f32Exp := (exp - 15 + 127) << 23
		f32Mant := mant << 13
		return math.Float32frombits((sign << 31) | f32Exp | f32Mant)
	}
}

// F32ToF16 converts a float32 to IEEE 754 binary16 (uint16).
func F32ToF16(f float32) uint16 {
	bits := math.Float32bits(f)
	sign := uint16((bits >> 31) << 15)
	exp := int32((bits>>23)&0xFF) - 127 + 15
	mant := bits & 0x7FFFFF

	switch {
	case (bits & 0x7FFFFFFF) == 0: // ±0
		return sign
	case exp <= 0: // subnormal or underflow
		if exp < -10 {
			return sign
		}
		// subnormal f16: m already holds the 10-bit mantissa
		m := (mant | 0x800000) >> uint(14-exp)
		return sign | uint16(m)
	case exp >= 31: // overflow → inf
		return sign | 0x7C00
	default: // normal
		return sign | uint16(exp<<10) | uint16(mant>>13)
	}
}
