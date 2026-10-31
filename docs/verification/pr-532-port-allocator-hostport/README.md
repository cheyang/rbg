# pr-532-port-allocator-hostport — bug verification

Reproducible evidence for the review of https://github.com/sgl-project/rbg/pull/532
("feat(port-allocator): expose host-network ports to the scheduler"), reviewer: codex.
Head under review: `c05052c28c9e6cd5e0aab6362172c4ac651510ae`; merge-base: `d162208e457a13006c9a550944cb08d5b5cf7fbd`.

Layers run against the code under review:

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `InjectPortsIntoPod`, `injectContainerPort`, `RandomAllocator` collision behavior | `go test -count=1 ./pkg/port-allocator/ -run 'TestVerifyPR532' -v` |
| 2. Integration | real kube-apiserver (envtest 1.31): injected pod shapes accepted + round-trip; apiserver hostNetwork hostPort defaulting | `KUBEBUILDER_ASSETS=$(bin/setup-envtest use 1.31.0 --bin-dir bin -p path) go test -count=1 -v ./test/envtest/testcase/verifypr532/` |
| 3. Live | intentionally skipped (no KUBECONFIG layer in this round). The PR's own e2e spread test needs >=2 schedulable nodes and skips on the single-node CI kind cluster. | — |

> Polarity: all harness tests are CONTRACT tests (assert intended behavior): green on the PR
> head, red on the base branch (the red run on base IS the premise reproduction). No canaries.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "The port allocator can randomly assign the same port to different pods. When a pod uses `hostNetwork: true`, those ports occupy the node's network namespace, so two pods with the same allocated port must not be scheduled on the same node." (PR body, Motivation) |
| Linked issue | none ("NONE" in the PR body) |
| Reported component | port allocator (random) + missing scheduler-visible port declaration |
| Patched component | `pkg/port-allocator` (injection + protocol field + validation) — matches |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | `RandomAllocator` guarantees uniqueness only within one `AllocateBatch` call; per-pod PodScoped allocations are separate calls. Deterministic repro (1-port range → two calls both return 30000): on base `d162208e` injected host-network pods declare **zero** container ports, so the scheduler's NodePorts plugin sees nothing → co-location of colliding pods possible. `results/unit-base-p0.txt` (red), `results/unit-head-full.txt` (green). |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | Colliding host-network pods declare nothing the scheduler can filter on | 1 (base) | Confirmed on base; fixed on head | `results/unit-base-p0.txt` (FAIL: 0 ports declared), `results/unit-head-full.txt` (PASS) |
| AC1 | hostNetwork pod: containerPort + hostPort == allocated value | 1+2 | Pass on head | `results/unit-head-full.txt`, `results/integration-head-envtest.txt` (pre-create assertions) |
| AC2 | non-hostNetwork pod: containerPort declared, hostPort unset | 1+2 | Pass on head | same files |
| AC3 | invalid port values (`""`, `0`, `-1`, `65536`, non-numeric) and invalid protocols rejected | 1 | Pass on head (author's tests + parser tests) | `results/unit-head-full.txt` |
| AC4 | reuse of pre-declared ports (any container); conflicting user hostPort declarations fail closed; cross-batch same-pod collision collapses to one declaration | 1 | Pass on head | `results/unit-head-full.txt` |
| AC5 | two host-network pods sharing a RoleScoped port land on different nodes | 3 | Not verified here (needs >=2 nodes; PR e2e skips on single-node CI kind) | see liveNote in verify-manifest.json |
| AC6 | injected pod shapes (hostNetwork TCP/UDP/SCTP hostPort==containerPort; regular containerPort-only) accepted by a real API server | 2 | Pass | `results/integration-head-envtest.txt` |
| F3-evidence | kube-apiserver defaults hostPort=containerPort for hostNetwork pods at admission (`defaultHostNetworkPorts`) | 2 | Confirmed (platform behavior) | `results/integration-head-envtest.txt` (`APIServerDefaultsHostNetworkHostPort`: hostNetwork=true → hostPort=30000; hostNetwork=false → hostPort=0) |

## Per-finding detail

### P0 — premise (runs against base)
`pkg/port-allocator/zz_verify_pr532_test.go: TestVerifyPR532_P0_CollidingHostNetworkPodsDeclareHostPort`
builds a 1-port `RandomAllocator` (every `AllocateBatch` returns 30000 — forced collision between
two pods), injects two host-network pods via the real `InjectPortsIntoPod`, and asserts both
declare `hostPort == containerPort == 30000`. Base (`d162208e`): **fails** — zero ports declared
(premise reproduced). Head (`c05052c2`): **passes**.

### AC1/AC2/AC6 — injection contract + API acceptance
Unit: `TestVerifyPR532_AC2_NonHostNetworkOmitsHostPort` (+P0 test for AC1). Integration:
`test/envtest/testcase/verifypr532/pod_acceptance_test.go` runs the real injector and submits the
result to a real 1.31 API server for TCP/UDP/SCTP + non-hostNetwork shapes. The **pre-create**
assertions are the ones that pin the controller's hostPort logic (see "apiserver defaulting"
below); post-create assertions prove API acceptance and round-trip.

