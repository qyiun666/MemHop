// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"encoding/json"
	"fmt"

	"memhop/internal/core/index"
	"memhop/internal/core/model"
	"memhop/internal/core/record"
	"memhop/internal/core/storage"
	"memhop/internal/common/hash"
	"memhop/internal/common/timeutil"
	"memhop/internal/common/mherrors"
)

// GenerateProfile regenerates the L0 Profile from sparse index keyword distribution.
func GenerateProfile(
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
) error {
	topKeywords := sparseIdx.TopTerms(20)
	topTerms := make([]string, len(topKeywords))
	for i, tk := range topKeywords {
		topTerms[i] = tk.Term
	}

	totalEngrams := engine.RecordCount()
	nowMs := timeutil.NowMs()
	profileID := hash.HashID("profile")

	existing, err := tryReadProfile(engine, profileID)
	if err != nil {
		return err
	}

	if existing != nil {
		return updateExistingProfile(engine, existing, profileID, topTerms, totalEngrams, nowMs)
	}
	return createNewProfile(engine, profileID, topTerms, totalEngrams, nowMs)
}

func tryReadProfile(engine *storage.StorageEngine, profileID uint64) (*model.ProfileSlot, error) {
	rt, data, err := engine.ReadRecord(profileID)
	if err != nil {
		// Not found is OK — we'll create a new one.
		return nil, nil
	}
	if rt != storage.RecL0Profile {
		return nil, nil
	}
	var profile model.ProfileSlot
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, mherrors.NewError(mherrors.ErrDeserialization, "profile", err)
	}
	return &profile, nil
}

func updateExistingProfile(
	engine *storage.StorageEngine,
	profile *model.ProfileSlot,
	profileID uint64,
	topTerms []string,
	totalEngrams uint32,
	nowMs int64,
) error {
	profile.Personality = joinTopTerms(topTerms, 5)
	if profile.Preferences == nil {
		profile.Preferences = make(map[string]string)
	}
	profile.Preferences["top_keywords"] = joinTopTerms(topTerms, 20)
	profile.Preferences["total_engrams"] = fmt.Sprintf("%d", totalEngrams)
	profile.UpdatedAt = nowMs
	profile.Version++
	return record.WriteProfileSlot(engine, profileID, profile)
}

func createNewProfile(
	engine *storage.StorageEngine,
	profileID uint64,
	topTerms []string,
	totalEngrams uint32,
	nowMs int64,
) error {
	prefs := map[string]string{
		"top_keywords":  joinTopTerms(topTerms, 20),
		"total_engrams": fmt.Sprintf("%d", totalEngrams),
	}
	slot := model.ProfileSlot{
		IDHash:          profileID,
		Name:            "Agent",
		Role:            "assistant",
		Personality:     joinTopTerms(topTerms, 5),
		Worldview:       "",
		Preferences:     prefs,
		Lexicon:         make(map[string]string),
		StyleTraits:     []string{},
		EmotionPatterns: make(map[string]string),
		CreatedAt:       nowMs,
		UpdatedAt:       nowMs,
		Version:         1,
	}
	return record.WriteProfileSlot(engine, profileID, &slot)
}

func joinTopTerms(terms []string, n int) string {
	limit := n
	if limit > len(terms) {
		limit = len(terms)
	}
	result := ""
	for i := 0; i < limit; i++ {
		if i > 0 {
			result += ", "
		}
		result += terms[i]
	}
	return result
}
