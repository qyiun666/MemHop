// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Shared test helpers for MemHop unit tests.

use crate::file::header::FileHeader;
use crate::file::page::{allocate_page, encode_page_ref, write_page_data, PageHeader};
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::layers::context::{ActivationState, ContextSlot, LlmParams};
use crate::layers::context_node::ContextNode;
use crate::layers::hypergraph::{GraphEdgeKind, HypergraphEdge, HypergraphNode};
use crate::util::{PageType, PAGE_SIZE, SENTINEL_PAGE_ID};
use memmap2::MmapMut;
use std::fs::File;
use std::io::Write;

/// Create a minimal memory-mapped file with `pages` zeroed pages.
/// Returns only the mmap; callers manage headers and free lists themselves.
pub fn create_test_mmap_raw(pages: usize) -> MmapMut {
    let tf = tempfile::NamedTempFile::new().unwrap();
    let mut f = File::create(tf.path()).unwrap();
    f.write_all(&vec![0u8; PAGE_SIZE * pages]).unwrap();
    drop(f);
    let f = std::fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open(tf.path())
        .unwrap();
    unsafe { MmapMut::map_mut(&f).unwrap() }
}

/// Create a memory-mapped test file initialized with a `FileHeader` and free list.
/// Returns `(mmap, header, btree, file)` for tests that need a B-tree and allocator.
pub fn create_test_mmap(pages: usize) -> (MmapMut, FileHeader, BTreeIndex, File) {
    let temp_file = tempfile::NamedTempFile::new().unwrap();
    let path = temp_file.path();
    let mut file = File::create(path).unwrap();
    file.write_all(&vec![0u8; PAGE_SIZE * pages]).unwrap();
    drop(file);

    let file = std::fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open(path)
        .unwrap();
    let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
    let mut header = FileHeader::new(768);
    header.page_count = pages as u32;
    crate::file::free_list::init_free_list(&mut header).unwrap();

    for page_id in (2..pages as u32).rev() {
        crate::file::free_list::free_page(&mut mmap, &mut header, page_id).unwrap();
    }

    let btree = BTreeIndex::new();
    (mmap, header, btree, file)
}

/// Create a memory-mapped test file and keep the `NamedTempFile` alive so the
/// path remains valid for the lifetime of the test. Uses a manually written free list.
pub fn create_test_mmap_with_tempfile(
    pages: usize,
) -> (tempfile::NamedTempFile, MmapMut, FileHeader, File) {
    let temp_file = tempfile::NamedTempFile::new().unwrap();
    let path = temp_file.path();
    let mut file = File::create(path).unwrap();
    file.write_all(&vec![0u8; PAGE_SIZE * pages]).unwrap();
    drop(file);
    let file = std::fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open(path)
        .unwrap();
    let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
    let mut header = FileHeader::new(768);
    for page_id in 2..pages as u32 {
        let offset = page_id as usize * PAGE_SIZE;
        let next_free = if page_id + 1 < pages as u32 {
            page_id + 1
        } else {
            SENTINEL_PAGE_ID
        };
        mmap[offset..offset + 4].copy_from_slice(&next_free.to_le_bytes());
    }
    header.page_count = pages as u32;
    header.free_list_head = 2;
    (temp_file, mmap, header, file)
}

/// Build a `HypergraphNode` with DSL-test defaults.
pub fn make_node(id: u64, graph_id: u64, title: &str) -> HypergraphNode {
    HypergraphNode {
        id_hash: id,
        graph_id,
        title: title.to_string(),
        node_type: "concept".to_string(),
        content: "test".to_string(),
        keywords: vec![],
        source_ref: None,
        importance: 0.5,
        created_at: 0,
        updated_at: 0,
        version: 1,
    }
}

/// Build a `HypergraphEdge` with DSL-test defaults.
pub fn make_edge(id: u64, graph_id: u64, nodes: Vec<u64>) -> HypergraphEdge {
    HypergraphEdge {
        id_hash: id,
        graph_id,
        kind: GraphEdgeKind::Related,
        node_ids: nodes,
        weight: 1.0,
        label: None,
        created_at: 0,
    }
}

/// Build a small graph for DSL executor tests.
pub fn build_dsl_test_graph(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    file: &mut File,
) -> u64 {
    let gid = 1u64;
    let nodes = [
        make_node(101, gid, "Rust"),
        make_node(102, gid, "Cargo"),
        make_node(103, gid, "Borrow Checker"),
        make_node(104, gid, "Lifetime"),
        make_node(105, gid, "Trait"),
    ];
    for n in &nodes {
        crate::l3::store::add_node(mmap, header, btree, n.clone(), file, None, None).unwrap();
    }
    let edges = [
        make_edge(201, gid, vec![101, 102]),
        make_edge(202, gid, vec![101, 103]),
        make_edge(203, gid, vec![103, 104]),
        make_edge(204, gid, vec![101, 105, 102]),
    ];
    for e in &edges {
        crate::l3::store::add_edge(mmap, header, btree, e.clone(), file, None).unwrap();
    }
    gid
}

