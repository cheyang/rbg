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

Every finding below was re-derived against `f8f2a59f`. The change is a genuine improvement to
the API surface, and it resolves most of F4. It does not change F2 or F5 — the two findings that
affect the mainstream `RoleInstanceSet` path — because those follow from the *effective* default,
wherever that default lives. It also leaves the CEL rule structurally unable to guard a
controller-side default (F1b), since CEL only ever sees the stored object, where the field is
absent.

---

## Observed vs. expected

| # | Finding | Severity | Layers | Result | Raised by |
|---|---------|----------|--------|--------|-----------|
| **F2** | Existing `RoleInstanceSet` roles that never set the field silently lose worker endpoints on upgrade; the stored object shows nothing | `major` | L1 | **CONFIRMED** | here |
| **F5** | Roles that already set `All` get their worker pods replaced by the controller upgrade alone | `major` | L1 | **CONFIRMED** | @NoobDream2568 |
| **F1** | StatefulSet + `leaderWorkerPattern`, policy unset → selector narrowed to `component-name=leader`, which no StatefulSet pod carries → zero endpoints, NXDOMAIN | `minor` **(TODO)** | L1+L2+L3 | **CONFIRMED**, accepted on a deprecating path | here |
| **F1b** | CEL rejects an *explicit* `LeaderOnly` on that role while the controller *applies* it via the default — CEL structurally cannot see a controller-side default | `minor` **(TODO)** | L1+L2 | **CONFIRMED** | here |
| **F3** | Reverse `All → LeaderOnly` untested; the replacement guarantee lives only in the pod-level check | `minor` | L1 | **CONFIRMED** (coverage gap, not a defect) | Copilot |
| **F7** | KEP still documents the CRD default that `f8f2a59f` removed, and no longer documents the CEL rule it restored | `minor` | — | **CONFIRMED** by inspection | @NoobDream2568 |
| **F6** | No test covers StatefulSet + `leaderWorkerPattern` | `minor` | L2 | **CONFIRMED** | here |
| **F4** | `LeaderOnly` silently inert on LWS roles | `minor` | — | **partially superseded** by `f8f2a59f` | here |

The PR's own unit and envtest suites are green, and so is its CI (including e2e) on `f8f2a59f`
— see `results/sweep.txt`. Every finding here sits in a gap those tests do not cover.

**All harness tests are currently green**, because every one of them is a *canary* recording
present behaviour. Green therefore means "behaviour unchanged", not "nothing wrong"; a test going
red means the behaviour it pins moved. See the polarity table at the bottom.

---

## F1 in detail — real, but on a deprecating path

**Disposition: TODO, not a blocker.** The defect is real and reproduces at all three layers,
but the only way to reach it is to hand-write the deprecated `role-workload-type:
apps/v1/StatefulSet` annotation on a v1alpha2 role that *also* uses a `leaderWorkerPattern`.
The v1alpha1 conversion cannot produce that shape — `rolebasedgroup_conversion.go` builds a
`LeaderWorkerPattern` only when `src.LeaderWorkerSet != nil`, and StatefulSet falls through to
`StandalonePattern` — and the conversion code itself says *"New v1alpha2 RBGs should NOT set
this annotation"*. `keps/workload-compatibility-mode/README.md` deprecates the `workload` field
outright. The evidence below stands; only the priority changed.

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

Each assertion was run unchanged against **both** commits. Every one flips, which is what
distinguishes "introduced here" from "pre-existing quirk":

| observation | base `8a54787d` | head `f8f2a59f` |
|---|---|---|
| `F1` selector, StatefulSet role | `{group, role}` | `{group, role, component-name=leader}` |
| `F2` selector, RoleInstanceSet role, policy unset | `{group, role}` | `{group, role, component-name=leader}` |
| `F5` worker `serviceName` under `All` | `""` | `s-pr418-…` |

(`results/l1-unit-base.txt` shows these as *failures* at base — the tests assert the head
behaviour, so a failure there is the flip.)

### Harness-bites check

Temporarily scoping the selector to the supported workload type —

