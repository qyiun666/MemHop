// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package session

import (
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common/config"
)

func TestNewSessionManager(t *testing.T) {
	t.Run("with config", func(t *testing.T) {
		cfg := &config.SessionConfig{DefaultTTLMs: 5000, Capacity: 3}
		sm := NewSessionManager(cfg)
		if sm == nil {
			t.Fatal("NewSessionManager returned nil")
		}
		if sm.Len() != 0 {
			t.Errorf("expected 0 topics, got %d", sm.Len())
		}
		if sm.maxActive != 3 {
			t.Errorf("maxActive = %d; want 3", sm.maxActive)
		}
		if sm.defaultTTLMs != 5000 {
			t.Errorf("defaultTTLMs = %d; want 5000", sm.defaultTTLMs)
		}
	})

	t.Run("with nil config uses defaults", func(t *testing.T) {
		sm := NewSessionManager(nil)
		if sm.defaultTTLMs != 3600000 {
			t.Errorf("defaultTTLMs = %d; want 3600000", sm.defaultTTLMs)
		}
		if sm.maxActive != 7 {
			t.Errorf("maxActive = %d; want 7", sm.maxActive)
		}
	})

	t.Run("with zero values uses defaults", func(t *testing.T) {
		cfg := &config.SessionConfig{DefaultTTLMs: 0, Capacity: 0}
		sm := NewSessionManager(cfg)
		if sm.defaultTTLMs != 3600000 {
			t.Errorf("defaultTTLMs = %d; want 3600000", sm.defaultTTLMs)
		}
		if sm.maxActive != 7 {
			t.Errorf("maxActive = %d; want 7", sm.maxActive)
		}
	})
}

func TestTouchAndLen(t *testing.T) {
	sm := NewSessionManager(&config.SessionConfig{DefaultTTLMs: 60000, Capacity: 10})

	sm.Touch(1, 60000)
	if sm.Len() != 1 {
		t.Errorf("Len = %d; want 1", sm.Len())
	}

	sm.Touch(2, 60000)
	if sm.Len() != 2 {
		t.Errorf("Len = %d; want 2", sm.Len())
	}

	// Touch same topic again should not increase count
	sm.Touch(1, 60000)
	if sm.Len() != 2 {
		t.Errorf("Len = %d; want 2 (no duplicate)", sm.Len())
	}
}

func TestTouchWithZeroTTLUsesDefault(t *testing.T) {
	sm := NewSessionManager(&config.SessionConfig{DefaultTTLMs: 30000, Capacity: 5})

	sm.Touch(1, 0) // should use default
	if sm.Len() != 1 {
		t.Errorf("Len = %d; want 1", sm.Len())
	}
	act, ok := sm.activeTopics[1]
	if !ok {
		t.Fatal("topic 1 should exist")
	}
	if act.TTLMs != 30000 {
		t.Errorf("TTLMs = %d; want 30000 (default)", act.TTLMs)
	}
}

func TestTouchWithNegativeTTLUsesDefault(t *testing.T) {
	sm := NewSessionManager(&config.SessionConfig{DefaultTTLMs: 30000, Capacity: 5})

	sm.Touch(1, -100) // should use default
	if sm.Len() != 1 {
		t.Errorf("Len = %d; want 1", sm.Len())
	}
	act, ok := sm.activeTopics[1]
	if !ok {
		t.Fatal("topic 1 should exist")
	}
	if act.TTLMs != 30000 {
		t.Errorf("TTLMs = %d; want 30000 (default)", act.TTLMs)
	}
}

