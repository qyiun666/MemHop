#!/usr/bin/env python3
"""Remove all L6 references from MemHop codebase."""

import os
import sys

BASE = '/Volumes/zt_hd/projects/meow/memhop'

def read_file(path):
    with open(path, 'r') as f:
        return f.read()

def write_file(path, content):
    with open(path, 'w') as f:
        f.write(content)

# ============================================================================
# 1. layers/mod.rs - remove pathway module
# ============================================================================
path = os.path.join(BASE, 'src/layers/mod.rs')
content = read_file(path)
if 'pub(crate) mod pathway;' in content:
    content = content.replace('pub(crate) mod pathway;\n', '')
    write_file(path, content)
    print('OK: src/layers/mod.rs')
else:
    print('SKIP: src/layers/mod.rs (no pathway mod)')

# ============================================================================
# 2. dream/mod.rs - remove l6_decay module declaration
# ============================================================================
path = os.path.join(BASE, 'src/dream/mod.rs')
content = read_file(path)
if 'pub(crate) mod l6_decay;' in content:
    content = content.replace('pub(crate) mod l6_decay;\n', '')
    write_file(path, content)
    print('OK: src/dream/mod.rs (module decl)')
else:
    print('SKIP: src/dream/mod.rs (no l6_decay module decl)')

# ============================================================================
# 3. api/mod.rs - remove pathway_ops module, pathways field, snapshot loading
# ============================================================================
path = os.path.join(BASE, 'src/api/mod.rs')
content = read_file(path)

# Remove mod pathway_ops
if 'mod pathway_ops;\n' in content:
    content = content.replace('mod pathway_ops;\n', '')

# Remove pathways field from MemHop struct
old = '    /// L6 pathway weights cache (loaded on demand).\n    pub(crate) pathways: Vec<crate::layers::pathway::PathwayWeightSlot>,\n'
if old in content:
    content = content.replace(old, '')

# Remove L6 pathway loading from open() snapshot deserialization
old = '''                    // L6 pathway weights
                    let pw: Vec<crate::layers::pathway::PathwayWeightSlot> =
                        if !snapshot.l6_pathway_data.is_empty() {
                            match bincode::deserialize(&snapshot.l6_pathway_data) {
                                Ok(p) => p,
                                Err(e) => {
                                    tracing::warn!(
                                        "Failed to deserialize pathways from snapshot: {}. Using empty vec.",
                                        e
                                    );
                                    Vec::new()
                                }
                            }
                        } else {
                            Vec::new()
                        };

'''
if old in content:
    content = content.replace(old, '')

# Update tuple in match arm: (si, l3, pw, l1) -> (si, l3, l1)
old = '                    (si, l3, pw, l1)\n'
new = '                    (si, l3, l1)\n'
if old in content:
    content = content.replace(old, new)

# Update None arm tuple
old = '                    (SparseIndex::new(), HashMap::new(), Vec::new(), l1)\n'
new = '                    (SparseIndex::new(), HashMap::new(), l1)\n'
if old in content:
    content = content.replace(old, new)

# Remove pathways from MemHop constructor
old = '            l3_index_map,\n            pathways,\n            l2_meta:'
new = '            l3_index_map,\n            l2_meta:'
if old in content:
    content = content.replace(old, new)

write_file(path, content)
print('OK: src/api/mod.rs')

# ============================================================================
# 4. dream/mod.rs - remove l6_decay stage from dream_pipeline
# ============================================================================
path = os.path.join(BASE, 'src/dream/mod.rs')
content = read_file(path)

old = '''    run_stage(
        "l6_decay",
        "L6 pathway decay failed",
        |report, _| {
            let l6_report = l6_decay::decay_l6_pathways(engine, decay_config)?;
            report.l6_decayed = l6_report.decayed;
            report.l6_pruned = l6_report.pruned;
            report.l6_decayed_details = if l6_report.decayed_details.is_empty() {
                None
            } else {
                Some(l6_report.decayed_details)
            };
            report.l6_pruned_details = if l6_report.pruned_details.is_empty() {
                None
            } else {
                Some(l6_report.pruned_details)
            };
            let count = l6_report.decayed + l6_report.pruned;
            Ok((
                format!(
                    "L6 decay: {} decayed, {} pruned",
                    l6_report.decayed, l6_report.pruned
                ),
                count,
            ))
        },
        &mut report,
        sparse_index,
        &mut stages,
        false,
        |_, _| {},
        start_time,
    )?;

'''
if old in content:
    content = content.replace(old, '')
