// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// ImportMemory: import data into L0/L2/L3 layers.

package importx

import (
	"encoding/json"
	"strings"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/common/strutil"
	"github.com/qyiun666/MemHop/internal/common/timeutil"
	"github.com/qyiun666/MemHop/internal/core/index"
	"github.com/qyiun666/MemHop/internal/core/model"
	"github.com/qyiun666/MemHop/internal/core/record"
	"github.com/qyiun666/MemHop/internal/core/storage"
	"github.com/qyiun666/MemHop/internal/query/crud"
	"github.com/qyiun666/MemHop/internal/query/write"
)

// ImportMemory imports data into the specified layer.
func ImportMemory(
	engine *storage.StorageEngine,
	sparse *index.SparseIndex,
	l3Idx *index.L3Index,
	l3Deg *index.DegreeTracker,
	l3Cac *index.AdjacencyCache,
	req ImportRequest,
) (*ImportResult, error) {
	switch req.TargetLayer {
	case write.TargetProfile:
		return importL0Profile(engine, req.Data, req.Mode)
	case write.TargetTopic:
		return importL2Topics(engine, sparse, req.Data, req.Mode, req.KnowledgeTitle)
	case write.TargetKnowledge:
		return importL3Knowledge(engine, req.Data, req.Mode, req.KnowledgeTitle, l3Idx, l3Deg, l3Cac)
	default:
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "unknown target layer")
	}
}

// --- L0 Profile Import ---

func importL0Profile(
	engine *storage.StorageEngine,
	data ImportData,
	mode write.ImportMode,
) (*ImportResult, error) {
	if data.Profile == nil {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "profile data required")
	}
	nowMs := timeutil.NowMs()
	profileHash := hash.HashID("profile")
	p := data.Profile

	if engine.Contains(profileHash) {
		return mergeProfile(engine, profileHash, p, mode, nowMs)
	}
	return createProfile(engine, profileHash, p, nowMs)
}

func mergeProfile(
	engine *storage.StorageEngine,
	profileHash uint64,
	p *ProfileImportData,
	mode write.ImportMode,
	nowMs int64,
) (*ImportResult, error) {
	if mode == write.ImportSkip {
		return &ImportResult{
			Status:       write.ImportSuccess,
			SkippedCount: 1,
			Errors:       []write.ImportError{},
			CreatedIDs:   []string{},
			UpdatedIDs:   []string{},
		}, nil
	}
	var profile model.ProfileSlot
	if _, data, err := engine.ReadRecord(profileHash); err != nil {
		return nil, err
	} else if err := json.Unmarshal(data, &profile); err != nil {
		return nil, mherrors.NewError(mherrors.ErrDeserialization, "unmarshal profile", err)
	}
	applyProfileUpdates(&profile, p)
	profile.UpdatedAt = nowMs
	profile.Version++
	if err := writeProfile(engine, profileHash, &profile); err != nil {
		return nil, err
	}
	hexID := hash.FormatHash(profileHash)
	return &ImportResult{
		Status:     write.ImportSuccess,
		UpdatedIDs: []string{hexID},
		Errors:     []write.ImportError{},
		CreatedIDs: []string{},
	}, nil
}

