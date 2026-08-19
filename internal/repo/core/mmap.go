// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"fmt"
	"os"

	"github.com/qyiun666/MemHop/internal/common"
)

// RemapFile remaps the file at its current size; the new mapping is
// established before the old one is released, so on failure the old
// mapping stays valid.
func RemapFile(f *os.File, oldData []byte) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, common.NewError(common.ErrIO, fmt.Sprintf("stat failed for %s", f.Name()), err)
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
