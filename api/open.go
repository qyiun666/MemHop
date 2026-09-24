// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package api is the public facade of MemHop: a Go-module surface over one .meh file,
// with no business logic here. The two handle types hold their internal counterpart in
// an unexported field and declare every callable method themselves, so the declared set
// is exactly the externally callable surface (api/surface_public_test.go pins it) and the
// internal handles are unreachable from outside. Methods that need no DTO mapping
// forward in one line and carry the contract a host cannot read anywhere else; the rest
// map internal shapes to the published DTOs, rendering every id as a 16-character hex
// string.
//
// Open is the single entry point; domains are reached as handles, not as ids — Primary
// for the domain the file was opened on, SubAgent for one created under it. The only
// external service the engine talks to is the OpenAI-compatible endpoint in LlmConfig.
package api

import (
	"github.com/qyiun666/MemHop/internal"
)

// DB is the handle Open returns: one .meh file, its primary domain, and the sub-agent
// domains created under it. Business operations run through a Session; the methods here
// cover domain identity and file-level lifecycle.
type DB struct {
	db *internal.DB
}

// Open opens the database at path and settles its primary domain. What happens depends
// on the file and on that domain's profile:
//
//	file there, profile there  → the file's own profile wins; the argument is ignored
//	file there, no profile     → seed it from profile; without one, an error
//	no file                    → create and seed; without a profile, an error
//
// A refused Open leaves no file on the path: seeding a new one is what a profile is for.
// An existing file is opened before its primary profile is looked for, and opening one
// repairs a torn tail — what a refusal guarantees is only that nothing new appears.
// profile is optional only in the sense that an existing file may already have settled
// its primary; a new file always needs one, because "whose memory is this" has no other
// answer. That is the only reason this door takes a pointer where SubAgent and UpdateL0
// take the value: nil here means "do not seed", and neither of those calls has such a
// reading. Name is required and trimmed. llm is validated before the path is touched,
// and there is no fallback for a call it cannot make.
// defaults may be the zero value: every knob left at 0 takes the library default, so
// tuning one field does not require copying the whole table first.
func Open(path string, llm LlmConfig, defaults MemHopDefaults, profile *ProfileInput) (*DB, error) {
	var primary *internal.ProfileSlot
	if profile != nil {
		slot := toCoreProfileSlot(*profile)
		primary = &slot
	}
	d, err := internal.OpenDB(path, llm, defaults, primary)
	if err != nil {
		return nil, err
	}
	return &DB{db: d}, nil
}

// Primary returns the handle of the domain the file was opened on.
func (d *DB) Primary() (*Session, error) {
	s, err := d.db.Primary()
	if err != nil {
		return nil, err
	}
	return &Session{s}, nil
}

// SubAgent returns the handle of the sub-agent domain named profile.Name, creating that
// domain the first time and handing back the same one every time after. The name is a
// tenant key and is frozen at creation: editing the profile's Name later does not move
// the domain, and asking for a name nobody registered opens a second domain instead of
// finding the first. Name is required, trimmed, and capped at 256 bytes.
//
// llm is this domain's own endpoint, so a sub-agent can run on a different model from
// the primary; naming the same domain again replaces the endpoint its turns use from
// here on. The profile is written only if the domain has none yet, so a crash between
// registration and the profile is finished off by the next call with the same name.
func (d *DB) SubAgent(llm LlmConfig, profile ProfileInput) (*Session, error) {
	s, err := d.db.SubAgent(llm, toCoreProfileSlot(profile))
	if err != nil {
		return nil, err
	}
	return &Session{s}, nil
}

// Agent returns the handle of a domain this file already holds, addressed by the id
// Session.AgentID handed out for it, and points that domain at ll — the same endpoint
// replacement SubAgent performs, minus the name and minus creation. The id names a domain
// of this file: every file has its own primary, and every primary is the implicit zero
// domain, so the same string means another memory in another .meh.
//
// It creates nothing: an id that is not this file's primary and not a registered tenant is
// refused with ErrAgentNotFound, so a mistyped or invented id cannot open an empty memory
// on top of somebody else's. The primary's id addresses the primary — the same domain
// Primary returns, with llm now as its endpoint. A domain whose profile was lost to a crash
// between registration and the profile is healed by SubAgent (its name is the key that
// carries the profile); this call hands back the handle either way, and GetL0 on it reports
// what the domain holds. A host that deploys one file per agent keys across files by the
// path — id alone is unique only inside a file.
func (d *DB) Agent(llm LlmConfig, agentID string) (*Session, error) {
	s, err := d.db.Agent(llm, agentID)
	if err != nil {
		return nil, err
	}
	return &Session{s}, nil
}

