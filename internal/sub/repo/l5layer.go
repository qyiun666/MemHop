// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 plugin operations: query / import (path) / crystallization upsert /
// update / delete / list / match.

package repo

import (
	"fmt"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
	"github.com/qyiun666/MemHop/internal/sub/repo/index"
)

// CreateOrUpdatePluginL5 creates a plugin or, when the same name:trigger
// already exists, preserves its runtime fields (Confidence/SuccessRate/
// TriggerCount/...) and only refreshes Manifest, PluginType, Path and
// UpdatedAt. Returns the plugin ID and whether it already existed.
func CreateOrUpdatePluginL5(engine *core.StorageEngine, name, trigger, pluginType string, manifest core.PluginManifest, path *string) (uint64, bool, error) {
	pluginID := common.HashID(fmt.Sprintf("%s:%s", name, trigger))
	now := time.Now().UnixMilli()
	if existing, err := core.ReadPluginSlot(engine, pluginID); err == nil {
		existing.Manifest = manifest
		existing.PluginType = pluginType
		existing.Path = path
		existing.UpdatedAt = now
		if err := core.WritePluginSlot(engine, pluginID, existing); err != nil {
			return 0, true, err
		}
		return pluginID, true, nil
	}
	plugin := &core.PluginSlot{
		IDHash:     pluginID,
		Name:       name,
		Trigger:    trigger,
		PluginType: pluginType,
		Status:     core.PluginActive,
		Manifest:   manifest,
		Path:       path,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := core.WritePluginSlot(engine, pluginID, plugin); err != nil {
		return 0, false, err
	}
	return pluginID, false, nil
}

func GetPluginL5(engine *core.StorageEngine, id string) (*core.PluginSlot, error) {
	idHash, err := common.ParseID(id)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse plugin id", err)
	}
	return core.ReadPluginSlot(engine, idHash)
}

func UpdatePluginL5(engine *core.StorageEngine, id string, slot *core.PluginSlot) error {
	idHash, err := common.ParseID(id)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse plugin id", err)
	}
	slot.IDHash = idHash
	slot.UpdatedAt = time.Now().UnixMilli()
	return core.WritePluginSlot(engine, idHash, slot)
}

// DeletePluginL5 removes a plugin record (plugins have no child records).
func DeletePluginL5(engine *core.StorageEngine, id string) bool {
	pluginHash, err := common.ParseID(id)
	if err != nil {
		return false
	}
	_, err = engine.DeleteRecordBatch([]uint64{pluginHash})
	return err == nil
}

func ListPluginsL5(engine *core.StorageEngine) []core.PluginSlot {
	return core.CollectAllPlugins(engine)
}

// MatchPluginsL5 returns plugins whose name or trigger contains any query
// term (case-insensitive substring, tokenized by the shared tokenizer).
func MatchPluginsL5(engine *core.StorageEngine, query string) []core.PluginSlot {
	terms := index.Tokenize(query)
	if len(terms) == 0 {
		return nil
	}
	var out []core.PluginSlot
	for _, plugin := range core.CollectAllPlugins(engine) {
		text := strings.ToLower(plugin.Name + " " + plugin.Trigger)
		for _, term := range terms {
			if strings.Contains(text, strings.ToLower(term)) {
				out = append(out, plugin)
				break
			}
		}
	}
	return out
}
