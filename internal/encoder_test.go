// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHttpEncoderHealthAndEmbed(t *testing.T) {
	const dim = 2
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/embed":
			var req struct {
				Model string `json:"model"`
				Input string `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode embed request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if req.Model != "mock-embed" || req.Input != "hello" {
				t.Errorf("unexpected embed request: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"embeddings": [][]float32{{0.1, 0.2}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	enc, err := NewHttpEncoder(srv.URL, dim, "mock-embed", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()

	vec, err := enc.Encode("hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != dim || vec[0] != 0.1 || vec[1] != 0.2 {
		t.Fatalf("unexpected vector: %v", vec)
	}
}

func TestHttpEncoderDimensionMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float32{{0.1, 0.2, 0.3}},
		})
	}))
	defer srv.Close()

	enc, err := NewHttpEncoder(srv.URL, 2, "mock-embed", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	if _, err := enc.Encode("hello"); err == nil {
		t.Fatal("expected dimension mismatch error")
	}
}

func TestHttpEncoderNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	enc, err := NewHttpEncoder(srv.URL, 2, "mock-embed", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	if _, err := enc.Encode("hello"); err == nil {
		t.Fatal("expected non-OK status error")
	}
}

func TestNewHttpEncoderRejectsInvalidScheme(t *testing.T) {
	if _, err := NewHttpEncoder("127.0.0.1:11434", 2, "m", 0); err == nil {
		t.Fatal("expected invalid scheme error")
	}
}