### AC4 — reuse / fail-closed conflict
`TestVerifyPR532_AC4_CrossBatchCollisionSamePodSameProtocol`: RoleScoped and PodScoped
allocations come from separate `AllocateBatch` calls and can collide on the same value; for one
pod this must collapse to a single port declaration (idempotent reuse), with both env vars set.
`TestVerifyPR532_AC4_ConflictingUserHostPortFailsClosed`: a template already binding host port
30000 for another container port makes injection of an allocated 30000 fail with a clear error
(the guarantee is not silently dropped). Author's tests cover the rest of the reuse matrix.

### F3-evidence — kube-apiserver hostNetwork hostPort defaulting (platform behavior)
`test/envtest/testcase/verifypr532/apiserver_defaulting_test.go`: a host-network pod declaring
only `containerPort: 30000` comes back from the API server with `hostPort: 30000`; a
non-host-network pod keeps `hostPort: 0`. Source: `pkg/apis/core/v1/defaults.go`
`defaultHostNetworkPorts`, called from `SetDefaults_Pod` under `if obj.Spec.HostNetwork`
(verified present in k8s v1.29–v1.34; observed live on the 1.31 envtest apiserver).
Consequence: the PR's scheduler-spreading effect for host-network pods follows from the
`containerPort` declaration alone; the explicit `hostPort` assignment is belt-and-braces, and
persisted-pod assertions (e2e) cannot distinguish controller-set from apiserver-defaulted
hostPort. Discovered via a harness-bites anomaly: the L2 round-trip assertions stayed green with
the controller's hostPort assignment neutered, which forced the pre-create assertions.

## Harness-bites check (step 4 evidence)

Temporarily neutered the head code (`if false { containerPort.HostPort = port }` in
`injectContainerPort`), re-ran, then reverted (`git diff` empty afterwards):
- L1 `TestVerifyPR532_P0_...`: FAIL ("hostPort must equal the allocated port") — bites.
- L2 hostnetwork subtests: FAIL on the pre-create assertion (`got HostPort:0, want 30000`) —
  bites. Without the pre-create assertions L2 stayed green because the apiserver defaults
  hostPort (see F3-evidence); the harness was hardened accordingly.
- Full base-branch run: all four harness unit tests red on `d162208e` (`results/unit-base-p0.txt`
  shows the P0 test; the AC2/AC4 tests fail identically because base declares no ports).

## Proposed fixes (NOT applied to production here)

No defects confirmed. Non-blocking observations are tracked in the review document
(findings-codex.md): F1 e2e spread test skips on single-node CI kind; F2 operational boundaries
(scheduler-dependent guarantee; in-place Required-mode pinning vs. hostPort conflict → Pending);
F3 explicit hostPort is redundant with apiserver defaulting (harmless; suggest a code comment
referencing `defaultHostNetworkPorts`); N1 doc wording ("first container" vs reuse into a later
container; fail-closed conflict behavior undocumented).

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/pr-532-port-allocator-hostport-codex` (fork remote), based on
the PR head with production code untouched.

1. Graft onto the code to check:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/pr-532-port-allocator-hostport-codex
   git checkout <fixed-ref>
   git checkout FETCH_HEAD -- docs/verification/pr-532-port-allocator-hostport \
     pkg/port-allocator/zz_verify_pr532_test.go test/envtest/testcase/verifypr532
   ```
   or simply run `bash docs/verification/pr-532-port-allocator-hostport/scripts/re-verify.sh`
   from a checkout of this branch (no ref → fetches the current PR head; prints per-finding
   Fixed / Still-broken and the `last-reviewed..head` delta).
2. Prereqs: L1 = Go toolchain only. L2 = envtest assets
   (`bin/setup-envtest use 1.31.0 --bin-dir bin -p path`; downloads if absent).
3. Polarity: all tests are contract tests — all should be GREEN on correct code; any red L1/L2
   test is a regression. There are no canaries to invert.
4. Harness-bites: run L1 against the merge-base to confirm the P0 test still goes red there.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/pr-532-port-allocator-hostport-codex
(https://github.com/cheyang/rbg.git). Background: review of sgl-project/rbg PR #532
(port-allocator exposing host-network ports to the scheduler) found no blocking defects; a
harness confirmed the premise on base and the injection contract on head. The PR may have been
updated. Read docs/verification/pr-532-port-allocator-hostport/README.md ("Continuing after the
fix") and follow it: run scripts/re-verify.sh (it fetches the current PR head), review the
.last-reviewed..head delta, mind that all tests are contract polarity, and report an
observed-vs-expected table. Layer 3 (real cluster, >=2 nodes) is optional and out of scope
unless a kubeconfig is provided.
```
