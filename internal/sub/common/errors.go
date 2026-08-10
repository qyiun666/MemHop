// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Numeric error code system. Code ranges follow G-01:
// 0=success | 1001-1999=parameter | 2001-2999=authentication |
// 3001-3999=resource | 4001-4999=business | 5001-5999=system | 9001-9999=third-party.
// Covers every error source under internal/sub; see each code's source note.
package common

import "errors"

// Code is a numeric error code.
type Code uint16

const (
	// Parameter range 1001-1999
	ErrConfig            Code = 1001 // 1001 configuration error: missing or invalid parameters (config.go validation, encoder address/model, tokenizer init)
	ErrVectorDimMismatch Code = 1002 // 1002 vector dimension mismatch: config vs engine (config.go)
	ErrInvalidQuery      Code = 1003 // 1003 invalid query or ID parse failure (l1-l5layer/scenefind/hash parse, reclaim legacy layout)

	// Resource range 3001-3999
	ErrNotFound Code = 3001 // 3001 resource not found (profile missing, record lookup)

	// System range 5001-5999
	ErrIO              Code = 5001 // 5001 io error: file read/write/lock/mmap failure (storage file operations)
	ErrClosed          Code = 5002 // 5002 database is closed: instance unavailable (engine/reclaim)
	ErrInvalidMagic    Code = 5003 // 5003 invalid magic bytes: .meh file header invalid (header.go)
	ErrCRCMismatch     Code = 5004 // 5004 crc32 mismatch: header/snapshot/record corruption (header/snapshot/record)
	ErrCorruption      Code = 5005 // 5005 data corruption: inconsistent file/snapshot/record structure (engine/reclaim/snapshot/record/compact)
	ErrSerialization   Code = 5006 // 5006 serialization failure: marshal and index snapshot encoding errors (record layer, db.go snapshot)
	ErrDeserialization Code = 5007 // 5007 deserialization failure: unmarshal and index parse errors (record layer, sparse.go, hypergraph)

	// Third-party range 9001-9999
	ErrEncoder Code = 9001 // 9001 encoder error: external vector service failure (encoder.go)
	ErrLLM     Code = 9002 // 9002 llm error: external model call/response parse failure (llm/ files)
)

// Error is a structured error carrying a numeric code.
type Error struct {
	Code    Code
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

// NewError creates a structured error with a numeric code.
func NewError(code Code, message string, cause ...error) *Error {
	e := &Error{Code: code, Message: message}
	if len(cause) > 0 {
		e.Cause = cause[0]
	}
	return e
}

// CodeOf extracts the numeric code from an error; returns 0 for unknown errors.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}
