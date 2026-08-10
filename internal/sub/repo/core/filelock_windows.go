// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build windows

package core

import (
	"os"
	"syscall"
	"unsafe"

	"github.com/qyiun666/MemHop/internal/sub/common"
)

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modkernel32.NewProc("LockFileEx")
	procUnlockFileEx = modkernel32.NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
	lockAllBytes            = ^uint32(0) // lock the whole file range
)

// lockFile acquires an exclusive, non-blocking lock on the file via
// LockFileEx. One agent binds one MemHop database: a second instance
// opening the same file must fail fast instead of corrupting shared state.
func lockFile(f *os.File) error {
	var ol syscall.Overlapped
	r1, _, err := procLockFileEx.Call(
		f.Fd(),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		0,
		uintptr(lockAllBytes), uintptr(lockAllBytes),
		uintptr(unsafe.Pointer(&ol)))
	if r1 == 0 {
		return common.NewError(common.ErrIO,
			"database already open by another instance", err)
	}
	return nil
}

// unlockFile releases the exclusive lock acquired by lockFile.
func unlockFile(f *os.File) error {
	var ol syscall.Overlapped
	r1, _, err := procUnlockFileEx.Call(
		f.Fd(),
		0,
		uintptr(lockAllBytes), uintptr(lockAllBytes),
		uintptr(unsafe.Pointer(&ol)))
	if r1 == 0 {
		return common.NewError(common.ErrIO, "unlock file", err)
	}
	return nil
}
