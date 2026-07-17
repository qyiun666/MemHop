// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// LRU adjacency cache for L3 hypergraph BFS.

package index

import (
	"container/list"
	"sync"

	"memhop/internal/core/model"
)

// cacheEntry holds the adjacency data and its position in the LRU list.
type cacheEntry struct {
	adjacency map[uint64][]model.AdjacencyEntry
	elem      *list.Element
}

// AdjacencyCache is an LRU cache for graph adjacency indexes.
// Each graph ID maps to its full adjacency table.
type AdjacencyCache struct {
	maxSize int
	cache   map[uint64]*cacheEntry
	order   list.List
	mu      sync.RWMutex
}

// NewAdjacencyCache creates a cache with the given max entries.
func NewAdjacencyCache(maxEntries int) *AdjacencyCache {
	if maxEntries <= 0 {
		maxEntries = 128
	}
	return &AdjacencyCache{
		maxSize: maxEntries,
		cache:   make(map[uint64]*cacheEntry, maxEntries),
	}
}

// Get retrieves a cached adjacency for a graph. Returns (nil, false) on miss.
func (c *AdjacencyCache) Get(graphID uint64) (map[uint64][]model.AdjacencyEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[graphID]
	if !ok {
		return nil, false
	}
	c.order.MoveToBack(e.elem)
	return e.adjacency, true
}

// Put stores an adjacency in the cache, evicting LRU if full.
func (c *AdjacencyCache) Put(graphID uint64, adjacency map[uint64][]model.AdjacencyEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, exists := c.cache[graphID]; exists {
		e.adjacency = adjacency
		c.order.MoveToBack(e.elem)
		return
	}
	if len(c.cache) >= c.maxSize {
		c.evictLRU()
	}
	elem := c.order.PushBack(graphID)
	c.cache[graphID] = &cacheEntry{
		adjacency: adjacency,
		elem:      elem,
	}
}

// Invalidate removes the cached adjacency for a specific graph.
func (c *AdjacencyCache) Invalidate(graphID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.cache[graphID]; ok {
		c.order.Remove(e.elem)
		delete(c.cache, graphID)
	}
}

// InvalidateAll clears the entire cache.
func (c *AdjacencyCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[uint64]*cacheEntry, c.maxSize)
	c.order.Init()
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
	front := c.order.Front()
	if front == nil {
		return
	}
	graphID := front.Value.(uint64)
	c.order.Remove(front)
	delete(c.cache, graphID)
}
