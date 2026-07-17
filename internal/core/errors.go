package core

import "errors"

var (
	ErrIO                = errors.New("memhop: io error")
	ErrInvalidMagic      = errors.New("memhop: invalid magic bytes")
	ErrCRCMismatch       = errors.New("memhop: crc32 mismatch")
	ErrCorruption        = errors.New("memhop: data corruption")
	ErrNotFound          = errors.New("memhop: not found")
	ErrVectorDimMismatch = errors.New("memhop: vector dimension mismatch")
	ErrSerialization     = errors.New("memhop: serialization error")
	ErrDeserialization   = errors.New("memhop: deserialization error")
	ErrEncoder           = errors.New("memhop: encoder error")
	ErrConfig            = errors.New("memhop: configuration error")
	ErrLLM               = errors.New("memhop: llm error")
	ErrInvalidQuery      = errors.New("memhop: invalid query")
	ErrClosed            = errors.New("memhop: database is closed")
)

// MemHopError contains structured error details.
type MemHopError struct {
	Kind    error
	Message string
	Cause   error
}

func (e *MemHopError) Error() string {
	if e.Cause != nil {
		return e.Kind.Error() + ": " + e.Message + ": " + e.Cause.Error()
	}
	return e.Kind.Error() + ": " + e.Message
}

func (e *MemHopError) Unwrap() error        { return e.Cause }
func (e *MemHopError) Is(target error) bool { return errors.Is(e.Kind, target) }

// NewError creates a MemHopError with kind, message, and optional cause.
func NewError(kind error, message string, cause ...error) *MemHopError {
	e := &MemHopError{Kind: kind, Message: message}
	if len(cause) > 0 {
		e.Cause = cause[0]
	}
	return e
}
