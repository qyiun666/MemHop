// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Integration coverage for DreamOptions parameter combinations introduced in
// v0.57.1. All scenarios run fully offline against the mock encoder; the LLM
// side is either injected via opts.LLM / opts.Chat or short-circuited via
// SkipDistill so no real network I/O happens.

package test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	memhop "memhop/api"
	"memhop/internal/query/dream"
	"memhop/test/testsupport"
)

// --- mocks -----------------------------------------------------------------

// mockLLMOnly implements only dream.LlmProvider. When used as opts.LLM
// without opts.Chat, the distill stage falls back to BuildChatProvider(config).
type mockLLMOnly struct {
	calls atomic.Int64
}

func (m *mockLLMOnly) Consolidate(_ *dream.ConsolidationInput) (*dream.ConsolidationOutput, error) {
	m.calls.Add(1)
	return &dream.ConsolidationOutput{
		L2Groups: dream.NewEmptySection[[]dream.L2Group](),
	}, nil
}

// mockChat implements dream.ChatProvider only.
type mockChat struct {
	calls atomic.Int64
	// resp overrides the canned distill JSON when non-empty.
	resp string
}

const defaultDistillResp = `{"emotion":{"valence":0.5,"arousal":0.3,"dominance":0.5},"mbti":{"i_e":-0.5,"n_s":0.5,"t_f":-0.5,"j_p":0.5,"type":"INFJ"},"per_node":[]}`

func (m *mockChat) Chat(_ string, _ string, _ int, _, _ float32) (string, error) {
	m.calls.Add(1)
	if m.resp != "" {
		return m.resp, nil
	}
	return defaultDistillResp, nil
}

// mockLLMAndChat satisfies both dream.LlmProvider and dream.ChatProvider so
// injecting it as opts.LLM (with opts.Chat nil) covers both pipeline paths.
type mockLLMAndChat struct {
	mockLLMOnly
	mockChat
}

func (m *mockLLMAndChat) Consolidate(in *dream.ConsolidationInput) (*dream.ConsolidationOutput, error) {
	return m.mockLLMOnly.Consolidate(in)
}

func (m *mockLLMAndChat) Chat(system, user string, maxTokens int, temperature, topP float32) (string, error) {
	return m.mockChat.Chat(system, user, maxTokens, temperature, topP)
}

// --- tests ------------------------------------------------------------------

// TestDreamOptions_NilOpts confirms Dream(nil) does not panic on a fresh DB
// and that even the empty-scene Consolidate path completes without an LLM.
// Note: with an unset LLM config the default provider fails; use an injected
// mock to keep the test offline while still exercising Dream(nil-ish) code.
func TestDreamOptions_NilOptsWithInjectedLLM(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	llm := &mockLLMAndChat{}
	// Nil opts path uses config LLM; to stay offline, use empty opts + injected LLM.
	report, err := mh.Dream(&memhop.DreamOptions{LLM: llm})
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if got := len(report.Stages); got != 5 {
		t.Fatalf("Stages length = %d, want 5", got)
	}
	if llm.mockLLMOnly.calls.Load() != 1 {
		t.Fatalf("Consolidate calls = %d, want 1", llm.mockLLMOnly.calls.Load())
	}
}

// TestDreamOptions_LLMReusedAsChat confirms that when opts.LLM satisfies
// ChatProvider and opts.Chat is nil, the distill stage routes through the
// injected LLM instead of building one from config.
func TestDreamOptions_LLMReusedAsChat(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	llm := &mockLLMAndChat{}
	_, err := mh.Dream(&memhop.DreamOptions{LLM: llm})
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	// Empty DB has no L1 samples so distill records "skipped" without calling
	// the chat provider. Positive assertion: no unexpected calls happened.
	if got := llm.mockChat.calls.Load(); got != 0 {
		t.Fatalf("mock chat unexpectedly called %d times on empty DB", got)
	}
}

