// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// SSE transport assembly: routes each incoming MCP session to the tenant
// identified by its URL path (/mcp/<tenant-id>). Tenant ids are validated
// against a strict whitelist (URL-safe characters only), which rejects
// path-traversal attempts — the whole server holds one .meh file inside
// --db-dir and a tenant is one agent domain inside it, so the whitelist is about
// the name being a safe address, not about keeping tenants in separate files.

package main

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// tenantIDRe admits only URL-safe, path-traversal-free tenant ids. '.' and
// '/' are deliberately excluded so a tenant can never address files outside
// db-dir, and '/' cannot smuggle sub-paths.
var tenantIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// parseTenant extracts the tenant id from a /mcp/<id> request path.
func parseTenant(path string) (string, error) {
	const prefix = "/mcp/"
	if !strings.HasPrefix(path, prefix) {
		return "", errors.New("path must start with " + prefix)
	}
	id := strings.TrimPrefix(path, prefix)
	if !tenantIDRe.MatchString(id) {
		return "", errors.New("invalid tenant id")
	}
	return id, nil
}

// serverForRequest routes each incoming MCP request to the tenant identified
// by its URL path (/mcp/<tenant-id>). An unknown or malformed tenant yields
// no server, which the SDK answers with 400 Bad Request.
func serverForRequest(reg *tenantRegistry) func(*http.Request) *mcp.Server {
	return func(req *http.Request) *mcp.Server {
		tenant, err := parseTenant(req.URL.Path)
		if err != nil {
			return nil
		}
		srv, err := reg.get(tenant)
		if err != nil {
			// The whitelist rejection above is the client misspelling an address;
			// this one is the server failing to open the domain a valid name points
			// at. Both answer 400, so only the log can tell an operator which of the
			// two a tenant is hitting — and a domain that cannot be opened is the one
			// they need to hear about.
			reg.logger.Warn("tenant domain refused", "tenant", tenant, "err", err)
			return nil
		}
		return srv
	}
}

// newSSEHandler returns the MCP SSE handler (2024-11-05 spec) that routes
// each new session to the tenant registry.
func newSSEHandler(reg *tenantRegistry) *mcp.SSEHandler {
	return mcp.NewSSEHandler(serverForRequest(reg), nil)
}

// newStreamableHandler returns the MCP Streamable HTTP handler (2025-03-26
// spec) routing each request to the tenant registry. Stateless mode avoids
// cross-request session state, so every POST independently resolves its
// tenant from the URL path — the same isolation model as the SSE transport.
// Stateless servers cannot send server-initiated requests; MCP clients such
// as dsh-mcp-client (streamable-http transport) work within that contract.
func newStreamableHandler(reg *tenantRegistry) *mcp.StreamableHTTPHandler {
	return mcp.NewStreamableHTTPHandler(serverForRequest(reg), &mcp.StreamableHTTPOptions{Stateless: true})
}
