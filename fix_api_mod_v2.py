#!/usr/bin/env python3
"""Remove L6 from api/mod.rs by line numbers."""

path = '/Volumes/zt_hd/projects/meow/memhop/src/api/mod.rs'
with open(path, 'r') as f:
    lines = f.readlines()

# Lines to delete (1-based, will convert to 0-based)
# Line 15: mod pathway_ops;
# Lines 110-111: pathways field and comment
# Lines 137: comment with pathways
# Lines 138: tuple with pathways
# Lines 190-205: L6 pathway weights block + blank line
# Line 207: (si, l3, pw, l1)
# Line 211: (SparseIndex::new(), HashMap::new(), Vec::new(), l1)
# Lines 243: pathways in constructor
lines_to_delete = set()

# Line 15 (0-based: 14)
if 'mod pathway_ops;' in lines[14]:
    lines_to_delete.add(14)

# Lines 110-111 (0-based: 109, 110)
if 'L6 pathway weights cache' in lines[109]:
    lines_to_delete.add(109)
if 'pub(crate) pathways:' in lines[110]:
    lines_to_delete.add(110)

# Line 137: comment (0-based: 136)
if 'L3 index map, and pathways' in lines[136]:
    lines[136] = lines[136].replace('L3 index map, and pathways in one snapshot pass', 'and L3 index map in one snapshot pass')

# Line 138: tuple (0-based: 137)
if 'pathways, l1_reverse_index' in lines[137]:
    lines[137] = lines[137].replace('sparse_index, l3_index_map, pathways, l1_reverse_index', 'sparse_index, l3_index_map, l1_reverse_index')

# Lines 190-205: L6 block (0-based: 189-204)
for i in range(189, 205):
    lines_to_delete.add(i)
# Also delete blank line 206 if it exists (0-based: 205)
if lines[205].strip() == '':
    lines_to_delete.add(205)

# Line 207: (si, l3, pw, l1) -> (si, l3, l1) (0-based: 206 or 207 depending on blank line)
for i in range(200, 210):
    if '(si, l3, pw, l1)' in lines[i]:
        lines[i] = lines[i].replace('(si, l3, pw, l1)', '(si, l3, l1)')
        break

# Line 211: None arm tuple (0-based: around 210)
for i in range(205, 215):
    if '(SparseIndex::new(), HashMap::new(), Vec::new(), l1)' in lines[i]:
        lines[i] = lines[i].replace('(SparseIndex::new(), HashMap::new(), Vec::new(), l1)', '(SparseIndex::new(), HashMap::new(), l1)')
        break

# Line 243: pathways in constructor (0-based: 242)
if 'pathways,' in lines[242]:
    lines_to_delete.add(242)

new_lines = [line for i, line in enumerate(lines) if i not in lines_to_delete]

with open(path, 'w') as f:
    f.writelines(new_lines)

# Verify
with open(path, 'r') as f:
    c = f.read()
remaining = [l.strip() for l in c.split('\n') if 'pathways' in l or 'l6_pathway' in l or 'pathway_ops' in l]
if remaining:
    print('STILL HAS L6:')
    for r in remaining:
        print('  ', r)
else:
    print('OK: api/mod.rs L6 fully removed')