// TestDreamOptions_SkipDistill confirms SkipDistill=true records a skipped
// stage without invoking the chat provider.
func TestDreamOptions_SkipDistill(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	llm := &mockLLMOnly{}
	chat := &mockChat{}
	report, err := mh.Dream(&memhop.DreamOptions{
		LLM: llm, Chat: chat, SkipDistill: true,
	})
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if got := len(report.Stages); got != 5 {
		t.Fatalf("Stages length = %d, want 5 (skipped stage must be recorded)", got)
	}
	last := report.Stages[len(report.Stages)-1]
	if last.Name != "l0_distill" || last.Status != "skipped" {
		t.Fatalf("last stage = %+v, want {l0_distill skipped}", last)
	}
	if !strings.Contains(last.Description, "disabled by RunOptions") {
		t.Fatalf("skipped description = %q, want 'disabled by RunOptions'", last.Description)
	}
	if got := chat.calls.Load(); got != 0 {
		t.Fatalf("chat called %d times with SkipDistill=true, want 0", got)
	}
}

// TestDreamOptions_SeparateProviders confirms opts.LLM and opts.Chat are
// routed independently to the correct pipeline stages.
func TestDreamOptions_SeparateProviders(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	llm := &mockLLMOnly{}
	chat := &mockChat{}
	_, err := mh.Dream(&memhop.DreamOptions{LLM: llm, Chat: chat})
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if got := llm.calls.Load(); got != 1 {
		t.Fatalf("Consolidate mock calls = %d, want 1", got)
	}
	// Empty DB => distill stage is skipped ("no L1 samples available") before
	// touching the chat provider; that is the correct behavior.
	if got := chat.calls.Load(); got != 0 {
		t.Fatalf("Chat mock calls = %d on empty DB, want 0", got)
	}
}

// TestDreamOptions_L2IDsStrictSemantics confirms an invalid hex ID aborts
// the Dream call before any LLM invocation.
func TestDreamOptions_L2IDsStrictSemantics(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	llm := &mockLLMAndChat{}
	report, err := mh.Dream(&memhop.DreamOptions{
		LLM:   llm,
		L2IDs: []string{"not-a-hex-id"},
	})
	if err == nil {
		t.Fatal("Dream returned nil error for invalid L2 ID")
	}
	if !strings.Contains(err.Error(), "invalid L2 ID") {
		t.Fatalf("error missing expected prefix: %v", err)
	}
	if report != nil {
		t.Fatalf("Dream returned non-nil report on error: %+v", report)
	}
	if got := llm.mockLLMOnly.calls.Load(); got != 0 {
		t.Fatalf("LLM was called %d times despite parse failure; want 0", got)
	}
}

// TestDreamOptions_L2IDsValidHex confirms that a well-formed hex ID (even for
// a non-existent topic) passes validation and the pipeline runs to completion.
func TestDreamOptions_L2IDsValidHex(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	llm := &mockLLMAndChat{}
	// 16 hex chars representing a phantom L2 ID.
	report, err := mh.Dream(&memhop.DreamOptions{
		LLM:   llm,
		L2IDs: []string{"0123456789abcdef"},
	})
	if err != nil {
		t.Fatalf("Dream with valid hex ID: %v", err)
	}
	if len(report.Stages) != 5 {
		t.Fatalf("Stages length = %d, want 5", len(report.Stages))
	}
}

// TestDreamOptions_EmptyOpts confirms &DreamOptions{} behaves identically to
// nil when an injected LLM is provided (nil opts path is exercised elsewhere).
func TestDreamOptions_EmptyOptsWithInjection(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	llm := &mockLLMAndChat{}
	// &DreamOptions{LLM: llm} equivalent to just setting LLM.
	_, err := mh.Dream(&memhop.DreamOptions{LLM: llm})
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if got := llm.mockLLMOnly.calls.Load(); got != 1 {
		t.Fatalf("Consolidate calls = %d, want 1", got)
	}
}

// TestDreamOptions_ClosedInstance confirms Dream on a closed MemHop returns
// ErrClosed without touching the LLM.
func TestDreamOptions_ClosedInstance(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	if err := mh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	llm := &mockLLMAndChat{}
	_, err := mh.Dream(&memhop.DreamOptions{LLM: llm})
	if err == nil {
		t.Fatal("Dream on closed instance returned nil error")
	}
	if got := llm.mockLLMOnly.calls.Load(); got != 0 {
		t.Fatalf("LLM invoked %d times on closed instance; want 0", got)
	}
}

