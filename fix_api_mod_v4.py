#!/usr/bin/env python3
"""Remove remaining L6 lines from api/mod.rs."""

path = '/Volumes/zt_hd/projects/meow/memhop/src/api/mod.rs'
with open(path, 'r') as f:
    lines = f.readlines()

# Current remaining lines (from previous script output):
# 103: pathways field
# 129: let mut pathways
# 150: L6 warning string (part of tracing::warn! macro)
# 255: pathways in constructor

lines_to_delete = {102, 128, 149, 254}

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
