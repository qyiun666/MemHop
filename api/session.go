// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Session is the only business handle of the public facade: it embeds the
// internal domain-bound session (internal.Session), so the promoted method
// set is exactly the externally callable surface. Every call is serialized
// per agent domain by the internal domain lock.

package api

import "github.com/qyiun666/MemHop/internal"

// Session binds every call to one agent domain.
type Session struct {
	*internal.Session
}
