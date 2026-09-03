// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The public surface of the facade, pinned by reflection. Session reaches most
// of its methods by embedding internal.Session, so a new internal method
// becomes host-callable without any edit in this package: this list is the
// review gate. Add a name only when the method is meant to be public, and check
// that its signature carries hex-string ids and api-aliased types only.

package api

import (
	"reflect"
	"sort"
	"testing"
)

func methodNames(v any) []string {
	t := reflect.TypeOf(v)
	out := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		out = append(out, t.Method(i).Name)
	}
	sort.Strings(out)
	return out
}

func diffNames(want, got []string) (missing, extra []string) {
	have := make(map[string]struct{}, len(got))
	for _, n := range got {
		have[n] = struct{}{}
	}
	wanted := make(map[string]struct{}, len(want))
	for _, n := range want {
		wanted[n] = struct{}{}
	}
	for _, n := range got {
		if _, ok := wanted[n]; !ok {
			extra = append(extra, n)
		}
	}
	for _, n := range want {
		if _, ok := have[n]; !ok {
			missing = append(missing, n)
		}
	}
	return missing, extra
}

func TestSessionPublicSurface(t *testing.T) {
	want := []string{
		// scene read / turn write
		"Search", "Update",
		// L0 profile
		"GetL0", "UpdateL0", "DistillL0",
		// L2 scenes and topics
		"ListScenes", "ListScenesByL3", "SetSceneL3ID", "SetSceneName",
		"SceneContext", "MergeScenes", "DeleteScene", "DeleteTopic",
		// L3 hypergraphs
		"GetL3", "ListL3", "ImportL3", "UpdateL3", "DeleteL3",
		"QueryL3Nodes", "QueryL3Subgraph",
		// L4 archives
		"SearchL4", "GetArchive",
		// L5 capabilities
		"GetCapability", "ImportCapability", "UpdateCapability", "DeleteCapability",
		"ListCapabilities", "ActivateCapability", "RecordCapabilityUsage",
		// L6 trajectory and plans
		"ReadTrajectory", "AppendTrajectory", "ListTrajectorySessions", "Crystallize",
		"PlanAppend", "PlanCommit", "PlanState", "PlanReplace", "SyncPlanTree", "ListPlans",
		// domain binding, consolidation and file-level lifecycle
		"AgentID", "Dream", "Checkpoint", "IsClosed",
	}
	sort.Strings(want)

	missing, extra := diffNames(want, methodNames(&Session{}))
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("Session public surface drifted: missing=%v unexpected=%v", missing, extra)
	}
}

func TestMultiAgentDBPublicSurface(t *testing.T) {
	want := []string{
		"CreateAgent", "ListAgents", "DeleteAgent", "Session",
		"Checkpoint", "Close", "IsClosed", "Lock", "Unlock",
	}
	sort.Strings(want)

	missing, extra := diffNames(want, methodNames(&MultiAgentDB{}))
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("MultiAgentDB public surface drifted: missing=%v unexpected=%v", missing, extra)
	}
}
