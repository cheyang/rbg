#!/usr/bin/env bash
# R2-F14/F15/F16/F17 -- robustness of the new chart-RBAC generator,
# hack/gen-helm-rbac, added this round to fix F5 (chart ClusterRole drifting from
# config/rbac/role.yaml).
#
# The generator is now the SOLE source of the chart ClusterRole, and CI enforces
# that `make manifests` leaves no diff (.github/workflows/project-check.yml:43-46).
# That genuinely fixes F5's named failure mode. But it also means anything the
# generator drops or mis-gates is invisible: there is no second copy left to
# disagree with it, so there is no diff for CI to catch.
#
# This script feeds the generator hand-built role.yaml inputs in a throwaway
# directory and reports what it does with each. It never touches the repo's own
# config/rbac/role.yaml or the committed chart template.
#
# CONTRACT test: it must exit 0.
set -uo pipefail
ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
rc=0
GEN="$WORK/gen-helm-rbac"

go build -o "$GEN" ./hack/gen-helm-rbac || { echo "cannot build the generator"; exit 1; }
echo "=== R2-F14/F15/F16/F17: what does hack/gen-helm-rbac drop or mis-gate? ==="
go version

# probe <label> <role.yaml body> -- run the generator in an isolated copy of the
# tree layout it expects, and print the rules it emitted.
probe() {
  local label="$1" body="$2"
  local d="$WORK/$(echo "$label" | tr -cd '[:alnum:]')"
  mkdir -p "$d/config/rbac" "$d/deploy/helm/rbgs/templates/rbac"
  printf '%s\n' "$body" > "$d/config/rbac/role.yaml"
  # The generator resolves paths from the working directory.
  local out err code
  out="$(cd "$d" && "$GEN" 2>"$d/err")"; code=$?
  err="$(cat "$d/err")"
  printf '\n--- %s ---\n' "$label"
  printf '  exit: %s\n' "$code"
  [ -n "$err" ] && printf '  stderr: %s\n' "$(printf '%s' "$err" | head -2 | tr '\n' ' ')"
  if [ -f "$d/deploy/helm/rbgs/templates/rbac/clusterrole.yaml" ]; then
    printf '  emitted resources: %s\n' \
      "$(grep -oE '^\s+- [a-z/.-]+$' "$d/deploy/helm/rbgs/templates/rbac/clusterrole.yaml" \
         | tr -d ' -' | tr '\n' ' ')"
    printf '  emitted nonResourceURLs: %s\n' \
      "$(grep -c 'nonResourceURLs' "$d/deploy/helm/rbgs/templates/rbac/clusterrole.yaml")"
    printf '  gated blocks: %s\n' \
      "$(grep -c 'deprecatedWorkloadTypes\|deprecatedEnabled' "$d/deploy/helm/rbgs/templates/rbac/clusterrole.yaml")"
  else
    printf '  emitted: <no file written>\n'
  fi
  # export for the caller's assertions
  LAST_DIR="$d"; LAST_CODE="$code"
}

hdr='apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: controller-role
rules:'

