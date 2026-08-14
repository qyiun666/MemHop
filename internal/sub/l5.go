// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 plugin operations of the sub layer: import from path / query / delete /
// list. Plugins are created only by path import or crystallization, never
// by hand.

package sub

import (
	"encoding/json"
	"os"
	"sort"
	"strings"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// PluginImport is the JSON description of a plugin read from an import path.
type PluginImport struct {
	Name       string              `json:"name"`
	Trigger    string              `json:"trigger"`
	PluginType string              `json:"plugin_type"`
	Manifest   core.PluginManifest `json:"manifest"`
}

// ImportPlugin reads a plugin description (PluginImport JSON) from path and
// upserts it with Path = path; ID = hash(name:trigger) makes re-import
// idempotent. The caller (internal layer) holds the write lock.
func (db *DB) ImportPlugin(path string) (string, error) {
	if path == "" {
		return "", common.NewError(common.ErrInvalidQuery, "import path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", common.NewError(common.ErrIO, "read plugin import file", err)
	}
	var in PluginImport
	if err := json.Unmarshal(data, &in); err != nil {
		return "", common.NewError(common.ErrInvalidQuery, "parse plugin import file", err)
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Trigger) == "" {
		return "", common.NewError(common.ErrInvalidQuery, "name and trigger are required")
	}
	id, _, err := repo.CreateOrUpdatePluginL5(db.engine, in.Name, in.Trigger, in.PluginType, in.Manifest, &path)
	if err != nil {
		return "", err
	}
	return common.FormatHash(id), nil
}

func (db *DB) GetPlugin(id string) (*core.PluginSlot, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	return repo.GetPluginL5(db.engine, id)
}

// DeletePlugin removes a plugin record.
func (db *DB) DeletePlugin(id string) error {
	if _, err := common.ParseID(id); err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse plugin id", err)
	}
	if !repo.DeletePluginL5(db.engine, id) {
		return common.NewError(common.ErrIO, "delete plugin", nil)
	}
	return nil
}

type PluginListQuery struct {
	Status     *string `json:"status,omitempty"`      // "draft"/"active"/"deprecated"
	PluginType *string `json:"plugin_type,omitempty"` // primary type label filter
	Keyword    string  `json:"keyword,omitempty"`     // Name substring (case-insensitive)
}

func (db *DB) ListPlugins(q PluginListQuery) ([]core.PluginSlot, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	kw := strings.ToLower(q.Keyword)
	all := repo.ListPluginsL5(db.engine)
	filtered := make([]core.PluginSlot, 0, len(all))
	for _, plugin := range all {
		if q.Status != nil && plugin.Status.String() != *q.Status {
			continue
		}
		if q.PluginType != nil && plugin.PluginType != *q.PluginType {
			continue
		}
		if kw != "" && !strings.Contains(strings.ToLower(plugin.Name), kw) {
			continue
		}
		filtered = append(filtered, plugin)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].UpdatedAt > filtered[j].UpdatedAt
	})
	if filtered == nil {
		return []core.PluginSlot{}, nil
	}
	return filtered, nil
}
