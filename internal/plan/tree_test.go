// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package plan

import (
	"reflect"
	"testing"
)

func TestActiveForestSkipsTerminalStepsAndReparentsTheirDescendants(t *testing.T) {
	full := []PlanNodeView{
		{Seq: 1, Status: PlanDone, Children: []PlanNodeView{
			{Seq: 2, ParentSeq: 1, Title: "active", Status: PlanInProgress, Children: []PlanNodeView{
				{Seq: 3, ParentSeq: 2, Status: PlanFailed, Children: []PlanNodeView{
					{Seq: 4, ParentSeq: 3, Title: "promoted", Status: PlanInProgress},
				}},
			}},
		}},
		{Seq: 5, Status: PlanFailed},
		{Seq: 6, Title: "root", Status: PlanInProgress, Children: []PlanNodeView{
			{Seq: 7, ParentSeq: 6, Status: PlanDone},
		}},
	}
	want := []PlanNodeView{
		{Seq: 2, Title: "active", Status: PlanInProgress, Children: []PlanNodeView{
			{Seq: 4, ParentSeq: 2, Title: "promoted", Status: PlanInProgress, Children: []PlanNodeView{}},
		}},
		{Seq: 6, Title: "root", Status: PlanInProgress, Children: []PlanNodeView{}},
	}
	if got := activeForest(full); !reflect.DeepEqual(got, want) {
		t.Fatalf("active forest = %#v, want %#v", got, want)
	}
}
