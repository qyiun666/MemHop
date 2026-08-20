// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build !windows

package core

import (
	"os"

	"github.com/qyiun666/MemHop/internal/common"
	"golang.org/x/sys/unix"
)

// lockFile acquires an exclusive, non-blocking advisory lock: a second
// instance opening the same file must fail fast, not corrupt shared state.
func lockFile(f *os.File) error {
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return common.NewError(common.ErrIO,
			"database already open by another instance", err)
	}
	return nil
}

func unlockFile(f *os.File) error {
	if err := unix.Flock(int(f.Fd()), unix.LOCK_UN); err != nil {
		return common.NewError(common.ErrIO, "unlock file", err)
	}
	return nil
}
