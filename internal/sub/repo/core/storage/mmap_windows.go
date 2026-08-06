// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build windows

package storage

import (
	"os"
	"syscall"
	"unsafe"

	"github.com/qyiun666/MemHop/internal/common/mherrors"
)

// MapFile maps a file region of the given size into memory using Windows
// CreateFileMapping + MapViewOfFile.
func MapFile(f *os.File, size int) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}

	// Split size into high/low 32-bit halves for CreateFileMapping.
	sizeHi := uint32(uint64(size) >> 32)
	sizeLo := uint32(size)

	// Create a file mapping object backed by the file.
	// PAGE_READONLY matches the Unix PROT_READ mapping.
	h, err := syscall.CreateFileMapping(
		syscall.Handle(f.Fd()), nil,
		syscall.PAGE_READONLY,
		sizeHi, sizeLo, nil)
	if h == 0 {
		return nil, mherrors.NewError(mherrors.ErrIO, "CreateFileMapping failed", err)
	}
	defer syscall.CloseHandle(h)

	// Map a read-only view of the file into the process address space.
	addr, err := syscall.MapViewOfFile(h, syscall.FILE_MAP_READ, 0, 0, 0)
	if addr == 0 {
		return nil, mherrors.NewError(mherrors.ErrIO, "MapViewOfFile failed", err)
	}
	// Keep the address as unsafe.Pointer (bit-for-bit reinterpret of the
	// system-returned uintptr) so unsafe.Slice takes a Pointer, not a
	// uintptr variable (go vet unsafeptr rule).
	p := *(*unsafe.Pointer)(unsafe.Pointer(&addr))
	return unsafe.Slice((*byte)(p), size), nil
}

// UnmapFile releases a previously mapped region.
func UnmapFile(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	addr := uintptr(unsafe.Pointer(&data[0]))
	if err := syscall.UnmapViewOfFile(addr); err != nil {
		return mherrors.NewError(mherrors.ErrIO, "UnmapViewOfFile failed", err)
	}
	return nil
}
