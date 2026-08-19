// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build windows

package core

import (
	"os"
	"syscall"
	"unsafe"

	"github.com/qyiun666/MemHop/internal/common"
)

func MapFile(f *os.File, size int) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}

	sizeHi := uint32(uint64(size) >> 32)
	sizeLo := uint32(size)

	// PAGE_READONLY matches the Unix PROT_READ mapping.
	h, err := syscall.CreateFileMapping(
		syscall.Handle(f.Fd()), nil,
		syscall.PAGE_READONLY,
		sizeHi, sizeLo, nil)
	if h == 0 {
		return nil, common.NewError(common.ErrIO, "CreateFileMapping failed", err)
	}
	defer syscall.CloseHandle(h)

	addr, err := syscall.MapViewOfFile(h, syscall.FILE_MAP_READ, 0, 0, 0)
	if addr == 0 {
		return nil, common.NewError(common.ErrIO, "MapViewOfFile failed", err)
	}
	// Keep the address as unsafe.Pointer so unsafe.Slice takes a Pointer,
	// not a uintptr variable (go vet unsafeptr rule).
	p := *(*unsafe.Pointer)(unsafe.Pointer(&addr))
	return unsafe.Slice((*byte)(p), size), nil
}

func UnmapFile(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	addr := uintptr(unsafe.Pointer(&data[0]))
	if err := syscall.UnmapViewOfFile(addr); err != nil {
		return common.NewError(common.ErrIO, "UnmapViewOfFile failed", err)
	}
	return nil
}
