package test

import (
	"encoding/json"
	"testing"

	"memhop/api"
	"memhop/test/testsupport"
)

// TestL5WriteAPI tests the new L5 write API methods via v0.60.0 Crystal(op).
// Covers: create chain, append step, incr trigger, update confidence, batch delete.
func TestL5WriteAPI(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	// ── 1. Create action chain ──
	r, err := mh.Crystal(memhop.CrystalOp{
		Kind: memhop.COpCreateChain,
		ChainInput: &memhop.L5ChainInput{
			Title:   "test_chain",
			Trigger: "user asks about weather",
			Steps: []memhop.L5StepInput{
				{Action: "call_weather_api", Parameters: nil},
			},
		},
	})
	if err != nil {
		t.Fatalf("Crystal(COpCreateChain) failed: %v", err)
	}
	chainID := r.ChainID
	if chainID == "" {
		t.Fatal("COpCreateChain returned empty ID")
	}
	t.Logf("Created chain: %s", chainID)

	// ── 2. Get(LayerCrystal) verifies chain exists ──
	getRes, err := mh.Get(memhop.LayerCrystal, chainID)
	if err != nil {
		t.Fatalf("Get(LayerCrystal) failed: %v", err)
	}
	chain := getRes.Crystal
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

	// ── 3. Append step ──
	r2, err := mh.Crystal(memhop.CrystalOp{
		Kind:      memhop.COpAppendStep,
		ChainID:   chainID,
		StepInput: &memhop.L5StepInput{Action: "parse_result", Parameters: nil},
	})
	if err != nil {
		t.Fatalf("Crystal(COpAppendStep) failed: %v", err)
	}
	if r2.StepID == "" {
		t.Fatal("COpAppendStep returned empty ID")
	}
	t.Logf("Appended step: %s", r2.StepID)

	// ── 4. Incr trigger ──
	if _, err := mh.Crystal(memhop.CrystalOp{Kind: memhop.COpIncrTrigger, ChainID: chainID}); err != nil {
		t.Fatalf("Crystal(COpIncrTrigger) failed: %v", err)
	}
	getRes, err = mh.Get(memhop.LayerCrystal, chainID)
	if err != nil {
		t.Fatalf("Get(LayerCrystal) after COpIncrTrigger failed: %v", err)
	}
	chain = getRes.Crystal
	if chain.TriggerCount != 1 {
		t.Errorf("TriggerCount = %d, want 1", chain.TriggerCount)
	}
	if chain.LastTriggered == nil {
		t.Error("LastTriggered should be set after COpIncrTrigger")
	}

	// ── 5. Update confidence (success) ──
	if _, err := mh.Crystal(memhop.CrystalOp{
		Kind: memhop.COpUpdateConfidence, ChainID: chainID, Success: true,
	}); err != nil {
		t.Fatalf("Crystal(COpUpdateConfidence success) failed: %v", err)
	}
	getRes, err = mh.Get(memhop.LayerCrystal, chainID)
	if err != nil {
		t.Fatalf("Get(LayerCrystal) after COpUpdateConfidence(success) failed: %v", err)
	}
	if getRes.Crystal.SuccessRate <= 0 {
		t.Errorf("SuccessRate = %f, want > 0", getRes.Crystal.SuccessRate)
	}

	// ── 6. Update confidence (failure) ──
	if _, err := mh.Crystal(memhop.CrystalOp{
		Kind: memhop.COpUpdateConfidence, ChainID: chainID, Success: false,
	}); err != nil {
		t.Fatalf("Crystal(COpUpdateConfidence failure) failed: %v", err)
	}

	// ── 7. Batch delete ──
	r3, err := mh.Crystal(memhop.CrystalOp{
		Kind: memhop.COpCreateChain,
		ChainInput: &memhop.L5ChainInput{
			Title:   "test_chain_2",
			Trigger: "user asks about time",
		},
	})
	if err != nil {
		t.Fatalf("COpCreateChain (2nd) failed: %v", err)
	}

	if _, err := mh.Crystal(memhop.CrystalOp{
		Kind: memhop.COpBatchDelete, IDs: []string{chainID, r3.ChainID},
	}); err != nil {
		t.Fatalf("Crystal(COpBatchDelete) failed: %v", err)
	}

	// Verify both chains are deleted
	if _, err := mh.Get(memhop.LayerCrystal, chainID); err == nil {
		t.Error("Get(LayerCrystal) should return error for deleted chain")
	}
	if _, err := mh.Get(memhop.LayerCrystal, r3.ChainID); err == nil {
		t.Error("Get(LayerCrystal) should return error for deleted chain 2")
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

	// Verify the topic exists via Get(LayerTopic)
	beforeRes, err := mh.Get(memhop.LayerTopic, topicID)
	if err != nil {
		t.Fatalf("Get(LayerTopic) failed: %v", err)
	}
	beforeDetail := beforeRes.Topic
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

	// ── 3. Verify L4 refs via Get(LayerTopic) ──
	afterRes, err := mh.Get(memhop.LayerTopic, topicID)
	if err != nil {
		t.Fatalf("Get(LayerTopic) after update failed: %v", err)
	}
	afterDetail := afterRes.Topic

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
			archRes, err := mh.Get(memhop.LayerArchive, refID)
			if err != nil {
				t.Errorf("Get(LayerArchive, %s) failed: %v", refID, err)
				continue
			}
			archive := archRes.Archive
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
