# Verification — PR #418 `sharedServiceSelection`

Reviewer-private evidence for <https://github.com/sgl-project/rbg/pull/418>
("fix: worker pods get no DNS identity under sharedServiceSelection=All").

**Reviewed head:** `f8f2a59f` · **Base:** `8a54787d` · **Round 1**

Production code on this branch is **untouched**. The only additions are the `pr418_*_test.go`
harness files and this directory.

---

## Heads-up: the design changed mid-review

The author pushed `f8f2a59f` ("Resume leaderworkerpattern cel validation") while this review was
in progress, reversing the earlier approach:

| | `4811ab04` (first reviewed) | `f8f2a59f` (current) |
|---|---|---|
| `+kubebuilder:default=LeaderOnly` | present | **removed** |
| CEL rule restricting `LeaderOnly` to `RoleInstanceSet` | **deleted** | **restored** |
| where the default lives | API server (stored on the object) | controller (`GetSharedServiceSelection`) |

Every finding below was re-derived against `f8f2a59f`. The change is an improvement to the API
surface, but it does **not** fix the main problem — it makes the guard *unreachable* on the path
that actually matters, because CEL only ever sees the stored object, where the field is absent.

---

## Observed vs. expected

| # | Finding | Severity | Layers | Result |
|---|---------|----------|--------|--------|
| **F1** | StatefulSet + `leaderWorkerPattern`, policy unset → Service selector narrowed to `component-name=leader`, which no StatefulSet pod carries → **zero endpoints, NXDOMAIN** | `blocker` | L1 + L2 + L3 | **CONFIRMED** |
| **F1b** | CEL rejects an *explicit* `LeaderOnly` on that role while the controller *applies* it via the default — the guard cannot fire | `major` | L1 + L2 | **CONFIRMED** |
| **F2** | Existing `RoleInstanceSet` roles that never set the field silently lose worker endpoints on upgrade; the stored object shows nothing | `major` | L1 | **CONFIRMED** |
| **F5** | Roles that already set `All` get their worker pods replaced by the controller upgrade alone | `major` | L1 | **CONFIRMED** |
| **F3** | Reverse `All → LeaderOnly` untested; the replacement guarantee lives in only one of two layers | `minor` | L1 | **CONFIRMED** (coverage gap, not a defect) |
| **F4** | `LeaderOnly` silently inert on LWS roles | `minor` | — | **partially superseded** by `f8f2a59f` |
| **F6** | No test covers StatefulSet + `leaderWorkerPattern` | `minor` | L2 | **CONFIRMED** |

The PR's own tests are green (`results/sweep.txt`). The only red is this harness.

---

## F1 in detail — the load-bearing finding

The chain, each link independently verified:

1. `sts_reconciler.go:83` reconciles an RBG-managed headless Service for StatefulSet roles.
2. `svc_reconciler.go:110-113` narrows the selector whenever the role has a
   `leaderWorkerPattern` **and** the effective policy is `LeaderOnly` — it never checks the
   workload type.
3. `GetSharedServiceSelection()` returns `LeaderOnly` for an unset field.
4. `ComponentNameLabelKey` is written only on the RoleInstanceSet path
   (`roleinstance/utils/instance_utils.go:93`). StatefulSet pods never carry it.
5. ⇒ the selector matches nothing.

**Why the restored CEL rule does not save it:** the rule fires on
`has(self.leaderWorkerPattern.sharedServiceSelection)`. On the defaulted path the field is
absent, so the rule short-circuits to *valid*. The API refuses to let a user *ask* for
`LeaderOnly` here, and the controller then applies `LeaderOnly` anyway.

### Live evidence (`results/l3-live-f1.txt`)

Real ACK cluster, k8s v1.36.1, both pods `Ready`:

```
--- pod labels: does any pod carry component-name? ---
POD                  COMPONENT   IP
pr418-sts-worker-0   <none>      10.39.55.165
pr418-sts-worker-1   <none>      10.39.55.166

ARM A  selector from the INSTALLED (pre-PR) controller
       {group-name, role-name}                                    -> 2 endpoint addresses
ARM B  selector PR#418 generates for the SAME role
       {group-name, role-name, component-name=leader}             -> 0 endpoint addresses

pr418-head-selector -> NXDOMAIN / no address
s-pr418-sts-worker  -> 10.39.55.166, 10.39.55.165
```

