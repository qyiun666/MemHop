// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 Profile operations.

package query

import (
	"encoding/json"

	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
	"github.com/qyiun666/memhop/memhop/internal/hash"
	"github.com/qyiun666/memhop/memhop/internal/timeutil"
)

// LoadProfileSlot reads the L0 profile from the engine.
func LoadProfileSlot(engine *storage.StorageEngine) (*model.ProfileSlot, error) {
	profileHash := hash.HashID("profile")
	_, data, err := engine.ReadRecord(profileHash)
	if err != nil {
		return nil, core.NewError(core.ErrNotFound, "profile not found")
	}
	var p model.ProfileSlot
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, core.NewError(core.ErrDeserialization, "profile", err)
	}
	return &p, nil
}

// WriteProfile writes a ProfileSlot derived from delta to the engine.
func WriteProfile(engine *storage.StorageEngine, delta ProfileDelta) error {
	now := timeutil.NowMs()
	profileHash := hash.HashID("profile")
	slot := model.ProfileSlot{
		IDHash:          profileHash,
		Name:            derefStr(delta.Name),
		Role:            derefStr(delta.Role),
		Personality:     derefStr(delta.Personality),
		Worldview:       derefStr(delta.Worldview),
		Preferences:     derefMap(delta.Preferences),
		Lexicon:         derefMap(delta.Lexicon),
		StyleTraits:     derefSlice(delta.StyleTraits),
		EmotionPatterns: derefMap(delta.EmotionPatterns),
		CreatedAt:       now,
		UpdatedAt:       now,
		Version:         1,
	}
	data, err := json.Marshal(slot)
	if err != nil {
		return core.NewError(core.ErrSerialization, "marshal profile", err)
	}
	_, err = engine.WriteRecord(storage.RecL0Profile, profileHash, data)
	return err
}

// UpdateProfile reads, modifies, and writes back the L0 profile.
func UpdateProfile(
	engine *storage.StorageEngine,
	req UpdateRequest,
) (*UpdateResult, error) {
	profileHash := hash.HashID("profile")
	_, data, err := engine.ReadRecord(profileHash)
	if err != nil {
		return nil, core.NewError(core.ErrNotFound, "profile not found")
	}
	var profile model.ProfileSlot
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, core.NewError(core.ErrDeserialization, "profile", err)
	}
	applyRawProfileUpdates(&profile, req.Fields)
	profile.UpdatedAt = timeutil.NowMs()
	profile.Version++
	pData, err := json.Marshal(profile)
	if err != nil {
		return nil, core.NewError(core.ErrSerialization, "marshal profile", err)
	}
	_, err = engine.WriteRecord(storage.RecL0Profile, profileHash, pData)
	if err != nil {
		return nil, err
	}
	return &UpdateResult{Status: StatusUpdated, ID: req.ID}, nil
}

// --- nil-safe helpers ---

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func derefSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func applyRawProfileUpdates(p *model.ProfileSlot, fields map[string]json.RawMessage) {
	if raw, ok := fields["name"]; ok {
		json.Unmarshal(raw, &p.Name)
	}
	if raw, ok := fields["role"]; ok {
		json.Unmarshal(raw, &p.Role)
	}
	if raw, ok := fields["personality"]; ok {
		json.Unmarshal(raw, &p.Personality)
	}
	if raw, ok := fields["worldview"]; ok {
		json.Unmarshal(raw, &p.Worldview)
	}
	if raw, ok := fields["preferences"]; ok {
		json.Unmarshal(raw, &p.Preferences)
	}
	if raw, ok := fields["lexicon"]; ok {
		json.Unmarshal(raw, &p.Lexicon)
	}
	if raw, ok := fields["style_traits"]; ok {
		json.Unmarshal(raw, &p.StyleTraits)
	}
	if raw, ok := fields["emotion_patterns"]; ok {
		json.Unmarshal(raw, &p.EmotionPatterns)
	}
}
