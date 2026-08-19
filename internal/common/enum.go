// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package common

import (
	"encoding/json"
	"fmt"
)

// EnumString returns the display name of v, falling back to "TypeName(n)"
// for unknown values.
func EnumString[T ~uint8](v T, names map[T]string, typeName string) string {
	if s, ok := names[v]; ok {
		return s
	}
	return fmt.Sprintf("%s(%d)", typeName, uint8(v))
}

func EnumMarshal[T ~uint8](v T) ([]byte, error) {
	return json.Marshal(uint8(v))
}

func EnumAssign[T ~uint8](dst *T, data []byte) error {
	var v uint8
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*dst = T(v)
	return nil
}