else:
    print('WARN: l6_decay stage not found in dream_pipeline')

write_file(path, content)
print('OK: src/dream/mod.rs (pipeline stage)')

# ============================================================================
# 5. dream/prune.rs - remove L6 fields from DreamReport
# ============================================================================
path = os.path.join(BASE, 'src/dream/prune.rs')
content = read_file(path)

# Remove PathwayWeightSlot import
if 'use crate::layers::pathway::PathwayWeightSlot;\n' in content:
    content = content.replace('use crate::layers::pathway::PathwayWeightSlot;\n', '')

# Remove L6 fields from struct
old = '''    /// Number of L6 pathway weights decayed
    pub l6_decayed: usize,
    /// Number of L6 pathway weights pruned (below threshold)
    pub l6_pruned: usize,
    /// Decayed L6 pathway weights with their updated values (None if no decay occurred)
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub l6_decayed_details: Option<Vec<PathwayWeightSlot>>,
    /// Pruned L6 pathway weights with their final values before removal (None if no pruning occurred)
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub l6_pruned_details: Option<Vec<PathwayWeightSlot>>,
'''
if old in content:
    content = content.replace(old, '')

# Remove L6 fields from test
old = '''            l6_decayed: 0,
            l6_pruned: 0,
            l6_decayed_details: None,
            l6_pruned_details: None,
'''
if old in content:
    content = content.replace(old, '')

write_file(path, content)
print('OK: src/dream/prune.rs')

# ============================================================================
# 6. storage/engine.rs - remove l6_pathway_data from IndexSnapshotData
# ============================================================================
path = os.path.join(BASE, 'src/storage/engine.rs')
content = read_file(path)

# Remove field from struct
old = '    pub l3_index_data: Vec<u8>,\n    pub l6_pathway_data: Vec<u8>,\n'
new = '    pub l3_index_data: Vec<u8>,\n'
if old in content:
    content = content.replace(old, new)

# Remove serialization of l6_pathway_data in build_snapshot
old = '''        buf.extend_from_slice(&(index_data.l3_index_data.len() as u32).to_le_bytes());
        buf.extend_from_slice(&index_data.l3_index_data);
        buf.extend_from_slice(&(index_data.l6_pathway_data.len() as u32).to_le_bytes());
        buf.extend_from_slice(&index_data.l6_pathway_data);
'''
new = '''        buf.extend_from_slice(&(index_data.l3_index_data.len() as u32).to_le_bytes());
        buf.extend_from_slice(&index_data.l3_index_data);
'''
if old in content:
    content = content.replace(old, new)

# In load_snapshot: read but discard l6_pathway_data for backward compat
old = '        let l6_pathway_data = parse_field(snap, &mut pos, "l6_pathway_data")?;\n'
new = '        let _l6_pathway_data = parse_field(snap, &mut pos, "l6_pathway_data")?; // backward compat: discard\n'
if old in content:
    content = content.replace(old, new)

# Remove from snapshot_data construction
old = '            l6_pathway_data,\n        });\n'
new = '        });\n'
if old in content:
    content = content.replace(old, new)

# Fix test: remove l6_pathway_data from test snapshot
old = '''                l3_index_data: b"l3".to_vec(),
                l6_pathway_data: b"l6".to_vec(),
'''
new = '                l3_index_data: b"l3".to_vec(),\n'
if old in content:
    content = content.replace(old, new)

write_file(path, content)
print('OK: src/storage/engine.rs')

# ============================================================================
# 7. query/types.rs - remove L6Filter, UpdateL6Fields, l6_weight_count
# ============================================================================
path = os.path.join(BASE, 'src/query/types.rs')
content = read_file(path)