### Regression proof (`results/l1-unit-base.txt`)

The identical contract test **passes at base** and **fails at head**:

| | base `8a54787d` | head `f8f2a59f` |
|---|---|---|
| `F1` selector | `{group, role}` → **PASS** | `{group, role, component-name=leader}` → **FAIL** |
| `F2` canary | not narrowed → FAIL | narrowed → PASS |
| `F5` worker `serviceName` under `All` | `""` → FAIL | `s-…` → PASS |

So F1 is introduced by this PR, and F2/F5 are genuine behaviour changes rather than
pre-existing quirks.

### Harness-bites check

Temporarily scoping the selector to the supported workload type —

```go
if role.IsLeaderWorkerPattern() &&
    role.GetWorkloadType() == constants.RoleInstanceSetWorkloadType &&
    role.LeaderWorkerPattern.GetSharedServiceSelection() == ...LeaderOnly {
```

— turns **F1 green** while leaving the F2/F5 canaries green, then the fix was reverted and the
production diff confirmed empty. So the test exercises the real path.

---

## Suggested direction (not prescriptive)

- **F1:** gate the narrowing on the workload type, so the controller and the CEL rule agree on
  the supported scope. Note the same reasoning applies to any future workload type whose pods
  lack component labels.
- **F2/F5:** state the upgrade consequences in the KEP and a release note — worker endpoints
  disappear for unset roles, and worker pods restart for `All` roles. Neither requires a user
  action to trigger, which is what makes them worth calling out.
- **F3/F6:** add the reverse transition and a StatefulSet case to the PR's own tests.

---

## How to run

```bash
# L1 — deterministic, no dependencies
go test ./pkg/reconciler/ ./pkg/reconciler/roleinstance/inplaceupdate/ \
        ./pkg/inplace/instance/inplaceupdate/ -run 'TestVerifyPR418' -count=1 -v

# L2 — real API server + real controller
KUBEBUILDER_ASSETS=$(setup-envtest use 1.31.0 -p path) \
  go test ./test/envtest/testcase/rbg/ -count=1 -timeout 20m -args -ginkgo.focus='PR418 F1'

# L3 — real cluster ($KUBECONFIG honoured; KEEP=1 keeps the namespace)
bash docs/verification/pr418-shared-service-selection/scripts/30-live-f1-endpoints.sh
```

`scripts/re-verify.sh` runs L1 + L2 against the current PR head and prints
Fixed / Still-broken / Partial / Harness-update per finding, honouring polarity.

---

## Continuing after the fix

Polarity matters — "all green" is **not** the same as "fixed":

| test | polarity | after a correct fix |
|---|---|---|
| `F1_StatefulSetLeaderWorkerSelector` | contract | goes **green** |
| `F6` envtest (`PR418 F1`) | contract | goes **green** |
| `F1b_ExplicitLeaderOnly…` | canary | **stays red** under a selector-only fix (by design); flips only if the helper stops claiming `LeaderOnly` for forbidden workload types |
| `F2_UnsetPolicyNarrows…` | canary | stays green while `LeaderOnly` is the default; **invert** if the default is reverted |
| `F5_WorkerServiceNameUnderAll` | canary | stays green (intended behaviour); retire once the rollout is documented |
| `F5b_ServiceNameChangeForcesRecreate` | canary | stays green; keep as a regression guard |
| `F3_InstanceLayerIgnoresServiceName` | canary | stays green until the RoleInstanceSet layer also considers `serviceName` |

### Kickoff prompt for a fresh session

> Continue the review pipeline for https://github.com/sgl-project/rbg/pull/418. The
> verification branch `verify/pr418-shared-service-selection` on my fork holds the harness.
> Read `docs/verification/pr418-shared-service-selection/README.md` and `verify-manifest.json`,
> then run `scripts/re-verify.sh` (no sha needed — it resolves the current PR head from the
> manifest and the delta start from `.last-reviewed`). Report Fixed / Still-broken / Partial
> per finding, honouring polarity, then review the `last-reviewed..head` delta for new issues.
> A Linux sandbox with a live cluster is at `root@43.99.38.217` (kubeconfig `~/.kube/config`).

### What does not travel between machines

Envtest assets (re-downloaded per machine), `gh`/kubeconfig credentials, and the live cluster's
state. L1/L2 verdicts are deterministic; **L3 needs a pre-PR controller** to keep arm A
meaningful as a baseline.
