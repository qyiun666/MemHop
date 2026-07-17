// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package health

import (
	"path/filepath"
	"testing"

	"memhop/internal/common/hash"
	"memhop/internal/core/storage"
)

// mockEncoder implements the same shape as encoder.Encoder but only for IsAvailable.
type mockEncoder struct {
	available bool
}

func (m *mockEncoder) IsAvailable() bool { return m.available }

func TestCountLayersEmpty(t *testing.T) {
	eng := createTestEngine(t)
	defer closeEngine(t, eng)

	counts := CountLayers(eng)
	wantKeys := []string{"l0_profile", "l1_engram", "l2_topic", "l3_knowledge", "l4_archive", "l5_crystal"}
	for _, k := range wantKeys {
		if counts[k] != 0 {
			t.Errorf("%s = %d; want 0", k, counts[k])
		}
	}
}

func TestCountLayersWithRecords(t *testing.T) {
	eng := createTestEngine(t)
	defer closeEngine(t, eng)

	writeRecords(t, eng, []recordDef{
		{storage.RecL0Profile, hash.HashID("profile"), []byte(`{"name":"Agent"}`)},
		{storage.RecL1SceneNode, 2, []byte("engram1")},
		{storage.RecL2Topic, 3, []byte("topic1")},
		{storage.RecL2Topic, 4, []byte("topic2")},
		{storage.RecL3GraphSlot, 5, []byte("graph1")},
		{storage.RecL4Archive, 6, []byte("archive1")},
		{storage.RecL5ActionChain, 7, []byte("crystal1")},
	})

	// L0 profile with a non-"profile" hash should NOT be counted
	writeRecords(t, eng, []recordDef{
		{storage.RecL0Profile, 99, []byte(`{"name":"Other"}`)},
	})

	counts := CountLayers(eng)

	tests := []struct {
		key  string
		want int
	}{
		{"l0_profile", 1}, // only hash of "profile" is counted
		{"l1_engram", 1},
		{"l2_topic", 2},
		{"l3_knowledge", 1},
		{"l4_archive", 1},
		{"l5_crystal", 1},
	}
	for _, tt := range tests {
		if counts[tt.key] != tt.want {
			t.Errorf("%s = %d; want %d", tt.key, counts[tt.key], tt.want)
		}
	}
}

func TestCollectIssues(t *testing.T) {
	tests := []struct {
		name   string
		enc    interface{ IsAvailable() bool }
		counts map[string]int
		want   []string
	}{
		{
			name:   "no issues",
			enc:    &mockEncoder{available: true},
			counts: map[string]int{"l2_topic": 3, "l0_profile": 1},
			want:   []string{},
		},
		{
			name:   "encoder not available",
			enc:    &mockEncoder{available: false},
			counts: map[string]int{"l2_topic": 1},
			want:   []string{"encoder not available"},
		},
		{
			name:   "nil encoder",
			enc:    nil,
			counts: map[string]int{"l2_topic": 1},
			want:   []string{"encoder not available"},
		},
		{
			name:   "no L2 topics",
			enc:    &mockEncoder{available: true},
			counts: map[string]int{"l2_topic": 0},
			want:   []string{"no L2 topics"},
		},
		{
			name:   "both issues",
			enc:    &mockEncoder{available: false},
			counts: map[string]int{"l2_topic": 0},
			want:   []string{"encoder not available", "no L2 topics"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CollectIssues(tt.enc, tt.counts)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d issues; want %d: %v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("issue[%d] = %q; want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// --- helpers ---

type recordDef struct {
	rt   uint8
	id   uint64
	data []byte
}

func createTestEngine(t *testing.T) *storage.StorageEngine {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.meh")
	eng, err := storage.Create(p, 768)
	if err != nil {
		t.Fatalf("Create engine: %v", err)
	}
	return eng
}

func closeEngine(t *testing.T, eng *storage.StorageEngine) {
	t.Helper()
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Errorf("CloseNoCheckpoint: %v", err)
	}
}

func writeRecords(t *testing.T, eng *storage.StorageEngine, records []recordDef) {
	t.Helper()
	for _, r := range records {
		_, err := eng.WriteRecord(r.rt, r.id, r.data)
		if err != nil {
			t.Fatalf("WriteRecord(type=%d, id=%d): %v", r.rt, r.id, err)
		}
	}
}
