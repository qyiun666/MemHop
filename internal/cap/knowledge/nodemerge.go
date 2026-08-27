// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Node field-merge policy of the knowledge capability: how an import folds
// into an existing hypergraph node (skip vs append vs replace).

package knowledge

import (
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// MergeFields folds imported values into an existing node: an empty imported
// value keeps the current one, a non-empty NodeType replaces, content is
// appended only when it adds information, keywords are unioned.
func MergeFields(node *core.HypergraphNode, nodeType, content string, keywords []string, now int64) {
	if nodeType != "" {
		node.NodeType = nodeType
	}
	node.Content = mergeContent(node.Content, content)
	node.Keywords = common.Union(node.Keywords, keywords)
	node.UpdatedAt = now
}

// OverwriteFields replaces the mutable fields of an existing node. The ID and
// graph membership are stable and untouched.
func OverwriteFields(node *core.HypergraphNode, nodeType, content string, keywords []string, now int64) {
	node.NodeType = nodeType
	node.Content = content
	node.Keywords = slices.Clone(keywords)
	node.UpdatedAt = now
}

func mergeContent(oldContent, newContent string) string {
	switch {
	case newContent == "":
		return oldContent
	case oldContent == "":
		return newContent
	case oldContent == newContent || strings.Contains(oldContent, newContent):
		return oldContent
	case strings.Contains(newContent, oldContent):
		return newContent
	default:
		return oldContent + "\n" + newContent
	}
}
