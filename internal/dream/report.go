// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
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

// StageCancelled reports one pipeline checkpoint's cancellation. The returned
// error carries ErrCancelled, because an error code 0 is the code of a pass that
// finished: a stopped pass that reports no code is indistinguishable from one
// that succeeded.
func StageCancelled(ctx context.Context, stage string) error {
	if err := ctx.Err(); err != nil {
		return common.NewError(common.ErrCancelled,
			fmt.Sprintf("dream: cancelled after %s stage", stage), err)
	}
	return nil
}
