package test

import (
	"encoding/json"
	"testing"

	"github.com/qyiun666/memhop/memhop"
	"github.com/qyiun666/memhop/test/testsupport"
)

// TestL5WriteAPI tests the new L5 write API methods:
// CreateActionChain, AppendActionStep, IncrChainTrigger,
// UpdateChainConfidence, BatchDeleteCrystals.
func TestL5WriteAPI(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	// ── 1. CreateActionChain ──
	chainID, err := mh.CreateActionChain(memhop.L5ChainInput{
		Title:   "test_chain",
		Trigger: "user asks about weather",
		Steps: []memhop.L5StepInput{
			{Action: "call_weather_api", Parameters: nil},
		},
	})
	if err != nil {
		t.Fatalf("CreateActionChain failed: %v", err)
	}
	if chainID == "" {
		t.Fatal("CreateActionChain returned empty ID")
	}
	t.Logf("Created chain: %s", chainID)

	// ── 2. GetL5 verifies chain exists ──
	chain, err := mh.GetL5(chainID)
	if err != nil {
		t.Fatalf("GetL5 failed: %v", err)
	}
	if chain.Title != "test_chain" {
		t.Errorf("Title = %q, want %q", chain.Title, "test_chain")
	}
	if chain.Condition != "user asks about weather" {
		t.Errorf("Condition = %q, want %q", chain.Condition, "user asks about weather")
	}
	if chain.Status != "draft" {
		t.Errorf("Status = %q, want %q", chain.Status, "draft")
	}
	if chain.TriggerCount != 0 {
		t.Errorf("TriggerCount = %d, want 0", chain.TriggerCount)
	}

	// ── 3. AppendActionStep ──
	stepID, err := mh.AppendActionStep(chainID, memhop.L5StepInput{
		Action:     "parse_result",
		Parameters: nil,
	})
	if err != nil {
		t.Fatalf("AppendActionStep failed: %v", err)
	}
	if stepID == "" {
		t.Fatal("AppendActionStep returned empty ID")
	}
	t.Logf("Appended step: %s", stepID)

	// ── 4. IncrChainTrigger ──
	err = mh.IncrChainTrigger(chainID)
	if err != nil {
		t.Fatalf("IncrChainTrigger failed: %v", err)
	}
	chain, err = mh.GetL5(chainID)
	if err != nil {
		t.Fatalf("GetL5 after IncrChainTrigger failed: %v", err)
	}
	if chain.TriggerCount != 1 {
		t.Errorf("TriggerCount = %d, want 1", chain.TriggerCount)
	}
	if chain.LastTriggered == nil {
		t.Error("LastTriggered should be set after IncrChainTrigger")
	}

	// ── 5. UpdateChainConfidence (success) ──
	err = mh.UpdateChainConfidence(chainID, true)
	if err != nil {
		t.Fatalf("UpdateChainConfidence(success) failed: %v", err)
	}
	chain, err = mh.GetL5(chainID)
	if err != nil {
		t.Fatalf("GetL5 after UpdateChainConfidence(success) failed: %v", err)
	}
	if chain.SuccessRate <= 0 {
		t.Errorf("SuccessRate = %f, want > 0", chain.SuccessRate)
	}

	// ── 6. UpdateChainConfidence (failure) ──
	err = mh.UpdateChainConfidence(chainID, false)
	if err != nil {
		t.Fatalf("UpdateChainConfidence(failure) failed: %v", err)
	}

	// ── 7. BatchDeleteCrystals ──
	chainID2, err := mh.CreateActionChain(memhop.L5ChainInput{
		Title:   "test_chain_2",
		Trigger: "user asks about time",
	})
	if err != nil {
		t.Fatalf("CreateActionChain (2nd) failed: %v", err)
	}

	err = mh.BatchDeleteCrystals([]string{chainID, chainID2})
	if err != nil {
		t.Fatalf("BatchDeleteCrystals failed: %v", err)
	}

	// Verify both chains are deleted
	_, err = mh.GetL5(chainID)
	if err == nil {
		t.Error("GetL5 should return error for deleted chain")
	}
	_, err = mh.GetL5(chainID2)
	if err == nil {
		t.Error("GetL5 should return error for deleted chain 2")
	}

	t.Log("✓ All L5 Write API tests passed")
}

// TestUpdateL4Append tests that UpdateMemory with dialogue_text appends L4 archive
// and updates the topic's L4 references.
func TestUpdateL4Append(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	// ── 1. Search to create an L2 topic ──
	result, err := mh.Search(memhop.SearchQuery{Text: "Hello, this is a test query for L4 append", AutoCreate: true})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(result.Contexts) == 0 {
		t.Fatal("Search returned no contexts")
	}

	topicID := result.Contexts[0].ID
	t.Logf("Created L2 topic: %s (scene=%s)", topicID, result.Contexts[0].SceneID)

	// Verify the topic exists via GetL2
	beforeDetail, err := mh.GetL2(topicID)
	if err != nil {
		t.Fatalf("GetL2 failed: %v", err)
	}
	beforeRefCount := len(beforeDetail.UserL4Refs) + len(beforeDetail.AgentL4Refs)
	t.Logf("Before update: L4 refs = %d (user=%d agent=%d)",
		beforeRefCount, len(beforeDetail.UserL4Refs), len(beforeDetail.AgentL4Refs))

	// ── 2. UpdateMemory with dialogue_text ──
	fields := map[string]json.RawMessage{
		"dialogue_text": json.RawMessage(`"hello agent"`),
		"role":          json.RawMessage(`1`),
	}

	updateResult, err := mh.UpdateMemory(memhop.UpdateRequest{
		ID:     topicID,
		Layer:  2,
		Fields: fields,
	})
	if err != nil {
		t.Fatalf("UpdateMemory failed: %v", err)
	}
	if updateResult.Status != "Updated" {
		t.Errorf("Status = %q, want %q", updateResult.Status, "Updated")
	}

	// ── 3. Verify L4 refs via GetL2 ──
	afterDetail, err := mh.GetL2(topicID)
	if err != nil {
		t.Fatalf("GetL2 after update failed: %v", err)
	}

	afterRefCount := len(afterDetail.UserL4Refs) + len(afterDetail.AgentL4Refs)
	if afterRefCount <= beforeRefCount {
		t.Errorf("L4 refs did not increase: before=%d after=%d", beforeRefCount, afterRefCount)
	}

	// The new ref should be in AgentL4Refs (role=1)
	if len(afterDetail.AgentL4Refs) <= len(beforeDetail.AgentL4Refs) {
		t.Error("AgentL4Refs should have increased after update with role=1")
	} else {
		// Find the new ref
		newRefs := afterDetail.AgentL4Refs[len(beforeDetail.AgentL4Refs):]
		for _, refID := range newRefs {
			archive, err := mh.GetArchive(refID)
			if err != nil {
				t.Errorf("GetArchive(%s) failed: %v", refID, err)
				continue
			}
			if archive == nil {
				t.Errorf("Archive %s is nil", refID)
				continue
			}
			if archive.Content != "hello agent" {
				t.Errorf("Archive content = %q, want %q", archive.Content, "hello agent")
			}
			t.Logf("Verified L4 archive: ID=%s Content=%q Type=%s",
				archive.ID, archive.Content, archive.ContentType)
		}
	}

	t.Log("✓ Update L4 append test passed")
}
