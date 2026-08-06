// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 Profile operations.

package crud

import (
	"encoding/json"
	"fmt"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// LoadProfileSlot reads the L0 profile from the engine.
func LoadProfileSlot(engine *storage.StorageEngine) (*model.ProfileSlot, error) {
	profileHash := hash.HashID("profile")
	_, data, err := engine.ReadRecord(profileHash)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrNotFound, "profile not found")
	}
	var p model.ProfileSlot
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, mherrors.NewError(mherrors.ErrDeserialization, "profile", err)
	}
	return &p, nil
}

// WriteProfile writes a ProfileSlot derived from delta to the engine.
func WriteProfile(engine *storage.StorageEngine, delta ProfileDelta) error {
	profileHash := hash.HashID("profile")
	slot := model.ProfileSlot{
		IDHash:          profileHash,
		Name:            derefStr(delta.Name),
		Role:            derefStr(delta.Role),
		Personality:     derefStr(delta.Personality),
		Preferences:     derefMap(delta.Preferences),
		Lexicon:         derefMap(delta.Lexicon),
		StyleTraits:     derefSlice(delta.StyleTraits),
		EmotionPatterns: derefMap(delta.EmotionPatterns),
	}
	data, err := json.Marshal(slot)
	if err != nil {
		return mherrors.NewError(mherrors.ErrSerialization, "marshal profile", err)
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
		return nil, mherrors.NewError(mherrors.ErrNotFound, "profile not found")
	}
	var profile model.ProfileSlot
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, mherrors.NewError(mherrors.ErrDeserialization, "profile", err)
	}
	if err := applyRawProfileUpdates(&profile, req.Fields); err != nil {
		return nil, err
	}
	pData, err := json.Marshal(profile)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrSerialization, "marshal profile", err)
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

// applyRawProfileUpdates applies raw JSON field updates to the profile.
// Any field whose JSON does not match the target type returns an
// ErrInvalidQuery instead of being silently skipped.
func applyRawProfileUpdates(p *model.ProfileSlot, fields map[string]json.RawMessage) error {
	apply := func(key string, dst any) error {
		raw, ok := fields[key]
		if !ok {
			return nil
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			return mherrors.NewError(mherrors.ErrInvalidQuery,
				fmt.Sprintf("profile field %q has wrong type", key), err)
		}
		return nil
	}
	if err := apply("name", &p.Name); err != nil {
		return err
	}
	if err := apply("role", &p.Role); err != nil {
		return err
	}
	if err := apply("personality", &p.Personality); err != nil {
		return err
	}
	if err := apply("preferences", &p.Preferences); err != nil {
		return err
	}
	if err := apply("lexicon", &p.Lexicon); err != nil {
		return err
	}
	if err := apply("style_traits", &p.StyleTraits); err != nil {
		return err
	}
	if err := apply("emotion_patterns", &p.EmotionPatterns); err != nil {
		return err
	}
	return nil
}
