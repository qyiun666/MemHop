// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import "github.com/qyiun666/MemHop/internal/sub"

// DB is the public handle: embeds sub.DB, all methods come from the instance Open created.
type DB struct {
	*sub.DB
}

// Open creates or opens a MemHop database.
func Open(cfg *sub.MemHopConfig) (*DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	enc, err := sub.CreateEncoder(cfg)
	if err != nil {
		return nil, err
	}
	d, err := sub.Open(cfg, enc)
	if err != nil {
		return nil, err
	}
	return &DB{d}, nil
}

// OpenWithEncoder creates or opens a MemHop database with a custom encoder.
func OpenWithEncoder(cfg *sub.MemHopConfig, enc sub.Encoder) (*DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	d, err := sub.Open(cfg, enc)
	if err != nil {
		return nil, err
	}
	return &DB{d}, nil
}
