// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build windows

package storage

import (
	"os"
	"reflect"
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
	// PAGE_READWRITE matches Unix PROT_READ|PROT_WRITE semantics.
	h, err := syscall.CreateFileMapping(
		syscall.Handle(f.Fd()), nil,
		syscall.PAGE_READWRITE,
		sizeHi, sizeLo, nil)
	if h == 0 {
		return nil, mherrors.NewError(mherrors.ErrIO, "CreateFileMapping failed", err)
	}
	defer syscall.CloseHandle(h)

	// Map a view of the file into the process address space.
	// FILE_MAP_WRITE grants read-write access (includes FILE_MAP_READ).
	addr, err := syscall.MapViewOfFile(h, syscall.FILE_MAP_WRITE, 0, 0, 0)
	if addr == 0 {
		return nil, mherrors.NewError(mherrors.ErrIO, "MapViewOfFile failed", err)
	}

	// Build a byte slice from the mapped address without triggering go vet's
	// unsafeptr warning. By constructing the slice header through a real Go
	// pointer (the local variable b), the uintptr from MapViewOfFile is only
	// stored as an integer struct field — never directly converted via
	// unsafe.Pointer(uintptr).
	var b []byte
	hdr := (*reflect.SliceHeader)(unsafe.Pointer(&b))
	hdr.Data = addr
	hdr.Len = size
	hdr.Cap = size
	return b, nil
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
