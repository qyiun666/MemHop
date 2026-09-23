// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Transport classification tests: what the provider answers when the endpoint
// gave an answer that cannot be used, and when the caller gave up mid-backoff.

package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/config"
)

// answerWith serves one fixed reply to every chat completion; the optional hook
// runs after the bytes are on the wire, which is how a test lands a cancellation
// behind a response instead of in front of it.
func answerWith(t *testing.T, status int, body string, after func()) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
		if after != nil {
			after()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testProvider(url string) *Provider {
	return New(config.LlmConfig{APIURL: url, APIKey: "test", Model: "mock"})
}

// An answer cut off at the ceiling is normally caught by the escalation retry, so
// the one that survives it is the caller's last word — and a caller reading the
// error code has to be told something failed rather than handed code 0.
func TestTruncatedAnswerCarriesTheLLMCode(t *testing.T) {
	srv := answerWith(t, http.StatusOK,
		`{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"{\"keywords\":[\"a\""}}]}`, nil)

	_, err := testProvider(srv.URL).Chat(context.Background(), "sys", "user", 512)
	if common.CodeOf(err) != common.ErrLLM {
		t.Fatalf("a truncated answer must carry a code, got %d (%v)", common.CodeOf(err), err)
	}
	if !errors.Is(err, common.ErrTruncated) {
		t.Fatalf("the truncation signal must survive the wrapper, got %v", err)
	}
}

// A caller that goes away is not an endpoint that refused: reported as a model
// failure, the host goes to check a service that answered. The cancellation can
// land in either of two waits — the request in flight, or the backoff before the
// retry — and both have to say the same thing.
func TestCallAbandonedDuringTheRequestCarriesTheCancellationCode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := answerWith(t, http.StatusInternalServerError,
		`{"error":{"message":"boom","type":"server_error"}}`, cancel)

	_, err := testProvider(srv.URL).Chat(ctx, "sys", "user", 512)
	if common.CodeOf(err) != common.ErrCancelled {
		t.Fatalf("a call its caller abandoned must report ErrCancelled, got %d (%v)",
			common.CodeOf(err), err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the cancellation must stay visible to errors.Is, got %v", err)
	}
}

func TestCallAbandonedDuringTheBackoffCarriesTheCancellationCode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The first attempt is a retryable status, so the transport sits in its 500 ms
	// wait; 20 ms is soon enough to land inside that window and slow enough that
	// the request itself has been answered. Which of the two waits the cancel
	// interrupts is not what this claims — that both report ErrCancelled is.
	timer := time.AfterFunc(20*time.Millisecond, cancel)
	defer timer.Stop()
	srv := answerWith(t, http.StatusInternalServerError,
		`{"error":{"message":"boom","type":"server_error"}}`, nil)

	_, err := testProvider(srv.URL).Chat(ctx, "sys", "user", 512)
	if common.CodeOf(err) != common.ErrCancelled {
		t.Fatalf("a call abandoned in its backoff must report ErrCancelled, got %d (%v)",
			common.CodeOf(err), err)
	}
}

// A gateway that refuses with a whole HTML page is not a reason to paste that page
// into an error the caller sees and the log line carries. The head is what has
// the diagnosis in it, and a cut that lands inside a multi-byte character would
// otherwise turn a Chinese gateway message into replacement noise.
func TestUpstreamErrorBodyIsEchoedBounded(t *testing.T) {
	page := strings.Repeat("<html><body>请求被网关拒绝：", 600)
	srv := answerWith(t, http.StatusBadRequest, page, nil)

	_, err := testProvider(srv.URL).Chat(context.Background(), "sys", "user", 512)
	if common.CodeOf(err) != common.ErrLLM {
		t.Fatalf("an endpoint that refused must report ErrLLM, got %d (%v)", common.CodeOf(err), err)
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("the status must survive the clamp, got %q", err.Error())
	}
	if len(err.Error()) > 2*maxUpstreamEcho {
		t.Fatalf("a %d-byte refusal page must not be echoed whole: %d bytes of error text",
			len(page), len(err.Error()))
	}
	if !utf8.ValidString(err.Error()) {
		t.Fatalf("clamping must not cut a multi-byte message into invalid UTF-8: %q", err.Error())
	}

	// Inside the budget the message is the endpoint's own words, untouched. A JSON
	// body would be parsed rather than echoed, so this checks the pass-through with
	// the same shape the oversized one above had.
	short := "<html><body>额度不足</body></html>"
	smallSrv := answerWith(t, http.StatusBadRequest, short, nil)
	_, err = testProvider(smallSrv.URL).Chat(context.Background(), "sys", "user", 512)
	if err == nil || !strings.Contains(err.Error(), short) {
		t.Fatalf("a body within the budget must come back verbatim, got %v", err)
	}
}

// The two budgets follow the vocabulary the tuning knobs use: an unfilled value takes the
// library default. Both directions of failure are silent, which is why this is asserted -
// a zero output ceiling would truncate every answer to nothing, and a zero HTTP timeout is
// not "instant" but "forever", holding a domain's lock open on an endpoint that never
// answers. A host that fills in only the endpoint therefore gets a working client.
func TestBudgetsTakeUnfilledValuesAsDefaults(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		in                         config.LlmConfig
		wantTimeout, wantMaxTokens int
	}{
		{"nothing filled", config.LlmConfig{APIURL: "http://x", APIKey: "k", Model: "m"}, defaultTimeoutSecs, defaultMaxOutputTokens},
		{"negative values", config.LlmConfig{TimeoutSecs: -1, MaxOutputTokens: -1}, defaultTimeoutSecs, defaultMaxOutputTokens},
		{"one filled, one not", config.LlmConfig{TimeoutSecs: 7}, 7, defaultMaxOutputTokens},
		{"both filled", config.LlmConfig{TimeoutSecs: 30, MaxOutputTokens: 1024}, 30, 1024},
	} {
		timeout, maxTokens := budgets(tc.in)
		if timeout != tc.wantTimeout || maxTokens != tc.wantMaxTokens {
			t.Errorf("%s: budgets(%+v) = (%d, %d), want (%d, %d)",
				tc.name, tc.in, timeout, maxTokens, tc.wantTimeout, tc.wantMaxTokens)
		}
	}
	// The construction path must actually carry them: what the prompt budget arithmetic
	// reads back is the provider's own ceiling, not the caller's zero.
	if got := New(config.LlmConfig{APIURL: "http://x", APIKey: "k", Model: "m"}).MaxOutputTokens(); got != defaultMaxOutputTokens {
		t.Errorf("an unset MaxOutputTokens reached the provider as %d, want %d", got, defaultMaxOutputTokens)
	}
}
