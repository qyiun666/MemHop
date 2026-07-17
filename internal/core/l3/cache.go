// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// LRU adjacency cache for L3 hypergraph BFS.

package l3

import "sync"

// AdjacencyCache is an LRU cache for graph adjacency indexes.
// Each graph ID maps to its full adjacency table.
type AdjacencyCache struct {
	maxSize int
	cache   map[uint64]map[uint64][]AdjacencyEntry
	order   []uint64 // LRU order: front = oldest
	mu      sync.RWMutex
}

// NewAdjacencyCache creates a cache with the given max entries.
func NewAdjacencyCache(maxEntries int) *AdjacencyCache {
	if maxEntries <= 0 {
		maxEntries = 128
	}
	return &AdjacencyCache{
		maxSize: maxEntries,
		cache:   make(map[uint64]map[uint64][]AdjacencyEntry, maxEntries),
		order:   make([]uint64, 0, maxEntries),
	}
}

// Get retrieves a cached adjacency for a graph. Returns (nil, false) on miss.
func (c *AdjacencyCache) Get(graphID uint64) (map[uint64][]AdjacencyEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	adj, ok := c.cache[graphID]
	if !ok {
		return nil, false
	}
	c.moveToBack(graphID)
	return adj, true
}

// Put stores an adjacency in the cache, evicting LRU if full.
func (c *AdjacencyCache) Put(graphID uint64, adjacency map[uint64][]AdjacencyEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.cache[graphID]; exists {
		c.cache[graphID] = adjacency
		c.moveToBack(graphID)
		return
	}
	if len(c.cache) >= c.maxSize {
		c.evictLRU()
	}
	c.cache[graphID] = adjacency
	c.order = append(c.order, graphID)
}

// Invalidate removes the cached adjacency for a specific graph.
func (c *AdjacencyCache) Invalidate(graphID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, graphID)
	c.removeFromOrder(graphID)
}

// InvalidateAll clears the entire cache.
func (c *AdjacencyCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[uint64]map[uint64][]AdjacencyEntry, c.maxSize)
	c.order = c.order[:0]
}

// Len returns the number of cached entries.
func (c *AdjacencyCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}

// --- internal helpers ---

// evictLRU removes the oldest entry (front of order). Caller must hold lock.
func (c *AdjacencyCache) evictLRU() {
	if len(c.order) == 0 {
		return
	}
	oldest := c.order[0]
	c.order = c.order[1:]
	delete(c.cache, oldest)
}

// moveToBack moves graphID to the back of the LRU order. Caller must hold lock.
func (c *AdjacencyCache) moveToBack(graphID uint64) {
	c.removeFromOrder(graphID)
	c.order = append(c.order, graphID)
}

// removeFromOrder removes graphID from the order slice. Caller must hold lock.
func (c *AdjacencyCache) removeFromOrder(graphID uint64) {
	for i, id := range c.order {
		if id == graphID {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}
