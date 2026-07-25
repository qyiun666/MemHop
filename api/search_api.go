// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/query/search"
)

// Search runs the full search pipeline and returns matching contexts.
// It also stores the user's dialogue content to a new depth1 topic created
// for this turn; its ID is exposed as SearchResult.NewTopicID and must be
// passed to Update to append the agent reply.
func (m *MemHop) Search(q search.SearchQuery) (*search.SearchResult, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	if q.DirectedL2ID != nil {
		return search.RunDirectedSearch(q, m.searchDeps(), m.sessionMgr, &m.config.LLM, m.defaults)
	}
	return search.RunSearch(q, m.searchDeps(), m.sessionMgr, &m.config.LLM, m.defaults)
}