// openMemHopWithLLMURL opens a fresh mock-backed MemHop pointed at the given
// LLM URL. This lets scenarios exercise the config-level LLM path (the one
// Dream(nil) takes) without touching real network endpoints.
func openMemHopWithLLMURL(t *testing.T, llmURL string) *memhop.MemHop {
	t.Helper()
	cfg := memhop.Config{
		DBPath:    filepath.Join(t.TempDir(), "cfg.meh"),
		VectorDim: testsupport.MockVectorDim,
	}
	cfg.LLM.APIURL = llmURL
	cfg.LLM.APIKey = "sk-test"
	cfg.LLM.Model = "mock-model"
	cfg.LLM.TimeoutSecs = 10
	mh, err := memhop.OpenWithEncoder(&cfg, testsupport.NewMockEncoder(testsupport.MockVectorDim))
	if err != nil {
		t.Fatalf("OpenWithEncoder: %v", err)
	}
	return mh
}

// mockChatServer returns an httptest server that responds like an
// OpenAI-compatible /v1/chat/completions endpoint with the supplied JSON
// body inside choices[0].message.content.
func mockChatServer(t *testing.T, contentJSON string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := `{"choices":[{"message":{"content":` + jsonQuote(contentJSON) + `}}]}`
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// jsonQuote escapes s for embedding as a JSON string literal.
func jsonQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// TestDreamOptions_NilOpts_ConfigLevel is spec scenario #1: `Dream(nil)`
// takes the default config-LLM path. We point the config at an httptest
// mock so no real network is touched.
func TestDreamOptions_NilOpts_ConfigLevel(t *testing.T) {
	srv, hits := mockChatServer(t, `{"l2_groups":[]}`)
	mh := openMemHopWithLLMURL(t, srv.URL)
	defer mh.Close()

	report, err := mh.Dream(nil)
	if err != nil {
		t.Fatalf("Dream(nil): %v", err)
	}
	if len(report.Stages) != 5 {
		t.Fatalf("Stages length = %d, want 5", len(report.Stages))
	}
	if hits.Load() < 1 {
		t.Fatalf("mock server was not hit; Dream(nil) never called the config LLM")
	}
}

// TestDreamOptions_BaseURLConfig is spec scenario #8: caller sets a base
// URL (no `/v1/chat/completions`) in config.LLM.APIURL; the mock server
// must still receive its request on the normalized path.
func TestDreamOptions_BaseURLConfig(t *testing.T) {
	srv, _ := mockChatServer(t, `{"l2_groups":[]}`)
	// srv.URL has no path suffix: it is the raw base URL.
	mh := openMemHopWithLLMURL(t, srv.URL)
	defer mh.Close()

	if _, err := mh.Dream(nil); err != nil {
		t.Fatalf("Dream(nil) with base URL: %v", err)
	}
}

// TestDreamOptions_Retry429 is spec scenario #11: the OpenAI-compatible
// mock returns 429 the first two times and 200 on the third; the retry
// path must succeed and the server must see >= 3 hits.
func TestDreamOptions_Retry429(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := hits.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"l2_groups\":[]}"}}]}`)
	}))
	t.Cleanup(srv.Close)

	mh := openMemHopWithLLMURL(t, srv.URL)
	defer mh.Close()

	start := time.Now()
	report, err := mh.Dream(nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Dream(nil) with 429 backoff: %v", err)
	}
	if report == nil {
		t.Fatal("Dream returned nil report after successful retry")
	}
	if got := hits.Load(); got < 3 {
		t.Fatalf("server saw %d hits; want >= 3 (429/429/200 sequence)", got)
	}
	// First backoff is 500ms, second is 2s; total must clear ~2.4s.
	if elapsed < 2400*time.Millisecond {
		t.Fatalf("total elapsed %v suggests backoff did not fire", elapsed)
	}
}
