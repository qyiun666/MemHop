// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Tenant registry: lazily opens one MemHop DB (and the MCP server bound to
// it) per tenant. Tenants are isolated by file path — each tenant's data
// lives in <db-dir>/<tenant-id>.meh, opened by a dedicated DB instance, so
// no data is ever shared across tenants. The registry is safe for
// concurrent use; the mutex also guarantees a tenant DB is opened exactly
// once even under simultaneous first connections.

package main

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

// tenantEntry pairs a tenant's DB with the MCP server exposing its tools.
type tenantEntry struct {
	db     *memhop.DB
	server *mcp.Server
}

// tenantRegistry lazily opens one DB per tenant and serves the MCP server
// bound to it.
type tenantRegistry struct {
	mu      sync.Mutex
	base    memhop.MemHopConfig // shared engine config; DBPath filled per tenant
	dbDir   string
	allowed map[string]bool // empty means any valid tenant id
	entries map[string]*tenantEntry
	logger  *slog.Logger
	// open is a small injection seam for offline tests; production always
	// uses memhop.Open.
	open func(cfg *memhop.MemHopConfig) (*memhop.DB, error)
}

// newRegistry builds a tenant registry. allowed is the tenant whitelist;
// when empty, any valid tenant id creates its database on first access.
func newRegistry(base memhop.MemHopConfig, dbDir string, allowed []string, logger *slog.Logger) *tenantRegistry {
	r := &tenantRegistry{
		base:    base,
		dbDir:   dbDir,
		entries: make(map[string]*tenantEntry),
		logger:  logger,
		open:    memhop.Open,
	}
	if len(allowed) > 0 {
		r.allowed = make(map[string]bool, len(allowed))
		for _, id := range allowed {
			r.allowed[id] = true
		}
	}
	return r
}

// get returns the tenant's entry, opening its database on first access.
func (r *tenantRegistry) get(tenant string) (*tenantEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !tenantIDRe.MatchString(tenant) {
		return nil, fmt.Errorf("invalid tenant id %q", tenant)
	}
	if e, ok := r.entries[tenant]; ok {
		return e, nil
	}
	if len(r.allowed) > 0 && !r.allowed[tenant] {
		return nil, fmt.Errorf("tenant %q is not allowed", tenant)
	}

	dbPath := filepath.Join(r.dbDir, tenant+".meh")
	// Defense in depth: tenant ids pass parseTenant's whitelist regexp
	// already, but never trust a path that resolves outside db-dir.
	if filepath.Dir(dbPath) != filepath.Clean(r.dbDir) {
		return nil, fmt.Errorf("tenant %q resolves outside db-dir", tenant)
	}
	cfg := r.base
	cfg.DBPath = dbPath
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	db, err := r.open(&cfg)
	if err != nil {
		return nil, err
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "memhop", Version: version}, &mcp.ServerOptions{
		Logger: r.logger,
	})
	registerTools(server, db)

	e := &tenantEntry{db: db, server: server}
	r.entries[tenant] = e
	return e, nil
}

// CloseAll persists and closes every open tenant DB. The first error is
// returned; remaining tenants are still closed.
func (r *tenantRegistry) CloseAll() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for tenant, e := range r.entries {
		if err := e.db.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close tenant %q: %w", tenant, err)
		}
		delete(r.entries, tenant)
	}
	return firstErr
}
