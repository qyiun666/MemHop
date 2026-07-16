// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package session implements topic activation tracking for MemHop sessions.
package session

import (
	"sync"
	"time"

	"github.com/qyiun666/memhop/memhop/internal/core"
)

// TopicActivation tracks a single topic's activation state.
type TopicActivation struct {
	TopicID   uint64
	LastHitAt int64
	TTLMs     int64
}

// SessionManager tracks active topics within a user session.
type SessionManager struct {
	activeTopics map[uint64]*TopicActivation
	maxActive    int
	defaultTTLMs int64
	mu           sync.RWMutex
}

// NewSessionManager creates a SessionManager from config.
func NewSessionManager(config *core.SessionConfig) *SessionManager {
	ttl := int64(3600000) // 1 hour default
	capacity := 7
	if config != nil {
		if config.DefaultTTLMs > 0 {
			ttl = config.DefaultTTLMs
		}
		if config.Capacity > 0 {
			capacity = config.Capacity
		}
	}
	return &SessionManager{
		activeTopics: make(map[uint64]*TopicActivation),
		maxActive:    capacity,
		defaultTTLMs: ttl,
	}
}

// GetActiveTopicIDs returns all non-expired active topic IDs.
func (sm *SessionManager) GetActiveTopicIDs() []uint64 {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	nowMs := time.Now().UnixMilli()
	ids := make([]uint64, 0, len(sm.activeTopics))
	for _, act := range sm.activeTopics {
		if nowMs-act.LastHitAt < act.TTLMs {
			ids = append(ids, act.TopicID)
		}
	}
	return ids
}

// MostRecentTopic returns the most recently activated topic, or nil.
func (sm *SessionManager) MostRecentTopic() *uint64 {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	nowMs := time.Now().UnixMilli()
	var bestID uint64
	var bestHit int64 = -1
	for _, act := range sm.activeTopics {
		if nowMs-act.LastHitAt < act.TTLMs && act.LastHitAt > bestHit {
			bestID = act.TopicID
			bestHit = act.LastHitAt
		}
	}
	if bestHit < 0 {
		return nil
	}
	return &bestID
}

// Touch activates a topic with the given TTL in milliseconds.
func (sm *SessionManager) Touch(topicID uint64, ttlMs int64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if ttlMs <= 0 {
		ttlMs = sm.defaultTTLMs
	}
	sm.activeTopics[topicID] = &TopicActivation{
		TopicID:   topicID,
		LastHitAt: time.Now().UnixMilli(),
		TTLMs:     ttlMs,
	}
	// Evict oldest if over capacity.
	if len(sm.activeTopics) > sm.maxActive {
		sm.evictOldest()
	}
}

// Len returns the number of tracked topics.
func (sm *SessionManager) Len() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.activeTopics)
}

// evictOldest removes the topic with the oldest LastHitAt. Must hold write lock.
func (sm *SessionManager) evictOldest() {
	var oldestID uint64
	var oldestTime int64 = 1<<62 - 1
	for id, act := range sm.activeTopics {
		if act.LastHitAt < oldestTime {
			oldestTime = act.LastHitAt
			oldestID = id
		}
	}
	delete(sm.activeTopics, oldestID)
}
