// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package content holds the small methods over a topic's L4 content: the key
// every one of them is addressed by, the write contract, appending one record,
// reading a topic's two tracks back, rendering a transcript for distillation,
// and the per-record payload budgets.
//
// It is named for what it serves rather than for a layer: a turn's dialogue
// originals and its operation events are the same records differing only in
// Kind, so one write path serves both.
package content

import (
	"fmt"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ParseTopicID parses the one key a turn's records are addressed by and rejects
// 0. Zero is the unset value of every record's owning id, so admitting it would
// let a caller address the unkeyed residue of a domain.
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

// MaxEventPayload caps a single event payload (no raw token streams). An event
// over the budget is refused rather than shortened: a truncated event reads
// exactly like a complete one.
const MaxEventPayload = 4 * 1024

// MaxUtterancePayload caps a single dialogue original. The budget is what keeps
// one append from turning into an unbounded number of LLM round-trips held inside
// the domain lock: the calls a text costs grow with its length, so an unbounded
// text is an unbounded lock hold.
const MaxUtterancePayload = 64 * 1024

// ValidateAppend checks what every content write path requires of a record,
// before any record or plan node is touched. Only the axes are looked at: the
// owning topic and the derived id are the library's to assign, and Seq 0 means
// "allocate" rather than being a value to validate.
//
// The two kinds own the three axes differently, and the rules below are what keep
// Kind, Role, ContentType and EventType from disagreeing about one record:
// an event names itself and carries no speaker; an utterance has a speaker and a
// medium and no event name.
func ValidateAppend(in core.ArchiveSlot) error {
	if !in.Kind.Valid() {
		return common.NewError(common.ErrInvalidQuery, "undefined content kind")
	}
	if in.Content == "" {
		return common.NewError(common.ErrInvalidQuery, "content is required")
	}
	if in.CreatedAt <= 0 {
		return common.NewError(common.ErrInvalidQuery, "a positive timestamp is required")
	}
	if !in.ContentType.Valid() {
		return common.NewError(common.ErrInvalidQuery, "undefined content type")
	}
	if in.Kind == core.KindEvent {
		if in.EventType == "" {
			return common.NewError(common.ErrInvalidQuery, "an event requires EventType")
		}
		return checkPayload(in.Content, MaxEventPayload, "event")
	}
	if in.EventType != "" {
		return common.NewError(common.ErrInvalidQuery, "an utterance carries no EventType")
	}
	if in.NodeSeq != 0 {
		return common.NewError(common.ErrInvalidQuery, "an utterance hangs on no plan node")
	}
	switch in.Role {
	case core.RoleUser, core.RoleAgent, core.RoleSystem:
	default:
		return common.NewError(common.ErrInvalidQuery,
			"an utterance speaks as user, agent or system")
	}
	return checkPayload(in.Content, MaxUtterancePayload, "utterance")
}

func checkPayload(content string, budget int, what string) error {
	if len(content) > budget {
		return common.NewError(common.ErrInvalidQuery,
			fmt.Sprintf("%s content of %d bytes exceeds the %d-byte budget", what, len(content), budget))
	}
	return nil
}

// Append is the only path that writes a record into a topic: it lands one entry
// on the topic's content track and returns the Seq it took. NodeSeq lives on the
// record: a non-zero one names the plan step an event belongs to, which is how a
// read attributes an event to a step afterwards.
//
// Field ownership is the contract. Of the record a caller passes, the ones the
// utterance kind owns are adopted verbatim (Role, ContentType, EventType,
// Content, CreatedAt) and the rest are assigned here — Kind is what the caller
// chose to validate against, IDHash follows from (topic, Seq), and an event
// leaves Role 0 and ContentType text because a thing that happened has no
// speaker and no medium. So neither kind can forge the other's shape.
//
// Seq 0 allocates a slot above every one the topic already holds, including the
// two reserved for dialogue: a caller records events while the turn runs and
// appends the originals afterwards, and the originals must still land on Seq 1
// and 2. A non-zero Seq writes that slot, and taking a slot that is already held
// is an overwrite, not an error — that is what lets a replayed turn converge
// instead of accumulating versions, and it reaches across Kind: naming a slot an
// event holds replaces the event.
func Append(ac *domain.Context, agentID, topicID uint64, in core.ArchiveSlot) (uint64, error) {
	if err := ValidateAppend(in); err != nil {
		return 0, err
	}
	seq := in.Seq
	if seq == 0 {
		seq = max(ac.L4.MaxSeq(topicID), core.LastUtteranceSeq) + 1
	}
	slot := repo.ArchiveContent{
		TopicID: topicID, Seq: seq, Kind: in.Kind,
		Type: core.ContentText, EventType: in.EventType, NodeSeq: in.NodeSeq,
		Text: in.Content, CreatedAt: in.CreatedAt,
	}
	if in.Kind == core.KindUtterance {
		slot.Type, slot.Role = in.ContentType, in.Role
	}
	if _, err := repo.AppendArchiveL4(ac.Engine, agentID, ac.L4, slot); err != nil {
		return 0, err
	}
	return seq, nil
}

// Read loads one topic's content of one kind, Seq ascending, through the domain's
// content mirror. A record the index names but the engine cannot read is an
// error, not a shorter track: a silently missing record is indistinguishable from
// one that was never written, and a transcript missing a line looks exactly like
// a turn that had one fewer line.
func Read(engine *core.StorageEngine, agentID uint64, ac *domain.Context, topicID uint64, kind core.ArchiveKind) ([]core.ArchiveSlot, error) {
	hashes := ac.L4.IDs(topicID, kind)
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

// RenderForDistill turns a topic's utterances into the one text a keyword call
// reads: Seq order, one "<speaker>: <content>" line each.
//
// Seq order is the order the topic reads back in, so the text that produced a
// topic's keywords is the text its transcript reads back as. The speaker labels
// are not decoration: without them the sides of an exchange collapse into one
// undifferentiated text and the extraction loses who asserted what.
func RenderForDistill(utterances []core.ArchiveSlot) string {
	var b strings.Builder
	for i, u := range utterances {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(speaker(u.Role))
		b.WriteString(": ")
		b.WriteString(u.Content)
	}
	return b.String()
}

func speaker(role uint8) string {
	switch role {
	case core.RoleAgent:
		return "Assistant"
	case core.RoleSystem:
		return "System"
	default:
		return "User"
	}
}
