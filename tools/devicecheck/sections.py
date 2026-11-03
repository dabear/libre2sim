# Reads 0-based byte offsets on stdin and prints how many fall in each Mach-O section of argv[1].
import subprocess, sys, collections
secs, cur = [], {}
for l in subprocess.run(["otool", "-l", sys.argv[1]], capture_output=True, text=True).stdout.splitlines():
    p = l.split()
    if len(p) < 2:
        continue
    if p[0] in ("sectname", "segname", "offset", "size"):
        cur[p[0]] = p[1]
    if p[0] == "align" and "sectname" in cur:
        secs.append((int(cur["offset"]), int(cur["size"], 16), cur["segname"] + "," + cur["sectname"]))
count = collections.Counter()
for line in sys.stdin:
    o = int(line)
    count[next((n for s, z, n in secs if s <= o < s + z), "outside any section")] += 1
for name, n in count.items():
    print(f"{n} bytes in {name}")
print("identical" if not count else "")
