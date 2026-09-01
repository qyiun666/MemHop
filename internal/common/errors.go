// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Numeric error code system (G-01): 0=success | 1001-1999 parameter |
// 2001-2999 auth | 3001-3999 resource | 4001-4999 business | 5001-5999
// system | 9001-9999 third-party.
package common

import "errors"

type Code uint16

// ErrTruncated marks an LLM response cut off by the output token ceiling
// (finish_reason=length); callers use errors.Is to escalate their budget and
// retry. Transport-level sentinel, shared by the provider and the LLM
// capabilities.
var ErrTruncated = errors.New("llm response truncated")

const (
	ErrConfig       Code = 1001 // 1001 configuration error: missing or invalid parameters (config.go validation, tokenizer init)
	ErrInvalidQuery Code = 1003 // 1003 invalid query or ID parse failure (hex id parse, layer read/write guards, reclaim legacy layout)

	ErrNotFound      Code = 3001 // 3001 resource not found (profile missing, record lookup)
	ErrAgentNotFound Code = 3002 // 3002 agent domain not found: unregistered or deleted agentID (contextFor, Session)

	ErrIO              Code = 5001 // 5001 io error: file read/write/lock/mmap failure (storage file operations)
	ErrClosed          Code = 5002 // 5002 database is closed: instance unavailable (engine/reclaim)
	ErrInvalidMagic    Code = 5003 // 5003 invalid magic bytes: .meh file header invalid (header.go)
	ErrCRCMismatch     Code = 5004 // 5004 crc32 mismatch: header/snapshot/record corruption (header/snapshot/record)
	ErrCorruption      Code = 5005 // 5005 data corruption: inconsistent file/snapshot/record structure (engine/reclaim/snapshot/record/compact)
	ErrSerialization   Code = 5006 // 5006 serialization failure: marshal and index snapshot encoding errors (record layer, db.go snapshot)
	ErrDeserialization Code = 5007 // 5007 deserialization failure: unmarshal and index parse errors (record layer, sparse.go, hypergraph)

	ErrLLM Code = 9002 // 9002 llm error: external model call/response parse failure (llm/ files)
	// 9001 (encoder error) was retired with the embedding-service dependency
	// and 1002 (vector-dimension mismatch) with the retrieval subsystem that
	// compared the configured dimension to the file header. Both numbers stay
	// reserved and are never reused.
)

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

func NewError(code Code, message string, cause ...error) *Error {
	e := &Error{Code: code, Message: message}
	if len(cause) > 0 {
		e.Cause = cause[0]
	}
	return e
}

func CodeOf(err error) Code {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Code
	}
	return 0
}
