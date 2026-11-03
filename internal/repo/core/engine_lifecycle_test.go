// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"errors"
	"reflect"
	"testing"
)

func TestCleanupStepsRunAllAndReturnFirstError(t *testing.T) {
	tests := []struct {
		name     string
		failures []int
		wantErr  int
	}{
		{name: "unmap", failures: []int{0}, wantErr: 0},
		{name: "sync", failures: []int{1}, wantErr: 1},
		{name: "unlock", failures: []int{2}, wantErr: 2},
		{name: "close", failures: []int{3}, wantErr: 3},
		{name: "first error wins", failures: []int{0, 1, 2, 3}, wantErr: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stepNames := []string{"unmap", "sync", "unlock", "close"}
			stepErrs := []error{
				errors.New("unmap failed"),
				errors.New("sync failed"),
				errors.New("unlock failed"),
				errors.New("close failed"),
			}
			failed := make(map[int]bool, len(tt.failures))
			for _, step := range tt.failures {
				failed[step] = true
			}
			var calls []string
			steps := make([]func() error, len(stepNames))
			for i, name := range stepNames {
				step := i
				steps[i] = func() error {
					calls = append(calls, name)
					if failed[step] {
						return stepErrs[step]
					}
					return nil
				}
			}

			err := cleanupHandles(steps[0], steps[1], steps[2], steps[3])
			if !errors.Is(err, stepErrs[tt.wantErr]) {
				t.Fatalf("error = %v, want %v", err, stepErrs[tt.wantErr])
			}
			if !reflect.DeepEqual(calls, stepNames) {
				t.Fatalf("calls = %v, want %v", calls, stepNames)
			}
		})
	}
}
