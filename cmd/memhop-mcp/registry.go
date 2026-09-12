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
var primaryProfile = memhop.ProfileInput{
	Name: "memhop-mcp",
	Role: "primary domain of the shared MCP database file",
}

// errRegistryClosed is what a request meets after shutdown. CloseAll nils the shared
// database, and nil is also "not opened yet", so without a flag of its own a session
// still connected would reopen the file: the revived database is closed by nobody,
// its checkpoint is never written, and the process still reports a clean exit.
var errRegistryClosed = errors.New("memhop-mcp is shut down")

// tenantRegistry opens the shared DB and serves one MCP server per tenant bound
// to that tenant's sub-agent domain.
type tenantRegistry struct {
	mu       sync.Mutex
	llm      memhop.LlmConfig
	defaults memhop.MemHopDefaults
	dbDir    string
	allowed  map[string]bool // empty means any valid tenant id
	db       *memhop.DB
	closed   bool
	entries  map[string]*mcp.Server
	logger   *slog.Logger
	// open is a small injection seam for offline tests; production always
	// uses memhop.Open.
	open func(path string, llm memhop.LlmConfig, defaults memhop.MemHopDefaults, profile *memhop.ProfileInput) (*memhop.DB, error)
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
// access. The registry lock covers the entry map, the whitelist and the one-time
// lazy open of the shared database. What it never covers is opening a tenant's own
// domain: that takes the domain lock inside the engine, and a busy tenant holds it
// for the length of an LLM round-trip. Since every request resolves its tenant
// here, a registry held across that call would put one tenant's work in front of
// all the others. Two concurrent first requests for one name may therefore both
// open the domain — registration is serialized by the engine and idempotent by
// name, so they land on the same domain and the later server is dropped.
func (r *tenantRegistry) get(tenant string) (*mcp.Server, error) {
	if !tenantIDRe.MatchString(tenant) {
		return nil, fmt.Errorf("invalid tenant id %q", tenant)
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, errRegistryClosed
	}
	if srv, ok := r.entries[tenant]; ok {
		r.mu.Unlock()
		return srv, nil
	}
	if len(r.allowed) > 0 && !r.allowed[tenant] {
		r.mu.Unlock()
		return nil, fmt.Errorf("tenant %q is not allowed", tenant)
	}
	if r.db == nil {
		if err := r.openShared(); err != nil {
			r.mu.Unlock()
			return nil, err
		}
	}
	db := r.db
	r.mu.Unlock()

	// The tenant name is the domain's address, so a reconnecting tenant lands on
	// the domain it already had rather than minting a second one.
	session, err := db.SubAgent(r.llm, memhop.ProfileInput{Name: tenant, Role: "MCP tenant"})
	if err != nil {
		return nil, err
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "memhop", Version: version}, &mcp.ServerOptions{
		Logger: r.logger,
	})
	registerTools(server, db, session)

	r.mu.Lock()
	defer r.mu.Unlock()
	if first, ok := r.entries[tenant]; ok {
		return first, nil
	}
	r.entries[tenant] = server
	return server, nil
}

// OpenShared opens the single shared database file inside db-dir. main calls it
// at startup so a bad --db-dir or an unusable LLM configuration is a process that
// refuses to start rather than a 500 on the first request; get also calls it, so
// a registry used without that step still works.
//
// The database filename is a constant, so the only way the path leaves db-dir is a
// symlink planted there: os.Root is what notices, and the file is then refused
// before it is opened. The open itself goes through the joined path, because the
// library takes a path rather than a root.
func (r *tenantRegistry) OpenShared() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errRegistryClosed
	}
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
		return fmt.Errorf("check %s inside db-dir: %w", dbFileName, err)
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
// per-agent index snapshots first), drops every tenant entry and refuses
// everything after it: this is the process's last word about the file.
func (r *tenantRegistry) CloseAll() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.entries)
	r.closed = true
	if r.db == nil {
		return nil
	}
	err := r.db.Close()
	r.db = nil
	return err
}
