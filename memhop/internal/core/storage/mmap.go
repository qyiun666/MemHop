// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package storage

import (
	"fmt"
	"os"
	"syscall"

	"github.com/qyiun666/memhop/memhop/internal/core"
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
		return nil, core.NewError(core.ErrIO, "mmap failed", err)
	}
	return data, nil
}

// UnmapFile releases a previously mapped region.
func UnmapFile(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if err := syscall.Munmap(data); err != nil {
		return core.NewError(core.ErrIO, "munmap failed", err)
	}
	return nil
}

// RemapFile remaps the file at its current size and releases the old
// mapping. The new mapping is established before the old one is released,
// so on failure the old mapping stays valid and usable.
func RemapFile(f *os.File, oldData []byte) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, core.NewError(core.ErrIO, fmt.Sprintf("stat failed for %s", f.Name()), err)
	}
	newData, err := MapFile(f, int(info.Size()))
	if err != nil {
		return nil, err
	}
	if err := UnmapFile(oldData); err != nil {
		UnmapFile(newData)
		return nil, err
	}
	return newData, nil
}
