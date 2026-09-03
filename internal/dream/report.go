// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

// stageStatus classifies a stage outcome into a report status string:
// ok / cancelled (context errors) / error.
func stageStatus(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return "cancelled"
	default:
		return "error"
	}
}

// AppendStage records one pipeline phase's outcome and wall time in the
// report; a non-nil err is classified cancelled-vs-error via context errors.
func AppendStage(rep *core.DreamReport, name string, start time.Time, err error) {
	rep.Stages = append(rep.Stages, core.DreamStage{Name: name, Status: stageStatus(err), DurationMs: time.Since(start).Milliseconds()})
}

func StageCancelled(ctx context.Context, stage string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("dream: cancelled after %s stage: %w", stage, err)
	}
	return nil
}