func createProfile(
	engine *storage.StorageEngine,
	profileHash uint64,
	p *ProfileImportData,
	nowMs int64,
) (*ImportResult, error) {
	profile := model.ProfileSlot{
		IDHash:          profileHash,
		Name:            stringOr(p.Name, "Agent"),
		Role:            stringOr(p.Role, "Assistant"),
		Personality:     stringOr(p.Personality, ""),
		Worldview:       stringOr(p.Worldview, ""),
		Preferences:     p.Preferences,
		Lexicon:         p.Lexicon,
		StyleTraits:     p.StyleTraits,
		EmotionPatterns: p.EmotionPatterns,
		CreatedAt:       nowMs,
		UpdatedAt:       nowMs,
		Version:         1,
	}
	if profile.Preferences == nil {
		profile.Preferences = make(map[string]string)
	}
	if profile.Lexicon == nil {
		profile.Lexicon = make(map[string]string)
	}
	if profile.StyleTraits == nil {
		profile.StyleTraits = []string{}
	}
	if profile.EmotionPatterns == nil {
		profile.EmotionPatterns = make(map[string]string)
	}
	if err := writeProfile(engine, profileHash, &profile); err != nil {
		return nil, err
	}
	hexID := hash.FormatHash(profileHash)
	return &ImportResult{
		Status:     write.ImportSuccess,
		ID:         &hexID,
		IDs:        []string{hexID},
		CreatedIDs: []string{hexID},
		UpdatedIDs: []string{},
		Errors:     []write.ImportError{},
		NodeCount:  1,
	}, nil
}

func writeProfile(engine *storage.StorageEngine, idHash uint64, p *model.ProfileSlot) error {
	return record.WriteProfileSlot(engine, idHash, p)
}

func applyProfileUpdates(p *model.ProfileSlot, upd *ProfileImportData) {
	if upd.Name != nil {
		p.Name = *upd.Name
	}
	if upd.Role != nil {
		p.Role = *upd.Role
	}
	if upd.Personality != nil {
		p.Personality = *upd.Personality
	}
	if upd.Worldview != nil {
		p.Worldview = *upd.Worldview
	}
	if upd.Preferences != nil {
		p.Preferences = upd.Preferences
	}
	if upd.Lexicon != nil {
		p.Lexicon = upd.Lexicon
	}
	if upd.StyleTraits != nil {
		p.StyleTraits = upd.StyleTraits
	}
	if upd.EmotionPatterns != nil {
		p.EmotionPatterns = upd.EmotionPatterns
	}
}

// --- L2 Topics Import ---

func importL2Topics(
	engine *storage.StorageEngine,
	sparse *index.SparseIndex,
	data ImportData,
	mode write.ImportMode,
	knowledgeTitle *string,
) (*ImportResult, error) {
	if len(data.Topics) == 0 {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "topics data required")
	}
	nowMs := timeutil.NowMs()
	l3Hash := resolveL3Hash(engine, knowledgeTitle)
	var createdIDs, updatedIDs []string
	var skipped int
	var errors []write.ImportError

	for i, item := range data.Topics {
		err := importOneTopic(engine, sparse, &item, mode, l3Hash, nowMs, &createdIDs, &updatedIDs, &skipped)
		if err != nil {
			errors = append(errors, write.ImportError{Index: i, Message: err.Error()})
		}
	}
	status := write.ImportSuccess
	if len(errors) > 0 {
		status = write.ImportPartialSuccess
	}
	return &ImportResult{
		Status:         status,
		ID:             firstOrNil(createdIDs),
		IDs:            createdIDs,
		CreatedIDs:     createdIDs,
		UpdatedIDs:     updatedIDs,
		SkippedCount:   skipped,
		Errors:         errors,
		KnowledgeTitle: knowledgeTitle,
		NodeCount:      len(createdIDs),
	}, nil
}

func resolveL3Hash(engine *storage.StorageEngine, title *string) uint64 {
	if title == nil {
		return 0
	}
	h := hash.HashID(*title)
	if !engine.Contains(h) {
		return 0
	}
	return h
}

func importOneTopic(
	engine *storage.StorageEngine,
	sparse *index.SparseIndex,
	item *TopicImportItem,
	mode write.ImportMode,
	l3Hash uint64,
	nowMs int64,
	createdIDs, updatedIDs *[]string,
	skipped *int,
) error {
	idHash := hash.HashID(item.Title)
	if engine.Contains(idHash) {
		return handleExistingTopic(engine, sparse, idHash, item, mode, l3Hash, nowMs, updatedIDs, skipped)
	}
	return createNewTopic(engine, sparse, idHash, item, l3Hash, nowMs, createdIDs)
}

