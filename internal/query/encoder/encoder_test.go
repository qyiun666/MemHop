// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package encoder

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/ollama/ollama/api"
)

func TestHttpEncoderUnavailable(t *testing.T) {
	_, err := NewHttpEncoder("http://127.0.0.1:1", 768, "test-embed", 0)
	if err == nil {
		t.Fatal("expected error connecting to non-existent service")
	}
	if !errors.Is(err, ErrEncoder) {
		t.Logf("got error: %v (expected encoder error)", err)
	}
}

func TestHttpEncoderRejectsBadScheme(t *testing.T) {
	_, err := NewHttpEncoder("unix:///tmp/test.sock", 768, "test-embed", 0)
	if err == nil {
		t.Fatal("expected error for unix scheme")
	}
}

func TestHttpEncoderRejectsBareAddr(t *testing.T) {
	_, err := NewHttpEncoder("127.0.0.1:27110", 768, "test-embed", 0)
	if err == nil {
		t.Fatal("expected error for bare address without scheme")
	}
}

// ErrEncoder is a sentinel for test assertions (mirrors mherrors.ErrEncoder).
var ErrEncoder = errors.New("memhop: encoder error")

// newTestClient creates an api.Client pointing at the given test server.
func newTestClient(srv *httptest.Server) *api.Client {
	parsed, _ := url.Parse(srv.URL)
	return api.NewClient(parsed, &http.Client{})
}

func TestHttpEncoderEncode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/":
			// Heartbeat
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/embed":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"embeddings":[[0.1,0.2]]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	e, err := NewHttpEncoder(srv.URL, 2, "test-embed", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	out, err := e.Encode("hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Dense) != 2 {
		t.Fatalf("dense dim: %d, want 2", len(out.Dense))
	}
	if got := out.Dense[0]; got < 0.09 || got > 0.11 {
		t.Fatalf("dense[0]: %v, want ~0.1", got)
	}
}

func TestHttpEncoderEncodeTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := &HttpEncoder{client: newTestClient(srv), httpClient: &http.Client{}, dim: 2, model: "m"}
	_, err := e.Encode("hello")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestHttpEncoderEncodeNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	e := &HttpEncoder{client: newTestClient(srv), httpClient: &http.Client{}, dim: 2, model: "m"}
	if _, err := e.Encode("hello"); err == nil {
		t.Fatal("expected error for non-200 response")
	}
}
