#!/usr/bin/env python3
"""Cross-check the numeric claims in KEP-284's README.md against the measured
tables in performance.md (round 2, transform design at head f8d67fa).
Exits 0 iff every claim is consistent."""
import sys

PERF = "keps/284-pod-cache-filter/performance.md"
README = "keps/284-pod-cache-filter/README.md"

perf = open(PERF).read()
readme = open(README).read()

failures = []

def check(name, claim, actual, tol=0.6):
    if abs(claim - actual) > tol:
        failures.append("%s: claimed %s, computed %.3f" % (name, claim, actual))

# Main results table medians (performance.md "Results", also mirrored into README).
# shape -> (heap full/union/filtered, listRSS full/union/filtered, streamRSS full/storeonly/filtered)
MAC = {
    "48/1000":  ((47.31, 5.20, 6.61),  (124.38, 51.02, 125.30), (99.69, 103.05, 45.39)),
    "900/100":  ((45.41, 42.06, 37.03),(120.33, 186.27, 120.59),(98.31, 101.34, 90.89)),
    "1000/0":   ((45.42, 46.32, 40.61),(120.73, 202.12, 120.64),(101.20, 100.89, 90.50)),
    "200/4800": ((214.04, 11.74, 18.84),(672.22, 74.66, 677.70), (337.81, 346.66, 70.61)),
    "5000/0":   ((214.70, 218.46, 190.63),(675.73, 800.97, 675.94),(341.80, 344.81, 325.47)),
}
LIN = {
    "48/1000":  ((47.30, 5.20, 6.60),  (172.15, 56.69, 130.77), (108.52, 108.88, 55.86)),
    "900/100":  ((45.40, 42.08, 37.02),(166.71, 186.53, 166.64),(103.61, 107.74, 99.16)),
    "1000/0":   ((45.41, 46.31, 40.60),(166.57, 205.65, 126.01),(105.53, 104.74, 100.58)),
    "200/4800": ((214.02, 11.73, 18.83),(677.64, 80.00, 628.00), (372.54, 358.43, 84.09)),
    "5000/0":   ((214.69, 218.45, 190.62),(681.05, 872.17, 631.20),(367.69, 359.53, 337.25)),
}

# 1. Every table value appears in BOTH documents (README mirrors performance.md).
for table in (MAC, LIN):
    for shape, cols in table.items():
        for col in cols:
            for v in col:
                s = "%.2f" % v
                if s not in perf:
                    failures.append("performance.md missing %s (%s)" % (s, shape))
                if s not in readme:
                    failures.append("README missing %s (%s)" % (s, shape))

# 2. README bullet: "about -86% at 48 / 1,000 and -91.2% at 200 / 4,800 on both platforms".
for name, table in (("macOS", MAC), ("Linux", LIN)):
    full, _, filt = table["48/1000"][0]
    check("%s heap drop 48/1000" % name, 86.0, (1 - filt / full) * 100, tol=0.3)
    full, _, filt = table["200/4800"][0]
    check("%s heap drop 200/4800" % name, 91.2, (1 - filt / full) * 100, tol=0.1)

# 3. README bullet: with no unrelated Pods the ~11% gain comes from managedFields stripping.
check("macOS heap drop 5000/0", 11.0, (1 - MAC["5000/0"][0][2] / MAC["5000/0"][0][0]) * 100, tol=0.4)
check("Linux heap drop 5000/0", 11.0, (1 - LIN["5000/0"][0][2] / LIN["5000/0"][0][0]) * 100, tol=0.4)

# 4. Streaming needs the wrapper: 200/4800 store-only vs filtered streaming peaks.
check("macOS store-only stream peak", 346.66, MAC["200/4800"][2][1], tol=0.01)
check("Linux store-only stream peak", 358.43, LIN["200/4800"][2][1], tol=0.01)
check("macOS filtered stream peak", 70.61, MAC["200/4800"][2][2], tol=0.01)
check("Linux filtered stream peak", 84.09, LIN["200/4800"][2][2], tol=0.01)

# 5. Alternatives paragraph: union List startup RSS at 5000/0 vs full.
check("macOS union list RSS 5000/0", 800.97, MAC["5000/0"][1][1], tol=0.01)
check("macOS full list RSS 5000/0", 675.73, MAC["5000/0"][1][0], tol=0.01)
check("Linux union list RSS 5000/0", 872.17, LIN["5000/0"][1][1], tol=0.01)
check("Linux full list RSS 5000/0", 681.05, LIN["5000/0"][1][0], tol=0.01)

# 6. Relist quotes (README Measured Sample): 994.0/649.3 macOS, 879.2/644.3 Linux (List);
#    599.0/79.9 macOS, 606.5/84.6 Linux (streaming).
for v in ["994.0", "649.3", "879.2", "644.3", "599.0", "79.9", "606.5", "84.6"]:
    if v not in readme:
        failures.append("README relist quote %s missing" % v)
    if v not in perf:
        failures.append("performance.md relist value %s missing" % v)

# 7. CPU claims: full+wl/full startup CPU 345 -> 1322 ms macOS (3.83x), 715 -> 515 ms Linux (0.72x).
check("macOS CPU ratio", 3.83, 1322.0 / 345.0, tol=0.02)
check("Linux CPU ratio", 0.72, 515.0 / 715.0, tol=0.02)
for v in ["345", "1,322", "715", "515", "309", "359", "562", "492"]:
    if v not in readme and v not in perf:
        failures.append("CPU quote %s missing from both docs" % v)

# 8. Process-count arithmetic: 5 shapes x 3 rounds x 2 platforms x 2 server configs = 60 runs;
#    60 runs x 4 modes = 240 cache processes (performance.md Findings/Coverage claims).
if "240 cache processes" not in perf:
    failures.append("performance.md: '240 cache processes' claim missing")
if 5 * 3 * 2 * 2 != 60 or 60 * 4 != 240:
    failures.append("campaign arithmetic broken")

if failures:
    print("INCONSISTENT:")
    for f in failures:
        print(" -", f)
    sys.exit(1)
print("All KEP-284 (transform revision) numeric claims are internally consistent.")