# Remove UpdateL6Fields
old = '''/// Partial update fields for an L6 pathway weight.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct UpdateL6Fields {
    pub source_node: Option<String>,
    pub target_node: Option<String>,
    pub weight: Option<f32>,
    pub weight_delta: Option<f32>,
    pub success_rate: Option<f32>,
    pub trigger_count: Option<u32>,
    pub last_accessed: Option<u64>,
    pub metadata: Option<String>,
}

'''
if old in content:
    content = content.replace(old, '')

# Remove L6Filter
old = '''/// Filter for listing L6 pathway weights.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct L6Filter {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source_prefix: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub target_prefix: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub min_weight: Option<f32>,
}

'''
if old in content:
    content = content.replace(old, '')

# Remove l6_weight_count from MemHopStats
old = '''    /// L5 action-chain / crystal count
    pub l5_crystal_count: usize,
    /// L6 pathway weight entry count
    pub l6_weight_count: usize,
    /// Database file size in bytes
'''
new = '''    /// L5 action-chain / crystal count
    pub l5_crystal_count: usize,
    /// Database file size in bytes
'''
if old in content:
    content = content.replace(old, new)

write_file(path, content)
print('OK: src/query/types.rs')

# ============================================================================
# 8. lib.rs - remove L6 re-exports
# ============================================================================
path = os.path.join(BASE, 'src/lib.rs')
content = read_file(path)

if 'pub use layers::pathway::PathwayWeightSlot;\n' in content:
    content = content.replace('pub use layers::pathway::PathwayWeightSlot;\n', '')

if '    L6Filter,\n' in content:
    content = content.replace('    L6Filter,\n', '')

if '    UpdateL6Fields,\n' in content:
    content = content.replace('    UpdateL6Fields,\n', '')

write_file(path, content)
print('OK: src/lib.rs')

# ============================================================================
# 9. config.rs - remove lambda_pathway and pathway_remove_threshold
# ============================================================================
path = os.path.join(BASE, 'src/config.rs')
content = read_file(path)

old = '''                lambda_pathway: 0.01,
                pathway_remove_threshold: 0.05,
'''
if old in content:
    content = content.replace(old, '')

old = '''    /// L6 pathway weight exponential decay lambda (per second).
    pub lambda_pathway: f32,
    /// L6 pathway weight removal threshold after decay.
    pub pathway_remove_threshold: f32,
'''
if old in content:
    content = content.replace(old, '')

write_file(path, content)
print('OK: src/config.rs')

# ============================================================================
# 10. api/checkpoint.rs - remove l6_pathway_data from snapshot
# ============================================================================
path = os.path.join(BASE, 'src/api/checkpoint.rs')
content = read_file(path)

old = '''            l3_index_data: bincode::serialize(&self.l3_index_map)
                .map_err(|e| MemHopError::Serialization(e.to_string()))?,
            l6_pathway_data: bincode::serialize(&self.pathways)
                .map_err(|e| MemHopError::Serialization(e.to_string()))?,
'''
new = '''            l3_index_data: bincode::serialize(&self.l3_index_map)
                .map_err(|e| MemHopError::Serialization(e.to_string()))?,
'''
if old in content:
    content = content.replace(old, new)

write_file(path, content)
print('OK: src/api/checkpoint.rs')

# ============================================================================
# 11. api/diagnostic_ops.rs - remove pathways counting
# ============================================================================
path = os.path.join(BASE, 'src/api/diagnostic_ops.rs')
content = read_file(path)

old = '''        // L6 pathway weights are stored as a serialized blob, count from in-memory cache.
        layer_counts.insert("l6_pathway".to_string(), self.pathways.len());

'''
if old in content:
    content = content.replace(old, '')

write_file(path, content)
print('OK: src/api/diagnostic_ops.rs')

# ============================================================================
# 12. api/dream_ops.rs - update comment
# ============================================================================
path = os.path.join(BASE, 'src/api/dream_ops.rs')
content = read_file(path)

old = '    /// L3 distillation, L2 compression, L1 rebuild/decay, L0 profile regeneration,\n    /// habit distillation, L5 crystallization, L6 pathway decay, and crystal pruning.\n'
new = '    /// L3 distillation, L2 compression, L1 rebuild/decay, L0 profile regeneration,\n    /// habit distillation, L5 crystallization, and crystal pruning.\n'
if old in content:
    content = content.replace(old, new)

