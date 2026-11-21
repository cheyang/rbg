# Premise (P0) assets

- `restart_budget_premise_verify_test.go` — premise tests for the **base** branch. They compile
  only against base (`45e6e1c2`), where `reconcileRestartBudget` has the pre-PR signature.
  Run: `git checkout 45e6e1c2`, copy this file into `pkg/reconciler/roleinstance/sync/`, then
  `go test -mod=vendor ./pkg/reconciler/roleinstance/sync/ -run TestVerifyP0 -count=1 -v`.
  Both PASS on base by demonstrating the bug (that is the premise evidence).
- `migration-fix.patch` — the smallest production change that makes the F1a/F1b contract tests on
  this branch pass (`git apply` on the PR head). Evidence: `../results/f1-with-migration-fix.txt`.
  Not applied here; production code on this branch is untouched.
- `restart_budget_continuity_verify_test.go` — F1 base control (also base-only signature): on
  `45e6e1c2`, a native-format stored revision reconciles with `groupRestartCount` preserved and
  exhaustion retained (`../results/f1-base-control.txt`). Together with the red head-side F1a/F1b
  this gives the differential attribution: the upgrade reset is introduced by the PR's format
  change, not pre-existing on base.
