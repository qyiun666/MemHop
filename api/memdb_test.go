// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"errors"
	"path/filepath"
	"testing"

	"memhop/internal/core"
	"memhop/internal/core/encoder"
)

// fakeEncoder returns zero vectors of a fixed dimension.
type fakeEncoder struct{ dim int }

func (f *fakeEncoder) Encode(text string) (*encoder.EncoderOutput, error) {
	return &encoder.EncoderOutput{Dense: make([]uint16, f.dim)}, nil
}
func (f *fakeEncoder) Dim() int          { return f.dim }
func (f *fakeEncoder) Mode() string      { return "fake" }
func (f *fakeEncoder) IsAvailable() bool { return true }

// Regression: opening a database with a mismatched VectorDim must fail with
// ErrVectorDimMismatch without destroying the on-disk index snapshot.
func TestOpenVectorDimMismatchPreservesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dim.meh")
	const dim = 8
	cfg := &core.MemHopConfig{DBPath: path, VectorDim: dim}

	db, err := OpenWithEncoder(cfg, &fakeEncoder{dim: dim})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.BatchStore(StoreBatch{Items: []StoreItem{
		{Content: "memhop crash recovery test", Keywords: []string{"memhop"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen with a wrong dimension: must fail with ErrVectorDimMismatch.
	badCfg := &core.MemHopConfig{DBPath: path, VectorDim: dim * 2}
	if _, err := OpenWithEncoder(badCfg, &fakeEncoder{dim: dim * 2}); !errors.Is(err, core.ErrVectorDimMismatch) {
		t.Fatalf("want ErrVectorDimMismatch, got %v", err)
	}

	// Reopen with the correct dimension: the failed open above must not have
	// destroyed the snapshot or the stored records.
	db2, err := OpenWithEncoder(cfg, &fakeEncoder{dim: dim})
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	sd := db2.engine.SnapshotData()
	if sd == nil || len(sd.SparseData) == 0 {
		t.Fatalf("index snapshot lost after mismatched open: %+v", sd)
	}
	graph, err := db2.GetL1Graph(nil)
	if err != nil {
		t.Fatal(err)
	}
	if graph == nil || len(graph.Nodes) == 0 {
		t.Fatal("stored L1 nodes missing after mismatched open")
	}
}
