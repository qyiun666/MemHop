// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Encoder injection seam of the composition root: HttpEncoder is the
// built-in implementation backed by the Ollama HTTP API (Encode returns a
// dense f32 vector, IsAvailable probes health). Capability packages declare
// the narrow Encoder they need (e.g. scenefind.Encoder); HttpEncoder
// satisfies all of them structurally.
//
// MemHop talks to Ollama directly over plain HTTP. Ollama is owned and
// operated by the host; importing its Go SDK here would pull in a large
// dependency tree just to issue two small JSON requests.
package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
)

const (
	healthCheckTimeout   = 5 * time.Second
	defaultEncodeTimeout = 20 * time.Second
	healthCheckTTL       = 5 * time.Second // IsAvailable cache TTL
)

// Encoder is the host-injected embedding contract re-exported by the public
// facade (api.Encoder): Open assembles an HttpEncoder when the host does not
// supply one. Capability packages declare the narrower shape they use, which
// this satisfies structurally.
type Encoder interface {
	Encode(text string) ([]float32, error)
	IsAvailable() bool
}

// HttpEncoder is a minimal HTTP client for the Ollama embed API.
type HttpEncoder struct {
	baseURL       string
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

// NewHttpEncoder constructs an Ollama HTTP encoder. It performs an immediate
// health check so misconfigured addresses fail at Open time.
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

	if _, err := url.Parse(baseURL); err != nil {
		return nil, common.NewError(common.ErrConfig,
			fmt.Sprintf("invalid encoder address %s", baseURL), err)
	}

	// No client-level Timeout: per-request deadlines are set via context,
	// so health checks (5s) and encodes can share one pooled client.
	e := &HttpEncoder{
		baseURL:       baseURL,
		httpClient:    &http.Client{},
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

// checkHealth probes the encoder endpoint with HEAD / and requires a 2xx.
// The endpoint MUST answer HEAD on the root path; there is intentionally no
// GET fallback, so a proxy that only supports GET fails loudly at Open time
// instead of degrading silently at encode time.
func (e *HttpEncoder) checkHealth() error {
	ctx, cancel := context.WithTimeout(context.Background(), healthCheckTimeout)
	defer cancel()
	url := e.baseURL + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return common.NewError(common.ErrEncoder, "Ollama health check failed", err)
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return common.NewError(common.ErrEncoder, "Ollama health check failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return common.NewError(common.ErrEncoder,
			fmt.Sprintf("Ollama health check failed: HEAD %s returned status %d; the encoder endpoint must respond 2xx to HEAD /", url, resp.StatusCode))
	}
	return nil
}

type ollamaEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Encode sends POST /api/embed and returns an f32 dense vector.
func (e *HttpEncoder) Encode(text string) ([]float32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.encodeTimeout)
	defer cancel()

	body, err := json.Marshal(ollamaEmbedRequest{Model: e.model, Input: text})
	if err != nil {
		return nil, common.NewError(common.ErrEncoder, "Ollama encode request marshal", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, common.NewError(common.ErrEncoder, "Ollama encode request build", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, common.NewError(common.ErrEncoder, "Ollama encode failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, common.NewError(common.ErrEncoder,
			fmt.Sprintf("Ollama encode returned status %d: %s", resp.StatusCode, string(msg)))
	}

	var out ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, common.NewError(common.ErrEncoder, "Ollama encode response decode", err)
	}
	if len(out.Embeddings) == 0 {
		return nil, common.NewError(common.ErrEncoder, "Ollama returned no embeddings")
	}
	if len(out.Embeddings[0]) != e.dim {
		return nil, common.NewError(common.ErrEncoder,
			fmt.Sprintf("dimension mismatch: expected %d, got %d", e.dim, len(out.Embeddings[0])))
	}
	return out.Embeddings[0], nil
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
