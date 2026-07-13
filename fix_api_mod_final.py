#!/usr/bin/env python3
"""Fix api/mod.rs by reading and doing exact string replacements."""

path = '/Volumes/zt_hd/projects/meow/memhop/src/api/mod.rs'
with open(path, 'r') as f:
    content = f.read()

# 1. Remove mod pathway_ops
content = content.replace('mod pathway_ops;\n', '')

# 2. Remove pathways field (including comment)
content = content.replace(
    '    /// L6 pathway weights cache (loaded on demand).\n    pub(crate) pathways: Vec<crate::layers::pathway::PathwayWeightSlot>,\n',
    ''
)

# 3. Change tuple declaration from 4 to 3 elements
content = content.replace(
    'let (sparse_index, l3_index_map, pathways, l1_reverse_index) =',
    'let (sparse_index, l3_index_map, l1_reverse_index) ='
)

# 4. Change comment
content = content.replace(
    'Handle L1, sparse index, L3 index map, and pathways in one snapshot pass',
    'Handle L1, sparse index, and L3 index map in one snapshot pass'
)

# 5. Remove the entire L6 pathway weights block
old_block = '''                    // L6 pathway weights
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
content = content.replace(old_block, '')

# 6. Change tuple return from 4 to 3
content = content.replace('(si, l3, pw, l1)', '(si, l3, l1)')

# 7. Change None arm tuple from 4 to 3
content = content.replace(
    '(SparseIndex::new(), HashMap::new(), Vec::new(), l1)',
    '(SparseIndex::new(), HashMap::new(), l1)'
)

# 8. Remove pathways from constructor
content = content.replace(
    '            l3_index_map,\n            pathways,\n            l2_meta:',
    '            l3_index_map,\n            l2_meta:'
)

with open(path, 'w') as f:
    f.write(content)

# Verify
with open(path, 'r') as f:
    c = f.read()
remaining = [l.strip() for l in c.split('\n') if 'pathway' in l.lower() or 'l6_pathway' in l.lower()]
if remaining:
    print('STILL HAS L6:')
    for r in remaining:
        print('  ', r)
else:
    print('OK: api/mod.rs L6 fully removed')
