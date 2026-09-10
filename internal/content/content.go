// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package content holds the small methods over a topic's L4 content: the key
// every one of them is addressed by, the event write contract, appending one
// event, reading a topic's event track back, and trimming a read to an LLM
// payload budget.
//
// It is named for what it serves rather than for a layer: since a turn's
// dialogue originals and its operation events are the same records differing
// only in Kind, and a plan tree hangs off that same key, the work here is
// content and keys, not trajectories. Crystallize is a pure read — the engine
// stores no capability records — so this package has one write step: an event.
//
// The big methods (AppendTrajectory, ReadTrajectory, PlanCommit, PlanState,
// Crystallize) stay in the composition root with the domain lock.
package content

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ParseTopicID parses the one key a turn's content and its plan tree share — the
// topic id Search issued for the turn — and rejects 0. Zero is the unset value of
// every record's owning id, so admitting it would let a caller address the
// unkeyed residue of a domain.
func ParseTopicID(topicID string) (uint64, error) {
	h, err := common.ParseID(topicID)
	if err != nil {
		return 0, common.NewError(common.ErrInvalidQuery, "parse topic id", err)
	}
	if h == 0 {
		return 0, common.NewError(common.ErrInvalidQuery, "topic id 0000000000000000 is reserved")
	}
	return h, nil
}

// MaxCrystallizePayload caps the event payload bytes fed to one crystallize LLM
// call; over-budget events drop from the oldest.
const MaxCrystallizePayload = 128 * 1024

// MaxEventPayload caps a single event payload (no raw token streams). An event
// over the budget is refused: the payload is the host's own record of what
// happened, and silently shortening it would leave a truncated event that reads
// exactly like a complete one.
const MaxEventPayload = 4 * 1024

// ValidateEvent checks what every event write path requires of an event, before
// any record or plan node is touched. Only the fields a host owns are looked at:
// Kind, Seq and the owning topic are the library's to assign.
func ValidateEvent(ev core.ArchiveSlot) error {
	if ev.EventType == "" || ev.CreatedAt <= 0 {
		return common.NewError(common.ErrInvalidQuery, "EventType and Timestamp are required")
	}
	if len(ev.Content) > MaxEventPayload {
		return common.NewError(common.ErrInvalidQuery,
			fmt.Sprintf("payload of %d bytes exceeds the %d-byte event budget", len(ev.Content), MaxEventPayload))
	}
	return nil
}

// AppendEvent writes one event into the topic's content track and returns the Seq
// it landed on. nodePath names the step the event belongs to — the path goes on
// the record, which is how a read attributes an event to a step afterwards; empty
// means a bare turn event. An event names itself and carries nothing else: the
// fields a host cannot be trusted with are assigned here, so the event path
// cannot slip an utterance-shaped record into the transcript.
//
// Seq is allocated above every slot the topic already holds, including the two
// utterance slots: a host records events while the turn runs and settles the turn
// afterwards, and the originals must still land on Seq 1 and 2.
func AppendEvent(ac *domain.Context, agentID, topicID uint64, nodePath string, ev core.ArchiveSlot) (uint64, error) {
	if err := ValidateEvent(ev); err != nil {
		return 0, err
	}
	seq := max(ac.L4.MaxSeq(topicID), core.LastUtteranceSeq) + 1
	if _, err := repo.AppendArchiveL4(ac.Engine, agentID, ac.L4, repo.ArchiveContent{
		TopicID: topicID, Seq: seq, Kind: core.KindEvent, Type: core.ContentText,
		EventType: ev.EventType, NodePath: nodePath, Text: ev.Content, CreatedAt: ev.CreatedAt,
	}); err != nil {
		return 0, err
	}
	return seq, nil
}

// ReadEvents loads one topic's events (Seq ascending) through the domain's
// content mirror. A record the index names but the engine cannot read is an
// error, not a shorter log: a silently missing event is indistinguishable from
// one that was never written.
func ReadEvents(engine *core.StorageEngine, agentID uint64, ac *domain.Context, topicID uint64) ([]core.ArchiveSlot, error) {
	hashes := ac.L4.IDs(topicID, core.KindEvent)
	out := make([]core.ArchiveSlot, 0, len(hashes))
	for _, h := range hashes {
		ev, err := core.ReadArchiveSlot(engine, agentID, h)
		if err != nil {
			if common.CodeOf(err) == common.ErrNotFound {
				return nil, common.NewError(common.ErrIO, "content index names a missing record", err)
			}
			return nil, err
		}
		out = append(out, *ev)
	}
	return out, nil
}

// TrimByBudget keeps the newest events within budget payload bytes (at least
// one). ponytail: dropping the oldest is lossy for very
// long turns; map-reduce induction over chunks is the upgrade path.
func TrimByBudget(events []core.ArchiveSlot, budget int) []core.ArchiveSlot {
	total := 0
	start := len(events)
	for start > 0 {
		p := len(events[start-1].Content)
		if total+p > budget {
			break
		}
		total += p
		start--
	}
	if start == len(events) && len(events) > 0 {
		start = len(events) - 1
	}
	return events[start:]
}
