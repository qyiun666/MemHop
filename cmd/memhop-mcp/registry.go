// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Tenant registry: one shared MemHop database serves every tenant. Each tenant
// maps to an isolated sub-agent domain inside the single <db-dir>/memhop.meh
// file, addressed by the tenant name and created on first access, so no data is
// ever shared across tenants while one engine instance carries all domains. The
// registry is safe for concurrent use; the mutex also guarantees the shared DB
// is opened exactly once even under simultaneous first connections.

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

// primaryProfile is the profile the shared file is opened on. Every tenant gets
// its own sub-agent domain and no tool is bound to the primary, so this exists
// only because opening a file that does not exist yet needs to know whose memory
// it is. It is deliberately not configurable: there is nothing to configure.
var primaryProfile = memhop.ProfileSlot{
	Name: "memhop-mcp",
	Role: "primary domain of the shared MCP database file",
}

// tenantRegistry opens the shared DB and serves one MCP server per tenant bound
// to that tenant's sub-agent domain.
type tenantRegistry struct {
	mu       sync.Mutex
	llm      memhop.LlmConfig
	defaults memhop.MemHopDefaults
	dbDir    string
	allowed  map[string]bool // empty means any valid tenant id
	db       *memhop.DB
	entries  map[string]*mcp.Server
	logger   *slog.Logger
	// open is a small injection seam for offline tests; production always
	// uses memhop.Open.
	open func(path string, llm memhop.LlmConfig, defaults memhop.MemHopDefaults, profile *memhop.ProfileSlot) (*memhop.DB, error)
}

// newRegistry builds a tenant registry. allowed is the tenant whitelist;
// when empty, any valid tenant id creates its agent domain on first access.
func newRegistry(llm memhop.LlmConfig, defaults memhop.MemHopDefaults, dbDir string, allowed []string, logger *slog.Logger) *tenantRegistry {
	r := &tenantRegistry{
		llm:      llm,
		defaults: defaults,
		dbDir:    dbDir,
		entries:  make(map[string]*mcp.Server),
		logger:   logger,
		open:     memhop.Open,
	}
	if len(allowed) > 0 {
		r.allowed = make(map[string]bool, len(allowed))
		for _, id := range allowed {
			r.allowed[id] = true
		}
	}
	return r
}

// get returns the tenant's MCP server, creating its agent domain on first
// access.
func (r *tenantRegistry) get(tenant string) (*mcp.Server, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !tenantIDRe.MatchString(tenant) {
		return nil, fmt.Errorf("invalid tenant id %q", tenant)
	}
	if srv, ok := r.entries[tenant]; ok {
		return srv, nil
	}
	if len(r.allowed) > 0 && !r.allowed[tenant] {
		return nil, fmt.Errorf("tenant %q is not allowed", tenant)
	}
	if r.db == nil {
		if err := r.openShared(); err != nil {
			return nil, err
		}
	}
	// The tenant name is the domain's address, so a reconnecting tenant lands on
	// the domain it already had rather than minting a second one.
	session, err := r.db.SubAgent(r.llm, memhop.ProfileSlot{Name: tenant, Role: "MCP tenant"})
	if err != nil {
		return nil, err
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "memhop", Version: version}, &mcp.ServerOptions{
		Logger: r.logger,
	})
	registerTools(server, r.db, session)

	r.entries[tenant] = server
	return server, nil
}

// OpenShared opens the single shared database file inside db-dir. main calls it
// at startup so a bad --db-dir or an unusable LLM configuration is a process that
// refuses to start rather than a 500 on the first request; get also calls it, so
// a registry used without that step still works.
//
// os.Root anchors every file operation to db-dir: the constant database filename
// is resolved through the root, whose operations can never escape the directory
// (path-traversal defense in depth on top of the tenant-id whitelist).
func (r *tenantRegistry) OpenShared() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db != nil {
		return nil
	}
	return r.openShared()
}

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
	primary := primaryProfile
	db, err := r.open(filepath.Join(r.dbDir, dbFileName), r.llm, r.defaults, &primary)
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
	clear(r.entries)
	if r.db == nil {
		return nil
	}
	err := r.db.Close()
	r.db = nil
	return err
}
