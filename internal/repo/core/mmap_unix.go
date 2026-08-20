// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build !windows

package core

import (
	"os"

	"github.com/qyiun666/MemHop/internal/common"
	"golang.org/x/sys/unix"
)

func MapFile(f *os.File, size int) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	data, err := unix.Mmap(int(f.Fd()), 0, size,
		unix.PROT_READ,
		unix.MAP_SHARED)
	if err != nil {
		return nil, common.NewError(common.ErrIO, "mmap failed", err)
	}
	return data, nil
}

func UnmapFile(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if err := unix.Munmap(data); err != nil {
		return common.NewError(common.ErrIO, "munmap failed", err)
	}
	return nil
}
