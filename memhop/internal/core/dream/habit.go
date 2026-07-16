// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"encoding/json"
	"sort"

	"github.com/qyiun666/memhop/memhop/internal/hash"
	"github.com/qyiun666/memhop/memhop/internal/timeutil"
	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
)

const (
	maxDialogues       = 30
	maxLexicon         = 30
	maxStyleTraits     = 10
	maxEmotionPatterns = 10
)

// HabitUpdateResult holds habit merge metrics.
type HabitUpdateResult struct {
	NewLexicon   int
	NewStyle     int
	NewEmotion   int
}

// ExtractRecentDialogues extracts recent user dialogue texts from L4 archives.
func ExtractRecentDialogues(engine *storage.StorageEngine, maxCount int) []string {
	type tsContent struct {
		ts      int64
		content string
	}
	var archives []tsContent

	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL4Archive {
			return true
		}
		var archive model.ArchiveSlot
		if json.Unmarshal(data, &archive) != nil {
			return true
		}
		if archive.Role == 0 && archive.Content != "" {
			archives = append(archives, tsContent{archive.CreatedAt, archive.Content})
		}
		return true
	})

	sort.Slice(archives, func(i, j int) bool {
		return archives[i].ts > archives[j].ts
	})

	limit := maxCount
	if limit > len(archives) {
		limit = len(archives)
	}
	result := make([]string, limit)
	for i := 0; i < limit; i++ {
		result[i] = archives[i].content
	}
	return result
}

// MergeHabitsIntoProfile merges LLM habit analysis into the existing L0 Profile.
func MergeHabitsIntoProfile(
	engine *storage.StorageEngine,
	analysis *HabitAnalysis,
) (*HabitUpdateResult, error) {
	profileID := hash.HashID("profile")
	profile, err := readProfile(engine, profileID)
	if err != nil {
		return nil, err
	}

	result := &HabitUpdateResult{}
	mergeLexicon(profile, analysis.Lexicon, result)
	mergeStyleTraits(profile, analysis.StyleTraits, result)
	mergeEmotionPatterns(profile, analysis.EmotionPatterns, result)

	profile.UpdatedAt = timeutil.NowMs()
	profile.Version++
	return result, writeProfile(engine, profileID, profile)
}

func mergeLexicon(profile *model.ProfileSlot, lexicon map[string]string, result *HabitUpdateResult) {
	if profile.Lexicon == nil {
		profile.Lexicon = make(map[string]string)
	}
	for word, meaning := range lexicon {
		if _, exists := profile.Lexicon[word]; !exists {
			result.NewLexicon++
		}
		profile.Lexicon[word] = meaning
	}
	if len(profile.Lexicon) > maxLexicon {
		keys := sortedMapKeys(profile.Lexicon)
		for _, k := range keys[maxLexicon:] {
			delete(profile.Lexicon, k)
		}
	}
}

func mergeStyleTraits(profile *model.ProfileSlot, traits []string, result *HabitUpdateResult) {
	for _, tag := range traits {
		if !containsStr(profile.StyleTraits, tag) {
			profile.StyleTraits = append(profile.StyleTraits, tag)
			result.NewStyle++
		}
	}
	if len(profile.StyleTraits) > maxStyleTraits {
		profile.StyleTraits = profile.StyleTraits[:maxStyleTraits]
	}
}

func mergeEmotionPatterns(profile *model.ProfileSlot, patterns map[string]string, result *HabitUpdateResult) {
	if profile.EmotionPatterns == nil {
		profile.EmotionPatterns = make(map[string]string)
	}
	for expr, meaning := range patterns {
		if _, exists := profile.EmotionPatterns[expr]; !exists {
			result.NewEmotion++
		}
		profile.EmotionPatterns[expr] = meaning
	}
	if len(profile.EmotionPatterns) > maxEmotionPatterns {
		keys := sortedMapKeys(profile.EmotionPatterns)
		for _, k := range keys[maxEmotionPatterns:] {
			delete(profile.EmotionPatterns, k)
		}
	}
}

func readProfile(engine *storage.StorageEngine, id uint64) (*model.ProfileSlot, error) {
	rt, data, err := engine.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	if rt != storage.RecL0Profile {
		return nil, core.NewError(core.ErrDeserialization, "not a profile record")
	}
	var profile model.ProfileSlot
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, core.NewError(core.ErrDeserialization, "profile", err)
	}
	return &profile, nil
}

func writeProfile(engine *storage.StorageEngine, id uint64, profile *model.ProfileSlot) error {
	data, err := json.Marshal(profile)
	if err != nil {
		return core.NewError(core.ErrSerialization, "profile", err)
	}
	_, err = engine.WriteRecord(storage.RecL0Profile, id, data)
	return err
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func sortedMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
