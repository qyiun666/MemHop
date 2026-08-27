// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Error contract of the public facade: the numeric error-code type, the
// CodeOf extractor and the error-code constants, all forwarded to the
// internal re-export seam (interval contract: 1001-1999 parameter /
// 3001-3999 resource / 5001-5999 system / 9001-9999 third-party).

package api

import "github.com/qyiun666/MemHop/internal"

// Code is the numeric error-code type carried inside Error.
type Code = internal.Code

// CodeOf extracts the numeric error code of err (0 when it is not a MemHop Error).
func CodeOf(err error) Code { return internal.CodeOf(err) }

// Error codes of the public contract.
const (
	ErrConfig            = internal.ErrConfig
	ErrVectorDimMismatch = internal.ErrVectorDimMismatch
	ErrInvalidQuery      = internal.ErrInvalidQuery
	ErrNotFound          = internal.ErrNotFound
	ErrAgentNotFound     = internal.ErrAgentNotFound
	ErrIO                = internal.ErrIO
	ErrClosed            = internal.ErrClosed
	ErrInvalidMagic      = internal.ErrInvalidMagic
	ErrCRCMismatch       = internal.ErrCRCMismatch
	ErrCorruption        = internal.ErrCorruption
	ErrSerialization     = internal.ErrSerialization
	ErrDeserialization   = internal.ErrDeserialization
	ErrEncoder           = internal.ErrEncoder
	ErrLLM               = internal.ErrLLM
)