func handleExistingTopic(
	engine *storage.StorageEngine,
	sparse *index.SparseIndex,
	idHash uint64,
	item *TopicImportItem,
	mode write.ImportMode,
	l3Hash uint64,
	nowMs int64,
	updatedIDs *[]string,
	skipped *int,
) error {
	if mode == write.ImportSkip {
		*skipped++
		return nil
	}
	var ctx model.TopicSlot
	if _, data, err := engine.ReadRecord(idHash); err != nil {
		return err
	} else if err := json.Unmarshal(data, &ctx); err != nil {
		return err
	}
	ctx.FusedKeywords = []string{item.Title}
	ctx.FusedSummary = item.Summary
	if l3Hash != 0 && !crud.ContainsUint64(ctx.UserL3Refs, l3Hash) && !crud.ContainsUint64(ctx.AgentL3Refs, l3Hash) {
		ctx.AgentL3Refs = append(ctx.AgentL3Refs, l3Hash)
	}
	ctx.UpdatedAt = nowMs
	ctx.Version++
	sparse.RemoveDocument(ctx.ID)
	crud.ReindexTopic(sparse, &ctx)
	crud.WriteTopic(engine, idHash, &ctx)
	*updatedIDs = append(*updatedIDs, hash.FormatHash(idHash))
	return nil
}

func createNewTopic(
	engine *storage.StorageEngine,
	sparse *index.SparseIndex,
	idHash uint64,
	item *TopicImportItem,
	l3Hash uint64,
	nowMs int64,
	createdIDs *[]string,
) error {
	var agentL3Refs []uint64
	if l3Hash != 0 {
		agentL3Refs = []uint64{l3Hash}
	}
	ctx := model.TopicSlot{
		ID:             idHash,
		FusedKeywords:  []string{item.Title},
		FusedSummary:   item.Summary,
		ChildrenIDs:    []uint64{},
		Depth:          1,
		UserKeywords:   []string{},
		UserTimestamp:  nowMs,
		UserL4Refs:     []uint64{},
		UserL3Refs:     []uint64{},
		AgentKeywords:  []string{},
		AgentTimestamp: nowMs,
		AgentL4Refs:    []uint64{},
		AgentL3Refs:    agentL3Refs,
		CreatedAt:      nowMs,
		UpdatedAt:      nowMs,
		Version:        1,
	}
	if err := crud.WriteTopic(engine, idHash, &ctx); err != nil {
		return err
	}
	crud.ReindexTopic(sparse, &ctx)
	*createdIDs = append(*createdIDs, hash.FormatHash(idHash))
	return nil
}

// --- L3 Knowledge Import ---

func importL3Knowledge(
	engine *storage.StorageEngine,
	data ImportData,
	mode write.ImportMode,
	knowledgeTitle *string,
	l3Idx *index.L3Index,
	l3Deg *index.DegreeTracker,
	l3Cac *index.AdjacencyCache,
) (*ImportResult, error) {
	if len(data.Knowledge) == 0 {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "knowledge data required")
	}
	nowMs := timeutil.NowMs()
	graphCache := make(map[string]uint64)
	var createdIDs, updatedIDs []string
	var createdNodes []*model.HypergraphNode
	var skipped int
	var errors []write.ImportError

	for i, item := range data.Knowledge {
		node, err := importOneKnowledge(engine, &item, mode, graphCache, nowMs, &createdIDs, &updatedIDs, &skipped)
		if err != nil {
			errors = append(errors, write.ImportError{Index: i, Message: err.Error()})
		}
		if node != nil {
			createdNodes = append(createdNodes, node)
		}
	}
	updateL3Indexes(createdNodes, l3Idx, l3Deg, l3Cac)
	status := write.ImportSuccess
	if len(errors) > 0 && len(createdIDs) > 0 {
		status = write.ImportPartialSuccess
	} else if len(createdIDs) == 0 && len(errors) > 0 {
		status = write.ImportFailed
	}
	return &ImportResult{
		Status:         status,
		ID:             firstOrNil(createdIDs),
		IDs:            createdIDs,
		CreatedIDs:     createdIDs,
		UpdatedIDs:     updatedIDs,
		SkippedCount:   skipped,
		Errors:         errors,
		KnowledgeTitle: knowledgeTitle,
		NodeCount:      len(createdIDs),
	}, nil
}