func TestGetActiveTopicIDs(t *testing.T) {
	// Use a long TTL to ensure topics are still active during the test
	sm := NewSessionManager(&config.SessionConfig{DefaultTTLMs: 60000, Capacity: 10})
	sm.Touch(10, 60000)
	sm.Touch(20, 60000)
	sm.Touch(30, 60000)

	ids := sm.GetActiveTopicIDs()
	if len(ids) != 3 {
		t.Errorf("got %d active topics; want 3", len(ids))
	}

	// All IDs should be present
	seen := make(map[uint64]bool)
	for _, id := range ids {
		seen[id] = true
	}
	for _, want := range []uint64{10, 20, 30} {
		if !seen[want] {
			t.Errorf("active topic %d not found in results", want)
		}
	}
}

func TestGetActiveTopicIDsEmpty(t *testing.T) {
	sm := NewSessionManager(nil)
	ids := sm.GetActiveTopicIDs()
	if len(ids) != 0 {
		t.Errorf("expected empty, got %d topics", len(ids))
	}
}

func TestMostRecentTopic(t *testing.T) {
	sm := NewSessionManager(&config.SessionConfig{DefaultTTLMs: 60000, Capacity: 10})
	sm.Touch(100, 60000)
	time.Sleep(time.Millisecond)
	sm.Touch(200, 60000)
	time.Sleep(time.Millisecond)
	sm.Touch(300, 60000)

	recent := sm.MostRecentTopic()
	if recent == nil {
		t.Fatal("MostRecentTopic returned nil")
	}
	if *recent != 300 {
		t.Errorf("MostRecentTopic = %d; want 300", *recent)
	}
}

func TestMostRecentTopicEmpty(t *testing.T) {
	sm := NewSessionManager(nil)
	recent := sm.MostRecentTopic()
	if recent != nil {
		t.Errorf("expected nil, got %d", *recent)
	}
}

func TestCapacityEviction(t *testing.T) {
	sm := NewSessionManager(&config.SessionConfig{DefaultTTLMs: 60000, Capacity: 3})

	sm.Touch(1, 60000)
	time.Sleep(time.Millisecond)
	sm.Touch(2, 60000)
	time.Sleep(time.Millisecond)
	sm.Touch(3, 60000)

	if sm.Len() != 3 {
		t.Errorf("Len = %d; want 3", sm.Len())
	}

	// Adding 4th should evict oldest (1)
	sm.Touch(4, 60000)
	if sm.Len() != 3 {
		t.Errorf("after eviction Len = %d; want 3", sm.Len())
	}

	// Topic 1 should be evicted (oldest timestamp)
	if _, ok := sm.activeTopics[1]; ok {
		t.Error("topic 1 should have been evicted")
	}
}

func TestReTouchUpdatesTime(t *testing.T) {
	sm := NewSessionManager(&config.SessionConfig{DefaultTTLMs: 60000, Capacity: 5})

	sm.Touch(1, 60000)
	firstAct := sm.activeTopics[1]
	firstTime := firstAct.LastHitAt

	// Touch again to update timestamp
	time.Sleep(time.Millisecond)
	sm.Touch(1, 60000)
	secondAct := sm.activeTopics[1]
	secondTime := secondAct.LastHitAt

	if secondTime < firstTime {
		t.Error("re-touch should update LastHitAt to a later time")
	}
}

func TestSessionConfigEdgeCases(t *testing.T) {
	t.Run("huge capacity", func(t *testing.T) {
		cfg := &config.SessionConfig{DefaultTTLMs: 1000, Capacity: 1000}
		sm := NewSessionManager(cfg)
		for i := 0; i < 100; i++ {
			sm.Touch(uint64(i), 1000)
		}
		if sm.Len() != 100 {
			t.Errorf("Len = %d; want 100", sm.Len())
		}
	})

	t.Run("single topic capacity", func(t *testing.T) {
		cfg := &config.SessionConfig{DefaultTTLMs: 1000, Capacity: 1}
		sm := NewSessionManager(cfg)
		sm.Touch(1, 1000)
		sm.Touch(2, 1000)
		if sm.Len() != 1 {
			t.Errorf("Len = %d; want 1", sm.Len())
		}
	})
}
