// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"bytes"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// A pass scheduled by a round close can lose the race with the host handing the file back, and the
// two answers that mean "the host stopped asking" are ErrClosed and ErrCancelled. Warning about
// those would put a line in every clean exit — and a warning that appears on every exit is a
// warning nobody reads. A failure the pipeline actually hit still has to reach the log, so this
// pins both halves: the silent ones and the loud one.
func TestScheduledPassLogsOnlyRealFailures(t *testing.T) {
	srv := slowLLMServer(t, 0, `{"keywords":[]}`)

	t.Run("a pass that arrives after the close stays quiet", func(t *testing.T) {
		db := newSearchTestDB(t, srv.URL)
		ac, err := db.contextFor(core.DefaultAgentID)
		if err != nil {
			t.Fatal(err)
		}
		sceneID := common.HashID("scene-never-consolidated")
		logged := captureWarnings(t)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		// The premise of the quiet arm: a pass over a closed database answers with one of the two
		// codes that mean "the host stopped asking". Without this, silence could just as well mean
		// the goroutine took some third path this test never intended to cover.
		_, err = db.RunDream(ac.OpCtx, core.DefaultAgentID, sceneID)
		if code := common.CodeOf(err); code != common.ErrClosed && code != common.ErrCancelled {
			t.Fatalf("a pass over a closed database answers %v (code %d), want ErrClosed or ErrCancelled",
				err, code)
		}
		ac.Mu.Lock()
		db.triggerSceneDream(ac, sceneID)
		ac.Mu.Unlock()
		waitForPass(t, ac, sceneID)
		if got := logged(); got != "" {
			t.Fatalf("a consolidation the host stopped asking for was warned about: %s", got)
		}
	})

	// The other arm needs a failure the library did cause: an unknown scene is the pipeline
	// refusing a real request, and it must still be reported.
	t.Run("a pass the pipeline refused is still warned about", func(t *testing.T) {
		db := newSearchTestDB(t, srv.URL)
		ac, err := db.contextFor(core.DefaultAgentID)
		if err != nil {
			t.Fatal(err)
		}
		foreign := common.HashID("scene-nobody-created")
		if _, err := db.RunDream(ac.OpCtx, core.DefaultAgentID, foreign); common.CodeOf(err) != common.ErrNotFound {
			t.Fatalf("a pass over a scene that does not exist answers %v (code %d), want ErrNotFound — "+
				"the warning arm below is untestable until that holds", err, common.CodeOf(err))
		}
		logged := captureWarnings(t)
		ac.Mu.Lock()
		db.triggerSceneDream(ac, foreign)
		ac.Mu.Unlock()
		waitForPass(t, ac, foreign)
		if got := logged(); got == "" {
			t.Fatal("the trigger swallowed a refused consolidation: the pipeline failed and nothing was reported")
		}
	})
}

// captureWarnings points the default logger at a buffer for the duration of the test and returns a
// reader of what landed there at warn level or above.
func captureWarnings(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&writer{mu: &mu, buf: &buf}, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

// writer guards the buffer: the logging goroutine and the test are different goroutines, and the
// wait below orders them only for the record, not for the race detector.
type writer struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w *writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// waitForPass blocks until the scheduled pass for one scene leaves the in-flight set. That set is
// the trigger's own bookkeeping, cleared after the log line is written, so it is what lets this
// test read the buffer without racing the goroutine.
func waitForPass(t *testing.T, ac *domain.Context, sceneID uint64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ac.Mu.Lock()
		_, pending := ac.DreamInFlight[sceneID]
		ac.Mu.Unlock()
		if !pending {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the scheduled pass never left the in-flight set, so its logging could not be observed")
}