```go
if role.IsLeaderWorkerPattern() &&
    role.GetWorkloadType() == constants.RoleInstanceSetWorkloadType &&
    role.LeaderWorkerPattern.GetSharedServiceSelection() == ...LeaderOnly {
```

— **flipped the F1 assertion** (which was still a contract test at that point) while leaving the
F2/F5 canaries untouched. The fix was then reverted and the production diff confirmed empty. That
is what rules out a test that is red for an unrelated reason: the assertion responds to exactly
the line it is about.

---

## Suggested direction (not prescriptive)

### Reviewer position: keep `LeaderOnly` as the default, but make it visible

The KEP's own motivation is the strongest argument for the new default, and the PR undersells it:

> for large-scale inference engines where only leader Pods accept external requests, routing
> traffic to all Pods causes request failures. … worker Pods may only run a dummy API server,
> and exposing those Pods through the role-level Service causes requests to be routed to
> non-functional endpoints.

So the pre-PR default is not a neutral old behaviour — it routes requests at dummy API servers.
`LeaderOnly` is corrective, and all six `leaderWorkerPattern` examples in the repo leave the
field unset, so the intended topology is exactly the one that was mis-served. That reframes F2:
the population helped is larger than the population hurt. Who is still hurt is anyone using the
role-level headless Service for peer discovery over every Pod IP, and they now need an explicit
`All`.

**The tension that comes with choosing `LeaderOnly`:** a discoverable default and the restored
CEL rule are mutually exclusive. Defaulting runs before validation, so a CRD default writes
`LeaderOnly` onto every LWS/StatefulSet role and CEL then rejects it. `f8f2a59f` resolved that by
keeping the rule and moving the default into the controller — which produces the worst
combination for F2: the behaviour changes, and nothing in the stored object records it. No
`kubectl get -o yaml` evidence, nothing to diff, nothing to audit.

**Suggested resolution** (not raised in the PR discussion so far): CRD default **+** drop the CEL
rule **+** add a workload-type scope check in the controller.

```go
if role.IsLeaderWorkerPattern() &&
    role.GetWorkloadType() == constants.RoleInstanceSetWorkloadType &&
    role.LeaderWorkerPattern.GetSharedServiceSelection() == ...LeaderOnly {
```

The CEL rule is the weaker of the two, and F1b is the evidence: it blocks only the explicit
spelling of a policy the controller applies anyway through the unset path, so it is ceremony
rather than protection. The scope check is what actually enforces the invariant, and it fixes F1
in passing.

This is **not** a return to `4811ab04`. That commit dropped the rule *without* the scope check,
which is precisely what produced F1. Dropping it *with* the check is a different proposal.

Residual cost: `LeaderOnly` becomes writable on LWS roles where it is inert. A documentation line
covers it; a status condition exposing the effective policy would be more rigorous but probably
not worth the surface.

### Per-finding direction

- **F2:** keep the `LeaderOnly` default, but restore a CRD default so the choice is recorded on
  the object, and ship an upgrade note. The ask is discoverability, not reversion.
- **F1/F1b:** promoted from "TODO" to the *enabling change* for the above — the scope check is
  what makes a CRD default safe.
- **F5:** accepted as a corrective rollout. Worth noting in the release notes that it is bounded
  by the role's rollout strategy (`limitUpdateIndexes`, `statelessmode/sync/update.go`, and the
  equivalent budget in `statefulmode`), so it is a rolling replacement rather than a mass restart.
- **F3/F6:** add the reverse transition and a StatefulSet case to the PR's own tests.
- **F7:** re-sync the KEP — **but note the direction depends on the decision above.** If the CRD
  default returns, the `+kubebuilder:default` marker and the "the API server defaults it" line in
  the KEP become correct again, and what needs deleting is instead the "deliberately not a CRD
  default" comment `f8f2a59f` added to `rolebasedgroup_types.go`. The `### Validation` section
  should only come back if the CEL rule stays. Settling the default question first avoids making
  the author revise the KEP twice.

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
| `F1_StatefulSetLeaderWorkerSelector` | canary | stays green while accepted; **flips** if the narrowing is scoped — invert or retire it then |
| `F6` envtest (`PR418 F1`) | canary | same as `F1` |
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
