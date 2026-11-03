# Print a Swift file as a device build sees it: keep the #else branch of every
# `#if targetEnvironment(simulator)` block (or nothing), drop the simulator branch.
import sys
out, stack = [], []  # stack entries: (is_sim_block, in_sim_branch)
for line in open(sys.argv[1]):
    t = line.strip()
    if t.startswith('#if'):
        stack.append([t == '#if targetEnvironment(simulator)', True]); 
        if not stack[-1][0]: out.append(line)
        continue
    if t.startswith('#else') and stack:
        if stack[-1][0]: stack[-1][1] = False
        else: out.append(line)
        continue
    if t.startswith('#endif') and stack:
        sim = stack.pop()[0]
        if not sim: out.append(line)
        continue
    if any(s[0] and s[1] for s in stack): continue
    out.append(line)
sys.stdout.write(''.join(out))
