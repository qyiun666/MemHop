// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"cmp"
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// L4 content operations. A topic's content is everything that filled that turn:
// its dialogue originals and its operation events, told apart by Kind.
// AppendArchiveL4 stores one slot under the id its
// (topic, Seq) hashes and mirrors it into the domain's L4Index, which is how a
// topic's records are enumerated again. QueryArchivesL4 reads by that key or by
// any AND-ed combination of filters.

// AppendArchiveL4 writes one content slot. The caller owns every field of the
// record except the id: this is where (topic, Seq) becomes
// core.HashContent(TopicID, Seq), because the id is positional rather than
// content-derived — writing the same (topic, Seq) again re-points the same record
// instead of leaving a second live copy behind, which is what lets a replayed turn
// converge with no list of what it supersedes. Nothing reports an overwrite as
// distinct from a first write, and no handle comes back: the address is the
// (topic, Seq) the caller already holds.
func AppendArchiveL4(engine *core.StorageEngine, agentID uint64, idx *index.L4Index, arc *core.ArchiveSlot) error {
	arc.IDHash = core.HashContent(arc.TopicID, arc.Seq)
	if err := core.WriteArchiveSlot(engine, agentID, arc.IDHash, arc); err != nil {
		return err
	}
	idx.Append(arc.TopicID, arc.Seq, arc.IDHash, arc.Kind, arc.CreatedAt)
	return nil
}

// DeleteTopicArchives tombstones every content record the index credits the
// given topics with, both kinds, then drops those topics from the index. A topic
// with nothing indexed costs no write, and a topic being deleted never has to
// hand over the ids it owned — the mirror is the list. The records go first, so
// a failed pass cannot leave an entry naming a record the topic still holds.
func DeleteTopicArchives(engine *core.StorageEngine, agentID uint64, idx *index.L4Index, topics []uint64) error {
	var doomed []uint64
	for _, topicID := range topics {
		doomed = append(doomed, idx.AllIDs(topicID)...)
	}
	if len(doomed) > 0 {
		if _, err := engine.DeleteRecordBatch(agentID, common.DedupSorted(doomed)); err != nil {
			return common.NewError(common.ErrIO, "delete l4 content", err)
		}
	}
	for _, topicID := range topics {
		idx.RemoveTopic(topicID)
	}
	return nil
}

// DropExpiredArchives tombstones every content record the index reports as
// created before cutoff and mirrors the removal, returning how many went away.
// ExpiredBefore reads without mutating, so a failed delete leaves the mirror
// naming records that are still live — the recoverable direction — rather than
// dropping entries for records no sweep will visit again.
func DropExpiredArchives(engine *core.StorageEngine, agentID uint64, idx *index.L4Index, cutoff int64) (int, error) {
	expired := idx.ExpiredBefore(cutoff)
	var all []uint64
	for _, ids := range expired {
		all = append(all, ids...)
	}
	if len(all) == 0 {
		return 0, nil
	}
	n, err := engine.DeleteRecordBatch(agentID, all)
	if err != nil {
		return 0, common.NewError(common.ErrIO, "drop expired content", err)
	}
	for topicID, ids := range expired {
		idx.RemoveIDs(topicID, ids)
	}
	return n, nil
}

// ArchiveQuery is the L4 read filter: every field is optional and the set
// conditions AND together, so an empty query selects the domain's whole content
// set — utterances AND events alike. Kind narrows to one of the two and is a
// condition like any other, not a mode switch. Keyword is matched
// case-insensitively against a lower-cased query keyword (the L3 node filter
// matches the same way).
//
// Index lets a topic-scoped read go through the domain's content cache instead
// of scanning the whole bucket; it only applies when TopicID is set.
// NodeSeqs filters on the record's attribution field — ordinals inside one
// turn, so the caller is expected to have set TopicID alongside it. It holds the
// whole subtree already: a step's own ordinal plus every ordinal nested under it,
// which the caller walks over the plan tree's parent links. Once a step is split
// into sub-steps the work it did lives on the children, and "what did this step
// do" that answers only for the parent's own line is a partial answer.
type ArchiveQuery struct {
	IDs      []uint64
	TopicID  *uint64
	Type     *core.ContentType
	Kind     *core.ArchiveKind
	NodeSeqs []uint32
	Keyword  string
	Start    int64
	End      int64
	Limit    int
	Index    *index.L4Index
}

