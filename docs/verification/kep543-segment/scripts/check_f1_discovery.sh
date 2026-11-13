#!/usr/bin/env bash
# F1 contract check (the reproduction): the KEP-543 design imposes a behavioral
# requirement on address discovery ("for an effective Segment, discovery must
# publish only the aligned base-group target; no residual or surge identities")
# but currently provides no mechanism/owner for it.
#
# Usage: check_f1_discovery.sh <repo-root>
# Polarity: CONTRACT — this script FAILS while the gap exists (current head)
# and PASSES once the design document names the discovery implementation
# entry point (config builder / RBG controller discovery step) AND carries
# integration- or e2e-level test-plan coverage for Segment-aware discovery.
set -u
ROOT="${1:-$(pwd)}"
DOC="$ROOT/keps/543-segment/README.md"
rc=0

echo "-- (a) mechanism/owner named in the design? --"
if grep -qE "config_builder|ConfigBuilder|pkg/discovery|discovery ConfigMap|reconcileDiscovery|discovery entry point|Implementation entry points.*discovery" "$DOC"; then
  echo "PASS design names a discovery mechanism/entry point"
else
  echo "FAIL design names no discovery mechanism/entry point (requirement stated in Non-Goals + Complete Segment Invariants has no owner)"
  rc=1
fi

echo "-- (b) integration/e2e test coverage for Segment-aware discovery? --"
# The integration/e2e test sections lie between the '#### Integration Tests' and
# '### Graduation Criteria' headings. Look for discovery mentions there.
coverage=$(awk '/^#### Integration Tests/,/^### Graduation Criteria/' "$DOC" | grep -ci "discovery\|address")
if [ "${coverage:-0}" -ge 1 ]; then
  echo "PASS integration/e2e test plan covers discovery"
else
  echo "FAIL integration/e2e test plan has no discovery coverage (only one unit-level bullet exists)"
  rc=1
fi

echo "-- (c) current code trace: discovery enumerates by RBG spec role replicas --"
if git -C "$ROOT" grep -q "memberOrdinals" HEAD -- pkg/discovery/config_builder.go 2>/dev/null \
   && git -C "$ROOT" grep -q "role.Replicas" HEAD -- pkg/discovery/config_builder.go 2>/dev/null; then
  echo "PASS (evidence) discovery today enumerates [0, spec.replicas) from RBG spec roles — residual ordinals of an unaligned Segment would be published"
else
  echo "NOTE discovery code shape changed — re-inspect pkg/discovery/config_builder.go (Harness-update)"
fi

exit $rc
