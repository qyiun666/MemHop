// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package encoder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"memhop/internal/common/mherrors"
	"memhop/internal/common/numeric"
)

const (
	defaultEmbedModel  = "nomic-embed-text"
	healthCheckTimeout = 5 * time.Second
	encodeTimeout      = 20 * time.Second
	healthCheckTTL     = 5 * time.Second // IsAvailable cache TTL
	// maxResponseBodyBytes caps response bodies read from the encoder service.
	maxResponseBodyBytes = 64 << 20 // 64MB
)

// HttpEncoder is an HTTP client for an Ollama embedding API.
type HttpEncoder struct {
	baseURL    string
	dim        int
	model      string
	httpClient *http.Client

	// TTL cache for IsAvailable
	healthMu   sync.Mutex
	lastCheck  time.Time
	lastResult bool
	healthTTL  time.Duration
}

// --- HTTP request / response types ---

type ollamaEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// NewHttpEncoder creates an HttpEncoder and verifies connectivity via /api/tags.
func NewHttpEncoder(baseURL string, dim int, model string) (*HttpEncoder, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, mherrors.NewError(mherrors.ErrConfig,
			fmt.Sprintf("encoder address must use http:// or https:// scheme, got: %s", baseURL))
	}
	if model == "" {
		model = defaultEmbedModel
	}

	// No client-level Timeout: per-request deadlines are set via context,
	// so health checks (5s) and encodes (20s) can share one pooled client.
	client := &http.Client{}
	e := &HttpEncoder{
		baseURL:    baseURL,
		dim:        dim,
		model:      model,
		httpClient: client,
		healthTTL:  healthCheckTTL,
	}

	if err := e.checkHealth(); err != nil {
		return nil, err
	}
	return e, nil
}

// checkHealth calls GET /api/tags to verify Ollama is reachable.
func (e *HttpEncoder) checkHealth() error {
	ctx, cancel := context.WithTimeout(context.Background(), healthCheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.baseURL+"/api/tags", nil)
	if err != nil {
		return mherrors.NewError(mherrors.ErrEncoder,
			fmt.Sprintf("Ollama health check failed at %s", e.baseURL), err)
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return mherrors.NewError(mherrors.ErrEncoder,
			fmt.Sprintf("Ollama health check failed at %s", e.baseURL), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return mherrors.NewError(mherrors.ErrEncoder,
			fmt.Sprintf("Ollama unhealthy at %s: status %d", e.baseURL, resp.StatusCode))
	}
	return nil
}

// Encode sends POST /api/embed and returns an f16-converted dense vector.
func (e *HttpEncoder) Encode(text string) (*EncoderOutput, error) {
	body, err := e.doPost("/api/embed", ollamaEmbedRequest{
		Model: e.model, Input: text,
	}, encodeTimeout)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrEncoder, "Ollama encode failed", err)
	}

	var resp ollamaEmbedResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, mherrors.NewError(mherrors.ErrEncoder, "Ollama encode decode failed", err)
	}
	if len(resp.Embeddings) == 0 {
		return nil, mherrors.NewError(mherrors.ErrEncoder, "Ollama returned no embeddings")
	}
	if len(resp.Embeddings[0]) != e.dim {
		return nil, mherrors.NewError(mherrors.ErrEncoder,
			fmt.Sprintf("dimension mismatch: expected %d, got %d", e.dim, len(resp.Embeddings[0])))
	}

	return &EncoderOutput{Dense: f32SliceToF16(resp.Embeddings[0])}, nil
}

// Dim returns the configured dimension.
func (e *HttpEncoder) Dim() int { return e.dim }

// Mode returns the encoder mode.
func (e *HttpEncoder) Mode() string { return "ollama:" + e.model }

// IsAvailable probes the Ollama service health with TTL caching.
func (e *HttpEncoder) IsAvailable() bool {
	e.healthMu.Lock()
	if time.Since(e.lastCheck) < e.healthTTL {
		result := e.lastResult
		e.healthMu.Unlock()
		return result
	}
	e.healthMu.Unlock()

	result := e.checkHealth() == nil

	e.healthMu.Lock()
	e.lastCheck = time.Now()
	e.lastResult = result
	e.healthMu.Unlock()
	return result
}

// Close releases idle HTTP connections.
func (e *HttpEncoder) Close() error {
	e.httpClient.CloseIdleConnections()
	return nil
}

// doPost sends a JSON POST request and returns the response body bytes.
func (e *HttpEncoder) doPost(path string, payload any, timeout time.Duration) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// f32SliceToF16 converts a []float32 to []uint16 using f16 encoding.
func f32SliceToF16(in []float32) []uint16 {
	out := make([]uint16, len(in))
	for i, v := range in {
		out[i] = numeric.F32ToF16(v)
	}
	return out
}
