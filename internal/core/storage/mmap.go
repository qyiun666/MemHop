// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package storage

import (
	"fmt"
	"os"

	"memhop/internal/common/mherrors"
)

// MapFile is platform-specific — see mmap_unix.go or mmap_windows.go.
// UnmapFile is platform-specific — see mmap_unix.go or mmap_windows.go.

// RemapFile remaps the file at its current size and releases the old
// mapping. The new mapping is established before the old one is released,
// so on failure the old mapping stays valid and usable.
func RemapFile(f *os.File, oldData []byte) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrIO, fmt.Sprintf("stat failed for %s", f.Name()), err)
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
