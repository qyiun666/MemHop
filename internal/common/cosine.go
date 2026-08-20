// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package common

import "math"

// CosineSimilarity computes the cosine similarity of two f32 vectors.
//
// A single scalar loop is used even for large vectors: goroutine chunking
// costs more (spawn + channel sync) than it saves below ~10k elements, and
// the default embedding dimension is only 1024.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float32
	for i := range a {
		av := a[i]
		bv := b[i]
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	return finalizeCosine(dot, normA, normB)
}

func finalizeCosine(dot, normA, normB float32) float32 {
	denom := float32(math.Sqrt(float64(normA * normB)))
	if denom < 1e-10 {
		return 0
	}
	return dot / denom
}
