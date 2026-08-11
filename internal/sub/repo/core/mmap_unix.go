// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build !windows

package core

import (
	"os"
	"syscall"

	"github.com/qyiun666/MemHop/internal/sub/common"
)

func MapFile(f *os.File, size int) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, size,
		syscall.PROT_READ,
		syscall.MAP_SHARED)
	if err != nil {
		return nil, common.NewError(common.ErrIO, "mmap failed", err)
	}
	return data, nil
}

func UnmapFile(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if err := syscall.Munmap(data); err != nil {
		return common.NewError(common.ErrIO, "munmap failed", err)
	}
	return nil
}
