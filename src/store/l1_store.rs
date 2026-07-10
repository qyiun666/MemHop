// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L1 SceneNode + SceneEdge CRUD — pure data operations.

use crate::layers::context_node::SceneNode;
use crate::layers::hyperedge::SceneEdge;
use crate::shared::common::format_hash;
use crate::storage::record::{REC_L1_HYPEREDGE, REC_L1_SCENE_NODE};
use crate::storage::StorageEngine;
use crate::store::{read_slot, write_slot};
use crate::MemHopError;

/// Read a SceneNode by its id_hash.
pub fn read_scene_node(
    engine: &StorageEngine,
    id_hash: u64,
) -> Result<Option<SceneNode>, MemHopError> {
    read_slot(engine, id_hash)
}

/// Write a SceneNode. Returns the hex-formatted node ID.
pub fn write_scene_node(
    engine: &mut StorageEngine,
    node: SceneNode,
) -> Result<String, MemHopError> {
    write_slot(engine, REC_L1_SCENE_NODE, node.id_hash, &node)?;
    Ok(format_hash(node.id_hash))
}

/// Read a SceneEdge by its id_hash.
pub fn read_scene_edge(
    engine: &StorageEngine,
    id_hash: u64,
) -> Result<Option<SceneEdge>, MemHopError> {
    read_slot(engine, id_hash)
}

/// Write a SceneEdge. Returns the hex-formatted edge ID.
pub fn write_scene_edge(
    engine: &mut StorageEngine,
    edge: SceneEdge,
) -> Result<String, MemHopError> {
    write_slot(engine, REC_L1_HYPEREDGE, edge.id_hash, &edge)?;
    Ok(format_hash(edge.id_hash))
}
