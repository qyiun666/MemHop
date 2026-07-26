// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build !windows

package storage

import (
	"os"
	"syscall"

	"github.com/qyiun666/MemHop/internal/common/mherrors"
)

// lockFile acquires an exclusive, non-blocking advisory lock on the file.
// One agent binds one MemHop database: a second instance opening the same
// file must fail fast instead of corrupting shared state.
func lockFile(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return mherrors.NewError(mherrors.ErrIO,
			"database already open by another instance", err)
	}
	return nil
}

// unlockFile releases the exclusive lock acquired by lockFile.
func unlockFile(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		return mherrors.NewError(mherrors.ErrIO, "unlock file", err)
	}
	return nil
}
