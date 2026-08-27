// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Test fixtures of the scenefind capability: engine + topic writers and the
// fixed-vector encoder mock (mirrors internal/testutil_test.go, which the
// composition-root tests share).

package scenefind

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// mockEncoder returns a fixed vector, simulating an available encoder.
type mockEncoder struct {
	vec []float32
}

func (m *mockEncoder) Encode(string) ([]float32, error) { return m.vec, nil }

//nolint:unused // satisfies the host encoder shape
func (m *mockEncoder) Dim() int          { return len(m.vec) }
func (m *mockEncoder) Mode() string      { return "mock" }
func (m *mockEncoder) IsAvailable() bool { return true }

// testVec is shared with topic vector pages: identical 768-dim vector, cosine 1.0.
var testVec = func() []float32 {
	v := make([]float32, 768)
	for i := range v {
		v[i] = 0.5
	}
	return v
}()

func TestMain(m *testing.M) {
	if err := index.InitTokenizer(index.EngineAuto); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func newTestEngine(t *testing.T) *core.StorageEngine {
	t.Helper()
	engine, err := core.Create(filepath.Join(t.TempDir(), "test.meh"), 768)
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { engine.Close(&core.IndexSnapshotData{}) })
	return engine
}

func newTopic(id, scene uint64, ts int64, kws []string) core.TopicSlot {
	return core.TopicSlot{
		ID:            id,
		SceneID:       scene,
		Depth:         1,
		UserKeywords:  kws,
		UserTimestamp: ts,
	}
}

// writeTopic writes a topic record + sparse index into one agent domain;
// writes the fixed vector page when CentroidPageRef != 0.
func writeTopic(t *testing.T, engine *core.StorageEngine, sparse *index.SparseIndex, agentID uint64, topic core.TopicSlot) {
	t.Helper()
	data, err := json.Marshal(topic)
	if err != nil {
		t.Fatalf("marshal topic: %v", err)
	}
	if _, err := engine.WriteRecord(agentID, core.RecL2Topic, topic.ID, data); err != nil {
		t.Fatalf("write topic: %v", err)
	}
	fields := make([]string, 0, len(topic.FusedKeywords)+len(topic.UserKeywords)+len(topic.AgentKeywords))
	fields = append(fields, topic.FusedKeywords...)
	fields = append(fields, topic.UserKeywords...)
	fields = append(fields, topic.AgentKeywords...)
	terms := index.Tokenize(strings.Join(fields, " "))
	sparse.AddDocument(topic.ID, terms, uint32(len(terms)))
	if topic.CentroidPageRef != 0 {
		if _, err := engine.WriteRecord(agentID, core.RecVecCentroid, topic.CentroidPageRef, common.F32SliceToBytes(testVec)); err != nil {
			t.Fatalf("write vector: %v", err)
		}
	}
}

func approx(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-4 }

func mustParse(t *testing.T, s string) uint64 {
	t.Helper()
	v, err := common.ParseID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}
