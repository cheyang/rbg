# Round 2 — audit of `hack/gen-helm-rbac` (new in this PR)

Reviewed head `dcc7104aef6f6e8c16cf4342bb25baf9426c68ab`.

Round 1's **F5** was: `make manifests` no longer syncs the chart ClusterRole, and the only
remaining protection is an `@echo` asking a human to do it by hand. Round 2 replaces that
`@echo` with a real generator (`hack/gen-helm-rbac/main.go`, 208 lines) wired into
`make manifests`, which regenerates
`deploy/helm/rbgs/templates/rbac/clusterrole.yaml` from `config/rbac/role.yaml`.

**F5 verdict: FIXED, and now CI-enforced.** `.github/workflows/project-check.yml` already runs
`make manifests` and fails on any `git status --porcelain` output, so chart RBAC drift is now
caught by the existing gate. That is the substantive win of this PR.

## Faithfulness — clean

Both sides normalised into `(apiGroup, resource, verb, resourceNames)` tuples, with a separate
channel for `nonResourceURLs`.

| Comparison | Result |
|---|---|
| enabled render vs `config/rbac/role.yaml` | **209 == 209, symmetric difference empty** |
| chart default render (no `--set`) vs `config/rbac/role.yaml` | identical (so "unset means enabled" is consistent) |
| enabled render vs disabled render | 209 → 177, difference **exactly 32 tuples** |

All 32 removed tuples are `apps/deployments{,/status,/finalizers}`,
`apps/statefulsets{,/status,/finalizers}`, `leaderworkerset.x-k8s.io/leaderworkersets{,/status}`.
No over-removal, no under-removal. `apps/controllerrevisions` correctly stays unconditional.

Also verified clean:

* **Mixed-resource rules split correctly.** This is live in the real input — controller-gen
  emits `apps: [controllerrevisions, deployments, statefulsets]` as one rule and the generator
  splits it into an ungated `controllerrevisions` rule plus a gated `deployments, statefulsets`
  rule. An interleaved synthetic variant `[deployments, controllerrevisions, statefulsets,
  daemonsets]` also split correctly.
* **Deterministic** — 6 consecutive runs byte-identical.
* **Ordering-independent** — reversing all 17 input rules produced identical tuple sets.
* **`resourceNames` preserved** and correctly duplicated onto both halves of a split rule.
* **Mixed apiGroups** (`[apps, extensions]` + `deployments`) rejected with an actionable error,
  no file written.
* **Loud on bad input** — missing file, malformed YAML, empty `rules`, wrong `kind` all exit 1,
  and the write happens only after render succeeds, so a failure leaves the existing template
  byte-identical.
* **Committed file is in sync** with generator output.
* Both renders parse (12 documents each) and are accepted by a real k8s v1.36.1 API server via
  `kubectl apply --dry-run=server`, after renaming the object to `pr416audit-…` so the live
  `rbgs-controller-role` on the shared cluster was never touched (confirmed afterwards:
  resourceVersion 33347747, creationTimestamp 2026-07-22, unchanged).

## G2 — one reproduced defect: rules with no `resources` are silently dropped

`splitRules` iterates `rule.Resources`. If that slice is empty, neither the kept nor the gated
bucket is populated and the rule is never appended to `blocks`. There is no post-condition
check that the output rule count matches the input rule count.

A `//+kubebuilder:rbac:urls=/metrics,verbs=get` marker produces exactly this shape:

```yaml
rules:
- apiGroups: [""]
  resources: [pods]
  verbs: [get]
- nonResourceURLs: [/metrics, /healthz]
  verbs: [get]
```

Observed: exit 0, only the `pods` rule emitted. The `nonResourceURLs` rule is gone, no warning.

Degenerate case — `sigs.k8s.io/yaml` unmarshal is non-strict, so a typo'd field name is dropped
silently:

```yaml
rules:
- apiGroups: [apps]
  resourcess: [deployments]   # note the typo
  verbs: [get]
```

Observed: exit 0, output file ends at `rules:` with nothing after it. `helm template` on that
chart renders `rules: None` — a **completely permission-less controller**, silently, with
`make manifests` succeeding. The existing `len(role.Rules) == 0` guard does not catch it,
because the rules do exist in the input and are lost during the split.

Severity: **non-blocking** — not reachable with this project's current markers. But a generator
whose stated purpose is to prevent silent RBAC drift should not itself have a silent-drop path,
and the guard is three lines: reject rules with both `Resources` and `NonResourceURLs` empty,
pass `nonResourceURLs`-only rules through ungated, and assert `len(blocks) >= len(rules)` before
writing.

## Theoretical, not currently reachable

* **Wildcards leak.** `apps: ["*"]` and `"*": [deployments]` both pass the deprecated check
  (exact string match on group and base resource) and are emitted unconditionally. Output
  reproduced, but controller-gen does not emit wildcards for this project's markers.
* **Multi-document `config/rbac/role.yaml`**: only the first document is consumed, silently.
  controller-gen emits one document.
* **Not atomic** (`os.WriteFile` truncates in place), but all validation precedes the write, so
  only an I/O error mid-write could truncate. Low risk.