// Agents lists every domain the file holds: the primary first, then the registered
// sub-agents in id order. It is the discovery half of DB.Agent — a host that inherited a
// file, or lost its own roster, reads this to learn which ids and which names are in it,
// instead of guessing a name and quietly creating a second domain beside the real one.
// The list is what the file's registry records say, so a domain whose key cannot be read
// stops the listing with that cause rather than being left out of a list whose purpose is
// completeness. The file-wide L3 pool is not a domain and never appears.
func (d *DB) Agents() ([]AgentInfo, error) {
	list, err := d.db.Agents()
	if err != nil {
		return nil, err
	}
	return mapSlice(list, fromAgentInfo), nil
}

// ---- file-level lifecycle ----

// Checkpoint persists the per-agent index snapshots without closing.
func (d *DB) Checkpoint() error { return d.db.Checkpoint() }

// DBStats is the file-level view Stats hands back: the size of the .meh file in
// bytes and the number of live records across every domain of the file.
type DBStats struct {
	FileBytes   int64 `json:"file_bytes"`
	RecordCount int64 `json:"record_count"`
}

// Stats reports how big the file has grown and how many live records it holds —
// the numbers a compaction decision is made from. RecordCount is what a read can
// reach; the bytes a deleted record still occupies on the log are counted in FileBytes
// and belong to no record in RecordCount. The two are not two units of one quantity and
// are never subtracted from each other: what CompactTo gives back is the difference
// between FileBytes before it and FileBytes after, and the rewrite is a fixed point —
// measured on the offline corpus, 19 700 bytes to 17 827 with the same 47 live records,
// and a file with nothing left to reclaim comes out no larger a second time
// (18 830 to 18 680; TestInterfaceCompactTo pins both halves).
// It takes no domain lock, so it answers while domains are busy.
func (d *DB) Stats() (DBStats, error) {
	size, records, err := d.db.Stats()
	if err != nil {
		return DBStats{}, err
	}
	return DBStats{FileBytes: size, RecordCount: int64(records)}, nil
}

// CompactTo writes a defragmented copy of the whole file at newPath — only
// live records, in one fresh log with its own rebuilt index — and leaves the
// open file untouched. Deletions are tombstones, so this is where a domain that
// dropped scenes, topics or graphs gives the bytes back. newPath must not
// exist yet, and the copy is a point-in-time snapshot: compact while the domains
// are quiet (typically right before Close), then swap it in yourself.
//
// The argument is an arbitrary-file-write path, which the library takes as
// given: a host that exposes compaction to a model must pin the destination
// itself rather than pass through what the model named.
func (d *DB) CompactTo(newPath string) error { return d.db.CompactTo(newPath) }

// Close checkpoints every agent domain and releases the file. It is the one call
// on this surface that reports being late rather than succeeding: a second Close
// answers ErrClosed, because it closed nothing. That is the same answer every
// other call gives once the file is gone — the whole published surface, DB and
// every Session taken from it, refuses with ErrClosed instead of reading a
// released file or panicking — so a host that defers Close and also closes at the
// end of its run reads one vocabulary rather than two, and a worker still mid-turn
// gets a code it already knows how to branch on. The two accessors that read no
// engine state keep answering normally: Session.AgentID (the handle's own id) and
// IsClosed itself.
func (d *DB) Close() error { return d.db.Close() }

// IsClosed reports whether the database has been closed. It reads no engine state,
// so it answers on a closed database like any other accessor — which is also why it
// is the check a host can run to tell an ErrClosed it just read apart: "this handle
// is gone" versus "this record is unreadable".
func (d *DB) IsClosed() bool { return d.db.IsClosed() }
