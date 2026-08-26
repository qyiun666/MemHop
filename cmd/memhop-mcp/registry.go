// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Tenant registry: one shared multi-agent MemHop database serves every
// tenant. Each tenant maps to an isolated agent domain inside the single
// <db-dir>/memhop.meh file (CreateAgent hands out a stable agentID per
// tenant name), so no data is ever shared across tenants while one engine
// instance carries all domains. The registry is safe for concurrent use;
// the mutex also guarantees the shared DB is opened exactly once even under
// simultaneous first connections.

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

// dbFileName is the single shared database file inside --db-dir.
const dbFileName = "memhop.meh"

// tenantEntry pairs a tenant's agent session with the MCP server exposing
// its tools.
type tenantEntry struct {
	session *memhop.AgentSession
	server  *mcp.Server
}

// tenantRegistry lazily opens the shared multi-agent DB and serves one MCP
// server per tenant bound to that tenant's agent domain.
type tenantRegistry struct {
	mu      sync.Mutex
	base    memhop.MemHopConfig // shared engine config; DBPath set once at open
	dbDir   string
	allowed map[string]bool // empty means any valid tenant id
	db      *memhop.MultiAgentDB
	entries map[string]*tenantEntry
	logger  *slog.Logger
	// open is a small injection seam for offline tests; production always
	// uses memhop.OpenMulti.
	open func(cfg *memhop.MemHopConfig) (*memhop.MultiAgentDB, error)
}

// newRegistry builds a tenant registry. allowed is the tenant whitelist;
// when empty, any valid tenant id creates its agent domain on first access.
func newRegistry(base memhop.MemHopConfig, dbDir string, allowed []string, logger *slog.Logger) *tenantRegistry {
	r := &tenantRegistry{
		base:    base,
		dbDir:   dbDir,
		entries: make(map[string]*tenantEntry),
		logger:  logger,
		open:    memhop.OpenMulti,
	}
	if len(allowed) > 0 {
		r.allowed = make(map[string]bool, len(allowed))
		for _, id := range allowed {
			r.allowed[id] = true
		}
	}
	return r
}

// get returns the tenant's entry, creating its agent domain on first access.
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
	if r.db == nil {
		if err := r.openShared(); err != nil {
			return nil, err
		}
	}
	agentID, err := r.db.CreateAgent(tenant)
	if err != nil {
		return nil, err
	}
	session, err := r.db.Session(agentID)
	if err != nil {
		return nil, err
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "memhop", Version: version}, &mcp.ServerOptions{
		Logger: r.logger,
	})
	registerTools(server, session)

	e := &tenantEntry{session: session, server: server}
	r.entries[tenant] = e
	return e, nil
}

// openShared opens the single shared database file inside db-dir. os.Root
// anchors every file operation to db-dir: the constant database filename is
// resolved through the root, whose operations can never escape the
// directory (path-traversal defense in depth on top of the tenant-id
// whitelist). The engine itself creates the file on first write.
func (r *tenantRegistry) openShared() error {
	root, err := os.OpenRoot(r.dbDir)
	if err != nil {
		return fmt.Errorf("open db-dir: %w", err)
	}
	if _, err := root.Stat(dbFileName); err != nil && !errors.Is(err, fs.ErrNotExist) {
		root.Close()
		return fmt.Errorf("database file escapes db-dir: %w", err)
	}
	if err := root.Close(); err != nil {
		return err
	}
	cfg := r.base
	cfg.DBPath = filepath.Join(r.dbDir, dbFileName)
	if err := cfg.Validate(); err != nil {
		return err
	}
	db, err := r.open(&cfg)
	if err != nil {
		return err
	}
	r.db = db
	return nil
}

// CloseAll persists and closes the shared database (Close builds the
// per-agent index snapshots first) and drops every tenant entry.
func (r *tenantRegistry) CloseAll() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for tenant := range r.entries {
		delete(r.entries, tenant)
	}
	if r.db == nil {
		return nil
	}
	err := r.db.Close()
	r.db = nil
	return err
}
