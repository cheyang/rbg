# pr532-host-port-scheduling — verification

Reproducible evidence for the review of [PR #532](https://github.com/sgl-project/rbg/pull/532)
(`feat(port-allocator): expose host-network ports to the scheduler`).

Harness lives on branch `verify/pr532-host-port-scheduling-claude` (pushed to the
reviewer's fork `cheyang/rbg`). **Production code is untouched** — the diff is
test files + this directory only.

Code under review: PR head `c05052c28c9e6cd5e0aab6362172c4ac651510ae`
(frozen base `7894133d5b59d44f2a19dcf6b75c531996285bcf`; merge-base `d162208e`).

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `InjectPortsIntoPod` / `injectContainerPort` output shape + real kube-apiserver validation of the injected specs (`legacyscheme` conversion + `pkg/apis/core/validation`) | `go test ./pkg/port-allocator/ -run 'TestVerify' -v` |
| 2. Integration (envtest) | the real chain RoleBasedGroup → RoleInstanceSet (RoleScoped allocation) → RoleInstance → pod **created through a real kube-apiserver** | `KUBEBUILDER_ASSETS=$(setup-envtest use 1.34.1 -p path) go test ./test/envtest/testcase/portallocator/... -v` |
| 3. Live | NOT run this round (needs a real kube-scheduler + ≥2 nodes; see `liveNote` in the manifest) | — |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code / PASS
> when fixed. Bug-canary tests (assert current observed behavior) PASS now / FLIP to
> red when the behavior changes. `TestVerifyCanaryLegacyPodWithoutDeclarationIsInvisibleToNodePorts`
> is the only canary here.

## Problem premise (P0)

Run against the **base** branch (7894133), without the patch, because the question
is whether the problem exists today.

| | |
|---|---|
| Claimed symptom (PR body, quoted) | "The port allocator can randomly assign the same port to different pods. When a pod uses `hostNetwork: true`, those ports occupy the node's network namespace, so two pods with the same allocated port must not be scheduled on the same node." |
| Linked issue | none ("Does this pull request fix one issue? NONE") |
| Reported component | kube-scheduler placement of host-network pods given rbg's port allocator (`pkg/port-allocator`, `pkg/reconciler/roleinstance/sync`) |
| Patched component | `pkg/port-allocator` (manager/parser/type) + e2e + docs — same path |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | L1: `results/base-premise.txt` — on base, `InjectPortsIntoPod` leaves a host-network pod with **zero scheduler-visible host ports** (NodePorts only filters on `hostPort > 0`). L2: `results/l2-envtest-base.txt` — the full chain on base creates the worker pod with the shared annotation (port 30012) but **no hostPort declaration**, i.e. the scheduler is blind to the collision. Also `RandomAllocator`'s own doc comment states uniqueness is NOT guaranteed across calls/instances. |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | base leaves host-network pods invisible to NodePorts | 1+2 | Confirmed (base) / fixed on head | `results/base-premise.txt`, `results/l2-envtest-base.txt` vs `results/l1-unit.txt`, `results/l2-envtest.txt` |
| F1 | injected pod specs pass real kube-apiserver create-time validation (duplicate host ports incl. cross-container, hostNetwork hostPort==containerPort, protocols, defaulting) | 1 | Verified compliant — no bug found | `results/l1-unit.txt` (`TestVerifyInjectedPodSpecsPassRealAPIServerValidation`, `TestVerifyControlApIServerHostNetworkPortSemantics`) |
| F2 | re-injecting the same allocations is idempotent and stays API-valid | 1 | Verified compliant — no bug found | `results/l1-unit.txt` (`TestVerifyInjectionIsIdempotentAcrossReinjection`) |
| F3 | upgrade gap: pods created before the change never declare hostPort; NodePorts cannot protect them; docs do not say so | 1 | Open (minor, docs) — canary documents the gap | `results/l1-unit.txt` (`TestVerifyCanaryLegacyPodWithoutDeclarationIsInvisibleToNodePorts`) |
| V4 | full chain declares containerPort==hostPort==allocated RoleScoped port, two pods share one value, via real apiserver | 2 | Verified | `results/l2-envtest.txt` |
| V5 | non-host-network pods declare containerPort with hostPort==0 | 1+2 | Verified | `results/l1-unit.txt`, `results/l2-envtest.txt` |

## Per-finding detail

### F1 — apiserver compliance of injected specs (potential blocker, refuted)

Mechanism under test: `injectContainerPort` (pkg/port-allocator/manager.go) appends
`{containerPort, hostPort, protocol}` to `pod.Spec.Containers[0].Ports` (or fills the
hostPort of a matching existing declaration). Two upstream rules could reject the
resulting pod:

1. *duplicate host ports within one pod* — `AccumulateUniqueHostPorts` keys on
   `protocol/hostIP/hostPort` **across all containers** (k8s
   `pkg/apis/core/validation/validation.go`);
2. *hostNetwork pods require hostPort == containerPort* — `validatePodHostNetworkDeps`
   with `ResourceIsPod`.

The test converts the injected pod through the repo's vendored `legacyscheme`
(v1 → internal), applies the same versioned defaulting the apiserver does, and runs
the real `ValidatePodCreate`. Scenarios: single/multiple TCP, UDP, SCTP, two
same-protocol allocations that collided on the same random port, same port with
different protocols, user pre-declared matching port, later-container declaration,
non-host-network pod with a user hostPort. **All pass.** The cross-container reuse
loop added in commit `c05052c` is load-bearing: mutating it away makes the apiserver
reject the pod with `Duplicate value: "TCP//30000"` (see `results/mutations.txt`).

Control (`TestVerifyControlApIServerHostNetworkPortSemantics`): kube-apiserver
already defaults `hostPort = containerPort` for hostNetwork pods
(`pkg/apis/core/v1/defaults.go`, `defaultHostNetworkPorts`), so the PR's explicit
`hostPort = containerPort` matches upstream semantics rather than inventing new
ones; a user declaration with an explicitly mismatched hostPort is rejected with or
without injection (pre-existing user error, not PR-attributable).

### F2 — idempotency (potential blocker, refuted)

Mechanism under test: nothing persists the partially-injected pod, but a re-render
plus re-inject must not duplicate declarations. Injecting three times yields exactly
one declaration and a still-valid pod.

### F3 — upgrade gap (open, minor, docs)

Mechanism: `InjectPortsIntoPod` is called from exactly one place —
`pkg/reconciler/roleinstance/sync/instance_scale.go:275` (`createPods`), i.e. only at
pod **creation**; pods are immutable, so pods created by the pre-PR controller never
gain a hostPort declaration. After an upgrade, the scheduler can still co-locate a
new (declaring) pod onto a node where a legacy pod of the same component already
binds the shared RoleScoped port. The canary test demonstrates exactly that: a
legacy-shaped pod exposes zero host ports, and the new pod "fits" the node. The PR
docs (en+zh) state the NodePorts guarantee without stating it applies only to pods
created after the controller upgrade. Suggested remedy: one doc paragraph (release
note) telling operators to roll/recreate existing host-network pods of affected
components, or nothing at all if the project accepts the limitation knowingly.

## Harness-bites check

`results/mutations.txt` — three temporary production-code mutations, each detected
by the harness, then reverted (production diff empty):

1. drop `hostPort` for hostNetwork pods → P0 FAILs;
2. `hostPort = port + 1` → real apiserver validation error `must match containerPort
   when hostNetwork is true`;
3. disable the reuse loop → real apiserver `Duplicate value: "TCP//30000"`.

## Live run notes

Not run. The spread-across-nodes contract (the PR's own e2e
`should spread host-network pods sharing a role-scoped host port`) needs a real
kube-scheduler and ≥2 schedulable nodes; envtest has no scheduler. If run live,
check: both host-network pods declare the same hostPort and land on different
NodeNames.

## Proposed fixes (NOT applied to production here)

- **F3 (minor, docs)**: add to both
  `doc/best-practice/en|zh/05-port-allocation-and-service-discovery.md` (Host Port
  Scheduling section): the NodePorts guarantee applies only to pods created after
  the controller runs the new injection — recreate/roll existing host-network pods
  of components with allocated ports, otherwise legacy pods remain invisible to the
  scheduler; and (optional) note that a host-network component's size cannot exceed
  the number of schedulable nodes, since surplus pods stay Pending.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/pr532-host-port-scheduling-claude` (production code
untouched), so it grafts onto whatever the fixed code is.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/pr532-host-port-scheduling-claude
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- pkg/port-allocator/verify_hostport_scheduling_test.go \
       test/envtest/testcase/portallocator docs/verification/pr532-host-port-scheduling
   ```
2. Run everything:
   ```bash
   bash docs/verification/pr532-host-port-scheduling/scripts/re-verify.sh
   ```
   (no ref = fetch the current PR head from the manifest's `pr` URL; or pass a
   sha/branch). Exit 0 iff all contract claims pass. L2 needs
   `KUBEBUILDER_ASSETS=$(setup-envtest use 1.34.1 -p path)`.
3. Advance the marker after the round:
   ```bash
   echo <new-head-sha> > docs/verification/pr532-host-port-scheduling/.last-reviewed
   ```
   and commit + push the branch.

### Polarity table

| Test | Kind | On buggy code | On fixed code |
|------|------|---------------|----------------|
| `TestVerifyP0HostNetworkPodDeclaresSchedulerVisibleHostPort` | contract | FAIL (base: no declaration) | PASS |
| `TestVerifyP0NonHostNetworkPodStaysPortReservationFree` | contract | FAIL (if hostPort leaks in) | PASS |
| `TestVerifyInjectedPodSpecsPassRealAPIServerValidation` | contract | FAIL (apiserver rejects) | PASS |
| `TestVerifyControlApIServerHostNetworkPortSemantics` | contract | FAIL (upstream semantics diverge) | PASS |
| `TestVerifyInjectionIsIdempotentAcrossReinjection` | contract | FAIL (duplicates) | PASS |
| `TestVerifyCanaryLegacyPodWithoutDeclarationIsInvisibleToNodePorts` | **canary** | PASS (gap present) | FLIPS to FAIL when legacy pods become visible → invert/promote |
| envtest `TestPortAllocatorHostPortInjection` | contract | FAIL (base: no hostPort) | PASS |

### Kickoff prompt (fresh agent, another machine)

> You are re-verifying PR https://github.com/sgl-project/rbg/pull/532 after a fix.
> Fetch the branch `verify/pr532-host-port-scheduling-claude` from
> https://github.com/cheyang/rbg.git into a clone of sgl-project/rbg, graft its
> harness files onto the current PR head, and run
> `bash docs/verification/pr532-host-port-scheduling/scripts/re-verify.sh`
> (export `KUBEBUILDER_ASSETS=$(setup-envtest use 1.34.1 -p path)` first). Apply
> the polarity table in that directory's README; report Fixed / Still-broken /
> Partial / Harness-update per claim; advance `.last-reviewed` and push the branch.
