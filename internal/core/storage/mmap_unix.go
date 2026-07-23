// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build !windows

package storage

import (
	"os"
	"syscall"

	"memhop/internal/common/mherrors"
)

// MapFile maps a file region of the given size into memory.
func MapFile(f *os.File, size int) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, size,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_SHARED)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrIO, "mmap failed", err)
	}
	return data, nil
}

// UnmapFile releases a previously mapped region.
func UnmapFile(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if err := syscall.Munmap(data); err != nil {
		return mherrors.NewError(mherrors.ErrIO, "munmap failed", err)
	}
	return nil
}
