// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package numeric

import "math"

// CosineSimilarity computes the cosine similarity of two f16 vectors (uint16 slices).
// Uses goroutine chunking for large vectors: each chunk handles 500 elements, up to 10 goroutines.
func CosineSimilarity(a, b []uint16) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	const chunkSize = 500
	const maxGoroutines = 10
	n := len(a)

	if n < chunkSize*2 {
		return cosineScalar(a, b)
	}

	numChunks := (n + chunkSize - 1) / chunkSize
	if numChunks > maxGoroutines {
		numChunks = maxGoroutines
	}

	type partial struct {
		dot, normA, normB float32
	}
	results := make([]partial, numChunks)
	done := make(chan struct{}, numChunks)

	for i := 0; i < numChunks; i++ {
		start := i * (n / numChunks)
		end := start + n/numChunks
		if i == numChunks-1 {
			end = n
		}
		go func(idx, s, e int) {
			var dot, na, nb float32
			for j := s; j < e; j++ {
				av := F16ToF32(a[j])
				bv := F16ToF32(b[j])
				dot += av * bv
				na += av * av
				nb += bv * bv
			}
			results[idx] = partial{dot, na, nb}
			done <- struct{}{}
		}(i, start, end)
	}

	for i := 0; i < numChunks; i++ {
		<-done
	}

	var dot, normA, normB float32
	for _, r := range results {
		dot += r.dot
		normA += r.normA
		normB += r.normB
	}
	return finalizeCosine(dot, normA, normB)
}

func cosineScalar(a, b []uint16) float32 {
	var dot, normA, normB float32
	for i := range a {
		av := F16ToF32(a[i])
		bv := F16ToF32(b[i])
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
