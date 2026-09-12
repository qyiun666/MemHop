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

// MaxEventPayload caps one event record: its name and its body together. An event
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
		// The name is part of the record. Measuring only the body would leave an
		// unbounded text one field away from the budget, which is the same token
		// stream the budget exists to keep out.
		return checkPayload(len(in.EventType)+len(in.Content), MaxEventPayload, "event")
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
	return checkPayload(len(in.Content), MaxUtterancePayload, "utterance")
}

func checkPayload(size, budget int, what string) error {
	if size > budget {
		return common.NewError(common.ErrInvalidQuery,
			fmt.Sprintf("%s of %d bytes exceeds the %d-byte budget", what, size, budget))
	}
	return nil
}

// Append is this package's only write path, and the one every host-side record of
// a turn goes through: it lands one entry on the topic's content track. The slot it
// took is not handed back — it is readable on that topic's own track, and the write
// surface promises no handle.
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
func Append(ac *domain.Context, agentID, topicID uint64, in core.ArchiveSlot) error {
	if err := ValidateAppend(in); err != nil {
		return err
	}
	seq := in.Seq
	if seq == 0 {
		seq = max(ac.L4.MaxSeq(topicID), core.LastUtteranceSeq) + 1
		// Allocating is the library choosing an address, and the number it chose comes
		// from a mirror whose rebuild skips records it cannot decode — so the slot can
		// still be held by one, its ordinal living on in the derived id. Read that
		// address before taking it. A named Seq is unchecked on purpose: overwriting a
		// slot the caller points at is this write path's replay contract.
		if _, err := core.ReadArchiveSlot(ac.Engine, agentID, core.HashContent(topicID, seq)); err != nil && common.CodeOf(err) != common.ErrNotFound {
			return common.NewError(common.CodeOf(err), "read the slot the content mirror offered", err)
		}
	}
	in.TopicID, in.Seq = topicID, seq
	if in.Kind == core.KindEvent {
		in.Role, in.ContentType = 0, core.ContentText
	}
	return repo.AppendArchiveL4(ac.Engine, agentID, ac.L4, &in)
}

// Read loads one topic's content of one kind, Seq ascending, through the domain's
// content mirror. The mirror is the only list of what a topic owns, so a record it
// names but the disk cannot produce is an error rather than a shorter track.
func Read(agentID uint64, ac *domain.Context, topicID uint64, kind core.ArchiveKind) ([]core.ArchiveSlot, error) {
	return repo.ReadArchivesByIDs(ac.Engine, agentID, ac.L4.IDs(topicID, kind))
}

// RenderForDistill turns a topic's utterances into the one text a keyword call
// reads: Seq order, each entry labelled with its speaker. A record's own newlines
// are written out as they came in, so an entry can span several lines.
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
