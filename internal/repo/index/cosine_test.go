// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"math"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

func TestCosineSimilarity(t *testing.T) {
	t.Run("identical", func(t *testing.T) {
		a := []float32{1.0, 0.0, 0.0}
		sim := common.CosineSimilarity(a, a)
		if math.Abs(float64(sim-1.0)) > 1e-4 {
			t.Errorf("identical vectors should have similarity 1.0, got %f", sim)
		}
	})

	t.Run("orthogonal", func(t *testing.T) {
		a := []float32{1.0, 0.0, 0.0}
		b := []float32{0.0, 1.0, 0.0}
		sim := common.CosineSimilarity(a, b)
		if math.Abs(float64(sim)) > 1e-4 {
			t.Errorf("orthogonal vectors should have similarity ~0.0, got %f", sim)
		}
	})

	t.Run("opposite", func(t *testing.T) {
		a := []float32{1.0, 0.0, 0.0}
		b := []float32{-1.0, 0.0, 0.0}
		sim := common.CosineSimilarity(a, b)
		if math.Abs(float64(sim+1.0)) > 1e-4 {
			t.Errorf("opposite vectors should have similarity ~-1.0, got %f", sim)
		}
	})

	t.Run("zero_vector", func(t *testing.T) {
		a := []float32{0.0, 0.0}
		b := []float32{1.0, 0.0}
		sim := common.CosineSimilarity(a, b)
		if sim != 0.0 {
			t.Errorf("zero vector should give similarity 0.0, got %f", sim)
		}
	})

	t.Run("large_vector", func(t *testing.T) {
		n := 2000
		a := make([]float32, n)
		b := make([]float32, n)
		for i := range n {
			a[i] = float32(i) * 0.001
			b[i] = float32(i) * 0.001
		}
		sim := common.CosineSimilarity(a, b)
		if math.Abs(float64(sim-1.0)) > 1e-3 {
			t.Errorf("identical large vectors should have similarity ~1.0, got %f", sim)
		}
	})
}
