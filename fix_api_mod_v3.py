#!/usr/bin/env python3
"""Remove L6 from api/mod.rs - exact line numbers."""

path = '/Volumes/zt_hd/projects/meow/memhop/src/api/mod.rs'
with open(path, 'r') as f:
    lines = f.readlines()

# Delete exact lines (0-based index)
# Line 15 (0-based 14): mod pathway_ops;
# Lines 110-111 (0-based 109-110): pathways comment + field
# Line 132 (0-based 131): let mut pathways = Vec::new();
# Lines 154-158 (0-based 153-157): l6_pathway_data block
# Line 248 (0-based 247): pathways in constructor
lines_to_delete = {14, 109, 110, 131, 153, 154, 155, 156, 157, 247}

new_lines = []
for i, line in enumerate(lines):
    if i in lines_to_delete:
        continue
    new_lines.append(line)

with open(path, 'w') as f:
    f.writelines(new_lines)

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
