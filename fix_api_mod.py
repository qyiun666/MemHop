#!/usr/bin/env python3
"""Clean remaining L6 in api/mod.rs."""

import os

path = '/Volumes/zt_hd/projects/meow/memhop/src/api/mod.rs'
with open(path, 'r') as f:
    lines = f.readlines()

# We'll rebuild the file line by line, skipping L6-related parts
new_lines = []
i = 0
while i < len(lines):
    line = lines[i]
    
    # Skip pathways field (lines 110-111)
    if '/// L6 pathway weights cache' in line:
        i += 1
        # Skip the next line too (the field declaration)
        if i < len(lines) and 'pub(crate) pathways:' in lines[i]:
            i += 1
        continue
    
    # Skip comment about pathways in snapshot pass
    if 'Handle L1, sparse index, L3 index map, and pathways' in line:
        line = line.replace('Handle L1, sparse index, L3 index map, and pathways in one snapshot pass',
                           'Handle L1, sparse index, and L3 index map in one snapshot pass')
    
    # Change tuple from 4 to 3 elements
    if 'let (sparse_index, l3_index_map, pathways, l1_reverse_index) =' in line:
        line = line.replace('let (sparse_index, l3_index_map, pathways, l1_reverse_index) =',
                           'let (sparse_index, l3_index_map, l1_reverse_index) =')
    
    # Skip the entire L6 pathway weights block (lines 190-205)
    if '// L6 pathway weights' in line:
        # Skip until we find the blank line after the block
        while i < len(lines) and lines[i].strip() != '':
            i += 1
        # Skip the blank line too
        if i < len(lines) and lines[i].strip() == '':
            i += 1
        continue
    
    # Change tuple return from 4 to 3 elements
    if '(si, l3, pw, l1)' in line:
        line = line.replace('(si, l3, pw, l1)', '(si, l3, l1)')
    
    # Change None arm tuple from 4 to 3 elements
    if '(SparseIndex::new(), HashMap::new(), Vec::new(), l1)' in line:
        line = line.replace('(SparseIndex::new(), HashMap::new(), Vec::new(), l1)',
                           '(SparseIndex::new(), HashMap::new(), l1)')
    
    # Skip pathways in constructor
    if 'l3_index_map,' in line and i+1 < len(lines) and 'pathways,' in lines[i+1]:
        new_lines.append(line)
        i += 2  # skip pathways line
        continue
    
    new_lines.append(line)
    i += 1

with open(path, 'w') as f:
    f.writelines(new_lines)

# Verify
with open(path, 'r') as f:
    c = f.read()
remaining = [l.strip() for l in c.split('\n') if 'pathways' in l or 'l6_pathway' in l]
if remaining:
    print('STILL HAS L6:')
    for r in remaining:
        print('  ', r)
else:
    print('OK: api/mod.rs L6 fully removed')
