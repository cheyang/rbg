#!/usr/bin/env python3
"""Cross-check the numeric claims made in KEP-284's README.md against the
measured table in performance.md. Exits 0 iff every claim is consistent."""
import re
import sys

PERF = "keps/284-pod-cache-filter/performance.md"
README = "keps/284-pod-cache-filter/README.md"

perf = open(PERF).read()
readme = open(README).read()

failures = []

def check(name, claim, actual, tol=0.6):
    if abs(claim - actual) > tol:
        failures.append("%s: claimed %s, computed %.2f" % (name, claim, actual))

# Medians transcribed from performance.md results table (union-serial vs full).
full =  {"48/1000": (46.91, 165.08), "900/100": (45.01, 159.81), "1000/0": (45.02, 159.92)}
union = {"48/1000": (4.82, 47.03),  "900/100": (41.71, 192.31), "1000/0": (45.94, 208.34)}

# README: "retained heap falls about 10x" at 95.4% unrelated
check("heap ratio 48/1000", 10.0, full["48/1000"][0] / union["48/1000"][0], tol=0.5)
# performance.md summary: -89.7% heap, -71.5% RSS at 48/1000
check("heap drop % 48/1000", 89.7, (1 - union["48/1000"][0] / full["48/1000"][0]) * 100)
check("rss drop % 48/1000", 71.5, (1 - union["48/1000"][1] / full["48/1000"][1]) * 100)
# performance.md: 900/100 heap -7.3%, RSS +20.3%; 1000/0 heap +2.1%, RSS +30.3%
check("heap drop % 900/100", 7.3, (1 - union["900/100"][0] / full["900/100"][0]) * 100)
check("rss rise % 900/100", 20.3, (union["900/100"][1] / full["900/100"][1] - 1) * 100)
check("heap rise % 1000/0", 2.1, (union["1000/0"][0] / full["1000/0"][0] - 1) * 100)
check("rss rise % 1000/0", 30.3, (union["1000/0"][1] / full["1000/0"][1] - 1) * 100)
# README: "95.4% unrelated Pods" = 1000 / 1048
check("unrelated share %", 95.4, 1000 / 1048 * 100, tol=0.05)

# The medians quoted in README's Measured Sample table must appear in performance.md.
for row in ["46.91", "4.82", "165.08", "47.03", "45.01", "41.71", "159.81", "192.31",
            "45.02", "45.94", "159.92", "208.34", "115.58", "123.70"]:
    if row not in readme:
        failures.append("README measured-sample value %s not found" % row)
    if row not in perf:
        failures.append("performance.md value %s not found" % row)

# README claims union peak RSS ranges; check they appear in performance.md per-run table.
for rng in ["189.08", "197.84", "207.03", "215.75", "159.72", "160.06", "159.81", "160.03"]:
    if rng not in perf:
        failures.append("performance.md per-run value %s missing" % rng)

if failures:
    print("INCONSISTENT:")
    for f in failures:
        print(" -", f)
    sys.exit(1)
print("All KEP-284 numeric claims are internally consistent.")