write_file(path, content)
print('OK: src/api/dream_ops.rs')

# ============================================================================
# 13. benches/agent_workflow.rs - remove L6 benchmark imports and functions
# ============================================================================
path = os.path.join(BASE, 'benches/agent_workflow.rs')
content = read_file(path)

# Remove L6 imports
old = '    ArchiveQuery, CrystalListQuery, KnowledgeNodeQuery, L6Filter, UpdateL6Fields,\n'
new = '    ArchiveQuery, CrystalListQuery, KnowledgeNodeQuery,\n'
if old in content:
    content = content.replace(old, new)

old = '    PathwayWeightSlot, RequestSource, SearchQuery, TargetLayer, TopicListQuery, UpdateRequest,\n'
new = '    RequestSource, SearchQuery, TargetLayer, TopicListQuery, UpdateRequest,\n'
if old in content:
    content = content.replace(old, new)

# Remove bench_l6_pathway_list function
old = '''// ============================================================================
// Benchmarks: L6 Pathway (list + CRUD on throw-away DB)
// ============================================================================

fn bench_l6_pathway_list(c: &mut Criterion) {
    c.bench_function("l6_pathway_list", |b| {
        b.iter(|| {
            let db = db().lock().unwrap();
            let res = db.list_l6(None).expect("list_l6 failed");
            black_box(res.len())
        })
    });
}

fn bench_l6_pathway_crud(c: &mut Criterion) {
    // Use a throw-away DB — L6 doesn't need encoder.
    c.bench_function("l6_pathway_crud", |b| {
        b.iter_batched(
            || {
                let dir = TempDir::new().expect("TempDir");
                let path = dir.path().join("l6.meh");
                let mut config = MemHopConfig::new(path, 768, String::new(), String::new(), String::new(), String::new(), String::new());
                let db = MemHop::open(config).expect("open");
                (db, dir)
            },
            |(mut db, _dir)| {
                // Add
                let slot = PathwayWeightSlot {
                    id_hash: 42,
                    source_node: "condition:deploy".into(),
                    target_node: "action:restart".into(),
                    weight: 0.9,
                    trigger_count: 10,
                    success_rate: 0.85,
                    last_accessed: 1700000000000,
                    metadata: r#"{"strategy":"react"}"#.into(),
                    created_at: 1000,
                    updated_at: 2000,
                    version: 1,
                };
                db.add_l6(vec![slot]).expect("add_l6 failed");

                // Get
                let got = db.get_l6("000000000000002a").expect("get_l6 failed");
                black_box(got.is_some());

                // Update
                let updated = db
                    .update_l6(
                        "000000000000002a",
                        UpdateL6Fields {
                            weight: Some(0.95),
                            ..Default::default()
                        },
                    )
                    .expect("update_l6 failed");
                black_box(updated.weight);

                // Update weight (via weight_delta)
                let adjusted = db
                    .update_l6(
                        "000000000000002a",
                        UpdateL6Fields {
                            weight_delta: Some(0.05),
                            ..Default::default()
                        },
                    )
                    .expect("update_l6 with delta failed");
                black_box(adjusted.weight);

                // List with filter
                let filtered = db
                    .list_l6(Some(L6Filter {
                        source_prefix: Some("condition:".into()),
                        min_weight: Some(0.5),
                        ..Default::default()
                    }))
                    .expect("list_l6(filtered) failed");
                black_box(filtered.len());

                // Delete
                db.delete_l6("000000000000002a").expect("delete_l6 failed");
            },
            criterion::BatchSize::SmallInput,
        )
    });
}

'''
if old in content:
    content = content.replace(old, '')
else:
    print('WARN: bench_l6_pathway_list/crud not found')

# Remove L6 from criterion_group
old = '''    // L6
    bench_l6_pathway_list,
    bench_l6_pathway_crud,
'''
if old in content:
    content = content.replace(old, '')

write_file(path, content)
print('OK: benches/agent_workflow.rs')

print('\nAll modifications complete.')
