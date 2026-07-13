#!/usr/bin/env python3
path = '/Volumes/zt_hd/projects/meow/memhop/src/storage/engine.rs'
with open(path, 'r') as f:
    lines = f.readlines()

# Delete line 512 (0-based: 511)
if 'l6_pathway_data' in lines[511]:
    del lines[511]
    print('Deleted line 512: l6_pathway_data')
else:
    print(f'Line 512: {lines[511].strip()!r}')

with open(path, 'w') as f:
    f.writelines(lines)

# Verify
with open(path, 'r') as f:
    c = f.read()
remaining = [l.strip() for l in c.split('\n') if 'l6_pathway' in l.lower()]
if remaining:
    print('STILL HAS L6:')
    for r in remaining:
        print('  ', r)
else:
    print('OK: engine.rs L6 fully removed')
