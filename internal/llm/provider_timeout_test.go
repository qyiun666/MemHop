// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// TimeoutSecs exists for one endpoint shape: a server that takes the connection and is slower to
// answer than the caller can wait. Nothing is recoverable from that, and a domain's lock is held
// for as long as its request is outstanding — which is why an unfilled window is refused rather
// than defaulted to "none". A number only protects its caller if it bites, so this measures the
// bite: the caller comes back inside the window it named, once (a slow endpoint is not a 429 or a
// 5xx, so the backoff loop has no business repeating the same wait), and the answer says the
// endpoint failed rather than that the caller gave up.

package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/config"
)

func TestSlowEndpointIsAbandonedInsideItsOwnWindow(t *testing.T) {
	// The endpoint answers after twice the window the caller named. Attempts are counted so a
	// client that repeated the wait cannot pass by luck: one window returns in about a second,
	// three of them plus the 500ms and 2s backoff cannot, and no runner is three times slower.
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		attempts.Add(1)
		time.Sleep(2 * time.Second)
		// A reply that would have been usable had it arrived in time: with no window, this call
		// returns content instead of an error, which is the outcome the first assertion below
		// refuses. Making it invalid would let a client that never timed out pass on a parse error.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"{\"keywords\":[\"late\"]}"}}]}`))
	}))
	t.Cleanup(srv.Close)

	provider := New(config.LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "m", TimeoutSecs: 1})
	start := time.Now()
	_, err := provider.Chat(context.Background(), "sys", "user", 512)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("an endpoint that answered after %s came back as a success", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("a 1s window took %s to give up: the timeout is either not honoured or repeated per attempt", elapsed)
	}
	if code := common.CodeOf(err); code != common.ErrLLM {
		t.Fatalf("an endpoint that never answered in time is reported as code %d (%v), want ErrLLM — "+
			"a host reading ErrCancelled goes to look at its own context instead of at the service", code, err)
	}
	if got := attempts.Load(); got > 1 {
		t.Fatalf("the window ran %d times: every repeat holds this domain's lock open for another "+
			"window, which is exactly what the caller named a timeout to avoid", got)
	}
}
