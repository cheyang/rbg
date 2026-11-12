# Verification harness — PR #570 (KEP-133: compact declarative discovery ConfigMap)

Reviewer: Codex (Reviewer B). PR: https://github.com/sgl-project/rbg/pull/570
Reviewed head: `91cac0738171a561882406a46a72f992a81f2e33` (base `c2896cc0d9ccb8b72de0dda4838f52d8807c9ff1`).

The PR is **documentation-only** (`keps/133-configmap-refine/README.md`), so the
claims under test are statements in the KEP, checked against the code they
describe. Two test polarities:

- **canary pins** (PASS today): record what the current code actually does —
  evidence for/against the KEP's description of the status quo. They flip when
  the implementation changes (then update/invert).
- **contract tests** (RED on the KEP as written): encode behavior the KEP's own
  text specifies (its render rule, its `base` template, its downgrade claim) and
  check it against ground truth. RED = reproduction of the design defect. They
  go green when the KEP text (and later the implementation) is corrected — for
  doc fixes, update the transcribed rule in `kep133RenderOrdinals` / `docBase`.

## P0 — premise (against base): CONFIRMED

The KEP's core problem is real: stored discovery content grows linearly with
replica count and crosses the 1 MiB ConfigMap hard limit at KEP-plausible scale.
`TestP0_Premise_StoredContentGrowsLinearlyWithReplicas` (real builder, fake
client): replicas=10 → 3,255 B; replicas=500 → 162,057 B; replicas=4000 (x5
pods = 20k entries) → 1,314,558 B > 1 MiB.

Nuance: the KEP's *secondary* claim ("one ConfigMap per role, content duplicated
across all roles") is **stale** — see F4.

## Observed vs expected

| ID | Claim (KEP-133 text) | Layer | Polarity | Observed | Expected (per KEP) | Verdict |
|---|---|---|---|---|---|---|
| P0 | content grows O(instances), crosses 1 MiB | L1 | contract | 4000x5 entries = 1.31 MB | grows / crosses limit | **Confirmed** |
| F1 | `excludedOrdinals` = KEP-499 semantics "verbatim"; renderer skips them | L1+L2 | contract | KEP rule renders [0,2] / [0] vs committed [0,2,4] / [0,1,2,4]; current code publishes committed members incl. ordinals >= replicas (envtest: {0,2,4} then {0,2,4,5} after scale-up) | rendered set == serving members | **Reproduced (KEP wrong)** |
| F2 | rendered file "field-by-field equal" to today's enumeration; `base` ends with `.{ns}.svc.cluster.local` | L1+L2 | contract | today's addresses are relative (`pod.s-svc`); doc template renders FQDN | equality | **Reproduced (KEP internally inconsistent)** |
| F3 | "older controllers ignore [the RBG-level CM] / will not overwrite" | L1+L2 | contract | current-main reconciler rewrites the object to the legacy schema on any RBG spec reconcile (fake-client + envtest after scale 1->2) | content preserved | **Reproduced (KEP wrong)** |
| F4 | "Today the controller creates one ConfigMap per role" | L1+L2 | canary pin | fresh RBG defaults to `refine`; exactly one CM named `<rbg>`; injector mounts it for stateful roles even in legacy mode | (KEP describes pre-#134 state) | **Stale description** |
| F7 | motivation example `group.size: 4` with 3 roles; `prefill.size: 2500` | L1 | canary pin | code: `group.size == len(roles)`; `roles[].size == len(instances)` (per-pod) | internally consistent example | **Inconsistent example** |

## Layout

- `pkg/discovery/kep133_review_test.go` — L1: P0, F1 (pin + contract), F2 (pin + contract), F7.
- `internal/controller/workloads/kep133_review_test.go` — L1: F3 (contract + pin), F4 (pin).
- `test/envtest/testcase/kep133discovery/` — L2: real controller stack vs real
  API server: F1 pin (committed membership published, incl. ordinal >=
  replicas), F2/F4 pin (single CM, relative addresses), F3 contract (content
  overwritten after a spec reconcile).
- `results/` — captured outputs (`unit-discovery.out`, `unit-workloads.out`,
  `integration-envtest.out`).

## How to run

```bash
# L1 (unit, fake client)
go test ./pkg/discovery/ -run 'TestP0_|TestF1_|TestF2_|TestF7_' -v
go test ./internal/controller/workloads/ -run 'TestF3_|TestF4_' -v

# L2 (integration, envtest; assets via setup-envtest)
KUBEBUILDER_ASSETS="$(setup-envtest use 1.31.0 -p path)" \
  go test ./test/envtest/testcase/kep133discovery/ -timeout 10m
```

Expected on the reviewed head: P0 + all canary pins PASS; the three contract
tests (F1 rule, F2 template, F3 preservation) FAIL — those failures ARE the
reproductions.

## Harness-bites check (done)

Temporarily applied the corrected semantics and confirmed every red contract
test flips green, then reverted:
- F1: render rule := KEP-499 committed membership (`ordinals.Select`) → green.
- F2: doc `base` template := relative form (`...s-pd-inference-decode`) → green.
- F3: skip the reconcile (KEP's claimed "older controller does not manage it") → green.
Production code was never touched (diff empty throughout).

## Continuing after the fix

The fix target for this PR is the KEP text. When the doc is corrected:
1. Update the transcriptions `kep133RenderOrdinals` / `docBase` (F1/F2) and the
   F3 setup to the new text, then re-run; contract tests should go green.
2. `bash docs/verification/kep133-compact-discovery-codex/scripts/re-verify.sh`
   re-runs L1+L2 against the current PR head and prints per-finding status.
3. L3 (live cluster) intentionally skipped per debate setup; the renderer init
   container does not exist yet, so there is nothing live to observe.

Copy-paste kickoff for a fresh machine:
```
git fetch https://github.com/cheyang/rbg.git verify/kep133-compact-discovery-codex
git checkout verify/kep133-compact-discovery-codex
bash docs/verification/kep133-compact-discovery-codex/scripts/re-verify.sh
```
