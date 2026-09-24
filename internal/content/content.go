// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package content holds the small methods over a topic's L4 content: the key every
// one of them is addressed by, the write contract, appending one record, reading a
// topic's two tracks back, rendering a transcript for distillation, and the
// per-record payload budgets. A turn's dialogue originals and its operation events
// are the same records differing only in Kind, so one write path serves both.
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
// 0 — the unset value of every record's owning id.
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

// MaxUtterancePayload caps a single dialogue original. The budget bounds the LLM
// round-trips one text costs inside the domain lock, which grow with its length.
const MaxUtterancePayload = 64 * 1024

// ValidateAppend checks what every content write path requires of a record, before
// any record or plan node is touched. Only the axes are looked at: the owning topic
// and the derived id are the library's to assign, and Seq 0 means "allocate".
//
// The two kinds own the axes differently: an event names itself and carries no
// speaker; an utterance has a speaker and a medium and no event name.
func ValidateAppend(in core.ArchiveSlot) error {
	if !in.Kind.Valid() {
		return common.NewError(common.ErrInvalidQuery, "undefined content kind")
	}
	if in.Content == "" {
		return common.NewError(common.ErrInvalidQuery, "content is required")
	}
	if err := checkTimestamp(in.CreatedAt); err != nil {
		return err
	}
	if !in.ContentType.Valid() {
		return common.NewError(common.ErrInvalidQuery, "undefined content type")
	}
	if in.Kind == core.KindEvent {
		if in.EventType == "" {
			return common.NewError(common.ErrInvalidQuery, "an event requires EventType")
		}
		// The event name is part of the record: measuring only the body leaves an
		// unbounded text one field away from the budget.
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
		// Two ways to get here, and they want different answers: a number the
		// vocabulary never heard of, or a defined role that is not a host's to claim.
		if !in.Role.Valid() {
			return common.NewError(common.ErrInvalidQuery, "undefined content role")
		}
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

// A record's timestamp is milliseconds since the epoch — the retention sweep and
// every L4 time filter compare it against a millisecond cutoff. The two bands below
// are the shapes a host produces by mistake, and each is a silent loss: a
// seconds-scale record is already older than the retention window, so the next Dream
// sweeps the whole turn's transcript, and a microsecond-scale one never expires.
// Anything under the seconds band is left alone — those are relative counters, not a
// wrong unit.
const (
	secondsScaleFloor = 1_000_000_000       // 1e9: 2001-09-09 read as seconds
	secondsScaleCeil  = 100_000_000_000     // 1e11: 5138-11-16 read as seconds
	millisScaleCeil   = 100_000_000_000_000 // 1e14: 5138-11-16 read as milliseconds
)

// wrongScale names the unit a value looks like instead of milliseconds, or "" when the
// value is inside the millisecond band. One judgement, both directions: what a write
// refuses is what a query must refuse too: a bound in another unit is never the window it
// names — as Start, seconds sit below every stamp and select everything, as End they sit
// below every stamp and select nothing.
func wrongScale(v int64) string {
	switch {
	case v >= secondsScaleFloor && v < secondsScaleCeil:
		return "seconds"
	case v > millisScaleCeil:
		return "microseconds"
	}
	return ""
}

// CheckQueryBound validates one of SearchL4's time bounds. Zero means "unset" and passes;
// otherwise the two impossible scales are refused with the same bands the write boundary
// uses, so a wrong-unit bound comes back as an error instead of a result set the host has
// to second-guess.
func CheckQueryBound(field string, v int64) error {
	if v == 0 {
		return nil
	}
	if scale := wrongScale(v); scale != "" {
		return common.NewError(common.ErrInvalidQuery,
			fmt.Sprintf("%s %d is %s, not milliseconds since the epoch: these bounds compare against a record's own millisecond stamp, so a wrong-scale bound answers with everything on one end and nothing on the other, never with the window it names",
				field, v, scale))
	}
	return nil
}

func checkTimestamp(v int64) error {
	if v <= 0 {
		return common.NewError(common.ErrInvalidQuery, "a positive timestamp is required")
	}
	if wrongScale(v) != "" {
		return common.NewError(common.ErrInvalidQuery,
			fmt.Sprintf("created_at %d is not milliseconds since the epoch: a seconds-scale stamp is swept by the retention window as soon as it is written, and a microsecond-scale one never expires", v))
	}
	return nil
}

// Append is this package's only write path: it lands one entry on the topic's
// content track and returns the slot it took. There is no id beyond (topic, Seq) —
// the address is the position — and a replay of a turn rewrites the same slots.
//
// Field ownership: of the record a caller passes, Role, ContentType, EventType,
// Content and CreatedAt are adopted verbatim; Kind is what the caller validated
// against and IDHash follows from (topic, Seq). An event leaves Role 0 and
// ContentType text — a thing that happened has no speaker and no medium.
//
// Seq 0 allocates above every slot the topic already holds, including the two
// reserved for dialogue: events recorded while the turn runs must not push the
// originals off Seq 1 and 2. A non-zero Seq writes that slot, and taking a slot
// already held is an overwrite rather than an error — that is what makes a
// replayed turn converge — and it reaches across Kind: naming a slot an event
// holds replaces the event.
func Append(ac *domain.Context, agentID, topicID uint64, in core.ArchiveSlot) (uint64, error) {
	if err := ValidateAppend(in); err != nil {
		return 0, err
	}
	seq := in.Seq
	if seq == 0 {
		seq = max(ac.L4.MaxSeq(topicID), core.LastUtteranceSeq) + 1
		// The offered slot comes from a mirror whose rebuild skips records it cannot
		// decode, so the slot can still be held by one — its ordinal lives on in the
		// derived id. Read that address before taking it. A named Seq is unchecked on
		// purpose: overwriting the slot a caller points at is this path's replay
		// contract.
		if _, err := core.ReadArchiveSlot(ac.Engine, agentID, core.HashContent(topicID, seq)); err != nil && common.CodeOf(err) != common.ErrNotFound {
			return 0, common.NewError(common.CodeOf(err), "read the slot the content mirror offered", err)
		}
	}
	in.TopicID, in.Seq = topicID, seq
	if in.Kind == core.KindEvent {
		in.Role, in.ContentType = 0, core.ContentText
	}
	if err := repo.AppendArchiveL4(ac.Engine, agentID, ac.L4, &in); err != nil {
		return 0, err
	}
	return seq, nil
}

// Read loads one topic's content of one kind, Seq ascending, through the domain's
// content mirror. The mirror is the only list of what a topic owns, so a record it
// names but the disk cannot produce is an error rather than a shorter track.
func Read(agentID uint64, ac *domain.Context, topicID uint64, kind core.ArchiveKind) ([]core.ArchiveSlot, error) {
	return repo.ReadArchivesByIDs(ac.Engine, agentID, ac.L4.IDs(topicID, kind))
}

// RenderForDistill turns a topic's utterances into the one text a keyword call
// reads: Seq order, each entry labelled with its speaker; a record's own newlines
// are written out as they came in.
//
// Seq order is the order the topic reads back in, so the text that produced a
// topic's keywords is the text its transcript reads back as. The labels are not
// decoration: without them the sides of an exchange collapse and the extraction
// loses who asserted what.
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

// The three roles a host may declare are the whole set this reaches: an update settles
// a turn's own utterances, and a fused group's summary — the one record carrying
// RoleDream — rides a topic that never settles.
func speaker(role core.ArchiveRole) string {
	switch role {
	case core.RoleAgent:
		return "Assistant"
	case core.RoleSystem:
		return "System"
	default:
		return "User"
	}
}
