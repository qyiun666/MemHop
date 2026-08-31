// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// memhop-mcp exposes the MemHop memory database as a Model Context Protocol
// (MCP) server over HTTP. A single multi-tenant process serves many hosts
// through one shared multi-agent database: each tenant reaches its own
// isolated agent domain through the URL path /mcp/<tenant-id> (a stable
// agentID per tenant name inside the single <db-dir>/memhop.meh file), so
// no data is ever shared across tenants.
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
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const version = "v1.4.2"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	cfg, err := loadConfig(os.Args[1:])
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(2)
	}

	reg := newRegistry(cfg.Base, cfg.DBDir, cfg.Tenants, logger)
	handler, err := buildHandler(cfg, reg)
	if err != nil {
		logger.Error("unknown transport", "transport", cfg.Transport)
		os.Exit(2)
	}
	srv := &http.Server{
		Addr:    cfg.Listen,
		Handler: handler,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, srv, cfg, logger); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}

	// Persist every open tenant DB (Close builds the index snapshot first).
	if cerr := gracefulShutdown(srv, reg, logger); cerr != nil {
		logger.Error("close databases", "error", cerr)
		os.Exit(1)
	}
	logger.Info("server exited cleanly")
}

// buildHandler wires the tenant registry into the selected MCP HTTP
// transport.
func buildHandler(cfg *serverConfig, reg *tenantRegistry) (http.Handler, error) {
	switch cfg.Transport {
	case "sse":
		return newSSEHandler(reg), nil
	case "streamable-http":
		return newStreamableHandler(reg), nil
	default:
		return nil, fmt.Errorf("unknown transport %q", cfg.Transport)
	}
}

// serve runs the HTTP server until a shutdown signal arrives or the
// server fails; the listen goroutine exits once the server is closed.
func serve(ctx context.Context, srv *http.Server, cfg *serverConfig, logger *slog.Logger) error {
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
		return nil
	case err := <-errCh:
		return err
	}
}

// gracefulShutdown bounds the HTTP drain (SSE sessions hold hanging GETs
// open, so Shutdown is time-boxed; remaining connections are dropped)
// then persists and closes every open tenant database.
func gracefulShutdown(srv *http.Server, reg *tenantRegistry, logger *slog.Logger) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown incomplete", "error", err)
	}
	return reg.CloseAll()
}