/// Build a `HypergraphNode` with store-test defaults.
pub fn create_test_node(id_hash: u64, graph_id: u64, title: &str) -> HypergraphNode {
    HypergraphNode {
        id_hash,
        graph_id,
        title: title.to_string(),
        node_type: "concept".to_string(),
        content: format!("content of {}", title),
        keywords: vec![],
        source_ref: None,
        importance: 0.5,
        created_at: 0,
        updated_at: 0,
        version: 1,
    }
}

/// Build a `HypergraphEdge` with store-test defaults.
pub fn create_test_edge(
    id_hash: u64,
    graph_id: u64,
    kind: GraphEdgeKind,
    node_ids: Vec<u64>,
) -> HypergraphEdge {
    HypergraphEdge {
        id_hash,
        graph_id,
        kind,
        node_ids,
        weight: 1.0,
        label: None,
        created_at: 0,
    }
}

/// Build a small graph for L3 store traversal tests.
pub fn build_test_graph(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    file: &mut File,
) -> (Vec<u64>, Vec<u64>) {
    let graph_id = 1u64;
    let node_ids = vec![101u64, 102, 103, 104, 105];
    let edge_ids = vec![201u64, 202, 203, 204];

    for &nid in &node_ids {
        crate::l3::store::add_node(
            mmap,
            header,
            btree,
            create_test_node(nid, graph_id, &format!("node{}", nid)),
            file,
            None,
            None,
        )
        .unwrap();
    }

    crate::l3::store::add_edge(
        mmap,
        header,
        btree,
        create_test_edge(201, graph_id, GraphEdgeKind::Related, vec![101, 102]),
        file,
        None,
    )
    .unwrap();
    crate::l3::store::add_edge(
        mmap,
        header,
        btree,
        create_test_edge(202, graph_id, GraphEdgeKind::Related, vec![102, 103]),
        file,
        None,
    )
    .unwrap();
    crate::l3::store::add_edge(
        mmap,
        header,
        btree,
        create_test_edge(203, graph_id, GraphEdgeKind::Dependency, vec![103, 104]),
        file,
        None,
    )
    .unwrap();
    crate::l3::store::add_edge(
        mmap,
        header,
        btree,
        create_test_edge(204, graph_id, GraphEdgeKind::Causal, vec![101, 103, 105]),
        file,
        None,
    )
    .unwrap();

    (node_ids, edge_ids)
}

/// Insert a `ContextSlot` into the mmap and update btree/sparse indexes.
pub fn insert_test_context(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    ctx: ContextSlot,
    file: &mut File,
) {
    let page_id = allocate_page(mmap, header, PageType::Context, 2, 0, file).unwrap();
    let serialized = ctx.serialize().unwrap();
    write_page_data(mmap, page_id, &serialized).unwrap();
    let page_ref = encode_page_ref(page_id, 0);
    btree.insert(ctx.id_hash, page_ref);
    let terms: Vec<String> = ctx
        .title
        .split_whitespace()
        .map(|s| s.to_lowercase())
        .collect();
    sparse_index.add_document(
        ctx.id_hash,
        terms,
        ctx.title.split_whitespace().count() as u32,
    );
}

/// Insert a `ContextNode` into the mmap and update the btree.
pub fn insert_test_context_node(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    node: ContextNode,
    file: &mut File,
) -> u64 {
    let page_id = allocate_page(mmap, header, PageType::ContextNode, 1, 0, file).unwrap();
    let serialized = node.serialize().unwrap();
    write_page_data(mmap, page_id, &serialized).unwrap();
    let page_ref = encode_page_ref(page_id, 0);
    btree.insert(node.id_hash, page_ref);
    page_ref
}

/// Write a `HypergraphNode` directly to a page without allocation.
pub fn write_hypergraph_node_page(mmap: &mut MmapMut, page_id: u32, node: HypergraphNode) {
    let offset = (page_id as usize) * PAGE_SIZE;
    let hdr = PageHeader::new(page_id, PageType::HypergraphNode, 3, SENTINEL_PAGE_ID);
    mmap[offset..offset + 32].copy_from_slice(&hdr.to_bytes());
    let data = node.serialize().unwrap();
    mmap[offset + 32..offset + 32 + data.len()].copy_from_slice(&data);
}

/// Write a `ContextSlot` directly to a page without allocation.
pub fn write_context_page(mmap: &mut MmapMut, page_id: u32, ctx: ContextSlot) {
    let offset = (page_id as usize) * PAGE_SIZE;
    let hdr = PageHeader::new(page_id, PageType::Context, 2, SENTINEL_PAGE_ID);
    mmap[offset..offset + 32].copy_from_slice(&hdr.to_bytes());
    let data = ctx.serialize().unwrap();
    mmap[offset + 32..offset + 32 + data.len()].copy_from_slice(&data);
}

/// Build a `ContextSlot` with sparse-index-test defaults.
pub fn create_test_context(id_hash: u64, title: &str, l3_refs: Vec<u64>) -> ContextSlot {
    ContextSlot {
        id_hash,
        parent_id: None,
        depth: 1,
        title: title.to_string(),
        summary: None,
        archive_refs: Vec::new(),
        l3_refs,
        turn_count: 0,
        created_at: 0,
        updated_at: 0,
        version: 1,
        importance: 0.5,
        activation_score: 0.0,
        is_active: true,
        activation_state: ActivationState::Active,
        centroid_page_ref: 0,
        dialogue_range: (0, 0),
        llm_params: LlmParams::default(),
    }
}
