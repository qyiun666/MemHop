// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package encoder provides the embedding encoder implementation backed by
// the Ollama HTTP API. HttpEncoder satisfies the scenefind.Encoder narrow
// interface (Encode returns a dense f32 vector, IsAvailable probes health).
package sub

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ollama/ollama/api"
	"github.com/qyiun666/MemHop/internal/sub/common"
)

const (
	healthCheckTimeout   = 5 * time.Second
	defaultEncodeTimeout = 20 * time.Second
	healthCheckTTL       = 5 * time.Second // IsAvailable cache TTL
)

// HttpEncoder is an HTTP client for an Ollama embedding API.
type HttpEncoder struct {
	client        *api.Client
	httpClient    *http.Client
	dim           int
	model         string
	encodeTimeout time.Duration
	// TTL cache for IsAvailable
	healthMu   sync.Mutex
	lastCheck  time.Time
	lastResult bool
	healthTTL  time.Duration
}

func CreateEncoder(cfg *MemHopConfig) (*HttpEncoder, error) {
	if cfg.EncoderAddr == "" {
		return nil, common.NewError(common.ErrConfig,
			"Config.EncoderAddr is required")
	}
	return NewHttpEncoder(cfg.EncoderAddr, cfg.VectorDim, cfg.EmbedModel, cfg.EncoderTimeoutSecs)
}

// the documented 20-second default.
func NewHttpEncoder(baseURL string, dim int, model string, timeoutSecs int) (*HttpEncoder, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, common.NewError(common.ErrConfig,
			fmt.Sprintf("encoder address must use http:// or https:// scheme, got: %s", baseURL))
	}
	if model == "" {
		return nil, common.NewError(common.ErrConfig, "embed model is required")
	}
	encodeTimeout := defaultEncodeTimeout
	if timeoutSecs > 0 {
		encodeTimeout = time.Duration(timeoutSecs) * time.Second
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, common.NewError(common.ErrConfig,
			fmt.Sprintf("invalid encoder address %s", baseURL), err)
	}

	// No client-level Timeout: per-request deadlines are set via context,
	// so health checks (5s) and encodes can share one pooled client.
	httpClient := &http.Client{}
	e := &HttpEncoder{
		client:        api.NewClient(parsed, httpClient),
		httpClient:    httpClient,
		dim:           dim,
		model:         model,
		encodeTimeout: encodeTimeout,
		healthTTL:     healthCheckTTL,
	}

	if err := e.checkHealth(); err != nil {
		return nil, err
	}
	return e, nil
}

// checkHealth calls Heartbeat to verify Ollama is reachable.
func (e *HttpEncoder) checkHealth() error {
	ctx, cancel := context.WithTimeout(context.Background(), healthCheckTimeout)
	defer cancel()
	if err := e.client.Heartbeat(ctx); err != nil {
		return common.NewError(common.ErrEncoder,
			"Ollama health check failed", err)
	}
	return nil
}

// Encode sends an embed request and returns an f32 dense vector.
func (e *HttpEncoder) Encode(text string) ([]float32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.encodeTimeout)
	defer cancel()

	resp, err := e.client.Embed(ctx, &api.EmbedRequest{
		Model: e.model,
		Input: text,
	})
	if err != nil {
		return nil, common.NewError(common.ErrEncoder, "Ollama encode failed", err)
	}
	if len(resp.Embeddings) == 0 {
		return nil, common.NewError(common.ErrEncoder, "Ollama returned no embeddings")
	}
	if len(resp.Embeddings[0]) != e.dim {
		return nil, common.NewError(common.ErrEncoder,
			fmt.Sprintf("dimension mismatch: expected %d, got %d", e.dim, len(resp.Embeddings[0])))
	}

	return resp.Embeddings[0], nil
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