# --- CONTROL: a plain input the generator is designed for -----------------------
probe "control-plain" "$hdr
- apiGroups: [\"\"]
  resources: [pods]
  verbs: [get]"
if [ "$LAST_CODE" -ne 0 ]; then
  echo "  HARNESS PROBLEM: the generator failed on a plain valid input, so every"
  echo "        result below is untrustworthy."
  rc=1
fi

# --- R2-F15: a nonResourceURLs-only rule ---------------------------------------
probe "R2-F15 nonResourceURLs-only rule" "$hdr
- nonResourceURLs: [/metrics, /healthz]
  verbs: [get]
- apiGroups: [\"\"]
  resources: [pods]
  verbs: [get]"
urls="$(grep -c 'nonResourceURLs' "$LAST_DIR/deploy/helm/rbgs/templates/rbac/clusterrole.yaml" 2>/dev/null | head -1)"
urls="${urls:-0}"
if [ "$LAST_CODE" -eq 0 ] && [ "$urls" -eq 0 ]; then
  echo "  R2-F15 REPRODUCED: the /metrics + /healthz rule was silently dropped and the"
  echo "        generator still exited 0. splitRules partitions on .Resources, so a"
  echo "        nonResourceURLs-only rule yields neither a kept nor a gated block."
  echo "        Latent today (no kubebuilder:rbac urls= marker in the tree), but the"
  echo "        generator is the only copy, so CI would stay green when it bites."
  rc=1
fi

# --- R2-F16: a multi-document role.yaml ----------------------------------------
probe "R2-F16 multi-document role.yaml" "$hdr
- apiGroups: [\"\"]
  resources: [pods]
  verbs: [get]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: second-role
rules:
- apiGroups: [apps]
  resources: [daemonsets]
  verbs: [get]"
if [ "$LAST_CODE" -eq 0 ] && \
   ! grep -q 'daemonsets' "$LAST_DIR/deploy/helm/rbgs/templates/rbac/clusterrole.yaml" 2>/dev/null; then
  echo "  R2-F16 REPRODUCED: the second document was silently lost (daemonsets absent),"
  echo "        exit 0. sigs.k8s.io/yaml.Unmarshal decodes only the first document."
  rc=1
fi

# --- R2-F14: a resource missing from the generator's deprecated list ------------
# The generator hardcodes its own copy of the deprecated-resource list
# (hack/gen-helm-rbac/main.go), separate from isDeprecatedWorkloadType in
# api/workloads/v1alpha2/rolebasedgroup_validation.go and the gated Owns() calls
# in internal/controller/workloads/rolebasedgroup_controller.go. Nothing
# cross-checks the three.
probe "R2-F14 unmapped resource beside a mapped one" "$hdr
- apiGroups: [apps]
  resources: [deployments, replicasets]
  verbs: [get]"
crf="$LAST_DIR/deploy/helm/rbgs/templates/rbac/clusterrole.yaml"
if [ "$LAST_CODE" -eq 0 ] && [ -f "$crf" ]; then
  # replicasets must NOT be inside the conditional; deployments must be.
  gated_line="$(grep -n '{{- if $deprecatedEnabled }}' "$crf" | head -1 | cut -d: -f1)"
  rs_line="$(grep -n 'replicasets' "$crf" | head -1 | cut -d: -f1)"
  if [ -n "$gated_line" ] && [ -n "$rs_line" ] && [ "$rs_line" -lt "$gated_line" ]; then
    echo "  R2-F14 REPRODUCED: 'replicasets' is emitted OUTSIDE the conditional (line"
    echo "        $rs_line, gate starts at $gated_line), i.e. granted even when the deprecated"
    echo "        types are disabled. The gate list is a third hand-maintained copy of"
    echo "        'which types are deprecated', and an entry missing from it FAILS OPEN"
    echo "        (over-grant) with no test and no CI signal."
    rc=1
  fi
fi

# --- R2-F17: non-strict unmarshal ----------------------------------------------
probe "R2-F17 misspelled key + aggregationRule + wrong kind" 'apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: controller-role
aggregationRule:
  clusterRoleSelectors:
  - matchLabels: {x: y}
rules:
- apiGroups: [""]
  resource: [pods]
  verbs: [get]'
if [ "$LAST_CODE" -eq 0 ]; then
  echo "  R2-F17 REPRODUCED: kind: Role (not ClusterRole) was accepted, aggregationRule"
  echo "        was parsed then never emitted, and the misspelled 'resource:' key made"
  echo "        that rule vanish -- all at exit 0. UnmarshalStrict plus an"
  echo "        apiVersion/kind assertion would close R2-F16 and R2-F17 together."
  rc=1
fi

# --- determinism (a genuine strength -- record it) ------------------------------
echo
echo "--- determinism: 3 runs against the repo's real config/rbac/role.yaml ---"
sums="$(for _ in 1 2 3; do
  d="$WORK/det$RANDOM"; mkdir -p "$d/config/rbac" "$d/deploy/helm/rbgs/templates/rbac"
  cp config/rbac/role.yaml "$d/config/rbac/role.yaml"
  (cd "$d" && "$GEN" >/dev/null 2>&1)
  md5sum < "$d/deploy/helm/rbgs/templates/rbac/clusterrole.yaml"
done | sort -u | wc -l)"
if [ "$sums" -eq 0 ]; then
  echo "  HARNESS PROBLEM: no output was produced, so determinism was not tested."; rc=1
elif [ "$sums" -eq 1 ]; then
  echo "  OK: byte-identical across 3 runs (no Go map-iteration nondeterminism)."
else
  echo "  R2 NEW: generator output is NOT deterministic ($sums distinct hashes)."; rc=1
fi

echo
if [ "$rc" -eq 0 ]; then
  echo "RESULT: generator robustness findings FIXED."
else
  echo "RESULT: generator robustness findings reproduced (see above)."
  echo "        Note these are about the generator being the SOLE source of truth:"
  echo "        F5's named failure mode (chart drifting from config/rbac) IS fixed and"
  echo "        is CI-gated. What is new is that a drop or a mis-gate now fails silently."
fi
exit "$rc"
