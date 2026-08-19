//go:build integration

package test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/sub"
	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// TestLocomoFailDiag ingests conv-26 and classifies recall misses by QA
// category, dumping sample misses so we can see whether failures come from
// scene selection, in-scene truncation, or missing context.
func TestLocomoFailDiag(t *testing.T) {
	items := loadLocomo10(t, 1)
	item := items[0]
	cfg := &sub.MemHopConfig{
		DBPath:      filepath.Join(t.TempDir(), "diag.meh"),
		VectorDim:   1024,
		EncoderAddr: "http://127.0.0.1:11434",
		EmbedModel:  "qllama/bge-m3:q4_k_m",
		Defaults:    *sub.DefaultMemHopDefaults,
	}
	if err := testsupport.LoadLLMConfig(cfg); err != nil {
		t.Skipf("skip: %v", err)
	}
	db, err := memhop.Open(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	base := time.Now().UnixMilli()
	var seq int64
	for _, sess := range item.Sessions {
		sessionBase := locomoSessionBaseTS(sess.ID)
		if sessionBase == 0 {
			seq++
			sessionBase = base + seq*1000
		}
		var activeTopic string
		for i, tn := range sess.Turns {
			seq++
			ts := sessionBase + int64(i)*30_000
			if tn.Speaker == item.SpeakerA {
				res, err := db.Search(sub.SearchQuery{Text: tn.Text, Timestamp: ts})
				if err != nil {
					t.Fatalf("ingest: %v", err)
				}
				activeTopic = common.FormatHash(res.NewTopicID)
			} else if activeTopic != "" {
				if _, err := db.Update(activeTopic, tn.Text, ts); err != nil {
					t.Fatalf("update: %v", err)
				}
			}
		}
	}
	t.Logf("ingested %d sessions, %d turns", len(item.Sessions), seq)

	// Classify misses by category without an LLM judge: a QA counts as a hit
	// when the reference answer's tokens appear in the retrieved context
	// (lenient entity proxy), and we dump details for misses.
	type catStat struct{ total, entityHit int }
	stats := make(map[int]*catStat)
	var missSamples []string
	for _, qa := range item.QA {
		seq++
		ts := base + seq*1000
		res, err := db.Search(sub.SearchQuery{Text: qa.Question, Timestamp: ts})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		ctxText := gatherLocomoContext(db, res)
		ent := locomoEntityHit(qa.Answer, ctxText)
		s := stats[qa.Category]
		if s == nil {
			s = &catStat{}
			stats[qa.Category] = s
		}
		s.total++
		if ent > 0.99 {
			s.entityHit++
		}
		if ent < 0.5 && len(missSamples) < 8 {
			sceneIDs := ""
			for i := range res.Contexts {
				sceneIDs += fmt.Sprintf("%d,", res.Contexts[i].SceneID)
			}
			missSamples = append(missSamples, fmt.Sprintf("cat=%d | Q: %s | A: %s | ent=%.2f | scenes=[%s] | ctxTopics=%d",
				qa.Category, qa.Question, qa.Answer, ent, sceneIDs, len(res.Contexts)))
		}
	}
	for cat, s := range stats {
		t.Logf("category %d: total=%d entityHit(>=0.99)=%d (%.2f)", cat, s.total, s.entityHit, float64(s.entityHit)/float64(s.total))
	}
	for _, m := range missSamples {
		t.Logf("MISS: %s", m)
	}
}
