#!/usr/bin/env python3
"""Remove L6 from api/mod.rs - matching CURRENT actual file format."""

path = '/Volumes/zt_hd/projects/meow/memhop/src/api/mod.rs'
with open(path, 'r') as f:
    content = f.read()

# 1. Remove mod pathway_ops
content = content.replace('mod pathway_ops;\n', '')

# 2. Remove pathways field (no L6 comment in this version!)
content = content.replace(
    '    pub(crate) pathways: Vec<crate::layers::pathway::PathwayWeightSlot>,\n',
    ''
)

# 3. Remove let mut pathways = Vec::new();
content = content.replace('        let mut pathways = Vec::new();\n\n', '')

# 4. Remove the L6 snapshot loading block (12-space indent, no comment prefix)
old_block = '''            if !snapshot.l6_pathway_data.is_empty() {
                match bincode::deserialize(&snapshot.l6_pathway_data) {
                    Ok(pw) => pathways = pw,
                    Err(e) => tracing::warn!(
                        "Failed to deserialize L6 pathway data from snapshot: {}. Starting empty.",
                        e
                    ),
                }
            }
'''
content = content.replace(old_block, '')

# 5. Remove pathways from constructor
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
