// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// memhop-mcp exposes the MemHop memory database as a Model Context Protocol
// (MCP) server over stdio. One process owns one .meh file (single-instance
// contract): a client that starts this server gets exclusive access to its
// database for the lifetime of the connection.
//
// All logging goes to stderr — stdout is the MCP protocol channel.

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop"
)

const version = "v1.2.1"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	cfg, err := loadConfig(os.Args[1:])
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(2)
	}
	db, err := memhop.Open(cfg)
	if err != nil {
		logger.Error("open database", "db", cfg.DBPath, "error", err)
		os.Exit(1)
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "memhop", Version: version}, &mcp.ServerOptions{
		// Debug-level protocol logs go to stderr (stdout is the MCP channel).
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	registerTools(server, db)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Run blocks until the client closes stdin or the context is cancelled.
	// lineTransport replaces the SDK's StdioTransport (message-dropping bug,
	// see transport.go).
	err = server.Run(ctx, lineTransport{})
	if isNormalShutdown(err) {
		err = nil
	}

	// Always persist on exit (Close builds the index snapshot first).
	if cerr := db.Close(); cerr != nil {
		logger.Error("close database", "error", cerr)
		if err == nil {
			err = cerr
		}
	}
	if err != nil {
		logger.Error("server exited with error", "error", err)
		os.Exit(1)
	}
	logger.Info("server exited cleanly")
}

// isNormalShutdown reports whether the Run error is a client disconnect or
// context cancellation — the normal end of a stdio MCP server lifecycle.
func isNormalShutdown(err error) bool {
	if err == nil {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "server is closing") || errors.Is(err, context.Canceled)
}
