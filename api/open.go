// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package api is the public facade of MemHop. It contains no business logic:
// handle types embed the internal domain-bound session (internal.Session) so the
// promoted method set is exactly the externally callable surface, and every
// remaining method is a one-line forward to the internal composition root.
//
// Open is the single entry point. It takes the file path, the LLM endpoint, the
// tuning knobs and the primary agent's profile, and whether it succeeds depends on
// what the file already holds — see Open. Domains are reached as handles, not as
// ids: Primary for the domain the file was opened on, SubAgent for one created
// under it. No agent id crosses this boundary.

package api

import (
	"github.com/qyiun666/MemHop/internal"
)

// DB is the handle Open returns: one .meh file, its primary domain, and the
// sub-agent domains created under it. Business operations run through a Session;
// the methods here cover domain identity and file-level lifecycle.
type DB struct {
	db *internal.DB
}

// Open opens the database at path and settles its primary domain. What happens
// depends on the file and on that domain's profile:
//
//	file there, profile there  → the file's own profile wins; the argument is ignored
//	file there, no profile     → seed it from profile; without one, an error
//	no file                    → create and seed; without a profile, an error
//
// Both refusals happen before anything touches the filesystem, so a refused Open
// leaves no file behind. profile is optional only in the sense that an existing
// file may already have settled its primary; a new file always needs one, because
// "whose memory is this" has no other answer.
//
// The domain a file is opened on is its primary — AgentType is the library's to
// decide, which is why it is not part of the argument. Name is required and is
// trimmed.
// llm is validated before the path is touched — the endpoint is the engine's only
// external service and there is no fallback for a call it cannot make.
func Open(path string, llm LlmConfig, defaults MemHopDefaults, profile *ProfileInput) (*DB, error) {
	var primary *internal.ProfileSlot
	if profile != nil {
		slot := toCoreProfileSlot(profile)
		primary = &slot
	}
	d, err := internal.OpenDB(path, llm, defaults, primary)
	if err != nil {
		return nil, err
	}
	return &DB{db: d}, nil
}

// Primary returns the handle of the domain the file was opened on. A file holds
// exactly one, so this needs nothing to look it up by.
func (d *DB) Primary() (*Session, error) {
	s, err := d.db.Primary()
	if err != nil {
		return nil, err
	}
	return &Session{s}, nil
}

// SubAgent returns the handle of the sub-agent domain named profile.Name,
// creating that domain the first time and handing back the same one every time
// after. The name is a tenant key and is frozen at creation: it is how the domain
// is addressed, so editing the profile's Name later does not move the domain, and
// asking for a name nobody registered opens a second domain instead of finding
// the first. Name is required, trimmed, and capped at 256 bytes.
//
// llm is this domain's own endpoint, so a sub-agent can run on a different model
// from the primary. Naming the same domain again replaces its endpoint: the one a
// host hands over now is the one its turns use from here on.
//
// A domain created here is a sub-agent — AgentType is not part of the argument.
// The profile is written only if the domain has none yet, so a crash between
// registration and the profile is finished off by the next call with the same
// name rather than left as a domain with no identity.
func (d *DB) SubAgent(llm LlmConfig, profile ProfileInput) (*Session, error) {
	s, err := d.db.SubAgent(llm, toCoreProfileSlot(&profile))
	if err != nil {
		return nil, err
	}
	return &Session{s}, nil
}

// ---- file-level lifecycle ----

// Checkpoint persists the per-agent index snapshots without closing.
func (d *DB) Checkpoint() error { return d.db.Checkpoint() }

// CompactTo writes a defragmented copy of the whole file at newPath — only
// live records, in one fresh log with its own rebuilt index — and leaves the
// open file untouched. Deletions are tombstones, so this is where a domain that
// dropped scenes, topics or graphs gives the bytes back. newPath must not
// exist yet, and the copy is a point-in-time snapshot: compact while the domains
// are quiet (typically right before Close), then swap it in yourself.
//
// Go-side only, deliberately: an output path is an arbitrary-file-write
// primitive, which is not something to hand a model over MCP.
func (d *DB) CompactTo(newPath string) error { return d.db.CompactTo(newPath) }

// Close checkpoints every agent domain and releases the file.
func (d *DB) Close() error { return d.db.Close() }

// IsClosed reports whether the database has been closed.
func (d *DB) IsClosed() bool { return d.db.IsClosed() }
