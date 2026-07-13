#!/usr/bin/env python3
"""Remove remaining L6 references."""

import os

BASE = '/Volumes/zt_hd/projects/meow/memhop'

def read_file(path):
    with open(path, 'r') as f:
        return f.read()

def write_file(path, content):
    with open(path, 'w') as f:
        f.write(content)

# 1. layers/mod.rs - remove L6 comment
path = os.path.join(BASE, 'src/layers/mod.rs')
content = read_file(path)
content = content.replace('// L6: PathwayWeightSlot\n', '')
write_file(path, content)
print('OK: layers/mod.rs comment')

# 2. api/mod.rs - remove pathways field and snapshot loading
path = os.path.join(BASE, 'src/api/mod.rs')
content = read_file(path)

# Remove pathways field
old = '    /// L6 pathway weights cache (loaded on demand).\n    pub(crate) pathways: Vec<crate::layers::pathway::PathwayWeightSlot>,\n'
if old in content:
    content = content.replace(old, '')
    print('OK: api/mod.rs pathways field')
else:
    print('SKIP: api/mod.rs pathways field')

# Remove snapshot loading block
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
    print('OK: api/mod.rs snapshot loading')
else:
    print('SKIP: api/mod.rs snapshot loading')

# Fix tuple returns
if '(si, l3, pw, l1)' in content:
    content = content.replace('(si, l3, pw, l1)', '(si, l3, l1)')
    print('OK: api/mod.rs tuple match')

if '(SparseIndex::new(), HashMap::new(), Vec::new(), l1)' in content:
    content = content.replace('(SparseIndex::new(), HashMap::new(), Vec::new(), l1)', '(SparseIndex::new(), HashMap::new(), l1)')
    print('OK: api/mod.rs tuple None')

# Remove pathways from constructor
old = '            l3_index_map,\n            pathways,\n            l2_meta:'
new = '            l3_index_map,\n            l2_meta:'
if old in content:
    content = content.replace(old, new)
    print('OK: api/mod.rs constructor')

write_file(path, content)

# 3. dream/mod.rs - remove l6_decayed fields from DreamReport init
path = os.path.join(BASE, 'src/dream/mod.rs')
content = read_file(path)

old = '''        l6_decayed: 0,
        l6_pruned: 0,
        l6_decayed_details: None,
        l6_pruned_details: None,
'''
if old in content:
    content = content.replace(old, '')
    print('OK: dream/mod.rs report init')
else:
    print('SKIP: dream/mod.rs report init')

write_file(path, content)

# 4. benches/agent_workflow.rs - remove L6 benchmarks
path = os.path.join(BASE, 'benches/agent_workflow.rs')
content = read_file(path)

# Find and remove the entire L6 benchmark section
start_marker = '// ============================================================================\n// Benchmarks: L6 Pathway (list + CRUD on throw-away DB)\n// ============================================================================\n\n'
if start_marker in content:
    start_idx = content.find(start_marker)
    # Find the next section marker after this
    next_section = content.find('// ============================================================================\n// Benchmarks: L5 Crystal update', start_idx)
    if next_section == -1:
        next_section = content.find('// ============================================================================\n// Helpers', start_idx)
    if next_section != -1:
        content = content[:start_idx] + content[next_section:]
        print('OK: benches/agent_workflow.rs L6 section removed')
    else:
        print('WARN: could not find end of L6 section')
else:
    print('SKIP: benches L6 section marker not found')

# Remove L6 from criterion_group
if '    // L6\n    bench_l6_pathway_list,\n    bench_l6_pathway_crud,\n' in content:
    content = content.replace('    // L6\n    bench_l6_pathway_list,\n    bench_l6_pathway_crud,\n', '')
    print('OK: benches criterion_group')

# Remove L6 imports
if '    ArchiveQuery, CrystalListQuery, KnowledgeNodeQuery, L6Filter, UpdateL6Fields,\n' in content:
    content = content.replace('    ArchiveQuery, CrystalListQuery, KnowledgeNodeQuery, L6Filter, UpdateL6Fields,\n', '    ArchiveQuery, CrystalListQuery, KnowledgeNodeQuery,\n')
    print('OK: benches imports L6Filter/UpdateL6Fields')

if '    PathwayWeightSlot, RequestSource, SearchQuery, TargetLayer, TopicListQuery, UpdateRequest,\n' in content:
    content = content.replace('    PathwayWeightSlot, RequestSource, SearchQuery, TargetLayer, TopicListQuery, UpdateRequest,\n', '    RequestSource, SearchQuery, TargetLayer, TopicListQuery, UpdateRequest,\n')
    print('OK: benches imports PathwayWeightSlot')

write_file(path, content)

print('\nRemaining cleanup complete.')