func importOneKnowledge(
	engine *storage.StorageEngine,
	item *KnowledgeImportItem,
	mode write.ImportMode,
	graphCache map[string]uint64,
	nowMs int64,
	createdIDs, updatedIDs *[]string,
	skipped *int,
) (*model.HypergraphNode, error) {
	titleHash := hash.HashID(item.Title)

	switch mode {
	case write.ImportSkip:
		if engine.Contains(titleHash) {
			*skipped++
			return nil, nil
		}
	case write.ImportMerge:
		if engine.Contains(titleHash) {
			*updatedIDs = append(*updatedIDs, hash.FormatHash(titleHash))
			return nil, nil
		}
	case write.ImportOverwrite:
		// Continue — will overwrite
	}

	graphID, err := ensureGraphSlot(engine, item.Domain, graphCache, nowMs)
	if err != nil {
		return nil, err
	}
	node := model.HypergraphNode{
		IDHash:     titleHash,
		GraphID:    graphID,
		Title:      item.Title,
		NodeType:   item.KnowledgeType,
		Content:    strutil.SafeCharSlice(item.Text, 200),
		Keywords:   item.Keywords,
		SourceRef:  item.SourceRef,
		Importance: 0.7,
		ValidFrom:  nowMs,
		CreatedAt:  nowMs,
		UpdatedAt:  nowMs,
		Version:    1,
	}
	if err := record.WriteHypergraphNode(engine, titleHash, &node); err != nil {
		return nil, err
	}
	*createdIDs = append(*createdIDs, hash.FormatHash(titleHash))
	return &node, nil
}

func ensureGraphSlot(
	engine *storage.StorageEngine,
	domain string,
	cache map[string]uint64,
	nowMs int64,
) (uint64, error) {
	if gid, ok := cache[domain]; ok {
		return gid, nil
	}
	gid := hash.HashID(domain)
	cache[domain] = gid
	if engine.Contains(gid) {
		return gid, nil
	}
	slot := model.HypergraphSlot{
		IDHash:    gid,
		Name:      domain,
		Source:    model.HypergraphSource{Kind: model.SourceManual},
		CreatedAt: nowMs,
		UpdatedAt: nowMs,
		Version:   1,
	}
	if err := record.WriteGraphSlot(engine, gid, &slot); err != nil {
		return gid, err
	}
	return gid, nil
}

// --- helpers ---

// updateL3Indexes updates L3 indexes after knowledge node creation.
func updateL3Indexes(
	nodes []*model.HypergraphNode,
	l3Idx *index.L3Index,
	l3Deg *index.DegreeTracker,
	l3Cac *index.AdjacencyCache,
) {
	if l3Idx == nil || l3Deg == nil || l3Cac == nil {
		return
	}
	seen := make(map[uint64]bool)
	for _, node := range nodes {
		l3Idx.AddNode(node)
		l3Deg.OnNodeAdded(node.GraphID, node.IDHash)
		seen[node.GraphID] = true
	}
	for gid := range seen {
		l3Cac.Invalidate(gid)
	}
}

func stringOr(s *string, def string) string {
	if s != nil {
		return *s
	}
	return def
}

func firstOrNil(ss []string) *string {
	if len(ss) == 0 {
		return nil
	}
	return &ss[0]
}

// matchesKeyword checks case-insensitive keyword containment.
func matchesKeyword(text, keyword string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(keyword))
}
