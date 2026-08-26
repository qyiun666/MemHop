// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"io/fs"

	"github.com/qyiun666/MemHop/capabilities"
	"github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// DB is the public handle: embeds internal.DB, all methods come from the instance Open created.
type DB struct {
	*internal.DB
}

// Open creates or opens a MemHop database.
func Open(cfg *MemHopConfig) (*DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	enc, err := internal.CreateEncoder(cfg)
	if err != nil {
		return nil, err
	}
	return openWithEncoder(cfg, enc)
}

// OpenWithEncoder creates or opens a MemHop database with a custom encoder.
func OpenWithEncoder(cfg *MemHopConfig, enc Encoder) (*DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return openWithEncoder(cfg, enc)
}

// openWithEncoder opens the engine and attaches the embedded built-in
// capability manuals to the DB. Built-ins are read-only reference cards
// appended to L5 query responses — nothing is written into the .meh file.
func openWithEncoder(cfg *MemHopConfig, enc Encoder) (*DB, error) {
	d, err := internal.Open(cfg, enc)
	if err != nil {
		return nil, err
	}
	builtins, err := loadBuiltinCapabilities()
	if err != nil {
		_ = d.Close()
		return nil, err
	}
	d.SetBuiltinCapabilities(builtins)
	return &DB{d}, nil
}

// loadBuiltinCapabilities parses the embedded memhop-capability/v3 manuals
// (root capabilities/ directory) into read-only in-memory capabilities with
// stable name-derived IDs. The files are validated by unit tests, so a
// parse failure here means a corrupted build and aborts Open.
func loadBuiltinCapabilities() ([]Capability, error) {
	entries, err := fs.ReadDir(capabilities.FS, ".")
	if err != nil {
		return nil, err
	}
	out := make([]Capability, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(capabilities.FS, entry.Name())
		if err != nil {
			return nil, err
		}
		cap, err := internal.BuildCapability(data, "builtin:"+entry.Name())
		if err != nil {
			return nil, err
		}
		cap.IDHash = core.CapabilityID(cap.Name)
		cap.Status = CapabilityActive
		cap.Origin = CapabilityOriginBuiltin
		out = append(out, *cap)
	}
	return out, nil
}
