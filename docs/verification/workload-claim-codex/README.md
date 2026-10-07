# Verification: PR #487 — "fix: do not claim a child workload the RBG does not control"

Reviewer: Codex (Reviewer B). Harness is **additive only** — no production code
touched (`git diff origin/pr/487 -- pkg/ internal/ api/` outside the two new
test files is empty).

- Base (merge-base): `7ed1860c2841dd911ac8c01e1458a6b9ce5ee49a`
- PR head reviewed:  `cc12857b771c282ca5302ea98f510fba83565f40` (recorded in `.last-reviewed`)

## P0 — premise: CONFIRMED (with one refinement)

Claim: a same-named RBG recreated inside the background-GC window finds the
predecessor's leftover workload by name and inherits its readiness
(`Ready=True` it never earned).

Reproduced on base: `TestVerifyP0LeftoverReadinessInherited` **fails** on
`7ed1860c` — `ConstructRoleStatus` returns the leftover's `readyReplicas=1`,
`CheckWorkloadReady` returns true, `Reconciler` writes without error
(results/unit-base.txt). On the PR head the same test passes
(results/unit-head.txt).

Refinement proven by the integration layer: against a real API server the base
**write** path did not actually succeed — the SSA apply of the new incarnation
over the foreign-controlled leftover is rejected with
`Only one reference can have Controller set to true`
(results/integration-base.txt, subtest
`foreign_controlled_leftover_rejects_second_controller_apply`). So on base the
silent damage is exactly the claimed one — status / readiness / dependency
reads — while the write path already failed with an obscure, retried-forever
422. The PR converts that 422 into a named, retryable claim error and closes
the read hole.

## Findings and observed-vs-expected

| ID | Sev | Claim | Layer | Base | Head | Verdict |
|----|-----|-------|-------|------|------|---------|
| P0 | —   | leftover readiness inherited by new incarnation | unit | test FAILS (bug present) | test PASSES | confirmed |
| F1 | major | orphan (`--cascade=orphan`) workload no longer adoptable; same-name recreate wedges forever | unit+integration | base ADOPTS orphan through the full reconciler path (unit pass; envtest pass) | head refuses with `not claimable`; orphan survives `CleanupOrphanedObjs`; retry never resolves | confirmed behavior change |
| F2 | minor | terminating workload owned by THIS rbg now reports 0/0 and errors reconcile until gone | unit | base reports live replicas, no error | head reports 0, errors | confirmed behavior change |
| F3 | minor | no envtest/e2e coverage (author deliberate) | — | — | this harness shows envtest can cover the apiserver semantics deterministically | gap is fillable |
| F4 | nit  | LWS builds the full apply config before the claim check (other three check first) | code read | — | — | reasoning only |
| F5 | minor | `ErrWorkloadNotClaimable` exported but never matched with `errors.Is`; expected-transient state emits a warning event per retry | code read | — | — | reasoning only |
| F6 | minor | adjacent same-class issue left: `shouldUseLegacyDiscoveryConfig` name-probes per-role ConfigMaps; a leftover pins a recreated RBG to legacy mode permanently | code read | — | — | reasoning only, pre-existing, out of scope |

## How to run

Unit (L1), from the repo root of this branch:

```bash
go test ./pkg/reconciler/ -run 'TestVerifyP0|TestVerifyF1|TestVerifyF2' -v
```

Expected on the PR head: P0, F1-wedge canary, F2 canary PASS;
`TestVerifyF1OrphanWorkloadIsAdopted` FAILS (it is the contract for the
adoption behavior the PR removed — its failure *is* the F1 evidence).

Integration (L2, needs envtest assets; no controller manager, no cluster):

```bash
KUBEBUILDER_ASSETS=$(bin/setup-envtest use 1.31.0 --bin-dir bin -p path) \
  go test ./test/verify/workloadclaim/ -v
```

Expected on the PR head: all four subtests PASS.

Base runs (already captured): `results/unit-base.txt`,
`results/integration-base.txt`; head runs: `results/unit-head.txt`,
`results/integration-head.txt`.

## Harness-bites check

Instead of patching production code, the harness itself was grafted onto the
merge-base (the "unfixed" code) and re-run: the P0 contract test goes red on
base (premise reproduced) and green on head; the F1/F2 canaries flip exactly as
their polarity requires (results/*-base.txt vs results/*-head.txt). That is the
flip-based proof that the tests exercise the claimed behavior.

## Continuing after a fix

```bash
bash docs/verification/workload-claim-codex/scripts/re-verify.sh   # auto-fetches current PR head
```

- F1 addressed by restoring adoption → `TestVerifyF1OrphanWorkloadIsAdopted`
  goes green; invert or delete the `TestVerifyF1OrphanWedgesAndSurvivesCleanup`
  canary.
- F2 revisited → invert `TestVerifyF2TerminatingOwnedWorkloadWaits`.
- `.last-reviewed` holds the reviewed head sha; advance it after each round.

Fresh-machine kickoff prompt: "Check out branch `verify/workload-claim-codex`
of https://github.com/cheyang/rbg.git, read
docs/verification/workload-claim-codex/README.md, run
`bash docs/verification/workload-claim-codex/scripts/re-verify.sh`."
