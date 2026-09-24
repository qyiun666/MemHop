// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The token budgets are the library's only lever over how much an endpoint may answer, and every
// existing case exercises them through a fake transport — so they prove what the callers decide,
// not what the endpoint receives. If `Chat` dropped the number on the way out, the escalation
// ladder would be a no-op and every fake would still pass. This reads the request bodies off the
// wire: the ceiling a call names is the ceiling the request carries, the escalated retry carries
// the larger one, and no second request goes out when the first failure was not a truncation (or
// when the caller named no headroom to escalate into).

package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/config"
)

// recordedRequest answers with the first reply in replies and remembers the max_tokens of every
// request it served, in order.
type recordedRequest struct {
	srv       *httptest.Server
	budgets   []int
	responses []string
}

func (r *recordedRequest) serve(w http.ResponseWriter, req *http.Request) {
	if !strings.HasSuffix(req.URL.Path, "/chat/completions") {
		http.NotFound(w, req)
		return
	}
	var body struct {
		MaxTokens int `json:"max_tokens"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	r.budgets = append(r.budgets, body.MaxTokens)
	// A ladder asked more times than this case canned answers for is itself the failure being
	// looked for, so answer it rather than panicking inside the handler: the assertions below
	// then report the extra attempt, and a mutant stays a red test instead of a stack trace.
	next := wholeReply
	if len(r.budgets) <= len(r.responses) {
		next = r.responses[len(r.budgets)-1]
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(next))
}

// truncatedReply and wholeReply are the two answers the ladder distinguishes: the first says the
// ceiling cut it off, the second does not. Both are otherwise usable.
const (
	truncatedReply = `{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"{\"keywords\":[\"a\""}}]}`
	wholeReply     = `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"{\"keywords\":[\"a\",\"b\"]}"}}]}`
)

func TestOutputCeilingReachesTheRequestItWasNamedFor(t *testing.T) {
	probes := []struct {
		name        string
		responses   []string
		primary     int
		retry       int
		wantBudgets []int
		wantCode    common.Code
	}{
		{
			name: "one whole answer carries the ceiling the call named", responses: []string{wholeReply},
			primary: 777, retry: 4096, wantBudgets: []int{777},
		},
		{
			name: "a truncated answer is asked again with the larger budget", responses: []string{truncatedReply, wholeReply},
			primary: 512, retry: 2048, wantBudgets: []int{512, 2048},
		},
		{
			name: "no headroom means no second request", responses: []string{truncatedReply},
			primary: 2048, retry: 2048, wantBudgets: []int{2048}, wantCode: common.ErrLLM,
		},
	}
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			rec := &recordedRequest{responses: probe.responses}
			rec.srv = httptest.NewServer(http.HandlerFunc(rec.serve))
			t.Cleanup(rec.srv.Close)
			provider := New(config.LlmConfig{APIURL: rec.srv.URL, APIKey: "k", Model: "m"})

			_, err := provider.ChatWithRetry(context.Background(), "sys", "user", probe.primary, probe.retry)
			if code := common.CodeOf(err); code != probe.wantCode {
				t.Fatalf("ChatWithRetry answered code %d (err %v), want %d", code, err, probe.wantCode)
			}
			if len(rec.budgets) != len(probe.wantBudgets) {
				t.Fatalf("the endpoint was asked %v, want %v — the number of attempts is part of the ladder",
					rec.budgets, probe.wantBudgets)
			}
			for i, want := range probe.wantBudgets {
				if rec.budgets[i] != want {
					t.Fatalf("attempt %d carried max_tokens %d, want %d", i+1, rec.budgets[i], want)
				}
			}
		})
	}
}

// A failure that is not a truncation must not be re-asked with a bigger ceiling: the endpoint
// refused the request, and a larger budget changes nothing about that while spending a second
// window on the domain's lock.
func TestRefusedRequestIsNotEscalated(t *testing.T) {
	rec := &recordedRequest{responses: []string{`{"error":{"message":"nope"}}`}}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			rec.budgets = append(rec.budgets, 0)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(rec.responses[0]))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(rec.srv.Close)

	provider := New(config.LlmConfig{APIURL: rec.srv.URL, APIKey: "k", Model: "m"})
	_, err := provider.ChatWithRetry(context.Background(), "sys", "user", 512, 4096)
	if code := common.CodeOf(err); code != common.ErrLLM {
		t.Fatalf("a refused request is reported as code %d (%v), want ErrLLM", code, err)
	}
	if strings.Contains(err.Error(), "truncat") {
		t.Fatalf("a refusal must not be classified as a truncation: %v", err)
	}
	if len(rec.budgets) != 1 {
		t.Fatalf("a request the endpoint refused was asked %d times: a larger ceiling does not change a "+
			"refusal, and every extra attempt holds this domain's lock open for another window", len(rec.budgets))
	}
}