// QueryArchivesL4 returns the content records matching every set condition. An
// ID that names no record is skipped (a tombstoned or foreign id simply selects
// nothing); a record that cannot be read is an error. A lookup that only names
// IDs takes the record-read fast path instead of scanning the domain. A
// topic-scoped lookup with an index reads exactly that topic's records, where an
// id the index names but the engine cannot read is an error rather than a shorter
// transcript. The order is the one the query width has: one topic comes back by
// Seq, anything wider by the record's own timestamp — and Limit keeps the last
// entries of that order, which is the newest content only because the order is
// oldest first. See compareArchives for why the two differ.
func QueryArchivesL4(engine *core.StorageEngine, agentID uint64, q ArchiveQuery) ([]core.ArchiveSlot, error) {
	q.Keyword = strings.ToLower(q.Keyword)
	var out []core.ArchiveSlot
	var err error
	switch {
	case q.TopicID != nil && q.Index != nil:
		out, err = ReadArchivesByIDs(engine, agentID, q.Index.AllIDs(*q.TopicID))
	case len(q.IDs) > 0 && q.TopicID == nil && q.Type == nil && q.Kind == nil &&
		len(q.NodeSeqs) == 0 && q.Keyword == "" && q.Start == 0 && q.End == 0:
		out, err = archivesByIDOnly(engine, agentID, q.IDs)
	default:
		// The domain-wide scan is a read, not a cache rebuild: one record the
		// index names but the payload will not decode would otherwise leave the
		// caller holding a shorter transcript that reads exactly like a whole one.
		out, err = core.CollectAllStrict[core.ArchiveSlot](engine, agentID, core.RecL4Archive)
	}
	if err != nil {
		return nil, err
	}
	filtered := out[:0]
	for _, arc := range out {
		if matchesArchiveQuery(arc, q) {
			filtered = append(filtered, arc)
		}
	}
	slices.SortFunc(filtered, compareArchives(q.TopicID != nil))
	return newest(filtered, q.Limit), nil
}

// compareArchives picks the order a read hands back, and the two cases are not
// the same order. Inside one topic, Seq is that order: the slot the writer
// allocated, unique within the topic, and what a turn's transcript is rendered
// in. Across topics a Seq means nothing — every topic numbers its slots from 1 —
// so a domain-wide read sorted by it interleaves turns by slot number and
// keeping the tail of that order retains the longest turn, not the newest
// content. There the ordering is when the record was said, and its id breaks a
// tie so one query answers in the same order every time.
func compareArchives(oneTopic bool) func(a, b core.ArchiveSlot) int {
	if oneTopic {
		return func(a, b core.ArchiveSlot) int { return cmp.Compare(a.Seq, b.Seq) }
	}
	return func(a, b core.ArchiveSlot) int {
		if c := cmp.Compare(a.CreatedAt, b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.IDHash, b.IDHash)
	}
}

// newest keeps the last limit entries of an ascending result.
func newest(out []core.ArchiveSlot, limit int) []core.ArchiveSlot {
	if limit <= 0 || len(out) <= limit {
		return out
	}
	return out[len(out)-limit:]
}

func archivesByIDOnly(engine *core.StorageEngine, agentID uint64, ids []uint64) ([]core.ArchiveSlot, error) {
	var out []core.ArchiveSlot
	for _, idHash := range ids {
		arc, err := core.ReadArchiveSlot(engine, agentID, idHash)
		if err != nil {
			if common.CodeOf(err) == common.ErrNotFound {
				continue
			}
			return nil, err
		}
		out = append(out, *arc)
	}
	return out, nil
}

// ReadArchivesByIDs loads the records one content read selected. A record that is
// gone is an error, not a skip: the ids come from the domain's mirror, so an entry
// naming nothing means the mirror and the disk disagree, and a transcript silently
// missing one utterance reads exactly like a complete one. A slot the retention
// window reclaimed is not this case — it is absent from the index too, and shows up
// as a gap in the Seq the read reports.
func ReadArchivesByIDs(engine *core.StorageEngine, agentID uint64, ids []uint64) ([]core.ArchiveSlot, error) {
	out := make([]core.ArchiveSlot, 0, len(ids))
	for _, idHash := range ids {
		arc, err := core.ReadArchiveSlot(engine, agentID, idHash)
		if err != nil {
			if common.CodeOf(err) == common.ErrNotFound {
				return nil, common.NewError(common.ErrIO, "content index names a missing record", err)
			}
			return nil, err
		}
		out = append(out, *arc)
	}
	return out, nil
}

func matchesArchiveQuery(arc core.ArchiveSlot, q ArchiveQuery) bool {
	if len(q.IDs) > 0 && !slices.Contains(q.IDs, arc.IDHash) {
		return false
	}
	if q.TopicID != nil && arc.TopicID != *q.TopicID {
		return false
	}
	if q.Kind != nil && arc.Kind != *q.Kind {
		return false
	}
	if len(q.NodeSeqs) > 0 && !slices.Contains(q.NodeSeqs, arc.NodeSeq) {
		return false
	}
	if q.Type != nil && arc.ContentType != *q.Type {
		return false
	}
	if q.Keyword != "" && !strings.Contains(strings.ToLower(arc.Content), q.Keyword) {
		return false
	}
	if q.Start > 0 && arc.CreatedAt < q.Start {
		return false
	}
	if q.End > 0 && arc.CreatedAt > q.End {
		return false
	}
	return true
}
