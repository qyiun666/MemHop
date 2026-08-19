// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package memhop is the public export layer of the MemHop memory engine.
//
// The v2 implementation lives entirely under internal/ (packages internal,
// internal/sub, internal/sub/repo/core and internal/sub/common). This root
// package re-exports the public surface as type aliases plus one-line
// constructor wrappers, so external modules can import
// github.com/qyiun666/MemHop without reaching into internal packages.
//
// Open returns a *DB whose full method set is available directly: Search,
// Update, Dream, the L0-L7 APIs, plus the promoted sub-layer methods
// Close / Checkpoint / IsClosed / HasActiveScenes / TouchLastDreamAt /
// Lock / Unlock. RunDream is the low-level unlocked variant; use Dream
// unless the caller explicitly serializes with Lock/Unlock.
package memhop

import (
	"io/fs"

	"github.com/qyiun666/MemHop/capabilities"
	memhopinternal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/sub"
	"github.com/qyiun666/MemHop/internal/sub/common"
)

// DB is the public database handle returned by Open / OpenWithEncoder.
// All methods of the internal DB are available on *DB.
type DB = memhopinternal.DB

// MemHopConfig configures a MemHop database.
type MemHopConfig = sub.MemHopConfig

// Encoder is the embedding encoder contract required by OpenWithEncoder.
type Encoder = sub.Encoder

// HttpEncoder is the Ollama-backed Encoder implementation.
type HttpEncoder = sub.HttpEncoder

// Code is the numeric error-code type carried inside Error.
type Code = common.Code

// Error is the structured error returned by MemHop operations.
type Error = common.Error

// Open creates or opens a MemHop database using a default Ollama encoder.
func Open(cfg *MemHopConfig) (*DB, error) {
	return memhopinternal.Open(cfg)
}

// OpenWithEncoder creates or opens a MemHop database with a custom encoder.
func OpenWithEncoder(cfg *MemHopConfig, enc Encoder) (*DB, error) {
	return memhopinternal.OpenWithEncoder(cfg, enc)
}

// CreateEncoder builds the default Ollama HTTP encoder for a config.
func CreateEncoder(cfg *MemHopConfig) (*HttpEncoder, error) {
	return sub.CreateEncoder(cfg)
}

// NewHttpEncoder constructs an Ollama encoder from raw parameters.
func NewHttpEncoder(baseURL string, dim int, model string, timeoutSecs int) (*HttpEncoder, error) {
	return sub.NewHttpEncoder(baseURL, dim, model, timeoutSecs)
}

// RenderCapabilityPrompt renders L5 capabilities as compact prompt cards.
func RenderCapabilityPrompt(caps []Capability) string {
	return sub.RenderCapabilityPrompt(caps)
}

// BuiltinCapabilityFS holds the embedded default L5 capability cards
// (memhop-capability/v2 JSON) shipped under capabilities/. Every Open
// attaches them to L5 query responses automatically; the FS is exported
// for hosts that want to inspect or extend the set.
var BuiltinCapabilityFS fs.FS = capabilities.FS

// HashID computes the stable xxhash64 used for MemHop IDs.
func HashID(s string) uint64 {
	return common.HashID(s)
}

// FormatHash formats an ID hash as the 16-char lowercase hex string used by
// all MemHop APIs.
func FormatHash(h uint64) string {
	return common.FormatHash(h)
}

// ParseID parses a 16-char hex ID string into its numeric hash.
func ParseID(id string) (uint64, error) {
	return common.ParseID(id)
}

// ParseAll parses a batch of hex ID strings; any malformed ID fails the call.
func ParseAll(ids []string) ([]uint64, bool) {
	return common.ParseAll(ids)
}

// FormatIDs formats a batch of numeric ID hashes as hex strings.
func FormatIDs(ids []uint64) []string {
	return common.FormatIDs(ids)
}

// NewError builds a structured MemHop error.
func NewError(code Code, message string, cause ...error) *Error {
	return common.NewError(code, message, cause...)
}

// CodeOf extracts the numeric error code of err (0 when it is not a MemHop Error).
func CodeOf(err error) Code {
	return common.CodeOf(err)
}
