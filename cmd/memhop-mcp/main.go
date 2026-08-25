// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// memhop-mcp exposes the MemHop memory database as a Model Context Protocol
// (MCP) server over HTTP. A single multi-tenant process serves many hosts:
// each tenant reaches its own isolated database through the URL path
// /mcp/<tenant-id>, and every tenant's data lives in a dedicated .meh file
// under --db-dir — no data is shared across tenants.
//
// Two HTTP transports are supported:
//
//   - SSE (default, 2024-11-05 spec): long-lived sessions per tenant.
//   - streamable-http (2025-03-26 spec, supported by dsh-mcp-client and
//     other modern MCP clients): stateless per-request routing.
//
// All logging goes to stderr.

package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const version = "v1.2.7"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	cfg, err := loadConfig(os.Args[1:])
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(2)
	}

	reg := newRegistry(cfg.Base, cfg.DBDir, cfg.Tenants, logger)
	var handler http.Handler
	switch cfg.Transport {
	case "sse":
		handler = newSSEHandler(reg)
	case "streamable-http":
		handler = newStreamableHandler(reg)
	default:
		logger.Error("unknown transport", "transport", cfg.Transport)
		os.Exit(2)
	}
	srv := &http.Server{
		Addr:    cfg.Listen,
		Handler: handler,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("memhop-mcp listening", "addr", cfg.Listen, "db_dir", cfg.DBDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down")
	case err := <-errCh:
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}

	// SSE sessions hold hanging GETs open, so Shutdown must be bounded;
	// remaining connections are dropped and the registry persists below.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown incomplete", "error", err)
	}

	// Persist every open tenant DB (Close builds the index snapshot first).
	if cerr := reg.CloseAll(); cerr != nil {
		logger.Error("close databases", "error", cerr)
		os.Exit(1)
	}
	logger.Info("server exited cleanly")
}
