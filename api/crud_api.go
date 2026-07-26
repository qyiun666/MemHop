// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// v0.60.0 unified CRUD entry points.
// Get / List / Delete dispatch by Layer to the appropriate internal handler.
// UpdateMemory remains in update_api.go (layer-generic field update).

package memhop

import (
	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/query/crud"
)

// Get retrieves a single record from the given layer.
// LayerScene: an empty id returns the full scene graph; a non-empty id
// filters the graph to that scene.
func (m *MemHop) Get(layer Layer, id string) (*GetResult, error) {
	if err := m.beginRead(); err != nil {
		return nil, err
	}
	defer m.mu.RUnlock()
	switch layer {
	case LayerProfile:
		p, err := crud.LoadProfileSlot(m.engine)
		if err != nil {
			return nil, err
		}
		return &GetResult{Profile: p}, nil
	case LayerScene:
		var sceneFilter *string
		if id != "" {
			sceneFilter = &id
		}
		g, err := crud.LoadL1Graph(m.engine, crud.ParseSceneFilter(sceneFilter))
		if err != nil {
			return nil, err
		}
		return &GetResult{SceneGraph: g}, nil
	case LayerTopic:
		slot, err := crud.GetL2(m.engine, id)
		if err != nil {
			return nil, err
		}
		detail := crud.ToTopicDetail(slot)
		return &GetResult{Topic: &detail}, nil
	case LayerKnowledge:
		d, err := crud.GetL3(m.engine, id)
		if err != nil {
			return nil, err
		}
		return &GetResult{Knowledge: d}, nil
	case LayerArchive:
		a, err := crud.GetArchive(m.engine, id)
		if err != nil {
			return nil, err
		}
		return &GetResult{Archive: a}, nil
	case LayerCrystal:
		chain, err := crud.GetL5(m.engine, id)
		if err != nil {
			return nil, err
		}
		summary := crud.ToCrystalSummary(chain)
		return &GetResult{Crystal: &summary}, nil
	default:
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "unsupported layer for Get")
	}
}

// List returns a paginated slice for the given layer.
// The corresponding sub-query in req (Topic / Knowledge / Archive / Crystal)
// must be populated for that layer.
func (m *MemHop) List(layer Layer, req ListRequest) (*ListResult, error) {
	if err := m.beginRead(); err != nil {
		return nil, err
	}
	defer m.mu.RUnlock()
	switch layer {
	case LayerTopic:
		q := TopicListQuery{}
		if req.Topic != nil {
			q = *req.Topic
		}
		r, err := crud.ListL2(m.engine, q)
		if err != nil {
			return nil, err
		}
		return &ListResult{Topics: r}, nil
	case LayerKnowledge:
		q := KnowledgeListQuery{}
		if req.Knowledge != nil {
			q = *req.Knowledge
		}
		r, err := crud.ListKnowledge(m.engine, q)
		if err != nil {
			return nil, err
		}
		return &ListResult{Knowledge: r}, nil
	case LayerArchive:
		q := ArchiveQuery{}
		if req.Archive != nil {
			q = *req.Archive
		}
		r, err := crud.QueryArchives(m.engine, q)
		if err != nil {
			return nil, err
		}
		return &ListResult{Archives: r}, nil
	case LayerCrystal:
		q := CrystalListQuery{}
		if req.Crystal != nil {
			q = *req.Crystal
		}
		r, err := crud.ListCrystals(m.engine, q)
		if err != nil {
			return nil, err
		}
		return &ListResult{Crystals: r}, nil
	default:
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "unsupported layer for List")
	}
}

// Delete removes a record from the given layer.
// LayerTopic / LayerKnowledge / LayerCrystal are supported. Other layers
// return ErrInvalidQuery (Profile is a singleton; Scene / Archive have no
// direct delete semantics).
func (m *MemHop) Delete(layer Layer, id string) error {
	if err := m.beginRead(); err != nil {
		return err
	}
	defer m.mu.RUnlock()
	switch layer {
	case LayerTopic:
		return crud.DeleteL2(m.engine, m.getL1Reverse(), m.sparseIndex, m.getL2Meta(), id)
	case LayerKnowledge:
		if err := crud.DeleteL3(m.engine, m.l3Index, id); err != nil {
			return err
		}
		// Invalidate L3 caches for the deleted graph (best-effort;
		// invalid hex simply means the graph didn't exist so nothing to invalidate).
		if h, perr := hash.ParseID(id); perr == nil {
			m.l3Cache.Invalidate(h)
			m.l3Degree.ClearGraph(h)
		}
		return nil
	case LayerCrystal:
		return crud.DeleteL5(m.engine, id)
	default:
		return mherrors.NewError(mherrors.ErrInvalidQuery, "unsupported layer for Delete")
	}
}
